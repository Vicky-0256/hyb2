// HYB2 Desktop: native MyGo shell over the bundled Python runtime
// (desktop/runtime/bridge.py). See MYGO_DESKTOP_PLAN.md.
package main

import (
	"fmt"
	"log"
	"path/filepath"
	goruntime "runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// probe is one runtime component reported by the bridge's `detect`.
type probe struct {
	ID, Status, Summary string
}

type app struct {
	runtime  *core.Runtime
	checking bool
	err      string
	platform string
	python   string
	probes   []probe
}

// repoRoot is the source checkout, for development builds without a
// bundled runtime (desktop/app/main.go -> repository root).
func repoRoot() string {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// start resolves the runtime and runs `detect` off the main thread.
func (a *app) start(win *mygo.Window) {
	a.checking = true
	go func() {
		resources, _ := mygo.App.Path(mygo.PathResources)
		cache, _ := mygo.App.Path(mygo.PathCache)
		bridge, err := core.ResolveBridge(resources, mygo.IsDev() || !mygo.App.IsPackaged(), repoRoot())
		var rt *core.Runtime
		var result map[string]any
		if err == nil {
			rt, err = core.NewRuntime(*bridge, cache)
		}
		if err == nil {
			result, err = rt.Call(core.Control, map[string]any{"operation": "detect"})
		}
		win.Update(func() {
			a.checking = false
			a.runtime = rt
			if err != nil {
				a.err = err.Error()
				return
			}
			a.python = bridge.Python
			a.platform, a.probes = parseDetect(result)
		})
	}()
}

func parseDetect(response map[string]any) (string, []probe) {
	result, _ := response["result"].(map[string]any)
	platform, _ := result["platform"].(map[string]any)
	label := fmt.Sprintf("%v %v · Python %v", platform["os"], platform["architecture"], platform["python"])
	var probes []probe
	list, _ := result["probes"].([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		id, _ := entry["id"].(string)
		status, _ := entry["status"].(string)
		summary, _ := entry["summary"].(string)
		probes = append(probes, probe{ID: id, Status: status, Summary: summary})
	}
	return label, probes
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Gap(12).Padding(24).Children(func() {
		ui.Text(c, "HYB2 Desktop").FontSize(26).Bold()
		ui.Text(c, "From raw chimeric reads to RNA structure, in one local project.").TextColor(t.TextMuted)
		switch {
		case a.checking:
			ui.Text(c, "Starting the bundled runtime…").TextColor(t.TextMuted)
		case a.err != "":
			ui.Text(c, "Runtime unavailable: "+a.err).TextColor(t.Danger)
		default:
			ui.Text(c, "Runtime: "+a.platform).Bold()
			ui.Text(c, a.python).TextColor(t.TextMuted).SingleLine()
			ui.Column(c).Gap(4).Children(func() {
				for _, p := range a.probes {
					color := t.TextMuted
					if p.Status == "available" {
						color = t.Success
					}
					ui.Row(c).Gap(10).Children(func() {
						ui.Text(c, p.Status).TextColor(color).Width(90)
						ui.Text(c, p.ID).Width(220)
						ui.Text(c, p.Summary).TextColor(t.TextMuted).SingleLine()
					})
				}
			})
		}
	})
}

func main() {
	a := &app{}
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "HYB2 Desktop",
			Width:     1100,
			Height:    720,
			MinWidth:  720,
			MinHeight: 480,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		a.start(win)
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if a.runtime != nil {
			a.runtime.Shutdown()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
