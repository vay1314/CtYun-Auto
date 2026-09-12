//go:build !windows

package update

func ResolveRestart(mode string) (string, string, error) { return "self", "", nil }
