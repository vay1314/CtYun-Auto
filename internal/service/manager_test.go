package service

import (
	"testing"
	"time"
)

func TestScheduledTaskKey(t *testing.T) {
	if got := scheduledTaskKey("pc"); got != "usage" {
		t.Fatalf("scheduledTaskKey(pc) = %q", got)
	}
	if got := scheduledTaskKey("chat"); got != "chat" {
		t.Fatalf("scheduledTaskKey(chat) = %q", got)
	}
}

func TestStatusUpdatedToday(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 9, 9, 0, 5, 0, 0, location)
	if !statusUpdatedToday("2026-09-08T16:01:00Z", now) {
		t.Fatal("UTC timestamp on the current local day should be current")
	}
	if statusUpdatedToday("2026-09-08T15:59:59Z", now) {
		t.Fatal("timestamp from the previous local day should be stale")
	}
}
