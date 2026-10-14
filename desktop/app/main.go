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
	"hyb2desktop/views"
)

// repoRoot is the source checkout, for development builds without a
// bundled runtime (desktop/app/main.go -> repository root).
func repoRoot() string {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// startRuntime resolves the bundled runtime and builds the service; the
// service works without a runtime (calls then fail with its error).
func startRuntime() (*core.Service, string, error) {
	resources, _ := mygo.App.Path(mygo.PathResources)
	cache, _ := mygo.App.Path(mygo.PathCache)
	userData, _ := mygo.App.Path(mygo.PathUserData)
	options := core.ServiceOptions{PreferencesPath: filepath.Join(userData, "preferences.json")}
	bridge, err := core.ResolveBridge(resources, mygo.IsDev() || !mygo.App.IsPackaged(), repoRoot())
	if err != nil {
		return core.NewService(nil, options), "", err
	}
	rt, err := core.NewRuntime(*bridge, cache)
	if err != nil {
		return core.NewService(nil, options), "", err
	}
	return core.NewService(rt, options), bridge.Python, nil
}

// detectSummary is one line about the runtime for the start page.
func detectSummary(response core.Obj) string {
	result, _ := response["result"].(map[string]any)
	platform, _ := result["platform"].(map[string]any)
	return fmt.Sprintf("Runtime: %v %v · Python %v", platform["os"], platform["architecture"], platform["python"])
}

func main() {
	service, _, runtimeErr := startRuntime()
	userData, _ := mygo.App.Path(mygo.PathUserData)
	app := views.New(service, nil, userData)
	if runtimeErr != nil {
		app.SetRuntimeStatus("", runtimeErr.Error())
	}
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "HYB2 Desktop",
			Width:     1280,
			Height:    820,
			MinWidth:  900,
			MinHeight: 600,
			StateKey:  "main",
			Content:   ui.View(app.View),
		})
		app.SetPoster(win)
		if runtimeErr == nil {
			// Detect the runtime, then compile the CPLfold kernels in the
			// background so the first fold does not wait for them.
			go func() {
				detected, err := service.Detect()
				win.Update(func() {
					if err != nil {
						app.SetRuntimeStatus("", err.Error())
						return
					}
					app.SetRuntimeStatus(detectSummary(detected), "")
				})
				if err == nil {
					service.WarmUp()
				}
			}()
		}
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		app.Stop()
		service.Shutdown()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
