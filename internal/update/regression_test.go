package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoppedDatabasePreservesWAL(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "db")
	backup := filepath.Join(root, "backup", "db")
	dst := filepath.Join(root, "restored", "db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.WriteFile(src+suffix, []byte("original"+suffix), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := BackupStoppedDatabase(src, backup); err != nil {
		t.Fatal(err)
	}
	if err := RestoreDatabaseFile(backup, dst); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(dst + suffix)
		if err != nil || string(data) != "original"+suffix {
			t.Fatalf("lost %s: %s %v", suffix, data, err)
		}
	}
	_ = os.Remove(src + "-wal")
	_ = os.Remove(src + "-shm")
	if err := BackupStoppedDatabase(src, backup); err != nil {
		t.Fatal(err)
	}
	if err := RestoreDatabaseFile(backup, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + "-wal"); !os.IsNotExist(err) {
		t.Fatal("stale WAL retained")
	}
}

func TestRetentionProtectsPreviousDespiteOldTimestamp(t *testing.T) {
	root := t.TempDir()
	versions := filepath.Join(root, "runtime", "versions")
	for _, name := range []string{"v2.0.0", "v2.1.0", "v2.2.0", "v2.3.0", "v2.4.0"} {
		p := filepath.Join(versions, name)
		_ = os.MkdirAll(p, 0750)
		if name == "v2.0.0" {
			old := time.Now().Add(-365 * 24 * time.Hour)
			_ = os.Chtimes(p, old, old)
		}
	}
	CleanupRetainedVersions(root, "2.4.0", "2.0.0", "2.1.0")
	for _, name := range []string{"v2.0.0", "v2.1.0", "v2.4.0"} {
		if _, err := os.Stat(filepath.Join(versions, name)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(versions)
	if len(entries) != 3 {
		t.Fatalf("retained %d", len(entries))
	}
}

func TestCleanupArtifactsPrunesRecoveryDatabaseGroups(t *testing.T) {
	root := t.TempDir()
	recovery := filepath.Join(root, "updates", "recovery")
	if err := os.MkdirAll(recovery, 0750); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, test := range []struct {
		name string
		age  time.Duration
	}{{"image-1.db", 2 * time.Hour}, {"image-2.db", time.Hour}} {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			path := filepath.Join(recovery, test.name+suffix)
			if err := os.WriteFile(path, []byte(test.name), 0600); err != nil {
				t.Fatal(err)
			}
			stamp := now.Add(-test.age)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
		}
	}
	CleanupArtifacts(root)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(recovery, "image-1.db"+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("older recovery sidecar %q was retained", suffix)
		}
		if _, err := os.Stat(filepath.Join(recovery, "image-2.db"+suffix)); err != nil {
			t.Fatalf("newest recovery sidecar %q was removed: %v", suffix, err)
		}
	}
	old := now.Add(-8 * 24 * time.Hour)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := filepath.Join(recovery, "image-2.db"+suffix)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	CleanupArtifacts(root)
	if _, err := os.Stat(filepath.Join(recovery, "image-2.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired newest recovery database was retained")
	}
}

func TestConsumeInstallResultsQuarantinesInvalidAndContinues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	valid := InstallResult{HistoryID: 2, FromVersion: "2.0.0", Version: "2.1.0", Platform: "linux-amd64", Status: "success", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	var consumed []int64
	err = ConsumeInstallResults(dir, func(result InstallResult) error {
		consumed = append(consumed, result.HistoryID)
		return nil
	})
	if err == nil {
		t.Fatal("invalid result was not reported")
	}
	if len(consumed) != 1 || consumed[0] != 2 {
		t.Fatalf("consumed results = %v", consumed)
	}
	if _, err := os.Stat(filepath.Join(dir, "2.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("valid result was not removed")
	}
	invalid, err := os.ReadDir(filepath.Join(dir, "invalid"))
	if err != nil || len(invalid) != 1 {
		t.Fatalf("quarantined results = %d, %v", len(invalid), err)
	}
}

func TestConsumeInstallResultsRejectsMismatchedHistoryID(t *testing.T) {
	dir := t.TempDir()
	result := InstallResult{HistoryID: 9, FromVersion: "2.0.0", Version: "2.1.0", Platform: "windows-amd64", Status: "rolled_back"}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "8.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	err = ConsumeInstallResults(dir, func(InstallResult) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("mismatched result: called=%v err=%v", called, err)
	}
	invalid, err := os.ReadDir(filepath.Join(dir, "invalid"))
	if err != nil || len(invalid) != 1 {
		t.Fatalf("quarantined results = %d, %v", len(invalid), err)
	}
}

func TestExecutorRejectsChangedSignedRequest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	digest := strings.Repeat("a", 64)
	m := Manifest{SchemaVersion: 1, Version: "2.1.0", Tag: "v2.1.0", Assets: map[string]Asset{"windows-amd64": {Name: "a.zip", Size: 1, SHA256: digest, PackageManifestSHA256: digest}}}
	raw, _ := json.Marshal(m)
	req := InstallRequest{ToVersion: m.Version, PackageManifestSHA256: digest, SignedManifest: raw, ManifestSignature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw))}
	key := base64.StdEncoding.EncodeToString(pub)
	if err := VerifySignedRequest(req, key, "windows-amd64"); err != nil {
		t.Fatal(err)
	}
	req.PackageManifestSHA256 = strings.Repeat("b", 64)
	if err := VerifySignedRequest(req, key, "windows-amd64"); err == nil {
		t.Fatal("accepted changed digest")
	}
}
