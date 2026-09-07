package ctyun

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPowerOnUsesOfficialOperationFields(t *testing.T) {
	client := NewClient("user", "password", "device-code", "")
	client.Profile = &Profile{UserID: 11, TenantID: 22, SecretKey: "secret"}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/desktop/client/operate" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if e := request.ParseForm(); e != nil {
			t.Fatal(e)
		}
		want := map[string]string{
			"desktopId":     "desktop-1",
			"operationType": "1",
		}
		for key, value := range want {
			if got := request.Form.Get(key); got != value {
				t.Errorf("%s = %q, want %q", key, got, value)
			}
		}
		if got := len(request.Form); got != len(want) {
			t.Errorf("power-on field count = %d, want %d (%v)", got, len(want), request.Form)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":null}`)), Header: make(http.Header)}, nil
	})}

	desktop := Desktop{ObjectType: 1, ObjectID: "pool-1", DesktopID: "desktop-1"}
	if e := client.PowerOn(context.Background(), desktop); e != nil {
		t.Fatalf("PowerOn() error = %v", e)
	}
}

func TestDesktopRunningIncludesOfflineRunning(t *testing.T) {
	for _, desktop := range []Desktop{{UseStatus: "25"}, {UseStatusText: "运行中"}, {UseStatusText: "离线运行"}} {
		if !desktop.Running() {
			t.Fatalf("desktop status %#v should be treated as running", desktop)
		}
	}
	if (Desktop{UseStatusText: "已关机"}).Running() {
		t.Fatal("powered-off desktop should not be treated as running")
	}
}

func TestDesktopConnectionStatusUsesStatusEndpoint(t *testing.T) {
	client := NewClient("user", "password", "device-code", "")
	client.Profile = &Profile{UserID: 11, TenantID: 22, SecretKey: "secret"}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/desktop/client/status" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if got := request.URL.Query().Get("desktopId"); got != "desktop-1" {
			t.Errorf("desktopId = %q, want desktop-1", got)
		}
		if got := request.URL.Query().Get("specifiedCertCategory"); got != "1" {
			t.Errorf("specifiedCertCategory = %q, want 1", got)
		}
		body := `{"code":0,"data":{"desktopInfo":{"desktopId":123,"clinkLvsOutHost":"example.test:443"}}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	info, err := client.DesktopConnectionStatus(context.Background(), Desktop{DesktopID: "desktop-1"})
	if err != nil {
		t.Fatalf("DesktopConnectionStatus() error = %v", err)
	}
	if !info.Ready() {
		t.Fatalf("DesktopConnectionStatus() returned incomplete info: %#v", info)
	}
}

func TestConnectionInfoReadyRequiresClinkProxyAddress(t *testing.T) {
	if (ConnectionInfo{DesktopID: 123, Host: "legacy.example.test"}).Ready() {
		t.Fatal("legacy host without clinkLvsOutHost must not be treated as Clink-ready")
	}
	if !(ConnectionInfo{DesktopID: 123, ClinkLVSOutHost: "proxy.example.test:443"}).Ready() {
		t.Fatal("complete Clink connection info should be ready")
	}
}
