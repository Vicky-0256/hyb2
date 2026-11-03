package views

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// stParityResult is a two-window CPLfold result whose windows' summaries
// differ, so a summary read for the wrong window shows.
func stParityResult(t *testing.T, meta string) *structureResult {
	t.Helper()
	a := &structureResult{}
	if err := json.Unmarshal([]byte(meta), a); err != nil {
		t.Fatal(err)
	}
	a.Records = []structureRecord{
		{WindowStart: 1, WindowEnd: 14, Sequence: "GGAAGGAACCAACC", DBN: "((..((..))..))", Candidates: []stCandidate{{}, {}}},
		// crossing: 1–10 and 2–9 cross 5–14 and 6–13, four pairs in all
		{WindowStart: 21, WindowEnd: 34, Sequence: "GGAAGGAACCAACC", DBN: "((..[[..))..]]", Candidates: []stCandidate{{}}},
	}
	return a
}

// The web's CPLfold stage cards, of the window shown (not the first
// window's summary), singular where the web's countLabel is.
func TestStructureStagesOfTheWindowShown(t *testing.T) {
	a := stParityResult(t, `{"meta": {"engine": "CPLfold", "parameters": {"allow_pseudoknot": true, "beam": 20, "energy_delta": 5},
		"records": [
		  {"evidence": {"source": "hyb-block-intervals", "inputRecords": 7, "uniqueBlocks": 3, "nonzeroBonusCells": 41, "nonzeroUpperTriangleCells": 20, "maximumBonus": 2.5}},
		  {"evidence": {"source": "hyb-block-intervals", "inputRecords": 1, "uniqueBlocks": 1, "nonzeroBonusCells": 11, "nonzeroUpperTriangleCells": 5, "maximumBonus": 0.75}}]}}`)
	got := stStagesOf(a, stPickRecords(a.Records, "21-34"))
	want := [][3]string{
		{"1 · HYB blocks", "1 row", "1 unique prepared interval blocks"},
		{"2 · Bonus matrix", "5 upper-triangle cells", "maximum log1p bonus 0.750"},
		{"3 · Two-phase search", "1 candidate", "beam 20 · ΔE 5.000"},
		{"4 · Selected topology", "pseudoknotted", "4 base pairs participate in crossings"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("window 2 stages\n%q\nwant\n%q", got, want)
	}
	got = stStagesOf(a, stPickRecords(a.Records, "1-14"))
	if got[0][1] != "7 rows" || got[1][1] != "20 upper-triangle cells" || got[2][1] != "2 candidates" ||
		got[3] != [3]string{"4 · Selected topology", "nested", "0 base pairs participate in crossings"} {
		t.Fatalf("window 1 stages %q", got)
	}
	// a result folded before the upper-triangle count: no such claim
	old := stParityResult(t, `{"meta": {"engine": "CPLfold", "parameters": {"allow_pseudoknot": false},
		"records": [{"evidence": {"source": "hyb-block-intervals", "inputRecords": 2, "nonzeroBonusCells": 41, "maximumBonus": 1}}, {}]}}`)
	got = stStagesOf(old, old.Records[:1])
	if got[1][1] != "41 nonzero cells" || got[2] != [3]string{"3 · Secondary search", "2 candidates", "Phase 2 skipped · beta ignored"} ||
		got[3][2] != "pseudoknot-free candidate" {
		t.Fatalf("old result stages %q", got)
	}
	// sequence only, as the web's: no rows, an empty matrix
	alone := stParityResult(t, `{"meta": {"engine": "CPLfold", "parameters": {"evidence": "none"},
		"records": [{"evidence": {"source": "none", "nonzeroUpperTriangleCells": 0}}, {}]}}`)
	got = stStagesOf(alone, alone.Records[:1])
	if got[0] != [3]string{"1 · HYB blocks", "0 rows", "sequence-only run"} ||
		got[1] != [3]string{"2 · Bonus matrix", "0 upper-triangle cells", "maximum log1p bonus 0.000"} {
		t.Fatalf("sequence-only stages %q", got)
	}
}

// The web's raw HYB arms under the arcs: wording, accent colour, width
// 1.1, and blocks drawn where the record draws its nucleotides.
func TestStructureRawArmsAsTheWeb(t *testing.T) {
	th := ui.LightTheme()
	r := structureRecord{WindowStart: 101, WindowEnd: 140, Sequence: strings.Repeat("A", 40)}
	if _, label, note := stRawArms([]structureRecord{r}, th); label != "Raw HYB data not attached to this result" ||
		note != "No synthetic or predicted arcs are substituted for missing raw data." {
		t.Fatalf("not attached: %q %q", label, note)
	}
	r.Arms = &stArms{}
	if arcs, label, note := stRawArms([]structureRecord{r}, th); arcs != nil || label != "No eligible HYB records mapped to this structure" ||
		note != "Arm midpoints on prepared-sequence coordinates; eligible mapped input records only." {
		t.Fatalf("no rows: %q %q", label, note)
	}
	r.Arms = &stArms{Rows: 5, Unique: 3, Shown: [][5]int{{1, 5, 31, 35, 3}, {2, 6, 20, 24, 2}}}
	arcs, label, note := stRawArms([]structureRecord{r}, th)
	if len(arcs) != 2 || arcs[0].I != 103 || arcs[0].J != 133 || arcs[0].Color != th.Accent.Alpha(0.6) || arcs[0].Width != 1.1 {
		t.Fatalf("arcs %+v", arcs)
	}
	if label != "Raw HYB interactions · below · 5 / 5 mapped records shown" || !strings.HasSuffix(note, " First 2,000 displayed.") {
		t.Fatalf("label %q note %q", label, note)
	}
	// two strands on one RNA: A+B positions drawn on the RNA's
	co := structureRecord{Sequence: strings.Repeat("A", 20), Cutpoint: 10,
		Positions: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60},
		Arms:      &stArms{Rows: 1, Unique: 1, Shown: [][5]int{{2, 4, 13, 15, 1}}}}
	if arcs, _, _ := stRawArms([]structureRecord{co}, th); len(arcs) != 1 || arcs[0].I != 3 || arcs[0].J != 54 {
		t.Fatalf("two-strand arcs %+v", arcs)
	}
}

// Arc and view opacity of a CPLfold pair are relative to the window's
// bonus matrix maximum (evidence.maximumBonus), as the web's, and only for
// a fold with HYB evidence.
func TestStructureOpacityByMatrixMaximum(t *testing.T) {
	th := ui.LightTheme()
	a := &structureResult{}
	if err := json.Unmarshal([]byte(`{"meta": {"engine": "CPLfold", "parameters": {},
		"records": [{"evidence": {"source": "hyb-block-intervals", "maximumBonus": 4}}]}}`), a); err != nil {
		t.Fatal(err)
	}
	a.Records = []structureRecord{{WindowStart: 1, WindowEnd: 10, Sequence: "GGGAAACCCU", DBN: "(((...))).",
		Pairs: [][2]int{{1, 9}, {2, 8}, {3, 7}}, Bonus: stFloat64s(2, 0, 4)}}
	pairs := stShownPairs(a.Records, nil)
	views := stViewStyles(a, a.Records, pairs, th)
	arcs := stArcStyles(a, a.Records, pairs, th)
	for i, pr := range pairs {
		want := map[int]float64{1: 0.64, 2: 0.32, 3: 0.96}[pr.I]
		if math.Abs(views[i].Alpha-want) > 1e-9 || arcs[i].Color != th.Accent.Alpha(float32(math.Round(want*10)/10)) {
			t.Fatalf("pair %d: view %v arc %+v, want %v", pr.I, views[i].Alpha, arcs[i], want)
		}
	}
	// sequence only: no opacity by bonus
	a.Meta.Records[0].Evidence.Source = "none"
	if s := stViewStyles(a, a.Records, pairs, th); s[0].Alpha != 0.9 {
		t.Fatalf("sequence-only alpha %v", s[0].Alpha)
	}
}

func TestStructureViewCopyAsTheWeb(t *testing.T) {
	vr := &structureResult{}
	vr.Meta.Engine = "ViennaRNA"
	cpl := &structureResult{}
	cpl.Meta.Parameters = map[string]any{"allow_pseudoknot": true}
	for _, c := range []struct {
		a          *structureResult
		view, want string
	}{
		{vr, "arc", "Each arc represents a ViennaRNA MFE base pair."},
		{cpl, "arc", "Arc layers distinguish nested phase-1 pairs from crossing pseudoknot pairs; opacity reflects HYB bonus support when enabled."},
		{vr, "circular", "The sequence runs around a circular backbone; chords make long-range and crossing contacts easy to scan."},
		{vr, "folded", "The RNA backbone is laid out as a folded molecule: paired bases form stem rungs while unpaired bases open into hairpin and internal loops."},
	} {
		if got := stViewCopy(c.a, c.view); got != c.want {
			t.Fatalf("%s: %q", c.view, got)
		}
	}
}

// "All datasets merged" in the top bar does not block a fold: Structure
// takes the samples mapped to the RNA's reference. A prefill's
// "sampleIds" are the evidence exactly.
func TestStructureEvidenceFromMergedAndPinnedSamples(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Selected.Key = AllSelection
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	p.gene = africaGene
	tt.Frame()
	all := a.SelectionIDs(AllSelection)
	ids := p.evidenceIDs()
	if p.evidence != AllSelection || len(ids) == 0 || len(ids) >= len(all) || p.evidenceMismatch() != "" {
		t.Fatalf("all: %v of %v, mismatch %q", ids, all, p.evidenceMismatch())
	}
	for _, id := range ids {
		if s := a.Project.Sample(id); s.ReferenceID != p.geneReference().ID {
			t.Fatalf("%s is on %s", id, s.ReferenceID)
		}
	}
	if pl := p.plan(); len(pl.problems) != 0 || !slices.Equal(pl.req.SampleIDs, ids) || !strings.Contains(pl.evidence, " mapped to ") {
		t.Fatalf("plan %q %v %q", pl.problems, pl.req.SampleIDs, pl.evidence)
	}
	_ = f

	// Explore's prefill with Compare's pinned samples
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"gene": africaGene,
		"windows": []map[string]any{{"start": 3000, "end": 3400}}, "sampleIds": []string{ids[0], "NOT_A_SAMPLE"}, "source": "Explore"}})
	csSettle(a, tt)
	if got := p.evidenceIDs(); !slices.Equal(got, []string{ids[0]}) {
		t.Fatalf("pinned %v", got)
	}
	want := "Samples sent with the fold request: " + p.sampleNames([]string{ids[0]})
	if !tt.HasText(want) {
		t.Fatalf("lacks %q: %q", want, tt.Texts())
	}
	if pl := p.plan(); !slices.Equal(pl.req.SampleIDs, []string{ids[0]}) {
		t.Fatalf("plan samples %v", pl.req.SampleIDs)
	}
	if err := tt.Click("Use the selection"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.pinned != nil || slices.Equal(p.evidenceIDs(), []string{ids[0]}) && len(ids) > 1 {
		t.Fatalf("still pinned %v", p.evidenceIDs())
	}
	// a prefill without sampleIds pins nothing
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"regions": []core.Region{
		{GeneID: africaGene, Start: 1, End: 60}, {GeneID: africaGene, Start: 100, End: 160}}}})
	csSettle(a, tt)
	if p.pinned != nil {
		t.Fatalf("pinned %v", p.pinned)
	}
}
