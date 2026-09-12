package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxPackageSize int64 = 512 << 20

// Downloader fetches release packages with progress reporting and digest
// verification. A configured proxy is the exclusive download route.
type Downloader struct {
	client    *http.Client
	userAgent string
}

func NewDownloader() *Downloader {
	return &Downloader{
		client:    secureHTTPClient(10 * time.Minute),
		userAgent: "CtYunKeeper-Updater",
	}
}

func (d *Downloader) Download(ctx context.Context, rawURL, proxy, expectedSHA256 string, onProgress func(received, total int64)) ([]byte, error) {
	dir, err := os.MkdirTemp("", "ctyun-update-download-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "package")
	if err := d.DownloadFile(ctx, rawURL, proxy, 0, expectedSHA256, path, onProgress); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// DownloadFile streams a package to disk, verifies its exact advertised size
// (when non-zero) and digest, then atomically publishes the completed file.
func (d *Downloader) DownloadFile(ctx context.Context, rawURL, proxy string, expectedSize int64, expectedSHA256, dest string, onProgress func(received, total int64)) error {
	if !sha256Pattern.MatchString(strings.TrimSpace(expectedSHA256)) {
		return errors.New("校验值格式无效")
	}
	if expectedSize < 0 || expectedSize > maxPackageSize {
		return errors.New("更新包大小无效")
	}
	requestURL, err := ResolveRequestURL(proxy, rawURL)
	if err != nil {
		return fmt.Errorf("代理设置无效: %w", err)
	}
	if err := d.downloadFile(ctx, requestURL, expectedSize, expectedSHA256, dest, onProgress); err != nil {
		return fmt.Errorf("下载更新包失败: %w", err)
	}
	return nil
}

func (d *Downloader) downloadFile(ctx context.Context, rawURL string, expectedSize int64, expectedSHA256, dest string, onProgress func(received, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", d.userAgent)
	resp, err := d.client.Do(req)
	if err != nil {
		return safeNetworkError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 HTTP %d", redactedURL(rawURL), resp.StatusCode)
	}
	limit := maxPackageSize
	if expectedSize > 0 {
		limit = expectedSize
		if resp.ContentLength > 0 && resp.ContentLength != expectedSize {
			return fmt.Errorf("更新包大小不符：期望 %d，响应 %d", expectedSize, resp.ContentLength)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return err
	}
	tmp := dest + ".part"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		out.Close()
		if !success {
			_ = os.Remove(tmp)
		}
	}()
	hash := sha256.New()
	reader := &progressReader{reader: io.LimitReader(resp.Body, limit+1), total: expectedSize, callback: onProgress}
	written, err := io.Copy(io.MultiWriter(out, hash), reader)
	if err != nil {
		return err
	}
	if written > limit {
		return errors.New("下载包超过允许大小")
	}
	if expectedSize > 0 && written != expectedSize {
		return fmt.Errorf("更新包大小不符：期望 %d，实际 %d", expectedSize, written)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedSHA256) {
		return errors.New("下载包 SHA-256 校验失败")
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Remove(dest)
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	success = true
	return nil
}

type progressReader struct {
	reader   io.Reader
	read     int64
	total    int64
	callback func(received, total int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	if n > 0 && r.callback != nil {
		r.callback(r.read, r.total)
	}
	return n, err
}

func verifySHA256(data []byte, expected string) error {
	expected = strings.TrimSpace(expected)
	if !sha256Pattern.MatchString(expected) {
		return errors.New("校验值格式无效")
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
		return errors.New("下载包 SHA-256 校验失败")
	}
	return nil
}
