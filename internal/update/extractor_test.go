package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	file, _ := writer.Create("dir/hello.txt")
	_, _ = file.Write([]byte("hello"))
	_ = writer.Close()

	dest := t.TempDir()
	if err := ExtractZip(buf.Bytes(), dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "dir", "hello.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("extracted content = %q, %v", data, err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	file, _ := writer.Create("../evil.txt")
	_, _ = file.Write([]byte("bad"))
	_ = writer.Close()

	if err := ExtractZip(buf.Bytes(), t.TempDir()); err == nil {
		t.Fatal("zip traversal was accepted")
	}
}

func TestExtractTarGz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	_ = writer.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0640, Size: 5, Typeflag: tar.TypeReg})
	_, _ = writer.Write([]byte("hello"))
	_ = writer.Close()
	_ = gz.Close()

	dest := t.TempDir()
	if err := ExtractTarGz(buf.Bytes(), dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("extracted content = %q, %v", data, err)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	_ = writer.WriteHeader(&tar.Header{Name: "../evil.txt", Mode: 0640, Size: 3, Typeflag: tar.TypeReg})
	_, _ = writer.Write([]byte("bad"))
	_ = writer.Close()
	_ = gz.Close()

	if err := ExtractTarGz(buf.Bytes(), t.TempDir()); err == nil {
		t.Fatal("tar traversal was accepted")
	}
}
