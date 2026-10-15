package core

// Speed of this computer for time estimates. Every finished sample
// pipeline adds its active time (sample_run.py's result "performance",
// without waiting for the map slot) to performance.json beside
// preferences.json; estimates use the recent runs of the same input format,
// or a built-in rate measured on an Apple M1 Pro until there are none.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// PerfRecord is one finished sample pipeline.
type PerfRecord struct {
	Kind       string  `json:"kind"`
	Format     string  `json:"format"`
	InputBytes int64   `json:"input_bytes"`
	CPU        uint32  `json:"cpu"`
	Seconds    float64 `json:"seconds"`
	At         string  `json:"at"`
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
)

// priorSecondsPerMB are seconds per MB of input at 10 CPUs (Apple M1 Pro,
// ZIKV SPLASH reads; fastq.gz: 3 M reads, 160 MB in 76 s, and a full 2 GB
// sample in ~15 min).
var priorSecondsPerMB = map[string]float64{
	"fastq.gz": 0.47, "fastq": 0.078, "fasta.gz": 0.4, "fasta": 0.07, "sam": 0.02, "hyb": 0.01,
}

// sampleStageWeights is the share of a sample pipeline's time before and
// in each stage, for the remaining time of a running one (measured: reads
// ~15%, map ~82%, the rest ~3%).
var sampleStageWeights = map[string][2]float64{
	"check": {0, 0}, "reference": {0, 0}, "reads": {0, 0.15}, "map": {0.15, 0.82},
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

// AppendPerf adds a record, keeping the most recent perfKeep.
func AppendPerf(path string, record PerfRecord) error {
	if path == "" || record.Seconds <= 0 || record.InputBytes <= 0 {
		return nil
	}
	perfMu.Lock()
	defer perfMu.Unlock()
	records := append(readPerf(path), record)
	if len(records) > perfKeep {
		records = records[len(records)-perfKeep:]
	}
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

// perfFromResult is the record of a finished sample pipeline's result.json
// ("performance"), or false.
func perfFromResult(runDir string) (PerfRecord, bool) {
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
		} `json:"performance"`
	}
	if json.Unmarshal(data, &result) != nil || !result.OK || result.Performance == nil {
		return PerfRecord{}, false
	}
	p := result.Performance
	return PerfRecord{Kind: "sample_pipeline", Format: p.Format, InputBytes: p.InputBytes, CPU: p.CPU,
		Seconds: p.Seconds, At: time.Now().UTC().Format(time.RFC3339)}, true
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
			rates = append(rates, r.Seconds/float64(r.InputBytes)*cpuScale(r.CPU, cpu))
		}
	}
	if len(rates) > 0 {
		sort.Float64s(rates)
		return rates[len(rates)/2] * float64(inputBytes), true
	}
	perMB, ok := priorSecondsPerMB[format]
	if !ok {
		perMB = priorSecondsPerMB["fastq.gz"]
	}
	return perMB * float64(inputBytes) / 1e6 * cpuScale(priorCPU, cpu), false
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
}

// EstimatePipelines adds up the samples' times. With several samples the
// next one's read preparation overlaps the current mapping, which the
// estimate ignores (it errs long).
func EstimatePipelines(history []PerfRecord, inputs []PipelineInput, cpu uint32) PipelineEstimate {
	e := PipelineEstimate{Samples: len(inputs), CPU: cpu}
	for _, in := range inputs {
		seconds, measured := SampleEstimate(history, in.Format, in.InputBytes, cpu)
		e.Seconds += seconds
		if measured {
			e.Measured++
		}
	}
	return e
}

// remainingSeconds is the time left of a running sample pipeline from its
// progress ({stage, detail, bytes_done, bytes_total, work}), or false when
// unknown (waiting for the map slot, unknown stage or input).
func remainingSeconds(history []PerfRecord, progress Obj, cpu uint32) (float64, bool) {
	work, _ := progress["work"].(map[string]any)
	format, _ := work["format"].(string)
	inputBytes, _ := work["input_bytes"].(float64)
	stage, _ := progress["stage"].(string)
	detail, _ := progress["detail"].(string)
	if format == "" || inputBytes <= 0 || len(detail) >= 7 && detail[:7] == "waiting" {
		return 0, false
	}
	if c, ok := work["cpu"].(float64); ok && c >= 1 {
		cpu = uint32(c)
	}
	total, _ := SampleEstimate(history, format, int64(inputBytes), cpu)
	weights, ok := sampleStageWeights[stage]
	if !ok {
		// Past mapping: seconds left.
		return math.Max(total*0.03*0.5, 1), true
	}
	done := weights[0]
	if d, ok := progress["bytes_done"].(float64); ok {
		if t, ok := progress["bytes_total"].(float64); ok && t > 0 {
			done += weights[1] * math.Min(d/t, 1)
		}
	}
	return total * (1 - done), true
}
