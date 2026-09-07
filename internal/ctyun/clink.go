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
func splitHost(v string) (string, string) {
	i := strings.LastIndex(v, ":")
	if i > 0 {
		return v[:i], v[i+1:]
	}
	return v, "443"
}
func RunClink(ctx context.Context, info ConnectionInfo, p Profile, notify func(string)) error {
	if notify == nil {
		notify = func(string) {}
	}
	host, port := splitHost(info.ClinkLVSOutHost)
	endpoint := url.URL{Scheme: "wss", Host: info.ClinkLVSOutHost, Path: fmt.Sprintf("/clinkProxy/%d/MAIN", info.DesktopID)}
	dial := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Subprotocols: []string{"binary"}, Proxy: http.ProxyFromEnvironment, TLSClientConfig: clinkTLS(host)}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		notify("连接中")
		ws, _, e := dial.DialContext(ctx, endpoint.String(), http.Header{"Origin": {"https://pc.ctyun.cn"}})
		if e != nil {
			notify("重试中：" + e.Error())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				continue
			}
		}
		handshake := map[string]any{"type": 1, "ssl": 1, "host": host, "port": port, "ca": info.CACert, "cert": info.ClientCert, "key": info.ClientKey, "servername": info.Host + ":" + info.Port, "oqs": 0}
		raw, _ := json.Marshal(handshake)
		_ = ws.WriteMessage(websocket.TextMessage, raw)
		time.Sleep(500 * time.Millisecond)
		_ = ws.WriteMessage(websocket.BinaryMessage, initialPayload)
		notify("在线")
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
			typ, data, err := ws.ReadMessage()
			if err != nil {
				break
			}
			if typ != websocket.BinaryMessage {
				continue
			}
			if len(data) >= 4 && string(data[:4]) == "REDQ" {
				reply, e := redq(data)
				if e == nil {
					_ = ws.WriteMessage(websocket.BinaryMessage, reply)
				}
				continue
			}
			for off := 0; off+6 <= len(data); {
				t := binary.LittleEndian.Uint16(data[off:])
				n := int(binary.LittleEndian.Uint32(data[off+2:]))
				if n < 0 || off+6+n > len(data) {
					break
				}
				if t == 103 {
					_ = ws.WriteMessage(websocket.BinaryMessage, userInfo(p))
				}
				if t == 2 {
					_ = ws.WriteMessage(websocket.BinaryMessage, clinkMessage(3, nil, false))
				}
				off += 6 + n
			}
		}
		_ = ws.Close()
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
