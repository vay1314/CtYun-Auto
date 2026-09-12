//go:build !windows

package update

import "golang.org/x/sys/unix"

func processAlive(pid int) bool { return pid > 0 && unix.Kill(pid, 0) != unix.ESRCH }
