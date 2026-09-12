package update

import "testing"

func TestParseSemVer(t *testing.T) {
	tests := []struct {
		in   string
		want SemVer
		ok   bool
	}{
		{"2.1.0", SemVer{2, 1, 0}, true},
		{"v2.1.0", SemVer{2, 1, 0}, true},
		{"2.1", SemVer{}, false},
		{"dev", SemVer{}, false},
		{"2.1.0-alpha", SemVer{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseSemVer(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("ParseSemVer(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSemVerCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2.0.0", "2.1.0", -1},
		{"2.1.0", "2.1.1", -1},
		{"2.1.1", "3.0.0", -1},
		{"2.1.0", "2.1.0", 0},
		{"2.1.1", "2.1.0", 1},
	}
	for _, tt := range tests {
		a, _ := ParseSemVer(tt.a)
		b, _ := ParseSemVer(tt.b)
		if got := a.Compare(b); got != tt.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsDevVersion(t *testing.T) {
	if !IsDevVersion("dev") || !IsDevVersion("") || !IsDevVersion("dev-abc123") || IsDevVersion("2.0.0") || IsDevVersion("developer") || IsDevVersion("dev-../../escape") {
		t.Fatal("IsDevVersion returned an unexpected result")
	}
}
