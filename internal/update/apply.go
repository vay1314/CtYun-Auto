package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func CopyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("拒绝复制非常规文件: %s", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func RestoreDatabaseFile(backup, dest string) error {
	if backup == "" {
		return errors.New("缺少数据库备份")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return err
	}
	suffixes := []string{"", "-wal", "-shm"}
	prepared, moved, installed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	defer func() {
		for _, suffix := range suffixes {
			_ = os.Remove(dest + suffix + ".restore-tmp")
		}
	}()
	for _, suffix := range suffixes {
		if _, err := os.Stat(backup + suffix); err != nil {
			if suffix != "" && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if err := CopyFile(backup+suffix, dest+suffix+".restore-tmp"); err != nil {
			return err
		}
		prepared[suffix] = true
	}
	revert := func(cause error) error {
		for _, suffix := range suffixes {
			if installed[suffix] {
				_ = os.Remove(dest + suffix)
			}
			if moved[suffix] {
				if err := os.Rename(dest+suffix+".restore-old", dest+suffix); err != nil {
					cause = fmt.Errorf("%v；恢复原数据库文件失败: %w", cause, err)
				}
			}
		}
		return cause
	}
	for _, suffix := range suffixes {
		if _, err := os.Stat(dest + suffix); err == nil {
			_ = os.Remove(dest + suffix + ".restore-old")
			if err := os.Rename(dest+suffix, dest+suffix+".restore-old"); err != nil {
				return revert(err)
			}
			moved[suffix] = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return revert(err)
		}
	}
	for _, suffix := range suffixes {
		if prepared[suffix] {
			if err := os.Rename(dest+suffix+".restore-tmp", dest+suffix); err != nil {
				return revert(err)
			}
			installed[suffix] = true
		}
	}
	for _, suffix := range suffixes {
		_ = os.Remove(dest + suffix + ".restore-old")
	}
	return nil
}

// BackupStoppedDatabase must only run after the database owner has exited.
// Preserve WAL as well: a crash or forced container stop need not checkpoint it.
func BackupStoppedDatabase(source, destination string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(source + suffix); err != nil {
			if suffix != "" && errors.Is(err, os.ErrNotExist) {
				_ = os.Remove(destination + suffix)
				continue
			}
			return err
		}
		if err := CopyFile(source+suffix, destination+suffix); err != nil {
			return err
		}
	}
	return nil
}

func FinalizeDatabaseBackup(req InstallRequest) error {
	return BackupStoppedDatabase(req.DatabasePath, req.DatabaseBackup)
}

func CopyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝复制符号链接: %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return CopyFile(path, target)
	})
}

func RemoveIfExists(path string) error {
	if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type healthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	Database  string `json:"database"`
	Scheduler string `json:"scheduler"`
}

// WaitForHealth polls healthURL until it returns expectedVersion for the
// required number of consecutive successes.
func WaitForHealth(ctx context.Context, healthURL, expectedVersion string, timeout, interval time.Duration, successCount int) error {
	if healthURL == "" {
		return errors.New("健康检查地址不能为空")
	}
	if successCount < 1 || timeout <= 0 || interval <= 0 {
		return errors.New("健康检查参数无效")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	consecutive := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待新版本健康检查超时")
		}
		resp, err := client.Get(healthURL)
		if err == nil {
			var payload healthResponse
			if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload) == nil &&
				resp.StatusCode == http.StatusOK && payload.Status == "ok" &&
				payload.Version == expectedVersion && payload.Database == "ok" && payload.Scheduler == "ok" {
				consecutive++
				if consecutive >= successCount {
					resp.Body.Close()
					return nil
				}
			} else {
				consecutive = 0
			}
			resp.Body.Close()
		} else {
			consecutive = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
