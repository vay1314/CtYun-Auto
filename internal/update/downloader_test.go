package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestDownloaderVerifiesSHA256AndReportsProgress(t *testing.T) {
	content := []byte("release package contents")
	sum := sha256.Sum256(content)

	var server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = w.Write(content)
	}))
	defer server.Close()

	var progress int
	data, err := NewDownloader().Download(
		context.Background(),
		server.URL,
		"",
		hex.EncodeToString(sum[:]),
		func(received, total int64) { progress++ },
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Fatalf("downloaded = %q", data)
	}
	if progress == 0 {
		t.Fatal("progress callback was not invoked")
	}
}

func TestDownloaderRejectsMismatchedSHA256(t *testing.T) {
	var server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()

	_, err := NewDownloader().Download(
		context.Background(),
		server.URL,
		"",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		nil,
	)
	if err == nil {
		t.Fatal("mismatched sha256 was accepted")
	}
}

func TestDownloaderRejectsManifestSizeMismatch(t *testing.T) {
	content := []byte("short")
	sum := sha256.Sum256(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer server.Close()
	err := NewDownloader().DownloadFile(
		context.Background(), server.URL, "", int64(len(content)+1),
		hex.EncodeToString(sum[:]), t.TempDir()+"/package", nil,
	)
	if err == nil {
		t.Fatal("manifest size mismatch was accepted")
	}
}
