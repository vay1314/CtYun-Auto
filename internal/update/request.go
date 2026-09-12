package update

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// InstallRequest is written by the running application and consumed by the
// platform executor (updater on Windows, launcher in Docker).
type InstallRequest struct {
	SignedManifest         []byte `json:"signedManifest,omitempty"`
	ManifestSignature      string `json:"manifestSignature,omitempty"`
	Action                 string `json:"action"`
	FromVersion            string `json:"fromVersion"`
	ToVersion              string `json:"toVersion"`
	MinimumLauncherVersion string `json:"minimumLauncherVersion,omitempty"`
	PackageManifestSHA256  string `json:"packageManifestSha256,omitempty"`
	StagingDir             string `json:"stagingDir"`
	Executable             string `json:"executable"`
	StaticDir              string `json:"staticDir"`
	BackupDir              string `json:"backupDir"`
	DatabasePath           string `json:"databasePath"`
	DatabaseBackup         string `json:"databaseBackup"`
	HealthURL              string `json:"healthUrl"`
	ParentPID              int    `json:"parentPid"`
	RestartMode            string `json:"restartMode"`
	ServiceName            string `json:"serviceName,omitempty"`
	Token                  string `json:"token"`
	CreatedAt              string `json:"createdAt"`
	HistoryID              int64  `json:"historyId"`
	ResultPath             string `json:"resultPath"`
}

// The executor verifies the release authority independently of the application.
func VerifySignedRequest(req InstallRequest, publicKey, platform string) error {
	_, err := VerifySignedRequestManifest(req, publicKey, platform)
	return err
}

// VerifySignedRequestManifest verifies the release authority and returns the
// signed policy so platform executors can enforce compatibility themselves.
func VerifySignedRequestManifest(req InstallRequest, publicKey, platform string) (*Manifest, error) {
	key, err := ParsePublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	signature, err := DecodeSignature(req.ManifestSignature)
	if err != nil {
		return nil, err
	}
	if !VerifySignature(key, req.SignedManifest, signature) {
		return nil, errors.New("执行器清单签名校验失败")
	}
	m, err := ParseManifest(req.SignedManifest)
	if err != nil {
		return nil, err
	}
	a, ok := m.Assets[platform]
	if !ok || m.Version != req.ToVersion || m.MinimumLauncherVersion != req.MinimumLauncherVersion || !strings.EqualFold(a.PackageManifestSHA256, req.PackageManifestSHA256) {
		return nil, errors.New("执行请求与签名清单不匹配")
	}
	return m, nil
}

func WriteInstallRequest(path string, req InstallRequest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

func ReadInstallRequest(path string) (InstallRequest, error) {
	var req InstallRequest
	data, err := os.ReadFile(path)
	if err != nil {
		return req, err
	}
	err = json.Unmarshal(data, &req)
	return req, err
}

func NewRequestToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func installTokenPath(dataDir string) string {
	return filepath.Join(dataDir, "updates", "install.token")
}

func WriteInstallToken(dataDir, token string) error {
	if len(token) != 64 {
		return errors.New("更新令牌无效")
	}
	path := installTokenPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token), 0600); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

func ConsumeInstallToken(dataDir string) (string, error) {
	path := installTokenPath(dataDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取更新令牌失败: %w", err)
	}
	_ = os.Remove(path)
	token := strings.TrimSpace(string(raw))
	if len(token) != 64 {
		return "", errors.New("更新令牌无效")
	}
	return token, nil
}

func (r InstallRequest) Validate(dataDir, installDir, expectedToken string) error {
	if r.Action != "install" {
		return errors.New("更新请求动作无效")
	}
	if len(expectedToken) != 64 || r.Token != expectedToken {
		return errors.New("更新请求认证失败")
	}
	if r.ParentPID <= 0 {
		return errors.New("更新请求缺少主进程编号")
	}
	if _, ok := ParseSemVer(r.FromVersion); !ok && !IsDevVersion(r.FromVersion) {
		return errors.New("更新请求的当前版本无效")
	}
	if _, ok := ParseSemVer(r.ToVersion); !ok {
		return errors.New("更新请求的目标版本无效")
	}
	if r.MinimumLauncherVersion != "" {
		if _, ok := ParseSemVer(r.MinimumLauncherVersion); !ok {
			return errors.New("更新请求的最低 Launcher 版本无效")
		}
	}
	if r.Action == "install" && !sha256Pattern.MatchString(r.PackageManifestSHA256) {
		return errors.New("更新请求缺少有效的包清单校验值")
	}
	for name, value := range map[string]string{
		"stagingDir": r.StagingDir, "backupDir": r.BackupDir,
		"databaseBackup": r.DatabaseBackup, "resultPath": r.ResultPath,
	} {
		if value == "" || !pathWithin(dataDir, value) {
			return fmt.Errorf("更新请求的 %s 超出数据目录", name)
		}
		if err := rejectLinkedPath(dataDir, value); err != nil {
			return fmt.Errorf("更新请求的 %s 不安全: %w", name, err)
		}
	}
	if !pathWithin(filepath.Join(dataDir, "updates", "staging"), r.StagingDir) {
		return errors.New("更新暂存目录无效")
	}
	for name, value := range map[string]string{
		"executable": r.Executable,
	} {
		if value == "" || !pathWithin(installDir, value) {
			return fmt.Errorf("更新请求的 %s 超出安装目录", name)
		}
		if err := rejectLinkedPath(installDir, value); err != nil {
			return fmt.Errorf("更新请求的 %s 不安全: %w", name, err)
		}
	}
	if r.DatabasePath == "" || !pathWithin(dataDir, r.DatabasePath) {
		return errors.New("更新请求的数据库路径无效")
	}
	if !validHealthURL(r.HealthURL) {
		return errors.New("更新请求的健康检查地址无效")
	}
	if r.RestartMode != "self" && r.RestartMode != "supervisor" {
		return errors.New("更新请求的重启模式无效")
	}
	if r.RestartMode == "supervisor" && r.ServiceName == "" {
		return errors.New("服务更新请求缺少服务名")
	}
	if r.HistoryID <= 0 {
		return errors.New("更新请求缺少历史记录编号")
	}
	created, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil || time.Since(created) > 30*time.Minute || time.Until(created) > 5*time.Minute {
		return errors.New("更新请求已过期")
	}
	return nil
}

func validHealthURL(raw string) bool {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "/health" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

func pathWithin(root, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func rejectLinkedPath(root, candidate string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil {
		return err
	}
	current := rootAbs
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("路径包含符号链接")
		}
	}
	return nil
}

type InstallResult struct {
	FromVersion string `json:"fromVersion"`
	Platform    string `json:"platform"`
	HistoryID   int64  `json:"historyId"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	Version     string `json:"version"`
	CreatedAt   string `json:"createdAt"`
}

func ResultFor(req InstallRequest, status, message, platform string) InstallResult {
	return InstallResult{HistoryID: req.HistoryID, FromVersion: req.FromVersion, Version: req.ToVersion, Platform: platform, Status: status, Message: message}
}

func WriteInstallResult(path string, result InstallResult) error {
	result.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	_ = os.Remove(path)
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(filepath.Dir(filepath.Dir(path)), "executor-progress.json"))
	if result.Status == "failed" || (result.Status == "rolled_back" && strings.Contains(result.Message, "失败")) {
		_ = os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(path)), "last-failure.json"), data, 0600)
	}
	return nil
}

func WriteExecutorProgress(req InstallRequest, status Status, message string) {
	path := filepath.Join(filepath.Dir(filepath.Dir(req.ResultPath)), "executor-progress.json")
	p := Progress{Status: status, Message: message, FromVersion: req.FromVersion, TargetVersion: req.ToVersion, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	raw, _ := json.Marshal(p)
	_ = os.WriteFile(path+".tmp", raw, 0600)
	_ = os.Remove(path)
	_ = os.Rename(path+".tmp", path)
}

func ConsumeInstallResults(dir string, consume func(InstallResult) error) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var consumeErrors []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			consumeErrors = append(consumeErrors, fmt.Errorf("读取更新结果 %s: %w", entry.Name(), err))
			continue
		}
		var result InstallResult
		if err := json.Unmarshal(raw, &result); err != nil {
			consumeErrors = append(consumeErrors, quarantineInvalidResult(path, fmt.Errorf("解析更新结果: %w", err)))
			continue
		}
		if result.HistoryID <= 0 || (result.Status != "success" && result.Status != "failed" && result.Status != "rolled_back") {
			consumeErrors = append(consumeErrors, quarantineInvalidResult(path, errors.New("更新结果文件无效")))
			continue
		}
		expectedName := strconv.FormatInt(result.HistoryID, 10) + ".json"
		if entry.Name() != expectedName {
			consumeErrors = append(consumeErrors, quarantineInvalidResult(path, fmt.Errorf("更新结果文件名应为 %s", expectedName)))
			continue
		}
		if err := consume(result); err != nil {
			consumeErrors = append(consumeErrors, fmt.Errorf("提交更新结果 %s: %w", entry.Name(), err))
			continue
		}
		if err := os.Remove(path); err != nil {
			consumeErrors = append(consumeErrors, fmt.Errorf("删除已提交更新结果 %s: %w", entry.Name(), err))
		}
	}
	return errors.Join(consumeErrors...)
}

func quarantineInvalidResult(path string, cause error) error {
	dir := filepath.Join(filepath.Dir(path), "invalid")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("%v；创建隔离目录失败: %w", cause, err)
	}
	target := filepath.Join(dir, filepath.Base(path)+"."+strconv.FormatInt(time.Now().UnixNano(), 10)+".invalid")
	if err := os.Rename(path, target); err != nil {
		return fmt.Errorf("%v；隔离文件失败: %w", cause, err)
	}
	return fmt.Errorf("%v；已隔离到 %s", cause, target)
}
