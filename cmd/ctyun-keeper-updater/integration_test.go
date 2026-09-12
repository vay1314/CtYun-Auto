//go:build windows

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/vay1314/CtYun-Keeper/internal/update"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("CTYUN_TEST_CHILD") == "1" {
		os.Exit(0)
	}
	if os.Getenv("CTYUN_UPDATER_TEST_APP") == "1" && filepath.Base(os.Args[0]) == "ctyun-keeper.exe" {
		data := os.Getenv("CTYUN_DATA_DIR")
		_ = os.WriteFile(filepath.Join(data, "app.pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
		exe, _ := os.Executable()
		version, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "static", "version"))
		if string(version) == "2.1.0" && os.Getenv("CTYUN_UPDATER_TEST_FAIL") == "1" {
			_ = os.WriteFile(filepath.Join(data, "ctyun-keeper.db"), []byte("new migration"), 0600)
			os.Exit(1)
		}
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"status":"ok","version":%q,"database":"ok","scheduler":"ok"}`, string(version))
		})
		if http.ListenAndServe("127.0.0.1:"+os.Getenv("APP_PORT"), nil) != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

func TestWindowsInstallAndAutomaticRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			root := t.TempDir()
			data := filepath.Join(root, "data")
			installed := filepath.Join(root, "installed")
			staging := filepath.Join(data, "updates", "staging", "2.1.0")
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{installed, staging} {
				if err := os.MkdirAll(filepath.Join(dir, "static"), 0750); err != nil {
					t.Fatal(err)
				}
				if err := update.CopyFile(self, filepath.Join(dir, "ctyun-keeper.exe")); err != nil {
					t.Fatal(err)
				}
				_ = os.WriteFile(filepath.Join(dir, "ctyun-keeper-updater.exe"), []byte("updater fixture"), 0600)
				version := "2.0.0"
				if dir == staging {
					version = "2.1.0"
				}
				_ = os.WriteFile(filepath.Join(dir, "static", "version"), []byte(version), 0600)
			}
			_ = os.WriteFile(filepath.Join(installed, "config.env"), []byte("user settings"), 0600)
			_ = os.WriteFile(filepath.Join(installed, "config.env.example"), []byte("old template"), 0600)
			_ = os.WriteFile(filepath.Join(staging, "config.env.example"), []byte("new template"), 0600)
			files := map[string]string{}
			for _, name := range []string{"ctyun-keeper.exe", "ctyun-keeper-updater.exe", "static/version", "config.env.example"} {
				raw, _ := os.ReadFile(filepath.Join(staging, name))
				hash := sha256.Sum256(raw)
				files[name] = hex.EncodeToString(hash[:])
			}
			pm, _ := json.Marshal(update.PackageManifest{SchemaVersion: 1, Version: "2.1.0", Platform: "windows-" + runtime.GOARCH, Files: files})
			_ = os.WriteFile(filepath.Join(staging, update.PackageManifestName), pm, 0600)
			hash := sha256.Sum256(pm)
			digest := hex.EncodeToString(hash[:])
			pub, priv, _ := ed25519.GenerateKey(rand.Reader)
			manifest, _ := json.Marshal(update.Manifest{SchemaVersion: 1, Version: "2.1.0", Tag: "v2.1.0", Assets: map[string]update.Asset{"windows-" + runtime.GOARCH: {Name: "x.zip", Size: 1, SHA256: strings.Repeat("a", 64), PackageManifestSHA256: digest}}})
			updatePublicKey = base64.StdEncoding.EncodeToString(pub)
			healthTimeout = 2 * time.Second
			healthInterval = 20 * time.Millisecond
			t.Cleanup(func() { updatePublicKey = ""; healthTimeout = 60 * time.Second; healthInterval = 2 * time.Second })
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
			listener.Close()
			t.Setenv("CTYUN_UPDATER_TEST_APP", "1")
			t.Setenv("CTYUN_DATA_DIR", data)
			t.Setenv("APP_PORT", port)
			if fail {
				t.Setenv("CTYUN_UPDATER_TEST_FAIL", "1")
			}
			req := update.InstallRequest{Action: "install", HistoryID: 1, FromVersion: "2.0.0", ToVersion: "2.1.0", Executable: filepath.Join(installed, "ctyun-keeper.exe"), StagingDir: staging, BackupDir: filepath.Join(data, "updates", "backups", "2.0.0"), DatabasePath: filepath.Join(data, "ctyun-keeper.db"), DatabaseBackup: filepath.Join(data, "updates", "backups", "2.0.0", "database", "ctyun-keeper.db"), ResultPath: filepath.Join(data, "updates", "results", "1.json"), RestartMode: "self", HealthURL: "http://127.0.0.1:" + port + "/health", SignedManifest: manifest, ManifestSignature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest)), PackageManifestSHA256: digest}
			_ = os.WriteFile(req.DatabasePath, []byte("original database"), 0600)
			stopApp := func() {
				raw, _ := os.ReadFile(filepath.Join(data, "app.pid"))
				pid, _ := strconv.Atoi(string(raw))
				if pid > 0 {
					if p, err := os.FindProcess(pid); err == nil {
						_ = p.Kill()
						_, _ = p.Wait()
					}
				}
			}
			defer stopApp()
			if err := apply(req, installed); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(req.ResultPath)
			if err != nil {
				t.Fatal(err)
			}
			var result update.InstallResult
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			want := "success"
			version := "2.1.0"
			if fail {
				want = "rolled_back"
				version = "2.0.0"
			}
			if result.Status != want {
				t.Fatalf("result=%+v", result)
			}
			got, _ := os.ReadFile(filepath.Join(installed, "static", "version"))
			if string(got) != version {
				t.Fatalf("wrong version: %s", got)
			}
			got, _ = os.ReadFile(req.DatabasePath)
			if string(got) != "original database" {
				t.Fatalf("database not restored: %s", got)
			}
			got, _ = os.ReadFile(filepath.Join(installed, "config.env"))
			if string(got) != "user settings" {
				t.Fatal("user config overwritten")
			}
			got, _ = os.ReadFile(filepath.Join(installed, "config.env.example"))
			wantTemplate := "new template"
			if fail {
				wantTemplate = "old template"
			}
			if string(got) != wantTemplate {
				t.Fatalf("config template = %q, want %q", got, wantTemplate)
			}
		})
	}
}
