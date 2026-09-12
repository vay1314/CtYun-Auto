//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireLauncherLock(dataDir string) (*os.File, error) {
	dir := filepath.Join(dataDir, "updates")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "launcher.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("数据目录已被另一个 Launcher 实例使用")
		}
		return nil, fmt.Errorf("锁定数据目录: %w", err)
	}
	return f, nil
}

func releaseLauncherLock(f *os.File) {
	if f == nil {
		return
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	_ = f.Close()
}
