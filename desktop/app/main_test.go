package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// snapshot writes the tester's last frame to $HYB2_SNAPSHOT_DIR/<name>.png
// so a window can be reviewed without screen capture.
func snapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("HYB2_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestStartupStates(t *testing.T) {
	a := &app{checking: true}
	tt := ui.NewTester(a.view, 1100, 720)
	if !tt.HasText("Starting the bundled runtime…") {
		t.Fatalf("texts %q", tt.Texts())
	}
	a.checking, a.err = false, "Native runtime is not bundled."
	tt.Frame()
	if !tt.HasText("Runtime unavailable: Native runtime is not bundled.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	a.err = ""
	a.platform, a.probes = parseDetect(map[string]any{"result": map[string]any{
		"platform": map[string]any{"os": "macos", "architecture": "arm64", "python": "3.11.17"},
		"probes": []any{
			map[string]any{"id": "language.python", "status": "available", "summary": "Detector Python runtime"},
			map[string]any{"id": "tool.bowtie2", "status": "missing", "summary": "Bowtie2"},
		},
	}})
	a.python = "/Applications/HYB2 Desktop.app/Contents/Resources/native/python/bin/python3"
	tt.Frame()
	for _, text := range []string{"Runtime: macos arm64 · Python 3.11.17", "language.python", "missing"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "m0-runtime")
}
