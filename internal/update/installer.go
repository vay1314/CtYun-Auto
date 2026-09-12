package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Installer downloads, verifies and stages a release package so a platform
// executor can apply it.
type Installer struct {
	checker    *Checker
	downloader *Downloader
	dataDir    string
	version    string
	platform   Platform
}

func NewInstaller(checker *Checker, dataDir, version string, platform Platform) *Installer {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	return &Installer{
		checker:    checker,
		downloader: NewDownloader(),
		dataDir:    dataDir,
		version:    version,
		platform:   platform,
	}
}

// Prepare downloads and extracts the package for the current platform into
// dataDir/updates/staging, returning the install request for the executor.
func (i *Installer) Prepare(ctx context.Context, manifest *Manifest, onProgress func(received, total int64)) (InstallRequest, error) {
	asset, ok := manifest.AssetFor(i.platform.OS, i.platform.Arch)
	if !ok {
		return InstallRequest{}, fmt.Errorf("当前架构 %s 暂不支持在线更新", i.platform.AssetKey())
	}
	url, err := i.checker.AssetURL(ctx, manifest, asset.Name)
	if err != nil {
		return InstallRequest{}, err
	}
	staging := filepath.Join(i.dataDir, "updates", "staging", manifest.Version)
	if err := os.MkdirAll(staging, 0750); err != nil {
		return InstallRequest{}, err
	}
	packagePath := filepath.Join(i.dataDir, "updates", "downloads", asset.Name)
	if err := i.downloader.DownloadFile(ctx, url, i.checker.Proxy(), asset.Size, asset.SHA256, packagePath, onProgress); err != nil {
		return InstallRequest{}, err
	}
	defer os.Remove(packagePath)
	expanded, err := ArchiveExpandedSize(packagePath)
	if err != nil {
		return InstallRequest{}, err
	}
	installed, err := os.Executable()
	if err != nil {
		return InstallRequest{}, err
	}
	if err := CheckInstallSpace(i.dataDir, filepath.Dir(installed), expanded); err != nil {
		return InstallRequest{}, err
	}
	if err := os.RemoveAll(staging); err != nil {
		return InstallRequest{}, err
	}
	if err := os.MkdirAll(staging, 0750); err != nil {
		return InstallRequest{}, err
	}
	if err := extractPackage(packagePath, asset.Name, staging); err != nil {
		return InstallRequest{}, err
	}
	if err := flattenPackage(staging); err != nil {
		return InstallRequest{}, err
	}
	if err := VerifyPackage(staging, manifest.Version, i.platform.AssetKey()); err != nil {
		return InstallRequest{}, err
	}
	packageManifestSHA256, err := fileSHA256(filepath.Join(staging, PackageManifestName))
	if err != nil {
		return InstallRequest{}, err
	}
	if !strings.EqualFold(packageManifestSHA256, asset.PackageManifestSHA256) {
		return InstallRequest{}, errors.New("包清单与签名发布清单不匹配，请使用包含包清单摘要的正式发布版本")
	}

	executable, err := os.Executable()
	if err != nil {
		return InstallRequest{}, err
	}
	return InstallRequest{
		SignedManifest: manifest.SignedJSON, ManifestSignature: manifest.Signature,
		FromVersion:            i.version,
		ToVersion:              manifest.Version,
		MinimumLauncherVersion: manifest.MinimumLauncherVersion,
		PackageManifestSHA256:  packageManifestSHA256,
		StagingDir:             staging,
		Executable:             executable,
		StaticDir:              filepath.Join(staging, "static"),
		BackupDir:              filepath.Join(i.dataDir, "updates", "backups", i.version),
	}, nil
}

func extractPackage(packagePath, assetName, dest string) error {
	switch {
	case strings.HasSuffix(assetName, ".zip"):
		return ExtractZipFile(packagePath, dest)
	case strings.HasSuffix(assetName, ".tar.gz"):
		return ExtractTarGzFile(packagePath, dest)
	default:
		return errors.New("不支持的更新包格式")
	}
}

// flattenPackage removes a single wrapping directory created by packaging so
// staging contains the executable and static directory directly.
func flattenPackage(dest string) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	inner := filepath.Join(dest, entries[0].Name())
	children, err := os.ReadDir(inner)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := os.Rename(filepath.Join(inner, child.Name()), filepath.Join(dest, child.Name())); err != nil {
			return err
		}
	}
	return os.Remove(inner)
}
