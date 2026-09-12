package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type WindowsTransaction struct {
	Request         InstallRequest `json:"request"`
	InstallDir      string         `json:"installDir"`
	CompletedResult *InstallResult `json:"completedResult,omitempty"`
}

func CompleteWindowsTransaction(dataDir string, result InstallResult) error {
	transaction, err := ReadWindowsTransaction(dataDir)
	if err != nil {
		return err
	}
	if !installResultMatches(dataDir, transaction.Request, result) {
		return errors.New("更新完成结果与恢复事务不匹配")
	}
	transaction.CompletedResult = &result
	return WriteWindowsTransaction(dataDir, transaction)
}

// FinalizeCompletedWindowsTransaction turns the durable completion marker into
// the normal result file. Callers serialize it with the update lock.
func FinalizeCompletedWindowsTransaction(dataDir string) error {
	transaction, err := ReadWindowsTransaction(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if transaction.CompletedResult == nil {
		return nil
	}
	result := *transaction.CompletedResult
	if !installResultMatches(dataDir, transaction.Request, result) {
		return errors.New("Windows 更新完成记录无效")
	}
	if err := rejectLinkedPath(dataDir, transaction.Request.ResultPath); err != nil {
		return fmt.Errorf("Windows 更新结果路径不安全: %w", err)
	}
	if err := WriteInstallResult(transaction.Request.ResultPath, result); err != nil {
		return err
	}
	return ClearWindowsTransaction(dataDir)
}

func installResultMatches(dataDir string, req InstallRequest, result InstallResult) bool {
	if req.HistoryID <= 0 || result.HistoryID != req.HistoryID || result.FromVersion != req.FromVersion || result.Version != req.ToVersion {
		return false
	}
	if result.Status != "success" && result.Status != "failed" && result.Status != "rolled_back" {
		return false
	}
	expected := filepath.Join(dataDir, "updates", "results", fmt.Sprintf("%d.json", req.HistoryID))
	left, right := filepath.Clean(req.ResultPath), filepath.Clean(expected)
	return left == right || (runtime.GOOS == "windows" && strings.EqualFold(left, right))
}

func WindowsTransactionPath(dataDir string) string {
	return filepath.Join(dataDir, "updates", "windows-transaction.json")
}

func WriteWindowsTransaction(dataDir string, transaction WindowsTransaction) error {
	path := WindowsTransactionPath(dataDir)
	raw, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(tmp, path)
}

func ReadWindowsTransaction(dataDir string) (WindowsTransaction, error) {
	var transaction WindowsTransaction
	raw, err := os.ReadFile(WindowsTransactionPath(dataDir))
	if err != nil {
		return transaction, err
	}
	err = json.Unmarshal(raw, &transaction)
	return transaction, err
}

func HasWindowsTransaction(dataDir string) bool {
	_, err := os.Stat(WindowsTransactionPath(dataDir))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func ClearWindowsTransaction(dataDir string) error {
	err := os.Remove(WindowsTransactionPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
