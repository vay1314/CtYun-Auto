package ctyun

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestMainClientLoginInfo(t *testing.T) {
	info := ConnectionInfo{DesktopID: 42, Token: "session-token"}
	profile := Profile{UserName: "user@example.com"}
	raw := mainClientLoginInfo(info, profile, "device-code")
	if got := binary.LittleEndian.Uint16(raw[:2]); got != 112 {
		t.Fatalf("message type = %d, want 112", got)
	}
	body := raw[6:]
	if got := binary.LittleEndian.Uint32(body[:4]); got != 42 {
		t.Fatalf("desktop id = %d, want 42", got)
	}
	values := []string{"session-token", DeviceType, "device-code", "user@example.com"}
	for i, want := range values {
		length := int(binary.LittleEndian.Uint32(body[4+i*8:]))
		offset := int(binary.LittleEndian.Uint32(body[8+i*8:]))
		if length != len(want)+1 {
			t.Fatalf("field %d length = %d, want %d", i, length, len(want)+1)
		}
		if got := string(body[offset : offset+length-1]); got != want {
			t.Fatalf("field %d = %q, want %q", i, got, want)
		}
		if body[offset+length-1] != 0 {
			t.Fatalf("field %d is not NUL terminated", i)
		}
	}
}

func TestPreemptionTypes(t *testing.T) {
	for _, value := range []uint16{119, 120, 137} {
		if !preemptionType(value) {
			t.Errorf("type %d should trigger session yielding", value)
		}
	}
	for _, value := range []uint16{1, 3, 4, 7, 103, 104, 112, 118} {
		if preemptionType(value) {
			t.Errorf("type %d should not trigger session yielding", value)
		}
	}
}

func TestPreemptionClose(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{&websocket.CloseError{Code: 1000, Text: "normal"}, true},
		{&websocket.CloseError{Code: 1001, Text: "away"}, true},
		{&websocket.CloseError{Code: 4001, Text: "login elsewhere"}, true},
		{&websocket.CloseError{Code: 4010, Text: "session preempted"}, true},
		{&websocket.CloseError{Code: 4010, Text: "client conflict"}, true},
		{&websocket.CloseError{Code: 1006, Text: "network error"}, false},
		{errors.New("plain network error"), false},
	}
	for _, test := range tests {
		if _, got := preemptionClose(test.err); got != test.want {
			t.Errorf("preemptionClose(%v) = %t, want %t", test.err, got, test.want)
		}
	}
}

func TestWaitClinkCanBeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := waitClink(ctx, 20*time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitClink error = %v, want context.Canceled", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancelled wait did not return immediately")
	}
}
