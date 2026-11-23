package views

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/model"
	"hyb2desktop/seqmap"
	"hyb2desktop/widgets"
)

func TestExecutableFileFollowsGOOS(t *testing.T) {
	dir := t.TempDir()
	exe := writeFile(t, filepath.Join(dir, "mfold.exe"), 0o644)
	bare := writeFile(t, filepath.Join(dir, "mfold"), 0o755)
	t.Setenv("PATHEXT", "")
	setGOOS(t, "windows")
	if !executableFile(exe) || executableFile(bare) || executableFile(dir) {
		t.Fatalf("windows: exe %v, bare %v", executableFile(exe), executableFile(bare))
	}
	t.Setenv("PATHEXT", ".COM;.BAT")
	if executableFile(exe) {
		t.Fatal("windows: .exe outside PATHEXT")
	}
	setGOOS(t, "linux")
	if executableFile(exe) || !executableFile(bare) {
		t.Fatalf("linux: exe %v, bare %v", executableFile(exe), executableFile(bare))
	}
}

// writeFile writes a file of the given mode and returns its path.
func writeFile(t *testing.T, path string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// The RNAduplex kernel runs in the bundled ViennaRNA: CPLfold and CPLcofold
// send only its name (no program path), with PATH empty, and the engines
// without a support kernel send nothing.
func TestStructureDuplexKernelNeedsNoProgram(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	t.Setenv("PATH", t.TempDir())
	p.gene, p.mode, p.windows = africaGene, "windows", "3001-3400"
	p.engine, p.kernel = engineCPLfold, "duplex"
	pl := p.plan()
	params := pl.req.Parameters.(map[string]any)
	if len(pl.problems) != 0 || params["support_kernel"] != "duplex" {
		t.Fatalf("cplfold %q %v", pl.problems, params)
	}
	for key := range params {
		if key == "hybrid_min" || key == "hybridMin" { // legacy-name: parameter removed with RNAduplex
			t.Fatalf("cplfold sends %s: %v", key, params)
		}
	}
	for _, engine := range []string{engineVienna, engineComrades, engineIRIS} {
		p.engine = engine
		p.engineChanged()
		pl := p.plan()
		if params := pl.req.Parameters.(map[string]any); params["support_kernel"] != nil {
			t.Fatalf("%s parameters %v", engine, params)
		}
	}
}

func TestStructureReadSupportPicker(t *testing.T) {
	project := &model.Project{
		References: []model.Reference{{ID: "ref", Sequences: []model.Sequence{{Name: "toy", Length: 60}}}},
		Samples:    []model.Sample{{ID: "test", ReferenceID: "ref", Ready: true}},
	}
	p := newStructurePage(&App{Project: project})
	p.gene, p.evidence, p.region1, p.region2 = "toy", "s:test", "1-20", "41-60"
	tt := ui.NewTester(p.form, 900, 2200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("Advanced settings")
	for _, engine := range []string{engineCPLfold, engineCPLcofold} {
		if engine == engineCPLcofold {
			click("CPLcofold, two regions as two strands (one RNA or two RNAs)")
		}
		if !tt.HasText("Gaussian around arm centres · IRIS (default)") {
			t.Fatalf("%s did not show the default kernel", engine)
		}
		// Stale state must block a run, not appear selected as Gaussian.
		p.kernel = "rnacofold"
		tt.Frame()
		if pl := p.plan(); !tt.HasText("Choose read support") || !strings.Contains(strings.Join(pl.problems, " "), `Read support "rnacofold" is unavailable`) {
			t.Fatalf("%s stale selection: %v", engine, pl.problems)
		}
		p.seqOnly = true
		if pl := p.plan(); len(pl.problems) != 0 || pl.req.Parameters.(map[string]any)["support_kernel"] != nil {
			t.Fatalf("%s sequence-only request retained its hidden stale kernel: params=%v problems=%v", engine, pl.req.Parameters, pl.problems)
		}
		p.seqOnly = false
		for _, option := range []struct{ label, kernel string }{
			{"Full arm blocks", "block"}, {"Full arm blocks ÷ √(arm lengths)", "block-normalized"},
			{"RNAduplex pairs, saturating", "duplex"}, {"COMRADES (RNAcofold), saturating", "comrades"},
			{"Gaussian around arm centres · IRIS (default)", "gaussian"},
		} {
			click("Read support")
			if tt.HasText("RNAcofold pairs of each read") || tt.HasText("pairs RNAcofold forms") {
				t.Fatalf("%s still offers RNAcofold read support", engine)
			}
			if option.kernel == "block-normalized" {
				snapshot(t, tt, engine+"-read-support-picker")
			}
			click(option.label)
			tt.Frame()
			pl := p.plan()
			if p.engine != engine || len(pl.problems) != 0 || pl.req.Parameters.(map[string]any)["support_kernel"] != option.kernel {
				t.Fatalf("%s choosing %q: engine %s, request %v, problems %v", engine, option.label, p.engine, pl.req.Parameters, pl.problems)
			}
		}
	}
}

func TestStructureInjectedKernelStartsTask(t *testing.T) {
	previous := stKernels
	t.Cleanup(func() { stKernels = previous })
	stKernels = parseSupportKernels([]byte(`{"defaultKernel":"test-default","options":[
		{"value":"test-default","label":"Web default","desktopLabel":"Desktop default"},
		{"value":"test-extra","label":"Additional pair model","pairModel":"RNAduplex"}]}`))
	stKernels.Engines = previous.Engines
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 2200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	a.Navigate("structure", nil)
	csSettle(a, tt)
	if err := tt.Click("Advanced settings"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Desktop default") {
		t.Fatal("picker did not show the catalog default's desktop label")
	}
	for _, control := range []string{"Read support", "Additional pair model", "Run fold"} {
		if err := tt.Click(control); err != nil {
			t.Fatal(err)
		}
	}
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || started[0].Parameters.(map[string]any)["support_kernel"] != "test-extra" {
		t.Fatalf("unexpected task payload: %+v", started)
	}
	result := structureResult{}
	result.Meta.Parameters = started[0].Parameters.(map[string]any)
	candidate := stCandidate{SupportKernel: "test-extra"}
	if got := candidate.estimator(); got != "RNAduplex" || stBonusLabel(&result) != "saturating bonus" {
		t.Fatalf("injected pair model: estimator %q, bonus %q", got, stBonusLabel(&result))
	}
}

func TestSavedRNAcofoldKernelKeepsItsResultLabel(t *testing.T) {
	var r structureResult
	r.Meta.Engine = "CPLfold"
	r.Meta.Parameters = map[string]any{"support_kernel": "rnacofold", "alpha": 0.5}
	if line := (&structurePage{}).metaLine(&r); !strings.Contains(line, "RNAcofold pair read support") {
		t.Fatalf("saved result mislabeled: %s", line)
	}
}

func TestStructureRawPairScoreViewer(t *testing.T) {
	for _, tc := range []struct{ kernel, estimator string }{{"duplex", "RNAduplex"}, {"comrades", "RNAcofold"}} {
		t.Run(tc.kernel, func(t *testing.T) {
			a := &structureResult{}
			a.Meta.Engine, a.Meta.GeneID = "CPLfold", "toy"
			a.Meta.Parameters = map[string]any{"support_kernel": tc.kernel}
			score, zero := 12, 0
			a.Records = []structureRecord{{WindowStart: 101, WindowEnd: 112, Sequence: "GGGGAAAACCCC", Candidates: []stCandidate{
				{Structure: "(((......)))", Pairs: 3, Type: "phase1", Topology: "nested", Energy: stFloat(-3), Bonus: stFloat64s(2.5, 1.5, 0),
					SupportKernel: tc.kernel, ComradesScore: &score, ComradesPairCounts: []int{5, 1, 0}},
				{Structure: "..(......)..", Pairs: 1, Type: "phase1", Topology: "nested", Energy: stFloat(-1), Bonus: stFloat64s(0),
					SupportKernel: tc.kernel, ComradesScore: &zero, ComradesPairCounts: []int{0}},
			}}}
			r := &a.Records[0]
			if !r.selectCandidate(0) {
				t.Fatal("missing candidate")
			}
			p := newStructurePage(&App{Project: &model.Project{}})
			p.arcs.Selected = 102
			tt := ui.NewTester(func(c *ui.Context) {
				p.candidatesPanel(c, a)
				p.resultMetrics(c, a, a.Records, stShownPairs(a.Records, nil), len(r.Sequence))
				v, ok := p.stStructView(c, a, widgets.ModeMatrix, c.Theme(), true)
				if ok {
					v.Show(c.Key("toy-matrix"), 310, stColors(c.Theme()), "Candidate pair support")
				}
				p.nucleotideInspector(c, a, a.Records)
			}, 1000, 780)
			tt.Frame()
			if !tt.HasText("COMRADES score") || !tt.HasText(tc.estimator+" · up to 1M per paired endpoint") || !tt.HasText("12") {
				t.Fatal(tt.Texts())
			}
			if got := stStagesOf(a, a.Records)[1][2]; !strings.Contains(got, "saturating bonus") {
				t.Fatalf("matrix caption: %s", got)
			}
			if text := stNucleotideText(a.Records, 102, "CPLfold"); !strings.Contains(text, tc.estimator+" count 1 · HYB bonus 1.5") {
				t.Fatal(text)
			}
			th := ui.LightTheme()
			marks := stViewMarks(a, a.Records, stShownPairs(a.Records, nil), 101, 12, th)
			if marks[0] != th.Warning || marks[11] != th.Warning || marks[1] != th.Accent || marks[10] != th.Accent || marks[2] != th.TextMuted || marks[5] != th.TextMuted {
				t.Fatalf("marks came from saturated bonuses instead of counts: %v", marks)
			}
			source, err := stDrawingSource(a, *r)
			if err != nil || !slices.Equal(source.Support, []float64{5, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 5}) || source.evidence().Kind != tc.estimator+" supporting observations" {
				t.Fatalf("drawing: %+v, %v", source, err)
			}
			snapshot(t, tt, tc.kernel+"-candidate-score")
			if err := tt.Click("#2"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if r.SelectedCandidate != 1 || !tt.HasText("0") || !strings.Contains(stNucleotideText(a.Records, 103, "CPLfold"), tc.estimator+" count 0") {
				t.Fatalf("zero-score candidate: %v", tt.Texts())
			}
			source, err = stDrawingSource(a, *r)
			if err != nil {
				t.Fatal(err)
			}
			editor := newDrawingEditor(nil, source)
			if editor.data.Evidence == nil || *editor.data.Evidence.Values[2] != 0 || editor.data.Styles[3].Fill != "#ffffff" ||
				!strings.Contains(editor.message, "raw count of 0") || validateDrawing(editor.data, r.Sequence, r.DBN) != nil {
				t.Fatalf("measured zero lost in drawing: %s", editor.message)
			}
			rows := stCandidatesRows("toy", a.Records)
			if row := rows[2]; !slices.Equal(row[len(row)-2:], []string{tc.kernel, "0"}) {
				t.Fatalf("zero-score export: %v", rows)
			}
			pairs := stPairsTable(a.Records, "CPLfold", nil, true)
			if row := pairs[1]; !slices.Equal(pairs[0][len(row)-2:], []string{"support_kernel", "raw_pair_count"}) ||
				!slices.Equal(row[len(row)-2:], []string{tc.kernel, "0"}) || row[8] != "saturating_hyb_bonus" {
				t.Fatalf("pair export: %v", pairs)
			}
			report := make(map[string]any)
			p.stReportWeb(a, report)
			prediction := report["prediction"].(map[string]any)
			candidates := report["cplfoldCandidates"].([]map[string]any)
			if prediction["supportKernel"] != tc.kernel || prediction["comradesScore"] != 0 ||
				prediction["pairs"].([]map[string]any)[0]["rawPairCount"] != 0 ||
				!slices.Equal(candidates[0]["comradesPairCounts"].([]int), []int{5, 1, 0}) {
				t.Fatalf("score and count report: %v", report)
			}
		})
	}
}

func TestStructurePerWindowCountShading(t *testing.T) {
	// Both windows contain pair (1,8), but their counts differ. A projected
	// comparison drops (2,7) at a gap. Colours must travel with the pairs.
	scoreA, scoreB := 14, 0
	records := []structureRecord{
		{WindowStart: 1, WindowEnd: 8, Sequence: "GGAAAACC", Candidates: []stCandidate{
			{Structure: "((....))", ComradesScore: &scoreA, ComradesPairCounts: []int{5, 2}}}},
		{WindowStart: 1, WindowEnd: 9, Sequence: "GGAAAACCA", Candidates: []stCandidate{
			{Structure: "(......).", ComradesScore: &scoreB, ComradesPairCounts: []int{0}}}},
	}
	for i := range records {
		records[i].selectCandidate(0)
	}
	if got := stShadeWeights(records); !slices.Equal(got, []float64{2, 5, 0}) {
		t.Fatalf("overlapping windows: %v", got)
	}
	mapped, dropped := stMapRecords(records, &seqmap.Map{To: []int32{2, 0, 3, 4, 5, 6, 7, 8, 9}})
	if got := stShadeWeights(mapped); dropped != 1 || !slices.Equal(got, []float64{5, 0}) {
		t.Fatalf("projected windows: dropped %d, counts %v", dropped, got)
	}
	// Standalone COMRADES stores its raw counts in Bonus, not Candidates.
	records[0].Candidates, records[1].Candidates = nil, nil
	records[0].Bonus, records[1].Bonus = stFloat64s(5, 2), stFloat64s(0)
	if got := stShadeWeights(records); !slices.Equal(got, []float64{2, 5, 0}) {
		t.Fatalf("standalone window shading: %v", got)
	}
	rows := stPairsRows(records, "COMRADES(ViennaRNA)", nil, true)
	if rows[0][6] != "5" || rows[1][6] != "0" || rows[2][6] != "2" {
		t.Fatalf("standalone window export: %v", rows)
	}
}
