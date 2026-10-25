package views

import (
	"encoding/csv"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/widgets"
)

// xmlWellFormed parses s as XML to the end.
func xmlWellFormed(t *testing.T, s string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("not well-formed XML: %v\n%.300s", err, s)
		}
	}
}

// pickSaveInto makes the save dialog answer dir/<default name>; it returns
// the paths asked for.
func pickSaveInto(t *testing.T, dir string) *[]string {
	t.Helper()
	previous := PickSave
	var asked []string
	PickSave = func(title, name string) string {
		path := filepath.Join(dir, name)
		asked = append(asked, path)
		return path
	}
	t.Cleanup(func() { PickSave = previous })
	return &asked
}

func readExport(t *testing.T, asked *[]string) string {
	t.Helper()
	if len(*asked) == 0 {
		t.Fatal("the save dialog was not shown")
	}
	path := (*asked)[len(*asked)-1]
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTSVQuotesTabsNewlinesAndQuotes(t *testing.T) {
	table := tsvTable{
		Comments: []string{"first line\nsecond line", "plain"},
		Header:   []string{"name", "value"},
		Rows:     [][]string{{"a\tb", "1"}, {"line\nbreak", `say "hi"`}, {"plain", ""}},
	}
	raw, err := table.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "# first line second line\n# plain\nname\tvalue\n") {
		t.Fatalf("head %q", text)
	}
	if !strings.Contains(text, "\"a\tb\"\t1\n") || !strings.Contains(text, "\"say \"\"hi\"\"\"") {
		t.Fatalf("quoting %q", text)
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma, r.Comment = '\t', '#'
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := append([][]string{table.Header}, table.Rows...)
	if len(records) != len(want) {
		t.Fatalf("records %q", records)
	}
	for i := range want {
		if strings.Join(records[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("row %d: %q want %q", i, records[i], want[i])
		}
	}
}

func TestExportName(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{exportName(".tsv", "AFR (2 merged)", "vs", "BRA"), "AFR__2_merged_vs_BRA.tsv"},
		{exportName("-report.html", "ZIKV <GUI>/check"), "ZIKV__GUI__check-report.html"},
		{exportName(".svg", "", "///"), "export.svg"},
		{exportName(".svg", "αβ", "map"), "αβ_map.svg"},
		{exportName(".svg", "γ", "map"), "γ_map.svg"},
	} {
		if c.got != c.want {
			t.Fatalf("%q want %q", c.got, c.want)
		}
	}
}

func TestHeatmapSVGWellFormedEscapedAndCapped(t *testing.T) {
	var cells []widgets.Cell
	for i := range 50 {
		cells = append(cells, widgets.Cell{X: float64(i * 10), Y: float64(i * 10), W: 10, H: 10, Color: ui.RGB(214, 69, 65).Alpha(0.5)})
	}
	// A cell outside the view is not counted.
	cells = append(cells, widgets.Cell{X: 5000, Y: 5000, W: 10, H: 10, Color: ui.RGB(1, 2, 3)})
	svg, drawn, total := heatSVG{Cells: cells, View: widgets.Window{X0: 0, X1: 500, Y0: 0, Y1: 500}, MaxCells: 10,
		Title: `A <&> "B"`, XLabel: "Arm 1", YLabel: "Arm 2", Note: "red & blue", ID: "t1"}.SVG()
	if drawn != 10 || total != 50 {
		t.Fatalf("drawn %d of %d", drawn, total)
	}
	xmlWellFormed(t, svg)
	if !strings.Contains(svg, "A &lt;&amp;&gt; &#34;B&#34;") || strings.Contains(svg, "<&>") {
		t.Fatalf("title not escaped: %.400s", svg)
	}
	// The last (strongest) cells are kept: the cell at 490 is drawn, 0 is not.
	if n := strings.Count(svg, `fill="#d64541" fill-opacity="0.50"`); n != 10 {
		t.Fatalf("%d cells drawn", n)
	}
	if !strings.Contains(svg, `<desc>10 of 50 cells in view drawn`) || !strings.Contains(svg, `id="t1-clip"`) {
		t.Fatalf("desc or id missing: %.600s", svg)
	}
	xmlWellFormed(t, string(svgFile(svg)))
}

func TestArcSVGWellFormed(t *testing.T) {
	a := []widgets.Pair{{I: 10, J: 50}, {I: 20, J: 40}, {I: 60, J: 90}}
	b := []widgets.Pair{{I: 20, J: 40}, {I: 70, J: 80}}
	svg, drawn := arcSVG{A: a, B: b, Start: 1, End: 100, Title: "gene <1>", LabelA: "A & co", LabelB: "B"}.SVG()
	xmlWellFormed(t, svg)
	if drawn != 5 {
		t.Fatalf("drawn %d", drawn)
	}
	// A only (red), B only (blue), shared above and below (grey).
	if strings.Count(svg, "<path") != 4 || !strings.Contains(svg, `stroke="#d64541"`) || !strings.Contains(svg, `stroke="#2b6cc4"`) {
		t.Fatalf("paths: %s", svg)
	}
	if !strings.Contains(svg, "gene &lt;1&gt;") || !strings.Contains(svg, "A &amp; co") || !strings.Contains(svg, "in both") {
		t.Fatalf("labels: %s", svg)
	}
	// One structure: arcs above only; a pair outside the range is skipped.
	svg, drawn = arcSVG{A: a, Start: 55, End: 100}.SVG()
	xmlWellFormed(t, svg)
	if drawn != 1 || strings.Contains(svg, "in both") {
		t.Fatalf("drawn %d: %s", drawn, svg)
	}
	// The arc rises above the axis: its control points have y < base.
	m := regexp.MustCompile(`d="M[0-9.]+ ([0-9.]+)C[0-9.]+ ([0-9.]+) `).FindStringSubmatch(svg)
	if m == nil || atof(m[2]) >= atof(m[1]) {
		t.Fatalf("arc %q", m)
	}
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// Each expected string is Node's String(v) / v.toFixed(d).
func TestJSNumberAndFixed(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{{0.000024497189003056657, "0.000024497189003056657"}, {3.679863616759913e-9, "3.679863616759913e-9"},
		{1e21, "1e+21"}, {1.5e21, "1.5e+21"}, {1e-6, "0.000001"}, {1e-7, "1e-7"}, {123, "123"}, {-0.5, "-0.5"},
		{math.Copysign(0, -1), "0"}, {12345.678, "12345.678"}, {1.2345678901234569e23, "1.2345678901234569e+23"},
		{5e-324, "5e-324"}, {math.Inf(1), "Infinity"}, {math.NaN(), "NaN"}} {
		if got := jsNumber(c.v); got != c.want {
			t.Errorf("jsNumber(%v) = %q, want %q", c.v, got, c.want)
		}
	}
	for _, c := range []struct {
		v    float64
		d    int
		want string
	}{{-12.3, 2, "-12.30"}, {math.Copysign(0, -1), 2, "0.00"}, {-0.001, 2, "-0.00"}, {1e-7, 2, "0.00"},
		{0.125, 2, "0.13"}, {-0.125, 2, "-0.13"}, {1.005, 2, "1.00"}, {2.5, 0, "3"}, {9.999, 2, "10.00"}} {
		if got := jsFixed(c.v, c.d); got != c.want {
			t.Errorf("jsFixed(%v, %d) = %q, want %q", c.v, c.d, got, c.want)
		}
	}
}

// The web's interactionsToTsv on the same rows (Node, HYB2 Web Lite).
func TestCompareInteractionsTSVMatchesWeb(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	rows := []cmpInteraction{
		{Direction: "down", X: 0, Y: 10, LFC: f(-8.776445444752918), Padj: f(0.0000011796673442258842), BaseMean: f(28.05259848605843)},
		{Direction: "up", X: 60, Y: 310, LFC: f(4.433926817464807), Padj: f(2.6603136482030757e-23), BaseMean: f(430.3326360217795)},
		{Direction: "down", X: 0, Y: 2220, LFC: f(-3.83531835132079), Padj: f(0.00002120333486596111), BaseMean: f(29.56968483743743)},
		{Direction: "up", X: 10, Y: 2220, LFC: f(4.905582335739226), Padj: f(2.5203360745034645e-12), BaseMean: nil},
	}
	want := "direction\tx\ty\tlog2FoldChange\tpadj\tbaseMean\n" +
		"up\t60\t310\t4.433926817464807\t2.6603136482030757e-23\t430.3326360217795\n" +
		"up\t10\t2220\t4.905582335739226\t2.5203360745034645e-12\tNA\n" +
		"down\t0\t10\t-8.776445444752918\t0.0000011796673442258842\t28.05259848605843\n" +
		"down\t0\t2220\t-3.83531835132079\t0.00002120333486596111\t29.56968483743743\n"
	if got := string(cmpInteractionsTSV(rows)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
