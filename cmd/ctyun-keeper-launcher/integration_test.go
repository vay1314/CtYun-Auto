//go:build linux

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/update"
)

func TestMain(m *testing.M) {
	if os.Getenv("CTYUN_LAUNCHER_TEST_HELPER") == "1" {
		if filepath.Base(os.Args[0]) == "ctyun-keeper" {
			testApplication()
			return
		}
		launcherVersion = "1.0.0"
		updatePublicKey = os.Getenv("CTYUN_TEST_KEY")
		healthTimeout = 2 * time.Second
		healthInterval = 20 * time.Millisecond
		main()
		return
	}
	os.Exit(m.Run())
}

func testApplication() {
	version := os.Getenv("CTYUN_RUNTIME_VERSION")
	data := os.Getenv("CTYUN_DATA_DIR")
	if version == "2.1.0" && os.Getenv("CTYUN_TEST_FAIL") == "1" {
		_ = os.WriteFile(filepath.Join(data, "ctyun-keeper.db"), []byte("new migration"), 0600)
		os.Exit(1)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		for {
			req, err := update.ReadInstallRequest(filepath.Join(data, "updates", "install-request.json"))
			if err == nil && req.FromVersion == version {
				os.Exit(update.ExitUpdateRequested)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"status":"ok","version":%q,"database":"ok","scheduler":"ok"}`, version)
	})
	if err := http.ListenAndServe("127.0.0.1:"+os.Getenv("APP_PORT"), nil); err != nil {
		os.Exit(1)
	}
}

func TestLauncherInstallsAndAutomaticallyRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			root := t.TempDir()
			data := filepath.Join(root, "data")
			builtin := filepath.Join(root, "builtin")
			staging := filepath.Join(data, "updates", "staging", "2.1.0")
			for _, dir := range []string{builtin, staging} {
				if err := os.MkdirAll(dir, 0750); err != nil {
					t.Fatal(err)
				}
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{builtin, staging} {
				if err := update.CopyFile(self, filepath.Join(dir, "ctyun-keeper")); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(dir, "static"), 0750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "static", "app.js"), []byte("static"), 0640); err != nil {
					t.Fatal(err)
				}
			}
			writeTestVersion(t, builtin, "2.0.0")
			appBytes, err := os.ReadFile(filepath.Join(staging, "ctyun-keeper"))
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(appBytes)
			staticHash := sha256.Sum256([]byte("static"))
			pm, _ := json.Marshal(update.PackageManifest{SchemaVersion: 1, Version: "2.1.0", Platform: "linux-" + runtime.GOARCH, Files: map[string]string{"ctyun-keeper": hex.EncodeToString(hash[:]), "static/app.js": hex.EncodeToString(staticHash[:])}})
			if err := os.WriteFile(filepath.Join(staging, update.PackageManifestName), pm, 0600); err != nil {
				t.Fatal(err)
			}
			pmHash := sha256.Sum256(pm)
			digest := hex.EncodeToString(pmHash[:])
			pub, priv, _ := ed25519.GenerateKey(rand.Reader)
			manifest, _ := json.Marshal(update.Manifest{SchemaVersion: 1, Version: "2.1.0", Tag: "v2.1.0", Assets: map[string]update.Asset{"linux-" + runtime.GOARCH: {Name: "test.tar.gz", Size: 1, SHA256: strings.Repeat("a", 64), PackageManifestSHA256: digest}}})
			token, _ := update.NewRequestToken()
			req := update.InstallRequest{Action: "install", FromVersion: "2.0.0", ToVersion: "2.1.0", StagingDir: staging, Executable: filepath.Join(builtin, "ctyun-keeper"), BackupDir: filepath.Join(data, "updates", "backups", "2.0.0"), DatabasePath: filepath.Join(data, "ctyun-keeper.db"), DatabaseBackup: filepath.Join(data, "updates", "backups", "2.0.0", "database", "ctyun-keeper.db"), ResultPath: filepath.Join(data, "updates", "results", "1.json"), ParentPID: os.Getpid(), RestartMode: "self", HistoryID: 1, Token: token, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), PackageManifestSHA256: digest, SignedManifest: manifest, ManifestSignature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest))}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
			listener.Close()
			req.HealthURL = "http://127.0.0.1:" + port + "/health"
			if err := os.WriteFile(req.DatabasePath, []byte("original database"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := update.WriteInstallRequest(filepath.Join(data, "updates", "install-request.json"), req); err != nil {
				t.Fatal(err)
			}
			if err := update.WriteInstallToken(data, token); err != nil {
				t.Fatal(err)
			}
			// The launcher takes ownership of the lock after the old process exits.
			cmd := exec.Command(self)
			cmd.Env = append(os.Environ(), "CTYUN_LAUNCHER_TEST_HELPER=1", "CTYUN_DATA_DIR="+data, "BUILTIN_DIR="+builtin, "APP_PORT="+port, "CTYUN_TEST_KEY="+base64.StdEncoding.EncodeToString(pub))
			if fail {
				cmd.Env = append(cmd.Env, "CTYUN_TEST_FAIL=1")
			}
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = cmd.Process.Signal(os.Interrupt)
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
			}()
			deadline := time.Now().Add(8 * time.Second)
			// Startup discards stale locks; emulate the live application's lock creation.
			for time.Now().Before(deadline) {
				if response, err := http.Get(req.HealthURL); err == nil {
					response.Body.Close()
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			manager := update.NewManager(data)
			if err := manager.Begin(); err != nil {
				t.Fatal(err)
			}
			if err := manager.HandOff(token); err != nil {
				t.Fatal(err)
			}
			var result update.InstallResult
			for time.Now().Before(deadline) {
				raw, err := os.ReadFile(req.ResultPath)
				if err == nil && json.Unmarshal(raw, &result) == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			want := "success"
			target := "v2.1.0"
			if fail {
				want = "rolled_back"
				target = "v2.0.0"
			}
			if result.Status != want {
				t.Fatalf("result=%+v", result)
			}
			current := resolveCurrent(filepath.Join(data, "runtime", "current"), "")
			if filepath.Base(current) != target {
				t.Fatalf("current=%s", current)
			}
			db, _ := os.ReadFile(req.DatabasePath)
			if string(db) != "original database" {
				t.Fatalf("database not restored: %q", db)
			}
		})
	}
}
