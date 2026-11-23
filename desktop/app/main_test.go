package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hyb2desktop/core"
)

func TestDetectSummary(t *testing.T) {
	got := detectSummary(map[string]any{"result": map[string]any{
		"platform": map[string]any{"os": "macos", "architecture": "arm64", "python": "3.11.17"}}})
	if got != "Runtime: macos arm64 · Python 3.11.17" {
		t.Fatalf("got %q", got)
	}
}

func TestPackedLaunchWithoutCache(t *testing.T) {
	t.Chdir(t.TempDir())
	resources := t.TempDir()
	// Once PackedRuntime finds an archive, fail before even trying to unpack it.
	data := []byte("not opened without a cache")
	if err := os.WriteFile(filepath.Join(resources, core.RuntimeArchive), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, core.RuntimeArchiveSHA256), []byte(fmt.Sprintf("%x\n", sha256.Sum256(data))), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRuntime(resources, ""); err == nil || !strings.Contains(err.Error(), "cache") {
		t.Errorf("expected a cache path error, got %v", err)
	}
	if _, err := os.Stat("runtime"); !os.IsNotExist(err) {
		t.Fatalf("launch touched ./runtime: %v", err)
	}
}
