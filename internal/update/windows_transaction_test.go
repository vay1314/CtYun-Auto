package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsTransactionRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	want := WindowsTransaction{Request: InstallRequest{Action: "install", FromVersion: "2.0.0", ToVersion: "2.1.0", HistoryID: 7}, InstallDir: filepath.Join(dataDir, "app")}
	if err := WriteWindowsTransaction(dataDir, want); err != nil {
		t.Fatal(err)
	}
	if !HasWindowsTransaction(dataDir) {
		t.Fatal("transaction was not persisted")
	}
	got, err := ReadWindowsTransaction(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.InstallDir != want.InstallDir || got.Request.HistoryID != want.Request.HistoryID {
		t.Fatalf("transaction = %#v", got)
	}
	if err := ClearWindowsTransaction(dataDir); err != nil || HasWindowsTransaction(dataDir) {
		t.Fatalf("transaction was not cleared: %v", err)
	}
}

func TestCompletedWindowsTransactionFinalizesResultBeforeClearing(t *testing.T) {
	dataDir := t.TempDir()
	req := InstallRequest{
		FromVersion: "2.0.0", ToVersion: "2.1.0", HistoryID: 8,
		ResultPath: filepath.Join(dataDir, "updates", "results", "8.json"),
	}
	if err := WriteWindowsTransaction(dataDir, WindowsTransaction{Request: req, InstallDir: filepath.Join(dataDir, "app")}); err != nil {
		t.Fatal(err)
	}
	result := ResultFor(req, "success", "updated", "windows-amd64")
	if err := CompleteWindowsTransaction(dataDir, result); err != nil {
		t.Fatal(err)
	}
	if !HasWindowsTransaction(dataDir) {
		t.Fatal("transaction was cleared before its result was durable")
	}
	if err := FinalizeCompletedWindowsTransaction(dataDir); err != nil {
		t.Fatal(err)
	}
	if HasWindowsTransaction(dataDir) {
		t.Fatal("completed transaction was not cleared")
	}
	if _, err := os.Stat(req.ResultPath); err != nil {
		t.Fatalf("result was not written: %v", err)
	}
}

func TestCompletedWindowsTransactionRejectsResultOutsideDataDir(t *testing.T) {
	dataDir := t.TempDir()
	req := InstallRequest{
		FromVersion: "2.0.0", ToVersion: "2.1.0", HistoryID: 9,
		ResultPath: filepath.Join(t.TempDir(), "9.json"),
	}
	if err := WriteWindowsTransaction(dataDir, WindowsTransaction{Request: req, InstallDir: filepath.Join(dataDir, "app")}); err != nil {
		t.Fatal(err)
	}
	if err := CompleteWindowsTransaction(dataDir, ResultFor(req, "success", "updated", "windows-amd64")); err == nil {
		t.Fatal("completion result outside data directory was accepted")
	}
}
