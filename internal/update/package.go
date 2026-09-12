package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const PackageManifestName = "package-manifest.json"

type PackageManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Version       string            `json:"version"`
	Platform      string            `json:"platform"`
	Files         map[string]string `json:"files"`
}

func VerifyPackage(root, version, platform string) error {
	raw, err := os.ReadFile(filepath.Join(root, PackageManifestName))
	if err != nil {
		return fmt.Errorf("更新包缺少 %s: %w", PackageManifestName, err)
	}
	var manifest PackageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("更新包清单无效: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Version != version || manifest.Platform != platform {
		return fmt.Errorf("更新包清单与目标版本或平台不匹配")
	}
	if len(manifest.Files) == 0 {
		return fmt.Errorf("更新包清单没有文件")
	}
	required := []string{"ctyun-keeper"}
	if strings.HasPrefix(platform, "windows-") {
		required = []string{"ctyun-keeper.exe", "ctyun-keeper-updater.exe"}
	}
	for _, name := range required {
		if _, ok := manifest.Files[name]; !ok {
			return fmt.Errorf("更新包清单缺少必要文件 %s", name)
		}
	}
	hasStatic := false
	for name := range manifest.Files {
		if strings.HasPrefix(filepath.ToSlash(name), "static/") {
			hasStatic = true
			break
		}
	}
	if !hasStatic {
		return fmt.Errorf("更新包清单缺少 static 静态资源")
	}
	for name, expected := range manifest.Files {
		if name == PackageManifestName || !sha256Pattern.MatchString(expected) {
			return fmt.Errorf("更新包文件校验项无效: %s", name)
		}
		path, err := safeExtractPath(root, name)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("更新包缺少文件 %s", name)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("更新包文件类型无效: %s", name)
		}
		sum, err := fileSHA256(path)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, expected) {
			return fmt.Errorf("更新包文件校验失败: %s", name)
		}
	}
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("更新包包含不支持的文件类型: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == PackageManifestName {
			return nil
		}
		if _, ok := manifest.Files[rel]; !ok {
			return fmt.Errorf("更新包包含未校验文件: %s", rel)
		}
		return nil
	})
}

func VerifyPackageWithManifestDigest(root, version, platform, expected string) error {
	if !sha256Pattern.MatchString(expected) {
		return fmt.Errorf("包清单校验值无效")
	}
	actual, err := fileSHA256(filepath.Join(root, PackageManifestName))
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("包清单在验证后发生变化")
	}
	return VerifyPackage(root, version, platform)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
