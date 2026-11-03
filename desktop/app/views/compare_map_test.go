package views

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hyb2desktop/model"
	"hyb2desktop/widgets"
)

// The numbers as the web card prints them (Node: padjText, fixed, toFixed).
func TestCompareNumbersMatchWeb(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		v    *float64
		want string
	}{{f(5.39e-15), "5.39e-15"}, {f(0.0123456), "0.0123"}, {f(0.5), "0.500"}, {f(1), "1.00"}, {f(0.00099999), "1.00e-3"},
		{f(0.001), "0.00100"}, {f(2.5e-300), "2.50e-300"}, {nil, "NA"}} {
		if got := cmpPadjText(c.v); got != c.want {
			t.Fatalf("padj %v: %q, want %q", c.v, got, c.want)
		}
	}
	if got := cmpFixed(f(686.55), 1); got != "686.5" {
		t.Fatalf("mean %q", got)
	}
	if cmpFixed(nil, 1) != "NA" || cmpTSVNumber(nil) != "NA" {
		t.Fatal("missing numbers are not NA")
	}
	if got := cmpSizeFactors([]float64{1.08455, 0.5441}, " · "); got != "1.0845 · 0.5441" {
		t.Fatalf("size factors %q", got)
	}
	if got := cmpSeconds(23.32); got != "23.3 s" {
		t.Fatalf("run time %q", got)
	}
	minCount := 2.0
	if got := cmpCellsTested(comparisonSummary{CellsTested: 1023236, CellsTotal: 1121855}, &minCount); got != "1,023,236 of 1,121,855 (the rest have a total count of 2 or less)" {
		t.Fatalf("cells tested %q", got)
	}
	if got := cmpMapLength(comparisonSummary{MaxCoordinate: 10800}); got != 10810 {
		t.Fatalf("map length %d", got)
	}
}

// eachCell: a cell with no or a zero log2 fold change is left out; a
// missing or zero padj is drawn fully opaque, as the web's.
func TestCompareCellsAsWeb(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	var r comparisonResult
	r.Cells.X = []int{0, 10, 20, 30, 40}
	r.Cells.Y = []int{0, 10, 20, 30, 40}
	r.Cells.LFC = []*float64{f(2), f(-1), f(0), nil, f(3)}
	r.Cells.Padj = []*float64{f(1e-10), f(1), f(1e-5), f(1e-5), nil}
	cells := cmpCells(&r, 0)
	if len(cells) != 3 || cells[0].X != 40 || cells[0].Alpha != 1 || !cells[0].Up || cells[1].Alpha != 0.2 || cells[1].Up ||
		cells[2].X != 0 || cells[2].Alpha < 0.5999 || cells[2].Alpha > 0.6001 {
		t.Fatalf("cells %+v", cells)
	}
	if up := cmpCells(&r, 1); len(up) != 2 {
		t.Fatalf("up %+v", up)
	}
}

// The colour bar is web/ui-kit.js legend.svg with deseq-card.js
// legendOptions (Node, with the desktop's red and blue).
func TestCompareLegendMatchesWeb(t *testing.T) {
	want := `<g class="chart-legend" transform="translate(58,762)" font-family="Inter, Arial, sans-serif" font-size="10"><defs><linearGradient id="ID" x1="0" y1="0" x2="1" y2="0"><stop offset="0.0000" stop-color="rgb(43,108,196)"/><stop offset="0.0625" stop-color="rgb(64,123,202)"/><stop offset="0.1250" stop-color="rgb(85,137,208)"/><stop offset="0.1875" stop-color="rgb(107,152,214)"/><stop offset="0.2500" stop-color="rgb(128,167,220)"/><stop offset="0.3125" stop-color="rgb(149,182,226)"/><stop offset="0.3750" stop-color="rgb(170,196,231)"/><stop offset="0.4375" stop-color="rgb(191,211,237)"/><stop offset="0.5000" stop-color="rgb(255,255,255)"/><stop offset="0.5625" stop-color="rgb(243,199,198)"/><stop offset="0.6250" stop-color="rgb(239,181,179)"/><stop offset="0.6875" stop-color="rgb(235,162,160)"/><stop offset="0.7500" stop-color="rgb(230,143,141)"/><stop offset="0.8125" stop-color="rgb(226,125,122)"/><stop offset="0.8750" stop-color="rgb(222,106,103)"/><stop offset="0.9375" stop-color="rgb(218,88,84)"/><stop offset="1.0000" stop-color="rgb(214,69,65)"/></linearGradient></defs><text x="0" y="10" fill="#222222" font-weight="600">−log10 padj (opacity); colour: direction</text><rect x="0" y="14" width="280" height="10" fill="url(#ID)" stroke="#6b7280" stroke-width="0.5"/><line x1="0.0" x2="0.0" y1="24" y2="28" stroke="#6b7280"/><text x="0.0" y="38" text-anchor="start" fill="#222222">≥ 20</text><line x1="70.0" x2="70.0" y1="24" y2="28" stroke="#6b7280"/><text x="70.0" y="38" text-anchor="middle" fill="#222222">10</text><line x1="140.0" x2="140.0" y1="24" y2="28" stroke="#6b7280"/><text x="140.0" y="38" text-anchor="middle" fill="#222222">0</text><line x1="210.0" x2="210.0" y1="24" y2="28" stroke="#6b7280"/><text x="210.0" y="38" text-anchor="middle" fill="#222222">10</text><line x1="280.0" x2="280.0" y1="24" y2="28" stroke="#6b7280"/><text x="280.0" y="38" text-anchor="end" fill="#222222">≥ 20</text><text x="0" y="52" fill="#6b7280">Higher in B</text><text x="280" y="52" text-anchor="end" fill="#6b7280">Higher in A</text></g>`
	g := newCmpGeometry(760, 820, 10810, widgets.Window{})
	got := strings.ReplaceAll(cmpLegendSVG(g, "A", "B", "x"), "hyb2-legend-x", "ID")
	if got != want {
		t.Fatalf("legend\n%s\nwant\n%s", got, want)
	}
}

func TestComparePanelSVG(t *testing.T) {
	cells := []cmpCell{{X: 0, Y: 0, Up: false, Alpha: 0.2}, {X: 100, Y: 200, Up: true, Alpha: 1}, {X: 5000, Y: 10, Up: true, Alpha: 0.5}}
	svg, drawn, total := cmpPanel{Cells: cells, Length: 1000, LabelA: "A<1>", LabelB: "B", RNAX: "X", MaxCells: 1}.SVG()
	xmlWellFormed(t, svg)
	if drawn != 1 || total != 2 || strings.Count(svg, "fill-opacity=") != 1 || !strings.Contains(svg, `fill-opacity="1.000"`) {
		t.Fatalf("%d of %d: %s", drawn, total, svg)
	}
	for _, want := range []string{`viewBox="0 0 760 820"`, "Differential coverage map · A&lt;1&gt; vs B", "X position (nt, arm 1)", "X position (nt, arm 2)",
		`<text x="58.0" y="734.0" text-anchor="middle" fill="#6b7280">0</text>`, "Higher in A&lt;1&gt;"} {
		if !strings.Contains(svg, want) {
			t.Fatalf("lacks %q: %s", want, svg)
		}
	}
	// zoomed: only the cells in the window
	_, _, total = cmpPanel{Cells: cells, Length: 1000, View: widgets.Window{X0: 50, X1: 150, Y0: 150, Y1: 250}}.SVG()
	if total != 1 {
		t.Fatalf("%d in view", total)
	}
}

func TestCompareInputsCurrent(t *testing.T) {
	pr := &model.Project{Samples: []model.Sample{{ID: "a", Stages: map[string]model.Stage{"map": {UpdatedAt: "2026-10-14T10:00:00Z"}}}, {ID: "b"}}}
	e := comparisonEntry{SamplesOne: []string{"a"}, SamplesTwo: []string{"b"}, CreatedAt: "2026-10-14T12:00:00Z"}
	if !cmpInputsCurrent(pr, &e) {
		t.Fatal("unchanged inputs are not current")
	}
	pr.Samples[0].Stages["map"] = model.Stage{UpdatedAt: "2026-10-14T13:00:00Z"}
	if cmpInputsCurrent(pr, &e) {
		t.Fatal("a sample run again after the test is current")
	}
	pr.Samples[0].Stages = nil
	e.SamplesTwo = []string{"gone"}
	if cmpInputsCurrent(pr, &e) || cmpCurrentText(false) != "No (inputs changed after the run)" {
		t.Fatal("a removed sample is current")
	}
}

// A sample moved between conditions makes a test stale, as the web's
// inputKey (which holds each dataset's condition).
func TestCompareInputsCurrentConditions(t *testing.T) {
	sample := func(id, cond string) model.Sample { return model.Sample{ID: id, Condition: cond, Ready: true} }
	pr := &model.Project{Samples: []model.Sample{sample("a1", "A"), sample("a2", "A"), sample("b1", "B"), sample("b2", "B")},
		Conditions: []model.Condition{{Name: "A", SampleIDs: []string{"a1", "a2"}}, {Name: "B", SampleIDs: []string{"b1", "b2"}}}}
	e := comparisonEntry{SamplesOne: []string{"a1", "a2"}, SamplesTwo: []string{"b1", "b2"}, CreatedAt: "2026-10-14T12:00:00Z"}
	if !cmpInputsCurrent(pr, &e) {
		t.Fatal("unchanged conditions are not current")
	}
	// b2 moved to A: neither side is its condition any more
	pr.Samples[3].Condition = "A"
	pr.Conditions = []model.Condition{{Name: "A", SampleIDs: []string{"a1", "a2", "b2"}}, {Name: "B", SampleIDs: []string{"b1"}}}
	if cmpInputsCurrent(pr, &e) {
		t.Fatal("a reassigned sample is current")
	}
	// single samples (s: keys) carry no condition to check
	e = comparisonEntry{SamplesOne: []string{"a1"}, SamplesTwo: []string{"b2"}, CreatedAt: e.CreatedAt}
	if !cmpInputsCurrent(pr, &e) {
		t.Fatal("single samples are not current")
	}
}

// Changed settings make the test of the pickers' groups stale, not others.
func TestCompareSettingsChanged(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	gene := "X"
	e := comparisonEntry{SamplesOne: []string{"a1", "a2"}, SamplesTwo: []string{"b1"},
		Parameters: comparisonParams{MinCount: f(2), Range: f(100), PadjCutoff: f(0.05), GeneID: &gene}}
	s := cmpSettings{IDsA: []string{"a2", "a1"}, IDsB: []string{"b1"}, MinCount: 2, Range: 100, Padj: 0.05, GeneX: "X"}
	if cmpSettingsChanged(&e, s) {
		t.Fatal("the test's settings changed")
	}
	for _, change := range []func(*cmpSettings){func(s *cmpSettings) { s.MinCount = 3 }, func(s *cmpSettings) { s.Range = 50 },
		func(s *cmpSettings) { s.Padj = 0.01 }, func(s *cmpSettings) { s.GeneX = "" }, func(s *cmpSettings) { s.GeneY = "Y" }} {
		changed := s
		change(&changed)
		if !cmpSettingsChanged(&e, changed) {
			t.Fatalf("%+v is unchanged", changed)
		}
	}
	other := s
	other.IDsB, other.Padj = []string{"b2"}, 0.01
	if cmpSettingsChanged(&e, other) {
		t.Fatal("another pair's settings stale the test")
	}
}

// The map pick (web deseq-card.js onpointerup): the nearest cell centre
// within max(radius, 10, 4 px of the map).
func TestCompareNearestPick(t *testing.T) {
	rows := []cmpInteraction{{X: 100, Y: 200}, {X: 1000, Y: 2000}}
	if i := cmpNearest(rows, 140, 205, cmpPickReach(50, 10000, 500)); i != 0 {
		t.Fatalf("picked %d", i)
	}
	if i := cmpNearest(rows, 300, 205, cmpPickReach(50, 10000, 500)); i != -1 {
		t.Fatalf("picked %d beyond reach", i)
	}
	// zoomed out, 4 px is 400 nt
	if r := cmpPickReach(50, 50000, 500); r != 400 {
		t.Fatalf("reach %g", r)
	}
	if i := cmpNearest(rows, 1300, 2105, cmpPickReach(0, 50000, 500)); i != 1 {
		t.Fatalf("picked %d", i)
	}
	if r := cmpPickReach(0, 100, 500); r != 10 {
		t.Fatalf("reach %g", r)
	}
}

// The result package's map draws every significant cell of the TSV.
func TestResultSignificantCells(t *testing.T) {
	rows := [][]string{{"10", "20", "-2", "2e-7", "5.5"}, {"30", "40", "1.5", "NA", "3"}, {"x", "1", "1", "1", "1"}}
	cells := rpSignificantCells(rows)
	if len(cells) != 2 || cells[0].X != 30 || cells[0].Alpha != 1 || !cells[0].Up || cells[1].X != 10 || cells[1].Up {
		t.Fatalf("cells %+v", cells)
	}
}

// The parameters JSON's summary has the web's camelCase keys.
func TestCompareSummaryCamel(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	e := comparisonEntry{SamplesOne: []string{"a"}, SamplesTwo: []string{"b", "c"}, Parameters: comparisonParams{MinCount: f(2), Range: f(100), PadjCutoff: f(0.05)}}
	s := comparisonSummary{CellsTested: 5, CellsTotal: 9, SignificantUp: 1, InteractionsDown: 2, SizeFactors: []float64{1, 2, 3}}
	s.Timings.TotalS = f(23.3)
	raw, err := json.Marshal(cmpSummaryCamel(&e, s, strings.ToUpper))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"samples":[{"name":"A","condition":"one"},{"name":"B","condition":"two"}`, `"parameters":{"minCount":2,"range":100,"alpha":0.01,"padjCutoff":0.05,"binSize":10}`,
		`"cellsTotal":9,"cellsTested":5`, `"significantUp":1`, `"interactionsDown":2`, `"sizeFactors":[1,2,3]`, `"timings":{"totalMs":23300}`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("lacks %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "_") {
		t.Fatalf("snake_case key: %s", raw)
	}
}

// A test whose files could not be read is not "Current inputs: Yes"; the
// figure's caption says where all cells are.
func TestReportTestCurrentAndCaption(t *testing.T) {
	x := reportTest{Err: "the result files are missing"}
	if got := x.currentText(); got != "No (the result files are missing)" {
		t.Fatalf("current %q", got)
	}
	if got := (&reportTest{Current: true}).currentText(); got != "Yes" {
		t.Fatalf("current %q", got)
	}
	if got := reportCellsElsewhere(83023); got != "the Compare page's SVG export has all." {
		t.Fatalf("caption %q", got)
	}
	if got := reportCellsElsewhere(300000); !strings.Contains(got, "the 250,000 most significant; its significant cells TSV and the result package's map have all.") {
		t.Fatalf("caption %q", got)
	}
}

// Differential arcs: opacity by −log10 padj, as the web arc map's.
func TestStructureArcOpacity(t *testing.T) {
	for _, c := range [][2]float64{{1e-10, 0.5}, {1e-40, 1}, {0.5, 0.08}, {0, 1}} {
		if got := stArcOpacity(c[0]); math.Abs(got-c[1]) > 1e-9 {
			t.Fatalf("padj %g: %g, want %g", c[0], got, c[1])
		}
	}
}

// The package's significant cells table: the web's significantToTsv
// columns, read from significant.tsv.
func TestResultSignificantRows(t *testing.T) {
	file := filepath.Join(t.TempDir(), "significant.tsv")
	text := "x\ty\tbaseMean\tlog2FoldChange\tlfcSE\tstat\tpvalue\tpadj\n10\t20\t5.5\t-2\t0.1\t3\t1e-9\t2e-7\n"
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := rpSignificantRows(file)
	if err != nil || len(rows) != 1 || strings.Join(rows[0], ",") != "10,20,-2,2e-7,5.5" {
		t.Fatalf("rows %q (%v)", rows, err)
	}
	if _, err := rpSignificantRows(""); err == nil {
		t.Fatal("no file read")
	}
}
