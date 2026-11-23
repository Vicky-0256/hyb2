package views

// mfold 3.6 for COMRADES(mfold) (⑤ Structure › COMRADES › Folding program,
// desktop/runtime/mfold.py): found as the mfold preference (given to task
// processes as HYB2_MFOLD by core.Service), else HYB2_MFOLD, else PATH, else
// the usual conda and Homebrew locations, as mfold.find. It is free for
// academic use but not bundled; bioconda builds it for Linux and Intel
// macOS (Apple silicon runs it under Rosetta), not for Windows.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// mfoldInstall is the install command the app suggests (mfold.INSTALL).
const mfoldInstall = "conda create -n mfold -c conda-forge -c bioconda mfold"

// Measured mfold 3.6 MFE folds (random RNA, Rosetta on Apple silicon):
// 7.0 s at 1,000 nt, 47 s at 3,000 nt.
const mfoldRefNT, mfoldRefSeconds, mfoldTimeExponent = 1000, 7.0, 1.74

// setMfold takes the mfold preference from a preferences answer.
func (a *App) setMfold(got core.Obj) {
	value, _ := got["mfold"].(string)
	a.mfold = strings.TrimSpace(value)
}

// findMfold is the mfold the runs will use: pref (the preference), else
// HYB2_MFOLD, else PATH, else the usual install locations; a problem when
// none is found or a given path is not executable.
func findMfold(pref string) (string, string) {
	if pref = strings.TrimSpace(pref); pref != "" {
		if path := expandHome(pref); executableFile(path) {
			return path, ""
		}
		return "", "mfold is not an executable file at " + pref + " (Preferences): correct it in Preferences, or clear it to search the usual install locations."
	}
	if env := os.Getenv(core.MfoldEnv); env != "" {
		if path := expandHome(env); executableFile(path) {
			return path, ""
		}
		return "", "mfold is not an executable file at " + env + " (" + core.MfoldEnv + "): correct or unset it, or set the path in Preferences."
	}
	if path, err := exec.LookPath("mfold"); err == nil {
		return path, ""
	}
	for _, prefix := range toolPrefixes {
		root := expandHome(prefix)
		envs, _ := filepath.Glob(filepath.Join(root, "envs", "*", "bin", "mfold"))
		slices.Sort(envs)
		for _, path := range append([]string{filepath.Join(root, "bin", "mfold")}, envs...) {
			if executableFile(path) {
				return path, ""
			}
		}
	}
	if goos == "windows" {
		return "", "mfold 3.6 has no Windows build: fold with ViennaRNA, or run COMRADES(mfold) on macOS or Linux."
	}
	return "", "mfold 3.6 was not found. Install it with: " + mfoldInstall + " — or set its path in Preferences, or fold with ViennaRNA."
}

// mfoldCache keeps findMfold's answers for toolTTL: the Structure
// form asks every frame.
var mfoldCache = struct {
	sync.Mutex
	entries map[string]toolAnswer
}{entries: map[string]toolAnswer{}}

func cachedMfold(pref string) (string, string) {
	home, _ := os.UserHomeDir()
	key := strings.Join([]string{pref, os.Getenv(core.MfoldEnv), os.Getenv("PATH"), home, goos}, "\x00")
	c := &mfoldCache
	c.Lock()
	defer c.Unlock()
	now := time.Now()
	if e, ok := c.entries[key]; ok && now.Sub(e.at) < toolTTL {
		return e.path, e.problem
	}
	if len(c.entries) >= toolCacheMax {
		clear(c.entries)
	}
	path, problem := findMfold(pref)
	c.entries[key] = toolAnswer{path, problem, now}
	return path, problem
}

// appMfold is the mfold COMRADES(mfold) runs with (nil-safe).
func (a *App) appMfold() (string, string) {
	pref := ""
	if a != nil {
		pref = a.mfold
	}
	return cachedMfold(pref)
}

// mfoldStatus is the preference's status line for a typed path.
func mfoldStatus(typed string) (text string, bad bool) {
	path, problem := cachedMfold(typed)
	if path != "" {
		return "Using " + path + ".", false
	}
	if strings.TrimSpace(typed) != "" {
		return "mfold is not an executable file at " + strings.TrimSpace(typed) + ".", true
	}
	return problem, true
}

// mfoldPrefs is the Preferences section for mfold.
func (a *App) mfoldPrefs(c *ui.Context) {
	t := c.Theme()
	p := &a.prefs
	stored, _ := p.stored["mfold"].(string)
	ui.Text(c, "mfold (COMRADES folding program)").Bold()
	ui.Text(c, "Structure › COMRADES can fit the chimera stems with mfold 3.6 instead of ViennaRNA. mfold applies them as forced "+
		"base pairs (the hybrid-ss-min of OligoArrayAux ignores them). It is free for academic use but not bundled with HYB2. "+
		"Leave the path empty to search PATH and the usual conda and Homebrew locations.").FontSize(12).TextColor(t.TextMuted)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Path").Width(120)
		ui.TextInput(c.Key("pref-mfold"), &p.mfold).Placeholder("search automatically").Label("mfold path").Grow(1)
	})
	status, bad := mfoldStatus(p.mfold)
	color := t.TextMuted
	if bad {
		color = t.Danger
	}
	ui.Text(c, status).FontSize(12).TextColor(color)
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if ui.Button(c.Key("pref-mfold-search"), "Search automatically").Disabled(stored == "" && strings.TrimSpace(p.mfold) == "").Clicked() {
			p.mfold = ""
			a.savePrefs(core.Obj{"mfold": nil})
		}
		if ui.PrimaryButton(c.Key("pref-mfold-save"), "Save path").Disabled(strings.TrimSpace(p.mfold) == stored).Clicked() {
			a.savePrefs(core.Obj{"mfold": strings.TrimSpace(p.mfold)})
		}
	})
}
