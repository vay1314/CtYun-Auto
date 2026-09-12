//go:build windows

package update

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess = 0x00000008
	createNoWindow  = 0x08000000
)

func LaunchUpdater(updaterPath, requestPath, token, installDir, dataDir string) error {
	cmd := exec.Command(updaterPath, requestPath, token, installDir, dataDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNoWindow,
	}
	return cmd.Start()
}
