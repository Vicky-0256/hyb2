package views

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

const (
	structA    = "structure-1791990427292"
	structB    = "structure-b"
	africaGene = "ZIKV_African_KX377335-1_virusRNA"
)

// structureB derives a second result from the real one: every third pair
// opened (so A has 1055 pairs, B fewer, all of them shared), with BRA as
// its evidence.
func stFixtureB(t *testing.T) (core.Obj, core.Obj, int) {
	read := fixture(t, "structure_read.json")
	records := read["records"].([]any)
	record := records[0].(map[string]any)
	dbn := []byte(record["dbn"].(string))
	pairs := stParseDBN(string(dbn), 1)
	removed := 0
	for i, pr := range pairs {
		if i%3 == 0 {
			dbn[pr.I-1], dbn[pr.J-1] = '.', '.'
			removed++
		}
	}
	record["dbn"] = string(dbn)
	record["pairs"] = []any{}
	entry := read["structure"].(map[string]any)
	entry["id"], entry["task_id"] = structB, structB
	entry["sample_ids"] = []any{"ZBRA_REP1", "ZBRA_REP2"}
	entry["created_at"] = "2026-10-14T16:00:00Z"
	list := fixture(t, "structure_list.json")
	items := list["structures"].([]any)
	raw, _ := json.Marshal(entry)
	var copied map[string]any
	_ = json.Unmarshal(raw, &copied)
	list["structures"] = append([]any{copied}, items...)
	return read, list, removed
}

func TestStructureViewerAndCompare(t *testing.T) {
	a, f := openCS(t)
	readB, listAB, removed := stFixtureB(t)
	f.structures[structB] = readB
	f.ops["list_structures"] = listAB
	tt := ui.NewTester(a.View, 1100, 900)
	// The tasks drawer's "View structure".
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.viewA != structA || a.NavOptions != nil {
		t.Fatalf("viewA %q", p.viewA)
	}
	for _, text := range []string{"New fold", "Results", "ZIKV_African_KX377335-1_virusRNA · 1 window", "Evidence: AFR_REP1 + AFR_REP2 · " + FormatTime("2026-10-14T15:07:24Z"),
		"Viewer", "A · AFR_REP1 + AFR_REP2", "Length (nt)", "3,060", "1,055", "-452.0 kcal/mol", "Dot-bracket (A)"} {
		if !tt.HasText(text) {
			t.Fatalf("structure page lacks %q: %q", text, tt.Texts())
		}
	}
	if len(p.arcs.A) != 1055 || p.arcs.Start != 3170 || p.arcs.End != 6229 || p.arcs.Length != 10807 {
		t.Fatalf("arcs %d pairs, %v-%v of %d", len(p.arcs.A), p.arcs.Start, p.arcs.End, p.arcs.Length)
	}
	snapshot(t, tt, "structure")
	// Copy the dot-bracket.
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); !strings.HasPrefix(got, ">ZIKV_African_KX377335-1_virusRNA:3170-6229 -452.02\n") {
		t.Fatalf("clipboard %.80q", got)
	}
	// Compare as B: shared pairs and pairs only in A / B.
	if err := tt.Click("Compare as B"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	if p.viewB != structB {
		t.Fatalf("viewB %q", p.viewB)
	}
	s := p.stats
	if s.PairsA != 1055 || s.PairsB != 1055-removed || s.Shared != 1055-removed || s.OnlyA != removed || s.OnlyB != 0 {
		t.Fatalf("stats %+v (removed %d)", s, removed)
	}
	for _, text := range []string{"Shared pairs", "Only in this structure", "Pair agreement (Jaccard)", "B · ZBRA_REP1 + ZBRA_REP2",
		FormatNumber(int64(1055 - removed)), FormatNumber(int64(removed)), "Clear B"} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	if p.arcs.Shared() != 1055-removed {
		t.Fatalf("arcs shared %d", p.arcs.Shared())
	}
	snapshot(t, tt, "structure-ab")
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "structure-ab-dark")
	tt.SetDark(false)
	if err := tt.Click("Clear B"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.viewB != "" {
		t.Fatal("B not cleared")
	}
}

func TestStructureFormPrefillAndRun(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 900)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.mode != "genome" || p.gene != africaGene || p.evidence != "c:SUB" || p.workers != 10 {
		t.Fatalf("form %q %q %q workers %v", p.mode, p.gene, p.evidence, p.workers)
	}
	if !tt.HasText("Choose a result to display; add a second one with “Compare as B”.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	// Whole RNA.
	if err := tt.Click("Run fold"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || *started[0].Mode != "genome" || *started[0].GeneID != africaGene ||
		strings.Join(started[0].SampleIDs, ",") != "SUB_REP1,SUB_REP2" || *started[0].Workers != 10 || len(started[0].Windows) != 0 {
		t.Fatalf("started %+v", started)
	}
	params := started[0].Parameters.(map[string]any)
	if params["allow_pseudoknot"] != false || params["alpha"] != 0.5 || params["beam"] != 100 {
		t.Fatalf("params %+v", params)
	}
	for _, text := range []string{"Started. Progress is shown in the list and the task drawer.", "Structure · SUB (2 merged) · whole RNA", "Cancel"} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}

	// Prefill from Explore's "Fold this region".
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"gene": africaGene,
		"windows": []map[string]any{{"start": 3000, "end": 3400}}, "selectionKey": "c:BRA", "source": "the contact map"}})
	csSettle(a, tt)
	if p.mode != "windows" || !p.pseudoknot || p.windows != "3000-3400" || p.evidence != "c:BRA" {
		t.Fatalf("prefill %q %v %q %q", p.mode, p.pseudoknot, p.windows, p.evidence)
	}
	if !tt.HasText("Prefilled from the contact map. Adjust the window and press Run fold.") || !tt.HasText("Allow pseudoknots in windows") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "structure-prefill")

	// Validation of the window list.
	for text, want := range map[string]string{
		"abc":     "Not a window: abc",
		"1-10":    "1-10 is shorter than 20 nt",
		"1-20000": "1-20000 is outside 1-10807",
		"  \n  ":  "Add at least one window.",
	} {
		p.windows = text
		tt.Frame()
		_ = tt.Click("Run fold")
		tt.Frame()
		if p.formMsg != want || !tt.HasText(want) {
			t.Fatalf("%q: message %q", text, p.formMsg)
		}
	}
	p.windows = "3000-3400\n5000 – 5600"
	tt.Frame()
	_ = tt.Click("Run fold")
	csSettle(a, tt)
	started = f.startedKinds()
	if len(started) != 2 {
		t.Fatalf("started %d", len(started))
	}
	req := started[1]
	params = req.Parameters.(map[string]any)
	if *req.Mode != "windows" || strings.Join(req.SampleIDs, ",") != "ZBRA_REP1,ZBRA_REP2" || params["allow_pseudoknot"] != true ||
		len(req.Windows) != 2 || req.Windows[1] != (core.Window{Start: 5000, End: 5600}) {
		t.Fatalf("request %+v params %+v", req, params)
	}
	if a.TaskLabel(a.Tasks["structure-2"]) != "Structure · BRA (2 merged) · 2 windows" || p.prefillNote != "" {
		t.Fatalf("label %q note %q", a.TaskLabel(a.Tasks["structure-2"]), p.prefillNote)
	}
	// No evidence.
	p.evidence = "s:nobody"
	_ = tt.Click("Run fold")
	tt.Frame()
	if p.formMsg != "Choose analysed samples as evidence." {
		t.Fatalf("message %q", p.formMsg)
	}
}

func TestStructurePairFromCompare(t *testing.T) {
	a, f := openCS(t)
	readB, listAB, _ := stFixtureB(t)
	f.structures[structB] = readB
	tt := ui.NewTester(a.View, 1100, 900)
	// Only A is listed when the Structure page opens.
	a.Navigate("structure", map[string]any{"pair": []string{structA, structB}, "pairLabels": []string{"AFR (2 merged)", "BRA (2 merged)"}})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.viewA != structA || p.viewB != "" || len(p.pendingPair) != 2 {
		t.Fatalf("view %q %q pending %v", p.viewA, p.viewB, p.pendingPair)
	}
	if !tt.HasText("Folding AFR (2 merged) (A) and BRA (2 merged) (B); they show here as the folds finish.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	// B finishes: the list reloads and B shows below A.
	f.ops["list_structures"] = listAB
	a.Tasks[structB] = model.Task{TaskID: structB, Kind: "structure", Status: "running"}
	a.storeTask(model.Task{TaskID: structB, Kind: "structure", Status: "succeeded"})
	csSettle(a, tt)
	if p.viewA != structA || p.viewB != structB || p.pendingPair != nil {
		t.Fatalf("view %q %q pending %v", p.viewA, p.viewB, p.pendingPair)
	}
	if !tt.HasText("Shared pairs") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestParseWindowsAndCoverage(t *testing.T) {
	windows, errs := stParseWindows("1-400, 2700:2300; 50 90", 10807)
	if len(errs) != 0 || len(windows) != 3 || windows[1] != (core.Window{Start: 2300, End: 2700}) || windows[2] != (core.Window{Start: 50, End: 90}) {
		t.Fatalf("windows %+v errs %v", windows, errs)
	}
	// A window fold against a whole-RNA fold: pairs count inside the window only.
	whole := structureRecord{WindowStart: 1, WindowEnd: 40, Sequence: strings.Repeat("A", 40), DBN: "((((....))))" + strings.Repeat(".", 8) + "((((....))))" + strings.Repeat(".", 8)}
	window := structureRecord{WindowStart: 21, WindowEnd: 40, Sequence: strings.Repeat("A", 20), DBN: "((((....))))[[..]].."}
	_, _, s := stCompare([]structureRecord{whole}, []structureRecord{window}, true)
	if !s.DifferentCover || s.PairsA != 4 || s.PairsB != 6 || s.Shared != 4 || s.OnlyB != 2 || s.KnottedB != 2 || s.Jaccard != "0.667" {
		t.Fatalf("stats %+v", s)
	}
}

func TestStructureShadeByChimeraSupport(t *testing.T) {
	a, f := openFixture(t)
	f.ops["list_structures"] = fixture(t, "structure_list.json")
	p := a.PageFor("structure").(*structurePage)
	tt := ui.NewTester(a.View, 1280, 900)
	a.Navigate("structure", nil)
	waitFlush(a)
	tt.Frame()
	var result structureResult
	if err := model.Decode(fixture(t, "structure_read.json"), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Records) == 0 || len(result.Records[0].Bonus) != len(result.Records[0].Pairs) {
		t.Fatalf("fixture lacks bonus values")
	}
	id := result.Structure.ID
	p.cache[id] = &result
	p.show(id)
	tt.Frame()
	if len(p.arcs.WeightA) != len(p.arcs.A) || len(p.arcs.A) == 0 {
		t.Fatalf("weights %d for %d arcs", len(p.arcs.WeightA), len(p.arcs.A))
	}
	positive := 0
	for _, w := range p.arcs.WeightA {
		if w > 0 {
			positive++
		}
	}
	if positive == 0 {
		t.Fatal("no chimera-supported pair")
	}
	if err := tt.Click("Shade by chimera support"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !p.shade || !p.arcs.Shade {
		t.Fatal("shading not on")
	}
	snapshot(t, tt, "structure-shaded")
}
