package ctyun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeLoginAndTicketUseOneNativeIdentity(t *testing.T) {
	fixedNow := time.UnixMilli(1700000005000)
	loginCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertNativeBaseHeaders(t, r)
		switch r.URL.Path {
		case "/api/auth/client/genChallengeData":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"challengeId": "challenge-id", "challengeCode": "salt"}})
		case "/api/auth/client/login":
			loginCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.Form.Get("password"); got != nativeSHA(nativeSHA("password")+"salt") {
				t.Fatalf("native login password = %q", got)
			}
			if r.Form.Get("deviceType") != NativeDeviceType || r.Form.Get("clientVersion") != NativeVersion || r.Form.Get("deviceName") != NativeDeviceName {
				t.Fatalf("native login identity = %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"userId": 123, "userEid": "eid", "tenantId": 456, "secretKey": "secret",
				"commonLoginReqHeader": "common", "bondedDevice": true, "timestamp": fixedNow.UnixMilli(),
			}})
		case "/api/auth/client/getTicket":
			if r.URL.Query().Get("service") != "https://service.test" {
				t.Fatalf("service = %q", r.URL.Query().Get("service"))
			}
			requestID, timestamp := r.Header.Get("CTG-REQUESTID"), r.Header.Get("CTG-TIMESTAMP")
			source := NativeDeviceType + requestID + "456" + timestamp + "123" + NativeVersion + "secret"
			if r.Header.Get("CTG-SIGNATURESTR") != strings.ToUpper(nativeSHA(source)) {
				t.Fatal("native ticket signature is invalid")
			}
			if r.Header.Get("CTG-COMMON-DATA") != "common" || r.Header.Get("x-product-id") != "7" || r.Header.Get("x-client-trace-id") == "" {
				t.Fatalf("native ticket headers = %v", r.Header)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"ticket": "native-ticket"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewNativeClientWithOptions("user", "password", "device-code", nil, NativeOptions{
		APIOrigin: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return fixedNow }, Random: strings.NewReader(strings.Repeat("a", 512)),
	})
	profile, err := client.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loginCalls != 1 || profile.UserID != 123 || profile.CommonLoginReqHeader != "common" {
		t.Fatalf("login calls=%d profile=%#v", loginCalls, profile)
	}
	ticket, err := client.GetTicket(context.Background(), "https://service.test")
	if err != nil || ticket != "native-ticket" {
		t.Fatalf("GetTicket() = %q, %v", ticket, err)
	}
}

func TestNativeLoginFetchesCaptchaOnlyWhenRequired(t *testing.T) {
	loginCalls, captchaCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/client/genChallengeData":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"challengeId": fmt.Sprintf("challenge-%d", loginCalls), "challengeCode": "salt"}})
		case "/api/auth/client/captcha":
			captchaCalls++
			w.Header().Set("CTG-CAPTCHA-KEY", "captcha-key")
			_, _ = w.Write([]byte("captcha-image"))
		case "/api/auth/client/login":
			loginCalls++
			_ = r.ParseForm()
			if loginCalls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 51040, "msg": "需要验证码"})
				return
			}
			if r.Form.Get("captchaCode") != "ABCD" || r.Form.Get("captchaCodeKey") != "captcha-key" {
				t.Fatalf("captcha fields = %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"userId": 1, "userEid": "eid", "tenantId": 2, "secretKey": "secret", "commonLoginReqHeader": "common"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	solver := func(_ context.Context, image []byte) (string, error) {
		if string(image) != "captcha-image" {
			t.Fatalf("captcha image = %q", image)
		}
		return "ABCD", nil
	}
	client := NewNativeClientWithOptions("user", "password", "device-code", solver, NativeOptions{APIOrigin: server.URL, HTTPClient: server.Client(), Random: strings.NewReader(strings.Repeat("b", 512))})
	if _, err := client.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loginCalls != 2 || captchaCalls != 1 {
		t.Fatalf("login calls=%d captcha calls=%d", loginCalls, captchaCalls)
	}
}

func assertNativeBaseHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	want := map[string]string{"CTG-DEVICECODE": "device-code", "CTG-DEVICETYPE": NativeDeviceType, "CTG-VERSION": NativeVersion, "CTG-APPMODEL": NativeAppModel, "CTG-APPCHANNEL": NativeAppChannel, "CTG-DEVICE-MODEL": NativeDeviceModel}
	for key, value := range want {
		if got := r.Header.Get(key); got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}
	if _, err := strconv.ParseInt(r.Header.Get("CTG-REQUESTID"), 10, 64); err != nil {
		t.Fatalf("request id: %v", err)
	}
	if _, err := strconv.ParseInt(r.Header.Get("CTG-TIMESTAMP"), 10, 64); err != nil {
		t.Fatalf("timestamp: %v", err)
	}
}
