//go:build e2e

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hyb2desktop/core"
)

// TestOverlapBenchmark runs the same samples twice through the real
// scheduler, with and without the early start of the next sample, and
// reports the wall time of each batch.
//
//	HYB2_BENCH_DATA=<dir with *.fastq.gz and their <prefix>.fasta> \
//	  CGO_ENABLED=0 go test -tags e2e -run TestOverlapBenchmark -timeout 60m -v .
func TestOverlapBenchmark(t *testing.T) {
	data := os.Getenv("HYB2_BENCH_DATA")
	if data == "" {
		t.Skip("HYB2_BENCH_DATA is not set")
	}
	reads, _ := filepath.Glob(filepath.Join(data, "*.fastq.gz"))
	if len(reads) < 2 {
		t.Fatalf("need at least two .fastq.gz in %s", data)
	}
	root := repoRoot()
	python := filepath.Join(root, "desktop", "resources", "native", "python", "bin", "python3")
	defer func() { core.EarlyStart = true }()
	order := []bool{false, true}
	if os.Getenv("HYB2_BENCH_EARLY_FIRST") != "" {
		order = []bool{true, false}
	}
	prefs := filepath.Join(t.TempDir(), "preferences.json") // one speed history for both rounds
	for _, early := range order {
		core.EarlyStart = early
		dir := t.TempDir()
		rt, err := core.NewRuntime(core.Bridge{Root: root, Python: python}, filepath.Join(dir, "cache"))
		if err != nil {
			t.Fatal(err)
		}
		service := core.NewService(rt, core.ServiceOptions{PreferencesPath: prefs})
		project := filepath.Join(dir, "bench.hyb2proj")
		if _, err := service.CreateProject(project, "Bench", ""); err != nil {
			t.Fatal(err)
		}
		var rows []core.Obj
		for _, r := range reads {
			name := strings.TrimSuffix(filepath.Base(r), ".fastq.gz")
			ref := filepath.Join(data, strings.SplitN(name, "_", 2)[0]+".fasta")
			rows = append(rows, core.Obj{"path": r, "name": name, "reference": ref})
		}
		imported, err := service.ProjectOp(project, "import_sample_rows", core.Obj{"rows": rows})
		if err != nil {
			t.Fatal(err)
		}
		samples := imported["samples"].([]any)
		var inputs []core.PipelineInput
		for _, s := range samples {
			reads := s.(map[string]any)["reads"].(map[string]any)
			inputs = append(inputs, core.PipelineInput{Format: reads["format"].(string), InputBytes: int64(reads["size_bytes"].(float64))})
		}
		estimate := service.EstimatePipelines(inputs)
		started := time.Now()
		var ids []string
		for _, s := range samples {
			id := s.(map[string]any)["id"].(string)
			snap, err := service.StartTask(core.TaskStartRequest{ProjectPath: project, Kind: "sample_pipeline", SampleID: &id})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, snap["task_id"].(string))
		}
		var maxRunning int
		for {
			tasks, err := service.ListTasks(project)
			if err != nil {
				t.Fatal(err)
			}
			running, done := 0, 0
			for _, task := range tasks {
				switch task["status"] {
				case "running":
					running++
				case "succeeded":
					done++
				case "failed", "cancelled", "interrupted":
					t.Fatalf("task %v: %v", task["task_id"], task)
				}
			}
			maxRunning = max(maxRunning, running)
			if elapsed := time.Since(started); elapsed > 30*time.Second && elapsed < 30*time.Second+300*time.Millisecond {
				for _, task := range tasks {
					if left, ok := task["seconds_left"]; ok {
						p, _ := task["progress"].(map[string]any)
						t.Logf("  at 30s: %v %v/%v %v: %vs left", task["task_id"], p["stage"], p["bytes_done"], p["bytes_total"], left)
					}
				}
			}
			if done == len(ids) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		wall := time.Since(started)
		t.Logf("early start %v: %d samples in %.1fs (at most %d running at once); estimated %.0fs beforehand (%d of %d measured)",
			early, len(ids), wall.Seconds(), maxRunning, estimate.Seconds, estimate.Measured, estimate.Samples)
		fmt.Fprintf(os.Stderr, "BENCH early=%v wall=%.1f\n", early, wall.Seconds())
		for _, id := range ids {
			t.Logf("  %s: %s", id, stageTimes(filepath.Join(project, "runs", id, "stderr.log"), started))
		}
		service.Shutdown()
	}
}

// stageTimes is "stage@start+duration ..." from a task's progress events,
// in seconds since the batch started.
func stageTimes(stderr string, batch time.Time) string {
	f, err := os.Open(stderr)
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	type event struct {
		Stage, Detail string
		At            time.Time
	}
	var events []event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var e struct {
			Type      string `json:"type"`
			Stage     string `json:"stage"`
			Detail    string `json:"detail"`
			UpdatedAt string `json:"updated_at"`
		}
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.Type != "progress" {
			continue
		}
		at, _ := time.Parse(time.RFC3339, e.UpdatedAt)
		key := e.Stage
		if strings.HasPrefix(e.Detail, "waiting") {
			key = "wait"
		}
		if len(events) == 0 || events[len(events)-1].Stage != key {
			events = append(events, event{key, e.Detail, at})
		}
	}
	var out []string
	for i, e := range events {
		end := e.At
		if i+1 < len(events) {
			end = events[i+1].At
		}
		out = append(out, fmt.Sprintf("%s@%.0f+%.0f", e.Stage, e.At.Sub(batch).Seconds(), end.Sub(e.At).Seconds()))
	}
	return strings.Join(out, " ")
}
