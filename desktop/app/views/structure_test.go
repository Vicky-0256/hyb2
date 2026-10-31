package views

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
	"hyb2desktop/seqmap"
	"hyb2desktop/widgets"
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
	tt := ui.NewTester(a.View, 1100, 1400)
	// The tasks drawer's "View structure".
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.viewA != structA || a.NavOptions != nil {
		t.Fatalf("viewA %q", p.viewA)
	}
	for _, text := range []string{"New fold", "Results", "1 window · 3,060 nt (3,170–6,229)", "1,055 base pairs · -452.0 kcal/mol", "Evidence: AFR_REP1 + AFR_REP2 · " + FormatTime("2026-10-14T15:07:24Z"),
		"Viewer", "A · AFR_REP1 + AFR_REP2", "Length (nt)", "3,060", "1,055", "-452.0 kcal/mol", "Dot-bracket (A)"} {
		if !tt.HasText(text) {
			t.Fatalf("structure page lacks %q: %q", text, tt.Texts())
		}
	}
	if len(p.arcs.A) != 1055 || p.arcs.Start != 3170 || p.arcs.End != 6229 || p.arcs.Length != 10807 {
		t.Fatalf("arcs %d pairs, %v-%v of %d", len(p.arcs.A), p.arcs.Start, p.arcs.End, p.arcs.Length)
	}
	// The form estimates the whole-RNA fold (built-in rate, no history).
	for _, text := range []string{"the whole RNA, 10,807 nt, without pseudoknots", "about 19 s (rough; the first fold after installing also prepares the folding engine, about 1 min more)",
		"about 15.9 GB of the 24.0 GB tasks may use"} {
		if !tt.HasText(text) {
			t.Fatalf("no fold check %q: %q", text, tt.Texts())
		}
	}
	// Windows change it.
	p.mode, p.windows = "windows", "1-3000\n3001-6000"
	waitFlush(a)
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	found := false
	for _, text := range tt.Texts() {
		found = found || text == "about 4 s (rough; the first fold after installing also prepares the folding engine, about 1 min more)"
	}
	if !found {
		t.Fatalf("no windows estimate: %q", tt.Texts())
	}
	p.mode, p.windows = "genome", ""
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	snapshot(t, tt, "structure")
	// Copy the dot-bracket.
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	// the web's .dbn: the energy follows the structure, two decimals
	if got := tt.Clipboard(); !strings.HasPrefix(got, ">ZIKV_African_KX377335-1_virusRNA:3170-6229\n") || !strings.Contains(got, " (-452.02)\n") {
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
	// the web RNA Structure page's CPLfold defaults
	if params["allow_pseudoknot"] != false || params["alpha"] != 0.5 || params["beam"] != 20 || params["max_phase1"] != 3 ||
		params["energy_delta"] != 5.0 || params["beta"] != 0.0 || params["energy_model"] != "DP09" {
		t.Fatalf("params %+v", params)
	}
	// The fold's status stays on the page, with Cancel; Run waits for it.
	for _, text := range []string{"Folding…", "Cancel", "Fold running…", "Fold progress"} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	id := p.lastTask
	a.storeTask(model.Task{TaskID: id, Kind: "structure", Status: "succeeded"})
	csSettle(a, tt)
	if !tt.HasText("Fold finished") || !tt.HasText("Run fold") {
		t.Fatalf("after success %q", tt.Texts())
	}

	// Prefill from Explore's "Fold this region".
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"gene": africaGene,
		"windows": []map[string]any{{"start": 3000, "end": 3400}}, "selectionKey": "c:BRA", "source": "the contact map"}})
	csSettle(a, tt)
	// pseudoknots stay off, the web's default topology
	if p.mode != "windows" || p.pseudoknot || p.windows != "3000-3400" || p.evidence != "c:BRA" {
		t.Fatalf("prefill %q %v %q %q", p.mode, p.pseudoknot, p.windows, p.evidence)
	}
	if !tt.HasText("Prefilled from the contact map. Check the window, then press Run fold below.") || !tt.HasText("Allow pseudoknots in the windows") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "structure-prefill")

	// Evidence mapped to another reference than the RNA's: the check says
	// why and Run fold stays disabled.
	mismatch := "BRA (2 merged) is mapped to ZBRA, not to ZAFR (the reference of this RNA). Choose evidence mapped to ZAFR."
	if !tt.HasText("Cannot run yet:") || !tt.HasText("• "+mismatch) {
		t.Fatalf("no mismatch notice: %q", tt.Texts())
	}
	_ = tt.Click("Run fold")
	csSettle(a, tt)
	if len(f.startedKinds()) != 1 {
		t.Fatalf("started %d", len(f.startedKinds()))
	}
	p.gene = "ZIKV_Brazilian_MF073357-1_virusRNA"
	tt.Frame()
	if msg := p.evidenceMismatch(); msg != "" {
		t.Fatalf("mismatch for matching evidence: %q", msg)
	}

	// Validation of the window list.
	for text, want := range map[string]string{
		"abc":     "• Not a window: abc (windows are start-end, at least 20 nt).",
		"1-10":    "• 1-10 is shorter than 20 nt (windows are start-end, at least 20 nt).",
		"1-20000": "• 1-20000 is outside 1-10807 (windows are start-end, at least 20 nt).",
		"  \n  ":  "• Add at least one window, one per line as start-end within 1-10,807.",
	} {
		p.windows = text
		tt.Frame()
		if !tt.HasText(want) {
			t.Fatalf("%q: %q", text, tt.Texts())
		}
	}
	p.windows = "3000-3400\n5000 – 5600"
	tt.Frame()
	if !tt.HasText("2 windows: 401 + 601 nt") {
		t.Fatalf("window summary %q", tt.Texts())
	}
	// Keyboard: focus the window list, then Cmd+Enter runs the fold.
	r, ok := tt.Find("Windows: start-end, one per line (1-based, inclusive)")
	if !ok {
		t.Fatal("no window field")
	}
	// The form scrolls above the pinned check: bring the window list into view.
	if check, ok := tt.Find("Check before running"); ok && r.Y+r.H+40 > check.Y {
		// scroll over the form, above the pinned check
		tt.Scroll(r.X+20, min(r.Y, check.Y-40), 0, r.Y+r.H+40-check.Y+60)
		tt.Frame()
		if r, ok = tt.Find("Windows: start-end, one per line (1-based, inclusive)"); !ok || r.Y+r.H+40 > check.Y {
			t.Fatalf("window list still hidden: %+v below %+v", r, check)
		}
	}
	tt.ClickAt(r.X+20, r.Y+r.H+20)
	tt.Frame()
	tt.TypeKey(ui.Cmd, ui.KeyEnter, "")
	csSettle(a, tt)
	started = f.startedKinds()
	if len(started) != 2 {
		t.Fatalf("started %d: problems %q running %v texts %q", len(started), p.plan().problems, p.running(), tt.Texts())
	}
	req := started[1]
	params = req.Parameters.(map[string]any)
	if *req.Mode != "windows" || strings.Join(req.SampleIDs, ",") != "ZBRA_REP1,ZBRA_REP2" || params["allow_pseudoknot"] != false ||
		len(req.Windows) != 2 || req.Windows[1] != (core.Window{Start: 5000, End: 5600}) {
		t.Fatalf("request %+v params %+v", req, params)
	}
	if a.TaskLabel(a.Tasks["structure-2"]) != "Structure · BRA (2 merged) · 2 windows" || p.prefillNote != "" {
		t.Fatalf("label %q note %q", a.TaskLabel(a.Tasks["structure-2"]), p.prefillNote)
	}
	// No evidence.
	a.storeTask(model.Task{TaskID: p.lastTask, Kind: "structure", Status: "succeeded"})
	p.evidence = "s:nobody"
	tt.Frame()
	if !tt.HasText("• Choose the samples whose chimeric reads guide the fold.") {
		t.Fatalf("no evidence: %q", tt.Texts())
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

func TestStructureMemoryClaim(t *testing.T) {
	genome := core.TaskStartRequest{Kind: "structure"}
	if got := StructureMemClaim(genome, 10807, false); got != 16231 {
		t.Fatalf("genome claim %d MiB", got)
	}
	workers := uint32(2)
	windows := core.TaskStartRequest{Kind: "structure", Workers: &workers,
		Windows: []core.Window{{Start: 1, End: 4000}, {Start: 3001, End: 7000}, {Start: 6001, End: 10000}}}
	// three 4000-nt pseudoknot windows, two at a time (above the 4096 MiB floor)
	if got := StructureMemClaim(windows, 10807, true); got != 2*core.CPLfoldMemoryMB(4000, true) {
		t.Fatalf("windows claim %d MiB", got)
	}
	msg := StructureStartError(errors.New("task mem_mb must be between 1 and the budget (8192)"), 16231)
	if !strings.Contains(msg, "15.9 GB") || !strings.Contains(msg, "Preferences") {
		t.Fatalf("message %q", msg)
	}
}

// The web's ViennaRNA mode: an engine choice with its settings and hard pairs.
func TestStructureViennaForm(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1300)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.engine != engineCPLfold || p.temperature != 37 || p.minLoop != 3 {
		t.Fatalf("defaults %q %v %v", p.engine, p.temperature, p.minLoop)
	}
	if err := tt.Click("ViennaRNA MFE, the sequence alone (optional hard pairs)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, text := range []string{"ViennaRNA settings", "37 °C · minimum loop 3 · defaults", "Engine", "ViennaRNA MFE, 37 °C, minimum loop 3",
		"• ViennaRNA MFE folds the sequence alone: the chimeric reads are not used, the samples only give the reference."} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("Advanced settings") || tt.HasText("Evidence weight (alpha)") {
		t.Fatal("CPLfold settings shown for ViennaRNA")
	}
	// whole RNA, about 5 minutes by the cubic rule
	if pl := p.plan(); pl.memMB != 730 || viennaSeconds(pl.lengths) < 280 || viennaSeconds(pl.lengths) > 290 {
		t.Fatalf("memory %d MB, %g s", pl.memMB, viennaSeconds(pl.lengths))
	}
	snapshot(t, tt, "structure-vienna")
	p.hardPairs = "120-188\n5-4\nabc"
	tt.Frame()
	for _, text := range []string{"• Hard pair line 2 must have i < j.", "• Hard pair line 3 must be one pair as i-j, for example 4-18."} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	p.mode, p.windows, p.hardPairs, p.temperature = "windows", "100-400", "120-188\n500-560", 25
	tt.Frame()
	if !tt.HasText("• Hard pair 500-560 is not inside a folded window.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	p.hardPairs = "120 - 188"
	tt.Frame()
	if err := tt.Click("Run fold"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || started[0].Engine == nil || *started[0].Engine != "viennarna" || *started[0].Workers != 1 {
		t.Fatalf("started %+v", started)
	}
	params := started[0].Parameters.(map[string]any)
	if params["temperature"] != 25.0 || params["min_loop"] != 3 || fmt.Sprint(params["hard_pairs"]) != "[[120 188]]" {
		t.Fatalf("params %+v", params)
	}
}

// The web's COMRADES(ViennaRNA) mode: evidence settings and its window limit.
func TestStructureComradesForm(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if err := tt.Click("COMRADES(ViennaRNA), guided by RNAcofold of the chimeras"); err != nil {
		t.Fatal(err)
	}
	p.advancedOpen = true
	tt.Frame()
	for _, text := range []string{"Evidence stem constraints", "Randomised folds", "Seed", "Evidence", "SUB (2 merged)",
		"75 stems · 0 randomised folds · seed HYB2-Web", "• COMRADES(ViennaRNA) folds at most 3,000 nt at a time, as the web: fold windows of that size."} {
		if !strings.Contains(strings.Join(tt.Texts(), "\n"), text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("Hard base pairs (optional): i-j, one per line, RNA positions") {
		t.Fatal("hard pairs shown for COMRADES")
	}
	snapshot(t, tt, "structure-comrades")
	// more than 100 randomised folds wait for the large-ensemble consent
	p.mode, p.windows, p.randomFolds = "windows", "2800-3300", 150
	tt.Frame()
	if !tt.HasText("• Confirm the large ensemble under ViennaRNA settings (more than 100 randomised folds) to run it.") ||
		!tt.HasText("I understand that up to 1,000 serial folds may take a long time; Cancel stops them.") {
		t.Fatalf("no large-ensemble consent: %q", tt.Texts())
	}
	p.allowLargeEnsemble = true
	tt.Frame()
	if tt.HasText("• Confirm the large ensemble under ViennaRNA settings (more than 100 randomised folds) to run it.") {
		t.Fatal("the consent did not lift the block")
	}
	p.mode, p.windows, p.randomFolds, p.seed, p.homodimerOnly = "windows", "2800-3300", 5, " run1 ", true
	tt.Frame()
	if err := tt.Click("Run fold"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || *started[0].Engine != "comrades" {
		t.Fatalf("started %+v", started)
	}
	params := started[0].Parameters.(map[string]any)
	if params["random_folds"] != 5 || params["seed"] != "run1" || params["constraint_limit"] != 75 || params["homodimer_only"] != true {
		t.Fatalf("params %+v", params)
	}
}

// The ViennaRNA estimate is near measured folds at short lengths too.
func TestViennaEstimateNearMeasured(t *testing.T) {
	for _, m := range []struct {
		nt      int
		seconds float64
	}{{1000, 0.81}, {3000, 10.9}, {10_807, 285}} {
		if got := viennaSeconds([]int{m.nt}); got < m.seconds*0.8 || got > m.seconds*1.25 {
			t.Errorf("%d nt: %.2f s, measured %.2f s", m.nt, got, m.seconds)
		}
	}
}

// CPLcofold: two regions as two strands, from Explore's contact map or the
// form, with the chimeras or the sequence alone.
func TestStructureCofoldForm(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 900)
	a.Navigate("structure", map[string]any{"prefill": map[string]any{
		"regions":      []core.Region{{GeneID: africaGene, Start: 1, End: 45}, {GeneID: africaGene, Start: 1086, End: 1134}},
		"selectionKey": "c:SUB", "source": "the contact map"}})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.engine != engineCPLcofold || p.region1 != "1-45" || p.region2 != "1086-1134" || p.gene2 != africaGene {
		t.Fatalf("prefill %q %q %q %q", p.engine, p.region1, p.region2, p.gene2)
	}
	for _, text := range []string{"1  Region 1 (strand A)", "3  Region 2 (strand B)", "2 regions as two strands, 45 + 49 nt",
		"• Chimeras with one arm in each region join the strands; chimeras with both arms in one region are evidence within it."} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "structure-cofold")
	if err := tt.Click("Run fold"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 {
		t.Fatalf("started %d: %q", len(started), p.plan().problems)
	}
	req := started[0]
	params := req.Parameters.(map[string]any)
	if *req.Engine != "cplcofold" || *req.Mode != "cofold" || len(req.Windows) != 0 || len(req.Regions) != 2 ||
		req.Regions[1] != (core.Region{GeneID: africaGene, Start: 1086, End: 1134}) || params["evidence"] != nil ||
		params["allow_pseudoknot"] != false || *req.Workers != 1 {
		t.Fatalf("request %+v params %+v", req, params)
	}
	// sequence only; the energy model and beam CPLcofold supports
	p.seqOnly = true
	if pl := p.plan(); pl.req.Parameters.(map[string]any)["evidence"] != "none" || len(pl.problems) != 0 {
		t.Fatalf("sequence only %+v %q", pl.req.Parameters, pl.problems)
	}
	p.model, p.beam = "CC06", 500
	if pl := p.plan(); len(pl.problems) != 2 {
		t.Fatalf("model and beam problems %q", pl.problems)
	}
	p.model, p.beam = "DP09", 20
	for text, want := range map[string]string{"": "Region 2: enter it as start-end, for example 1-45.",
		"1086-1090": "Region 2: at least 10 nt.", "1-20000": "Region 2: 1-20000 is outside 1-10,807."} {
		p.region2 = text
		if pl := p.plan(); !slices.Contains(pl.problems, want) {
			t.Fatalf("%q: %q", text, pl.problems)
		}
	}
	// COMRADES(ViennaRNA) of the same two regions joined by the web's spacer
	p.region2, p.seqOnly, p.engine, p.mode = "1086-1134", false, engineComrades, "paired"
	tt.Frame()
	for _, text := range []string{"Two regions joined by a 100-nt spacer (long-range, intermolecular or homodimer)",
		"Region 2 (strand B)", "• Only chimeras with one arm in each region are evidence; each is cofolded (RNAcofold)."} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "structure-comrades-paired")
	pl := p.plan()
	if len(pl.problems) != 0 || *pl.req.Mode != "paired" || *pl.req.Engine != "comrades" || len(pl.req.Regions) != 2 ||
		len(pl.req.Windows) != 0 || pl.span() != "45 + 49 nt" {
		t.Fatalf("paired plan %+v %q", pl.req, pl.problems)
	}
	p.region1, p.region2 = "1-1500", "2000-3500"
	if pl := p.plan(); !slices.Contains(pl.problems, "Both regions and the 100-nt spacer are 3,101 nt; COMRADES(ViennaRNA) folds at most 3,000 nt, as the web.") {
		t.Fatalf("too long %q", pl.problems)
	}
	// a large ensemble needs the confirmation in this layout too
	p.region1, p.region2, p.randomFolds = "1-45", "1086-1134", 500
	if pl := p.plan(); !slices.Contains(pl.problems, "Confirm the large ensemble under ViennaRNA settings (more than 100 randomised folds) to run it.") {
		t.Fatalf("large ensemble %q", pl.problems)
	}
	p.randomFolds = 0
	// two joined regions are a COMRADES layout only: changing the engine resets it
	p.engine = engineVienna
	p.engineChanged()
	if pl := p.plan(); p.mode != "genome" || *pl.req.Mode != "genome" {
		t.Fatalf("mode %q", p.mode)
	}
	// a window prefill goes back to CPLfold
	p.engine = engineCPLcofold
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"gene": africaGene,
		"windows": []map[string]any{{"start": 3000, "end": 3400}}}})
	csSettle(a, tt)
	if p.engine != engineCPLfold || p.mode != "windows" {
		t.Fatalf("window prefill %q %q", p.engine, p.mode)
	}
}

// A CPLcofold record is drawn on its RNA positions, written with '&' at
// the strand break and counted only inside its two regions.
func TestStructureCofoldRecord(t *testing.T) {
	r := structureRecord{WindowStart: 1, WindowEnd: 14, Sequence: "GGGAAACCCUUUCC", DBN: "(((....)))..()",
		Positions: []int{1, 2, 3, 4, 5, 6, 101, 102, 103, 104, 105, 106, 107, 108}, Cutpoint: 6,
		Segments: []core.Region{{GeneID: "G", Start: 1, End: 6}, {GeneID: "G", Start: 101, End: 108}}}
	pairs := stRecordPairs(r)
	want := []stPair{{I: 3, J: 102}, {I: 2, J: 103}, {I: 1, J: 104}, {I: 107, J: 108}}
	if !slices.Equal(pairs, want) {
		t.Fatalf("pairs %+v", pairs)
	}
	if s, e := r.span(); s != 1 || e != 108 {
		t.Fatalf("span %d-%d", s, e)
	}
	if got := r.ranges(); !slices.Equal(got, [][2]int{{1, 6}, {101, 108}}) {
		t.Fatalf("ranges %v", got)
	}
	if r.localOf(102) != 8 || r.positionOf(8) != 102 || r.localOf(50) != 0 {
		t.Fatal("position mapping")
	}
	if got := stDBNText("G", []structureRecord{r}); got != ">G:1-6 + G:101-108\nGGGAAA&CCCUUUCC\n(((...&.)))..()\n" {
		t.Fatalf("dbn %q", got)
	}
	ct := strings.Split(stCTText("G", []structureRecord{r}), "\n")
	if ct[0] != "14 ENERGY = NA G:1-6 + G:101-108" || ct[6] != "6\tA\t5\t0\t0\t6" || ct[7] != "7\tC\t0\t8\t0\t101" ||
		ct[8] != "8\tC\t7\t9\t3\t102" {
		t.Fatalf("ct %q", ct[:9])
	}
	// a whole-RNA fold is compared only inside the two regions
	whole := structureRecord{WindowStart: 1, WindowEnd: 200, Sequence: strings.Repeat("A", 200), Pairs: [][2]int{{1, 104}, {50, 60}}}
	_, _, stats := stCompare([]structureRecord{r}, []structureRecord{whole}, true)
	if stats.PairsB != 1 || stats.Shared != 1 {
		t.Fatalf("stats %+v", stats)
	}
	// the other views draw A+B indices; the folded layout drops the linker
	res := &structureResult{Records: []structureRecord{r}}
	vp, _, start, length, seq := stViewData(res, "all")
	if start != 1 || length != 14 || seq != r.Sequence || len(vp) != 4 || vp[2] != (widgets.LayeredPair{I: 1, J: 10}) {
		t.Fatalf("view %+v %d %d", vp, start, length)
	}
	// two RNAs: drawn on A+B indices, the .ct keeps each base's RNA position
	two := r
	two.Positions = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	two.Segments = []core.Region{{GeneID: "G", Start: 1, End: 6}, {GeneID: "H", Start: 40, End: 47}}
	if ct := strings.Split(stCTText("G", []structureRecord{two}), "\n"); ct[7] != "7\tC\t0\t8\t0\t40" {
		t.Fatalf("two-RNA ct %q", ct[7])
	}
	// BPSEQ has no break: the comment names the strands, positions are local
	if bp := strings.Split(stBPSEQText("G", []structureRecord{two}), "\n"); !strings.HasPrefix(bp[0], "# G:1-6 + H:40-47; two strands: A is 1-6, B 7-14;") {
		t.Fatalf("two-RNA bpseq %q", bp[0])
	}
	// a two-strand B shown on another reference is plain pairs there
	ref := strings.Repeat("ACGUUGCA", 25)
	m, err := seqmap.Align(ref, ref)
	if err != nil {
		t.Fatal(err)
	}
	mapped, _ := stMapRecords([]structureRecord{r}, m)
	if mapped[0].Cutpoint != 0 || mapped[0].Positions != nil || len(stRecordPairs(mapped[0])) != 4 {
		t.Fatalf("mapped %+v", mapped[0])
	}
	if vp, _, _, _, _ := stViewData(&structureResult{Records: mapped}, "all"); len(vp) != 4 {
		t.Fatalf("mapped view %+v", vp)
	}
	coords := make([][2]float64, 17)
	if got := stDropLinker(coords, 6); len(got) != 14 {
		t.Fatalf("linker %d", len(got))
	}
}

// A CPLcofold result in the viewer: arcs on the RNA, the folded view of two
// strands, the dot-bracket with '&' and a two-strand CT.
func TestStructureCofoldViewer(t *testing.T) {
	a, f := openCS(t)
	f.ops["read_structure"] = fixture(t, "structure_read_cofold.json")
	f.ops["structure_layout"] = fixture(t, "structure_layout_cofold.json")
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("structure", map[string]any{"structureId": "structure-cofold1"})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	r := p.cache["structure-cofold1"].Records[0]
	if r.Cutpoint != 81 || len(r.Positions) != 201 || r.Positions[81] != 1052 {
		t.Fatalf("record cut %d positions %d", r.Cutpoint, len(r.Positions))
	}
	meta := p.metaLine(p.cache["structure-cofold1"])
	if !strings.Contains(meta, "Engine CPLcofold") || !strings.Contains(meta, "48 pairs between them") {
		t.Fatalf("meta %q", meta)
	}
	if !strings.Contains(p.dbnText, "&") || !strings.HasPrefix(p.dbnText, ">ZIKV_FrenchPolynesian_KJ776791-2_virusRNA:1-81 + ") {
		t.Fatalf("dbn %q", p.dbnText)
	}
	if p.arcs.Start != 1 || p.arcs.End != 1171 {
		t.Fatalf("arcs %v-%v", p.arcs.Start, p.arcs.End)
	}
	snapshot(t, tt, "structure-cofold-arcs")
	p.view = "Folded"
	csSettle(a, tt)
	csSettle(a, tt)
	if tt.HasText("The folded view draws one window: choose it above.") || !tt.HasText("Folded view of the base pairs") && p.layouts[stNestedDBN(nil, 1, 0)].err != "" {
		t.Fatalf("folded %q", tt.Texts())
	}
	snapshot(t, tt, "structure-cofold-folded")
	ct := strings.Split(stCTText("G", []structureRecord{r}), "\n")
	if ct[81] != "81\tU\t80\t0\t"+strings.Split(ct[81], "\t")[4]+"\t81" || !strings.HasPrefix(ct[82], "82\t") ||
		strings.Split(ct[82], "\t")[2] != "0" || strings.Split(ct[82], "\t")[5] != "1052" {
		t.Fatalf("ct break %q %q", ct[81], ct[82])
	}
}
