package core

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(b)) }

func TestSampleEstimatePriorAndMeasured(t *testing.T) {
	// No history: the built-in rate, scaled by CPUs.
	seconds, measured := SampleEstimate(nil, "fastq.gz", 2_000_000_000, 10)
	if measured || !near(seconds, sampleOverhead+940) {
		t.Fatalf("prior %v %v", seconds, measured)
	}
	if half, _ := SampleEstimate(nil, "fastq.gz", 2_000_000_000, 5); !near(half, sampleOverhead+940*math.Pow(2, perfCPUExponent)) {
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
	if !measured || !near(seconds, sampleOverhead+(80-sampleOverhead)*5) { // median rate: the 200 MB run
		t.Fatalf("measured %v %v", seconds, measured)
	}
	// A run measured with 5 CPUs counts as faster with 10.
	seconds, _ = SampleEstimate([]PerfRecord{{Kind: "sample_pipeline", Format: "hyb", InputBytes: 1e6, CPU: 5, Seconds: 10}}, "hyb", 1e6, 10)
	if !near(seconds, sampleOverhead+(10-sampleOverhead)*math.Pow(0.5, perfCPUExponent)) {
		t.Fatalf("scaled %v", seconds)
	}
	e := EstimatePipelines(history, []PipelineInput{{"fastq.gz", 1e9}, {"sam", 1e9}}, 10)
	if e.Samples != 2 || e.Measured != 1 || !near(e.Seconds, sampleOverhead+(80-sampleOverhead)*5+sampleOverhead+20) {
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
		got, ok := remainingSeconds(nil, c.progress, 4, time.Now())
		if ok != c.ok || (ok && !near(got, c.want)) {
			t.Errorf("case %d: %v %v, want %v %v", i, got, ok, c.want, c.ok)
		}
	}
	if left, ok := remainingSeconds(nil, Obj{"stage": "index", "work": work}, 10, time.Now()); !ok || left > total*0.03 {
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
	record, ok := perfFromResult("sample_pipeline", run)
	if !ok || record.Format != "fastq.gz" || record.InputBytes != 160000000 || record.CPU != 10 || record.Seconds != 76.5 {
		t.Fatalf("%+v %v", record, ok)
	}
	os.WriteFile(filepath.Join(run, "result.json"), []byte(`{"ok":false,"performance":{"format":"fastq","input_bytes":1,"seconds":1}}`), 0o644)
	if _, ok := perfFromResult("sample_pipeline", run); ok {
		t.Fatal("a failed run was recorded")
	}
}

// beam100 is a guided, pseudoknot-free fold at beam 100.
func beam100(workers int, lengths ...int) FoldJob {
	return FoldJob{Lengths: lengths, Beam: 100, Workers: workers, Guided: true}
}

func TestEstimateFold(t *testing.T) {
	// Built-in rate: the whole 10,807-nt genome at beam 100 with 735,000
	// chimeras took 155 s on an M1 Pro (bonus matrix 43 s, fold 112 s).
	e := EstimateFold(nil, beam100(1, 10807))
	if e.Measured || e.Seconds < 130 || e.Seconds > 175 {
		t.Fatalf("genome %+v", e)
	}
	// Windows spread over workers, never faster than the largest window.
	one := EstimateFold(nil, beam100(1, 3000, 3000, 3000, 3000)).Seconds - foldOverhead
	four := EstimateFold(nil, beam100(4, 3000, 3000, 3000, 3000)).Seconds - foldOverhead
	if !near(one, 4*four) {
		t.Fatalf("parallel %v vs %v", one, four)
	}
	if got := EstimateFold(nil, beam100(8, 5000, 100)).Seconds - foldOverhead; !near(got, EstimateFold(nil, beam100(1, 5000)).Seconds-foldOverhead) {
		t.Fatalf("largest window bound %v", got)
	}
	// Measured folds replace the built-in rate (seconds per unit of work).
	job := beam100(1, 2000)
	history := []PerfRecord{{Kind: "structure", Units: FoldUnits(job), Work: foldWork(job), WorkModel: foldWorkModel, Parallel: 1, Seconds: 10}}
	e = EstimateFold(history, job)
	if !e.Measured || !near(e.Seconds, 10+foldOverhead) {
		t.Fatalf("measured %+v", e)
	}
	// A rate charged by an older model (no WorkModel) does not calibrate.
	old := []PerfRecord{{Kind: "structure", Units: 1, Work: 1, Parallel: 1, Seconds: 500}}
	if e := EstimateFold(old, job); e.Measured {
		t.Fatalf("an old record calibrated %+v", e)
	}
}

// TestEstimateFoldNearMeasured: the built-in model against folds timed on
// an Apple M1 Pro (10 CPUs, one core per fold, kernels already compiled).
func TestEstimateFoldNearMeasured(t *testing.T) {
	for _, c := range []struct {
		job     FoldJob
		seconds float64 // measured, with foldOverhead
	}{
		// whole ZIKV, beam 20, Livefire1: bonus matrix ~38 s + fold 71 s
		{FoldJob{Lengths: []int{10807}, Beam: 20, Workers: 1, Guided: true}, 112},
		// the same with pseudoknots: fold 94 s
		{FoldJob{Lengths: []int{10807}, Beam: 20, Workers: 1, Pseudoknot: true, Guided: true}, 135},
		// whole ZIKV at beam 100: 155 s
		{beam100(1, 10807), 158},
		// sequence only, beam 20
		{FoldJob{Lengths: []int{4000}, Beam: 20, Workers: 1}, 13},
		{FoldJob{Lengths: []int{4000}, Beam: 20, Workers: 1, Pseudoknot: true}, 17.3},
		{FoldJob{Lengths: []int{2000}, Beam: 100, Workers: 1}, 9.2},
		// a 601-nt window of Livefire1 (pseudoknots or not): under a second of folding
		{FoldJob{Lengths: []int{601}, Beam: 20, Workers: 1, Pseudoknot: true, Guided: true}, 4},
	} {
		got := EstimateFold(nil, c.job).Seconds
		if got < c.seconds/1.35 || got > c.seconds*1.35 {
			t.Errorf("%+v: estimate %.1f s, measured %.1f s", c.job, got, c.seconds)
		}
	}
	// The pseudoknot search and the bonus matrix cost more.
	plain := FoldJob{Lengths: []int{5000}, Beam: 20, Workers: 1}
	pk, guided := plain, plain
	pk.Pseudoknot, guided.Guided = true, true
	if !(FoldUnits(pk) > FoldUnits(plain) && FoldUnits(guided) > FoldUnits(plain)) {
		t.Fatal("pseudoknots or the bonus matrix cost nothing")
	}
}

func TestFoldRemaining(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	total := EstimateFold(nil, beam100(1, 10807)).Seconds
	work := map[string]any{"lengths": []any{10807.0}, "beam": 100.0, "workers": 1.0}
	left, ok := foldRemaining(nil, Obj{"stage": "fold", "work": work, "updated_at": now.Add(-30 * time.Second).Format(time.RFC3339)}, now)
	if !ok || !near(left, total-foldOverhead-30) {
		t.Fatalf("genome %v %v", left, ok)
	}
	if _, ok := foldRemaining(nil, Obj{"stage": "fold", "work": work, "updated_at": now.Add(-time.Hour).Format(time.RFC3339)}, now); ok {
		t.Fatal("overdue fold reported a time left")
	}
	windows := map[string]any{"lengths": []any{3000.0, 3000.0, 3000.0, 3000.0}, "beam": 100.0, "workers": 2.0}
	wtotal := EstimateFold(nil, beam100(2, 3000, 3000, 3000, 3000)).Seconds
	left, ok = foldRemaining(nil, Obj{"stage": "fold", "work": windows, "bytes_done": 1.0, "bytes_total": 4.0}, now)
	if !ok || !near(left, wtotal*0.75) {
		t.Fatalf("windows %v %v", left, ok)
	}
	if _, ok := foldRemaining(nil, Obj{"stage": "write", "work": windows}, now); ok {
		t.Fatal("write stage")
	}
	// A finished fold's result.json becomes a structure record.
	run := t.TempDir()
	os.WriteFile(filepath.Join(run, "result.json"), []byte(`{"ok":true,"performance":{"lengths":[3000,3000],"beam":100,"workers":2,"seconds":12}}`), 0o644)
	record, ok := perfFromResult("structure", run)
	if !ok || record.Parallel != 2 || !near(record.Units, FoldUnits(beam100(2, 3000, 3000))) || record.WorkModel != foldWorkModel || record.Seconds != 12 {
		t.Fatalf("%+v %v", record, ok)
	}
	if err := AppendPerf(filepath.Join(run, perfFile), record); err != nil || len(LoadPerf(filepath.Join(run, perfFile))) != 1 {
		t.Fatalf("structure record not stored: %v", err)
	}
	// Its work follows the pseudoknot search and the bonus matrix it ran.
	os.WriteFile(filepath.Join(run, "result.json"), []byte(`{"ok":true,"performance":{"lengths":[3000],"beam":20,"workers":1,"pseudoknot":true,"guided":false,"seconds":9}}`), 0o644)
	record, ok = perfFromResult("structure", run)
	if want := foldWork(FoldJob{Lengths: []int{3000}, Beam: 20, Workers: 1, Pseudoknot: true}); !ok || !near(record.Work, want) {
		t.Fatalf("pseudoknot, sequence only: %+v, want work %v", record, want)
	}
	pkWork := map[string]any{"lengths": []any{3000.0}, "beam": 20.0, "workers": 1.0, "pseudoknot": true, "guided": false}
	if job := foldJob(pkWork); !job.Pseudoknot || job.Guided || job.Beam != 20 {
		t.Fatalf("progress work %+v", job)
	}
	// A fold that compiled the kernels does not measure the speed.
	os.WriteFile(filepath.Join(run, "result.json"), []byte(`{"ok":true,"performance":{"lengths":[3000],"beam":100,"workers":1,"seconds":70,"compiled":true}}`), 0o644)
	if _, ok := perfFromResult("structure", run); ok {
		t.Fatal("a compiling fold was recorded")
	}
	// Nor does a ViennaRNA fold measure CPLfold's.
	os.WriteFile(filepath.Join(run, "result.json"), []byte(`{"ok":true,"performance":{"lengths":[3000],"engine":"viennarna","workers":1,"seconds":7}}`), 0o644)
	if _, ok := perfFromResult("structure", run); ok {
		t.Fatal("a ViennaRNA fold was recorded")
	}
}

// TestFoldRateFromAParallelRun: a run that took exactly its estimate does
// not change the next estimate (the stored rate is per unit of the work an
// estimate charges, not of all windows times the workers).
func TestFoldRateFromAParallelRun(t *testing.T) {
	lengths := []int{10000, 100}
	prior := EstimateFold(nil, beam100(2, lengths...))
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	result := fmt.Sprintf(`{"ok":true,"performance":{"lengths":[10000,100],"beam":100,"workers":2,"seconds":%f}}`,
		prior.Seconds-foldOverhead)
	if err := os.WriteFile(filepath.Join(runDir, "result.json"), []byte(result), 0o644); err != nil {
		t.Fatal(err)
	}
	record, ok := perfFromResult("structure", runDir)
	if !ok {
		t.Fatal("no record")
	}
	again := EstimateFold([]PerfRecord{record}, beam100(2, lengths...))
	if math.Abs(again.Seconds-prior.Seconds)/prior.Seconds > 0.01 {
		t.Fatalf("estimate %.1f s, after a run that took exactly that: %.1f s", prior.Seconds, again.Seconds)
	}
	if single := EstimateFold([]PerfRecord{record}, beam100(1, 10000)); single.Seconds > prior.Seconds*1.01 {
		t.Fatalf("one 10 kb fold now %.1f s, was %.1f s", single.Seconds, prior.Seconds)
	}
}

// TestFoldsDoNotPushSampleTimingsOut: the history is kept per kind.
func TestFoldsDoNotPushSampleTimingsOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), perfFile)
	if err := AppendPerf(path, PerfRecord{Kind: "sample_pipeline", Format: "fastq.gz", InputBytes: 1 << 20, CPU: 10, Seconds: 5}); err != nil {
		t.Fatal(err)
	}
	for range perfKeep + 5 {
		if err := AppendPerf(path, PerfRecord{Kind: "structure", Units: 1, Parallel: 1, Work: 1, Seconds: 1}); err != nil {
			t.Fatal(err)
		}
	}
	samples, folds := 0, 0
	for _, r := range LoadPerf(path) {
		switch r.Kind {
		case "sample_pipeline":
			samples++
		case "structure":
			folds++
		}
	}
	if samples != 1 || folds != perfKeep {
		t.Fatalf("kept %d sample and %d fold records", samples, folds)
	}
}

// The built-in rates match measured runs: a 362 MB .hyb indexed in 37 s;
// the index stage's time left follows the .hyb it is indexing.
func TestIndexStageEstimates(t *testing.T) {
	seconds, measured := SampleEstimate(nil, "hyb", 362e6, priorCPU)
	if measured || seconds < 30 || seconds > 45 {
		t.Fatalf(".hyb prior %v", seconds)
	}
	hyb := map[string]any{"format": "hyb", "input_bytes": 362e6, "cpu": 10.0}
	if left, ok := remainingSeconds(nil, Obj{"stage": "index", "work": hyb, "bytes_done": 181e6, "bytes_total": 362e6}, 10, time.Now()); !ok || !near(left, seconds/2) {
		t.Fatalf(".hyb half indexed: %v", left)
	}
	// A fastq.gz sample's index stage: 200 MB of .hyb left is about 20 s.
	reads := map[string]any{"format": "fastq.gz", "input_bytes": 2e9, "cpu": 10.0}
	if left, ok := remainingSeconds(nil, Obj{"stage": "index", "work": reads, "bytes_done": 100e6, "bytes_total": 300e6}, 10, time.Now()); !ok || !near(left, 20) {
		t.Fatalf("index of a mapped sample: %v", left)
	}
}

// Collapsed FASTA reads: the built-in rate matches the 319 MB ZIKV sample
// (204 s measured on the M1 Pro), a built-in rate gives a range and a
// measured one a single value, and the time after mapping is 22% of it.
func TestFastaEstimates(t *testing.T) {
	seconds, measured := SampleEstimate(nil, "fasta.gz", 319_415_770, priorCPU)
	if measured || seconds < 190 || seconds > 230 {
		t.Fatalf("fasta.gz prior %v", seconds)
	}
	e := EstimatePipelines(nil, []PipelineInput{{"fasta.gz", 319_415_770}}, priorCPU)
	if !near(e.Low, seconds*priorLow) || !near(e.High, seconds*priorHigh) || !near(e.Longest, seconds) {
		t.Fatalf("prior range %+v", e)
	}
	history := []PerfRecord{{Kind: "sample_pipeline", Format: "fasta.gz", InputBytes: 418_477_564, CPU: 10, Seconds: 292}}
	e = EstimatePipelines(history, []PipelineInput{{"fasta.gz", 319_415_770}, {"fasta.gz", 291_564_500}}, 10)
	if e.Measured != 2 || !near(e.Low, e.Seconds) || !near(e.High, e.Seconds) || e.Longest <= e.Seconds/2 {
		t.Fatalf("measured range %+v", e)
	}
	work := map[string]any{"format": "fasta.gz", "input_bytes": 319_415_770.0, "cpu": 10.0}
	if left, ok := remainingSeconds(nil, Obj{"stage": "map", "work": work, "bytes_done": 100.0, "bytes_total": 100.0}, 10, time.Now()); !ok || !near(left, seconds*0.22) {
		t.Fatalf("fasta.gz mapped: %v", left)
	}
	if left, ok := remainingSeconds(nil, Obj{"stage": "annotate", "work": work}, 10, time.Now()); !ok || !near(left, seconds*0.11) {
		t.Fatalf("fasta.gz annotating: %v", left)
	}
	// Mapping FASTA reports no bytes: the time since the stage started counts.
	now := time.Now()
	started := Obj{"stage": "map", "work": work, "updated_at": now.Add(-60 * time.Second).UTC().Format(time.RFC3339)}
	if left, ok := remainingSeconds(nil, started, 10, now); !ok || math.Abs(left-(seconds-60)) > 1 { // timestamps keep whole seconds
		t.Fatalf("fasta.gz mapping for 60 s: %v of %v", left, seconds)
	}
	started["updated_at"] = now.Add(-time.Hour).UTC().Format(time.RFC3339)
	if left, ok := remainingSeconds(nil, started, 10, now); !ok || left <= seconds*0.22 || left > seconds*0.23 {
		t.Fatalf("fasta.gz mapping far past its estimate: %v, want just over the 22%% after mapping", left)
	}
}

// A running sample pipeline gets the share of the whole pipeline done,
// which moves while mapping FASTA reads (no bytes reported) and stays
// below 1 until the task finishes.
func TestAddRemainingFractionDone(t *testing.T) {
	s := NewService(nil, ServiceOptions{})
	work := map[string]any{"format": "fasta.gz", "input_bytes": 319_415_770.0, "cpu": 10.0}
	task := func(ago time.Duration) Obj {
		return Obj{"kind": "sample_pipeline", "status": "running", "progress": map[string]any{
			"stage": "map", "work": work, "updated_at": time.Now().Add(-ago).UTC().Format(time.RFC3339)}}
	}
	tasks := []Obj{task(0), task(80 * time.Second), task(time.Hour),
		{"kind": "sample_pipeline", "status": "running", "progress": map[string]any{"stage": "map", "detail": "waiting: another sample is mapping", "work": work}}}
	s.addRemaining(tasks)
	early, mid, late := tasks[0]["fraction_done"].(float64), tasks[1]["fraction_done"].(float64), tasks[2]["fraction_done"].(float64)
	if early > 0.02 || mid < 0.3 || mid > 0.45 || late < 0.77 || late > 0.79 {
		t.Fatalf("fractions %v %v %v", early, mid, late)
	}
	if _, ok := tasks[3]["fraction_done"]; ok {
		t.Fatal("a sample waiting for the map slot has no fraction")
	}
	if tasks[1]["over_estimate"] != nil || tasks[1]["seconds_left"] == nil {
		t.Fatalf("on time: %v", tasks[1])
	}
	if tasks[2]["over_estimate"] != true || tasks[2]["seconds_left"] != nil {
		t.Fatalf("past the estimate: %v", tasks[2])
	}
	// Past the estimate the bar still moves, more slowly, and stays below
	// the end of mapping.
	mapping, _ := SampleEstimate(nil, "fasta.gz", 319_415_770, 10)
	mapping *= 0.78
	var previous float64
	for i, x := range []float64{0.5, 0.9, 0.97, 1.05, 1.2, 1.5, 2} {
		one := []Obj{task(time.Duration(x * mapping * float64(time.Second)))}
		s.addRemaining(one)
		f := one[0]["fraction_done"].(float64)
		if f <= previous || f >= 0.78 {
			t.Fatalf("step %d (%.1f of the mapping estimate): %v after %v", i, x, f, previous)
		}
		if over := one[0]["over_estimate"] == true; over != (x > 1) {
			t.Fatalf("step %d: over %v", i, over)
		}
		previous = f
	}
}
