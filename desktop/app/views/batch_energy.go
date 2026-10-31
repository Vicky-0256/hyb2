package views

// Explore › Binding energies: the web's "HYB binding energies" batch tool
// (web/batch-tools.js). Column 3 of one sample's .hyb (the ΔG of the two
// arms) is recalculated for all its records, or for those matching the
// Interactions filters, with RNAcofold, CPLcofold or CPLcofold with
// pseudoknots, as a `batch_energy` task (desktop/runtime/batch_energy.py).
// The task writes energies.hyb, audit.tsv, parameters.json and a ZIP of the
// three to its run folder; the sample's own file is not changed.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// batchEngines are the web's engines: value, label and what to know.
var batchEngines = [][3]string{
	{"rnacofold", "RNAcofold (ViennaRNA), as the HYB2 pipeline", "The HYB2 pipeline's method; combined arms up to 2,000 nt."},
	{"cplcofold", "CPLcofold, nested (Turner energies)", "Same Turner energy model as RNAcofold, beam search (DP09, beam 20, energy delta 5, 3 phase-1 candidates); combined arms up to 500 nt. Slower: filter first for large files."},
	{"cplcofold-pk", "CPLcofold with pseudoknots (experimental score)", "Adds CPLfold's pseudoknot phase: its DP09 pseudoknot term is an experimental ranking score, not a validated free energy; combined arms up to 500 nt."},
}

var batchRecordLabels = []string{"All records of the sample", "Records matching the Interactions filters"}

type batchEnergyState struct {
	sample, engine, records string
	err                     string
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
	labels := make([]string, 0, len(p.ids))
	ids := map[string]string{}
	for _, id := range p.ids {
		label := id
		if s := p.a.Project.Sample(id); s != nil {
			label = s.Label()
		}
		if _, taken := ids[label]; taken {
			label += " (" + id + ")"
		}
		labels = append(labels, label)
		ids[label] = id
	}
	return labels, ids
}

func (p *explorePage) batchEnergyView(c *ui.Context) {
	s := &p.be
	t := c.Theme()
	labels, ids := p.batchSampleLabels()
	if s.engine == "" {
		s.engine, s.records = batchEngines[0][1], batchRecordLabels[0]
	}
	if _, ok := ids[s.sample]; !ok && len(labels) > 0 {
		s.sample = labels[0]
	}
	engine := batchEngineOf(s.engine)
	filtered := s.records == batchRecordLabels[1]
	ui.Scroll(c.Key("be-scroll")).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			Card(c, "HYB binding energies", func() {
				ui.Text(c, "Recalculate column 3 (ΔG of the two arms, kcal/mol, 37 °C) of one sample's .hyb from its reference's arm sequences. "+
					"The task writes an updated HYB, an audit and its parameters to its run folder; the sample's file and index stay unchanged.").
					FontSize(12).TextColor(t.TextMuted)
				running := cmpTaskRows(c, p.a, "batch_energy")
				ui.Row(c).Gap(12).Wrap().AlignItems(ui.End).Children(func() {
					picker(c, "be-sample", "Sample", &s.sample, labels, 180)
					names := make([]string, len(batchEngines))
					for i, e := range batchEngines {
						names[i] = e[1]
					}
					picker(c, "be-engine", "Engine", &s.engine, names, 300)
					picker(c, "be-records", "Records", &s.records, batchRecordLabels, 280)
					if ui.PrimaryButton(c, "Calculate binding energies").Tooltip("Runs as a task; progress and Cancel show here and in the task list").
						Disabled(running > 0 || len(labels) == 0).Clicked() {
						p.runBatchEnergy(ids[s.sample], engine, filtered)
					}
				})
				note := engine[2]
				if filtered {
					note += " Records: " + nameOr(describeFilters(p.ix.applied), "the Interactions filters (none set: every record)") + "."
				}
				ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, "Unmapped, antisense, ambiguous, over-limit or unselected rows keep their energy and are listed in the audit. Comments and unparsed lines are kept as they are.").
					FontSize(12).TextColor(t.TextMuted)
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
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				ui.Text(c, p.a.TaskLabel(task)).FontSize(16).Bold()
				ui.Text(c, "Finished "+FormatTime(task.FinishedAt)+" · "+task.TaskID).FontSize(12).TextColor(t.TextMuted)
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
			ui.Text(c, res.Message).FontSize(12)
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
