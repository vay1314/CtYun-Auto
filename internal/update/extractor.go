package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	maxExtractedSize  int64 = 1 << 30
	maxExtractedFile  int64 = 256 << 20
	maxExtractedFiles       = 10000
)

// ExtractZip safely extracts a zip archive into dest.
func ExtractZip(data []byte, dest string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	return extractZipReader(reader, dest)
}

func ExtractZipFile(path, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	return extractZipReader(&reader.Reader, dest)
}

func extractZipReader(reader *zip.Reader, dest string) error {
	var total int64
	seen := map[string]struct{}{}
	for index, file := range reader.File {
		if index >= maxExtractedFiles {
			return fmt.Errorf("压缩包文件数量过多")
		}
		target, err := safeExtractPath(dest, file.Name)
		if err != nil {
			return err
		}
		if err := rememberExtractPath(seen, target); err != nil {
			return err
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return fmt.Errorf("压缩包包含不支持的文件类型: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0750); err != nil {
				return err
			}
			continue
		}
		size := int64(file.UncompressedSize64)
		if size < 0 || size > maxExtractedFile || total+size > maxExtractedSize {
			return fmt.Errorf("压缩包展开大小超过限制")
		}
		total += size
		if err := writeExtractedFile(target, file.Mode(), size, func() (io.ReadCloser, error) { return file.Open() }); err != nil {
			return err
		}
	}
	return nil
}

// ExtractTarGz safely extracts a gzipped tar archive into dest.
func ExtractTarGz(data []byte, dest string) error {
	return extractTarGzReader(bytes.NewReader(data), dest)
}

func ExtractTarGzFile(path, dest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return extractTarGzReader(file, dest)
}

func extractTarGzReader(source io.Reader, dest string) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	count := 0
	seen := map[string]struct{}{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		count++
		if count > maxExtractedFiles {
			return fmt.Errorf("压缩包文件数量过多")
		}
		target, err := safeExtractPath(dest, header.Name)
		if err != nil {
			return err
		}
		if err := rememberExtractPath(seen, target); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0750); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > maxExtractedFile || total+header.Size > maxExtractedSize {
				return fmt.Errorf("压缩包展开大小超过限制")
			}
			total += header.Size
			if err := writeExtractedFile(target, header.FileInfo().Mode(), header.Size, func() (io.ReadCloser, error) { return io.NopCloser(reader), nil }); err != nil {
				return err
			}
		default:
			return fmt.Errorf("压缩包包含不支持的文件类型: %s", header.Name)
		}
	}
}

func safeExtractPath(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("压缩包包含非法路径: %s", name)
	}
	if runtime.GOOS == "windows" {
		for _, part := range strings.Split(clean, string(filepath.Separator)) {
			base := strings.TrimSuffix(strings.ToLower(part), filepath.Ext(part))
			if strings.Contains(part, ":") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || isWindowsReservedName(base) {
				return "", fmt.Errorf("压缩包包含 Windows 非法路径: %s", name)
			}
		}
	}
	target := filepath.Join(dest, clean)
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("压缩包路径越界: %s", name)
	}
	return target, nil
}

func rememberExtractPath(seen map[string]struct{}, target string) error {
	key := strings.ToLower(filepath.Clean(target))
	if _, ok := seen[key]; ok {
		return fmt.Errorf("压缩包包含重复路径: %s", target)
	}
	seen[key] = struct{}{}
	return nil
}

func isWindowsReservedName(name string) bool {
	switch name {
	case "con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return true
	}
	return false
}

func writeExtractedFile(target string, mode os.FileMode, expectedSize int64, open func() (io.ReadCloser, error)) error {
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return err
	}
	src, err := open()
	if err != nil {
		return err
	}
	defer src.Close()
	perm := os.FileMode(0750)
	if mode.Perm() != 0 {
		perm = mode.Perm()
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	written, err := io.Copy(out, io.LimitReader(src, expectedSize+1))
	if err != nil {
		out.Close()
		return err
	}
	if written != expectedSize {
		out.Close()
		return fmt.Errorf("压缩包文件大小不符: %s", target)
	}
	return out.Close()
}
