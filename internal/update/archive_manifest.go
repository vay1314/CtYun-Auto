package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
)

func ArchiveManifestDigest(filename string) (string, error) {
	digest := func(r io.Reader) (string, error) {
		data, err := io.ReadAll(io.LimitReader(r, (2<<20)+1))
		if err != nil {
			return "", err
		}
		if len(data) > 2<<20 {
			return "", errors.New("包清单超过大小限制")
		}
		h := sha256.Sum256(data)
		return hex.EncodeToString(h[:]), nil
	}
	if strings.HasSuffix(filename, ".zip") {
		z, err := zip.OpenReader(filename)
		if err != nil {
			return "", err
		}
		defer z.Close()
		for _, f := range z.File {
			if path.Base(f.Name) == PackageManifestName {
				r, err := f.Open()
				if err != nil {
					return "", err
				}
				defer r.Close()
				return digest(r)
			}
		}
	} else {
		f, err := os.Open(filename)
		if err != nil {
			return "", err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", err
		}
		defer gz.Close()
		r := tar.NewReader(gz)
		for {
			h, err := r.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			if path.Base(h.Name) == PackageManifestName {
				return digest(r)
			}
		}
	}
	return "", errors.New("发布包缺少包清单")
}

func ArchiveExpandedSize(filename string) (int64, error) {
	var total int64
	add := func(size int64) error {
		if size < 0 || size > maxExtractedFile || total > maxExtractedSize-size {
			return errors.New("压缩包展开大小超过限制")
		}
		total += size
		return nil
	}
	if strings.HasSuffix(filename, ".zip") {
		z, err := zip.OpenReader(filename)
		if err != nil {
			return 0, err
		}
		defer z.Close()
		for _, f := range z.File {
			if !f.FileInfo().IsDir() {
				if err := add(int64(f.UncompressedSize64)); err != nil {
					return 0, err
				}
			}
		}
	} else {
		f, err := os.Open(filename)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return 0, err
		}
		defer gz.Close()
		r := tar.NewReader(gz)
		for {
			h, err := r.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return 0, err
			}
			if err := add(h.Size); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}
