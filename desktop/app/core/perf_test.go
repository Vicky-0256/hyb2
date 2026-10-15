package core

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(b)) }

func TestSampleEstimatePriorAndMeasured(t *testing.T) {
	// No history: the built-in rate, scaled by CPUs.
	seconds, measured := SampleEstimate(nil, "fastq.gz", 2_000_000_000, 10)
	if measured || !near(seconds, 940) {
		t.Fatalf("prior %v %v", seconds, measured)
	}
	if half, _ := SampleEstimate(nil, "fastq.gz", 2_000_000_000, 5); !near(half, 940*math.Pow(2, perfCPUExponent)) {
		t.Fatalf("5 cpus %v", half)
	}
	// Measured runs of the same format win (median; other formats ignored).
	history := []PerfRecord{
		{Kind: "sample_pipeline", Format: "fastq.gz", InputBytes: 100e6, CPU: 10, Seconds: 30},
		{Kind: "sample_pipeline", Format: "fastq.gz", InputBytes: 100e6, CPU: 10, Seconds: 50},
		{Kind: "sample_pipeline", Format: "fastq.gz", InputBytes: 200e6, CPU: 10, Seconds: 80},
		{Kind: "sample_pipeline", Format: "fastq", InputBytes: 100e6, CPU: 10, Seconds: 999},
	}
	seconds, measured = SampleEstimate(history, "fastq.gz", 1e9, 10)
	if !measured || !near(seconds, 400) {
		t.Fatalf("measured %v %v", seconds, measured)
	}
	// A run measured with 5 CPUs counts as faster with 10.
	seconds, _ = SampleEstimate([]PerfRecord{{Kind: "sample_pipeline", Format: "hyb", InputBytes: 1e6, CPU: 5, Seconds: 10}}, "hyb", 1e6, 10)
	if !near(seconds, 10*math.Pow(0.5, perfCPUExponent)) {
		t.Fatalf("scaled %v", seconds)
	}
	e := EstimatePipelines(history, []PipelineInput{{"fastq.gz", 1e9}, {"sam", 1e9}}, 10)
	if e.Samples != 2 || e.Measured != 1 || !near(e.Seconds, 400+20) {
		t.Fatalf("batch %+v", e)
	}
}

func TestRemainingSeconds(t *testing.T) {
	work := map[string]any{"format": "fastq.gz", "input_bytes": 1e9, "cpu": 10.0}
	total, _ := SampleEstimate(nil, "fastq.gz", 1e9, 10)
	cases := []struct {
		progress Obj
		want     float64
		ok       bool
	}{
		{Obj{"stage": "check", "work": work}, total, true},
		{Obj{"stage": "reads", "work": work, "bytes_done": 50.0, "bytes_total": 100.0}, total * (1 - 0.075), true},
		{Obj{"stage": "map", "work": work, "bytes_done": 100.0, "bytes_total": 100.0}, total * 0.03, true},
		{Obj{"stage": "map", "detail": "waiting: another sample is mapping", "work": work}, 0, false},
		{Obj{"stage": "map"}, 0, false},
	}
	for i, c := range cases {
		got, ok := remainingSeconds(nil, c.progress, 4)
		if ok != c.ok || (ok && !near(got, c.want)) {
			t.Errorf("case %d: %v %v, want %v %v", i, got, ok, c.want, c.ok)
		}
	}
	if left, ok := remainingSeconds(nil, Obj{"stage": "index", "work": work}, 10); !ok || left > total*0.03 {
		t.Fatalf("after map %v", left)
	}
}

func TestPerfHistoryFile(t *testing.T) {
	dir := t.TempDir()
	path := perfPath(filepath.Join(dir, PrefsFileName))
	for i := 0; i < perfKeep+5; i++ {
		if err := AppendPerf(path, PerfRecord{Kind: "sample_pipeline", Format: "fastq", InputBytes: int64(i + 1), Seconds: 1}); err != nil {
			t.Fatal(err)
		}
	}
	got := LoadPerf(path)
	if len(got) != perfKeep || got[0].InputBytes != 6 {
		t.Fatalf("kept %d, first %+v", len(got), got[0])
	}
	// Nothing measured is not recorded.
	_ = AppendPerf(path, PerfRecord{Kind: "sample_pipeline", Format: "fastq"})
	if len(LoadPerf(path)) != perfKeep {
		t.Fatal("empty record stored")
	}
	run := filepath.Join(dir, "run")
	os.MkdirAll(run, 0o755)
	os.WriteFile(filepath.Join(run, "result.json"), []byte(`{"ok":true,"performance":{"format":"fastq.gz","input_bytes":160000000,"cpu":10,"seconds":76.5,"stage_seconds":{"map":66}}}`), 0o644)
	record, ok := perfFromResult(run)
	if !ok || record.Format != "fastq.gz" || record.InputBytes != 160000000 || record.CPU != 10 || record.Seconds != 76.5 {
		t.Fatalf("%+v %v", record, ok)
	}
	os.WriteFile(filepath.Join(run, "result.json"), []byte(`{"ok":false,"performance":{"format":"fastq","input_bytes":1,"seconds":1}}`), 0o644)
	if _, ok := perfFromResult(run); ok {
		t.Fatal("a failed run was recorded")
	}
}
