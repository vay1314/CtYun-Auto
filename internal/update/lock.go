package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type fileLock struct {
	path  string
	token string
}

type lockRecord struct {
	PID       int    `json:"pid"`
	Token     string `json:"token"`
	CreatedAt string `json:"createdAt"`
}

func acquireFileLock(dataDir string) (*fileLock, error) {
	if dataDir == "" {
		return &fileLock{}, nil
	}
	dir := filepath.Join(dataDir, "updates")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "update.lock")
	token := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	record, _ := json.Marshal(lockRecord{PID: os.Getpid(), Token: token, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			if _, err = f.Write(record); err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			return &fileLock{path: path, token: token}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		raw, readErr := os.ReadFile(path)
		var owner lockRecord
		if readErr != nil || json.Unmarshal(raw, &owner) != nil || processAlive(owner.PID) {
			return nil, errors.New("已有其他进程正在执行更新")
		}
		_ = os.Remove(path)
	}
	return nil, errors.New("无法取得更新文件锁")
}

func TakeOverUpdateLock(dataDir string, expectedToken ...string) error {
	path := filepath.Join(dataDir, "updates", "update.lock")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var owner lockRecord
	if err := json.Unmarshal(raw, &owner); err != nil {
		return err
	}
	if len(expectedToken) > 0 && expectedToken[0] != "" && owner.Token != expectedToken[0] {
		return errors.New("更新锁不属于当前更新事务")
	}
	owner.PID = os.Getpid()
	raw, _ = json.Marshal(owner)
	return os.WriteFile(path, raw, 0600)
}

func (l *fileLock) handOff(token string) error {
	if l == nil || l.path == "" || token == "" {
		return nil
	}
	raw, err := os.ReadFile(l.path)
	if err != nil {
		return err
	}
	var owner lockRecord
	if json.Unmarshal(raw, &owner) != nil || owner.Token != l.token {
		return errors.New("更新锁所有者已改变")
	}
	owner.Token = token
	owner.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, _ = json.Marshal(owner)
	return os.WriteFile(l.path, raw, 0600)
}

func (l *fileLock) release() {
	if l == nil || l.path == "" {
		return
	}
	raw, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	var record lockRecord
	if json.Unmarshal(raw, &record) == nil && record.Token == l.token {
		_ = os.Remove(l.path)
	}
}

func ReleaseUpdateLock(dataDir string, expectedToken ...string) {
	if dataDir == "" {
		return
	}
	path := filepath.Join(dataDir, "updates", "update.lock")
	if len(expectedToken) > 0 && expectedToken[0] != "" {
		raw, err := os.ReadFile(path)
		var owner lockRecord
		if err != nil || json.Unmarshal(raw, &owner) != nil || owner.Token != expectedToken[0] {
			return
		}
	}
	_ = os.Remove(path)
}

// ClearStaleUpdateLock is called while the Docker Launcher holds its
// kernel-backed, volume-wide lock, so no other live instance can own this
// process handoff lock.
func ClearStaleUpdateLock(dataDir string, _ time.Duration) error {
	path := filepath.Join(dataDir, "updates", "update.lock")
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func UpdateLockActive(dataDir string) bool {
	raw, err := os.ReadFile(filepath.Join(dataDir, "updates", "update.lock"))
	var owner lockRecord
	return err == nil && json.Unmarshal(raw, &owner) == nil && processAlive(owner.PID)
}
