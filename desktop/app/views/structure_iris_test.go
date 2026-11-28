package views

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestStructureIrisKRange(t *testing.T) {
	raw, err := os.ReadFile("testdata/iris_k_range.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		ValidText   map[string][]int
		InvalidText []string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	cases.ValidText[""] = []int{4, 5, 6, 7} // task default, not a valid catalog string
	for text, want := range cases.ValidText {
		if got, msg := stIrisParseStems(text); msg != "" || !slices.Equal(got, want) {
			t.Errorf("%q: got %v %q, want %v", text, got, msg, want)
		}
	}
	for _, bad := range cases.InvalidText {
		if _, msg := stIrisParseStems(bad); msg != "IRIS stem length must be an integer from 2 to 12." {
			t.Errorf("%q: %q", bad, msg)
		}
	}
}

func TestStructureIrisHelpers(t *testing.T) {
	what, next, ok := stIrisExplain("No complete HYB arm pair is inside G:1-200. Both arms of each row must fit inside the same region (IRIS limit: 1,000 nt).")
	if !ok || !strings.HasPrefix(what, "No complete HYB arm pair") || !strings.Contains(next, "nearby HYB-supported region") {
		t.Fatalf("explain %q %q %v", what, next, ok)
	}
	if _, next, ok := stIrisExplain("IRIS desktop limit: more than 1,000 constrained folds. Select a smaller region or a higher support quantile."); !ok ||
		!strings.Contains(next, "support quantile") {
		t.Fatalf("folds %q", next)
	}
	if _, _, ok := stIrisExplain("exceeds the CPLfold limit"); ok {
		t.Fatal("CPLfold error read as IRIS")
	}
	ir := &stIris{Members: []stIrisMember{{Weight: 0.7074822853495175, Coefficient: 0.6081574811014534, Energy: -29.700000762939453, DotBracket: "((..))"},
		{Weight: 0.2925177146504824, Coefficient: 0.25, Energy: -33.4, DotBracket: "......"}}}
	if got := string(webTSV(stIrisRows(ir, 1))); got != "member\tselected\tweight\tcoefficient\tenergy_kcal_per_mol\tdot_bracket\n"+
		"1\tno\t0.7074822853495175\t0.6081574811014534\t-29.700000762939453\t((..))\n2\tyes\t0.2925177146504824\t0.25\t-33.4\t......\n" {
		t.Fatalf("tsv %q", got)
	}
}

// The form: IRIS is an engine of the reference region, with the web's
// options, checks, recovery block and run label.
func TestStructureIrisForm(t *testing.T) {
	a, f := openCS(t)
	f.queries["analysis_iris_region"] = map[string]any{
		"counts":     map[string]any{"total": 900, "sameRna": 850, "forward": 850, "contained": 0, "tooWide": 3},
		"suggestion": map[string]any{"rna": africaGene, "start": 3001, "end": 4000},
		"message":    "No complete HYB arm pair is inside " + africaGene + ":1-200. Both arms of each row must fit inside the same region (IRIS limit: 1,000 nt).",
	}
	tt := ui.NewTester(a.View, 1100, 2400)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	p.gene = africaGene
	p.setSource(stSourcePaste)
	p.engine = engineIRIS
	p.engineChanged()
	if p.src.kind != stSourceReference {
		t.Fatalf("source %q", p.src.kind)
	}
	p.mode, p.windows = "windows", "1-200"
	csSettle(a, tt)
	for _, text := range []string{"IRIS · for comparison", "IRIS source", "Licence", "Predict IRIS ensemble",
		"IRIS requires HYB evidence inside the selected reference region.",
		"HYB evidence: 900 loaded; 850 with both arms on this RNA; 850 with forward coordinates; 0 fully inside this region.",
		"Use nearby HYB-supported region " + africaGene + ":3001-4000",
		"K 2 · C 20 · support quantile 0.75 · stem lengths 4 5 6 7 · 37 °C · defaults"} {
		if !tt.HasText(text) {
			t.Fatalf("form lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "structure_iris_form")
	f.queries["analysis_iris_region"] = map[string]any{
		"counts": map[string]any{"total": 900, "sameRna": 850, "forward": 850, "contained": 12, "tooWide": 3}, "message": ""}
	if err := tt.Click("Use nearby HYB-supported region " + africaGene + ":3001-4000"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	if p.windows != "3001-4000" || p.mode != "windows" || !tt.HasText("Region updated. Review the interval, then run IRIS.") {
		t.Fatalf("windows %q", p.windows)
	}
	pl := p.plan()
	if len(pl.problems) != 0 {
		t.Fatalf("problems %q", pl.problems)
	}
	// the desktop limits and the web's option checks
	p.windows = "1-1500"
	if pl := p.plan(); !slices.Contains(pl.problems, "IRIS desktop limit is 1,000 nt; select a smaller region.") {
		t.Fatalf("limit %q", pl.problems)
	}
	p.windows = "1-300\n400-600"
	if pl := p.plan(); !slices.Contains(pl.problems, "IRIS uses one reference region: enter one window.") {
		t.Fatalf("one window %q", pl.problems)
	}
	p.windows, p.iris.k, p.iris.c, p.iris.stems = "3001-4000", 3, 2, "4 20"
	pl = p.plan()
	if !slices.Contains(pl.problems, "IRIS candidate count C must be an integer from 3 to 50.") ||
		!slices.Contains(pl.problems, "IRIS stem length must be an integer from 2 to 12.") {
		t.Fatalf("options %q", pl.problems)
	}
	// Reset must use the installed catalog, not a second set of constants in the form.
	previous := engineSettings(engineIRIS)
	t.Cleanup(func() { stKernels.Engines[engineIRIS] = previous })
	updated := previous
	updated.Defaults.K, updated.Defaults.C, updated.Defaults.Fraction, updated.Defaults.KRange = 1, 9, 0.5, "3 5"
	stKernels.Engines[engineIRIS] = updated
	p.advancedOpen = true
	csSettle(a, tt)
	if err := tt.Click("Reset to defaults"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	if !tt.HasText("K 1 · C 9 · support quantile 0.5 · stem lengths 3 5 · 37 °C · defaults") {
		t.Fatal("reset did not display the catalog defaults")
	}
	p.iris.stems = ""
	got := p.plan().req.Parameters.(map[string]any)["kRange"]
	if lengths, ok := got.([]int); !ok || !slices.Equal(lengths, []int{3, 5}) {
		t.Errorf("empty form field should submit catalog defaults [3 5], got %#v", got)
	}
	p.iris.stems = "4,"
	p.runFold()
	csSettle(a, tt)
	if started := f.startedKinds(); len(started) != 0 {
		t.Fatalf("malformed stem list started a task: %+v", started)
	}
	p.iris.stems = "7, 5,5  4"
	if pl := p.plan(); len(pl.problems) != 0 {
		t.Fatalf("problems before running %q", pl.problems)
	}
	// (the toast above covers the button in the tester: run it as the button does)
	p.runFold()
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 {
		t.Fatalf("expected one IRIS request, got %+v", started)
	}
	req := started[0]
	params, _ := json.Marshal(req.Parameters)
	if req.Engine == nil || *req.Engine != "iris" || *req.Mode != "windows" || len(req.Windows) != 1 || req.Windows[0].Start != 3001 ||
		string(params) != `{"C":9,"K":1,"fraction":0.5,"kRange":[4,5,7]}` {
		t.Fatalf("request %+v %s", req, params)
	}
	if title := a.TaskLabel(a.Tasks[p.lastTask]); !strings.HasPrefix(title, "Structure (IRIS)") {
		t.Fatalf("title %q", title)
	}
}

// The result: the weighted ensemble table switches the member shown in
// every view; metrics, hard pairs, the download and the report follow.
func TestStructureIrisResult(t *testing.T) {
	a, f := openCS(t)
	read := fixture(t, "structure_read.json")
	record := read["records"].([]any)[0].(map[string]any)
	dbn := record["dbn"].(string)
	second := []byte(strings.Repeat(".", len(dbn)))
	for k := 0; k < 4; k++ {
		second[10+k], second[40-k] = '(', ')'
	}
	record["candidates"] = []any{
		map[string]any{"rank": 1, "type": "IRIS ensemble member", "topology": "nested", "energy": -452.02, "structure": dbn, "pairs": 1055},
		map[string]any{"rank": 2, "type": "IRIS ensemble member", "topology": "nested", "energy": -440.5, "structure": string(second), "pairs": 4},
	}
	record["selected_candidate"] = 0
	meta := read["meta"].(map[string]any)
	meta["engine"] = "IRIS"
	meta["parameters"] = map[string]any{"engine": "IRIS", "K": 2, "C": 20, "fraction": 0.75, "kRange": "4 5 6 7", "temperature": 37, "min_loop": 3}
	meta["records"] = []any{map[string]any{"iris": map[string]any{
		"engineVersion": "1.0-desktop.1", "model": "IRIS desktop adaptation", "parameters": map[string]any{"K": 2, "C": 20, "fraction": 0.75, "kRange": []any{4, 5, 6, 7}},
		"ensemble": map[string]any{"meanEnergy": -448.649, "meanSupport": 0.78, "lambda": 67.7, "testedEnsembles": 190, "candidateCount": 20, "generatedCount": 155},
		"evidence": map[string]any{"inputRecords": 65, "stemCount": 21}, "warnings": []any{"Desktop adaptation of the web's browser IRIS."},
		"selectedMember": 0,
		"members": []any{
			map[string]any{"member": 1, "weight": 0.7074822853495175, "coefficient": 0.61, "energy": -452.02, "dotBracket": dbn, "constraints": []any{}},
			map[string]any{"member": 2, "weight": 0.2925177146504824, "coefficient": 0.25, "energy": -440.5, "dotBracket": string(second),
				"constraints": []any{[]any{11, 41}, []any{12, 40}}},
		}}}}
	f.structures[structA] = read
	tt := ui.NewTester(a.View, 1100, 2400)
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	for _, text := range []string{"IRIS weighted ensemble", "Select a member to update every structure view. Weights describe the fitted mixture, not confidence.",
		"70.7%", "29.3%", "-452.02", "-440.50", "Selected", "View member 2", "Weighted ensemble TSV…",
		"Ensemble mean energy: -448.65 kcal/mol · 20 candidates · 190 ensembles tested", "Constraint mode", "IRIS ensemble", "0 enforced pairs", "Energy"} {
		if !tt.HasText(text) {
			t.Fatalf("result lacks %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("CPLfold candidates") {
		t.Fatal("CPLfold candidates shown for IRIS")
	}
	snapshot(t, tt, "structure_iris_result")
	if err := tt.Click("View member 2"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	a2 := p.cache[structA]
	if got := a2.Records[0]; got.SelectedCandidate != 1 || got.DBN != string(second) || len(p.arcs.A) != 4 {
		t.Fatalf("selected %d, %d arcs", got.SelectedCandidate, len(p.arcs.A))
	}
	hard := stHardPairs(a2)
	if len(hard) != 2 || !hard[[2]int{3170 + 10, 3170 + 40}] {
		t.Fatalf("hard pairs %v", hard)
	}
	if !tt.HasText("2 enforced pairs") || !tt.HasText("IRIS ensemble member 2 selected.") {
		t.Fatalf("after selection: %q", tt.Texts())
	}
	snapshot(t, tt, "structure_iris_member2")
	report, _ := json.Marshal(p.irisReport(a2))
	if !strings.Contains(string(report), `"selectedMember":1`) || !strings.Contains(string(report), `"testedEnsembles":190`) {
		t.Fatalf("report %s", report)
	}
	rows := stPairsRows(a2.Records, "IRIS", hard, false)
	if len(rows) != 4 || rows[1][5] != "yes" || rows[0][12] != "IRIS" || rows[0][8] != "none" {
		t.Fatalf("pairs rows %q", rows)
	}
	if line := p.metaLine(a2); !strings.Contains(line, "Engine IRIS · weighted ensemble, K 2, C 20, support quantile 0.75, stem lengths 4 5 6 7") {
		t.Fatalf("meta line %q", line)
	}
}
