//go:build windows

package update

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

func serviceManager() (*mgr.Mgr, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	return &mgr.Mgr{Handle: h}, err
}

func openUpdateService(name string, access uint32) (*mgr.Service, error) {
	m, err := serviceManager()
	if err != nil {
		return nil, err
	}
	defer m.Disconnect()
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenService(m.Handle, n, access)
	return &mgr.Service{Name: name, Handle: h}, err
}

// ResolveRestart finds a service owning this process or an ancestor (e.g. NSSM).
// Check control permissions before the application agrees to shut down.
func ResolveRestart(mode string) (string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "" && mode != "auto" && mode != "self" && mode != "supervisor" {
		return "", "", errors.New("UPDATE_RESTART_MODE 无效")
	}
	name := strings.TrimSpace(os.Getenv("UPDATE_SERVICE_NAME"))
	if name == "" && mode != "self" {
		parents := map[uint32]uint32{}
		executables := map[uint32]string{}
		h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
		if err != nil {
			return "", "", err
		}
		defer windows.CloseHandle(h)
		entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
		for err := windows.Process32First(h, &entry); err == nil; err = windows.Process32Next(h, &entry) {
			parents[entry.ProcessID] = entry.ParentProcessID
			executables[entry.ProcessID] = windows.UTF16ToString(entry.ExeFile[:])
		}
		// A native service may report the application PID itself. For an
		// ancestor process, only auto-detect NSSM: matching an arbitrary service
		// ancestor can select Task Scheduler or another unrelated system service.
		candidates := map[uint32]bool{uint32(os.Getpid()): true}
		seen := map[uint32]bool{}
		for pid := parents[uint32(os.Getpid())]; pid != 0 && !seen[pid]; pid = parents[pid] {
			seen[pid] = true
			if strings.EqualFold(filepath.Base(executables[pid]), "nssm.exe") {
				candidates[pid] = true
			}
		}
		m, err := serviceManager()
		if err != nil {
			return "", "", err
		}
		defer m.Disconnect()
		names, err := m.ListServices()
		if err != nil {
			return "", "", err
		}
		for _, candidate := range names {
			s, err := openUpdateService(candidate, windows.SERVICE_QUERY_STATUS)
			if err != nil {
				continue
			}
			status, err := s.Query()
			s.Close()
			if err == nil && status.ProcessId != 0 && candidates[status.ProcessId] {
				name = candidate
				break
			}
		}
	}
	if mode == "self" || (name == "" && mode != "supervisor") {
		return "self", "", nil
	}
	if name == "" {
		return "", "", errors.New("无法识别服务管理器，请设置 UPDATE_SERVICE_NAME")
	}
	s, err := openUpdateService(name, windows.SERVICE_STOP|windows.SERVICE_START|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return "", "", fmt.Errorf("更新需要服务 %s 的停止和启动权限: %w", name, err)
	}
	s.Close()
	// NSSM otherwise kills the temporary updater together with its parent.
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name+`\Parameters`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if application, _, _ := k.GetStringValue("Application"); application != "" {
			kill, _, e := k.GetIntegerValue("AppKillProcessTree")
			if e != nil || kill != 0 {
				return "", "", errors.New("NSSM 在线更新需要先设置 AppKillProcessTree=0，以保留独立更新助手")
			}
		}
	}
	return "supervisor", name, nil
}

func StopUpdateService(name string) error {
	s, err := openUpdateService(name, windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if status.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		status, err = s.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("等待服务停止超时")
}

func StartUpdateService(name string) error {
	s, err := openUpdateService(name, windows.SERVICE_START|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Start()
}
