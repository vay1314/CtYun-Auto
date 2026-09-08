package web

import (
	"testing"
	"time"
)

func TestFormatTime(t *testing.T) {
	tests := map[string]string{
		"2026-09-07T21:35:47+08:00": "2026-09-07 21:35:47",
		"2026-09-07T13:35:47Z":      "2026-09-07 13:35:47",
		"2026-09-07 21:35:47":       "2026-09-07 21:35:47",
		"":                          "尚未更新",
	}
	for input, want := range tests {
		if got := formatTime(input); got != want {
			t.Errorf("formatTime(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestReverseLogText(t *testing.T) {
	tests := map[string]string{
		"":                         "",
		"one":                      "one",
		"old\nnew\n":               "new\nold\n",
		"old\r\nmiddle\r\nnew\r\n": "new\nmiddle\nold\n",
	}
	for input, want := range tests {
		if got := reverseLogText(input); got != want {
			t.Errorf("reverseLogText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPlatformStatusUpdatedToday(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 9, 2, 0, 5, 0, 0, location)
	tests := map[string]bool{
		"2026-09-02T00:01:00+08:00": true,
		"2026-09-01T23:59:59+08:00": false,
		"2026-09-01T16:01:00Z":      true,
		"2026-09-02 00:03:00":       true,
		"":                          false,
		"invalid":                   false,
	}
	for updatedAt, want := range tests {
		if got := platformStatusUpdatedToday(updatedAt, now); got != want {
			t.Errorf("platformStatusUpdatedToday(%q) = %v, want %v", updatedAt, got, want)
		}
	}
}
