package views

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// COMRADES › Folding program: mfold sets its fixed 37 °C and minimum
// loop 3, blocks the run until mfold is found, and sends fold_program.
func TestStructureComradesMfold(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HOME", dir)
	t.Setenv("HYB2_MFOLD", "")
	saved := toolPrefixes
	toolPrefixes = nil
	t.Cleanup(func() { toolPrefixes = saved })
	clear(mfoldCache.entries)
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 3000)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	stPick(t, tt, engineComrades, false)
	p.advancedOpen, p.mode, p.windows, p.temperature = true, "windows", "2800-3300", 25
	tt.Frame()
	if !tt.HasText("Fits the stems with ViennaRNA's constrained MFE, as the web.") {
		t.Fatalf("no folding program: %q", tt.Texts())
	}
	if err := tt.Click("mfold 3.6 (installed separately)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.temperature != 37 || int(p.minLoop) != 3 {
		t.Fatalf("mfold settings %g %g %q", p.temperature, p.minLoop, p.foldProgram)
	}
	all := strings.Join(tt.Texts(), "\n")
	for _, text := range []string{"mfold 3.6 was not found. Install it with: " + mfoldInstall, "COMRADES settings", "mfold · 37 °C · minimum loop 3"} {
		if !strings.Contains(all, text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	if pl := p.plan(); len(pl.problems) == 0 || !strings.Contains(strings.Join(pl.limits, " "), "each fitted with mfold") {
		t.Fatalf("plan %+v", pl)
	}
	// a found mfold lifts the block; a changed loop is a problem again
	exe := filepath.Join(dir, "mfold")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	clear(mfoldCache.entries)
	tt.Frame()
	if !tt.HasText("Using " + exe + ". mfold applies each stem as forced base pairs; it folds at 37 °C with minimum loop 3, and is slower than ViennaRNA (about 7 s per fold at 1,000 nt).") {
		t.Fatalf("mfold not found: %q", tt.Texts())
	}
	p.minLoop = 4
	if pl := p.plan(); !slices.Contains(pl.problems, "mfold's minimum hairpin loop is fixed at 3: set Minimum hairpin loop to 3, or fold with ViennaRNA.") {
		t.Fatalf("problems %q", pl.problems)
	}
	p.minLoop = 3
	if pl := p.plan(); len(pl.problems) != 0 {
		t.Fatalf("problems %q", pl.problems)
	}
	tt.Frame()
	if !tt.HasText("COMRADES(mfold), 37 °C, minimum loop 3") {
		t.Fatalf("no engine line: %q", tt.Texts())
	}
	if err := tt.Click("Run fold"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || started[0].Parameters.(map[string]any)["fold_program"] != "mfold" {
		t.Fatalf("started %+v", started)
	}
	// the defaults are ViennaRNA again
	p.foldProgram = ""
	if p.comradesProgram() != foldViennaRNA || p.comradesName() != "COMRADES(ViennaRNA)" {
		t.Fatal(p.comradesName())
	}
	if !stIsComrades("COMRADES(mfold)") || stIsComrades("CPLfold") {
		t.Fatal("stIsComrades")
	}
}

// mfold is found on the Preferences path, else HYB2_MFOLD, else a conda env.
func TestFindMfold(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", filepath.Join(dir, "nothing"))
	t.Setenv("HOME", dir)
	t.Setenv("HYB2_MFOLD", "")
	saved := toolPrefixes
	toolPrefixes = []string{"~/miniforge3"}
	t.Cleanup(func() { toolPrefixes = saved })
	if _, problem := findMfold(""); !strings.Contains(problem, mfoldInstall) {
		t.Fatal(problem)
	}
	env := filepath.Join(dir, "miniforge3", "envs", "mfold", "bin", "mfold")
	os.MkdirAll(filepath.Dir(env), 0o755)
	os.WriteFile(env, []byte("#!/bin/sh\n"), 0o755)
	if path, _ := findMfold(""); path != env {
		t.Fatalf("conda env: %q", path)
	}
	t.Setenv("HYB2_MFOLD", filepath.Join(dir, "gone"))
	if _, problem := findMfold(""); !strings.Contains(problem, "HYB2_MFOLD") {
		t.Fatal(problem)
	}
	if path, _ := findMfold(env); path != env {
		t.Fatalf("preference: %q", path)
	}
	if _, problem := findMfold(filepath.Join(dir, "x")); !strings.Contains(problem, "(Preferences)") {
		t.Fatal(problem)
	}
}
