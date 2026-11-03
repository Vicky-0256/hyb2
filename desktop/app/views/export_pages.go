package views

// The pages' "Export…" buttons: what each page writes, from the state it
// shows. The data is captured on the main thread; the file is rendered
// and written by saveExport off it.

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
	"hyb2desktop/widgets"
)

// svgMaxCells caps the cells of a map exported from a page (the strongest
// are kept); the TSV exports have every cell.
const svgMaxCells = 100_000

// selectionName is a selection key without its "c:" / "s:" prefix.
func selectionName(key string) string {
	if len(key) > 2 && key[1] == ':' {
		return key[2:]
	}
	return key
}

// ---- ③ Explore: interactions table

func (p *explorePage) exportInteractions() {
	a := p.a
	s := &p.ix
	rows := append([]hybridRow(nil), s.rows...)
	ids := append([]string(nil), p.ids...)
	total := s.total
	comments := []string{"HYB2 interactions · selection " + a.SelectionLabel(a.Selected.Key)}
	if f := describeFilters(s.applied); f != "" {
		comments = append(comments, "Filters: "+f)
	}
	comments = append(comments, "Order: "+s.order)
	if len(rows) < total {
		comments = append(comments, fmt.Sprintf("%d of %d rows (the rows loaded on the Interactions page)", len(rows), total))
	}
	render := func() ([]byte, error) {
		multi := len(ids) > 1
		header := []string{"id", "gene1", "start1", "end1", "gene2", "start2", "end2", "reads", "type", "dG", "support", "name", "sequence"}
		if multi {
			header = append(header, "sample")
		}
		out := make([][]string, 0, len(rows))
		for _, r := range rows {
			dg := ""
			if r.DG != nil {
				dg = strconv.FormatFloat(*r.DG, 'f', -1, 64)
			}
			row := []string{strconv.FormatInt(r.ID, 10), r.Gene1, strconv.Itoa(r.Start1), strconv.Itoa(r.End1),
				r.Gene2, strconv.Itoa(r.Start2), strconv.Itoa(r.End2), strconv.Itoa(r.Reads), r.Type, dg,
				strconv.Itoa(r.Support), r.Name, r.Sequence}
			if multi {
				sample := ""
				if r.Index < len(ids) {
					sample = ids[r.Index]
				}
				row = append(row, sample)
			}
			out = append(out, row)
		}
		return tsvTable{Comments: comments, Header: header, Rows: out}.Bytes()
	}
	saveExport(a, "Export interactions", exportName(".tsv", selectionName(a.Selected.Key), "interactions"), render)
}

// exportItem is one file of a page's Export menu.
type exportItem struct {
	label    string
	disabled bool
	run      func()
}

// exportMenu is a page's "Export" menu button, as the web's export menus.
func exportMenu(c *ui.Context, items ...exportItem) ui.Element {
	return ui.MenuButton(c, "Export", func(m *ui.Menu) {
		for _, it := range items {
			if m.Item(it.label).Disabled(it.disabled).Chosen() {
				it.run()
			}
		}
	})
}

// hybridsPageMax is the bridge's analysis_hybrids limit: the page size of
// an export of every matching record.
const hybridsPageMax = 10_000

// pageHybrids reads every row fetch can give, page by page from offset 0,
// handing each page to each; it stops at a short page or at the total.
func pageHybrids(fetch func(offset int) ([]hybridRow, int, error), each func([]hybridRow) error) (int, error) {
	count := 0
	for {
		rows, total, err := fetch(count)
		if err != nil {
			return count, err
		}
		if len(rows) > 0 {
			if err := each(rows); err != nil {
				return count, err
			}
		}
		count += len(rows)
		if len(rows) < hybridsPageMax || count >= total {
			return count, nil
		}
	}
}

// allHybrids reads every hybrid matching filters, with its .hyb line, in
// order (safe off the main thread).
func (p *explorePage) allHybrids(path string, ids []string, filters core.Obj, order string, each func([]hybridRow) error) (int, error) {
	return pageHybrids(func(offset int) ([]hybridRow, int, error) {
		got, err := p.query(path, "analysis_hybrids", ids, core.Obj{
			"filters": filters, "limit": hybridsPageMax, "offset": offset, "order": order, "with_sequence": false, "with_raw": true,
		})
		if err != nil {
			return nil, 0, err
		}
		var rows []hybridRow
		if err := model.Decode(got["rows"], &rows); err != nil {
			return nil, 0, err
		}
		return rows, int(num(got["total"])), nil
	}, each)
}

// recordsCSVHeader is the web's recordsToCsv header less what the index
// does not keep (support_source, chimera_type_source, line), with the
// arms' e-values; "sample" ends it when several samples are merged.
var recordsCSVHeader = []string{"sequence_id", "rna_1", "rna_1_start", "rna_1_end", "rna_2", "rna_2_start", "rna_2_end",
	"support_count", "raw_read_count", "overlap_score", "homodimer_overlap_ge_5", "chimera_type", "dG", "rna_1_evalue", "rna_2_evalue"}

// recordsCSV writes hybrids as the web's filtered-interactions.csv: every
// cell quoted, rows joined by newlines (none after the last).
type recordsCSV struct {
	b    bytes.Buffer
	ids  []string // the samples, for the sample column when several
	rows int
}

func newRecordsCSV(ids []string) *recordsCSV {
	w := &recordsCSV{ids: ids}
	header := recordsCSVHeader
	if len(ids) > 1 {
		header = append(append([]string(nil), header...), "sample")
	}
	w.line(header)
	return w
}

func (w *recordsCSV) line(cells []string) {
	if w.b.Len() > 0 {
		w.b.WriteByte('\n')
	}
	for i, cell := range cells {
		if i > 0 {
			w.b.WriteByte(',')
		}
		w.b.WriteString(`"` + strings.ReplaceAll(cell, `"`, `""`) + `"`)
	}
}

func (w *recordsCSV) add(rows []hybridRow) error {
	opt := func(v *float64) string {
		if v == nil {
			return ""
		}
		return jsNumber(*v)
	}
	for _, r := range rows {
		reads := ""
		if r.Reads > 0 { // none in the read id
			reads = strconv.Itoa(r.Reads)
		}
		homodimer := "false"
		if r.Gene1 == r.Gene2 && r.Overlap != nil && *r.Overlap >= 5 {
			homodimer = "true"
		}
		cells := []string{r.Name, r.Gene1, strconv.Itoa(r.Start1), strconv.Itoa(r.End1), r.Gene2, strconv.Itoa(r.Start2), strconv.Itoa(r.End2),
			strconv.Itoa(r.Support), reads, opt(r.Overlap), homodimer, r.Type, opt(r.DG), opt(r.EValue1), opt(r.EValue2)}
		if len(w.ids) > 1 {
			sample := ""
			if r.Index < len(w.ids) {
				sample = w.ids[r.Index]
			}
			cells = append(cells, sample)
		}
		w.line(cells)
		w.rows++
	}
	return nil
}

// errHybChanged: a row without its .hyb line (the file changed since the
// index was built).
var errHybChanged = fmt.Errorf("a sample's .hyb file has changed since its analysis index was built; rebuild the index on the Data page")

// recordsHYB writes hybrids as the web's filtered.hyb: each record's .hyb
// line, a newline after each.
type recordsHYB struct {
	b    bytes.Buffer
	rows int
}

func (w *recordsHYB) add(rows []hybridRow) error {
	for _, r := range rows {
		if r.Raw == "" {
			return errHybChanged
		}
		w.b.WriteString(r.Raw)
		w.b.WriteByte('\n')
		w.rows++
	}
	return nil
}

// exportRecords writes every hybrid matching each filters object (one
// after another) as CSV or HYB.
func (p *explorePage) exportRecords(title, name string, filters []core.Obj, order string, hyb bool) {
	p.exportRecordsOf(p.ids, title, name, filters, order, hyb)
}

// exportRecordsOf is exportRecords over the samples ids.
func (p *explorePage) exportRecordsOf(ids []string, title, name string, filters []core.Obj, order string, hyb bool) {
	path, ids := p.a.Project.Path, append([]string(nil), ids...)
	saveExport(p.a, title, name, func() ([]byte, error) {
		csvOut, hybOut := newRecordsCSV(ids), &recordsHYB{}
		add := csvOut.add
		if hyb {
			add = hybOut.add
		}
		for _, f := range filters {
			if _, err := p.allHybrids(path, ids, f, order, add); err != nil {
				return nil, err
			}
		}
		if hyb {
			return hybOut.b.Bytes(), nil
		}
		return csvOut.b.Bytes(), nil
	})
}

func (p *explorePage) exportFilteredCSV() {
	s := &p.ix
	p.exportRecords("Export the filtered records", exportName(".csv", selectionName(p.a.Selected.Key), "filtered-interactions"),
		[]core.Obj{s.applied.payload()}, s.orderValue(), false)
}

func (p *explorePage) exportFilteredHYB() {
	s := &p.ix
	p.exportRecords("Export the filtered records", exportName(".hyb", selectionName(p.a.Selected.Key), "filtered"),
		[]core.Obj{s.applied.payload()}, s.orderValue(), true)
}

// optFloat is a JSON number, or null for none.
func optFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// optInt is a JSON number, or null for 0 (unset).
func optInt(n int) any {
	if n <= 0 {
		return nil
	}
	return n
}

// optName is a JSON string, or null for "".
func optName(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// exploreParams is the web's parameters JSON of an Explore view: what was
// shown, from which samples, with which settings.
func (p *explorePage) exploreParams(analysis string, parameters, result map[string]any) map[string]any {
	a := p.a
	return map[string]any{
		"application": "HYB2 Desktop", "version": nameOr(reportAppVersion(), "development build"), "analysis": analysis,
		"generatedLocally": true, "selection": a.SelectionLabel(a.Selected.Key), "samples": append([]string(nil), p.ids...),
		"parameters": parameters, "result": result,
	}
}

// saveJSON writes payload as indented JSON.
func saveJSON(a *App, title, name string, payload any) {
	saveExport(a, title, name, func() ([]byte, error) {
		out, err := json.MarshalIndent(payload, "", "  ")
		return append(out, '\n'), err
	})
}

// interactionsParams are the Interactions view's settings (the web's
// filters, with the desktop's reads and order).
func (p *explorePage) interactionsParams() map[string]any {
	s := &p.ix
	f := s.applied
	countMode := "support"
	if s.countMode == countModes[1] {
		countMode = "records"
	}
	parameters := map[string]any{
		"rna": optName(f.gene1), "partner": optName(f.gene2), "orientedArms": f.oriented(), "scope": nameOr(f.scope, scopeLabels[0]),
		"chimeraType": nameOr(f.typ, allTypes), "countMode": countMode, "search": f.search,
		"armOneStart": optInt(f.x0), "armOneEnd": optInt(f.x1), "armTwoStart": optInt(f.y0), "armTwoEnd": optInt(f.y1),
		"maxEvalue": optFloat(f.maxEvalue), "minDg": optFloat(f.minDG), "maxDg": optFloat(f.maxDG),
		"minSupport": max(f.minSupport, 1), "minReads": max(f.minReads, 1), "order": s.orderValue(),
	}
	return p.exploreParams("interactions", parameters, map[string]any{"matchingRecords": s.total})
}

func (p *explorePage) exportInteractionsParams() {
	saveJSON(p.a, "Export the interaction parameters", exportName(".json", selectionName(p.a.Selected.Key), "interactions-parameters"),
		p.interactionsParams())
}

// ---- ③ Explore: contact map

// contactExportSize is the web's CONTACT_EXPORT_SIZE: the map, its axes,
// colour bar and title, in CSS pixels.
const (
	contactExportWidth  = 900
	contactExportHeight = 916
)

// contactExportMenu is the web's Export menu of the Contact Map.
func (p *explorePage) contactExportMenu(c *ui.Context) {
	none := p.cm.data == nil || len(p.cm.data.count) == 0
	// the menu leaves the keyboard focus on the map
	exportMenu(c,
		exportItem{"Map · SVG (vector)…", none, p.exportContactSVG},
		exportItem{"Map · PDF…", none, p.exportContactPDF},
		exportItem{"Map · PNG…", none, func() { p.exportContactPNG(false) }},
		exportItem{"Map · PNG, print resolution…", none, func() { p.exportContactPNG(true) }},
		exportItem{"Contact cells · TSV…", none, p.exportContactTSV},
		exportItem{"Matrix · CSV (bins × bins)…", none, p.exportContactMatrix},
		exportItem{"Parameters · JSON…", p.cm.data == nil, p.exportContactParams}).KeepFocus()
}

func (p *explorePage) contactExportName(ext string) string {
	d := p.cm.data
	genes := shortName(d.gene)
	if d.gene2 != d.gene {
		genes += "_x_" + shortName(d.gene2)
	}
	return exportName(ext, selectionName(p.a.Selected.Key), genes, fmt.Sprintf("%dnt", d.bin))
}

// shortName keeps the first part of a long reference name for file names.
func shortName(gene string) string {
	if len(gene) > 24 {
		return gene[:24]
	}
	return gene
}

// contactExportMap is the map as shown (its view, selection and display
// settings) for an export: light colours whatever the theme, as the web's.
func (p *explorePage) contactExportMap() (*widgets.ContactMap, widgets.MapGeometry, widgets.MapColors) {
	s := &p.cm
	d := s.data
	style := s.style().value
	m := widgets.NewContactMap()
	m.SetCells(d.cells(s.capChoice(), s.scale, false, style), d.bin, d.base)
	if s.cmap.Zoomed() {
		v := s.cmap.View()
		m.SetView(&v)
	}
	if sel := s.cmap.Selection; sel != nil {
		copied := *sel
		m.Selection = &copied
	}
	colors := contactPaper(style)
	if colors.Surface.A == 0 {
		colors = lightExportColors
	}
	m.Colors = colors
	g := m.Geometry(contactExportWidth, contactExportHeight)
	m.Legend = d.legend(s.capChoice(), s.scale, false, style, g.PlotWidth)
	m.XTitle, m.YTitle = d.gene+" position (nt)", d.gene2+" position (nt)"
	return m, g, colors
}

// contactExportTitle is "RNA X × RNA Y", shortened to leave the colour bar
// its room.
func (p *explorePage) contactExportTitle(g widgets.MapGeometry) string {
	d := p.cm.data
	return widgets.FitLabel(d.gene+" × "+d.gene2, (g.PlotWidth-widgets.LegendWidth(g.PlotWidth)-16)*0.8)
}

// contactImage draws the export map at scale device pixels per CSS pixel.
func (p *explorePage) contactImage(scale float32) *image.RGBA {
	m, g, colors := p.contactExportMap()
	title := p.contactExportTitle(g)
	return ui.Render(func(c *ui.Context) {
		c.SetTheme(ui.LightTheme())
		ui.Box(c).Size(contactExportWidth, contactExportHeight).Draw(func(pt *ui.Painter, r ui.Rect) {
			m.Paint(pt, r, g)
			pt.RichText(r.X+g.Left, r.Y+4, 0, ui.Span{Text: title, Size: 14, Weight: 600, Color: colors.Ink})
		})
	}, contactExportWidth, contactExportHeight, scale)
}

// exportContactPNG is the web's Map · PNG (twice the CSS size), or with
// print the print-resolution PNG (four times).
func (p *explorePage) exportContactPNG(print bool) {
	if p.cm.data == nil {
		return
	}
	scale, name := float32(2), ".png"
	if print {
		scale, name = 4, "_print.png"
	}
	img := p.contactImage(scale)
	saveExport(p.a, "Export the contact map", p.contactExportName(name), func() ([]byte, error) {
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	})
}

// exportContactPDF is the web's Map · PDF: the map at three times its CSS
// size as a lossless image on one page of 0.75 pt per CSS pixel.
func (p *explorePage) exportContactPDF() {
	if p.cm.data == nil {
		return
	}
	img := p.contactImage(3)
	saveExport(p.a, "Export the contact map", p.contactExportName(".pdf"), func() ([]byte, error) {
		return imagePDF(img, contactExportWidth*0.75, contactExportHeight*0.75)
	})
}

// imagePDF is a one-page PDF holding img (Flate-compressed RGB),
// widthPt x heightPt points (web/ui-kit.js canvasToPdf).
func imagePDF(img *image.RGBA, widthPt, heightPt float64) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rgb := make([]byte, 0, w*h*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[(y-b.Min.Y)*img.Stride:]
		for x := range w {
			// image.RGBA is premultiplied; exports are opaque
			rgb = append(rgb, row[x*4], row[x*4+1], row[x*4+2])
		}
	}
	var packed bytes.Buffer
	zw := zlib.NewWriter(&packed)
	if _, err := zw.Write(rgb); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	wPt, hPt := strconv.FormatFloat(widthPt, 'f', 2, 64), strconv.FormatFloat(heightPt, 'f', 2, 64)
	content := "q " + wPt + " 0 0 " + hPt + " 0 0 cm /Im0 Do Q"
	var out bytes.Buffer
	var offsets []int
	object := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	out.WriteString("%PDF-1.4\n")
	object("<< /Type /Catalog /Pages 2 0 R >>")
	object("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	object("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 " + wPt + " " + hPt + "] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>")
	offsets = append(offsets, out.Len())
	fmt.Fprintf(&out, "4 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", w, h, packed.Len())
	out.Write(packed.Bytes())
	out.WriteString("\nendstream\nendobj\n")
	object(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes(), nil
}

// exportContactSVG is the web's buildContactSvg: the cells of the export
// view, the frame, the colour bar, the axes and the title.
func (p *explorePage) exportContactSVG() {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	m, g, colors := p.contactExportMap()
	title := p.contactExportTitle(g)
	cells := d.cells(s.capChoice(), s.scale, false, s.style().value)
	saveExport(p.a, "Export the contact map", p.contactExportName(".svg"), func() ([]byte, error) {
		return svgFile(contactSVG(d, m, g, colors, cells, title)), nil
	})
}

func contactSVG(d *contactData, m *widgets.ContactMap, g widgets.MapGeometry, colors widgets.MapColors, cells []widgets.Cell, title string) string {
	var b strings.Builder
	num := func(v float32) string { return strconv.FormatFloat(float64(v), 'f', 2, 32) }
	v, bin := g.View, float32(d.bin)
	// svgFile adds the XML declaration
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		contactExportWidth, contactExportHeight, contactExportWidth, contactExportHeight)
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" %s/>`, svgFill("fill", colors.Surface))
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" %s/>`, num(g.Left), num(g.Top), num(g.PlotWidth), num(g.PlotHeight), svgFill("fill", colors.Surface))
	for i := range d.count {
		if d.x[i] < v.XStart || d.x[i] > v.XEnd || d.y[i] < v.YStart || d.y[i] > v.YEnd {
			continue
		}
		x := g.Left + float32(d.x[i]-v.XStart)/bin*g.Cell
		y := g.Top + float32(v.YEnd-d.y[i])/bin*g.Cell
		fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" %s/>`, num(x), num(y), num(g.Cell+0.4), num(g.Cell+0.4), svgFill("fill", cells[i].Color))
	}
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="none" %s stroke-width="1"/>`, num(g.Left), num(g.Top), num(g.PlotWidth), num(g.PlotHeight), svgFill("stroke", colors.Ink))
	b.WriteString(legendSVG(m.Legend, g, colors))
	b.WriteString(contactAxesSVG(d, g, colors, m.XTitle, m.YTitle))
	fmt.Fprintf(&b, `<text x="%s" y="18" font-family="%s" font-size="14" font-weight="600" %s>%s</text>`, num(g.Left), svgText(axisFontFamily), svgFill("fill", colors.Ink), svgText(title))
	b.WriteString("</svg>")
	return b.String()
}

const axisFontFamily = `ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif`

// legendSVG is web/ui-kit.js legend.svg for a horizontal colour bar.
func legendSVG(l widgets.Legend, g widgets.MapGeometry, colors widgets.MapColors) string {
	width := widgets.LegendWidth(g.PlotWidth)
	x, y := g.Left+g.PlotWidth-width, float32(4)
	var b strings.Builder
	fmt.Fprintf(&b, `<g class="chart-legend" transform="translate(%s,%s)" font-family="%s" font-size="10">`, svgNum(float64(x)), svgNum(float64(y)), svgText(axisFontFamily))
	b.WriteString(`<defs><linearGradient id="hyb2-legend-contact" x1="0" y1="0" x2="1" y2="0">`)
	for i := 0; i <= 16; i++ {
		t := float64(i) / 16
		fmt.Fprintf(&b, `<stop offset="%.4f" %s/>`, t, svgFill("stop-color", l.ColorAt(t)))
	}
	b.WriteString(`</linearGradient></defs>`)
	if l.Title != "" {
		fmt.Fprintf(&b, `<text x="0" y="10" %s font-weight="600">%s</text>`, svgFill("fill", colors.Ink), svgText(l.Title))
	}
	fmt.Fprintf(&b, `<rect x="0" y="14" width="%s" height="10" fill="url(#hyb2-legend-contact)" %s stroke-width="0.5"/>`, svgNum(float64(width)), svgFill("stroke", colors.Muted))
	for _, value := range l.Ticks {
		f := 0.0
		if l.Max > 0 {
			f = math.Max(0, math.Min(1, value/l.Max))
		}
		tx := float64(width) * f
		anchor := "middle"
		if f <= 0.001 {
			anchor = "start"
		} else if f >= 0.999 {
			anchor = "end"
		}
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="24" y2="28" %s/>`, tx, tx, svgFill("stroke", colors.Muted))
		fmt.Fprintf(&b, `<text x="%.1f" y="38" text-anchor="%s" %s>%s</text>`, tx, anchor, svgFill("fill", colors.Ink), svgText(l.Format(value)))
	}
	b.WriteString("</g>")
	return b.String()
}

// contactAxesSVG is the web's svgAxes for the map.
func contactAxesSVG(d *contactData, g widgets.MapGeometry, colors widgets.MapColors, xTitle, yTitle string) string {
	var b strings.Builder
	bottom := g.Top + g.PlotHeight
	fmt.Fprintf(&b, `<g font-family="%s" font-size="11">`, svgText(axisFontFamily))
	xs := widgets.CoordinateTicks(g.View.XStart, g.View.XEnd+d.bin-1, g.PlotWidth, false)
	for i, tick := range xs {
		x := svgNum(float64(g.Left + tick.Offset))
		anchor := "middle"
		if i == 0 {
			anchor = "start"
		} else if i == len(xs)-1 {
			anchor = "end"
		}
		fmt.Fprintf(&b, `<line x1="%s" x2="%s" y1="%s" y2="%s" %s/>`, x, x, svgNum(float64(bottom)), svgNum(float64(bottom+4)), svgFill("stroke", colors.Muted))
		fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="%s" %s>%s</text>`, x, svgNum(float64(bottom+16)), anchor, svgFill("fill", colors.Muted), svgText(tick.Label))
	}
	for _, tick := range widgets.CoordinateTicks(g.View.YStart, g.View.YEnd+d.bin-1, g.PlotHeight, true) {
		y := svgNum(float64(g.Top + g.PlotHeight - tick.Offset))
		fmt.Fprintf(&b, `<line x1="%s" x2="%s" y1="%s" y2="%s" %s/>`, svgNum(float64(g.Left-4)), svgNum(float64(g.Left)), y, y, svgFill("stroke", colors.Muted))
		fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="end" dominant-baseline="middle" %s>%s</text>`, svgNum(float64(g.Left-7)), y, svgFill("fill", colors.Muted), svgText(tick.Label))
	}
	fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" font-weight="600" %s>%s</text>`, svgNum(float64(g.Left+g.PlotWidth/2)), svgNum(float64(bottom+34)),
		svgFill("fill", colors.Ink), svgText(widgets.FitLabel(xTitle, g.PlotWidth)))
	fmt.Fprintf(&b, `<text transform="translate(13 %s) rotate(-90)" text-anchor="middle" dominant-baseline="middle" font-weight="600" %s>%s</text>`,
		svgNum(float64(g.Top+g.PlotHeight/2)), svgFill("fill", colors.Ink), svgText(widgets.FitLabel(yTitle, g.PlotHeight)))
	b.WriteString("</g>")
	return b.String()
}

// exportContactTSV writes every cell of the map as the web's
// contact-map.tsv: bin ranges, the hybrids in the cell and their summed
// cluster support, whatever the map's measure, in arm order.
func (p *explorePage) exportContactTSV() {
	d := p.cm.data
	if d == nil {
		return
	}
	render := func() ([]byte, error) {
		records, err := p.contactCells(d, cmRecords)
		if err != nil {
			return nil, err
		}
		support, err := p.contactCells(d, cmSupport)
		if err != nil {
			return nil, err
		}
		return contactTSV(d.bin, records, support), nil
	}
	saveExport(p.a, "Export the contact map cells", p.contactExportName(".tsv"), render)
}

// contactTSV is the web's contactToTsv (no newline after the last row).
func contactTSV(bin int, records, support contactMeasure) []byte {
	supportAt := make(map[[2]int]int, len(support.x))
	for i := range support.x {
		supportAt[[2]int{support.x[i], support.y[i]}] = support.count[i]
	}
	order := make([]int, len(records.x))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if records.x[a] != records.x[b] {
			return records.x[a] < records.x[b]
		}
		return records.y[a] < records.y[b]
	})
	rows := []string{"x_start\tx_end\ty_start\ty_end\trecords\tsupport_count"}
	for _, i := range order {
		x, y := records.x[i], records.y[i]
		rows = append(rows, fmt.Sprintf("%d\t%d\t%d\t%d\t%d\t%d", x, x+bin-1, y, y+bin-1, records.count[i], supportAt[[2]int{x, y}]))
	}
	return []byte(strings.Join(rows, "\n"))
}

// maxMatrixCells is the web's limit on a matrix export's cells.
const maxMatrixCells = 4_000_000

// exportContactMatrix writes the map as the web's contact-matrix.csv:
// one row per arm 2 bin, one column per arm 1 bin, over the cells' extent.
func (p *explorePage) exportContactMatrix() {
	d := p.cm.data
	if d == nil {
		return
	}
	bin, x, y, count := d.bin, d.x, d.y, d.count
	saveExport(p.a, "Export the contact matrix", p.contactExportName("_matrix.csv"), func() ([]byte, error) {
		return contactMatrixCSV(bin, x, y, count)
	})
}

// contactMatrixCSV is the web's contactMatrixToCsv: "y_start/x_start"
// then the x bin starts; each row a y bin start then its values (0 for an
// empty cell); no newline after the last row.
func contactMatrixCSV(bin int, x, y, count []int) ([]byte, error) {
	if len(count) == 0 {
		return nil, fmt.Errorf("the contact map has no cells")
	}
	bin = max(bin, 1)
	axis := func(values []int) []int {
		first, last := slices.Min(values), slices.Max(values)
		var out []int
		for v := first; v <= last; v += bin {
			out = append(out, v)
		}
		return out
	}
	xs, ys := axis(x), axis(y)
	if n := len(xs) * len(ys); n > maxMatrixCells {
		return nil, fmt.Errorf("the matrix would have %s cells; use larger bins for the matrix export, or the contact cells TSV", FormatNumber(int64(n)))
	}
	values := make([][]int, len(ys))
	for i := range values {
		values[i] = make([]int, len(xs))
	}
	for i := range count {
		col, row := (x[i]-xs[0])/bin, (y[i]-ys[0])/bin
		if row >= 0 && row < len(ys) && col >= 0 && col < len(xs) {
			values[row][col] = count[i]
		}
	}
	var b strings.Builder
	b.WriteString("y_start/x_start")
	for _, v := range xs {
		b.WriteString("," + strconv.Itoa(v))
	}
	for i, row := range values {
		b.WriteString("\n" + strconv.Itoa(ys[i]))
		for _, v := range row {
			b.WriteString("," + strconv.Itoa(v))
		}
	}
	return []byte(b.String()), nil
}

// contactCellFilters are the analysis_hybrids filters of the hybrids in the
// bin at sel (both arms over its bins), as the map counts them: the reverse
// arm order too for normalised pairs of two RNAs.
func contactCellFilters(d *contactData, sel [2]int) []core.Obj {
	x0, x1 := sel[0], sel[0]+d.bin-1
	y0, y1 := sel[1], sel[1]+d.bin-1
	cell := func(gene1, gene2 string, x, y []int) core.Obj {
		f := core.Obj{"gene1": gene1, "gene2": gene2, "region": core.Obj{"x": x, "y": y}}
		if types := typesPayload(d.typ); types != nil {
			f["types"] = types
		}
		if h := homodimerPayload(d.homodimer); h != "" && d.gene == d.gene2 {
			f["homodimer"] = h
		}
		return f
	}
	out := []core.Obj{cell(d.gene, d.gene2, []int{x0, x1}, []int{y0, y1})}
	if d.orientation == orientBoth && d.gene != d.gene2 {
		out = append(out, cell(d.gene2, d.gene, []int{y0, y1}, []int{x0, x1}))
	}
	return out
}

// exportContactCellRecords writes the selected cell's hybrids as the web's
// contact-cell-records.csv (Export records).
func (p *explorePage) exportContactCellRecords() {
	s := &p.cm
	d := s.data
	if d == nil || s.cmap.Selection == nil {
		return
	}
	sel := *s.cmap.Selection
	x0, x1 := cellRange(sel[0], d.bin)
	y0, y1 := cellRange(sel[1], d.bin)
	name := exportName(".csv", selectionName(p.a.Selected.Key), "cell", fmt.Sprintf("%d-%d_x_%d-%d", x0, x1, y0, y1), "records")
	p.exportRecordsOf(d.ids, "Export the cell's records", name, contactCellFilters(d, sel), "file", false)
}

// capCode is the web's colourCap value for a cap option.
func capCode(option string) string {
	switch option {
	case capOptions[0]:
		return "90"
	case capOptions[1]:
		return "95"
	case capOptions[2]:
		return "99"
	case capMaximum:
		return "max"
	}
	return "custom"
}

// exportContactParams writes the web's contact-map-parameters.json.
func (p *explorePage) exportContactParams() {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	var customCap any
	if v, err := strconv.ParseFloat(strings.TrimSpace(s.customCap), 64); err == nil && v > 0 && s.colorCap == capCustom {
		customCap = v
	}
	var selected, view any
	if sel := s.cmap.Selection; sel != nil {
		selected = map[string]any{"xStart": sel[0], "xEnd": sel[0] + d.bin - 1, "yStart": sel[1], "yEnd": sel[1] + d.bin - 1}
	}
	if len(d.count) > 0 {
		v := s.cmap.View()
		view = map[string]any{"stage": "overview", "xStart": v.XStart, "xEnd": v.XEnd + d.bin - 1, "yStart": v.YStart, "yEnd": v.YEnd + d.bin - 1}
	}
	measure := map[string]string{cmRecords: "record-count", cmSupport: "cluster-support", cmReads: "read-count"}[d.measure]
	requested := d.requested
	if requested <= 0 {
		requested = d.bin // not adjusted
	}
	scale := "linear"
	if s.scale == scaleLog {
		scale = "log"
	}
	parameters := map[string]any{
		"rnaX": d.gene, "rnaY": d.gene2, "requestedBinSize": requested, "binSize": d.bin,
		"binSizeAdjusted": d.adjusted, "measure": measure, "colourCap": capCode(s.colorCap), "customCap": customCap,
		"effectiveColourCap": d.colorCap(s.capChoice(), s.scale), "scale": scale, "palette": s.style().value,
		"orientation": map[string]string{orientBoth: "normalised", orientOriented: "original"}[d.orientation],
		"chimeraType": map[bool]string{true: "all", false: d.typ}[d.typ == allTypes], "homodimerSubset": nameOr(homodimerPayload(d.homodimer), "all"),
		"exportedView": view, "selectedCell": selected,
	}
	result := map[string]any{"cells": len(d.count), "recordsInMap": d.recordsTotal, "clusterSupport": d.supportTotal, "maximum": d.max}
	params := p.exploreParams("contact-map", parameters, result)
	// the one dataset drawn, as the web's "dataset"
	params["samples"], params["dataset"] = append([]string(nil), d.ids...), p.contactDatasetJSON(d)
	saveJSON(p.a, "Export the contact-map parameters", p.contactExportName("_parameters.json"), params)
}

// ---- ④ Compare: the selected test

func (p *comparePage) exportTarget() (*comparisonEntry, *comparisonResult, cmpContext, bool) {
	e := p.entry(p.selectedID)
	if e == nil || p.cache[p.selectedID] == nil {
		return nil, nil, cmpContext{}, false
	}
	return e, p.cache[p.selectedID], p.context(e), true
}

// exportInteractionsTSV writes the merged interactions as the web's
// interactions.tsv: in the result's order (the merge's, by padj), numbers
// as JavaScript prints them.
func (p *comparePage) exportInteractionsTSV() {
	_, result, _, ok := p.exportTarget()
	if !ok {
		return
	}
	rows := append([]cmpInteraction(nil), result.Interactions...)
	render := func() ([]byte, error) { return cmpInteractionsTSV(rows), nil }
	saveExport(p.a, "Export merged interactions", cmpExportNames["interactions"], render)
}

// cmpInteractionsTSV is the web's interactionsToTsv: direction x y
// log2FoldChange padj baseMean, "NA" for a missing number.
func cmpInteractionsTSV(rows []cmpInteraction) []byte {
	out := [][]string{{"direction", "x", "y", "log2FoldChange", "padj", "baseMean"}}
	for _, r := range rows {
		out = append(out, []string{r.Direction, strconv.Itoa(r.X), strconv.Itoa(r.Y), cmpTSVNumber(r.LFC), cmpTSVNumber(r.Padj), cmpTSVNumber(r.BaseMean)})
	}
	return webTSV(out)
}

// exportMapSVG writes the map as shown (zoom and direction) as the web's
// panelSvg.
func (p *comparePage) exportMapSVG() {
	_, result, ctx, ok := p.exportTarget()
	if !ok {
		return
	}
	panel := p.mapPanel(result, ctx)
	saveExport(p.a, "Export the differential coverage map", cmpExportNames["map_svg"], func() ([]byte, error) {
		svg, _, _ := panel.SVG()
		return svgFile(svg), nil
	})
}

func (p *comparePage) exportSimilaritySVG() {
	e, result, ctx, ok := p.exportTarget()
	if !ok || result.Similarity == nil {
		return
	}
	cells, view := p.simHeat.Cells(), p.simHeat.View()
	title := ctx.LabelA + " and " + ctx.LabelB + " · similarity heatmap"
	gene := strings.Join(e.Genes, ", ")
	panel := "#ffffff" // as similarity_heatmap.R, or ggplot's grey panel
	if p.simPanel == cmpPanelGrey {
		panel = "#ebebeb"
	}
	render := func() ([]byte, error) {
		svg, _, _ := heatSVG{Cells: cells, View: view, Size: 520, Title: title, MaxCells: svgMaxCells, Panel: panel,
			XLabel: "Arm 1 · " + gene, YLabel: "Arm 2",
			Note: fmt.Sprintf("Green: the smaller summed count of %s and %s (hyb2 plot_similarity_map), capped at the 95%% quantile (%g).",
				ctx.LabelA, ctx.LabelB, result.Summary.SimilarityCap)}.SVG()
		return svgFile(svg), nil
	}
	saveExport(p.a, "Export the similarity heatmap", exportName(".svg", ctx.LabelA, "and", ctx.LabelB, "similarity"), render)
}

// cmpParamsText summarises a test's parameters.
func cmpParamsText(e *comparisonEntry) string {
	f := func(v *float64, fallback string) string {
		if v == nil {
			return fallback
		}
		return strconv.FormatFloat(*v, 'g', -1, 64)
	}
	return fmt.Sprintf("DESeq2 · min count %s · merge range %s nt · padj < %s",
		f(e.Parameters.MinCount, "—"), f(e.Parameters.Range, "—"), f(e.Parameters.PadjCutoff, "0.05"))
}

// ---- ⑤ Structure: the arc diagram shown

func (p *structurePage) exportArcsSVG(a *structureResult) {
	spec, name := p.arcSpec(a)
	saveExport(p.a, "Export the arc diagram", name+".svg", func() ([]byte, error) {
		svg, _ := spec.SVG()
		return svgFile(svg), nil
	})
}

// exportArcsPNG draws the arc diagram off screen in the light theme, with
// the title and legend of the SVG, at twice its size as the web's PNG.
func (p *structurePage) exportArcsPNG(a *structureResult) {
	spec, name := p.arcSpec(a)
	arcs := &widgets.Arcs{A: spec.A, B: spec.B, Length: p.arcs.Length, WeightA: append([]float64(nil), p.arcs.WeightA...),
		WeightB: append([]float64(nil), p.arcs.WeightB...), Shade: p.arcs.Shade, Start: float64(spec.Start), End: float64(spec.End),
		Overlay: spec.Overlay, Mono: spec.Mono, Features: spec.Features, Gap: spec.Gap}
	height := float32(340)
	if len(spec.B) > 0 || len(spec.Overlay) > 0 {
		height = 440
	}
	if len(spec.Features) > 0 {
		height += 20
	}
	// drawn here, on the main thread: off-screen renders share the text
	// atlas with the window, so they must not run on another goroutine
	data, err := renderPNG(1000, int(height)+64, func(c *ui.Context) {
		t := c.Theme()
		ui.Text(c, spec.Title).FontSize(13).TextColor(t.Text)
		ui.Row(c).Gap(14).Wrap().Children(func() {
			colorA, colorB := cmpRed, cmpBlue
			if spec.Mono != (ui.Color{}) {
				colorA, colorB = spec.Mono, spec.Mono
			}
			stLegend(c, colorA, spec.LabelA)
			if spec.LabelB != "" {
				stLegend(c, colorB, spec.LabelB)
				if spec.Mono == (ui.Color{}) {
					stLegend(c, t.TextMuted, "in both")
				}
			}
			if len(spec.Overlay) > 0 {
				stLegend(c, cmpRed, spec.OverlayUpLabel)
				stLegend(c, cmpBlue, spec.OverlayDown)
			}
		})
		arcs.Show(c.Key("arcs"), height, cmpRed, cmpBlue, t.TextMuted, "Arc diagram of base pairs")
	})
	saveExport(p.a, "Export the arc diagram", name+".png", func() ([]byte, error) { return data, err })
}

// renderPNG draws view off screen in the light theme, width×height at
// twice the size, as a PNG. Call it on the main thread: the off-screen
// frame shares the text atlas with the window.
func renderPNG(width, height int, view func(c *ui.Context)) ([]byte, error) {
	img := ui.Render(func(c *ui.Context) {
		c.SetTheme(ui.LightTheme())
		t := c.Theme()
		ui.Column(c).FillWidth().FillHeight().Padding(8).Gap(6).Background(t.Surface).Children(func() { view(c) })
	}, width, height, 2)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// arcSpec is the arc diagram as shown, and the file name it saves under
// (without the extension).
func (p *structurePage) arcSpec(a *structureResult) (arcSVG, string) {
	gene := nameOr(a.Meta.GeneID, a.Structure.GeneID)
	labelA := "A · " + p.viewA
	if e := p.entry(p.viewA); e != nil {
		labelA = "A · " + p.evidenceNames(e)
	}
	labelB := ""
	// B only when the arcs hold it (read, aligned and comparable)
	withB := p.viewB != "" && p.shownB == p.viewB
	if withB {
		labelB = "B · " + p.viewB
		if e := p.entry(p.viewB); e != nil {
			labelB = "B · " + p.evidenceNames(e)
		}
	}
	start, end := int(math.Round(p.arcs.Start)), int(math.Round(p.arcs.End))
	spec := arcSVG{A: append([]widgets.Pair(nil), p.arcs.A...), B: append([]widgets.Pair(nil), p.arcs.B...),
		Start: start, End: end, Width: 1000, Title: fmt.Sprintf("%s · %s–%s", gene, FormatNumber(int64(start)), FormatNumber(int64(end))),
		LabelA: labelA, LabelB: labelB,
		Overlay: append([]widgets.OverlayArc(nil), p.arcs.Overlay...), OverlayUpLabel: p.overlayLabels[0], OverlayDown: p.overlayLabels[1],
		Features: append([]widgets.Feature(nil), p.arcs.Features...), Gap: p.arcs.Gap}
	if p.layers.mono {
		spec.Mono = ui.RGB(34, 34, 34)
	}
	name := strings.TrimSuffix(stViewFile("arc", ".svg"), ".svg") // the web's
	if withB {
		// A against B, which the web has not
		name = exportName("", gene, p.viewA, "vs", p.viewB)
	}
	return spec, name
}

// ---- ③ Explore: viewpoint and region

// profileRows are a profile's bins as start, end and value columns.
func profileRows(pr *widgets.Profile) [][]string {
	rows := make([][]string, pr.Len())
	for i := range rows {
		start, end, v := pr.Bin(i)
		rows[i] = []string{strconv.Itoa(start), strconv.Itoa(end), strconv.FormatFloat(v, 'f', -1, 64)}
	}
	return rows
}

// exportViewpointTSV writes the profile with the web's viewpoint.tsv columns.
func (p *explorePage) exportViewpointTSV() {
	s := &p.vp
	d := s.data
	if d == nil {
		return
	}
	rows := profileRows(s.profile)
	comments := []string{"HYB2 viewpoint · " + p.a.SelectionLabel(p.a.Selected.Key) + " · " + d.gene + ":" + span(d.start, d.end),
		fmt.Sprintf("Mean %s per nucleotide in %d nt bins; partner: %s; type: %s; %s", strings.ToLower(d.measure), d.bin, s.partner, s.typ, strings.ToLower(s.homodimer))}
	saveExport(p.a, "Export the viewpoint", exportName(".tsv", selectionName(p.a.Selected.Key), shortName(d.gene), "viewpoint"), func() ([]byte, error) {
		return tsvTable{Comments: comments, Header: []string{"start", "end", "mean_interaction_coverage"}, Rows: rows}.Bytes()
	})
}

// profileSVG describes a coverage plot: bins [start, end] with a value
// each, drawn as bars (bins thinner than a pixel merged per column, the
// column showing the largest value, as widgets.Profile).
type profileSVG struct {
	Starts, Ends []int
	Values       []float64
	// Width and Height are the plot's size in px (defaults 960 and 240).
	Width, Height float64
	// Title, XLabel, YLabel and Note are plain text (escaped here).
	Title, XLabel, YLabel, Note string
	Color                       ui.Color
}

// profileOf copies a profile's bins.
func profileOf(pr *widgets.Profile) (starts, ends []int, values []float64) {
	for i := range pr.Len() {
		start, end, v := pr.Bin(i)
		starts, ends, values = append(starts, start), append(ends, end), append(values, v)
	}
	return starts, ends, values
}

func (s profileSVG) SVG() string {
	width, height := s.Width, s.Height
	if width <= 0 {
		width = 960
	}
	if height <= 0 {
		height = 240
	}
	const left, right, bottom = 58.0, 14.0, 42.0
	top := 12.0
	if s.Title != "" {
		top = 30
	}
	noteH := 0.0
	if s.Note != "" {
		noteH = 18
	}
	fullW, fullH := left+width+right, top+height+bottom+noteH
	lo, hi, peak := 0.0, 1.0, 0.0
	if len(s.Starts) > 0 {
		lo, hi = float64(s.Starts[0]), float64(s.Ends[len(s.Ends)-1]+1)
	}
	for _, v := range s.Values {
		peak = math.Max(peak, v)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s" %s font-size="11">`,
		svgNum(fullW), svgNum(fullH), svgNum(fullW), svgNum(fullH), svgFont)
	fmt.Fprintf(&b, `<title>%s</title>`, svgText(nameOr(s.Title, "Coverage profile")))
	b.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)
	if s.Title != "" {
		fmt.Fprintf(&b, `<text x="%s" y="18" font-size="13" font-weight="600" fill="%s">%s</text>`, svgNum(left), svgInk, svgText(s.Title))
	}
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="#f6f7f9"/>`, svgNum(left), svgNum(top), svgNum(width), svgNum(height))
	if peak > 0 && hi > lo {
		scale := width / (hi - lo)
		columns := int(math.Ceil(width))
		tops := make([]float64, columns+1)
		for i, v := range s.Values {
			x0 := int((float64(s.Starts[i]) - lo) * scale)
			x1 := int((float64(s.Ends[i]+1) - lo) * scale)
			for x := max(x0, 0); x <= min(max(x1-1, x0), columns); x++ {
				tops[x] = math.Max(tops[x], v)
			}
		}
		fmt.Fprintf(&b, `<g %s shape-rendering="crispEdges">`, svgFill("fill", s.Color))
		for x, v := range tops {
			if v <= 0 {
				continue
			}
			h := v / peak * (height - 4)
			fmt.Fprintf(&b, `<rect x="%s" y="%s" width="1" height="%s"/>`, svgNum(left+float64(x)), svgNum(top+height-h), svgNum(h))
		}
		b.WriteString(`</g>`)
		for _, x := range niceTicks(lo, hi, 6) {
			px := left + (x-lo)*scale
			fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s"/>`, svgNum(px), svgNum(top+height), svgNum(px), svgNum(top+height+4), svgLine)
			fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" fill="%s">%d</text>`, svgNum(px), svgNum(top+height+16), svgMuted, int(x))
		}
		for _, v := range []float64{0, peak / 2, peak} {
			py := top + height - v/peak*(height-4)
			fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s"/>`, svgNum(left-4), svgNum(py), svgNum(left), svgNum(py), svgLine)
			fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="end" fill="%s">%s</text>`, svgNum(left-6), svgNum(py+4), svgMuted, trimFloat(v))
		}
	}
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="none" stroke="%s"/>`, svgNum(left), svgNum(top), svgNum(width), svgNum(height), svgLine)
	if s.XLabel != "" {
		fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" fill="%s">%s</text>`, svgNum(left+width/2), svgNum(top+height+32), svgMuted, svgText(s.XLabel))
	}
	if s.YLabel != "" {
		fmt.Fprintf(&b, `<text transform="translate(12 %s) rotate(-90)" text-anchor="middle" fill="%s">%s</text>`, svgNum(top+height/2), svgMuted, svgText(s.YLabel))
	}
	if s.Note != "" {
		fmt.Fprintf(&b, `<text x="%s" y="%s" fill="%s">%s</text>`, svgNum(left), svgNum(top+height+bottom+12), svgMuted, svgText(s.Note))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// profilePNG draws a profile off screen in the light theme, with a title
// and a note, at twice its size.
func profilePNG(starts, ends []int, values []float64, title, label, note string) ([]byte, error) {
	pr := widgets.NewProfile()
	pr.Set(starts, ends, values)
	return renderPNG(1040, 320, func(c *ui.Context) {
		t := c.Theme()
		ui.Text(c, title).FontSize(13).TextColor(t.Text)
		pr.Show(c.Key("profile"), 220, label, profileColor(t))
		ui.Text(c, note).FontSize(11).TextColor(t.TextMuted)
	})
}

// viewpointFigure is the viewpoint as its exports draw it.
func (p *explorePage) viewpointFigure() (profileSVG, string) {
	s := &p.vp
	d := s.data
	starts, ends, values := profileOf(s.profile)
	fig := profileSVG{Starts: starts, Ends: ends, Values: values, Color: ui.LightTheme().Accent,
		Title:  "HYB2 viewpoint · " + p.a.SelectionLabel(p.a.Selected.Key) + " · " + d.gene + ":" + span(d.start, d.end),
		XLabel: d.gene + " (nt)", YLabel: "Mean " + strings.ToLower(d.measure) + " per nt",
		Note: fmt.Sprintf("%d nt bins · partner: %s · type: %s · %s", d.bin, s.partner, s.typ, strings.ToLower(s.homodimer))}
	return fig, exportName("", selectionName(p.a.Selected.Key), shortName(d.gene), "viewpoint")
}

func (p *explorePage) exportViewpointSVG() {
	if p.vp.data == nil {
		return
	}
	fig, name := p.viewpointFigure()
	saveExport(p.a, "Export the viewpoint", name+".svg", func() ([]byte, error) { return svgFile(fig.SVG()), nil })
}

func (p *explorePage) exportViewpointPNG() {
	if p.vp.data == nil {
		return
	}
	fig, name := p.viewpointFigure()
	// drawn on the main thread (see exportArcsPNG)
	data, err := profilePNG(fig.Starts, fig.Ends, fig.Values, fig.Title, "Viewpoint coverage", fig.Note)
	saveExport(p.a, "Export the viewpoint", name+".png", func() ([]byte, error) { return data, err })
}

// exportViewpointParams writes the web's viewpoint-parameters.json.
func (p *explorePage) exportViewpointParams() {
	s := &p.vp
	d := s.data
	if d == nil {
		return
	}
	var requested int
	fmt.Sscanf(s.resolution, "%d", &requested)
	measure := map[string]string{"records": "record-count", "support": "cluster-support", "reads": "read-count"}[measurePayload(d.measure)]
	partner := ""
	if s.partner != allPartners {
		partner = s.partner
	}
	parameters := map[string]any{
		"rna": d.gene, "partner": optName(partner), "chimeraType": nameOr(s.typ, allTypes), "homodimerSubset": nameOr(homodimerPayload(s.homodimer), "all"),
		"requestedStart": optInt(s.from), "requestedEnd": optInt(s.to), "effectiveStart": d.start, "effectiveEnd": d.end,
		"requestedBinSize": requested, "effectiveBinSize": d.bin, "measure": measure,
	}
	result := map[string]any{"matchingRecords": d.records, "reads": d.reads, "armContributions": d.arms}
	saveJSON(p.a, "Export the viewpoint parameters", exportName(".json", selectionName(p.a.Selected.Key), shortName(d.gene), "viewpoint-parameters"),
		p.exploreParams("viewpoint", parameters, result))
}

// regionAnchor is the analysis_hybrids anchor filter of the region's
// hybrids (the rows analysis_region counts).
func regionAnchor(d *regionData) core.Obj {
	anchor := core.Obj{"gene": d.gene, "start": d.f.start, "end": d.f.end, "overlap": "any"}
	if d.f.overlap == overlapRules[1] {
		anchor["overlap"] = "contained"
	}
	if d.f.partner != "" {
		anchor["partner"] = d.f.partner
	}
	if d.f.partnerStart > 0 {
		anchor["partner_start"], anchor["partner_end"] = d.f.partnerStart, d.f.pEnd
	}
	filters := core.Obj{"anchor": anchor}
	if types := typesPayload(d.f.typ); types != nil {
		filters["types"] = types
	}
	return filters
}

// exportRegionRecords writes every hybrid of the region (not only the
// strongest interactions listed) as the web's region-interactions.csv or
// .hyb.
func (p *explorePage) exportRegionRecords(d *regionData, hyb bool) {
	ext := ".csv"
	if hyb {
		ext = ".hyb"
	}
	p.exportRecords("Export the region records", p.regionExportName(d, ext, "records"), []core.Obj{regionAnchor(d)}, "file", hyb)
}

func (p *explorePage) exportRegionProfileSVG(d *regionData) {
	starts, ends, values := profileOf(p.rg.profile)
	fig := profileSVG{Starts: starts, Ends: ends, Values: values, Color: ui.LightTheme().Accent,
		Title:  "HYB2 region · " + p.a.SelectionLabel(p.a.Selected.Key) + " · " + d.gene + ":" + span(d.f.start, d.f.end),
		XLabel: d.gene + " (nt)", YLabel: "Cluster support",
		Note: fmt.Sprintf("%s · %d nt bins of the anchor arm", regionSubtitle(d.f), d.bin)}
	saveExport(p.a, "Export the region profile", p.regionExportName(d, ".svg", "profile"), func() ([]byte, error) {
		return svgFile(fig.SVG()), nil
	})
}

// exportRegionParams writes the web's region-parameters.json.
func (p *explorePage) exportRegionParams(d *regionData) {
	overlap := "any"
	if d.f.overlap == overlapRules[1] {
		overlap = "contained"
	}
	parameters := map[string]any{
		"rna": d.gene, "start": d.f.start, "end": d.f.end, "partner": optName(d.f.partner),
		"partnerStart": optInt(d.f.partnerStart), "partnerEnd": optInt(d.f.pEnd), "overlapRule": overlap,
		"chimeraType": nameOr(d.f.typ, allTypes), "effectiveProfileBinSize": d.bin,
	}
	result := map[string]any{"matchingRecords": d.records, "clusterSupport": d.support, "reads": d.reads, "partnerRnas": len(d.partners)}
	saveJSON(p.a, "Export the region parameters", p.regionExportName(d, ".json", "parameters"), p.exploreParams("region", parameters, result))
}

func (p *explorePage) regionComments(d *regionData) []string {
	return []string{"HYB2 region · " + p.a.SelectionLabel(p.a.Selected.Key) + " · " + d.gene + ":" + span(d.f.start, d.f.end),
		regionSubtitle(d.f)}
}

func (p *explorePage) regionExportName(d *regionData, ext, what string) string {
	return exportName(ext, selectionName(p.a.Selected.Key), shortName(d.gene), span(d.f.start, d.f.end), what)
}

func (p *explorePage) exportRegionProfileTSV(d *regionData) {
	rows := profileRows(p.rg.profile)
	comments := append(p.regionComments(d), fmt.Sprintf("Cluster support of the matching hybrids per %d nt bin of the anchor arm", d.bin))
	saveExport(p.a, "Export the region profile", p.regionExportName(d, ".tsv", "profile"), func() ([]byte, error) {
		return tsvTable{Comments: comments, Header: []string{"start", "end", "cluster_support"}, Rows: rows}.Bytes()
	})
}

func (p *explorePage) exportRegionInteractionsTSV(d *regionData) {
	rows := append([]regionRow(nil), d.rows...)
	comments := append(p.regionComments(d), "Hybrids with the same genes, arm coordinates and type merged; by reads")
	if d.hasDistinct && int64(len(rows)) < d.distinct {
		comments = append(comments, fmt.Sprintf("The %d strongest of %d distinct interactions", len(rows), d.distinct))
	}
	saveExport(p.a, "Export the region interactions", p.regionExportName(d, ".tsv", "interactions"), func() ([]byte, error) {
		out := make([][]string, len(rows))
		for i, r := range rows {
			out[i] = []string{r.Gene1, strconv.Itoa(r.Start1), strconv.Itoa(r.End1), r.Gene2, strconv.Itoa(r.Start2), strconv.Itoa(r.End2),
				r.Type, strconv.FormatInt(r.Records, 10), strconv.FormatInt(r.Reads, 10)}
		}
		header := []string{"gene1", "start1", "end1", "gene2", "start2", "end2", "type", "hybrids", "reads"}
		return tsvTable{Comments: comments, Header: header, Rows: out}.Bytes()
	})
}

// exportRegionFASTA writes the region's reference sequence as the web does:
// <gene>_<start>-<end>.fasta holding regionFASTA.
func (p *explorePage) exportRegionFASTA(d *regionData) {
	a := p.a
	pr, gene, start, end := a.Project, d.gene, d.f.start, d.f.end
	name := exportName(".fasta", fmt.Sprintf("%s_%d-%d", gene, start, end))
	saveExport(a, "Export the region sequence", name, func() ([]byte, error) {
		ref := stReferenceOf(pr, gene)
		if ref == "" {
			return nil, fmt.Errorf("no reference holds %s", gene)
		}
		raw, err := a.B.ProjectOp(pr.Path, "read_reference_sequence", core.Obj{"reference_id": ref, "name": gene})
		if err != nil {
			return nil, err
		}
		seq, _ := raw["sequence"].(string)
		header, _ := raw["header"].(string)
		text, err := regionFASTA(gene, header, seq, start, end)
		return []byte(text), err
	})
}

// regionFASTA is the web's region FASTA: ">id:start-end" with the FASTA
// id of the gene's entry (the header's first word), the sequence without
// whitespace and in capitals, the end clamped to the sequence.
func regionFASTA(gene, header, seq string, start, end int) (string, error) {
	seq = strings.ToUpper(strings.Join(strings.Fields(seq), ""))
	id := gene
	if words := strings.Fields(header); len(words) > 0 {
		id = words[0]
	}
	start, end = max(start, 1), min(end, len(seq))
	if start > end {
		return "", fmt.Errorf("the reference sequence of %s (%d nt) does not cover the region", gene, len(seq))
	}
	return fmt.Sprintf(">%s:%d-%d\n%s\n", id, start, end, seq[start-1:end]), nil
}

// ---- ④ Compare: the quick view

// exportQuickTSV writes every bin with the web's comparison.tsv columns
// (less datasets_present and similarity, which the quick view does not
// compute), in arm order.
func (p *comparePage) exportQuickTSV() {
	q := &p.quick
	d := q.data
	if d == nil {
		return
	}
	ctx := p.quickContext()
	rows := append([]cmpQuickRow(nil), p.quickRows()...)
	measure, values := "record counts", "per-million values"
	if d.Measure == "support" {
		measure = "cluster support"
	}
	if d.Normalisation == "none" {
		values = "raw values"
	}
	pc := d.Pseudocount
	if pc <= 0 {
		pc = 1
	}
	comments := []string{"HYB2 quick view · " + ctx.LabelA + " (A) vs " + ctx.LabelB + " (B) · " + q.gene,
		fmt.Sprintf("%d-nt bins of %s; condition means of %s; log2_effect = log2((A + %g) / (B + %g)); raw = summed counts; no statistics",
			d.BinSize, measure, values, pc, pc)}
	saveExport(p.a, "Export the quick view", exportName(".tsv", ctx.LabelA, "vs", ctx.LabelB, "quick"), func() ([]byte, error) {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].X != rows[j].X {
				return rows[i].X < rows[j].X
			}
			return rows[i].Y < rows[j].Y
		})
		f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
		out := make([][]string, len(rows))
		for i, r := range rows {
			out[i] = []string{strconv.Itoa(r.X), strconv.Itoa(r.X + d.BinSize - 1), strconv.Itoa(r.Y), strconv.Itoa(r.Y + d.BinSize - 1),
				f(r.A), f(r.B), tsvFloat(r.Ratio), f(r.RawA), f(r.RawB)}
		}
		header := []string{"x_start", "x_end", "y_start", "y_end", "condition_a_mean", "condition_b_mean", "log2_effect", "condition_a_raw", "condition_b_raw"}
		return tsvTable{Comments: comments, Header: header, Rows: out}.Bytes()
	})
}

// ---- ⑤ Structure: CT, base pairs and the report

// stLayerName is the web's CPLfold pair layer: "primary", then
// "pseudoknot-1", ….
func stLayerName(layer int) string {
	if layer == 0 {
		return "primary"
	}
	return "pseudoknot-" + strconv.Itoa(layer)
}

// stCTText is one CT block per record (window), as the web's .ct: local
// positions, the pair partner (0 for none) and the local position again
// as the natural numbering; the energy with two decimals.
func stCTText(gene string, records []structureRecord) string {
	gene = nameOr(gene, "RNA")
	var b strings.Builder
	for _, r := range records {
		start, _ := r.span()
		n := len(r.Sequence)
		partner := make([]int, n+2)
		for _, pr := range stRecordPairs(r) {
			if i, j := r.localOf(pr.I), r.localOf(pr.J); i >= 1 && j >= 1 {
				partner[i], partner[j] = j, i
			}
		}
		label := fmt.Sprintf("%s:%d-%d", gene, start, start+n-1)
		if r.Cutpoint > 0 {
			label = stRegionsLabel(r.Segments)
		}
		fmt.Fprintf(&b, "%d ENERGY = %s %s\n", n, stEnergyText(r.Energy, "NA"), label)
		for pos := 1; pos <= n; pos++ {
			prev, next := pos-1, pos+1
			if pos == n || pos == r.Cutpoint {
				next = 0 // no backbone across a CPLcofold strand break
			}
			if pos == r.Cutpoint+1 && r.Cutpoint > 0 {
				prev = 0
			}
			// a CPLcofold record too, as the web (A+B, the & left out); the
			// RNA positions are in the pairs TSV
			fmt.Fprintf(&b, "%d\t%c\t%d\t%d\t%d\t%d\n", pos, r.Sequence[pos-1], prev, next, partner[pos], pos)
		}
	}
	return b.String()
}

// stEnergyText is an energy as the web writes it (toFixed(2)), or none.
func stEnergyText(v *float64, none string) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return none
	}
	return jsFixed(*v, 2)
}

// stHardPairs are a result's ViennaRNA hard pairs (meta parameters).
func stHardPairs(a *structureResult) map[[2]int]bool {
	if iris := stIrisHardPairs(a); iris != nil {
		return iris // the constraints of the IRIS member shown
	}
	out := map[[2]int]bool{}
	list, _ := a.Meta.Parameters["hard_pairs"].([]any)
	for _, item := range list {
		if pr, ok := item.([]any); ok && len(pr) == 2 {
			i, _ := pr[0].(float64)
			j, _ := pr[1].(float64)
			out[stPairKey(int(i), int(j))] = true // as the pairs, lower position first
		}
	}
	return out
}

// stHybEvidence: whether CPLfold folded with HYB evidence (the web's
// evidence.source "hyb-block-intervals"); without window summaries, whether
// any pair carries a bonus.
func stHybEvidence(a *structureResult) bool {
	if len(a.Meta.Records) > 0 {
		for _, r := range a.Meta.Records {
			if r.Evidence.Source == "hyb-block-intervals" {
				return true
			}
		}
		return false
	}
	for _, w := range stWeights(a.Records) {
		if w > 0 {
			return true
		}
	}
	return false
}

// stPairsHeader is the web's rna-secondary-structure-pairs.tsv header.
var stPairsHeader = []string{"left_position", "left_base", "right_position", "right_base", "pair_type", "hard_constraint",
	"rna_cofold_evidence", "mfe_kcal_per_mol", "evidence_kind", "evidence_support", "pair_layer",
	"selected_energy_kcal_per_mol", "engine"}

// stPairsTwoStrandHeader are the columns a two-strand (CPLcofold) result
// adds: the web's cutpoint and each end's strand and position on it, then
// (desktop only) each end's RNA and position on that RNA.
var stPairsTwoStrandHeader = []string{"cutpoint", "left_strand", "left_strand_position", "right_strand", "right_strand_position",
	"left_rna", "left_rna_position", "right_rna", "right_rna_position"}

// stPairsTable is the header and rows of the pairs TSV.
func stPairsTable(records []structureRecord, engine string, hard map[[2]int]bool, hybEvidence bool) [][]string {
	header := stPairsHeader
	for _, r := range records {
		if r.Cutpoint > 0 {
			header = append(append([]string{}, stPairsHeader...), stPairsTwoStrandHeader...)
			break
		}
	}
	return append([][]string{header}, stPairsRows(records, engine, hard, hybEvidence)...)
}

// stStrandCells are a two-strand pair's stPairsTwoStrandHeader cells, i and
// j its A+B positions.
func stStrandCells(r structureRecord, i, j int) []string {
	cut := r.Cutpoint
	strand := func(k int) (string, int) {
		if k <= cut {
			return "A", k
		}
		return "B", k - cut
	}
	rna := func(k int) string {
		if len(r.Segments) == 2 {
			if k <= cut {
				return r.Segments[0].GeneID
			}
			return r.Segments[1].GeneID
		}
		return ""
	}
	si, pi := strand(i)
	sj, pj := strand(j)
	return []string{strconv.Itoa(cut), si, strconv.Itoa(pi), sj, strconv.Itoa(pj),
		rna(i), strconv.Itoa(r.rnaPosition(i)), rna(j), strconv.Itoa(r.rnaPosition(j))}
}

// stPairsRows are the web's rna-secondary-structure-pairs.tsv rows, in
// gene positions (a two-strand record: the web's A+B positions, with
// stStrandCells), sorted by the left position. evidence_kind is the
// result's: log1p_hyb_bonus for CPLfold with HYB evidence,
// rna_cofold_observations for COMRADES(ViennaRNA), none otherwise (support
// 0); the ViennaRNA columns rna_cofold_evidence and mfe_kcal_per_mol are
// empty for CPLfold; pair_layer is "nested" and engine "ViennaRNA" for the
// ViennaRNA engines.
func stPairsRows(records []structureRecord, engine string, hard map[[2]int]bool, hybEvidence bool) [][]string {
	weights := stWeights(records)
	cplfold := engine == "CPLfold" || engine == "CPLcofold"
	kind := "none"
	switch {
	case cplfold && hybEvidence:
		kind = "log1p_hyb_bonus"
	case engine == "COMRADES(ViennaRNA)":
		// the web folds COMRADES with its ViennaRNA engine and names that
		kind, engine = "rna_cofold_observations", "ViennaRNA"
	}
	type row struct {
		left  int
		cells []string
	}
	var rows []row
	for _, r := range records {
		base := func(pos int) string {
			if k := r.localOf(pos); k >= 1 {
				return string(r.Sequence[k-1])
			}
			return ""
		}
		energy := stEnergyText(r.Energy, "")
		for _, pr := range stRecordPairs(r) {
			support := "0"
			if w, ok := weights[[2]int{pr.I, pr.J}]; ok && kind != "none" && !math.IsNaN(w) {
				support = jsNumber(w)
			}
			cofold, mfe, layer := support, energy, "nested"
			if cplfold {
				cofold, mfe, layer = "", "", stLayerName(pr.Layer)
			}
			left, right := base(pr.I), base(pr.J)
			fixed := "no"
			if hard[[2]int{pr.I, pr.J}] {
				fixed = "yes"
			}
			i, j := pr.I, pr.J
			if r.Cutpoint > 0 {
				// the web's prepared (A+B) positions, strand A's end first
				// when region 2 lies before region 1 on the RNA
				var swapped bool
				if i, j, swapped = r.localPair(pr); swapped {
					left, right = right, left
				}
			}
			cells := []string{strconv.Itoa(i), left, strconv.Itoa(j), right, left + "–" + right, fixed,
				cofold, mfe, kind, support, layer, energy, engine}
			if r.Cutpoint > 0 {
				cells = append(cells, stStrandCells(r, i, j)...)
			}
			rows = append(rows, row{i, cells})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].left < rows[j].left })
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = r.cells
	}
	return out
}

// stExportFiles are the Structure downloads' file names, the web's fixed
// names (structure-ui.js); .bpseq, the VARNA script and the candidates
// JSON, which the web's result has not, are named alike.
var stExportFiles = map[string]string{
	"dbn": "rna-secondary-structure.dbn", "ct": "rna-secondary-structure.ct", "bpseq": "rna-secondary-structure.bpseq",
	"varna": "rna-secondary-structure-varna.sh", "pairs": "rna-secondary-structure-pairs.tsv",
	"report": "rna-secondary-structure-report.json", "evidence": "rna-cofold-evidence.tsv",
	"constraints": "hyb-guided-folding-constraints.tsv", "ensemble": "hyb-guided-structure-ensemble.tsv",
	"bonus": "cplfold-bonus-matrix.tsv", "candidates": "cplfold-candidates.tsv", "candidates_json": "cplfold-candidates.json",
	"iris": "iris-ensemble.tsv",
}

// stViewFile is the web's file name of a view's SVG or PNG (ext ".svg" or
// ".png"): rna-secondary-structure-arcs.svg, -folded, -radial, -circular,
// -matrix.
func stViewFile(view, ext string) string {
	stem := strings.ToLower(view)
	if stem == "arc" {
		stem = "arcs"
	}
	return "rna-secondary-structure-" + stem + ext
}

func (p *structurePage) exportCT(a *structureResult) {
	gene, records := nameOr(a.Meta.GeneID, a.Structure.GeneID), stPickRecords(a.Records, p.record)
	saveExport(p.a, "Save the CT file", stExportFiles["ct"], func() ([]byte, error) {
		return []byte(stCTText(gene, records)), nil
	})
}

// stBPSEQText is one BPSEQ block per record (window), as the web's .bpseq:
// a comment line, then local position, base and partner (0 for none).
// BPSEQ has no strand break, so a CPLcofold record names its strands in
// the comment.
func stBPSEQText(gene string, records []structureRecord) string {
	gene = nameOr(gene, "RNA")
	var b strings.Builder
	for _, r := range records {
		start, _ := r.span()
		n := len(r.Sequence)
		partner := make([]int, n+1)
		for _, pr := range stRecordPairs(r) {
			if i, j := r.localOf(pr.I), r.localOf(pr.J); i >= 1 && j >= 1 {
				partner[i], partner[j] = j, i
			}
		}
		label := fmt.Sprintf("%s:%d-%d", gene, start, start+n-1)
		if r.Cutpoint > 0 {
			label = fmt.Sprintf("%s; two strands: A is 1-%d, B %d-%d", stRegionsLabel(r.Segments), r.Cutpoint, r.Cutpoint+1, n)
		}
		fmt.Fprintf(&b, "# %s; energy %s\n", label, stEnergyText(r.Energy, "NA"))
		for pos := 1; pos <= n; pos++ {
			fmt.Fprintf(&b, "%d %c %d\n", pos, r.Sequence[pos-1], partner[pos])
		}
	}
	return b.String()
}

// stShellQuote is a single-quoted shell word.
func stShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// stVARNAText is a shell script with one VARNAcmd call per record, as the
// web's VARNA command: the sequence and dot-bracket (pseudoknot layers as
// brackets), VARNA's own radiate layout, one SVG per record.
func stVARNAText(gene string, records []structureRecord) string {
	gene = nameOr(gene, "RNA")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# VARNA command generated by HYB2 Desktop. Requires VARNA (GPL, varna.lri.fr) and Java.\n" +
		"# VARNA computes its own layout; option spellings follow VARNA 3.9.\n")
	for k, r := range records {
		start, _ := r.span()
		n := len(r.Sequence)
		title := fmt.Sprintf("%s:%d-%d", gene, start, start+n-1)
		if r.Cutpoint > 0 {
			title = stRegionsLabel(r.Segments)
			fmt.Fprintf(&b, "# Two strands: A is 1-%d, B %d-%d; VARNA draws them as one chain.\n", r.Cutpoint, r.Cutpoint+1, n)
		}
		out := fmt.Sprintf("hyb2-structure-%d.svg", k+1)
		fmt.Fprintf(&b, "java -cp VARNA.jar fr.orsay.lri.varna.applications.VARNAcmd \\\n  -sequenceDBN %s \\\n  -structureDBN %s \\\n"+
			"  -algorithm radiate \\\n  -title %s \\\n  -resolution 4.0 \\\n  -o %s\n",
			stShellQuote(r.Sequence), stShellQuote(r.DBN), stShellQuote(title), out)
	}
	return b.String()
}

func (p *structurePage) exportBPSEQ(a *structureResult) {
	gene, records := nameOr(a.Meta.GeneID, a.Structure.GeneID), stPickRecords(a.Records, p.record)
	saveExport(p.a, "Save the BPSEQ file", stExportFiles["bpseq"], func() ([]byte, error) {
		return []byte(stBPSEQText(gene, records)), nil
	})
}

func (p *structurePage) exportVARNA(a *structureResult) {
	gene, records := nameOr(a.Meta.GeneID, a.Structure.GeneID), stPickRecords(a.Records, p.record)
	saveExport(p.a, "Save the VARNA command", stExportFiles["varna"], func() ([]byte, error) {
		return []byte(stVARNAText(gene, records)), nil
	})
}

func (p *structurePage) exportPairsTSV(a *structureResult) {
	records, engine, hard := stPickRecords(a.Records, p.record), nameOr(a.Meta.Engine, "CPLfold"), stHardPairs(a)
	evidence := stHybEvidence(a)
	saveExport(p.a, "Save the base pairs", stExportFiles["pairs"], func() ([]byte, error) {
		return webTSV(stPairsTable(records, engine, hard, evidence)), nil
	})
}

// exportReport writes the result as the web's structure report JSON:
// what was folded, how, and each window's structure.
func (p *structurePage) exportReport(a *structureResult) {
	var windows []map[string]any
	for _, r := range a.Records {
		start, end := r.span()
		pairs, knotted := 0, 0
		for _, pr := range stRecordPairs(r) {
			pairs++
			if pr.Layer > 0 {
				knotted++
			}
		}
		window := map[string]any{"start": start, "end": end, "sequence": r.Sequence, "dot_bracket": r.DBN,
			"energy_kcal_per_mol": r.Energy, "base_pairs": pairs, "pseudoknotted_pairs": knotted}
		if r.Cutpoint > 0 {
			// CPLcofold: strand A is the first cutpoint nucleotides
			window["cutpoint"], window["regions"] = r.Cutpoint, r.Segments
		}
		windows = append(windows, window)
	}
	payload := map[string]any{
		"application": "HYB2 Desktop", "version": nameOr(reportAppVersion(), "development build"), "analysis": "rna-secondary-structure",
		"structure_id": p.viewA, "engine": nameOr(a.Meta.Engine, "CPLfold"), "rna": nameOr(a.Meta.GeneID, a.Structure.GeneID),
		"mode": nameOr(a.Meta.Mode, a.Structure.Mode), "parameters": a.Meta.Parameters, "evidence_samples": a.Structure.Samples,
		"created_at": a.Structure.CreatedAt, "total_seconds": a.Meta.Timings.TotalS, "summary": a.Structure.Summary, "windows": windows,
	}
	if iris := p.irisReport(a); iris != nil {
		payload["iris"] = iris
	}
	p.stReportWeb(a, payload) // the web's sections (structure_report.go)
	saveExport(p.a, "Save the structure report", stExportFiles["report"], func() ([]byte, error) {
		payload["sourceHyb"], payload["sourceFasta"] = stReportInputs(a)
		out, err := json.MarshalIndent(payload, "", "  ")
		return append(out, '\n'), err
	})
}
