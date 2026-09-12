package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckerFindsUpdateFromManifest(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	var mux = http.NewServeMux()
	var server = httptest.NewServer(mux)
	defer server.Close()

	manifest := `{"schemaVersion":1,"version":"2.2.0","tag":"v2.2.0","publishedAt":"2026-09-12T01:02:03Z","releaseUrl":"https://github.com/vay1314/CtYun-Keeper/releases/tag/v2.2.0","assets":{"linux-amd64":{"name":"x.tar.gz","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","packageManifestSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1}}}`
	mux.HandleFunc("/repos/vay1314/CtYun-Keeper/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		release := releaseInfo{
			TagName: "v2.2.0", PublishedAt: "untrusted", HTMLURL: "javascript:alert(1)",
			Assets: []releaseAsset{
				{Name: manifestAsset, BrowserDownloadURL: server.URL + "/manifest"},
				{Name: signatureAsset, BrowserDownloadURL: server.URL + "/sig"},
			},
		}
		_ = json.NewEncoder(w).Encode(release)
	})
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(manifest))
	})
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(manifest)))))
	})

	for _, current := range []string{"2.1.0", "dev-abc123"} {
		t.Run(current, func(t *testing.T) {
			checker := NewChecker("vay1314/CtYun-Keeper", current, "", Platform{OS: "linux", Arch: "amd64", InDocker: true, LauncherVersion: "1.0.0"})
			checker.apiBase = server.URL
			checker.SetPublicKey(publicKey)
			result, err := checker.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !result.HasUpdate || result.LatestVersion != "2.2.0" || result.Manifest == nil {
				t.Fatalf("unexpected result: %+v", result)
			}
			if result.PublishedAt != "2026-09-12T01:02:03Z" || result.ReleaseURL != "https://github.com/vay1314/CtYun-Keeper/releases/tag/v2.2.0" {
				t.Fatalf("checker did not use signed release metadata: %+v", result)
			}
			if isDev := IsDevVersion(current); result.CurrentIsDev != isDev || (isDev && result.Message != "开发版可更新到正式版 v2.2.0") {
				t.Fatalf("development update state is wrong: %+v", result)
			}
		})
	}
}

func TestCheckerReportsUnsupportedArch(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := `{"schemaVersion":1,"version":"2.2.0","tag":"v2.2.0","assets":{"windows-amd64":{"name":"x.zip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","packageManifestSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1}}}`
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest" {
			_, _ = w.Write([]byte(manifest))
			return
		}
		if r.URL.Path == "/sig" {
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(manifest)))))
			return
		}
		release := releaseInfo{TagName: "v2.2.0", Assets: []releaseAsset{
			{Name: manifestAsset, BrowserDownloadURL: server.URL + "/manifest"},
			{Name: signatureAsset, BrowserDownloadURL: server.URL + "/sig"},
		}}
		_ = json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()

	checker := NewChecker("vay1314/CtYun-Keeper", "2.1.0", "", Platform{OS: "linux", Arch: "amd64", InDocker: true})
	checker.apiBase = server.URL
	checker.SetPublicKey(publicKey)

	result, err := checker.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Supported {
		t.Fatalf("expected unsupported arch, got %+v", result)
	}
}

func TestCheckerDoesNotRequireAssetWhenAlreadyCurrent(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := `{"schemaVersion":1,"version":"2.2.0","tag":"v2.2.0","assets":{"windows-amd64":{"name":"x.zip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","packageManifestSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1}}}`
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			_, _ = w.Write([]byte(manifest))
		case "/sig":
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(manifest)))))
		default:
			_ = json.NewEncoder(w).Encode(releaseInfo{TagName: "v2.2.0", Assets: []releaseAsset{
				{Name: manifestAsset, BrowserDownloadURL: server.URL + "/manifest"},
				{Name: signatureAsset, BrowserDownloadURL: server.URL + "/sig"},
			}})
		}
	}))
	defer server.Close()

	checker := NewChecker("", "2.2.0", "", Platform{OS: "linux", Arch: "amd64", InDocker: true})
	checker.apiBase = server.URL
	checker.SetPublicKey(publicKey)
	result, err := checker.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Supported || result.HasUpdate || result.Message != "已是最新版本" {
		t.Fatalf("current version was rejected for a missing update asset: %+v", result)
	}
}

func TestCheckerRejectsBadSignature(t *testing.T) {
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	_, wrongKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := `{"schemaVersion":1,"version":"2.2.0","tag":"v2.2.0","assets":{"linux-amd64":{"name":"x.tar.gz","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1}}}`
	signature := ed25519.Sign(wrongKey, []byte(manifest))

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			_, _ = w.Write([]byte(manifest))
		case "/sig":
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(signature)))
		default:
			release := releaseInfo{TagName: "v2.2.0", Assets: []releaseAsset{
				{Name: manifestAsset, BrowserDownloadURL: server.URL + "/manifest"},
				{Name: signatureAsset, BrowserDownloadURL: server.URL + "/sig"},
			}}
			_ = json.NewEncoder(w).Encode(release)
		}
	}))
	defer server.Close()

	checker := NewChecker("vay1314/CtYun-Keeper", "2.1.0", "", Platform{OS: "linux", Arch: "amd64", InDocker: true})
	checker.apiBase = server.URL
	checker.SetPublicKey(publicKey)
	if _, err := checker.Check(context.Background()); err == nil {
		t.Fatal("update with invalid signature was accepted")
	}
}

func TestCheckerRejectsManifestWithoutConfiguredPublicKey(t *testing.T) {
	manifest := `{"schemaVersion":1,"version":"2.2.0","tag":"v2.2.0","assets":{"linux-amd64":{"name":"x.tar.gz","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1}}}`
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest" {
			_, _ = w.Write([]byte(manifest))
			return
		}
		_ = json.NewEncoder(w).Encode(releaseInfo{TagName: "v2.2.0", Assets: []releaseAsset{{Name: manifestAsset, BrowserDownloadURL: server.URL + "/manifest"}}})
	}))
	defer server.Close()
	checker := NewChecker("", "2.1.0", "", Platform{OS: "linux", Arch: "amd64", InDocker: true})
	checker.apiBase = server.URL
	if _, err := checker.Check(context.Background()); err == nil {
		t.Fatal("unsigned update was accepted without a configured public key")
	}
}

func TestImageRequirementIsPlatformAndImageSpecific(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	manifest := `{"schemaVersion":1,"version":"2.2.0","tag":"v2.2.0","requiresImageUpdate":true,"assets":{"windows-amd64":{"name":"x.zip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","packageManifestSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1},"linux-amd64":{"name":"x.tar.gz","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","packageManifestSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1}}}`
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			_, _ = w.Write([]byte(manifest))
		case "/sig":
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(manifest)))))
		default:
			_ = json.NewEncoder(w).Encode(releaseInfo{TagName: "v2.2.0", Assets: []releaseAsset{{Name: manifestAsset, BrowserDownloadURL: server.URL + "/manifest"}, {Name: signatureAsset, BrowserDownloadURL: server.URL + "/sig"}}})
		}
	}))
	defer server.Close()
	for _, tt := range []struct {
		name, current         string
		platform              Platform
		hasUpdate, needsImage bool
	}{
		{"windows", "2.1.0", Platform{OS: "windows", Arch: "amd64"}, true, false},
		{"docker old image same app", "2.2.0", Platform{OS: "linux", Arch: "amd64", InDocker: true, ImageVersion: "2.1.0"}, false, true},
		{"docker updated image", "2.2.0", Platform{OS: "linux", Arch: "amd64", InDocker: true, ImageVersion: "2.2.0"}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := NewChecker("", tt.current, "", tt.platform)
			c.apiBase = server.URL
			c.SetPublicKey(pub)
			r, err := c.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if r.HasUpdate != tt.hasUpdate || r.RequiresImageUpdate != tt.needsImage {
				t.Fatalf("unexpected result %+v", r)
			}
		})
	}
}
