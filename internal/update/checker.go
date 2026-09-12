package update

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultRepo     = "vay1314/CtYun-Keeper"
	maxResponseSize = 2 << 20
	manifestAsset   = "update-manifest.json"
	signatureAsset  = "update-manifest.sig"
)

var errManifestMissing = errors.New("Release 未提供更新清单")

type Checker struct {
	repo      string
	apiBase   string
	current   string
	proxy     string
	publicKey ed25519.PublicKey
	platform  Platform
	client    *http.Client
	userAgent string
	mu        sync.RWMutex
}

type CheckResult struct {
	CurrentVersion      string
	LatestVersion       string
	LatestTag           string
	HasUpdate           bool
	CurrentIsDev        bool
	NewerThanLatest     bool
	Supported           bool
	Manifest            *Manifest
	Message             string
	PublishedAt         string
	ReleaseURL          string
	Notes               string
	RequiresImageUpdate bool
}

type releaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	PublishedAt string         `json:"published_at"`
	HTMLURL     string         `json:"html_url"`
	Assets      []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func NewChecker(repo, current, proxy string, platform Platform) *Checker {
	if strings.TrimSpace(repo) == "" {
		repo = defaultRepo
	}
	return &Checker{
		repo:      strings.TrimSpace(repo),
		apiBase:   "https://api.github.com",
		current:   strings.TrimSpace(current),
		proxy:     strings.TrimSpace(proxy),
		platform:  platform,
		client:    secureHTTPClient(15 * time.Second),
		userAgent: "CtYunKeeper-Updater",
	}
}

func (c *Checker) SetProxy(proxy string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.proxy = strings.TrimSpace(proxy)
}

func (c *Checker) SetPublicKey(key ed25519.PublicKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publicKey = key
}

func (c *Checker) Proxy() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.proxy
}

// AssetURL returns the download URL for the named release asset.
func (c *Checker) AssetURL(ctx context.Context, manifest *Manifest, assetName string) (string, error) {
	release, err := c.fetchLatestRelease(ctx)
	if err != nil {
		return "", err
	}
	if release.TagName != manifest.Tag {
		return "", fmt.Errorf("更新清单版本与 Release 标签不一致")
	}
	for _, asset := range release.Assets {
		if asset.Name == assetName && asset.BrowserDownloadURL != "" {
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("Release 未找到资源 %s", assetName)
}

func (c *Checker) Check(ctx context.Context) (*CheckResult, error) {
	current, currentOK := ParseSemVer(c.current)
	result := &CheckResult{CurrentVersion: c.current, Supported: true}
	isDev := IsDevVersion(c.current)
	result.CurrentIsDev = isDev
	if !currentOK && !isDev {
		result.Supported = false
		result.Message = "当前版本格式无效，无法检测更新"
		return result, nil
	}

	release, err := c.fetchLatestRelease(ctx)
	if err != nil {
		return nil, err
	}
	latest, latestOK := ParseSemVer(release.TagName)
	if !latestOK {
		return nil, fmt.Errorf("最新 Release 标签 %s 不是有效的版本号", release.TagName)
	}

	result.LatestVersion = latest.String()
	result.LatestTag = release.TagName
	manifest, manifestErr := c.fetchManifest(ctx, release)
	if manifestErr != nil {
		return nil, manifestErr
	}
	if manifest.Tag != release.TagName {
		return nil, fmt.Errorf("更新清单版本与 Release 标签不一致")
	}
	result.Manifest = manifest
	result.PublishedAt = manifest.PublishedAt
	result.ReleaseURL = manifest.ReleaseURL
	result.Notes = manifest.Notes
	comparison := -1
	if currentOK {
		comparison = current.Compare(latest)
	}
	if comparison > 0 {
		result.NewerThanLatest = true
		result.Message = "正在运行预发布或自定义版本"
		return result, nil
	}
	result.RequiresImageUpdate = c.platform.InDocker && manifest.RequiresImageUpdate && c.platform.ImageVersion != manifest.Version
	if c.platform.InDocker && manifest.MinimumLauncherVersion != "" {
		minimum, valid := ParseSemVer(manifest.MinimumLauncherVersion)
		launcher, known := ParseSemVer(c.platform.LauncherVersion)
		if valid && (!known || launcher.Compare(minimum) < 0) {
			result.RequiresImageUpdate = true
			result.Supported = false
			result.Message = "Launcher 版本过旧，请拉取新 Docker 镜像"
			return result, nil
		}
	}
	if result.RequiresImageUpdate && current.Compare(latest) == 0 {
		result.Supported = false
		result.Message = "当前程序已是最新版，但容器运行环境需要升级，请拉取新 Docker 镜像"
		return result, nil
	}
	if comparison == 0 {
		result.Message = "已是最新版本"
		return result, nil
	}
	if _, ok := manifest.AssetFor(c.platform.OS, c.platform.Arch); !ok {
		result.Supported = false
		result.Message = "当前架构暂不支持在线更新"
		return result, nil
	}
	if c.platform.OS != "windows" && !c.platform.InDocker {
		result.Supported = false
		result.Message = "当前部署方式暂不支持在线更新，请手动安装程序包"
		return result, nil
	}
	if err := manifest.CompatibleWith(c.current, c.platform); err != nil {
		result.Supported = false
		result.Message = err.Error()
		return result, nil
	}
	result.HasUpdate = true
	result.Message = "发现新版本 v" + latest.String()
	if isDev {
		result.Message = "开发版可更新到正式版 v" + latest.String()
	}
	return result, nil
}

func (c *Checker) fetchLatestRelease(ctx context.Context) (*releaseInfo, error) {
	apiURL := fmt.Sprintf("%s/repos/%s/releases/latest", c.apiBase, c.repo)
	data, err := c.get(ctx, apiURL)
	if err != nil {
		return nil, fmt.Errorf("查询最新版本失败: %w", err)
	}
	var release releaseInfo
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, fmt.Errorf("解析最新版本信息失败: %w", err)
	}
	if release.TagName == "" {
		return nil, errors.New("未找到任何 Release")
	}
	return &release, nil
}

func (c *Checker) fetchManifest(ctx context.Context, release *releaseInfo) (*Manifest, error) {
	manifestURL := ""
	signatureURL := ""
	for _, asset := range release.Assets {
		switch asset.Name {
		case manifestAsset:
			manifestURL = asset.BrowserDownloadURL
		case signatureAsset:
			signatureURL = asset.BrowserDownloadURL
		}
	}
	if manifestURL == "" {
		return nil, errManifestMissing
	}
	c.mu.RLock()
	publicKey := append(ed25519.PublicKey(nil), c.publicKey...)
	c.mu.RUnlock()
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("当前程序未配置有效的更新签名公钥")
	}
	data, err := c.get(ctx, manifestURL)
	if err != nil {
		return nil, fmt.Errorf("下载更新清单失败: %w", err)
	}
	if signatureURL == "" {
		return nil, errors.New("Release 未提供更新清单签名")
	}
	signatureRaw, err := c.get(ctx, signatureURL)
	if err != nil {
		return nil, fmt.Errorf("下载更新清单签名失败: %w", err)
	}
	signature, err := DecodeSignature(string(signatureRaw))
	if err != nil {
		return nil, err
	}
	if !VerifySignature(publicKey, data, signature) {
		return nil, errors.New("更新清单签名校验失败")
	}
	manifest, err := ParseManifest(data)
	if err == nil {
		manifest.SignedJSON = data
		manifest.Signature = string(signatureRaw)
	}
	return manifest, err
}

func (c *Checker) get(ctx context.Context, rawURL string) ([]byte, error) {
	requestURL, err := ResolveRequestURL(c.Proxy(), rawURL)
	if err != nil {
		return nil, fmt.Errorf("代理设置无效: %w", err)
	}
	return c.download(ctx, requestURL)
}

func secureHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("重定向次数过多")
			}
			if _, err := validateTargetURL(req.URL.String()); err != nil {
				return err
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("拒绝从 HTTPS 重定向到不安全协议")
			}
			return nil
		},
	}
}

func redactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "无效地址"
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String()
}

func (c *Checker) download(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, safeNetworkError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseSize {
		return nil, errors.New("响应内容过大")
	}
	return data, nil
}

func safeNetworkError(err error) error {
	var e *url.Error
	if errors.As(err, &e) {
		return fmt.Errorf("%s %s: %w", e.Op, redactedURL(e.URL), safeNetworkError(e.Err))
	}
	return err
}
