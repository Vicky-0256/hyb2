//go:build e2e

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hyb2desktop/core"
)

func TestZZRef(t *testing.T) {
	reads, ref, keep := os.Getenv("ZZ_READS"), os.Getenv("ZZ_REF"), os.Getenv("ZZ_KEEP")
	root := repoRoot()
	python := filepath.Join(root, "desktop", "resources", "native", "python", "bin", "python3")
	dir := t.TempDir()
	rt, err := core.NewRuntime(core.Bridge{Root: root, Python: python}, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	service := core.NewService(rt, core.ServiceOptions{PreferencesPath: filepath.Join(dir, "preferences.json")})
	defer service.Shutdown()
	project := filepath.Join(dir, "ref.hyb2proj")
	if _, err := service.CreateProject(project, "Ref", ""); err != nil {
		t.Fatal(err)
	}
	imported, err := service.ProjectOp(project, "import_sample_rows", core.Obj{"rows": []core.Obj{{"path": reads, "name": "test", "reference": ref}}})
	if err != nil {
		t.Fatal(err)
	}
	id := imported["samples"].([]any)[0].(map[string]any)["id"].(string)
	if _, err := service.StartTask(core.TaskStartRequest{ProjectPath: project, Kind: "sample_pipeline", SampleID: &id}); err != nil {
		t.Fatal(err)
	}
	for {
		tasks, _ := service.ListTasks(project)
		if len(tasks) > 0 && tasks[0]["status"] != "running" && tasks[0]["status"] != "queued" {
			t.Logf("status %v %v", tasks[0]["status"], tasks[0]["error"])
			break
		}
		time.Sleep(time.Second)
	}
	files, _ := filepath.Glob(filepath.Join(project, "samples", "*", "*"))
	for _, m := range files {
		if filepath.Ext(m) == ".sqlite" {
			continue
		}
		in, _ := os.Open(m)
		out, _ := os.Create(filepath.Join(keep, filepath.Base(m)))
		io.Copy(out, in)
		in.Close()
		out.Close()
	}
}
