package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vay1314/CtYun-Keeper/internal/update"
)

func TestPackageAndReleaseManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ctyun-keeper"), []byte("app"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "static"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "static", "app.js"), []byte("app"), 0640); err != nil {
		t.Fatal(err)
	}
	packageManifest([]string{"--root", root, "--version", "2.1.0", "--platform", "linux-amd64"})
	if err := update.VerifyPackage(root, "2.1.0", "linux-amd64"); err != nil {
		t.Fatal(err)
	}

	dist := t.TempDir()
	for _, name := range []string{
		"CtYunKeeper-v2.1.0-windows-amd64.zip",
		"CtYunKeeper-v2.1.0-windows-arm64.zip",
		"CtYunKeeper-v2.1.0-linux-amd64.tar.gz",
		"CtYunKeeper-v2.1.0-linux-arm64.tar.gz",
	} {
		f, err := os.Create(filepath.Join(dist, name))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(filepath.Join(root, update.PackageManifestName))
		if strings.HasSuffix(name, ".zip") {
			z := zip.NewWriter(f)
			w, err := z.Create("package/" + update.PackageManifestName)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(data)
			_ = z.Close()
		} else {
			gz := gzip.NewWriter(f)
			tr := tar.NewWriter(gz)
			_ = tr.WriteHeader(&tar.Header{Name: "package/" + update.PackageManifestName, Mode: 0640, Size: int64(len(data))})
			_, _ = tr.Write(data)
			_ = tr.Close()
			_ = gz.Close()
		}
		_ = f.Close()
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("UPDATE_SIGNING_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey))
	releaseManifest([]string{"--dist", dist, "--version", "2.1.0", "--minimum-app", "2.0.0"})
	manifest, err := os.ReadFile(filepath.Join(dist, "update-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	signatureRaw, err := os.ReadFile(filepath.Join(dist, "update-manifest.sig"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := update.DecodeSignature(string(signatureRaw))
	if err != nil {
		t.Fatal(err)
	}
	if !update.VerifySignature(publicKey, manifest, signature) {
		t.Fatal("release manifest signature is invalid")
	}
}
