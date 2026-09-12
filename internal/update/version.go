package update

import (
	"strconv"
	"strings"
)

// SemVer is a strict X.Y.Z semantic version without build metadata.
type SemVer struct {
	Major int
	Minor int
	Patch int
}

// ParseSemVer accepts an optional leading "v".
func ParseSemVer(raw string) (SemVer, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "v")
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return SemVer{}, false
	}
	numbers := make([]int, 3)
	for i, part := range parts {
		if part == "" {
			return SemVer{}, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return SemVer{}, false
		}
		numbers[i] = n
	}
	return SemVer{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, true
}

func (v SemVer) Compare(other SemVer) int {
	for _, pair := range [][2]int{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}
	return 0
}

func (v SemVer) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// IsDevVersion reports whether raw is an unversioned development build.
func IsDevVersion(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "dev" {
		return true
	}
	if !strings.HasPrefix(raw, "dev-") || len(raw) > 80 {
		return false
	}
	for _, ch := range raw[4:] {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' && ch != '.' {
			return false
		}
	}
	return len(raw) > 4
}
