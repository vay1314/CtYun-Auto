//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLaunchDetachedProcess(t *testing.T) {
	if os.Getenv("CTYUN_TEST_CHILD") == "1" {
		return
	}
	t.Setenv("CTYUN_TEST_CHILD", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child, err := launch(exe, filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestReplacePreservesUserConfiguration(t *testing.T) {
	root, staging := t.TempDir(), t.TempDir()
	for _, dir := range []string{root, staging} {
		if err := os.MkdirAll(filepath.Join(dir, "static"), 0750); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"app.exe", "config.env", "start.bat", "static/app.js"} {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(dir), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := replace(staging, root, "app.exe"); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"config.env", "start.bat"} {
		data, _ := os.ReadFile(filepath.Join(root, file))
		if string(data) != root {
			t.Fatalf("overwrote %s", file)
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, "app.exe"))
	if string(data) != staging {
		t.Fatal("application not replaced")
	}
}

func TestTrustedUpdatePublicKeyUsesEnvironmentOnlyAsFallback(t *testing.T) {
	original := updatePublicKey
	t.Cleanup(func() { updatePublicKey = original })
	t.Setenv("CTYUN_UPDATE_PUBLIC_KEY", "environment-key")
	updatePublicKey = ""
	if got := trustedUpdatePublicKey(); got != "environment-key" {
		t.Fatalf("development key fallback = %q", got)
	}
	updatePublicKey = "embedded-key"
	if got := trustedUpdatePublicKey(); got != "embedded-key" {
		t.Fatalf("environment replaced embedded release key: %q", got)
	}
}
