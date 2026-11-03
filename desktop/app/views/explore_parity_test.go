package views

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image/png"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// The Explore pages' parity with the web: filters, record actions and
// exports.

func (f *exploreFake) setAnswer(answer func(operation string, payload core.Obj) (any, bool)) {
	f.mu.Lock()
	f.answer = answer
	f.mu.Unlock()
}

// syntheticHybrids answers analysis_hybrids pages that ask for the .hyb
// lines (exports) from total made-up rows over two samples.
func syntheticHybrids(total int) func(string, core.Obj) (any, bool) {
	return func(operation string, payload core.Obj) (any, bool) {
		if operation != "analysis_hybrids" || payload["with_raw"] != true || int(num(payload["limit"])) != hybridsPageMax {
			return nil, false
		}
		offset := int(num(payload["offset"]))
		var rows []any
		for i := offset; i < min(total, offset+hybridsPageMax); i++ {
			name := fmt.Sprintf("%d_%d", 100+i, 1+i%3)
			rows = append(rows, map[string]any{"index": float64(i % 2), "id": float64(i + 1), "name": name, "sequence": "ACGU",
				"evalue1": 1e-9, "evalue2": nil, "gene1": "A", "start1": 10.0, "end1": 20.0, "gene2": "A", "start2": 30.0, "end2": 40.0,
				"reads": float64(1 + i%3), "support": 1.0, "dg": -12.5, "overlap": 7.0, "type": "Type_2",
				"raw": name + "\tACGU\t-12.5\tA\t1\t11\t10\t20\t1e-9\tA\t12\t22\t30\t40\t.\t\t7\tType_2"})
		}
		return map[string]any{"total": float64(total), "rows": rows}, true
	}
}

func openInteractions(t *testing.T) (*App, *exploreFake, *explorePage, *ui.Tester) {
	t.Helper()
	a, f, p, tt := openExplore(t)
	tt.SetSize(1300, 1100)
	if err := tt.Click("Interactions"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	return a, f, p, tt
}

func TestInteractionsEvalueFilterAndPartners(t *testing.T) {
	a, f, p, tt := openInteractions(t)
	s := &p.ix
	s.maxEvalue = "1e-5"
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	sent := f.last("analysis_hybrids")
	if filters := sent["filters"].(map[string]any); filters["max_evalue"] != 1e-5 || sent["with_raw"] != true || sent["partners"] != nil {
		t.Fatalf("payload %v", sent)
	}
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "e-value ≤ 0.00001") {
		t.Fatalf("summary %q", tt.Texts())
	}
	s.maxEvalue = "-1"
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("maximum e-value must be a number, 0 or more") {
		t.Fatalf("texts %q", tt.Texts())
	}
	s.maxEvalue = ""

	// An RNA chosen: the partner panel, ranked by the count mode.
	f.setAnswer(func(operation string, payload core.Obj) (any, bool) {
		if operation != "analysis_hybrids" || payload["partners"] != true {
			return nil, false
		}
		got := map[string]any{}
		for k, v := range fixture(t, "explore_hybrids.json") {
			got[k] = v
		}
		got["partners"] = []any{map[string]any{"name": "RNA_A", "records": 10.0, "support": 1.0},
			map[string]any{"name": "RNA_B", "records": 1.0, "support": 50.0}}
		return got, true
	})
	s.gene1 = zikvGene
	s.applyPickers()
	settle(a, tt)
	if sent := f.last("analysis_hybrids"); sent["partners"] != true || len(s.partners) != 2 {
		t.Fatalf("payload %v partners %v", sent, s.partners)
	}
	above := func(first, second string) bool {
		r1, ok1 := tt.Find(first)
		r2, ok2 := tt.Find(second)
		if !ok1 || !ok2 {
			t.Fatalf("partners not shown: %q", tt.Texts())
		}
		return r1.Y < r2.Y
	}
	if !above("RNA_B", "RNA_A") {
		t.Fatal("by support, RNA_B comes first")
	}
	s.countMode = countModes[1]
	tt.Frame()
	if !above("RNA_A", "RNA_B") {
		t.Fatal("by records, RNA_A comes first")
	}
	if err := tt.Click("Partner RNA_A"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.applied.gene2 != "RNA_A" || s.gene2 != "RNA_A" {
		t.Fatalf("partner filter %q", s.applied.gene2)
	}
}

func TestInteractionsExportEveryFilteredRecord(t *testing.T) {
	a, f, p, tt := openInteractions(t)
	f.setAnswer(syntheticHybrids(2*hybridsPageMax + 5))
	queried := len(f.all("analysis_hybrids"))
	name, text := exportMenuClick(t, a, tt, "Filtered records · CSV…")
	if name != "SUB_filtered-interactions.csv" {
		t.Fatalf("name %q", name)
	}
	pages := f.all("analysis_hybrids")[queried:]
	if len(pages) != 3 || pages[1]["offset"] != float64(hybridsPageMax) || pages[2]["offset"] != float64(2*hybridsPageMax) {
		t.Fatalf("%d pages: %v", len(pages), pages)
	}
	if fmt.Sprint(pages[0]["filters"]) != fmt.Sprint(map[string]any{}) || pages[0]["order"] != "file" {
		t.Fatalf("filters %v", pages[0])
	}
	r := csv.NewReader(strings.NewReader(text))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2*hybridsPageMax+6 || strings.HasSuffix(text, "\n") || !strings.HasPrefix(text, `"sequence_id","rna_1",`) {
		t.Fatalf("%d records: %.200q", len(records), text)
	}
	want := []string{"100_1", "A", "10", "20", "A", "30", "40", "1", "1", "7", "true", "Type_2", "-12.5", "1e-9", "", "SUB_REP1"}
	if strings.Join(records[0], ",") != strings.Join(append(recordsCSVHeader[:len(recordsCSVHeader):len(recordsCSVHeader)], "sample"), ",") ||
		strings.Join(records[1], ",") != strings.Join(want, ",") || records[2][15] != "SUB_REP2" {
		t.Fatalf("header %q, first %q", records[0], records[1])
	}

	_, hyb := exportMenuClick(t, a, tt, "Filtered records · HYB…")
	lines := strings.Split(hyb, "\n")
	if len(lines) != 2*hybridsPageMax+6 || lines[len(lines)-1] != "" || !strings.HasPrefix(lines[0], "100_1\tACGU\t-12.5\tA\t") {
		t.Fatalf("%d lines: %.120q", len(lines), hyb)
	}

	// Without the .hyb lines (the file changed) HYB cannot be written.
	f.setAnswer(func(operation string, payload core.Obj) (any, bool) {
		if operation != "analysis_hybrids" || payload["with_raw"] != true {
			return nil, false
		}
		return fixture(t, "explore_hybrids.json"), true
	})
	pickSaveInto(t, t.TempDir())
	before := lastToast(a)
	chooseExport(t, tt, "Filtered records · HYB…")
	waitToast(a, before)
	if last := lastToast(a); !strings.Contains(last, "has changed since its analysis index was built") {
		t.Fatalf("toast %q", last)
	}

	// The parameters: the filters and the matching total.
	p.ix.maxEvalue, p.ix.minDG = "0.001", "-30"
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	_, text = exportMenuClick(t, a, tt, "Parameters · JSON…")
	var params struct {
		Application string         `json:"application"`
		Analysis    string         `json:"analysis"`
		Samples     []string       `json:"samples"`
		Parameters  map[string]any `json:"parameters"`
		Result      map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &params); err != nil {
		t.Fatal(err)
	}
	if params.Application != "HYB2 Desktop" || params.Analysis != "interactions" || len(params.Samples) != 2 ||
		params.Parameters["maxEvalue"] != 0.001 || params.Parameters["minDg"] != -30.0 || params.Parameters["maxDg"] != nil ||
		params.Parameters["countMode"] != "support" || params.Result["matchingRecords"] != 1724.0 {
		t.Fatalf("params %s", text)
	}
}

func TestPageHybridsStopsAtTheEnd(t *testing.T) {
	var offsets []int
	fetch := func(total int) func(int) ([]hybridRow, int, error) {
		return func(offset int) ([]hybridRow, int, error) {
			offsets = append(offsets, offset)
			return make([]hybridRow, max(0, min(hybridsPageMax, total-offset))), total, nil
		}
	}
	seen := 0
	each := func(rows []hybridRow) error { seen += len(rows); return nil }
	if n, err := pageHybrids(fetch(2*hybridsPageMax), each); err != nil || n != 2*hybridsPageMax || seen != n ||
		fmt.Sprint(offsets) != fmt.Sprint([]int{0, hybridsPageMax}) {
		t.Fatalf("n %d seen %d offsets %v err %v", n, seen, offsets, err)
	}
	offsets = nil
	if n, _ := pageHybrids(fetch(0), each); n != 0 || len(offsets) != 1 {
		t.Fatalf("empty: n %d offsets %v", n, offsets)
	}
}

func TestInteractionRecordActions(t *testing.T) {
	a, _, p, tt := openInteractions(t)
	s := &p.ix
	s.chosen = 0
	tt.Frame()
	r := s.rows[0]
	if err := tt.Click("Copy record"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "" {
		t.Fatal("copied a record without its .hyb line")
	}
	s.rows[0].Raw = "line\tof\tthe\thyb"
	tt.Frame()
	if err := tt.Click("Copy record"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.Clipboard() != "line\tof\tthe\thyb" {
		t.Fatalf("clipboard %q", tt.Clipboard())
	}

	// Fold: the record's own sequence, as the web's "Fold sequence"
	if err := tt.Click("Fold sequence"); err != nil {
		t.Fatal(err)
	}
	prefill, _ := structureOptions(a)["prefill"].(map[string]any)
	if got, _ := prefill["record"].(hybridRow); got.Name != r.Name || got.ID != r.ID || prefill["windows"] != nil {
		t.Fatalf("prefill %v", prefill)
	}

	a.Navigate("explore", nil)
	settle(a, tt)
	s.chosen = 0
	tt.Frame()
	if err := tt.Click("Explore RNA 2 region"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	f := p.rg.applied
	if p.tab != tabRegion || f.start != r.Start2 || f.end != r.End2 || f.partner != r.Gene1 || f.partnerStart != 0 || p.rg.data == nil {
		t.Fatalf("tab %d region %+v", p.tab, f)
	}

	p.tab = tabInteractions
	settle(a, tt)
	s.chosen = 0
	tt.Frame()
	if err := tt.Click("Open in contact map"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	d := p.cm.data
	sel := p.cm.cmap.Selection
	if p.tab != tabContact || d == nil || p.cm.gene2 != r.Gene2 || sel == nil || p.cm.cmap.Zoomed() {
		t.Fatalf("tab %d data %v selection %v", p.tab, d != nil, sel)
	}
	if x, y := sel[0], sel[1]; x != r.Start1/d.bin*d.bin || y != r.Start2/d.bin*d.bin {
		t.Fatalf("picked cell %d,%d for arms at %d,%d", x, y, r.Start1, r.Start2)
	}

	// An RNA no reference holds stays out of the top bar.
	if p.selectGene("not-a-reference-rna") || a.Selected.Gene != zikvGene {
		t.Fatalf("gene %q", a.Selected.Gene)
	}
}

func TestContactExportsAndColourStyle(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1100, 1000)
	s := &p.cm
	d := s.data
	if d == nil {
		t.Fatal("no map")
	}
	name, img := exportMenuClick(t, a, tt, "Map · PNG…")
	decoded, err := png.Decode(bytes.NewReader([]byte(img)))
	if err != nil || !strings.HasSuffix(name, ".png") || decoded.Bounds().Dx() < 1000 {
		t.Fatalf("%s: %v", name, err)
	}
	name, matrix := exportMenuClick(t, a, tt, "Matrix · CSV (bins × bins)…")
	rows := strings.Split(matrix, "\n")
	if !strings.HasSuffix(name, "_matrix.csv") || !strings.HasPrefix(rows[0], "y_start/x_start,") {
		t.Fatalf("%s: %.200q", name, matrix)
	}
	total := 0
	for _, row := range rows[1:] {
		for _, cell := range strings.Split(row, ",")[1:] {
			total += int(atof(cell))
		}
	}
	sum := 0
	for _, n := range d.count {
		sum += n
	}
	if total != sum {
		t.Fatalf("matrix sums to %d, cells to %d", total, sum)
	}

	// The selected cell's records: one query over both arms' bins.
	s.cmap.Selection = &[2]int{d.x[0], d.y[0]}
	tt.Frame()
	f.setAnswer(syntheticHybrids(3))
	name, text := exportWith(t, a, tt, func() {
		if err := tt.Click("Export records"); err != nil {
			t.Fatal(err)
		}
	})
	sent := f.last("analysis_hybrids")
	filters := sent["filters"].(map[string]any)
	region := filters["region"].(map[string]any)
	if !strings.HasSuffix(name, "_records.csv") || filters["gene1"] != d.gene || filters["gene2"] != d.gene2 ||
		fmt.Sprint(region["x"]) != fmt.Sprint([]any{float64(d.x[0]), float64(d.x[0] + d.bin - 1)}) || strings.Count(text, "\n") != 3 {
		t.Fatalf("%s: %v\n%s", name, sent, text)
	}
	two := *d
	two.gene2 = "other"
	if got := contactCellFilters(&two, [2]int{d.x[0], d.y[0]}); len(got) != 2 || got[1]["gene1"] != "other" {
		t.Fatalf("both orders %v", got)
	}

	// White and black: grey cells on white paper, in the exports too.
	s.palette = "White and black"
	p.recolor()
	tt.Frame()
	if c := s.cmap.Cells()[0].Color; c.R != c.G || c.G != c.B || s.cmap.Colors.Surface != paperWhite {
		t.Fatalf("cell %v surface %v", c, s.cmap.Colors.Surface)
	}
	_, svg := exportMenuClick(t, a, tt, "Map · SVG (vector)…")
	if !strings.Contains(svg, `<rect width="100%" height="100%" fill="#ffffff"/>`) || !strings.Contains(svg, "Record count (linear)") {
		t.Fatalf("svg: %.300s", svg)
	}
	_, text = exportMenuClick(t, a, tt, "Parameters · JSON…")
	var params map[string]any
	if err := json.Unmarshal([]byte(text), &params); err != nil {
		t.Fatal(err)
	}
	got := params["parameters"].(map[string]any)
	if params["analysis"] != "contact-map" || got["palette"] != "monochrome" || got["binSize"] != float64(d.bin) ||
		got["rnaX"] != d.gene || got["selectedCell"].(map[string]any)["xStart"] != float64(d.x[0]) || got["measure"] != "record-count" {
		t.Fatalf("params %s", text)
	}
}

func TestContactMatrixCSVMatchesWeb(t *testing.T) {
	// the web's contactMatrixToCsv on the same cells (no extent padding)
	got, err := contactMatrixCSV(25, []int{25, 0, 0}, []int{50, 25, 0}, []int{1, 2, 3})
	want := "y_start/x_start,0,25\n0,3,0\n25,2,0\n50,0,1"
	if err != nil || string(got) != want {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := contactMatrixCSV(1, []int{0, 3_000_000}, []int{0, 3_000_000}, []int{1, 1}); err == nil {
		t.Fatal("no limit on the matrix")
	}
}

func TestViewpointAndRegionParityExports(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1100, 1400)
	if err := tt.Click("Viewpoint"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	name, svg := exportMenuClick(t, a, tt, "Plot · SVG…")
	xmlWellFormed(t, svg)
	if !strings.HasSuffix(name, "_viewpoint.svg") || !strings.Contains(svg, "HYB2 viewpoint") || !strings.Contains(svg, "<rect x=") {
		t.Fatalf("%s: %.300s", name, svg)
	}
	name, img := exportMenuClick(t, a, tt, "Plot · PNG…")
	if _, err := png.Decode(bytes.NewReader([]byte(img))); err != nil || !strings.HasSuffix(name, "_viewpoint.png") {
		t.Fatalf("%s: %v", name, err)
	}
	_, text := exportMenuClick(t, a, tt, "Parameters · JSON…")
	var params map[string]any
	if err := json.Unmarshal([]byte(text), &params); err != nil {
		t.Fatal(err)
	}
	if got := params["parameters"].(map[string]any); params["analysis"] != "viewpoint" || got["rna"] != zikvGene ||
		got["effectiveBinSize"] != float64(p.vp.data.bin) || got["partner"] != nil {
		t.Fatalf("params %s", text)
	}

	p.exploreRegion(3000, 3600)
	settle(a, tt)
	d := p.rg.data
	f.setAnswer(syntheticHybrids(4))
	name, text = exportMenuClick(t, a, tt, "Records · CSV…")
	sent := f.last("analysis_hybrids")
	anchor := sent["filters"].(map[string]any)["anchor"].(map[string]any)
	if !strings.HasSuffix(name, "_records.csv") || anchor["gene"] != zikvGene || anchor["start"] != float64(d.f.start) ||
		anchor["end"] != float64(d.f.end) || anchor["overlap"] != "any" || anchor["partner"] != nil || strings.Count(text, "\n") != 4 {
		t.Fatalf("%s: %v", name, sent)
	}
	name, hyb := exportMenuClick(t, a, tt, "Records · HYB…")
	if !strings.HasSuffix(name, "_records.hyb") || strings.Count(hyb, "\n") != 4 {
		t.Fatalf("%s: %q", name, hyb)
	}
	name, svg = exportMenuClick(t, a, tt, "Profile · SVG…")
	xmlWellFormed(t, svg)
	if !strings.HasSuffix(name, "_profile.svg") || !strings.Contains(svg, "HYB2 region") {
		t.Fatalf("%s", name)
	}
	_, text = exportMenuClick(t, a, tt, "Parameters · JSON…")
	params = nil
	if err := json.Unmarshal([]byte(text), &params); err != nil {
		t.Fatal(err)
	}
	if got := params["parameters"].(map[string]any); params["analysis"] != "region" || got["start"] != float64(d.f.start) ||
		got["overlapRule"] != "any" || params["result"].(map[string]any)["matchingRecords"] != float64(d.records) {
		t.Fatalf("params %s", text)
	}
}
