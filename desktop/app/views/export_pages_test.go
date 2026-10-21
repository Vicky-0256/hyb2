package views

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/widgets"
)

// exportClick clicks a page's export button and returns the file written;
// with HYB2_SNAPSHOT_DIR set the file is copied there for review.
func exportClick(t *testing.T, a *App, tt *ui.Tester, button string) (string, string) {
	t.Helper()
	asked := pickSaveInto(t, t.TempDir())
	if err := tt.Click(button); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	text := readExport(t, asked)
	path := (*asked)[0]
	if want := "Saved " + path; len(a.toasts) == 0 || a.toasts[len(a.toasts)-1].Text != want {
		t.Fatalf("toasts %q, want %q", a.toasts, want)
	}
	tt.Frame()
	if dir := os.Getenv("HYB2_SNAPSHOT_DIR"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "export-"+filepath.Base(path)), []byte(text), 0o644)
	}
	return filepath.Base(path), text
}

// tsvRecords parses an exported TSV (comments skipped).
func tsvRecords(t *testing.T, text string) [][]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(text))
	r.Comma, r.Comment, r.FieldsPerRecord = '\t', '#', -1
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestExploreInteractionsExportTSV(t *testing.T) {
	a, _, p, tt := openExplore(t)
	if err := tt.Click("Interactions"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	name, text := exportClick(t, a, tt, "Export TSV…")
	if name != "SUB_interactions.tsv" {
		t.Fatalf("name %q", name)
	}
	if !strings.Contains(text, "# HYB2 interactions · selection SUB (2 merged)\n") || !strings.Contains(text, "# 40 of 1724 rows") {
		t.Fatalf("comments %.300q", text)
	}
	records := tsvRecords(t, text)
	if len(records) != 41 || strings.Join(records[0], ",") != "id,gene1,start1,end1,gene2,start2,end2,reads,type,dG,support,name,sequence,sample" {
		t.Fatalf("%d records, header %q", len(records), records[0])
	}
	first := p.ix.rows[0]
	if records[1][1] != first.Gene1 || records[1][10] != "1" || records[1][11] != first.Name || records[1][12] != first.Sequence ||
		records[1][13] != p.ids[first.Index] {
		t.Fatalf("first row %q", records[1])
	}
}

func TestExploreContactExportSVGAndTSV(t *testing.T) {
	a, _, p, tt := openExplore(t)
	d := p.cm.data
	if d == nil {
		t.Fatal("no map")
	}
	name, svg := exportClick(t, a, tt, "Export SVG…")
	if !strings.HasSuffix(name, ".svg") || !strings.HasPrefix(name, "SUB_ZIKV") {
		t.Fatalf("name %q", name)
	}
	xmlWellFormed(t, svg)
	if n := strings.Count(svg, "<rect") - 4; n != len(d.count) {
		t.Fatalf("%d cells drawn of %d", n, len(d.count))
	}
	// Zoomed in: the TSV has the cells in view only.
	p.cm.heat.SetView(widgets.Window{X0: 0, X1: 2000, Y0: 0, Y1: 2000})
	tt.Frame()
	_, text := exportClick(t, a, tt, "Export TSV…")
	records := tsvRecords(t, text)
	inView := contactInView(d, p.cm.heat.View())
	if len(records) != len(inView)+1 || len(inView) == 0 || len(inView) == len(d.count) {
		t.Fatalf("%d records, %d in view of %d", len(records), len(inView), len(d.count))
	}
	if !strings.Contains(text, "# Cells in view: arm 1 0–2000, arm 2 0–2000") || records[0][6] != "records" {
		t.Fatalf("tsv %.400q", text)
	}
}

func TestCompareExportTSVAndSVG(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("compare", nil)
	csSettle(a, tt)
	name, text := exportClick(t, a, tt, "Export TSV…")
	if name != "AFR__2_merged_vs_BRA__2_merged_interactions.tsv" {
		t.Fatalf("name %q", name)
	}
	records := tsvRecords(t, text)
	if len(records) != 301 || records[1][2] != "5720" || records[1][1] != "higher in B" {
		t.Fatalf("%d records, first %q", len(records), records[1])
	}
	if !strings.Contains(text, "# HYB2 differential test compare-1791998444625 · AFR (2 merged) (A) vs BRA (2 merged) (B)") {
		t.Fatalf("comments %.300q", text)
	}
	_, svg := exportClick(t, a, tt, "Export SVG…")
	xmlWellFormed(t, svg)
	if n := strings.Count(svg, `fill="#d64541"`) + strings.Count(svg, `fill="#2b6cc4"`); n != 1500 {
		t.Fatalf("%d cells", n)
	}
}

func TestStructureExportArcsSVG(t *testing.T) {
	a, f := openCS(t)
	readB, listAB, _ := stFixtureB(t)
	f.structures[structB] = readB
	f.ops["list_structures"] = listAB
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	name, svg := exportClick(t, a, tt, "Export SVG…")
	if name != "ZIKV_African_KX377335-1_virusRNA_"+structA+".svg" {
		t.Fatalf("name %q", name)
	}
	xmlWellFormed(t, svg)
	if strings.Count(svg, "<path") != 1 || strings.Count(svg, "M") < 1055 || !strings.Contains(svg, "A · AFR_REP1 + AFR_REP2") {
		t.Fatalf("svg %.500s", svg)
	}
	// With B: arcs above and below, shared in grey.
	if err := tt.Click("Compare as B"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	name, svg = exportClick(t, a, tt, "Export SVG…")
	xmlWellFormed(t, svg)
	if !strings.Contains(name, "_vs_") || strings.Count(svg, "<path") != 3 || !strings.Contains(svg, "B · ZBRA_REP1 + ZBRA_REP2") {
		t.Fatalf("%s: %.600s", name, svg)
	}
}

func TestExploreViewpointAndRegionExports(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1100, 1400)
	if err := tt.Click("Viewpoint"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	name, text := exportClick(t, a, tt, "Export TSV…")
	rows := tsvRecords(t, text)
	if !strings.HasSuffix(name, "_viewpoint.tsv") || strings.Join(rows[0], ",") != "start,end,mean_interaction_coverage" ||
		len(rows) != p.vp.profile.Len()+1 {
		t.Fatalf("%s: %d rows, first %q", name, len(rows), rows[0])
	}

	p.exploreRegion(3000, 3600)
	settle(a, tt)
	name, text = exportClick(t, a, tt, "Profile TSV…")
	if rows = tsvRecords(t, text); !strings.HasSuffix(name, "_profile.tsv") || rows[0][2] != "cluster_support" || len(rows) != p.rg.profile.Len()+1 {
		t.Fatalf("%s: %q", name, rows[:1])
	}
	name, text = exportClick(t, a, tt, "Interactions TSV…")
	if rows = tsvRecords(t, text); !strings.HasSuffix(name, "_interactions.tsv") || len(rows) != 26 || rows[0][7] != "hybrids" {
		t.Fatalf("%s: %d rows %q", name, len(rows), rows[:1])
	}
	// The reference holds the RNA: FASTA as the web writes it.
	seq := strings.Repeat("ACGU", 3000)
	f.ops["read_reference_sequence"] = map[string]any{"ok": true, "sequence": seq}
	name, text = exportClick(t, a, tt, "FASTA…")
	want := ">" + zikvGene + ":3000-3600\n" + seq[2999:3600] + "\n"
	if name != exportName(".fasta", zikvGene+"_3000-3600") || text != want {
		t.Fatalf("%s:\n%q", name, text[:min(len(text), 120)])
	}
}

func TestCompareQuickViewExportTSV(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("compare", nil)
	csSettle(a, tt)
	if err := tt.Click("Quick view"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	p := a.PageFor("compare").(*comparePage)
	name, text := exportClick(t, a, tt, "Export TSV…")
	rows := tsvRecords(t, text)
	if !strings.HasSuffix(name, "_quick.tsv") || len(rows) != len(p.quickRows())+1 || rows[0][6] != "log2_effect" {
		t.Fatalf("%s: %d rows %q", name, len(rows), rows[:1])
	}
	// arm order, bins of the quick view's size
	for i := 2; i < len(rows); i++ {
		x0, _ := strconv.Atoi(rows[i-1][0])
		x1, _ := strconv.Atoi(rows[i][0])
		end, _ := strconv.Atoi(rows[i][1])
		if x1 < x0 || end != x1+p.quick.data.BinSize-1 {
			t.Fatalf("row %d: %q after %q", i, rows[i], rows[i-1])
		}
	}
}

func TestStructureExportCTPairsAndReport(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	res := p.cache[structA]
	r := res.Records[0]
	pairs := stRecordPairs(r)

	name, ct := exportClick(t, a, tt, "Save .ct")
	lines := strings.Split(strings.TrimSuffix(ct, "\n"), "\n")
	if !strings.HasSuffix(name, ".ct") || len(lines) != len(r.Sequence)+1 || !strings.HasPrefix(lines[0], strconv.Itoa(len(r.Sequence))+" ENERGY = ") {
		t.Fatalf("%s: %d lines, header %q", name, len(lines), lines[0])
	}
	// every pair is listed from both ends; the last column is the gene position
	start, _ := r.span()
	partners := 0
	for _, line := range lines[1:] {
		f := strings.Split(line, "\t")
		pos, _ := strconv.Atoi(f[0])
		partner, _ := strconv.Atoi(f[4])
		natural, _ := strconv.Atoi(f[5])
		if natural != start+pos-1 || f[1] != string(r.Sequence[pos-1]) {
			t.Fatalf("row %q", line)
		}
		if partner > 0 {
			partners++
		}
	}
	if partners != 2*len(pairs) {
		t.Fatalf("%d partner entries for %d pairs", partners, len(pairs))
	}

	name, text := exportClick(t, a, tt, "Pairs TSV…")
	rows := tsvRecords(t, text)
	if !strings.HasSuffix(name, "_pairs.tsv") || len(rows) != len(pairs)+1 || rows[0][6] != "pair_type" {
		t.Fatalf("%s: %d rows for %d pairs, header %q", name, len(rows), len(pairs), rows[0])
	}
	for _, row := range rows[1:] {
		if row[6] != row[3]+"–"+row[5] || row[7] != "no" || (row[8] != "primary" && !strings.HasPrefix(row[8], "pseudoknot-")) || row[12] != "CPLfold" {
			t.Fatalf("row %q", row)
		}
	}

	name, text = exportClick(t, a, tt, "Report JSON…")
	var report map[string]any
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatal(err)
	}
	windows := report["windows"].([]any)
	if !strings.HasSuffix(name, "_report.json") || report["structure_id"] != structA || len(windows) != len(res.Records) ||
		windows[0].(map[string]any)["base_pairs"] != float64(len(pairs)) {
		t.Fatalf("%s: %.400s", name, text)
	}
}

func TestStructureOtherViews(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1600)
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	res := p.cache[structA]
	pairs, _, start, length, _ := stViewData(res, p.record)
	if len(pairs) == 0 || start < 1 || length < 1 {
		t.Fatalf("view data: %d pairs from %d over %d", len(pairs), start, length)
	}
	for _, view := range []string{"Circular", "Radial", "Matrix"} {
		p.view = view
		tt.Frame()
		if _, ok := tt.Find(view + " view of the base pairs"); !ok {
			t.Fatalf("%s: %q", view, tt.Texts())
		}
		snapshot(t, tt, "structure-"+strings.ToLower(view))
		name, svg := exportClick(t, a, tt, "Export SVG…")
		xmlWellFormed(t, svg)
		if !strings.HasSuffix(name, "_"+strings.ToLower(view)+".svg") || !strings.Contains(svg, strings.ToLower(view)+" view") {
			t.Fatalf("%s: %.300s", name, svg)
		}
		want := map[string]string{"Circular": "<line", "Radial": "<path", "Matrix": "<circle"}[view]
		if strings.Count(svg, want) < len(pairs) {
			t.Fatalf("%s: %d %s for %d pairs", view, strings.Count(svg, want), want, len(pairs))
		}
	}
	// Folded: one window, laid out by structure_layout
	p.view = "Folded"
	if len(res.Records) > 1 {
		tt.Frame()
		if !tt.HasText("The folded view draws one window: choose it above.") {
			t.Fatalf("texts %q", tt.Texts())
		}
		r := res.Records[0]
		p.record = fmt.Sprintf("%d-%d", r.WindowStart, r.WindowEnd)
		_, _, _, length, _ = stViewData(res, p.record)
	}
	xs, ys := make([]any, length), make([]any, length)
	for i := range xs {
		angle := 2 * math.Pi * float64(i) / float64(length)
		xs[i], ys[i] = 1000*math.Cos(angle), 1000*math.Sin(angle)
	}
	f.ops["structure_layout"] = core.Obj{"ok": true, "x": xs, "y": ys}
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	if _, ok := tt.Find("Folded view of the base pairs"); !ok {
		t.Fatalf("folded: %q", tt.Texts())
	}
	snapshot(t, tt, "structure-folded")
	_, svg := exportClick(t, a, tt, "Export SVG…")
	xmlWellFormed(t, svg)
	if strings.Count(svg, "<line") < length-1 {
		t.Fatalf("folded svg has %d lines for %d nt", strings.Count(svg, "<line"), length)
	}
	// B shows the arc comparison only
	p.view = "Arcs"
	tt.Frame()
}

func TestNestedDBN(t *testing.T) {
	pairs := []widgets.LayeredPair{{I: 11, J: 20, Layer: 0}, {I: 12, J: 19, Layer: 0}, {I: 14, J: 25, Layer: 1}}
	if got := stNestedDBN(pairs, 10, 16); got != ".((......))....." {
		t.Fatalf("dbn %q", got)
	}
}
