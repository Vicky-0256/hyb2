package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/views"
)

// immediate runs posted work at once; the test drives frames itself.
type immediate struct{}

func (immediate) Update(fn func()) { fn() }

// TestRealStackHeadless drives the native views against the real Service
// and bridge: create a project, add a .hyb sample, index it as a task, and
// see the sample become ready.
func TestRealStackHeadless(t *testing.T) {
	root := repoRoot()
	python := filepath.Join(root, "desktop", "resources", "native", "python", "bin", "python3")
	if _, err := os.Stat(python); err != nil {
		t.Skip("staged runtime not available")
	}
	rt, err := core.NewRuntime(core.Bridge{Root: root, Python: python}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := core.NewService(rt, core.ServiceOptions{PreferencesPath: filepath.Join(t.TempDir(), "preferences.json")})
	t.Cleanup(service.Shutdown)

	project := filepath.Join(t.TempDir(), "study.hyb2proj")
	app := views.New(service, immediate{}, t.TempDir())
	tt := ui.NewTester(app.View, 1280, 820)
	if _, err := service.CreateProject(project, "Headless", ""); err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(root, "data", "Zika_18S_formatted.fasta")
	if _, err := service.ProjectOp(project, "add_reference", core.Obj{"source": reference, "mode": "linked"}); err != nil {
		t.Fatal(err)
	}
	hyb := filepath.Join(root, "pipeline", "tests", "golden", "testData.hyb")
	added, err := service.ProjectOp(project, "add_sample", core.Obj{"source": hyb, "name": "T1", "condition": "test",
		"reference": reference, "gene_id": "18S_rRNA", "mode": "linked"})
	if err != nil {
		t.Fatal(err)
	}
	sample := added["sample"].(map[string]any)["id"].(string)

	app.Open(project)
	waitFor(t, tt, func() bool { return app.Project != nil })
	if !tt.HasText("Headless") {
		t.Fatalf("texts %q", tt.Texts())
	}
	app.StartTask(core.TaskStartRequest{Kind: "analysis_index", SampleID: &sample}, "Index · T1", nil)
	waitFor(t, tt, func() bool { return len(app.Tasks) == 1 })
	// The app polls only with a window; poll by hand here.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		app.RefreshTasks()
		tt.Frame()
		done := false
		for _, task := range app.Tasks {
			done = !task.Active()
			if done && task.Status != "succeeded" {
				t.Fatalf("task %+v", task)
			}
		}
		if done {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	app.RefreshProject()
	waitFor(t, tt, func() bool { s := app.Project.Sample(sample); return s != nil && s.IsReady() })
	if keys := app.SelectionOptions(); len(keys) == 0 || keys[0].Key != "c:test" {
		t.Fatalf("selection options %+v", keys)
	}
}

func waitFor(t *testing.T, tt *ui.Tester, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
