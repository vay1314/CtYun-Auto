package eai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const ServiceURL = "https://eaichat.ctyun.cn:443/chat/#/aichat"

type TicketProvider interface {
	GetTicket(context.Context, string) (string, error)
}
type Client struct {
	tickets                TicketProvider
	http                   *http.Client
	host, sessionKey, xuid string
	tenant                 int64
}
type envelope struct {
	ResultCode any             `json:"resultCode"`
	ResultMsg  string          `json:"resultMsg"`
	Data       json.RawMessage `json:"data"`
}

func New(t TicketProvider) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{tickets: t, http: &http.Client{Timeout: 150 * time.Second, Jar: jar}, host: "https://eaichat.ctyun.cn:443", xuid: "pubweb_" + random(32)}
}
func random(n int) string {
	const a = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = a[int(b[i])%len(a)]
	}
	return string(b)
}
func unpad(v []byte) ([]byte, error) {
	if len(v) == 0 {
		return nil, errors.New("空密文")
	}
	n := int(v[len(v)-1])
	if n < 1 || n > 16 || n > len(v) {
		return nil, errors.New("PKCS7 填充无效")
	}
	for _, x := range v[len(v)-n:] {
		if int(x) != n {
			return nil, errors.New("PKCS7 填充无效")
		}
	}
	return v[:len(v)-n], nil
}
func decrypt(value string, key []byte) ([]byte, error) {
	value = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, value)
	raw, e := base64.StdEncoding.DecodeString(value)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(key)
	if e != nil || len(raw)%16 != 0 {
		return nil, errors.New("AES 密文无效")
	}
	out := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 16 {
		block.Decrypt(out[i:i+16], raw[i:i+16])
	}
	return unpad(out)
}
func parseKey(v string) (*rsa.PublicKey, error) {
	if b, _ := pem.Decode([]byte(v)); b != nil {
		v = base64.StdEncoding.EncodeToString(b.Bytes)
	}
	v = strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(v)
	raw, e := base64.StdEncoding.DecodeString(v)
	if e != nil {
		return nil, e
	}
	if p, e := x509.ParsePKIXPublicKey(raw); e == nil {
		if k, ok := p.(*rsa.PublicKey); ok {
			return k, nil
		}
	}
	return x509.ParsePKCS1PublicKey(raw)
}
func (c *Client) headers(tenant string, query url.Values, body []byte) http.Header {
	h := http.Header{}
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/137 Safari/537.36"
	h.Set("User-Agent", ua)
	h.Set("x-user-agent", ua)
	h.Set("x-client-trace-id", random(32))
	h.Set("x-eai-xuid", c.xuid)
	h.Set("x-eai-env", "pubWeb")
	h.Set("x-eai-version", "202060305")
	h.Set("x-eai-source", "web-eai")
	h.Set("YL-Main-Version", "202060305")
	h.Set("YL-Product-Id", "5")
	if tenant != "" {
		h.Set("x-eai-tenant-id", tenant)
	}
	if c.sessionKey != "" {
		var parts []string
		keys := make([]string, 0, len(query))
		for k := range query {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(query[k]) > 0 {
				parts = append(parts, k+"="+query[k][0])
			}
		}
		if len(body) > 0 {
			x := md5.Sum(body)
			parts = append(parts, hex.EncodeToString(x[:]))
		}
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		r := random(8)
		parts = append(parts, c.sessionKey, ts, r)
		x := sha256.Sum256([]byte(strings.Join(parts, "&")))
		h.Set("Web-Signature", hex.EncodeToString(x[:]))
		h.Set("Web-Random", r)
		h.Set("Web-Timestamp", ts)
	}
	return h
}
func readEnv(r *http.Response) (envelope, error) {
	defer r.Body.Close()
	var e envelope
	if r.StatusCode != 200 {
		return e, fmt.Errorf("EAI HTTP %d", r.StatusCode)
	}
	if x := json.NewDecoder(r.Body).Decode(&e); x != nil {
		return e, x
	}
	if fmt.Sprint(e.ResultCode) != "0" {
		return e, fmt.Errorf("EAI：%s", e.ResultMsg)
	}
	return e, nil
}
func (c *Client) Authorize(ctx context.Context) error {
	c.sessionKey = ""
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://gwyilian.ctyun.cn/server/eaiSysInfo", nil)
	req.Header = c.headers("", nil, nil)
	r, e := c.http.Do(req)
	if e != nil {
		return e
	}
	env, e := readEnv(r)
	if e != nil {
		return e
	}
	var encrypted string
	if e = json.Unmarshal(env.Data, &encrypted); e != nil {
		return e
	}
	plain, e := decrypt(encrypted, []byte("chinatelecom@cnn"))
	if e != nil {
		return e
	}
	var gw struct {
		EAI struct {
			PrivateHost string `json:"privateHost"`
			IP          string `json:"ip"`
			SecurePort  string `json:"secuPort"`
		} `json:"eai"`
		SSO struct {
			PublicKey   string `json:"ssopk"`
			PublicKeyID string `json:"ssopkid"`
		} `json:"sso"`
	}
	if e = json.Unmarshal(plain, &gw); e != nil {
		return e
	}
	if gw.EAI.PrivateHost != "" {
		c.host = strings.TrimRight(gw.EAI.PrivateHost, "/")
	}
	key, e := parseKey(gw.SSO.PublicKey)
	if e != nil {
		return e
	}
	clientKey := random(16)
	cipher, e := rsa.EncryptPKCS1v15(rand.Reader, key, []byte(clientKey))
	if e != nil {
		return e
	}
	ticket, e := c.tickets.GetTicket(ctx, ServiceURL)
	if e != nil {
		return e
	}
	form := url.Values{"loginType": {"iamTicket"}, "clientId": {"eaiapp"}, "iamTicket": {ticket}, "redirectUri": {ServiceURL}, "clientKey": {hex.EncodeToString(cipher)}, "clientKeyId": {gw.SSO.PublicKeyID}}
	req, _ = http.NewRequestWithContext(ctx, "POST", c.host+"/sso/login/v2/iam/ticketAuthorize", strings.NewReader(form.Encode()))
	req.Header = c.headers("", nil, nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, e = c.http.Do(req)
	if e != nil {
		return e
	}
	env, e = readEnv(r)
	if e != nil {
		return e
	}
	var auth struct {
		SessionKey string `json:"sessionKey"`
	}
	if e = json.Unmarshal(env.Data, &auth); e != nil {
		return e
	}
	sk, e := decrypt(auth.SessionKey, []byte(clientKey))
	if e != nil {
		return e
	}
	c.sessionKey = string(sk)
	return nil
}
func (c *Client) request(ctx context.Context, method, path string, q url.Values, body []byte) (envelope, error) {
	p := strings.Replace(path, "/ai/portal/v", "/ai/portal/wenc/v", 1)
	endpoint := c.host + p
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	req, _ := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	req.Header = c.headers(strconv.FormatInt(c.tenant, 10), q, body)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	r, e := c.http.Do(req)
	if e != nil {
		return envelope{}, e
	}
	return readEnv(r)
}
func (c *Client) Chat(ctx context.Context, text string) error {
	if e := c.Authorize(ctx); e != nil {
		return e
	}
	env, e := c.request(ctx, "GET", "/ai/portal/v2/user/queryUserTenantInfo", nil, nil)
	if e != nil {
		return e
	}
	var tenants []struct {
		TenantID int64 `json:"tenantId"`
	}
	if e = json.Unmarshal(env.Data, &tenants); e != nil || len(tenants) == 0 {
		return errors.New("EAI 没有可用租户")
	}
	c.tenant = tenants[0].TenantID
	env, e = c.request(ctx, "GET", "/ai/portal/v2/openai/chat/queryModels", url.Values{"type": {"all"}}, nil)
	if e != nil {
		return e
	}
	var models []struct {
		Key    string `json:"keyModel"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(env.Data, &models)
	model := ""
	for _, m := range models {
		if m.Key != "" && (m.Status == "avaiable" || model == "") {
			model = m.Key
		}
	}
	if model == "" {
		return errors.New("EAI 没有可用模型")
	}
	if strings.TrimSpace(text) == "" {
		text = "你好"
	}
	body, _ := json.Marshal(map[string]any{"key_model": model, "messages": []map[string]string{{"role": "user", "content": text}}, "stream": true, "client_retry": false, "web_search": false, "tenantId": c.tenant, "enable_thinking": false})
	p := strings.Replace("/ai/portal/v3/openai/chat/completions", "/ai/portal/v", "/ai/portal/wenc/v", 1)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.host+p, bytes.NewReader(body))
	req.Header = c.headers(strconv.FormatInt(c.tenant, 10), nil, body)
	req.Header.Set("Content-Type", "application/json")
	r, e := c.http.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("EAI 对话 HTTP %d", r.StatusCode)
	}
	scanner := bufio.NewScanner(io.LimitReader(r.Body, 8<<20))
	events := 0
	for scanner.Scan() {
		if strings.HasPrefix(strings.TrimSpace(scanner.Text()), "data:") {
			events++
		}
	}
	if e = scanner.Err(); e != nil {
		return e
	}
	if events == 0 {
		return errors.New("EAI 对话没有返回事件")
	}
	return nil
}
