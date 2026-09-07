package web

import "testing"

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
