package update

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type Asset struct {
	PackageManifestSHA256 string `json:"packageManifestSha256,omitempty"`
	Name                  string `json:"name"`
	SHA256                string `json:"sha256"`
	Size                  int64  `json:"size"`
}

type Manifest struct {
	SignedJSON             []byte           `json:"-"`
	Signature              string           `json:"-"`
	DatabaseVersion        int              `json:"databaseVersion"`
	MinimumDatabaseVersion int              `json:"minimumDatabaseVersion"`
	SchemaVersion          int              `json:"schemaVersion"`
	Version                string           `json:"version"`
	Tag                    string           `json:"tag"`
	PublishedAt            string           `json:"publishedAt"`
	ReleaseURL             string           `json:"releaseUrl"`
	MinimumAppVersion      string           `json:"minimumAppVersion"`
	MinimumLauncherVersion string           `json:"minimumLauncherVersion"`
	RequiresImageUpdate    bool             `json:"requiresImageUpdate"`
	Notes                  string           `json:"notes"`
	Assets                 map[string]Asset `json:"assets"`
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("更新清单格式无效: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) Validate() error {
	if m.DatabaseVersion < 0 || m.MinimumDatabaseVersion < 0 || (m.DatabaseVersion > 0 && m.MinimumDatabaseVersion > m.DatabaseVersion) {
		return fmt.Errorf("数据库兼容版本无效")
	}
	if m.SchemaVersion != 1 {
		return fmt.Errorf("不支持的更新清单版本 %d", m.SchemaVersion)
	}
	if m.Version == "" || m.Tag == "" {
		return fmt.Errorf("更新清单缺少版本或标签")
	}
	if err := ValidateReleaseURL(m.ReleaseURL, m.Tag); err != nil {
		return err
	}
	version, versionOK := ParseSemVer(m.Version)
	tagVersion, tagOK := ParseSemVer(m.Tag)
	if !versionOK || !tagOK {
		return fmt.Errorf("更新清单版本不是有效的 X.Y.Z 格式")
	}
	if version.Compare(tagVersion) != 0 {
		return fmt.Errorf("更新清单版本 %s 与标签 %s 不一致", m.Version, m.Tag)
	}
	if len(m.Assets) == 0 {
		return fmt.Errorf("更新清单没有可用的程序资源")
	}
	for name, value := range map[string]string{
		"minimumAppVersion":      m.MinimumAppVersion,
		"minimumLauncherVersion": m.MinimumLauncherVersion,
	} {
		if value != "" {
			if _, ok := ParseSemVer(value); !ok {
				return fmt.Errorf("更新清单的 %s 不是有效版本", name)
			}
		}
	}
	if minimum, ok := ParseSemVer(m.MinimumAppVersion); ok && minimum.Compare(version) > 0 {
		return fmt.Errorf("minimumAppVersion 不能高于目标版本")
	}
	for key, asset := range m.Assets {
		if strings.TrimSpace(asset.Name) == "" {
			return fmt.Errorf("更新资源 %s 缺少文件名", key)
		}
		if filepath.Base(asset.Name) != asset.Name || strings.ContainsAny(asset.Name, `/\`) {
			return fmt.Errorf("更新资源 %s 的文件名无效", key)
		}
		if !sha256Pattern.MatchString(asset.SHA256) {
			return fmt.Errorf("更新资源 %s 的 SHA-256 校验值无效", key)
		}
		if !sha256Pattern.MatchString(asset.PackageManifestSHA256) {
			return fmt.Errorf("更新资源 %s 的包清单 SHA-256 校验值无效", key)
		}
		if asset.Size <= 0 {
			return fmt.Errorf("更新资源 %s 的大小无效", key)
		}
	}
	return nil
}

func ValidateReleaseURL(raw, tag string) error {
	if raw == "" {
		return nil
	}
	u, err := url.ParseRequestURI(raw)
	wantedSuffix := "/releases/tag/" + url.PathEscape(tag)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.EscapedPath(), wantedSuffix) {
		return fmt.Errorf("更新清单的 releaseUrl 无效")
	}
	return nil
}

func (m *Manifest) AssetFor(osName, arch string) (Asset, bool) {
	asset, ok := m.Assets[osName+"-"+arch]
	return asset, ok
}

// CompatibleWith validates whether the running app and launcher can consume
// this manifest. A missing launcher version is only accepted outside Docker.
func (m *Manifest) CompatibleWith(current string, platform Platform) error {
	if m.MinimumDatabaseVersion > DatabaseVersion {
		return fmt.Errorf("当前数据库版本过旧，需要先安装中间版本")
	}
	if m.DatabaseVersion > 0 && m.DatabaseVersion < DatabaseVersion {
		return fmt.Errorf("目标正式版数据库结构早于当前程序，不能安全更新")
	}
	if minimum, ok := ParseSemVer(m.MinimumAppVersion); ok && !IsDevVersion(current) {
		running, runningOK := ParseSemVer(current)
		if !runningOK || running.Compare(minimum) < 0 {
			return fmt.Errorf("当前版本过旧，至少需要 v%s 才能在线更新", minimum.String())
		}
	}
	if platform.InDocker {
		if m.RequiresImageUpdate && (platform.ImageVersion == "" || platform.ImageVersion != m.Version) {
			return fmt.Errorf("此版本涉及容器运行环境变更，请拉取新 Docker 镜像")
		}
		if minimum, ok := ParseSemVer(m.MinimumLauncherVersion); ok {
			launcher, launcherOK := ParseSemVer(platform.LauncherVersion)
			if !launcherOK || launcher.Compare(minimum) < 0 {
				return fmt.Errorf("Launcher 版本过旧，请拉取新 Docker 镜像")
			}
		}
	}
	return nil
}
