package views

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/widgets"
)

func TestCompareMapPNG(t *testing.T) {
	cells := []widgets.Cell{{X: 100, Y: 200, W: 10, H: 10, Color: cmpRed}, {X: 500, Y: 50, W: 10, H: 10, Color: cmpBlue}}
	data, err := cmpMapPNG(cells, widgets.Window{X0: 0, X1: 1000, Y0: 0, Y1: 1000}, 1000, "A vs B", "x, y: G", "A", "B")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1200 || b.Dy() != 1440 {
		t.Fatalf("size %v", b)
	}
}

// TestCompareTopMapsMatchWeb: the top 10 local maps ZIP's files, made from
// the runtime's top_maps.json, are the web's mapFiles output byte for byte.
// The golden files are written by web/batch-tools.js itself
// (desktop/runtime/tests/test_compare.py, InterRnaCompareTests.test_matches_the_web,
// with HYB2_WRITE_GOLDEN=1).
func TestCompareTopMapsMatchWeb(t *testing.T) {
	dir := filepath.Join("testdata", "compare_top_maps")
	raw, err := os.ReadFile(filepath.Join(dir, "top_maps.json"))
	if err != nil {
		t.Fatal(err)
	}
	var maps cmpTopMaps
	if err := json.Unmarshal(raw, &maps); err != nil {
		t.Fatal(err)
	}
	files := cmpMapFiles(&maps, 0.95, map[string]any{"minCount": 2}, "2026-10-30T00:00:00Z")
	golden, _ := filepath.Glob(filepath.Join(dir, "*"))
	compared := 0
	for _, f := range files {
		if f.Path == "parameters.json" {
			var params map[string]any
			if err := json.Unmarshal([]byte(f.Text), &params); err != nil || params["cap"] == nil || params["quantile"] != 0.95 {
				t.Fatalf("parameters.json %s (%v)", f.Text, err)
			}
			continue
		}
		want, err := os.ReadFile(filepath.Join(dir, f.Path))
		if err != nil {
			t.Fatalf("%s: %v", f.Path, err)
		}
		if f.Text != string(want) {
			at := 0
			for at < len(f.Text) && at < len(want) && f.Text[at] == want[at] {
				at++
			}
			t.Fatalf("%s differs from the web's at byte %d:\n got %q\nwant %q", f.Path, at,
				f.Text[max(0, at-80):min(len(f.Text), at+80)], string(want[max(0, at-80):min(len(want), at+80)]))
		}
		compared++
	}
	if compared != len(golden)-1 { // all but top_maps.json
		t.Fatalf("compared %d of %d golden files", compared, len(golden)-1)
	}

	// The ZIP holds them under one folder, as the web's.
	data, err := cmpZip(files, "hyb2-top10-local-maps")
	if err != nil {
		t.Fatal(err)
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.File) != len(files) || r.File[0].Name != "hyb2-top10-local-maps/"+files[0].Path {
		t.Fatalf("zip entries %d, first %q", len(r.File), r.File[0].Name)
	}
	rc, _ := r.File[0].Open()
	text, _ := io.ReadAll(rc)
	rc.Close()
	if string(text) != files[0].Text {
		t.Fatal("zip entry differs")
	}
}

func TestCompareHistogramQuantile(t *testing.T) {
	// values 1,1,1,2,2,5: R's type 7 quantile
	hist := [][2]float64{{5, 1}, {1, 3}, {2, 2}}
	for _, c := range []struct{ p, want float64 }{{0, 1}, {0.5, 1.5}, {0.95, 4.25}, {1, 5}} {
		if got := cmpHistogramQuantile(hist, c.p); math.Abs(got-c.want) > 1e-12 {
			t.Fatalf("quantile %g: %g, want %g", c.p, got, c.want)
		}
	}
	if cmpHistogramQuantile(nil, 0.95) != 1 {
		t.Fatal("empty histogram")
	}
}

// jsPrecision is JavaScript's toPrecision (values checked in node).
func TestJSPrecision(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{{2.2071469247427917e-21, "2.207e-21"}, {0.000123456, "0.0001235"}, {1, "1.000"}, {123456, "1.235e+5"},
		{0.5, "0.5000"}, {9.9995, "9.999"}, {9.99951, "10.00"}, {1e-7, "1.000e-7"}, {0.0000012345, "0.000001234"},
		{-0.03125, "-0.03125"}, {0, "0.000"}, {1234, "1234"}} {
		if got := jsPrecision(c.v, 4); got != c.want {
			t.Fatalf("jsPrecision(%v, 4) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestCompareSignificantTSV(t *testing.T) {
	raw := "x\ty\tS1\tS2\tbaseMean\tlog2FoldChange\tlfcSE\tstat\tpvalue\tpadj\n" +
		"10\t20\t5\t0\t2.5\t3.25\t0.5\t6.5\t1e-10\t2e-8\n30\t40\t0\t7\t3.5\t-2\t0.4\t-5\t1e-6\tNA\n"
	got, err := cmpSignificantTSV([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := "x\ty\tlog2FoldChange\tpadj\tbaseMean\n10\t20\t3.25\t2e-8\t2.5\n30\t40\t-2\tNA\t3.5\n"
	if string(got) != want {
		t.Fatalf("got %q", got)
	}
}

// ceFake records the analysis queries' payloads.
type ceFake struct {
	*csFake
	qmu      sync.Mutex
	payloads []core.Obj
}

func (f *ceFake) Query(path, operation string, payload core.Obj) (any, error) {
	f.qmu.Lock()
	f.payloads = append(f.payloads, payload)
	f.qmu.Unlock()
	return f.csFake.Query(path, operation, payload)
}

// TestCompareSettings: labels, RNA X / RNA Y, measure, normalisation and the
// similarity panel reach the test, the quick view and the maps.
func TestCompareSettings(t *testing.T) {
	_, cs := openCS(t)
	f := &ceFake{csFake: cs}
	a := New(f, nil, "")
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 2400)
	a.Navigate("compare", nil)
	csSettle(a, tt)
	p := a.PageFor("compare").(*comparePage)
	if p.measure != cmpMeasureRecords || p.normalise != cmpNormaliseLibrary || p.simPanel != cmpPanelWhite || p.topQuantile != 0.95 {
		t.Fatalf("defaults %q %q %q %g", p.measure, p.normalise, p.simPanel, p.topQuantile)
	}
	p.settings = true
	tt.Frame()
	for _, text := range []string{"Condition A label", "RNA X", "RNA Y", "Quick view measure", "Quick view normalisation", "Similarity panel"} {
		if !tt.HasText(text) {
			t.Fatalf("settings lack %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "compare-settings")

	// the test: custom labels, an inter-RNA pair
	p.keyA, p.keyB = "c:AFR", "c:BRA"
	p.labelA, p.labelB = "African", " "
	p.geneX, p.geneY = "ZIKV_African_KX377335-1_virusRNA", "ZIKV_Brazilian_MF073357-1_virusRNA"
	p.runTest()
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 {
		t.Fatalf("started %+v (%q)", started, p.runMsg)
	}
	params := started[0].Parameters.(map[string]any)
	if params["label_one"] != "African" || params["label_two"] != "BRA · 2 datasets merged" ||
		params["gene_id"] != "ZIKV_African_KX377335-1_virusRNA" || params["gene2"] != "ZIKV_Brazilian_MF073357-1_virusRNA" {
		t.Fatalf("params %+v", params)
	}

	// the quick view: measure, normalisation and RNA Y in the query
	p.measure, p.normalise = cmpMeasureSupport, cmpNormaliseNone
	p.runQuick()
	csSettle(a, tt)
	f.qmu.Lock()
	payload := f.payloads[len(f.payloads)-1]
	f.qmu.Unlock()
	if payload["measure"] != "support" || payload["normalisation"] != "none" || payload["gene"] != "ZIKV_African_KX377335-1_virusRNA" ||
		payload["gene2"] != "ZIKV_Brazilian_MF073357-1_virusRNA" {
		t.Fatalf("payload %+v", payload)
	}
	if ctx := p.quickContext(); ctx.LabelA != "African" || ctx.Partner != "ZIKV_Brazilian_MF073357-1_virusRNA" {
		t.Fatalf("quick context %+v", ctx)
	}
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "Quick view · African vs BRA · 2 datasets merged") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Back to tests"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	// the similarity panel: white on a light theme, grey when chosen
	read := fixture(t, "compare_read.json")
	read["similarity"] = map[string]any{"x": []any{100.0}, "y": []any{200.0}, "count": []any{8.0}}
	f.ops["read_comparison"] = read
	p.cache = map[string]*comparisonResult{}
	p.selectComparison("compare-1791998444625")
	csSettle(a, tt)
	if p.simHeat.Background != ui.RGB(255, 255, 255) {
		t.Fatalf("panel %+v", p.simHeat.Background)
	}
	p.simPanel = cmpPanelGrey
	tt.Frame()
	if p.simHeat.Background != cmpGrey235 {
		t.Fatalf("grey panel %+v", p.simHeat.Background)
	}
	for _, text := range []string{"Download map PNG…", "Export significant cells TSV…", "Count table…", "names.table…", "Sample metadata…",
		"Parameters JSON…", "Top 10 local maps per direction · ZIP…"} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "compare-exports")
}

// An inter-RNA result: the header names both RNAs, Locate passes RNA Y and
// Fold A & B (a window on one RNA) is off.
func TestCompareInterRNAResult(t *testing.T) {
	a, f := openCS(t)
	explore := &csRecordPage{a: a}
	a.RegisterPage("explore", explore)
	list := fixture(t, "compare_list.json")
	for _, item := range list["comparisons"].([]any) {
		entry := item.(map[string]any)
		entry["parameters"].(map[string]any)["gene2"] = "TEST_partner"
	}
	f.ops["list_comparisons"] = list
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("compare", nil)
	csSettle(a, tt)
	p := a.PageFor("compare").(*comparePage)
	e := p.entry(p.selectedID)
	if ctx := p.context(e); ctx.Partner != "TEST_partner" {
		t.Fatalf("context %+v", ctx)
	}
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "× TEST_partner · DESeq2 on") {
		t.Fatalf("texts %q", tt.Texts())
	}
	p.row = 0
	tt.Frame()
	_ = tt.Click("Fold A & B")
	tt.Frame()
	if p.foldOpen {
		t.Fatal("fold opened for an inter-RNA cell")
	}
	if err := tt.Click("Locate"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	if explore.options["gene2"] != "TEST_partner" {
		t.Fatalf("options %+v", explore.options)
	}
}
