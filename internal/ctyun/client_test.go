package ctyun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestIsLoginExpired(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "expired code", err: APIError{Code: 40010, Message: "当前登录信息已过期，请重新登录"}, want: true},
		{name: "wrapped expired code", err: fmt.Errorf("获取票据：%w", APIError{Code: "40010", Message: "expired"}), want: true},
		{name: "pointer expired code", err: &APIError{Code: 40010, Message: "expired"}, want: true},
		{name: "expired message", err: errors.New("EAI：当前登录信息已过期，请重新登录"), want: true},
		{name: "other platform error", err: APIError{Code: 40011, Message: "参数错误"}, want: false},
		{name: "network error", err: errors.New("network timeout"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLoginExpired(tt.err); got != tt.want {
				t.Fatalf("IsLoginExpired(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

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

func TestPlaceOrderUsesOfficialBatchShape(t *testing.T) {
	tests := []struct {
		name       string
		reward     Reward
		desktop    Desktop
		wantKey    string
		wantValue  string
		wantAttrs  int
		wantPoints int
	}{
		{
			name: "upgrade prefers product instance", reward: Reward{ProductID: 17023101, ProductType: "pointstplupgrade", CostPoints: 500},
			desktop: Desktop{DesktopID: "42", ProdInstID: "instance-42"}, wantKey: "prodInstId", wantValue: "instance-42", wantAttrs: 1, wantPoints: 1,
		},
		{
			name: "upgrade falls back to desktop", reward: Reward{ProductID: 17023101, ProductType: "pointstplupgrade", CostPoints: 500},
			desktop: Desktop{DesktopID: "42"}, wantKey: "bindDesktopId", wantValue: "42", wantAttrs: 1, wantPoints: 1,
		},
		{
			name: "direct reward has no desktop attribute", reward: Reward{ProductID: 18000001, ProductType: "pointscomputer", CostPoints: 900},
			desktop: Desktop{DesktopID: "42"}, wantAttrs: 0, wantPoints: 500,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient("user", "password", "device-code", "")
			client.Profile = &Profile{UserID: 11, TenantID: 22, SecretKey: "secret"}
			client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost || request.URL.Path != "/selforder/api/selforder/paas/placeOrder" {
					t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
				}
				var body struct {
					BusinessChannel string `json:"busiChannel"`
					OrderType       int    `json:"orderType"`
					PointType       int    `json:"pointType"`
					Points          int    `json:"points"`
					SKU             []struct {
						ExecutionOrder int              `json:"execSort"`
						Attributes     []map[string]any `json:"attrs"`
						OrderNum       any              `json:"orderNum"`
					} `json:"sku"`
				}
				if e := json.NewDecoder(request.Body).Decode(&body); e != nil {
					t.Fatal(e)
				}
				if body.BusinessChannel != "010" || body.OrderType != 1 || body.PointType != tt.wantPoints || body.Points != tt.reward.CostPoints*2 {
					t.Fatalf("unexpected order header: %#v", body)
				}
				if len(body.SKU) != 2 || body.SKU[0].ExecutionOrder != 1 || body.SKU[1].ExecutionOrder != 2 {
					t.Fatalf("unexpected batch SKU: %#v", body.SKU)
				}
				for _, sku := range body.SKU {
					if sku.OrderNum != nil || len(sku.Attributes) != tt.wantAttrs {
						t.Fatalf("unexpected SKU: %#v", sku)
					}
					if tt.wantAttrs == 1 {
						if sku.Attributes[0]["attrKey"] != tt.wantKey || fmt.Sprint(sku.Attributes[0]["attrVal"]) != tt.wantValue {
							t.Fatalf("unexpected attributes: %#v", sku.Attributes)
						}
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{}}`)), Header: make(http.Header)}, nil
			})}
			if e := client.PlaceOrder(context.Background(), tt.reward, 2, tt.desktop); e != nil {
				t.Fatalf("PlaceOrder() error = %v", e)
			}
		})
	}
}

func TestRewardsPreserveAvailabilityFields(t *testing.T) {
	client := NewClient("user", "password", "device-code", "")
	client.Profile = &Profile{UserID: 11, TenantID: 22, SecretKey: "secret"}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":[{"series":[{"description":"series","sku":[{"prodId":99,"prodName":"reward","prodType":"gift","costPoints":300,"pointType":500,"prodStatus":2,"effDate":"2026-09-01","expireDate":"2026-09-30"}]}]}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	rewards, e := client.Rewards(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(rewards) != 1 || rewards[0].Status != 2 || rewards[0].PointType != 500 || rewards[0].EffectiveAt != "2026-09-01" || rewards[0].ExpiresAt != "2026-09-30" || rewards[0].Description != "series" {
		t.Fatalf("Rewards() = %#v", rewards)
	}
}

func TestPointsPrefersMainBalanceOverExpiringSubset(t *testing.T) {
	client := NewClient("user", "password", "device-code", "")
	client.Profile = &Profile{UserID: 11, TenantID: 22, SecretKey: "secret"}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":[{"pointType":1,"points":100,"willOutDate":true},{"pointType":1,"points":900,"willOutDate":false}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	points, e := client.Points(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if points != 900 {
		t.Fatalf("Points() = %d, want main balance 900", points)
	}
}
