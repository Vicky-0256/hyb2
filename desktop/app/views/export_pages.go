package views

// The pages' "Export…" buttons: what each page writes, from the state it
// shows. The data is captured on the main thread; the file is rendered
// and written by saveExport off it.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

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
		header := []string{"id", "gene1", "start1", "end1", "gene2", "start2", "end2", "reads", "type", "dG"}
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
				r.Gene2, strconv.Itoa(r.Start2), strconv.Itoa(r.End2), strconv.Itoa(r.Reads), r.Type, dg}
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
	cells, view := d.cells(s.colorCap, s.scale, false), s.heat.View()
	title := p.contactTitle()
	note := fmt.Sprintf("Teal–violet scale, saturated at %s %s (%s, %s scale).",
		formatCount(capCount(d.colorCap(s.colorCap, s.scale), s.scale)),
		strings.ToLower(d.measure), strings.ToLower(s.colorCap), strings.ToLower(s.scale))
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

func (p *explorePage) exportContactTSV() {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	view := s.heat.View()
	comments := []string{"HYB2 contact map · " + p.contactTitle(),
		fmt.Sprintf("Measure: %s · orientation: %s · type: %s", strings.ToLower(d.measure), d.orientation, d.typ),
		fmt.Sprintf("Cells in view: arm 1 %d–%d, arm 2 %d–%d", int(view.X0), int(math.Ceil(view.X1)), int(view.Y0), int(math.Ceil(view.Y1)))}
	render := func() ([]byte, error) {
		var rows [][]string
		for _, i := range contactInView(d, view) {
			x0, x1 := cellRange(d.x[i], d.bin)
			y0, y1 := cellRange(d.y[i], d.bin)
			rows = append(rows, []string{d.gene, strconv.Itoa(x0), strconv.Itoa(x1), d.gene2, strconv.Itoa(y0), strconv.Itoa(y1), strconv.Itoa(d.count[i])})
		}
		header := []string{"gene1", "start1", "end1", "gene2", "start2", "end2", strings.ToLower(d.measure)}
		return tsvTable{Comments: comments, Header: header, Rows: rows}.Bytes()
	}
	saveExport(p.a, "Export the contact map cells", p.contactExportName(".tsv"), render)
}

// ---- ④ Compare: the selected test

func (p *comparePage) exportTarget() (*comparisonEntry, *comparisonResult, cmpContext, bool) {
	e := p.entry(p.selectedID)
	if e == nil || p.cache[p.selectedID] == nil {
		return nil, nil, cmpContext{}, false
	}
	return e, p.cache[p.selectedID], p.context(e), true
}

func (p *comparePage) exportInteractionsTSV() {
	e, result, ctx, ok := p.exportTarget()
	if !ok {
		return
	}
	rows := append([]cmpInteraction(nil), result.Interactions...)
	comments := []string{"HYB2 differential test " + e.ID + " · " + ctx.LabelA + " (A) vs " + ctx.LabelB + " (B)",
		"Genes: " + strings.Join(e.Genes, ", "), cmpParamsText(e) + " · log2FoldChange = A / B; 10-nt cells merged into interactions"}
	render := func() ([]byte, error) {
		sort.SliceStable(rows, func(i, j int) bool { return cmpPadj(rows[i].Padj) < cmpPadj(rows[j].Padj) })
		out := make([][]string, len(rows))
		for i, r := range rows {
			direction := "higher in A"
			if r.Direction == "down" {
				direction = "higher in B"
			}
			out[i] = []string{r.Direction, direction, strconv.Itoa(r.X), strconv.Itoa(r.X + 9), strconv.Itoa(r.Y), strconv.Itoa(r.Y + 9),
				tsvFloat(r.LFC), tsvFloat(r.Padj), tsvFloat(r.BaseMean)}
		}
		header := []string{"direction", "meaning", "arm1_start", "arm1_end", "arm2_start", "arm2_end", "log2FoldChange", "padj", "baseMean"}
		return tsvTable{Comments: comments, Header: header, Rows: out}.Bytes()
	}
	saveExport(p.a, "Export merged interactions", exportName(".tsv", ctx.LabelA, "vs", ctx.LabelB, "interactions"), render)
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
	name := exportName(".svg", gene, p.viewA)
	if p.viewB != "" {
		name = exportName(".svg", gene, p.viewA, "vs", p.viewB)
	}
	saveExport(p.a, "Export the arc diagram", name, func() ([]byte, error) {
		svg, _ := spec.SVG()
		return svgFile(svg), nil
	})
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
		fmt.Sprintf("Mean %s per nucleotide in %d nt bins; partner: %s; type: %s", strings.ToLower(d.measure), d.bin, s.partner, s.typ)}
	saveExport(p.a, "Export the viewpoint", exportName(".tsv", selectionName(p.a.Selected.Key), shortName(d.gene), "viewpoint"), func() ([]byte, error) {
		return tsvTable{Comments: comments, Header: []string{"start", "end", "mean_interaction_coverage"}, Rows: rows}.Bytes()
	})
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
// <gene>_<start>-<end>.fasta with the header ">gene:start-end".
func (p *explorePage) exportRegionFASTA(d *regionData) {
	a := p.a
	pr, gene, start, end := a.Project, d.gene, d.f.start, d.f.end
	name := exportName(".fasta", fmt.Sprintf("%s_%d-%d", gene, start, end))
	saveExport(a, "Export the region sequence", name, func() ([]byte, error) {
		seq, err := fetchSequence(a.B, pr, pr.Path, gene)
		if err != nil {
			return nil, err
		}
		if end > len(seq) {
			return nil, fmt.Errorf("%s is %d nt; the region ends at %d", gene, len(seq), end)
		}
		return []byte(fmt.Sprintf(">%s:%d-%d\n%s\n", gene, start, end, seq[start-1:end])), nil
	})
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
