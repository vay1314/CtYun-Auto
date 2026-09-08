package ctyun

import (
	"bytes"
	"context"
	"crypto/md5" // 天翼 Web 客户端协议固定使用 MD5。
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	PCOrigin      = "https://desk.ctyun.cn:8810"
	PointsOrigin  = "https://desk.ctyun.cn/selforder/api"
	PCVersion     = "103020001"
	PointsVersion = "204000100"
	DeviceType    = "60"
)

type Profile struct {
	UserID, TenantID                                                int64
	UserEID, SecretKey, CommonLoginReqHeader, UserName, MobilePhone string
	BondedDevice                                                    bool
}
type Desktop struct {
	ObjectType    int    `json:"objType"`
	ObjectID      string `json:"objId"`
	ObjectName    string `json:"objName"`
	DesktopID     string `json:"desktopId"`
	DesktopName   string `json:"desktopName"`
	PoolID        string `json:"poolId"`
	PoolName      string `json:"poolName"`
	UseStatus     string `json:"useStatus"`
	UseStatusText string `json:"useStatusText"`
	Forbidden     bool   `json:"forbiddenConnect"`
}

func (d Desktop) ID() string {
	if d.DesktopID != "" {
		return d.DesktopID
	}
	if d.ObjectID != "" {
		return d.ObjectID
	}
	if d.PoolID != "" {
		return d.PoolID
	}
	return ""
}
func (d Desktop) Name() string {
	if d.DesktopName != "" {
		return d.DesktopName
	}
	if d.ObjectName != "" {
		return d.ObjectName
	}
	if d.PoolName != "" {
		return d.PoolName
	}
	return ""
}
func (d Desktop) Running() bool {
	return d.UseStatus == "25" || d.UseStatusText == "运行中" || d.UseStatusText == "离线运行"
}
func (d Desktop) StatusText() string {
	if d.UseStatusText != "" {
		return d.UseStatusText
	}
	if d.UseStatus != "" {
		return d.UseStatus
	}
	return "未知"
}

type ConnectionInfo struct {
	DesktopID       uint32 `json:"desktopId"`
	Host            string `json:"host"`
	Port            string `json:"port"`
	ClinkLVSOutHost string `json:"clinkLvsOutHost"`
	CACert          string `json:"caCert"`
	ClientCert      string `json:"clientCert"`
	ClientKey       string `json:"clientKey"`
	Token           string `json:"token"`
}

func (c ConnectionInfo) Ready() bool {
	return c.DesktopID != 0 && strings.TrimSpace(c.ClinkLVSOutHost) != ""
}

type Task struct {
	ID      int    `json:"taskDefId"`
	Name    string `json:"taskDefName"`
	Status  int    `json:"status"`
	Current int    `json:"currentProgress"`
	Total   int    `json:"totalProgress"`
}
type Reward struct {
	ProductID                             int64
	ProductName, ProductType, Description string
	CostPoints                            int
}
type APIError struct {
	Code    any
	Message string
}

func (e APIError) Error() string {
	return fmt.Sprintf("平台接口失败（%v）：%s", e.Code, e.Message)
}

func IsLoginExpired(err error) bool {
	if err == nil {
		return false
	}
	var apiErr APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprint(apiErr.Code) == "40010" || strings.Contains(apiErr.Message, "登录信息已过期")
	}
	var apiErrPtr *APIError
	if errors.As(err, &apiErrPtr) {
		return fmt.Sprint(apiErrPtr.Code) == "40010" || strings.Contains(apiErrPtr.Message, "登录信息已过期")
	}
	return strings.Contains(err.Error(), "当前登录信息已过期")
}

type Client struct {
	Username, Password, DeviceCode, OCR string
	HTTP                                *http.Client
	Profile                             *Profile
	last                                atomic.Int64
	mu                                  sync.Mutex
}

func NewClient(username, password, deviceCode, ocr string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{Username: username, Password: password, DeviceCode: deviceCode, OCR: ocr, HTTP: &http.Client{Timeout: 30 * time.Second, Jar: jar}}
}
func sha(v string) string { x := sha256.Sum256([]byte(v)); return hex.EncodeToString(x[:]) }
func (c *Client) next() string {
	n := time.Now().UnixMilli()
	for {
		old := c.last.Load()
		if n <= old {
			n = old + 1
		}
		if c.last.CompareAndSwap(old, n) {
			return strconv.FormatInt(n, 10)
		}
	}
}
func (c *Client) base() http.Header {
	h := http.Header{}
	h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/137 Safari/537.36")
	h.Set("Referer", "https://pc.ctyun.cn/")
	h.Set("ctg-devicetype", DeviceType)
	h.Set("ctg-version", PCVersion)
	h.Set("ctg-devicecode", c.DeviceCode)
	return h
}
func (c *Client) signed(version string, points bool) (http.Header, error) {
	if c.Profile == nil {
		return nil, errors.New("尚未登录")
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	rid := c.next()
	src := DeviceType + rid + strconv.FormatInt(c.Profile.TenantID, 10) + ts + strconv.FormatInt(c.Profile.UserID, 10) + version + c.Profile.SecretKey
	sum := md5.Sum([]byte(src))
	h := c.base()
	h.Set("ctg-appmodel", "2")
	h.Set("ctg-device-model", "xiaomicc")
	h.Set("ctg-requestid", rid)
	h.Set("ctg-signaturestr", strings.ToUpper(hex.EncodeToString(sum[:])))
	h.Set("ctg-softwarecode", "web_client")
	h.Set("ctg-tenantid", strconv.FormatInt(c.Profile.TenantID, 10))
	h.Set("ctg-timestamp", ts)
	h.Set("ctg-userid", strconv.FormatInt(c.Profile.UserID, 10))
	h.Set("ctg-version", version)
	if points {
		h.Set("ctg-authenticate", "1")
		h.Set("ctg-appchannel", "1")
		h.Set("ctg-device-manu", "Xiaomi")
	}
	return h, nil
}

func decodeEnvelope(r *http.Response, out any) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return err
	}
	var env struct {
		Code       any             `json:"code"`
		ResultCode any             `json:"resultCode"`
		Msg        string          `json:"msg"`
		ResultMsg  string          `json:"resultMsg"`
		Data       json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("平台返回非 JSON（HTTP %d）", r.StatusCode)
	}
	code := env.Code
	if code == nil {
		code = env.ResultCode
	}
	ok := code == nil || fmt.Sprint(code) == "0" || fmt.Sprint(code) == "200" || fmt.Sprint(code) == "<nil>"
	if r.StatusCode >= 400 || !ok {
		m := env.Msg
		if m == "" {
			m = env.ResultMsg
		}
		return APIError{code, m}
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err = json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("解析平台数据：%w", err)
		}
	}
	return nil
}
func (c *Client) do(ctx context.Context, method, endpoint string, body io.Reader, h http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header = h
	r, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	return decodeEnvelope(r, out)
}

func (c *Client) SolveCaptcha(ctx context.Context, image []byte) (string, error) {
	endpoint := strings.TrimSpace(c.OCR)
	if endpoint == "" {
		endpoint = "https://orc.1999111.xyz/ocr"
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormField("image")
	_, _ = part.Write([]byte(base64.StdEncoding.EncodeToString(image)))
	_ = w.Close()
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Referer", "https://pc.ctyun.cn/")
	r, e := c.HTTP.Do(req)
	if e != nil {
		return "", e
	}
	defer r.Body.Close()
	var v struct {
		Code    int    `json:"code"`
		Data    string `json:"data"`
		Message string `json:"message"`
	}
	if e = json.NewDecoder(r.Body).Decode(&v); e != nil {
		return "", e
	}
	value := strings.Join(strings.Fields(v.Data), "")
	if r.StatusCode >= 300 || value == "" {
		return "", fmt.Errorf("验证码识别失败：%s", v.Message)
	}
	return value, nil
}

func (c *Client) Login(ctx context.Context) (Profile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		var challenge struct{ ChallengeID, ChallengeCode string }
		h := c.base()
		h.Set("Content-Type", "application/json")
		if e := c.do(ctx, "POST", PCOrigin+"/api/auth/client/genChallengeData", strings.NewReader("{}"), h, &challenge); e != nil {
			last = e
			continue
		}
		params := url.Values{"height": {"36"}, "width": {"85"}, "userInfo": {c.Username}, "mode": {"auto"}, "_t": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}
		req, _ := http.NewRequestWithContext(ctx, "GET", PCOrigin+"/api/auth/client/captcha?"+params.Encode(), nil)
		req.Header = c.base()
		resp, e := c.HTTP.Do(req)
		if e != nil {
			last = e
			continue
		}
		img, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		captcha, e := c.SolveCaptcha(ctx, img)
		if e != nil {
			last = e
			continue
		}
		form := url.Values{"userAccount": {c.Username}, "password": {sha(c.Password + challenge.ChallengeCode)}, "sha256Password": {sha(sha(c.Password) + challenge.ChallengeCode)}, "challengeId": {challenge.ChallengeID}, "captchaCode": {captcha}, "deviceCode": {c.DeviceCode}, "deviceName": {"Chrome浏览器"}, "deviceType": {DeviceType}, "deviceModel": {"Windows NT 10.0; Win64; x64"}, "appVersion": {"3.2.0"}, "sysVersion": {"Windows NT 10.0; Win64; x64"}, "clientVersion": {PCVersion}}
		h = c.base()
		h.Set("Content-Type", "application/x-www-form-urlencoded")
		var d struct {
			UserID, TenantID                                                int64
			UserEid, SecretKey, CommonLoginReqHeader, UserName, Mobilephone string
			BondedDevice                                                    bool
		}
		e = c.do(ctx, "POST", PCOrigin+"/api/auth/client/login", strings.NewReader(form.Encode()), h, &d)
		if e != nil {
			last = e
			time.Sleep(time.Second)
			continue
		}
		if d.UserID == 0 || d.SecretKey == "" {
			last = errors.New("登录响应缺少鉴权字段")
			continue
		}
		p := Profile{d.UserID, d.TenantID, d.UserEid, d.SecretKey, d.CommonLoginReqHeader, d.UserName, d.Mobilephone, d.BondedDevice}
		c.Profile = &p
		return p, nil
	}
	return Profile{}, fmt.Errorf("云电脑登录失败：%w", last)
}
func (c *Client) EnsureLogin(ctx context.Context) error {
	if c.Profile != nil {
		return nil
	}
	_, e := c.Login(ctx)
	return e
}
func (c *Client) ListDesktops(ctx context.Context) ([]Desktop, error) {
	if e := c.EnsureLogin(ctx); e != nil {
		return nil, e
	}
	h, _ := c.signed(PCVersion, false)
	h.Set("Content-Type", "application/json")
	var d struct{ DesktopList, DesktopPoolList, PreemptionDesktopList []Desktop }
	e := c.do(ctx, "POST", PCOrigin+"/api/desktop/client/pageDesktop", strings.NewReader(`{"getCnt":20,"desktopTypes":["1","2001","2002","2003"],"sortType":"createTimeV1"}`), h, &d)
	for i := range d.DesktopPoolList {
		if d.DesktopPoolList[i].ObjectType == 0 {
			d.DesktopPoolList[i].ObjectType = 1
		}
	}
	for i := range d.PreemptionDesktopList {
		if d.PreemptionDesktopList[i].ObjectType == 0 {
			d.PreemptionDesktopList[i].ObjectType = 2
		}
	}
	return append(append(d.DesktopList, d.DesktopPoolList...), d.PreemptionDesktopList...), e
}
func (c *Client) Connect(ctx context.Context, d Desktop) (ConnectionInfo, error) {
	var out struct {
		DesktopInfo ConnectionInfo `json:"desktopInfo"`
	}
	h, _ := c.signed(PCVersion, false)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	objID := d.ObjectID
	if objID == "" {
		objID = d.PoolID
	}
	if objID == "" {
		objID = d.ID()
	}
	form := url.Values{"desktopId": {d.ID()}, "objId": {objID}, "objType": {strconv.Itoa(d.ObjectType)}, "osType": {"15"}, "deviceId": {DeviceType}, "vdCommand": {""}, "ipAddress": {""}, "macAddress": {""}, "deviceCode": {c.DeviceCode}, "deviceName": {"Chrome浏览器"}, "deviceType": {DeviceType}, "deviceModel": {"Windows NT 10.0; Win64; x64"}, "appVersion": {"3.2.0"}, "sysVersion": {"Windows NT 10.0; Win64; x64"}, "clientVersion": {PCVersion}, "specifiedCertCategory": {"1"}}
	e := c.do(ctx, "POST", PCOrigin+"/api/desktop/client/connect", strings.NewReader(form.Encode()), h, &out)
	return out.DesktopInfo, e
}

func (c *Client) DesktopConnectionStatus(ctx context.Context, d Desktop) (ConnectionInfo, error) {
	if d.ID() == "" {
		return ConnectionInfo{}, errors.New("云电脑缺少设备编号")
	}
	h, e := c.signed(PCVersion, false)
	if e != nil {
		return ConnectionInfo{}, e
	}
	query := url.Values{"desktopId": {d.ID()}, "specifiedCertCategory": {"1"}}
	var out struct {
		DesktopInfo ConnectionInfo `json:"desktopInfo"`
	}
	e = c.do(ctx, "GET", PCOrigin+"/api/desktop/client/status?"+query.Encode(), nil, h, &out)
	return out.DesktopInfo, e
}

func (c *Client) PowerOn(ctx context.Context, d Desktop) error {
	if d.ID() == "" {
		return errors.New("云电脑缺少设备编号")
	}
	h, e := c.signed(PCVersion, false)
	if e != nil {
		return e
	}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	form := url.Values{
		"desktopId":     {d.ID()},
		"operationType": {"1"},
	}
	e = c.do(ctx, "POST", PCOrigin+"/api/desktop/client/operate", strings.NewReader(form.Encode()), h, nil)
	if e == nil {
		return nil
	}
	var apiErr APIError
	if errors.As(e, &apiErr) {
		message := apiErr.Message
		if fmt.Sprint(apiErr.Code) == "30010" || strings.Contains(message, "已关机状态") || strings.Contains(message, "正在进行") || strings.Contains(message, "运行中") {
			return nil
		}
	}
	return e
}

// ReportDesktopLogin mirrors the two official web-client events emitted when a
// user opens the AI cloud desktop. The Clink login handshake remains the source
// of truth; these events keep the platform activity service in sync.
func (c *Client) ReportDesktopLogin(ctx context.Context, d Desktop) error {
	if c.Profile == nil {
		return errors.New("尚未登录")
	}
	now := time.Now()
	base := map[string]any{
		"bussiValue": 0, "eventName": "client_action", "userId": c.Profile.UserID,
		"userAccount": c.Profile.UserName, "tenantId": c.Profile.TenantID,
		"deviceCode": c.DeviceCode, "deviceOsType": "web",
		"deviceOsVersion": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"deviceModel":     "PC", "clientVersionCode": "3.7.0", "clientVersionName": "3.7.0",
		"appType": 6, "desktopId": d.ID(), "ctgDeviceType": DeviceType,
		"ctgAppModel": "PC", "vmUuid": d.ID(), "timeInterval": now.Hour(),
		"host": "pc.ctyun.cn",
	}
	events := make([]map[string]any, 0, 2)
	for _, key := range []int{11101, 10109} {
		event := make(map[string]any, len(base)+4)
		for k, v := range base {
			event[k] = v
		}
		ts := time.Now().UnixMilli()
		event["bussiKey"] = key
		event["eventTime"] = ts
		event["opLocalTimeStamp"] = ts
		event["uploadTimeStamp"] = ts
		events = append(events, event)
	}
	raw, _ := json.Marshal(events)
	h, e := c.signed(PCVersion, false)
	if e != nil {
		return e
	}
	h.Set("Content-Type", "application/json")
	return c.do(ctx, "POST", PCOrigin+"/api/cdserv/client/dataservice/api/dataEvent/sendBatch", bytes.NewReader(raw), h, nil)
}
func (c *Client) points(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	h, e := c.signed(PointsVersion, true)
	if e != nil {
		return e
	}
	h.Set("Accept", "application/json, text/plain, */*")
	h.Set("Referer", "https://desk.ctyun.cn/selforder/points.html")
	h.Set("Content-Type", "application/json")
	endpoint := PointsOrigin + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	return c.do(ctx, method, endpoint, r, h, out)
}
func (c *Client) Tasks(ctx context.Context) ([]Task, error) {
	var v []Task
	e := c.points(ctx, "GET", "/marketing/userPoints/getTaskList", nil, nil, &v)
	return v, e
}
func (c *Client) Points(ctx context.Context) (int, error) {
	var values []struct {
		PointType     int    `json:"pointType"`
		PointTypeName string `json:"pointTypeName"`
		Points        int    `json:"points"`
	}
	e := c.points(ctx, "GET", "/marketing/userPoints/getUserPoints", nil, nil, &values)
	if e != nil {
		return 0, e
	}
	for _, v := range values {
		if v.PointType == 1 || v.PointTypeName == "通用积分" {
			return v.Points, nil
		}
	}
	return 0, nil
}
func (c *Client) Rewards(ctx context.Context) ([]Reward, error) {
	var malls []struct {
		Series []struct {
			ExpireDate any `json:"expireDate"`
			SKU        []struct {
				ProductID   int64  `json:"prodId"`
				ProductName string `json:"prodName"`
				ProductType string `json:"prodType"`
				Cost        int    `json:"costPoints"`
				Description string `json:"description"`
				ExpireDate  any    `json:"expireDate"`
			} `json:"sku"`
		} `json:"series"`
	}
	e := c.points(ctx, "GET", "/selforder/prod/get", url.Values{"prodId": {"17000000"}, "prodCode": {"POINTS"}}, nil, &malls)
	if e != nil {
		return nil, e
	}
	var out []Reward
	for _, m := range malls {
		for _, s := range m.Series {
			for _, p := range s.SKU {
				if p.ExpireDate == nil {
					out = append(out, Reward{p.ProductID, p.ProductName, p.ProductType, p.Description, p.Cost})
				}
			}
		}
	}
	return out, nil
}
func (c *Client) PlaceOrder(ctx context.Context, productID int64, productType string, cost, times int, desktopID int64) error {
	attrs := []map[string]any{{"attrKey": "desktopId", "attrVal": desktopID}}
	body := map[string]any{"busiChannel": "1", "orderType": 1, "pointType": 1, "points": cost * times, "sku": []map[string]any{{"execSort": 1, "prodId": productID, "prodType": productType, "attrs": attrs, "orderNum": times}}}
	var out any
	return c.points(ctx, "POST", "/selforder/paas/placeOrder", nil, body, &out)
}
func (c *Client) GetTicket(ctx context.Context, service string) (string, error) {
	if c.Profile == nil {
		return "", errors.New("尚未登录")
	}
	ts, rid := strconv.FormatInt(time.Now().UnixMilli(), 10), c.next()
	source := "25" + rid + strconv.FormatInt(c.Profile.TenantID, 10) + ts + strconv.FormatInt(c.Profile.UserID, 10) + "204010005" + c.Profile.SecretKey
	digest := sha256.Sum256([]byte(source))
	h := c.base()
	h.Set("ctg-devicetype", "25")
	h.Set("ctg-version", "204010005")
	h.Set("ctg-appmodel", "2")
	h.Set("ctg-appchannel", "1020400")
	h.Set("ctg-device-model", "windows")
	h.Set("ctg-requestid", rid)
	h.Set("ctg-timestamp", ts)
	h.Set("ctg-userid", strconv.FormatInt(c.Profile.UserID, 10))
	h.Set("ctg-tenantid", strconv.FormatInt(c.Profile.TenantID, 10))
	h.Set("ctg-signaturestr", strings.ToUpper(hex.EncodeToString(digest[:])))
	h.Set("ctg-common-data", c.Profile.CommonLoginReqHeader)
	var out struct {
		Ticket string `json:"ticket"`
	}
	e := c.do(ctx, "GET", PCOrigin+"/api/auth/client/getTicket?"+url.Values{"service": {service}}.Encode(), nil, h, &out)
	return out.Ticket, e
}
func (c *Client) SendSMS(ctx context.Context) error {
	h, _ := c.signed(PCVersion, false)
	req, _ := http.NewRequestWithContext(ctx, "GET", PCOrigin+"/api/auth/client/validateCode/captcha?width=120&height=40&_t="+strconv.FormatInt(time.Now().UnixMilli(), 10), nil)
	req.Header = h
	r, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	img, _ := io.ReadAll(r.Body)
	r.Body.Close()
	code, e := c.SolveCaptcha(ctx, img)
	if e != nil {
		return e
	}
	phone := c.Profile.MobilePhone
	if phone == "" {
		phone = c.Username
	}
	return c.do(ctx, "GET", PCOrigin+"/api/cdserv/client/device/getSmsCode?"+url.Values{"mobilePhone": {phone}, "captchaCode": {code}}.Encode(), nil, h, nil)
}
func (c *Client) BindDevice(ctx context.Context, code string) error {
	h, _ := c.signed(PCVersion, false)
	q := url.Values{"verificationCode": {code}, "deviceName": {"Chrome浏览器"}, "deviceCode": {c.DeviceCode}, "deviceModel": {"Windows NT 10.0; Win64; x64"}, "sysVersion": {"Windows NT 10.0; Win64; x64"}, "appVersion": {"3.2.0"}, "hostName": {"pc.ctyun.cn"}, "deviceInfo": {"Win32"}}
	return c.do(ctx, "POST", PCOrigin+"/api/cdserv/client/device/binding?"+q.Encode(), nil, h, nil)
}
