package ctyun

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var initialPayload, _ = base64.StdEncoding.DecodeString("UkVEUQIAAAACAAAAGgAAAAAAAAABAAEAAAABAAAAEgAAAAkAAAAECAAA")

func mgf(seed []byte, n int) []byte {
	out := make([]byte, 0, n)
	for i := uint32(0); len(out) < n; i++ {
		v := make([]byte, 4)
		binary.BigEndian.PutUint32(v, i)
		h := sha1.New()
		h.Write(seed)
		h.Write(v)
		out = append(out, h.Sum(nil)...)
	}
	return out[:n]
}
func redq(ch []byte) ([]byte, error) {
	if len(ch) < 182 || string(ch[:4]) != "REDQ" {
		return nil, fmt.Errorf("REDQ 帧无效")
	}
	mod := new(big.Int).SetBytes(ch[48:177])
	exp := int(ch[179])<<16 | int(ch[180])<<8 | int(ch[181])
	seed := make([]byte, 20)
	_, _ = rand.Read(seed)
	db := make([]byte, 107)
	h := sha1.Sum(nil)
	copy(db, h[:])
	db[len(db)-2] = 1
	m := mgf(seed, len(db))
	for i := range db {
		db[i] ^= m[i]
	}
	m = mgf(db, len(seed))
	for i := range seed {
		seed[i] ^= m[i]
	}
	em := make([]byte, 128)
	copy(em[1:21], seed)
	copy(em[21:], db)
	cipher := new(big.Int).Exp(new(big.Int).SetBytes(em), big.NewInt(int64(exp)), mod).Bytes()
	out := make([]byte, 132)
	binary.LittleEndian.PutUint32(out, 1)
	copy(out[132-len(cipher):], cipher)
	return out, nil
}
func clinkMessage(t uint16, data []byte, wrapped bool) []byte {
	extra := 0
	if wrapped {
		extra = 8
	}
	b := make([]byte, 6+extra+len(data))
	binary.LittleEndian.PutUint16(b, t)
	binary.LittleEndian.PutUint32(b[2:], uint32(extra+len(data)))
	if wrapped {
		binary.LittleEndian.PutUint32(b[6:], uint32(len(data)))
		binary.LittleEndian.PutUint32(b[10:], 8)
	}
	copy(b[6+extra:], data)
	return b
}
func userInfo(p Profile) []byte {
	raw, _ := json.Marshal(map[string]any{"type": 1, "userName": p.UserName, "userInfo": "", "userId": p.UserID})
	return clinkMessage(118, raw, true)
}

func mainClientLoginInfo(info ConnectionInfo, p Profile, deviceCode string) []byte {
	values := [][]byte{
		[]byte(info.Token),
		[]byte(DeviceType),
		[]byte(deviceCode),
		[]byte(p.UserName),
	}
	size := 36
	for _, value := range values {
		size += len(value) + 1
	}
	body := make([]byte, size)
	binary.LittleEndian.PutUint32(body, info.DesktopID)
	offset := 36
	for i, value := range values {
		binary.LittleEndian.PutUint32(body[4+i*8:], uint32(len(value)+1))
		binary.LittleEndian.PutUint32(body[8+i*8:], uint32(offset))
		copy(body[offset:], value)
		offset += len(value) + 1
	}
	return clinkMessage(112, body, false)
}
func splitHost(v string) (string, string) {
	i := strings.LastIndex(v, ":")
	if i > 0 {
		return v[:i], v[i+1:]
	}
	return v, "443"
}

type ConnectionRefresher func(context.Context) (ConnectionInfo, error)

func RunClink(ctx context.Context, info ConnectionInfo, p Profile, deviceCode string, refresh ConnectionRefresher, notify func(string)) error {
	return runClink(ctx, info, p, deviceCode, refresh, notify, false)
}

// ActivateClink completes one official desktop login handshake and then returns.
func ActivateClink(ctx context.Context, info ConnectionInfo, p Profile, deviceCode string, notify func(string)) error {
	return runClink(ctx, info, p, deviceCode, nil, notify, true)
}

func waitClink(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func preemptionType(value uint16) bool {
	return value == 119 || value == 120 || value == 137
}

func preemptionClose(err error) (string, bool) {
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		return "", false
	}
	reason := strings.TrimSpace(closeErr.Text)
	lower := strings.ToLower(reason)
	preempted := closeErr.Code == 4001 ||
		strings.Contains(lower, "preempt") ||
		strings.Contains(lower, "conflict") ||
		strings.Contains(lower, "elsewhere") ||
		strings.Contains(reason, "抢占") ||
		strings.Contains(reason, "冲突") ||
		strings.Contains(reason, "异地登录") ||
		strings.Contains(reason, "其他客户端") ||
		strings.Contains(reason, "顶号")
	if preempted {
		if reason == "" {
			reason = "无附加说明"
		}
		return fmt.Sprintf("WebSocket 关闭码 %d（%s）", closeErr.Code, reason), true
	}
	return "", false
}

func runClink(ctx context.Context, info ConnectionInfo, p Profile, deviceCode string, refresh ConnectionRefresher, notify func(string), oneShot bool) error {
	if notify == nil {
		notify = func(string) {}
	}
	firstCycle := true
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !firstCycle && refresh != nil {
			notify("正在刷新连接凭据")
			fresh, e := refresh(ctx)
			if e != nil {
				notify("刷新连接凭据失败，5 秒后重试：" + e.Error())
				if e = waitClink(ctx, 5*time.Second); e != nil {
					return e
				}
				continue
			}
			info = fresh
			notify("连接凭据已刷新")
		}
		firstCycle = false
		if info.DesktopID == 0 || strings.TrimSpace(info.ClinkLVSOutHost) == "" {
			return errors.New("Clink 连接参数不完整")
		}
		host, port := splitHost(info.ClinkLVSOutHost)
		endpoint := url.URL{Scheme: "wss", Host: info.ClinkLVSOutHost, Path: fmt.Sprintf("/clinkProxy/%d/MAIN", info.DesktopID)}
		dial := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Subprotocols: []string{"binary"}, Proxy: http.ProxyFromEnvironment, TLSClientConfig: clinkTLS(host)}
		notify("连接中")
		ws, _, e := dial.DialContext(ctx, endpoint.String(), http.Header{"Origin": {"https://pc.ctyun.cn"}})
		if e != nil {
			notify("重试中：" + e.Error())
			if e = waitClink(ctx, 5*time.Second); e != nil {
				return e
			}
			continue
		}
		var writeMu sync.Mutex
		write := func(messageType int, data []byte) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			return ws.WriteMessage(messageType, data)
		}
		handshake := map[string]any{"type": 1, "ssl": 1, "host": host, "port": port, "ca": info.CACert, "cert": info.ClientCert, "key": info.ClientKey, "servername": info.Host + ":" + info.Port, "oqs": 0}
		raw, _ := json.Marshal(handshake)
		_ = write(websocket.TextMessage, raw)
		time.Sleep(500 * time.Millisecond)
		_ = write(websocket.BinaryMessage, initialPayload)
		notify("在线")
		heartbeatDone := make(chan struct{})
		var heartbeatOnce sync.Once
		var heartbeatStart sync.Once
		stopHeartbeat := func() { heartbeatOnce.Do(func() { close(heartbeatDone) }) }
		deadline := time.Now().Add(60 * time.Second)
		preempted := ""
		var readErr error
	readLoop:
		for time.Now().Before(deadline) {
			_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
			typ, data, err := ws.ReadMessage()
			if err != nil {
				readErr = err
				break
			}
			if typ != websocket.BinaryMessage {
				continue
			}
			if len(data) >= 4 && string(data[:4]) == "REDQ" {
				reply, e := redq(data)
				if e == nil {
					_ = write(websocket.BinaryMessage, reply)
				}
				continue
			}
			for off := 0; off+6 <= len(data); {
				t := binary.LittleEndian.Uint16(data[off:])
				n := int(binary.LittleEndian.Uint32(data[off+2:]))
				if n < 0 || off+6+n > len(data) {
					break
				}
				payload := data[off+6 : off+6+n]
				switch t {
				case 103:
					_ = write(websocket.BinaryMessage, userInfo(p))
					_ = write(websocket.BinaryMessage, mainClientLoginInfo(info, p, deviceCode))
					_ = write(websocket.BinaryMessage, clinkMessage(104, nil, false))
					notify("桌面登录会话已激活")
					if oneShot {
						stopHeartbeat()
						_ = ws.Close()
						return nil
					}
					heartbeatStart.Do(func() {
						go func() {
							ticker := time.NewTicker(5 * time.Second)
							defer ticker.Stop()
							for {
								select {
								case <-ctx.Done():
									return
								case <-heartbeatDone:
									return
								case <-ticker.C:
									if write(websocket.BinaryMessage, clinkMessage(7, nil, false)) != nil {
										return
									}
								}
							}
						}()
					})
				case 4:
					if len(payload) > 12 {
						payload = payload[:12]
					}
					_ = write(websocket.BinaryMessage, clinkMessage(3, payload, false))
				case 3:
					if len(payload) >= 8 {
						ack := make([]byte, 4)
						copy(ack, payload[:4])
						_ = write(websocket.BinaryMessage, clinkMessage(1, ack, false))
					}
				default:
					if preemptionType(t) {
						preempted = fmt.Sprintf("收到服务端会话通知 Type %d", t)
						break readLoop
					}
				}
				off += 6 + n
			}
		}
		stopHeartbeat()
		_ = ws.Close()
		if oneShot {
			if preempted != "" {
				return errors.New(preempted)
			}
			return errors.New("未收到云电脑登录握手")
		}
		if preempted == "" {
			if reason, ok := preemptionClose(readErr); ok {
				preempted = reason
			}
		}
		if preempted != "" {
			notify(preempted + "，主动让位 20 分钟")
			if e = waitClink(ctx, 20*time.Minute); e != nil {
				return e
			}
			continue
		}
		if readErr != nil {
			notify("连接中断，5 秒后重试：" + readErr.Error())
			if e = waitClink(ctx, 5*time.Second); e != nil {
				return e
			}
		}
	}
}

func clinkTLS(host string) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("Clink TLS 未提供证书")
		}
		leaf := state.PeerCertificates[0]
		inter := x509.NewCertPool()
		for _, certificate := range state.PeerCertificates[1:] {
			inter.AddCert(certificate)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter}); err == nil {
			return nil
		}
		if net.ParseIP(host) == nil || !time.Now().After(leaf.NotAfter) {
			return errors.New("Clink TLS 证书与目标不匹配")
		}
		name := ""
		for _, raw := range leaf.DNSNames {
			n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
			suffix := strings.TrimPrefix(n, "*.")
			if suffix == "ctyun.cn" || strings.HasSuffix(suffix, ".ctyun.cn") {
				name = n
				if strings.HasPrefix(n, "*.") {
					name = "clink." + suffix
				}
				break
			}
		}
		if name == "" {
			return errors.New("Clink TLS 证书不属于 ctyun.cn")
		}
		validAt := leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) / 2)
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Intermediates: inter, CurrentTime: validAt}); err != nil {
			return fmt.Errorf("Clink TLS 兼容校验失败：%w", err)
		}
		return nil
	}}
}
