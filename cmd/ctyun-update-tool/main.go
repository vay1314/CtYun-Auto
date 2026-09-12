package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		fatal("用法: ctyun-update-tool public-key|package-manifest|release-manifest")
	}
	switch os.Args[1] {
	case "public-key":
		key := privateKey()
		fmt.Print(base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	case "package-manifest":
		packageManifest(os.Args[2:])
	case "release-manifest":
		releaseManifest(os.Args[2:])
	default:
		fatal("未知命令")
	}
}

func privateKey() ed25519.PrivateKey {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("UPDATE_SIGNING_PRIVATE_KEY")))
	if err != nil {
		fatal("UPDATE_SIGNING_PRIVATE_KEY 不是有效 Base64: %v", err)
	}
	if len(raw) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(raw)
	}
	if len(raw) != ed25519.PrivateKeySize {
		fatal("UPDATE_SIGNING_PRIVATE_KEY 必须是 32 字节种子或 64 字节私钥")
	}
	return ed25519.PrivateKey(raw)
}

func packageManifest(args []string) {
	flags := flag.NewFlagSet("package-manifest", flag.ExitOnError)
	root := flags.String("root", "", "package root")
	version := flags.String("version", "", "version")
	platform := flags.String("platform", "", "platform")
	_ = flags.Parse(args)
	if *root == "" || *platform == "" {
		fatal("package-manifest 参数不完整")
	}
	if _, ok := update.ParseSemVer(*version); !ok {
		fatal("版本无效")
	}
	files := map[string]string{}
	err := filepath.Walk(*root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("不支持的文件类型: %s", path)
		}
		rel, err := filepath.Rel(*root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == update.PackageManifestName {
			return nil
		}
		files[rel], err = hashFile(path)
		return err
	})
	if err != nil {
		fatal("扫描更新包: %v", err)
	}
	writeJSON(filepath.Join(*root, update.PackageManifestName), update.PackageManifest{
		SchemaVersion: 1, Version: *version, Platform: *platform, Files: files,
	})
}

func releaseManifest(args []string) {
	flags := flag.NewFlagSet("release-manifest", flag.ExitOnError)
	dist := flags.String("dist", "dist", "dist")
	version := flags.String("version", "", "version")
	repository := flags.String("repository", "vay1314/CtYun-Keeper", "repository")
	minimumApp := flags.String("minimum-app", "", "minimum app")
	minimumLauncher := flags.String("minimum-launcher", "1.0.0", "minimum launcher")
	requiresImage := flags.Bool("requires-image-update", false, "requires image update")
	notes := flags.String("notes", "", "notes")
	_ = flags.Parse(args)
	if _, ok := update.ParseSemVer(*version); !ok {
		fatal("版本无效")
	}
	names := map[string]string{
		"windows-amd64": fmt.Sprintf("CtYunKeeper-v%s-windows-amd64.zip", *version),
		"windows-arm64": fmt.Sprintf("CtYunKeeper-v%s-windows-arm64.zip", *version),
		"linux-amd64":   fmt.Sprintf("CtYunKeeper-v%s-linux-amd64.tar.gz", *version),
		"linux-arm64":   fmt.Sprintf("CtYunKeeper-v%s-linux-arm64.tar.gz", *version),
	}
	assets := map[string]update.Asset{}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var checksums strings.Builder
	for _, key := range keys {
		name := names[key]
		path := filepath.Join(*dist, name)
		info, err := os.Stat(path)
		if err != nil {
			fatal("缺少发布包 %s", name)
		}
		sum, err := hashFile(path)
		if err != nil {
			fatal("计算校验值: %v", err)
		}
		packageDigest, err := update.ArchiveManifestDigest(path)
		if err != nil {
			fatal("读取包清单: %v", err)
		}
		assets[key] = update.Asset{Name: name, SHA256: sum, Size: info.Size(), PackageManifestSHA256: packageDigest}
		fmt.Fprintf(&checksums, "%s  %s\n", sum, name)
	}
	manifest := update.Manifest{
		DatabaseVersion: update.DatabaseVersion, MinimumDatabaseVersion: 1,
		SchemaVersion: 1, Version: *version, Tag: "v" + *version,
		PublishedAt:       time.Now().UTC().Format(time.RFC3339),
		ReleaseURL:        "https://github.com/" + *repository + "/releases/tag/v" + *version,
		MinimumAppVersion: *minimumApp, MinimumLauncherVersion: *minimumLauncher,
		RequiresImageUpdate: *requiresImage, Notes: *notes, Assets: assets,
	}
	if err := manifest.Validate(); err != nil {
		fatal("更新清单无效: %v", err)
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fatal("编码更新清单: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(*dist, "update-manifest.json"), raw, 0644); err != nil {
		fatal("写更新清单: %v", err)
	}
	signature := ed25519.Sign(privateKey(), raw)
	if err := os.WriteFile(filepath.Join(*dist, "update-manifest.sig"), []byte(base64.StdEncoding.EncodeToString(signature)+"\n"), 0644); err != nil {
		fatal("写签名: %v", err)
	}
	if err := os.WriteFile(filepath.Join(*dist, "checksums.txt"), []byte(checksums.String()), 0644); err != nil {
		fatal("写校验文件: %v", err)
	}
}

func writeJSON(path string, value any) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatal("编码 JSON: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
		fatal("写文件: %v", err)
	}
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func fatal(format string, values ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", values...)
	os.Exit(1)
}
