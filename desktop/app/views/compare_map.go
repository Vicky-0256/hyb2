package views

// ④ Compare: the differential coverage map as the web draws it (web
// deseq-card.js): the significant cells with their opacity by −log10 padj,
// the map's extent, its SVG (panelSvg, with the K1 colour bar of
// web/ui-kit.js legend.svg) and the numbers as the card prints them.

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/model"
	"hyb2desktop/widgets"
)

// cmpPadjFull is the −log10 padj at which a cell is fully opaque.
const cmpPadjFull = 20

// cmpValidation is how the desktop's DESeq2 port was checked (the facts of
// web deseq-card.js VALIDATION that concern this port).
const cmpValidation = "On the ZIKV virion comparison (ZAFR_REP1/2 vs ZILM_REP2/3, 1,038,539 tested 10-nt cells) the web app's JavaScript port of it matches R DESeq2 1.46: the same 83,023 significant cells (20,660 up / 62,363 down) and 668 / 1,725 merged interactions at padj < 0.05; against that port, this one's log2 fold changes agree to 2e-9 and padj to 3e-7 (relative) over every tested cell."

// cmpArcMapNote is the web's note under the card once "Use in arc map"
// sent the test (deseq-card.js arcMapFed), worded for the desktop: here the
// enriched regions and folding energy cards read the selected test
// themselves, not the arc map.
const cmpArcMapNote = "The Structure page's arc map now draws these HYB-derived merged interactions as its differential arcs, with its own padj cutoff. The enriched regions and folding energy below use this test directly, with their own padj cutoff (0.01 by default), so they can show fewer interactions than this table."

// cmpStaleNotice is the web card's notice on a stale result (deseq-card.js
// renderResult).
const cmpStaleNotice = "Datasets, conditions or parameters changed after this result was computed; run the test again to update it."

// cmpCell is one significant cell as drawn: Up is higher in A (red),
// Alpha 0.2 + 0.8 × min(1, −log10 padj / 20).
type cmpCell struct {
	X, Y  int
	Up    bool
	Alpha float64
}

// cmpCells are the cells to draw, least significant first (strongest on
// top), as the web's eachCell: a cell with no log2 fold change, or 0, is
// left out; a missing or zero padj counts as 1e-300 (fully opaque).
func cmpCells(result *comparisonResult, direction int) []cmpCell {
	cells := result.Cells
	out := make([]cmpCell, 0, len(cells.X))
	for i := len(cells.X) - 1; i >= 0; i-- {
		if i >= len(cells.Y) || i >= len(cells.LFC) || cells.LFC[i] == nil {
			continue
		}
		lfc := *cells.LFC[i]
		if !(lfc > 0) && !(lfc < 0) {
			continue
		}
		if (direction == 1 && lfc < 0) || (direction == 2 && lfc > 0) {
			continue
		}
		padj := 1e-300
		if i < len(cells.Padj) && cells.Padj[i] != nil && *cells.Padj[i] > 0 {
			padj = math.Max(*cells.Padj[i], 1e-300)
		}
		strength := math.Min(1, -math.Log10(padj)/cmpPadjFull)
		out = append(out, cmpCell{X: cells.X[i], Y: cells.Y[i], Up: lfc > 0, Alpha: 0.2 + 0.8*strength})
	}
	return out
}

// cmpWidgetCells are cells for widgets.Heatmap.
func cmpWidgetCells(cells []cmpCell) []widgets.Cell {
	out := make([]widgets.Cell, len(cells))
	for i, c := range cells {
		color := cmpBlue
		if c.Up {
			color = cmpRed
		}
		out[i] = widgets.Cell{X: float64(c.X), Y: float64(c.Y), W: 10, H: 10, Color: color.Alpha(float32(c.Alpha))}
	}
	return out
}

// cmpMapLength is the web's mapLength: the largest coordinate plus one cell.
func cmpMapLength(s comparisonSummary) int {
	return max(10, int(s.MaxCoordinate)+10)
}

// ---- numbers as the card prints them

// jsDigits rounds v > 0 half up (on its exact value) to digits significant
// digits: the digits and the decimal exponent of the first.
func jsDigits(v float64, digits int) (string, int) {
	if v == 0 {
		return strings.Repeat("0", digits), 0
	}
	mant, exp, _ := strings.Cut(strconv.FormatFloat(v, 'e', 800, 64), "e")
	e, _ := strconv.Atoi(exp)
	all := strings.Replace(mant, ".", "", 1)
	keep := all[:digits]
	if all[digits] >= '5' {
		b := []byte(keep)
		i := len(b) - 1
		for ; i >= 0 && b[i] == '9'; i-- {
			b[i] = '0'
		}
		if i < 0 {
			b = append([]byte{'1'}, b[:len(b)-1]...)
			e++
		} else {
			b[i]++
		}
		keep = string(b)
	}
	return keep, e
}

// jsExponential is JavaScript's Number.prototype.toExponential(f) for a
// finite v.
func jsExponential(v float64, f int) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	keep, e := jsDigits(v, f+1)
	out := keep[:1]
	if f > 0 {
		out += "." + keep[1:]
	}
	if e >= 0 {
		return sign + out + "e+" + strconv.Itoa(e)
	}
	return sign + out + "e" + strconv.Itoa(e)
}

// cmpPadjText is the web's padjText: toExponential(2) below 1e-3,
// toPrecision(3) otherwise, "NA" when missing.
func cmpPadjText(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return "NA"
	}
	if *v < 1e-3 {
		return jsExponential(*v, 2)
	}
	return jsPrecision(*v, 3)
}

// cmpFixed is the web's fixed: toFixed(digits), "NA" when missing.
func cmpFixed(v *float64, digits int) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return "NA"
	}
	return jsFixed(*v, digits)
}

// cmpTSVNumber is a number of the web's interactionsToTsv: String(value),
// "NA" when missing.
func cmpTSVNumber(v *float64) string {
	if v == nil || math.IsNaN(*v) {
		return "NA"
	}
	return jsNumber(*v)
}

// cmpSizeFactors prints the size factors with 4 decimals, joined by sep.
func cmpSizeFactors(values []float64, sep string) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = jsFixed(v, 4)
	}
	return strings.Join(out, sep)
}

// cmpSeconds is the web's seconds: "12.3 s".
func cmpSeconds(s float64) string { return jsFixed(s, 1) + " s" }

// cmpCellsTested is the card's "Cells tested" line.
func cmpCellsTested(s comparisonSummary, minCount *float64) string {
	text := FormatNumber(s.CellsTested)
	if s.CellsTotal > 0 {
		text += " of " + FormatNumber(s.CellsTotal)
	}
	if minCount != nil {
		text += " (the rest have a total count of " + FormatNumber(int64(*minCount)) + " or less)"
	}
	return text
}

// cmpDatasets lists a test's samples with their condition, as the web
// report's "Datasets": "AFR_REP1 (AFR), …".
func cmpDatasets(e *comparisonEntry, name func(string) string, labelA, labelB string) string {
	var parts []string
	for _, id := range e.SamplesOne {
		parts = append(parts, name(id)+" ("+labelA+")")
	}
	for _, id := range e.SamplesTwo {
		parts = append(parts, name(id)+" ("+labelB+")")
	}
	return strings.Join(parts, ", ")
}

// cmpInputsCurrent is the desktop's "Current inputs" as the project tells
// it: every sample of the test is still in the project, none was run again
// after the test, and no sample moved between conditions (the web's
// inputKey holds each dataset's condition). A side of two or more samples
// came from a condition (the A / B pickers offer conditions and single
// samples), so it must still be exactly one condition's analysed samples.
func cmpInputsCurrent(pr *model.Project, e *comparisonEntry) bool {
	created, err := time.Parse(time.RFC3339, e.CreatedAt)
	for _, id := range append(append([]string{}, e.SamplesOne...), e.SamplesTwo...) {
		s := pr.Sample(id)
		if s == nil {
			return false
		}
		if err != nil {
			continue
		}
		for _, st := range s.Stages {
			if at, err := time.Parse(time.RFC3339, st.UpdatedAt); err == nil && at.After(created) {
				return false
			}
		}
	}
	return cmpConditionSide(pr, e.SamplesOne) && cmpConditionSide(pr, e.SamplesTwo)
}

// cmpConditionSide: a side of two or more samples is one condition's
// analysed samples, all of them.
func cmpConditionSide(pr *model.Project, ids []string) bool {
	if len(ids) < 2 {
		return true
	}
	name := ""
	for _, id := range ids {
		s := pr.Sample(id)
		if s == nil || s.Condition == "" || (name != "" && s.Condition != name) {
			return false
		}
		name = s.Condition
	}
	var now []string
	for _, cond := range pr.Conditions {
		if cond.Name != name {
			continue
		}
		for _, id := range cond.SampleIDs {
			if s := pr.Sample(id); s != nil && s.IsReady() && !slices.Contains(now, id) {
				now = append(now, id)
			}
		}
	}
	return len(now) == len(ids) && cmpSameSet(now, ids)
}

// cmpSettings are the Compare page's inputs of the next test.
type cmpSettings struct {
	IDsA, IDsB            []string
	MinCount, Range, Padj float64
	GeneX, GeneY          string
}

// cmpSettingsChanged: the A / B pickers name the test's samples but the
// settings differ from the test's (min count, merge range, padj cutoff,
// RNA X or RNA Y), so running it again would give another result, as the
// web's inputKey holds the card's parameters.
func cmpSettingsChanged(e *comparisonEntry, s cmpSettings) bool {
	same := func(a, b []string) bool { return len(a) == len(b) && cmpSameSet(a, b) }
	if !same(s.IDsA, e.SamplesOne) || !same(s.IDsB, e.SamplesTwo) {
		return false
	}
	number := func(v *float64, fallback float64) float64 {
		if v == nil {
			return fallback
		}
		return *v
	}
	text := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	q := e.Parameters
	return number(q.MinCount, 2) != math.Trunc(s.MinCount) || number(q.Range, 100) != math.Trunc(s.Range) ||
		number(q.PadjCutoff, 0.05) != s.Padj || text(q.GeneID) != s.GeneX || text(q.Gene2) != s.GeneY
}

// inputsCurrent is the Compare page's "current" of a test: cmpInputsCurrent,
// and the settings unchanged when the pickers name its samples.
func (p *comparePage) inputsCurrent(e *comparisonEntry) bool {
	a := p.a
	if a.Project == nil || !cmpInputsCurrent(a.Project, e) {
		return false
	}
	return !cmpSettingsChanged(e, cmpSettings{IDsA: a.SelectionIDs(p.keyA), IDsB: a.SelectionIDs(p.keyB),
		MinCount: p.minCount, Range: p.mergeRange, Padj: p.padj, GeneX: p.geneX, GeneY: p.geneY})
}

// cmpCurrentText is the report's "Current inputs" value.
func cmpCurrentText(current bool) string {
	if current {
		return "Yes"
	}
	return "No (inputs changed after the run)"
}

// ---- the map as SVG (web panelSvg)

// cmpPanel describes a differential coverage map figure.
type cmpPanel struct {
	Cells          []cmpCell
	Length         int
	LabelA, LabelB string
	RNAX, RNAY     string
	// View is the zoom window (gene coordinates; empty: the whole map).
	View widgets.Window
	// Width, Height default to the web's 760 × 820.
	Width, Height float64
	// MaxCells keeps the most significant cells (0: all).
	MaxCells int
	// ID makes the legend's gradient id unique on a page.
	ID string
}

// cmpGeometry is the web's geometry.
type cmpGeometry struct {
	left, top, size, length, xStart, yStart, cell float64
}

func newCmpGeometry(width, height, length float64, view widgets.Window) cmpGeometry {
	g := cmpGeometry{left: 58, top: 40, length: length}
	if view.X1 > view.X0 && view.Y1 > view.Y0 {
		g.xStart, g.yStart, g.length = view.X0, view.Y0, view.X1-view.X0
	}
	g.size = math.Max(60, math.Min(width-g.left-16, height-g.top-104))
	g.cell = math.Max(0.6, 10/g.length*g.size)
	return g
}

func (g cmpGeometry) contains(x, y float64) bool {
	return x >= g.xStart && x < g.xStart+g.length && y >= g.yStart && y < g.yStart+g.length
}
func (g cmpGeometry) x(v float64) float64 { return g.left + (v-g.xStart)/g.length*g.size }
func (g cmpGeometry) y(v float64) float64 { return g.top + (1-(v-g.yStart)/g.length)*g.size }

// cmpNiceStep is the web's niceStep.
func cmpNiceStep(span float64) float64 {
	step := math.Pow(10, math.Floor(math.Log10(math.Max(span, 1)/5)))
	for _, m := range []float64{1, 2, 5, 10} {
		if span/(step*m) <= 6 {
			return step * m
		}
	}
	return step
}

// SVG renders the map; drawn is the number of cells written, total the
// number in view.
func (m cmpPanel) SVG() (svg string, drawn, total int) {
	width, height := m.Width, m.Height
	if width <= 0 {
		width = 760
	}
	if height <= 0 {
		height = 820
	}
	g := newCmpGeometry(width, height, float64(max(m.Length, 10)), m.View)
	n := jsNumber
	f := func(v float64, d int) string { return jsFixed(v, d) }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s" font-family="Inter, Arial, sans-serif" font-size="11" role="img" aria-label="Differential coverage map">`,
		n(width), n(height), n(width), n(height))
	b.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)
	fmt.Fprintf(&b, `<text x="%s" y="20" font-size="13" font-weight="600" fill="#222222">%s</text>`, n(g.left),
		svgText("Differential coverage map · "+nameOr(m.LabelA, "Condition A")+" vs "+nameOr(m.LabelB, "Condition B")))
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="none" stroke="#c7ced6"/>`, n(g.left-0.5), n(g.top-0.5), n(g.size+1), n(g.size+1))
	var cells []cmpCell
	for _, c := range m.Cells {
		if g.contains(float64(c.X), float64(c.Y)) {
			cells = append(cells, c)
		}
	}
	total = len(cells)
	if m.MaxCells > 0 && len(cells) > m.MaxCells {
		cells = cells[len(cells)-m.MaxCells:] // the most significant are last
	}
	drawn = len(cells)
	w := f(g.cell, 2)
	var red, blue strings.Builder
	for _, c := range cells {
		group := &blue
		if c.Up {
			group = &red
		}
		fmt.Fprintf(group, `<rect x="%s" y="%s" width="%s" height="%s" fill-opacity="%s"/>`, f(g.x(float64(c.X)), 2), f(g.y(float64(c.Y+10)), 2), w, w, f(c.Alpha, 3))
	}
	// keep the drawing order (weakest first) within each colour
	fmt.Fprintf(&b, `<g fill="rgb(%d,%d,%d)">%s</g>`, cmpBlue.R, cmpBlue.G, cmpBlue.B, blue.String())
	fmt.Fprintf(&b, `<g fill="rgb(%d,%d,%d)">%s</g>`, cmpRed.R, cmpRed.G, cmpRed.B, red.String())
	// axes
	step := cmpNiceStep(g.length)
	for v := math.Ceil(g.xStart/step) * step; v <= g.xStart+g.length; v += step {
		fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#c7ced6"/>`, f(g.x(v), 1), f(g.top+g.size, 1), f(g.x(v), 1), f(g.top+g.size+5, 1))
		fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" fill="#6b7280">%s</text>`, f(g.x(v), 1), f(g.top+g.size+18, 1), n(v))
	}
	for v := math.Ceil(g.yStart/step) * step; v <= g.yStart+g.length; v += step {
		fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#c7ced6"/>`, f(g.left-5, 1), f(g.y(v), 1), f(g.left, 1), f(g.y(v), 1))
		fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="end" fill="#6b7280">%s</text>`, f(g.left-8, 1), f(g.y(v)+4, 1), n(v))
	}
	fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" fill="#6b7280">%s</text>`, f(g.left+g.size/2, 1), f(g.top+g.size+36, 1),
		svgText(nameOr(m.RNAX, "RNA X")+" position (nt, arm 1)"))
	fmt.Fprintf(&b, `<text transform="translate(%s %s) rotate(-90)" text-anchor="middle" fill="#6b7280">%s</text>`, f(13, 1), f(g.top+g.size/2, 1),
		svgText(nameOr(m.RNAY, nameOr(m.RNAX, "RNA Y"))+" position (nt, arm 2)"))
	b.WriteString(cmpLegendSVG(g, m.LabelA, m.LabelB, nameOr(m.ID, "differential")))
	b.WriteString("</svg>")
	return b.String(), drawn, total
}

// cmpLegendColor is the colour bar at t in [0, 1]: signed −log10 padj, the
// colour giving the direction and the opacity the strength, over white.
func cmpLegendColor(t float64) [3]int {
	signed := 2*t - 1
	strength := math.Abs(signed)
	if strength < 1e-6 {
		return [3]int{255, 255, 255}
	}
	c := cmpBlue
	if signed > 0 {
		c = cmpRed
	}
	alpha := 0.2 + 0.8*strength
	tint := func(v uint8) int { return int(math.Floor(255 + (float64(v)-255)*alpha + 0.5)) }
	return [3]int{tint(c.R), tint(c.G), tint(c.B)}
}

func cmpLegendTick(v float64) string {
	prefix := ""
	if math.Abs(v) >= cmpPadjFull {
		prefix = "≥ "
	}
	return prefix + jsNumber(math.Abs(v))
}

// cmpLegendSVG is web/ui-kit.js legend.svg with deseq-card.js
// legendOptions: a horizontal diverging bar from −20 to 20.
func cmpLegendSVG(g cmpGeometry, labelA, labelB, id string) string {
	const ink, muted = "#222222", "#6b7280"
	width := math.Min(280, g.size)
	n := jsNumber
	var b strings.Builder
	fmt.Fprintf(&b, `<g class="chart-legend" transform="translate(%s,%s)" font-family="Inter, Arial, sans-serif" font-size="10">`, n(g.left), n(g.top+g.size+46))
	fmt.Fprintf(&b, `<defs><linearGradient id="hyb2-legend-%s" x1="0" y1="0" x2="1" y2="0">`, svgText(id))
	for i := 0; i <= 16; i++ {
		t := float64(i) / 16
		c := cmpLegendColor(t)
		fmt.Fprintf(&b, `<stop offset="%s" stop-color="rgb(%d,%d,%d)"/>`, jsFixed(t, 4), c[0], c[1], c[2])
	}
	b.WriteString(`</linearGradient></defs>`)
	fmt.Fprintf(&b, `<text x="0" y="10" fill="%s" font-weight="600">%s</text>`, ink, svgText("−log10 padj (opacity); colour: direction"))
	fmt.Fprintf(&b, `<rect x="0" y="14" width="%s" height="10" fill="url(#hyb2-legend-%s)" stroke="%s" stroke-width="0.5"/>`, n(width), svgText(id), muted)
	for _, v := range []float64{-cmpPadjFull, -cmpPadjFull / 2, 0, cmpPadjFull / 2, cmpPadjFull} {
		fr := math.Max(0, math.Min(1, (v+cmpPadjFull)/(2*cmpPadjFull)))
		x := width * fr
		anchor := "middle"
		if fr <= 0.001 {
			anchor = "start"
		} else if fr >= 0.999 {
			anchor = "end"
		}
		fmt.Fprintf(&b, `<line x1="%s" x2="%s" y1="24" y2="28" stroke="%s"/>`, jsFixed(x, 1), jsFixed(x, 1), muted)
		fmt.Fprintf(&b, `<text x="%s" y="38" text-anchor="%s" fill="%s">%s</text>`, jsFixed(x, 1), anchor, ink, svgText(cmpLegendTick(v)))
	}
	fmt.Fprintf(&b, `<text x="0" y="52" fill="%s">%s</text>`, muted, svgText("Higher in "+nameOr(labelB, "Condition B")))
	fmt.Fprintf(&b, `<text x="%s" y="52" text-anchor="end" fill="%s">%s</text>`, n(width), muted, svgText("Higher in "+nameOr(labelA, "Condition A")))
	b.WriteString("</g>")
	return b.String()
}

// cmpPadjLegend is the colour bar on screen (and in the PNG): blue (higher
// in B) through the surface to red (higher in A), opacity by −log10 padj.
func cmpPadjLegend(c *ui.Context, labelA, labelB string) {
	t := c.Theme()
	ui.Column(c).Gap(3).Width(280).Children(func() {
		ui.Text(c, "−log10 padj (opacity); colour: direction").FontSize(11).Bold().TextColor(t.Text)
		ui.Box(c).FillWidth().Height(10).Draw(func(pt *ui.Painter, r ui.Rect) {
			steps := 64
			w := r.W / float32(steps)
			for i := range steps {
				signed := 2*(float64(i)+0.5)/float64(steps) - 1
				color := cmpBlue
				if signed > 0 {
					color = cmpRed
				}
				pt.Fill(ui.Rect{X: r.X + float32(i)*w, Y: r.Y, W: w + 0.5, H: r.H}, color.Alpha(float32(0.2+0.8*math.Abs(signed))), 0)
			}
			pt.Stroke(r, t.TextMuted, 0, 0.5)
		})
		ui.Row(c).Children(func() {
			ui.Text(c, cmpLegendTick(-cmpPadjFull)).FontSize(10).TextColor(t.Text)
			ui.Spacer(c)
			ui.Text(c, "0").FontSize(10).TextColor(t.Text)
			ui.Spacer(c)
			ui.Text(c, cmpLegendTick(cmpPadjFull)).FontSize(10).TextColor(t.Text)
		})
		ui.Row(c).Children(func() {
			ui.Text(c, "Higher in "+labelB).FontSize(11).TextColor(t.TextMuted).Grow(1)
			ui.Text(c, "Higher in "+labelA).FontSize(11).TextColor(t.TextMuted)
		})
	})
}
