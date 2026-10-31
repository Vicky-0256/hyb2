package views

// The pages' "Export…" buttons: what each page writes, from the state it
// shows. The data is captured on the main thread; the file is rendered
// and written by saveExport off it.

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	path, ids := p.a.Project.Path, append([]string(nil), p.ids...)
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

// contactInView returns the indexes of the cells in the map's view.
func contactInView(d *contactData, v widgets.Window) []int {
	var out []int
	for i := range d.count {
		x, y := float64(d.x[i]), float64(d.y[i])
		if x+float64(d.bin) < v.X0 || x > v.X1 || y+float64(d.bin) < v.Y0 || y > v.Y1 {
			continue
		}
		out = append(out, i)
	}
	return out
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

func (p *explorePage) contactTitle() string {
	d := p.cm.data
	genes := d.gene
	if d.gene2 != d.gene {
		genes += " × " + d.gene2
	}
	return fmt.Sprintf("%s · %s · %d nt bins", p.a.SelectionLabel(p.a.Selected.Key), genes, d.bin)
}

func (p *explorePage) exportContactSVG() {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	// light colours whatever the theme, as the web's exports
	cells, view := d.cells(s.capChoice(), s.scale, false, s.mono()), s.heat.View()
	title, note := p.contactTitle(), p.contactNote()
	render := func() ([]byte, error) {
		// Weakest first, so a capped map keeps the strongest cells.
		order := contactInView(d, view)
		sort.SliceStable(order, func(i, j int) bool { return d.count[order[i]] < d.count[order[j]] })
		sorted := make([]widgets.Cell, 0, len(order))
		for _, i := range order {
			if i < len(cells) {
				sorted = append(sorted, cells[i])
			}
		}
		svg, _, _ := heatSVG{Cells: sorted, View: view, Size: 520, Title: title, MaxCells: svgMaxCells,
			XLabel: "Arm 1 · " + d.gene, YLabel: "Arm 2 · " + d.gene2, Note: note}.SVG()
		return svgFile(svg), nil
	}
	saveExport(p.a, "Export the contact map", p.contactExportName(".svg"), render)
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
		records, err := p.contactCells(d, measureRecords)
		if err != nil {
			return nil, err
		}
		support, err := p.contactCells(d, measureSupport)
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

// contactNote says how the map's colours read.
func (p *explorePage) contactNote() string {
	s := &p.cm
	d := s.data
	style := "Teal–violet scale"
	if s.mono() {
		style = "White–black scale"
	}
	return fmt.Sprintf("%s, saturated at %s %s (%s, %s scale).", style,
		formatCount(capCount(d.colorCap(s.capChoice(), s.scale), s.scale)),
		strings.ToLower(d.measure), strings.ToLower(s.colorCap), strings.ToLower(s.scale))
}

// exportContactPNG draws the map as shown (its view, colour style and cap)
// off screen in the light theme, with its title, axes and note.
func (p *explorePage) exportContactPNG() {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	mono := s.mono()
	cells, view := d.cells(s.capChoice(), s.scale, false, mono), s.heat.View()
	title, note := p.contactTitle(), p.contactNote()
	lengthX, lengthY := float64(d.lengthX), float64(d.lengthY)
	axes := "X: arm 1 · " + d.gene + "    Y: arm 2 · " + d.gene2
	saveExport(p.a, "Export the contact map", p.contactExportName(".png"), func() ([]byte, error) {
		const size = 560
		heat := widgets.NewHeatmap()
		heat.Background = paperOf(mono)
		heat.SetCells(cells, lengthX, lengthY)
		heat.SetView(view)
		return renderPNG(size+60, size+120, func(c *ui.Context) {
			t := c.Theme()
			ui.Text(c, title).FontSize(13).TextColor(t.Text)
			heat.Show(c.Key("map"), size, "Contact map")
			ui.Text(c, axes).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, note).FontSize(11).TextColor(t.TextMuted)
		})
	})
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

// contactCellFilters are the analysis_hybrids filters of the hybrids in
// cell i (both arms over its bins), as the map counts them: the reverse
// arm order too for both orders of two RNAs.
func contactCellFilters(d *contactData, i int) []core.Obj {
	x0, x1 := d.x[i], d.x[i]+d.bin-1
	y0, y1 := d.y[i], d.y[i]+d.bin-1
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
// contact-cell-records.csv.
func (p *explorePage) exportContactCellRecords() {
	s := &p.cm
	d := s.data
	if d == nil || s.picked < 0 || s.picked >= len(d.count) {
		return
	}
	x0, x1 := cellRange(d.x[s.picked], d.bin)
	y0, y1 := cellRange(d.y[s.picked], d.bin)
	name := exportName(".csv", selectionName(p.a.Selected.Key), "cell", fmt.Sprintf("%d-%d_x_%d-%d", x0, x1, y0, y1), "records")
	p.exportRecords("Export the cell's records", name, contactCellFilters(d, s.picked), "file", false)
}

// exportContactParams writes the web's contact-map-parameters.json.
func (p *explorePage) exportContactParams() {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	view := s.heat.View()
	var customCap any
	if v, err := strconv.ParseFloat(strings.TrimSpace(s.customCap), 64); err == nil && v > 0 && s.colorCap == capCustom {
		customCap = v
	}
	var selected any
	if s.picked >= 0 && s.picked < len(d.count) {
		selected = map[string]any{"xStart": d.x[s.picked], "xEnd": d.x[s.picked] + d.bin - 1, "yStart": d.y[s.picked], "yEnd": d.y[s.picked] + d.bin - 1}
	}
	measure := map[string]string{measureRecords: "record-count", measureSupport: "cluster-support", measureReads: "read-count"}[d.measure]
	palette := "colour"
	if s.mono() {
		palette = "monochrome"
	}
	requested := d.requested
	if requested <= 0 {
		requested = d.bin // not adjusted
	}
	parameters := map[string]any{
		"rnaX": d.gene, "rnaY": d.gene2, "requestedBinSize": requested, "binSize": d.bin,
		"binSizeAdjusted": d.adjusted, "measure": measure, "colourCap": s.colorCap, "customCap": customCap,
		"effectiveColourCap": capCount(d.colorCap(s.capChoice(), s.scale), s.scale), "scale": strings.ToLower(s.scale), "palette": palette,
		"orientation": map[string]string{orientBoth: "normalised", orientOriented: "original"}[d.orientation],
		"chimeraType": nameOr(d.typ, allTypes), "homodimerSubset": nameOr(homodimerPayload(d.homodimer), "all"),
		"exportedView": map[string]any{"xStart": math.Max(0, math.Floor(view.X0)), "xEnd": math.Ceil(view.X1),
			"yStart": math.Max(0, math.Floor(view.Y0)), "yEnd": math.Ceil(view.Y1)},
		"selectedCell": selected,
	}
	result := map[string]any{"cells": len(d.count), "recordsInMap": d.records, "maximum": d.max}
	saveJSON(p.a, "Export the contact-map parameters", p.contactExportName("_parameters.json"), p.exploreParams("contact-map", parameters, result))
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
// interactions.tsv: the up rows then the down rows, in the result's order,
// numbers as JavaScript prints them.
func (p *comparePage) exportInteractionsTSV() {
	_, result, ctx, ok := p.exportTarget()
	if !ok {
		return
	}
	rows := append([]cmpInteraction(nil), result.Interactions...)
	render := func() ([]byte, error) { return cmpInteractionsTSV(rows), nil }
	saveExport(p.a, "Export merged interactions", exportName(".tsv", ctx.LabelA, "vs", ctx.LabelB, "interactions"), render)
}

// cmpInteractionsTSV is the web's interactionsToTsv: direction x y
// log2FoldChange padj baseMean, "NA" for a missing number.
func cmpInteractionsTSV(rows []cmpInteraction) []byte {
	num := func(v *float64) string {
		if v == nil || math.IsNaN(*v) {
			return "NA"
		}
		return jsNumber(*v)
	}
	sorted := append([]cmpInteraction(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Direction == "up" && sorted[j].Direction != "up" })
	out := [][]string{{"direction", "x", "y", "log2FoldChange", "padj", "baseMean"}}
	for _, r := range sorted {
		out = append(out, []string{r.Direction, strconv.Itoa(r.X), strconv.Itoa(r.Y), num(r.LFC), num(r.Padj), num(r.BaseMean)})
	}
	return webTSV(out)
}

func (p *comparePage) exportMapSVG() {
	e, _, ctx, ok := p.exportTarget()
	if !ok {
		return
	}
	cells, view := p.heat.Cells(), p.heat.View()
	directions := []string{"", " · higher in A only", " · higher in B only"}
	title := ctx.LabelA + " vs " + ctx.LabelB + " · significant cells" + directions[min(max(p.direction, 0), 2)]
	gene := strings.Join(e.Genes, ", ")
	render := func() ([]byte, error) {
		svg, _, _ := heatSVG{Cells: cells, View: view, Size: 520, Title: title, MaxCells: svgMaxCells,
			XLabel: "Arm 1 · " + gene, YLabel: "Arm 2",
			Note: "Red: higher in " + ctx.LabelA + "; blue: higher in " + ctx.LabelB + "; stronger colour, smaller padj."}.SVG()
		return svgFile(svg), nil
	}
	saveExport(p.a, "Export the significant map", exportName(".svg", ctx.LabelA, "vs", ctx.LabelB, "map"), render)
}

func (p *comparePage) exportSimilaritySVG() {
	e, result, ctx, ok := p.exportTarget()
	if !ok || result.Similarity == nil {
		return
	}
	cells, view := p.simHeat.Cells(), p.simHeat.View()
	title := ctx.LabelA + " and " + ctx.LabelB + " · similarity heatmap"
	gene := strings.Join(e.Genes, ", ")
	render := func() ([]byte, error) {
		svg, _, _ := heatSVG{Cells: cells, View: view, Size: 520, Title: title, MaxCells: svgMaxCells,
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
		Overlay: spec.Overlay, Mono: spec.Mono, Features: spec.Features}
	height := float32(340)
	if len(spec.B) > 0 || len(spec.Overlay) > 0 {
		height = 440
	}
	if len(spec.Features) > 0 {
		height += 20
	}
	saveExport(p.a, "Export the arc diagram", name+".png", func() ([]byte, error) {
		return renderPNG(1000, int(height)+64, func(c *ui.Context) {
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
	})
}

// renderPNG draws view off screen in the light theme, width×height at
// twice the size, as a PNG.
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
	if p.viewB != "" {
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
		Features: append([]widgets.Feature(nil), p.arcs.Features...)}
	if p.layers.mono {
		spec.Mono = ui.RGB(34, 34, 34)
	}
	name := exportName("", gene, p.viewA)
	if p.viewB != "" {
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
	saveExport(p.a, "Export the viewpoint", name+".png", func() ([]byte, error) {
		return profilePNG(fig.Starts, fig.Ends, fig.Values, fig.Title, "Viewpoint coverage", fig.Note)
	})
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
	measure := map[string]string{measureRecords: "record-count", measureSupport: "cluster-support", measureReads: "read-count"}[d.measure]
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
	comments := []string{"HYB2 quick view · " + ctx.LabelA + " (A) vs " + ctx.LabelB + " (B) · " + q.gene,
		fmt.Sprintf("%d-nt bins; condition means of per-million values; log2_effect = log2((A + 1) / (B + 1)); raw = summed counts; no statistics", d.BinSize)}
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
			natural := pos
			if r.Cutpoint > 0 {
				natural = r.rnaPosition(pos) // as the runtime's structure.ct
			}
			fmt.Fprintf(&b, "%d\t%c\t%d\t%d\t%d\t%d\n", pos, r.Sequence[pos-1], prev, next, partner[pos], natural)
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
	out := map[[2]int]bool{}
	list, _ := a.Meta.Parameters["hard_pairs"].([]any)
	for _, item := range list {
		if pr, ok := item.([]any); ok && len(pr) == 2 {
			i, _ := pr[0].(float64)
			j, _ := pr[1].(float64)
			out[[2]int{int(i), int(j)}] = true
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

// stPairsRows are the web's rna-secondary-structure-pairs.tsv rows, in
// gene positions, sorted by the left position. evidence_kind is the
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
			rows = append(rows, row{pr.I, []string{strconv.Itoa(pr.I), left, strconv.Itoa(pr.J), right, left + "–" + right, fixed,
				cofold, mfe, kind, support, layer, energy, engine}})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].left < rows[j].left })
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = r.cells
	}
	return out
}

func (p *structurePage) exportName(a *structureResult, ext string) string {
	return exportName(ext, nameOr(a.Meta.GeneID, a.Structure.GeneID), p.viewA)
}

func (p *structurePage) exportCT(a *structureResult) {
	gene, records := nameOr(a.Meta.GeneID, a.Structure.GeneID), stPickRecords(a.Records, p.record)
	saveExport(p.a, "Save the CT file", p.exportName(a, ".ct"), func() ([]byte, error) {
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
	saveExport(p.a, "Save the BPSEQ file", p.exportName(a, ".bpseq"), func() ([]byte, error) {
		return []byte(stBPSEQText(gene, records)), nil
	})
}

func (p *structurePage) exportVARNA(a *structureResult) {
	gene, records := nameOr(a.Meta.GeneID, a.Structure.GeneID), stPickRecords(a.Records, p.record)
	saveExport(p.a, "Save the VARNA command", p.exportName(a, "_varna.sh"), func() ([]byte, error) {
		return []byte(stVARNAText(gene, records)), nil
	})
}

func (p *structurePage) exportPairsTSV(a *structureResult) {
	records, engine, hard := stPickRecords(a.Records, p.record), nameOr(a.Meta.Engine, "CPLfold"), stHardPairs(a)
	evidence := stHybEvidence(a)
	saveExport(p.a, "Save the base pairs", p.exportName(a, "_pairs.tsv"), func() ([]byte, error) {
		return webTSV(append([][]string{stPairsHeader}, stPairsRows(records, engine, hard, evidence)...)), nil
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
	saveExport(p.a, "Save the structure report", p.exportName(a, "_report.json"), func() ([]byte, error) {
		out, err := json.MarshalIndent(payload, "", "  ")
		return append(out, '\n'), err
	})
}
