package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestRejectsZeroSizedAsset(t *testing.T) {
	_, err := ParseManifest([]byte(`{"schemaVersion":1,"version":"2.1.0","tag":"v2.1.0","assets":{"linux-amd64":{"name":"x.tar.gz","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":0}}}`))
	if err == nil {
		t.Fatal("zero-sized asset was accepted")
	}
}

func TestDownloaderRequiresDigest(t *testing.T) {
	if _, err := NewDownloader().Download(context.Background(), "https://example.invalid/x", "", "", nil); err == nil {
		t.Fatal("download without digest was accepted")
	}
}

func TestExtractorsRejectLinks(t *testing.T) {
	t.Run("zip symlink", func(t *testing.T) {
		var buf bytes.Buffer
		writer := zip.NewWriter(&buf)
		header := &zip.FileHeader{Name: "link"}
		header.SetMode(os.ModeSymlink | 0777)
		file, _ := writer.CreateHeader(header)
		_, _ = file.Write([]byte("../outside"))
		_ = writer.Close()
		if err := ExtractZip(buf.Bytes(), t.TempDir()); err == nil {
			t.Fatal("zip symlink was accepted")
		}
	})
	t.Run("tar symlink", func(t *testing.T) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		writer := tar.NewWriter(gz)
		_ = writer.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../outside"})
		_ = writer.Close()
		_ = gz.Close()
		if err := ExtractTarGz(buf.Bytes(), t.TempDir()); err == nil {
			t.Fatal("tar symlink was accepted")
		}
	})
}

func TestInstallRequestValidatesTrustedRootsAndToken(t *testing.T) {
	dataDir := t.TempDir()
	installDir := t.TempDir()
	token := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	req := InstallRequest{
		Action: "install", FromVersion: "2.0.0", ToVersion: "2.1.0",
		PackageManifestSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		StagingDir:            filepath.Join(dataDir, "updates", "staging", "2.1.0"),
		Executable:            filepath.Join(installDir, "ctyun-keeper.exe"),
		BackupDir:             filepath.Join(dataDir, "updates", "backups", "2.0.0"),
		DatabasePath:          filepath.Join(dataDir, "ctyun-keeper.db"),
		DatabaseBackup:        filepath.Join(dataDir, "updates", "backups", "2.0.0", "database", "ctyun-keeper.db"),
		HealthURL:             "http://127.0.0.1:9845/health", ParentPID: 123,
		RestartMode: "self", Token: token, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		HistoryID: 1, ResultPath: filepath.Join(dataDir, "updates", "results", "1.json"),
	}
	if err := req.Validate(dataDir, installDir, token); err != nil {
		t.Fatal(err)
	}
	req.StagingDir = filepath.Join(dataDir, "..", "outside")
	if err := req.Validate(dataDir, installDir, token); err == nil {
		t.Fatal("path escape was accepted")
	}
	req.StagingDir = filepath.Join(dataDir, "updates", "backups", "2.0.0")
	if err := req.Validate(dataDir, installDir, token); err == nil {
		t.Fatal("install staging outside updates/staging was accepted")
	}
}

func TestInstallRequestRejectsRemovedRollbackAction(t *testing.T) {
	dataDir := t.TempDir()
	installDir := t.TempDir()
	token := strings.Repeat("f", 64)
	req := InstallRequest{Action: "rollback", Token: token}
	if err := req.Validate(dataDir, installDir, token); err == nil {
		t.Fatal("removed manual rollback action was accepted")
	}
}

func TestInstallRequestRejectsDeceptiveHealthURL(t *testing.T) {
	if validHealthURL("http://127.0.0.1:9845@evil.example/health") {
		t.Fatal("health URL with deceptive user info was accepted")
	}
	for _, raw := range []string{
		"https://127.0.0.1:9845/health",
		"http://localhost:9845/health",
		"http://127.0.0.1:0/health",
		"http://127.0.0.1:9845/other",
		"http://127.0.0.1:9845/health?ok=true",
	} {
		if validHealthURL(raw) {
			t.Fatalf("unsafe health URL was accepted: %s", raw)
		}
	}
	if !validHealthURL("http://127.0.0.1:9845/health") {
		t.Fatal("local health URL was rejected")
	}
}

func TestInstallRequestAcceptsSafeDevelopmentSourceVersion(t *testing.T) {
	dataDir := t.TempDir()
	installDir := t.TempDir()
	token := strings.Repeat("e", 64)
	req := InstallRequest{
		Action: "install", FromVersion: "dev-abc123", ToVersion: "2.1.0",
		PackageManifestSHA256: strings.Repeat("a", 64),
		StagingDir:            filepath.Join(dataDir, "updates", "staging", "2.1.0"),
		Executable:            filepath.Join(installDir, "ctyun-keeper.exe"),
		BackupDir:             filepath.Join(dataDir, "updates", "backups", "dev-abc123"),
		DatabasePath:          filepath.Join(dataDir, "ctyun-keeper.db"),
		DatabaseBackup:        filepath.Join(dataDir, "updates", "backups", "dev-abc123", "database", "ctyun-keeper.db"),
		HealthURL:             "http://127.0.0.1:9845/health", ParentPID: 123,
		RestartMode: "self", Token: token, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		HistoryID: 2, ResultPath: filepath.Join(dataDir, "updates", "results", "2.json"),
	}
	if err := req.Validate(dataDir, installDir, token); err != nil {
		t.Fatalf("safe development source version was rejected: %v", err)
	}
	req.FromVersion = "dev-../../escape"
	if err := req.Validate(dataDir, installDir, token); err == nil {
		t.Fatal("unsafe development source version was accepted")
	}
}

func TestInstallTokenRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	token := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := WriteInstallToken(dataDir, token); err != nil {
		t.Fatal(err)
	}
	got, err := ConsumeInstallToken(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatalf("token = %q", got)
	}
	if _, err := ConsumeInstallToken(dataDir); err == nil {
		t.Fatal("consumed token was readable a second time")
	}
}

func TestCopyFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("data"), 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not permitted in this environment")
	}
	if err := CopyFile(link, filepath.Join(dir, "copied")); err == nil {
		t.Fatal("symlink copy was accepted")
	}
}

func TestCompatibleWithDockerImageUpdate(t *testing.T) {
	manifest := &Manifest{MinimumAppVersion: "2.0.0", RequiresImageUpdate: true}
	err := manifest.CompatibleWith("2.1.0", Platform{InDocker: true, LauncherVersion: "1.0.0"})
	if err == nil {
		t.Fatal("docker image update requirement was ignored")
	}
	manifest.RequiresImageUpdate = false
	manifest.MinimumLauncherVersion = "2.0.0"
	if err := manifest.CompatibleWith("2.1.0", Platform{InDocker: true, LauncherVersion: "1.0.0"}); err == nil {
		t.Fatal("old launcher was accepted")
	}
}

func TestVerifyPackage(t *testing.T) {
	root := t.TempDir()
	content := []byte("binary")
	if err := os.WriteFile(filepath.Join(root, "ctyun-keeper"), content, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "static"), 0750); err != nil {
		t.Fatal(err)
	}
	staticContent := []byte("static")
	if err := os.WriteFile(filepath.Join(root, "static", "app.js"), staticContent, 0640); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	staticSum := sha256.Sum256(staticContent)
	manifest := PackageManifest{
		SchemaVersion: 1, Version: "2.1.0", Platform: "linux-amd64",
		Files: map[string]string{"ctyun-keeper": hex.EncodeToString(sum[:]), "static/app.js": hex.EncodeToString(staticSum[:])},
	}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, PackageManifestName), raw, 0640); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPackage(root, "2.1.0", "linux-amd64"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPackageRejectsMissingStaticAssets(t *testing.T) {
	root := t.TempDir()
	content := []byte("binary")
	if err := os.WriteFile(filepath.Join(root, "ctyun-keeper"), content, 0750); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	manifest := PackageManifest{SchemaVersion: 1, Version: "2.1.0", Platform: "linux-amd64", Files: map[string]string{"ctyun-keeper": hex.EncodeToString(sum[:])}}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, PackageManifestName), raw, 0640); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPackage(root, "2.1.0", "linux-amd64"); err == nil {
		t.Fatal("package without static assets was accepted")
	}
}

func TestWaitForHealthRequiresCompleteHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","version":"2.1.0","database":"error","scheduler":"ok"}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := WaitForHealth(ctx, server.URL, "2.1.0", 70*time.Millisecond, 10*time.Millisecond, 1); err == nil {
		t.Fatal("unhealthy database was accepted")
	}
}

func TestManagerUsesFileLock(t *testing.T) {
	dir := t.TempDir()
	first := NewManager(dir)
	second := NewManager(dir)
	if err := first.Begin(); err != nil {
		t.Fatal(err)
	}
	defer first.Finish()
	if err := second.Begin(); err == nil {
		t.Fatal("second manager acquired the same update lock")
	}
}

func TestManagerHandOffKeepsFileLock(t *testing.T) {
	dir := t.TempDir()
	first := NewManager(dir)
	second := NewManager(dir)
	if err := first.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := first.HandOff(); err != nil {
		t.Fatal(err)
	}
	if err := second.Begin(); err == nil {
		t.Fatal("handoff released the update lock too early")
	}
	ReleaseUpdateLock(dir)
	if err := second.Begin(); err != nil {
		t.Fatal(err)
	}
	second.Finish()
}

func TestUpdateLockHandoffRequiresTransactionToken(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)
	if err := manager.Begin(); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	if err := manager.HandOff(token); err != nil {
		t.Fatal(err)
	}
	defer ReleaseUpdateLock(dir)
	if err := TakeOverUpdateLock(dir, strings.Repeat("b", 64)); err == nil {
		t.Fatal("executor took over a lock for another transaction")
	}
	if err := TakeOverUpdateLock(dir, token); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseUpdateLockRequiresMatchingToken(t *testing.T) {
	dataDir := t.TempDir()
	manager := NewManager(dataDir)
	if err := manager.Begin(); err != nil {
		t.Fatal(err)
	}
	token := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := manager.HandOff(token); err != nil {
		t.Fatal(err)
	}
	ReleaseUpdateLock(dataDir, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
	if !UpdateLockActive(dataDir) {
		t.Fatal("mismatched owner removed the update lock")
	}
	ReleaseUpdateLock(dataDir, token)
	if UpdateLockActive(dataDir) {
		t.Fatal("matching owner did not remove the update lock")
	}
}

func TestRestoreDatabaseFileIsAtomic(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, "backup.db")
	dest := filepath.Join(dir, "ctyun-keeper.db")
	if err := os.WriteFile(backup, []byte("new-database-contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+"-wal", []byte("wal"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreDatabaseFile(backup, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "new-database-contents" {
		t.Fatalf("restored = %q, %v", data, err)
	}
	if _, err := os.Stat(dest + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wal sidecar was not removed")
	}
}
