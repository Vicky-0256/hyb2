package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// A valid gzip stream whose payload continues past the tar end marker (as
// with a tar padded to a record size) is hashed to its end.
func TestUnpackRuntimeHashesTheTail(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "a", Mode: 0o644, Typeflag: tar.TypeReg, Size: 1})
	tw.Write([]byte("x"))
	tw.Close()
	pad := make([]byte, 64<<10)
	rand.Read(pad) // incompressible padding after the end-of-archive blocks
	gz.Write(pad)
	gz.Close()
	path := filepath.Join(dir, RuntimeArchive)
	os.WriteFile(path, buf.Bytes(), 0o644)
	sum := sha256.Sum256(buf.Bytes())
	digest := hex.EncodeToString(sum[:])
	if err := extractVerified(path, digest, filepath.Join(dir, "out")); err != nil {
		t.Fatalf("archive with correct SHA-256 rejected: %v", err)
	}
}

// Zero padding after the gzip stream is hashed, not read as another stream.
func TestUnpackRuntimeAcceptsPaddingAfterGzip(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "a", Mode: 0o644, Typeflag: tar.TypeReg, Size: 1})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	buf.Write(make([]byte, 512))
	path := filepath.Join(dir, RuntimeArchive)
	os.WriteFile(path, buf.Bytes(), 0o644)
	sum := sha256.Sum256(buf.Bytes())
	if err := extractVerified(path, hex.EncodeToString(sum[:]), filepath.Join(dir, "out")); err != nil {
		t.Fatalf("archive with zero padding rejected: %v", err)
	}
}

// Chained in-archive symlinks, each lexically inside dest, would resolve
// outside it once created: they are refused, and nothing lands outside.
func TestUnpackRuntimeRejectsChainedLinks(t *testing.T) {
	dir := t.TempDir()
	path, digest := makeArchive(t, dir, []entry{
		{name: "d", link: "."},
		{name: "d/e", link: ".."},
		{name: "e/f/g", link: "x"},
	})
	dest := filepath.Join(dir, "unpack", "native")
	if err := extractVerified(path, digest, dest); err == nil {
		t.Fatal("chained links accepted")
	}
	if isDir(filepath.Join(dir, "unpack", "f")) {
		t.Fatalf("extraction created %s outside dest", filepath.Join(dir, "unpack", "f"))
	}
	if real, err := filepath.EvalSymlinks(filepath.Join(dest, "e")); err == nil {
		t.Fatalf("dest/e resolves outside dest to %s", real)
	}
}
