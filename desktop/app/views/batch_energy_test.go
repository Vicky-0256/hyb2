package views

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"hyb2desktop/core"
)

func TestBatchEnergyStartsTaskAndSavesOutputs(t *testing.T) {
	a, f, p, tt := openExplore(t)
	a.Navigate("explore", map[string]any{"tab": "energies"})
	settle(a, tt)
	if p.tab != tabEnergies {
		t.Fatalf("tab %d", p.tab)
	}
	for _, text := range []string{"HYB binding energies", "Sample", "Engine", "Records", "RNAcofold (ViennaRNA), as the HYB2 pipeline", "Calculate binding energies"} {
		if !tt.HasText(text) {
			t.Fatalf("binding energies lack %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "explore-energies")

	// CPLcofold on the rows matching the Interactions filters
	p.be.engine, p.be.records = batchEngines[1][1], batchRecordLabels[1]
	p.ix.applied.minReads = 3
	tt.Frame()
	if !tt.HasText("Records: ≥ 3 reads.") && !tt.HasText(batchEngines[1][2]+" Records: ≥ 3 reads.") {
		t.Fatalf("filters not described: %q", tt.Texts())
	}
	if err := tt.Click("Calculate binding energies"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	f.mu.Lock()
	started := append([]core.TaskStartRequest(nil), f.started...)
	f.mu.Unlock()
	if len(started) != 1 {
		t.Fatalf("started %d tasks", len(started))
	}
	req := started[0]
	params, _ := json.Marshal(req.Parameters)
	if req.Kind != "batch_energy" || req.SampleID == nil || *req.SampleID != p.ids[0] ||
		string(params) != `{"engine":"cplcofold","filters":{"min_reads":3},"records":"filtered"}` {
		t.Fatalf("request %+v %s", req, params)
	}
	// the running task shows with Cancel, and a second run waits for it
	if !tt.HasText("Cancel") || a.TaskLabel(a.TaskList()[0]) != "Binding energies · "+a.Project.Sample(p.ids[0]).Label()+" · cplcofold" {
		t.Fatalf("running task: %q", tt.Texts())
	}

	// a finished task offers its files
	dir := t.TempDir()
	hyb := filepath.Join(dir, "energies.hyb")
	zipPath := filepath.Join(dir, "hyb2-energies-cplcofold.zip")
	_ = os.WriteFile(hyb, []byte("1_1\tACGU\t-7.10\tA\n"), 0o644)
	_ = os.WriteFile(zipPath, []byte("PK"), 0o644)
	result, _ := json.Marshal(map[string]any{"message": "1 energies updated with CPLcofold, pseudoknots off in 2 s; 0 valid rows unchanged.",
		"outputs": map[string]string{"hyb": hyb, "zip": zipPath}, "summary": map[string]any{"engine": "cplcofold", "updated": 1}})
	_ = os.WriteFile(filepath.Join(dir, "result.json"), result, 0o644)
	f.mu.Lock()
	f.tasks[0]["status"], f.tasks[0]["output"], f.tasks[0]["finished_at"] = "succeeded", hyb, "2026-10-14T10:05:00Z"
	f.mu.Unlock()
	a.RefreshTasks()
	settle(a, tt)
	settle(a, tt)
	for _, text := range []string{"Save updated HYB…", "Save HYB + audit ZIP…", "1 energies updated with CPLcofold"} {
		if !tt.HasText(text) {
			t.Fatalf("result lacks %q: %q", text, tt.Texts())
		}
	}
	asked := pickSaveInto(t, t.TempDir())
	if err := tt.Click("Save updated HYB…"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	if got := readExport(t, asked); got != "1_1\tACGU\t-7.10\tA\n" || filepath.Base((*asked)[0]) != "hyb2-energies-cplcofold.hyb" {
		t.Fatalf("saved %q as %q", got, *asked)
	}
	if err := tt.Click("Save HYB + audit ZIP…"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	if got := readExport(t, asked); got != "PK" || filepath.Base((*asked)[1]) != "hyb2-energies-cplcofold.zip" {
		t.Fatalf("saved %q as %v", got, fmt.Sprint(*asked))
	}
}

// The run-time estimate's record count is kept per project, sample and
// index: a rebuilt index counts again.
func TestBatchEnergyCountsFollowTheIndex(t *testing.T) {
	a, _, p, tt := openExplore(t)
	a.Navigate("explore", map[string]any{"tab": "energies"})
	settle(a, tt)
	old := p.batchCountKey(p.ids[0])
	if _, ok := p.be.counts[old]; !ok || len(p.be.counts) != 1 {
		t.Fatalf("counts %v", p.be.counts)
	}
	s := a.Project.Sample(p.ids[0])
	stage := s.Stages["index"]
	stage.UpdatedAt += "-rebuilt"
	s.Stages["index"] = stage
	settle(a, tt)
	if _, ok := p.be.counts[p.batchCountKey(p.ids[0])]; !ok || len(p.be.counts) != 2 {
		t.Fatalf("a rebuilt index was not counted again: %v", p.be.counts)
	}
	p.be.countsFor = "/projects/other.hyb2proj"
	p.loadBatchInputs(p.ids[0])
	if len(p.be.counts) != 0 {
		t.Fatalf("another project's counts kept: %v", p.be.counts)
	}
}
