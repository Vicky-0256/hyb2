package views

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestExecutableFileFollowsGOOS(t *testing.T) {
	dir := t.TempDir()
	exe := writeFile(t, filepath.Join(dir, "mfold.exe"), 0o644)
	bare := writeFile(t, filepath.Join(dir, "mfold"), 0o755)
	t.Setenv("PATHEXT", "")
	setGOOS(t, "windows")
	if !executableFile(exe) || executableFile(bare) || executableFile(dir) {
		t.Fatalf("windows: exe %v, bare %v", executableFile(exe), executableFile(bare))
	}
	t.Setenv("PATHEXT", ".COM;.BAT")
	if executableFile(exe) {
		t.Fatal("windows: .exe outside PATHEXT")
	}
	setGOOS(t, "linux")
	if executableFile(exe) || !executableFile(bare) {
		t.Fatalf("linux: exe %v, bare %v", executableFile(exe), executableFile(bare))
	}
}

// writeFile writes a file of the given mode and returns its path.
func writeFile(t *testing.T, path string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// The RNAduplex kernel runs in the bundled ViennaRNA: CPLfold and CPLcofold
// send only its name (no program path), with PATH empty, and the engines
// without a support kernel send nothing.
func TestStructureDuplexKernelNeedsNoProgram(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	t.Setenv("PATH", t.TempDir())
	p.gene, p.mode, p.windows = africaGene, "windows", "3001-3400"
	p.engine, p.kernel = engineCPLfold, "duplex"
	pl := p.plan()
	params := pl.req.Parameters.(map[string]any)
	if len(pl.problems) != 0 || params["support_kernel"] != "duplex" {
		t.Fatalf("cplfold %q %v", pl.problems, params)
	}
	for key := range params {
		if key == "hybrid_min" || key == "hybridMin" { // legacy-name: parameter removed with RNAduplex
			t.Fatalf("cplfold sends %s: %v", key, params)
		}
	}
	for _, engine := range []string{engineVienna, engineComrades, engineIRIS} {
		p.engine = engine
		p.engineChanged()
		pl := p.plan()
		if params := pl.req.Parameters.(map[string]any); params["support_kernel"] != nil {
			t.Fatalf("%s parameters %v", engine, params)
		}
	}
}
