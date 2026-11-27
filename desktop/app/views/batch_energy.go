package views

// Explore › Binding energies: the web's "HYB binding energies" batch tool
// (web/batch-tools.js). Column 3 of one sample's .hyb (the ΔG of the two
// arms) is recalculated for all its records, or for those matching the
// Interactions filters, with RNAduplex (as the pipeline), RNAcofold,
// CPLcofold or CPLcofold with pseudoknots, as a `batch_energy` task (desktop/runtime/batch_energy.py).
// The task writes energies.hyb, audit.tsv, parameters.json and a ZIP of the
// three to its run folder; the sample's own file is not changed.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// batchEngines are the web's engines: value, label and what to know.
var batchEngines = [][3]string{
	{"rnaduplex", "RNAduplex (ViennaRNA), as the HYB2 pipeline", "The HYB2 pipeline's method for the dG column: only pairs between the two arms (bundled, 37 °C); combined arms up to 2,000 nt. Arms that form no pair keep their energy."},
	{"rnacofold", "RNAcofold (ViennaRNA)", "ViennaRNA's MFE cofold (bundled), in which each arm may also pair with itself; combined arms up to 2,000 nt."},
	{"cplcofold", "CPLcofold, nested (Turner energies)", "Same Turner energy model as RNAcofold, beam search (DP09, beam 200, energy delta 5, 3 phase-1 candidates); combined arms up to 500 nt."},
	{"cplcofold-pk", "CPLcofold with pseudoknots (experimental score)", "Adds CPLfold's pseudoknot phase: its DP09 pseudoknot term is an experimental ranking score, not a validated free energy; combined arms up to 500 nt."},
}

// batchCost is an engine's records per job and time per record, for the
// run-time estimate. RNAduplex and RNAcofold take the web's flat time per
// record (ENERGY_ENGINES msPerRecord). CPLcofold's time grows with the
// combined length of the two arms, and only records within maxLength are
// folded: byLength is the ms per record at batchCostLengths, the median of
// six ZIKV arm pairs each folded alone by this runtime on 4 workers of one
// Numba thread (Apple M1 Pro; beam 200, three phase-1 candidates),
// interpolated on log-log scales; a run adds batchStartMs to start the
// workers and read and write the files.
type batchCost struct {
	chunk     int64
	ms        float64
	byLength  []float64
	maxLength int64
}

var batchCostLengths = []float64{40, 100, 200, 500}

const batchStartMs = 2500

var batchEngineCost = map[string]batchCost{
	"rnaduplex":    {chunk: 2000, ms: 3},
	"rnacofold":    {chunk: 64, ms: 15},
	"cplcofold":    {chunk: 4, byLength: []float64{2.1, 10.6, 39, 211}, maxLength: 500},
	"cplcofold-pk": {chunk: 4, byLength: []float64{5, 16.8, 58, 246}, maxLength: 500},
}

// batchRecordMs is CPLcofold's time for a record whose arms are length nt
// together: the power law through the two nearest of batchCostLengths (the
// first or last two beyond them).
func batchRecordMs(byLength []float64, length float64) float64 {
	k := 1
	for k < len(batchCostLengths)-1 && length > batchCostLengths[k] {
		k++
	}
	slope := math.Log(byLength[k]/byLength[k-1]) / math.Log(batchCostLengths[k]/batchCostLengths[k-1])
	return byLength[k-1] * math.Pow(length/batchCostLengths[k-1], slope)
}

// batchCounts are a sample's records and how many there are of each
// combined arm length ([length, records], analysis_summary arm_lengths; nil
// while unknown).
type batchCounts struct {
	records int64
	lengths [][2]int64
}

// batchNoReference is the web's note when there is no mapped FASTA.
const batchNoReference = "Load and map a reference FASTA first."

var batchRecordLabels = []string{"All records of the sample", "Records matching the Interactions filters"}

type batchEnergyState struct {
	sample, engine, records string
	err                     string
	// cpus are the CPUs tasks may use (Preferences), the task's workers;
	// counts the records of each sample and their arm lengths
	// (analysis_summary), by batchCountKey: the project, the sample and its
	// index, so a rebuilt index or another project counts again; countsFor
	// is the project the counts are of (another one clears them).
	cpus      int
	cpusAsked bool
	countLoad loader
	counts    map[string]batchCounts
	countsFor string
	// result is the newest finished task's result.json (keyed by task id).
	resultFor string
	result    *batchEnergyResult
	loading   bool
}

// batchEnergyResult is the part of result.json the page shows.
type batchEnergyResult struct {
	Message string            `json:"message"`
	Outputs map[string]string `json:"outputs"`
	Summary struct {
		Updated    int64  `json:"updated"`
		Unchanged  int64  `json:"unchanged"`
		Engine     string `json:"engine"`
		EngineName string `json:"engine_name"`
		SampleID   string `json:"sample_id"`
		SourceFile string `json:"source_file"`
	} `json:"summary"`
}

func batchEngineOf(label string) [3]string {
	for _, e := range batchEngines {
		if e[1] == label {
			return e
		}
	}
	return batchEngines[0]
}

func (p *explorePage) batchSampleLabels() ([]string, map[string]string) {
	return p.sampleLabels(p.ids)
}

// batchHasReference: the sample's reference is in the project, so its arms
// can be cut from it.
func (p *explorePage) batchHasReference(id string) bool {
	s := p.a.Project.Sample(id)
	return s != nil && s.ReferenceID != "" && p.a.Project.Reference(s.ReferenceID) != nil
}

// loadBatchInputs asks for the task's CPUs once and the sample's records.
func (p *explorePage) loadBatchInputs(sample string) {
	s := &p.be
	a := p.a
	if !s.cpusAsked {
		s.cpusAsked = true
		a.Go(func() func() {
			cpus := 1
			if prefs, err := a.B.Preferences(); err == nil {
				if budget, _ := prefs["budget"].(map[string]any); budget != nil {
					cpus = max(1, int(num(budget["cpu"])))
				}
			}
			return func() { s.cpus = cpus }
		})
	}
	if sample == "" {
		return
	}
	path := a.Project.Path
	if s.countsFor != path {
		s.counts, s.countsFor = nil, path
	}
	key := p.batchCountKey(sample)
	if _, done := s.counts[key]; done {
		return
	}
	p.ensure(&s.countLoad, key, func() (func(), error) {
		got, err := p.query(path, "analysis_summary", []string{sample}, core.Obj{"pairs": false, "arm_lengths": true})
		if err != nil {
			return nil, err
		}
		merged, _ := got["merged"].(map[string]any)
		counts := batchCounts{records: int64(num(merged["records"]))}
		if items, ok := merged["arm_lengths"].([]any); ok {
			counts.lengths = make([][2]int64, 0, len(items))
			for _, item := range items {
				if pair, ok := item.([]any); ok && len(pair) == 2 {
					counts.lengths = append(counts.lengths, [2]int64{int64(num(pair[0])), int64(num(pair[1]))})
				}
			}
		}
		return func() {
			if s.counts == nil {
				s.counts = map[string]batchCounts{}
			}
			s.counts[key] = counts
		}, nil
	})
}

// batchCountKey keys a sample's record count: the project, the sample and
// its index (stage time and file), as the top bar's selection stamp.
func (p *explorePage) batchCountKey(sample string) string {
	index := ""
	if s := p.a.Project.Sample(sample); s != nil {
		index = s.Stages["index"].UpdatedAt + "\x00" + s.Outputs.Index
	}
	return p.a.Project.Path + "\x00" + sample + "\x00" + index
}

// batchEstimate is the run-time estimate of all the sample's records, as the
// web's, " All N records: about … with W parallel workers here." ("" while
// unknown). The workers are counted as the task counts them, from the jobs of
// the records it folds.
func batchEstimate(engine string, counts batchCounts, cpus int) string {
	cost, ok := batchEngineCost[engine]
	if !ok || counts.records <= 0 || cpus <= 0 || (cost.byLength != nil && counts.lengths == nil) {
		return ""
	}
	folded, ms := counts.records, float64(counts.records)*cost.ms
	if cost.byLength != nil {
		folded, ms = 0, 0
		for _, n := range counts.lengths {
			if n[0] <= cost.maxLength {
				folded += n[1]
				ms += float64(n[1]) * batchRecordMs(cost.byLength, float64(n[0]))
			}
		}
	}
	workers := max(1, min(int64(cpus), (folded+cost.chunk-1)/cost.chunk))
	ms /= float64(workers)
	if cost.byLength != nil {
		ms += batchStartMs
	}
	text := fmt.Sprintf(" All %s records: about %s with %d", FormatNumber(counts.records), batchDuration(ms), workers)
	if workers == 1 {
		return text + " worker here."
	}
	return text + " parallel workers here."
}

// batchDuration is the web's duration(ms): seconds, minutes or hours.
func batchDuration(ms float64) string {
	seconds := max(1, math.Round(ms/1000))
	switch {
	case seconds < 90:
		return fmt.Sprintf("%.0f s", seconds)
	case seconds < 5400:
		return fmt.Sprintf("%.0f min", math.Round(seconds/60))
	}
	return fmt.Sprintf("%.1f h", seconds/3600)
}

func (p *explorePage) batchEnergyView(c *ui.Context) {
	s := &p.be
	t := c.Theme()
	labels, ids := p.batchSampleLabels()
	if s.engine == "" {
		// RNAduplex, as the pipeline
		s.engine, s.records = batchEngines[0][1], batchRecordLabels[0]
	}
	if _, ok := ids[s.sample]; !ok && len(labels) > 0 {
		s.sample = labels[0]
	}
	engine := batchEngineOf(s.engine)
	filtered := s.records == batchRecordLabels[1]
	sample := ids[s.sample]
	p.loadBatchInputs(sample)
	hasReference := sample != "" && p.batchHasReference(sample)
	ui.Scroll(c.Key("be-scroll")).Grow(1).Children(func() {
		ui.Column(c).Gap(Space3).Children(func() {
			Card(c, "HYB binding energies", func() {
				ui.Text(c, "Recalculate column 3 (ΔG of the two arms, kcal/mol, 37 °C) of one sample's .hyb from its reference's arm sequences. "+
					"The task writes an updated HYB, an audit and its parameters to its run folder; the sample's file and index stay unchanged.").
					FontSize(12).TextColor(t.TextMuted)
				running := cmpTaskRows(c, p.a, "batch_energy")
				ui.Row(c).Gap(Space3).Wrap().AlignItems(ui.End).Children(func() {
					picker(c, "be-sample", "Sample", &s.sample, labels, 180)
					names := make([]string, len(batchEngines))
					for i, e := range batchEngines {
						names[i] = e[1]
					}
					picker(c, "be-engine", "Engine", &s.engine, names, 300)
					picker(c, "be-records", "Records", &s.records, batchRecordLabels, 280)
					if ui.PrimaryButton(c, "Calculate binding energies").Tooltip("Runs as a task; progress and Cancel show here and in the task list").
						Disabled(running > 0 || len(labels) == 0 || !hasReference).Clicked() {
						p.runBatchEnergy(sample, engine, filtered)
					}
				})
				note := engine[2] + batchEstimate(engine[0], s.counts[p.batchCountKey(sample)], s.cpus)
				if strings.HasPrefix(engine[0], "cplcofold") {
					note += " For many records, filter first or use the cplcofold command."
				}
				if filtered {
					note += " Records: " + nameOr(describeFilters(p.ix.applied), "the Interactions filters (none set: every record)") + "."
				}
				ui.Text(c, note).FontSize(FontSmall).TextColor(t.TextMuted)
				status := "Unmapped, antisense, ambiguous, over-limit or unselected rows retain their original energy and are listed in the audit."
				if !hasReference {
					status = batchNoReference
				}
				ui.Text(c, status).FontSize(FontSmall).TextColor(t.TextMuted)
				if s.err != "" {
					errorLine(c, s.err)
				}
			})
			p.batchEnergyResultCard(c)
		})
	})
}

func (p *explorePage) runBatchEnergy(sample string, engine [3]string, filtered bool) {
	s := &p.be
	parameters := map[string]any{"engine": engine[0], "records": "all"}
	if filtered {
		parameters["records"], parameters["filters"] = "filtered", p.ix.applied.payload()
	}
	label := "Binding energies · " + sample + " · " + engine[0]
	if smp := p.a.Project.Sample(sample); smp != nil {
		label = "Binding energies · " + smp.Label() + " · " + engine[0]
	}
	s.err = ""
	p.a.StartTask(core.TaskStartRequest{Kind: "batch_energy", SampleID: &sample, Parameters: parameters}, label, func(_ model.Task, err error) {
		if err != nil {
			s.err = "Could not start: " + ShortError(err.Error())
		}
	})
}

// latestBatchEnergy is the newest succeeded batch_energy task with an output.
func (p *explorePage) latestBatchEnergy() (model.Task, string) {
	for _, task := range p.a.TaskList() {
		if task.Kind != "batch_energy" || task.Status != "succeeded" {
			continue
		}
		if output, _ := task.Output.(string); output != "" {
			return task, output
		}
	}
	return model.Task{}, ""
}

func (p *explorePage) batchEnergyResultCard(c *ui.Context) {
	s := &p.be
	t := c.Theme()
	task, output := p.latestBatchEnergy()
	if output == "" {
		return
	}
	if s.resultFor != task.TaskID && !s.loading {
		s.loading = true
		id := task.TaskID
		p.a.Go(func() func() {
			res, err := readBatchEnergyResult(filepath.Join(filepath.Dir(output), "result.json"))
			return func() {
				s.loading, s.resultFor, s.result = false, id, res
				if err != nil {
					s.result = &batchEnergyResult{Message: "Could not read the result: " + ShortError(err.Error())}
				}
			}
		})
	}
	res := s.result
	if res == nil || s.resultFor != task.TaskID {
		return
	}
	Card(c, "", func() {
		ui.Row(c).Gap(Space2).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				ui.Text(c, p.a.TaskLabel(task)).FontSize(FontTitle).Bold()
				ui.Text(c, "Finished "+FormatTime(task.FinishedAt)+" · "+task.TaskID).FontSize(FontSmall).TextColor(t.TextMuted)
			})
			base := batchEnergyBase(res)
			if path := res.Outputs["hyb"]; path != "" && ui.Button(c, "Save updated HYB…").Clicked() {
				saveExport(p.a, "Save the updated HYB", base+".hyb", func() ([]byte, error) { return os.ReadFile(path) })
			}
			if path := res.Outputs["zip"]; path != "" && ui.Button(c, "Save HYB + audit ZIP…").Tooltip("energies.hyb, audit.tsv and parameters.json").Clicked() {
				saveExport(p.a, "Save the HYB and its audit", base+".zip", func() ([]byte, error) { return os.ReadFile(path) })
			}
		})
		if res.Message != "" {
			ui.Text(c, res.Message).FontSize(FontSmall)
		}
		if dir := filepath.Dir(output); dir != "" {
			ui.Text(c, "Files: "+strings.Join([]string{"energies.hyb", "audit.tsv", "parameters.json", batchEnergyBase(res) + ".zip"}, ", ")+" in "+dir).
				FontSize(11).TextColor(t.TextMuted).Selectable()
		}
	})
}

// batchEnergyBase names the saved files as the web's downloads.
func batchEnergyBase(res *batchEnergyResult) string {
	return "hyb2-energies-" + nameOr(res.Summary.Engine, "rnacofold")
}

func readBatchEnergyResult(path string) (*batchEnergyResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var res batchEnergyResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("result.json: %w", err)
	}
	return &res, nil
}
