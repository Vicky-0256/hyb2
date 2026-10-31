package core

import (
	"slices"
	"testing"
)

func TestBatchEnergyRequestsAreValidated(t *testing.T) {
	if !slices.Contains(TaskKinds, "batch_energy") || idPrefix("batch_energy") != "batch-energy-" {
		t.Fatal("batch_energy is not an allowed kind")
	}
	run := "/p/runs/a"
	good := TaskStartRequest{ProjectPath: "/p", Kind: "batch_energy", SampleID: ptr("sample-1"), Workers: ptr(uint32(2)),
		Parameters: map[string]any{"engine": "cplcofold", "records": "filtered", "filters": map[string]any{"min_reads": 2}}}
	claim, err := ClaimFor(good, big())
	if err != nil || claim != (Budget{2, 2048}) {
		t.Fatalf("claim %v %v", claim, err)
	}
	if c, _ := ClaimFor(TaskStartRequest{Kind: "batch_energy"}, big()); c != (Budget{4, 2048}) {
		t.Fatalf("default claim %v", c)
	}
	payload, err := TaskPayload(good, "/p", "a", run, Budget{3, 2048})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, payload, `{"operation":"batch_energy_run","project_path":"/p","sample_id":"sample-1",
		"parameters":{"engine":"cplcofold","records":"filtered","filters":{"min_reads":2}},"workers":2,"task_id":"a","run_dir":"/p/runs/a"}`)
	// workers default to the cpus granted
	noWorkers := good
	noWorkers.Workers = nil
	if payload, _ := TaskPayload(noWorkers, "/p", "a", run, Budget{3, 2048}); payload["workers"] != uint32(3) {
		t.Fatalf("workers %v", payload["workers"])
	}
	for i, edit := range []func(*TaskStartRequest){
		func(r *TaskStartRequest) { r.SampleID = nil },
		func(r *TaskStartRequest) { r.SampleID = ptr("../x") },
		func(r *TaskStartRequest) { r.Parameters = "rnacofold" },
	} {
		request := good
		edit(&request)
		if _, err := TaskPayload(request, "/p", "a", run, big()); err == nil {
			t.Fatalf("bad request %d accepted", i)
		}
	}
}
