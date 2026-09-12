//go:build !windows

package update

func StartPendingWindowsRecovery(string) (bool, error) { return false, nil }
