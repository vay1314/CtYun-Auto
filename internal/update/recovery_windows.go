//go:build windows

package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

func StartPendingWindowsRecovery(dataDir string) (bool, error) {
	if !HasWindowsTransaction(dataDir) {
		return false, nil
	}
	if UpdateLockActive(dataDir) {
		return false, nil
	}
	if err := FinalizeCompletedWindowsTransaction(dataDir); err != nil {
		return false, err
	}
	if !HasWindowsTransaction(dataDir) {
		return false, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return false, err
	}
	updater := filepath.Join(filepath.Dir(executable), "ctyun-keeper-updater.exe")
	cmd := exec.Command(updater, "--recover", dataDir, strconv.Itoa(os.Getpid()))
	cmd.Dir = filepath.Dir(executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	return true, nil
}
