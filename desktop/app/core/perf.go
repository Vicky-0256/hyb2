package core

// Speed of this computer for time estimates. Every finished sample
// pipeline adds its active time (sample_run.py's result "performance",
// without waiting for the map slot) to performance.json beside
// preferences.json; estimates use the recent runs of the same input format,
// or a built-in rate measured on an Apple M1 Pro until there are none.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

// PerfRecord is one finished sample pipeline or structure fold.
type PerfRecord struct {
	Kind       string  `json:"kind"`
	Format     string  `json:"format,omitempty"`
	InputBytes int64   `json:"input_bytes,omitempty"`
	CPU        uint32  `json:"cpu,omitempty"`
	Seconds    float64 `json:"seconds"`
	At         string  `json:"at"`
	// Structure folds: FoldUnits of the windows and how many ran at once.
	Units    float64 `json:"units,omitempty"`
	Parallel int     `json:"parallel,omitempty"`
	// Work is what EstimateFold charges for these windows (foldWork): the
	// fold's rate is Seconds / Work. WorkModel is the foldWorkModel that
	// charged it: a record of another model (or none) is not used.
	Work      float64 `json:"work,omitempty"`
	WorkModel int     `json:"work_model,omitempty"`
}

const (
	perfFile = "performance.json"
	perfKeep = 200
	// perfRecent is how many recent runs of a format an estimate uses.
	perfRecent = 20
	// perfCPUExponent: Bowtie2 time ~ cpus^-0.7 (measured: 4 -> 10 threads
	// is 1.9x faster).
	perfCPUExponent = 0.7
	priorCPU        = 10
	// sampleOverhead is the fixed time of a sample pipeline (process start,
	// Bowtie2 index, analysis index): 14 samples of 200,000 reads took 1.4 s
	// each more than their size alone predicts.
	sampleOverhead = 1.5
)

// priorSecondsPerMB are seconds per MB of input at 10 CPUs (Apple M1 Pro,
// ZIKV SPLASH reads; fastq.gz: 3 M reads, 160 MB in 76 s, and a full 2 GB
// sample in ~15 min). fasta.gz is the median of 7 collapsed ZIKV samples
// (226-418 MB, 0.51-0.95 s/MB); fasta keeps the fasta.gz/fasta ratio of the
// earlier rates. A .hyb input is only indexed: 362 MB in 37 s.
var priorSecondsPerMB = map[string]float64{
	"fastq.gz": 0.47, "fastq": 0.078, "fasta.gz": 0.65, "fasta": 0.11, "sam": 0.02, "hyb": indexSecondsPerMB,
}

// A built-in rate is one computer's; the range shown for it runs from
// priorLow to priorHigh times the estimate (measured on the M1 Pro: 0.8 to
// 1.5 times the median, and a slower or busier computer adds to the top).
const (
	priorLow  = 0.8
	priorHigh = 2.0
)

// indexSecondsPerMB is the analysis index build per MB of .hyb (measured:
// 362 MB in 37 s).
const indexSecondsPerMB = 0.1

// sampleStageWeights is the share of a sample pipeline's time before and
// in each stage, for the remaining time of a running one (measured: reads
// ~15%, map ~82%, the rest ~3%).
var sampleStageWeights = map[string][2]float64{
	"check": {0, 0}, "reference": {0, 0}, "reads": {0, 0.15}, "map": {0.15, 0.82},
}

// fastaStageWeights are the same for collapsed FASTA reads, which have no
// reads stage and many more hybrids to annotate (measured on two ZIKV
// samples: map 77-79%, the rest 21-23%).
var fastaStageWeights = map[string][2]float64{
	"check": {0, 0}, "reference": {0, 0}, "map": {0, 0.78},
}

// stageWeights is the weight table for a format and the share of the time
// after mapping.
func stageWeights(format string) (map[string][2]float64, float64) {
	if format == "fasta" || format == "fasta.gz" {
		return fastaStageWeights, 0.22
	}
	return sampleStageWeights, 0.03
}

var perfMu sync.Mutex

func perfPath(prefsPath string) string {
	if prefsPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(prefsPath), perfFile)
}

// LoadPerf reads the history; a missing or corrupt file is empty.
func LoadPerf(path string) []PerfRecord {
	if path == "" {
		return nil
	}
	perfMu.Lock()
	defer perfMu.Unlock()
	return readPerf(path)
}

func readPerf(path string) []PerfRecord {
	var records []PerfRecord
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &records)
	}
	return records
}

// AppendPerf adds a record, keeping the most recent perfKeep of its kind
// (many folds must not push the sample timings out, nor the reverse).
func AppendPerf(path string, record PerfRecord) error {
	if path == "" || record.Seconds <= 0 || (record.Kind == "structure" && record.Units <= 0) ||
		(record.Kind != "structure" && record.InputBytes <= 0) {
		return nil
	}
	perfMu.Lock()
	defer perfMu.Unlock()
	records := append(readPerf(path), record)
	seen := map[string]int{}
	kept := make([]PerfRecord, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		if seen[records[i].Kind]++; seen[records[i].Kind] <= perfKeep {
			kept = append(kept, records[i])
		}
	}
	slices.Reverse(kept)
	records = kept
	data, err := json.MarshalIndent(records, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// perfFromResult is the record of a finished sample pipeline's or
// structure fold's result.json ("performance"), or false.
func perfFromResult(kind, runDir string) (PerfRecord, bool) {
	data, err := os.ReadFile(filepath.Join(runDir, "result.json"))
	if err != nil {
		return PerfRecord{}, false
	}
	var result struct {
		OK          bool `json:"ok"`
		Performance *struct {
			Format     string  `json:"format"`
			InputBytes int64   `json:"input_bytes"`
			CPU        uint32  `json:"cpu"`
			Seconds    float64 `json:"seconds"`
			Lengths    []int   `json:"lengths"`
			Beam       int     `json:"beam"`
			Workers    int     `json:"workers"`
			Pseudoknot bool    `json:"pseudoknot"`
			Guided     *bool   `json:"guided"`
			// ViennaRNA and COMRADES folds are not CPLfold's speed.
			Engine string `json:"engine"`
			// The run compiled Numba kernels: its time is not the speed.
			Compiled bool `json:"compiled"`
		} `json:"performance"`
	}
	if json.Unmarshal(data, &result) != nil || !result.OK || result.Performance == nil || result.Performance.Compiled ||
		(result.Performance.Engine != "" && result.Performance.Engine != "cplfold") {
		return PerfRecord{}, false
	}
	p := result.Performance
	record := PerfRecord{Kind: kind, Seconds: p.Seconds, At: time.Now().UTC().Format(time.RFC3339)}
	switch kind {
	case "sample_pipeline":
		record.Format, record.InputBytes, record.CPU = p.Format, p.InputBytes, p.CPU
	case "structure":
		job := FoldJob{Lengths: p.Lengths, Beam: p.Beam, Workers: p.Workers, Pseudoknot: p.Pseudoknot, Guided: p.Guided == nil || *p.Guided}
		record.Units, record.Parallel = FoldUnits(job), foldParallel(len(p.Lengths), p.Workers)
		record.Work, record.WorkModel = foldWork(job), foldWorkModel
		if record.Units <= 0 {
			return PerfRecord{}, false
		}
	default:
		return PerfRecord{}, false
	}
	return record, true
}

// ---- structure folds

const (
	// CPLfold time grows as length^1.85 and as beam^0.3 (measured on an
	// Apple M1 Pro, one core, kernels already compiled: beam 20, 2000 nt
	// 3.0 s, 4000 nt 10 s, 10,807 nt 71 s; beam 100, 4000 nt 22 s, 10,807
	// nt 112 s). A long fold grows faster than its beam search alone.
	foldExponent     = 1.85
	foldBeamExponent = 0.3
	// priorFoldSeconds is the time of one unit (1000 nt, beam 100).
	priorFoldSeconds = 1.37
	// foldPseudoknot is the pseudoknot search (phase 2) on top of phase 1:
	// measured 1.1 to 1.45 times the pseudoknot-free fold, more for longer
	// sequences (the whole 10,807-nt genome at beam 20: 94 s, not 71 s).
	foldPseudoknot = 1.35
	// bonusSeconds builds the HYB bonus matrix, per (1000 nt)^2 of the
	// folded length: it is dense (measured 33-43 s for the whole 10,807-nt
	// ZIKV genome with 735,000 chimeras).
	bonusSeconds = 0.325
	// foldOverhead covers starting the task and reading the .hyb evidence.
	foldOverhead = 3.0
	// foldWorkModel versions the work FoldUnits charges: a rate measured
	// with another model does not calibrate this one.
	foldWorkModel = 2
)

// FoldJob is a structure fold to estimate: the lengths of its windows (one
// for the whole RNA, or two strands folded as one), the beam, how many
// windows run at once, whether pseudoknots are searched and whether a HYB
// bonus matrix is built (not a sequence-only fold).
type FoldJob struct {
	Lengths    []int
	Beam       int
	Workers    int
	Pseudoknot bool
	Guided     bool
}

// FoldUnits is the work of folding the windows, in units of
// priorFoldSeconds: per window (length/1000)^1.85 x (beam/100)^0.3, times
// foldPseudoknot when pseudoknots are searched, plus the bonus matrix when
// guided.
func FoldUnits(job FoldJob) float64 {
	var units float64
	for _, n := range job.Lengths {
		units += foldUnit(n, job)
	}
	return units
}

func foldUnit(n int, job FoldJob) float64 {
	beam := job.Beam
	if beam <= 0 {
		beam = 20 // the runtime's default (structure.DEFAULTS, as the web page)
	}
	kb := float64(n) / 1000
	units := math.Pow(kb, foldExponent) * math.Pow(float64(beam)/100, foldBeamExponent)
	if job.Pseudoknot {
		units *= foldPseudoknot
	}
	if job.Guided {
		units += bonusSeconds / priorFoldSeconds * kb * kb
	}
	return units
}

func foldParallel(windows, workers int) int {
	return max(1, min(max(workers, 1), max(windows, 1)))
}

// FoldEstimate is the expected time of a structure fold.
type FoldEstimate struct {
	Seconds  float64 `json:"seconds"`
	Measured bool    `json:"measured"`
}

// EstimateFold is the expected time of a fold, from this computer's recent
// folds (their seconds per unit of work) or the built-in rate.
func EstimateFold(history []PerfRecord, job FoldJob) FoldEstimate {
	var rates []float64
	for i := len(history) - 1; i >= 0 && len(rates) < perfRecent; i-- {
		if r := history[i]; r.Kind == "structure" && r.Seconds > 0 && r.Work > 0 && r.WorkModel == foldWorkModel {
			rates = append(rates, r.Seconds/r.Work)
		}
	}
	rate, measured := priorFoldSeconds, false
	if len(rates) > 0 {
		sort.Float64s(rates)
		rate, measured = rates[len(rates)/2], true
	}
	return FoldEstimate{Seconds: foldWork(job)*rate + foldOverhead, Measured: measured}
}

// foldWork is the work of folding the windows at most Workers at once:
// spread over the workers, but never less than the largest window.
func foldWork(job FoldJob) float64 {
	largest := 0
	for _, n := range job.Lengths {
		largest = max(largest, n)
	}
	return math.Max(FoldUnits(job)/float64(foldParallel(len(job.Lengths), job.Workers)), foldUnit(largest, job))
}

// foldJob is the fold a progress names (work: {lengths, beam, workers,
// pseudoknot, guided}); a runtime that does not say is taken as
// pseudoknot-free and guided, its defaults.
func foldJob(work map[string]any) FoldJob {
	job := FoldJob{Guided: true}
	raw, _ := work["lengths"].([]any)
	for _, v := range raw {
		if n, ok := v.(float64); ok && n > 0 {
			job.Lengths = append(job.Lengths, int(n))
		}
	}
	beam, _ := work["beam"].(float64)
	workers, _ := work["workers"].(float64)
	job.Beam, job.Workers = int(beam), int(workers)
	job.Pseudoknot, _ = work["pseudoknot"].(bool)
	if guided, ok := work["guided"].(bool); ok {
		job.Guided = guided
	}
	return job
}

// foldRemaining is the time left of a running fold from its progress
// ({stage, bytes_done, bytes_total, updated_at, work: {lengths, beam,
// workers, pseudoknot, guided}}), or false.
func foldRemaining(history []PerfRecord, progress Obj, now time.Time) (float64, bool) {
	work, _ := progress["work"].(map[string]any)
	job := foldJob(work)
	if len(job.Lengths) == 0 {
		return 0, false
	}
	total := EstimateFold(history, job).Seconds
	switch stage, _ := progress["stage"].(string); stage {
	case "evidence":
		return total, true
	case "fold":
	default:
		return 0, false
	}
	if len(job.Lengths) > 1 {
		done, _ := progress["bytes_done"].(float64)
		return total * (1 - math.Min(done/float64(len(job.Lengths)), 1)), true
	}
	// One fold reports nothing until it ends: time since it started.
	at, err := time.Parse(time.RFC3339, fmt.Sprint(progress["updated_at"]))
	if err != nil {
		return 0, false
	}
	left := total - foldOverhead - now.Sub(at).Seconds()
	return left, left > 0
}

// cpuScale converts a time measured with from CPUs to one with to CPUs.
func cpuScale(from, to uint32) float64 {
	if from == 0 || to == 0 {
		return 1
	}
	return math.Pow(float64(from)/float64(to), perfCPUExponent)
}

// SampleEstimate is the expected active time of one sample pipeline and
// whether it comes from runs measured on this computer.
func SampleEstimate(history []PerfRecord, format string, inputBytes int64, cpu uint32) (seconds float64, measured bool) {
	var rates []float64
	for i := len(history) - 1; i >= 0 && len(rates) < perfRecent; i-- {
		r := history[i]
		if r.Kind == "sample_pipeline" && r.Format == format && r.InputBytes > 0 && r.Seconds > 0 {
			rates = append(rates, math.Max(r.Seconds-sampleOverhead, 0)/float64(r.InputBytes)*cpuScale(r.CPU, cpu))
		}
	}
	if len(rates) > 0 {
		sort.Float64s(rates)
		return sampleOverhead + rates[len(rates)/2]*float64(inputBytes), true
	}
	perMB, ok := priorSecondsPerMB[format]
	if !ok {
		perMB = priorSecondsPerMB["fastq.gz"]
	}
	return sampleOverhead + perMB*float64(inputBytes)/1e6*cpuScale(priorCPU, cpu), false
}

// PipelineInput is one sample to estimate.
type PipelineInput struct {
	Format     string `json:"format"`
	InputBytes int64  `json:"input_bytes"`
}

// PipelineEstimate is the expected time of a batch of sample pipelines.
type PipelineEstimate struct {
	Seconds float64 `json:"seconds"`
	// Measured is how many of the samples have runs of their format
	// measured on this computer; the rest use the built-in rate.
	Measured int    `json:"measured"`
	Samples  int    `json:"samples"`
	CPU      uint32 `json:"cpu"`
	// Low and High bound Seconds: equal to it when every sample is
	// measured, wider for the samples on a built-in rate.
	Low  float64 `json:"low"`
	High float64 `json:"high"`
	// Longest is the longest single sample. Samples map one at a time, so
	// a later one waits in the queue for the ones before it.
	Longest float64 `json:"longest"`
}

// EstimatePipelines adds up the samples' times. With several samples the
// next one's read preparation overlaps the current mapping, which the
// estimate ignores (it errs long).
func EstimatePipelines(history []PerfRecord, inputs []PipelineInput, cpu uint32) PipelineEstimate {
	e := PipelineEstimate{Samples: len(inputs), CPU: cpu}
	for _, in := range inputs {
		seconds, measured := SampleEstimate(history, in.Format, in.InputBytes, cpu)
		e.Seconds += seconds
		e.Longest = math.Max(e.Longest, seconds)
		if measured {
			e.Measured++
			e.Low += seconds
			e.High += seconds
		} else {
			e.Low += seconds * priorLow
			e.High += seconds * priorHigh
		}
	}
	return e
}

// sampleTotal is the expected active time of the sample pipeline that wrote
// this progress, or false when its input is unknown.
func sampleTotal(history []PerfRecord, progress Obj, cpu uint32) (float64, bool) {
	work, _ := progress["work"].(map[string]any)
	format, _ := work["format"].(string)
	inputBytes, _ := work["input_bytes"].(float64)
	if format == "" || inputBytes <= 0 {
		return 0, false
	}
	if c, ok := work["cpu"].(float64); ok && c >= 1 {
		cpu = uint32(c)
	}
	total, _ := SampleEstimate(history, format, int64(inputBytes), cpu)
	return total, true
}

// remainingSeconds is the time left of a running sample pipeline from its
// progress ({stage, detail, bytes_done, bytes_total, updated_at, work}), or
// false when unknown (waiting for the map slot, unknown stage or input).
// A stage that reports no bytes (mapping FASTA reads) counts its time from
// updated_at, which it writes once when it starts.
func remainingSeconds(history []PerfRecord, progress Obj, cpu uint32, now time.Time) (float64, bool) {
	left, _, ok := sampleRemaining(history, progress, cpu, now)
	return left, ok
}

// sampleRemaining is remainingSeconds and whether a stage that reports no
// bytes has run longer than its estimate. Such a stage's share done grows
// with its time up to 90%, then ever more slowly towards 100% (95% at twice
// the estimate, 97% at three times), so its bar keeps moving and the time
// left never reaches the end of the stage.
func sampleRemaining(history []PerfRecord, progress Obj, cpu uint32, now time.Time) (left float64, over, ok bool) {
	stage, _ := progress["stage"].(string)
	detail, _ := progress["detail"].(string)
	total, ok := sampleTotal(history, progress, cpu)
	if !ok || len(detail) >= 7 && detail[:7] == "waiting" {
		return 0, false, false
	}
	work, _ := progress["work"].(map[string]any)
	format, _ := work["format"].(string)
	done, size := -1.0, -1.0
	if d, ok := progress["bytes_done"].(float64); ok {
		if t, ok := progress["bytes_total"].(float64); ok && t > 0 {
			done, size = math.Min(d, t), t
		}
	}
	if stage == "contact" || stage == "index" {
		switch {
		case format == "hyb":
			// Indexing is all the pipeline does with a .hyb.
			if stage == "index" && size > 0 {
				return math.Max(total*(1-done/size), 1), false, true
			}
			return total, false, true
		case stage == "index" && size > 0:
			// The .hyb the pipeline wrote: its size is the stage's work.
			return math.Max(indexSecondsPerMB*(size-done)/1e6, 1), false, true
		}
	}
	table, after := stageWeights(format)
	weights, ok := table[stage]
	if !ok {
		// Past mapping: half of the time after mapping is left.
		return math.Max(total*after*0.5, 1), false, true
	}
	share := weights[0]
	if size > 0 {
		share += weights[1] * done / size
	} else if at, err := time.Parse(time.RFC3339, fmt.Sprint(progress["updated_at"])); err == nil && weights[1] > 0 {
		x := math.Max(now.Sub(at).Seconds(), 0) / (total * weights[1])
		over = x > 1
		if x > 0.9 {
			x = 0.9 + 0.1*(x-0.9)/(x-0.9+1)
		}
		share += weights[1] * x
	}
	return math.Max(total*(1-share), 1), over, true
}
