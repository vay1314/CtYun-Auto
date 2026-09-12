//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/update"
	"golang.org/x/sys/windows"
)

const (
	detachedProcess = 0x00000008
	createNewGroup  = 0x00000200
)

var updatePublicKey = ""
var healthTimeout = 60 * time.Second
var healthInterval = 2 * time.Second

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--recover" {
		pid, err := strconv.Atoi(os.Args[3])
		if err != nil || pid <= 0 {
			log.Fatal("恢复助手参数无效")
		}
		if err := recoverPending(os.Args[2], pid); err != nil {
			log.Fatalf("恢复未完成更新失败: %v", err)
		}
		return
	}
	if len(os.Args) != 5 {
		log.Fatal("更新助手参数无效")
	}
	requestPath, token, installDir, dataDir := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	req, err := update.ReadInstallRequest(requestPath)
	if err != nil {
		log.Fatalf("读取更新请求失败: %v", err)
	}
	if err := req.Validate(dataDir, installDir, token); err != nil {
		log.Fatalf("拒绝不安全的更新请求: %v", err)
	}
	if err := os.Remove(requestPath); err != nil {
		log.Fatalf("无法消费更新请求: %v", err)
	}
	if err := update.TakeOverUpdateLock(dataDir, req.Token); err != nil {
		writeResult(req, "failed", err.Error())
		return
	}
	defer update.ReleaseUpdateLock(dataDir, req.Token)
	if req.RestartMode == "supervisor" {
		if err := update.StopUpdateService(req.ServiceName); err != nil {
			writeResult(req, "failed", err.Error())
			return
		}
	}
	if err := waitForProcess(req.ParentPID, 2*time.Minute); err != nil {
		writeResult(req, "failed", err.Error())
		log.Print(err)
		return
	}
	if err := apply(req, installDir); err != nil {
		writeResult(req, "failed", err.Error())
		log.Printf("应用更新失败: %v", err)
		return
	}
}

func waitForProcess(pid int, timeout time.Duration) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("打开主进程失败: %w", err)
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, uint32(timeout/time.Millisecond))
	if err != nil {
		return err
	}
	if result == uint32(windows.WAIT_TIMEOUT) {
		return errors.New("等待主程序退出超时")
	}
	return nil
}

func apply(req update.InstallRequest, installDir string) error {
	exeName := filepath.Base(req.Executable)
	update.WriteExecutorProgress(req, update.StatusInstalling, "正在安装程序")
	start := func() (*exec.Cmd, error) {
		update.WriteExecutorProgress(req, update.StatusRestarting, "正在启动程序并检查健康状态")
		if req.RestartMode == "supervisor" {
			return nil, update.StartUpdateService(req.ServiceName)
		}
		return launch(filepath.Join(installDir, exeName), installDir)
	}
	stop := func(child *exec.Cmd) error {
		if req.RestartMode == "supervisor" {
			return update.StopUpdateService(req.ServiceName)
		}
		if child != nil && child.Process != nil {
			_ = child.Process.Kill()
			_, _ = child.Process.Wait()
		}
		return nil
	}
	unchanged := func(cause error) error {
		if _, err := start(); err != nil {
			return fmt.Errorf("%v；原程序重启失败: %w", cause, err)
		}
		return cause
	}
	if err := update.FinalizeDatabaseBackup(req); err != nil {
		return unchanged(err)
	}
	recoverCurrent := func(cause error) error {
		update.WriteExecutorProgress(req, update.StatusRollingBack, "正在恢复原程序和数据库")
		err := restore(req, installDir, exeName)
		if err != nil {
			return fmt.Errorf("%v；恢复失败，需要人工修复: %w", cause, err)
		}
		child, err := start()
		if err != nil {
			return fmt.Errorf("%v；恢复后启动失败: %w", cause, err)
		}
		if err := update.WaitForHealth(context.Background(), req.HealthURL, req.FromVersion, healthTimeout, healthInterval, 3); err != nil {
			_ = stop(child)
			return fmt.Errorf("%v；恢复后健康检查失败: %w", cause, err)
		}
		return commitResult(req, "rolled_back", "操作失败，已恢复 v"+req.FromVersion+"："+cause.Error())
	}
	manifest, err := update.VerifySignedRequestManifest(req, trustedUpdatePublicKey(), "windows-"+runtime.GOARCH)
	if err != nil {
		return unchanged(err)
	}
	if err := manifest.CompatibleWith(req.FromVersion, update.Platform{OS: "windows", Arch: runtime.GOARCH}); err != nil {
		return unchanged(err)
	}
	if err := update.VerifyPackageWithManifestDigest(req.StagingDir, req.ToVersion, "windows-"+runtime.GOARCH, req.PackageManifestSHA256); err != nil {
		return unchanged(err)
	}
	if err := backup(installDir, req.BackupDir, exeName); err != nil {
		return unchanged(err)
	}
	if err := update.WriteWindowsTransaction(requestDataDir(req), update.WindowsTransaction{Request: req, InstallDir: installDir}); err != nil {
		return unchanged(fmt.Errorf("写入更新恢复事务失败: %w", err))
	}
	if err := replace(req.StagingDir, installDir, exeName); err != nil {
		return recoverCurrent(err)
	}
	child, err := start()
	if err != nil {
		return recoverCurrent(err)
	}
	if err := update.WaitForHealth(context.Background(), req.HealthURL, req.ToVersion, healthTimeout, healthInterval, 3); err != nil {
		if stopErr := stop(child); stopErr != nil {
			return fmt.Errorf("健康检查失败且无法停止新程序，未恢复数据库: %w", stopErr)
		}
		return recoverCurrent(err)
	}
	if err := commitResult(req, "success", "已更新到 v"+req.ToVersion); err != nil {
		if stopErr := stop(child); stopErr != nil {
			return fmt.Errorf("无法提交更新事务且无法停止新程序: %v；%w", err, stopErr)
		}
		return recoverCurrent(fmt.Errorf("无法提交更新事务: %w", err))
	}
	return nil
}

func recoverPending(dataDir string, parentPID int) error {
	transaction, err := update.ReadWindowsTransaction(dataDir)
	if err != nil {
		return err
	}
	req := transaction.Request
	req.ParentPID = parentPID
	req.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := req.Validate(dataDir, transaction.InstallDir, req.Token); err != nil {
		return fmt.Errorf("未完成更新事务无效: %w", err)
	}
	if err := update.TakeOverUpdateLock(dataDir, req.Token); err != nil {
		return err
	}
	defer update.ReleaseUpdateLock(dataDir, req.Token)
	if req.RestartMode == "supervisor" {
		if err := update.StopUpdateService(req.ServiceName); err != nil {
			return err
		}
	}
	if err := waitForProcess(parentPID, 2*time.Minute); err != nil {
		return err
	}
	exeName := filepath.Base(req.Executable)
	err = restore(req, transaction.InstallDir, exeName)
	if err != nil {
		return err
	}
	var child *exec.Cmd
	if req.RestartMode == "supervisor" {
		err = update.StartUpdateService(req.ServiceName)
	} else {
		child, err = launch(filepath.Join(transaction.InstallDir, exeName), transaction.InstallDir)
	}
	if err != nil {
		return err
	}
	if err := update.WaitForHealth(context.Background(), req.HealthURL, req.FromVersion, healthTimeout, healthInterval, 3); err != nil {
		if req.RestartMode == "supervisor" {
			_ = update.StopUpdateService(req.ServiceName)
		} else if child != nil && child.Process != nil {
			_ = child.Process.Kill()
		}
		return err
	}
	return commitResult(req, "rolled_back", "检测到更新过程被中断，已恢复 v"+req.FromVersion)
}

func requestDataDir(req update.InstallRequest) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(req.ResultPath)))
}

func backup(installDir, backupDir, exeName string) error {
	applicationBackup := filepath.Join(backupDir, "application")
	if err := update.RemoveIfExists(applicationBackup); err != nil {
		return err
	}
	for _, name := range []string{exeName, "ctyun-keeper-updater.exe", "static", "README.txt", "config.env.example"} {
		src := filepath.Join(installDir, name)
		info, err := os.Stat(src)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		dst := filepath.Join(applicationBackup, name)
		if info.IsDir() {
			if err := update.CopyDir(src, dst); err != nil {
				return err
			}
		} else if err := update.CopyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func replace(staging, installDir, exeName string) error {
	if err := atomicReplaceFile(filepath.Join(staging, exeName), filepath.Join(installDir, exeName)); err != nil {
		return err
	}
	if err := atomicReplaceDir(filepath.Join(staging, "static"), filepath.Join(installDir, "static")); err != nil {
		return err
	}
	for _, name := range []string{"ctyun-keeper-updater.exe", "README.txt"} {
		src := filepath.Join(staging, name)
		if _, err := os.Stat(src); err == nil {
			if err := atomicReplaceFile(src, filepath.Join(installDir, name)); err != nil {
				return err
			}
		}
	}
	if src := filepath.Join(staging, "config.env.example"); fileExists(src) {
		if err := atomicReplaceFile(src, filepath.Join(installDir, "config.env.example")); err != nil {
			return err
		}
	}
	return nil
}

func atomicReplaceFile(src, dst string) error {
	tmp := dst + ".update-new"
	old := dst + ".update-old"
	_ = update.RemoveIfExists(tmp)
	_ = update.RemoveIfExists(old)
	if err := update.CopyFile(src, tmp); err != nil {
		return err
	}
	if fileExists(dst) {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	_ = update.RemoveIfExists(old)
	return nil
}

func atomicReplaceDir(src, dst string) error {
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return fmt.Errorf("暂存目录缺少 static")
	}
	tmp := dst + ".update-new"
	old := dst + ".update-old"
	_ = update.RemoveIfExists(tmp)
	_ = update.RemoveIfExists(old)
	if err := update.CopyDir(src, tmp); err != nil {
		return err
	}
	if fileExists(dst) {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	_ = update.RemoveIfExists(old)
	return nil
}

func restore(req update.InstallRequest, installDir, exeName string) error {
	for _, name := range []string{exeName, "ctyun-keeper-updater.exe", "static", "README.txt", "config.env.example"} {
		src := filepath.Join(req.BackupDir, "application", name)
		info, err := os.Stat(src)
		if errors.Is(err, os.ErrNotExist) {
			if removeErr := update.RemoveIfExists(filepath.Join(installDir, name)); removeErr != nil {
				return removeErr
			}
			continue
		}
		if err != nil {
			return err
		}
		dst := filepath.Join(installDir, name)
		if info.IsDir() {
			if err := atomicReplaceDir(src, dst); err != nil {
				return err
			}
		} else if err := atomicReplaceFile(src, dst); err != nil {
			return err
		}
	}
	if req.DatabaseBackup != "" {
		if err := update.RestoreDatabaseFile(req.DatabaseBackup, req.DatabasePath); err != nil {
			return err
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func launch(exe, dir string) (*exec.Cmd, error) {
	cmd := exec.Command(exe)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewGroup}
	return cmd, cmd.Start()
}

func writeResult(req update.InstallRequest, status, message string) {
	if err := update.WriteInstallResult(req.ResultPath, update.ResultFor(req, status, message, "windows-"+runtime.GOARCH)); err != nil {
		log.Printf("写入更新结果失败: %v", err)
	}
}

func commitResult(req update.InstallRequest, status, message string) error {
	result := update.ResultFor(req, status, message, "windows-"+runtime.GOARCH)
	if err := update.CompleteWindowsTransaction(requestDataDir(req), result); err != nil {
		return err
	}
	if err := update.FinalizeCompletedWindowsTransaction(requestDataDir(req)); err != nil {
		log.Printf("更新已完成，结果将在程序运行后重试提交: %v", err)
	}
	return nil
}

func trustedUpdatePublicKey() string {
	if value := strings.TrimSpace(updatePublicKey); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("CTYUN_UPDATE_PUBLIC_KEY"))
}
