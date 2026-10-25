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
	cells, view := d.cells(s.capChoice(), s.scale, false), s.heat.View()
	title := p.contactTitle()
	note := fmt.Sprintf("Teal–violet scale, saturated at %s %s (%s, %s scale).",
		formatCount(capCount(d.colorCap(s.capChoice(), s.scale), s.scale)),
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
			i, j := pr.I-start+1, pr.J-start+1
			if i >= 1 && j >= 1 && i <= n && j <= n {
				partner[i], partner[j] = j, i
			}
		}
		fmt.Fprintf(&b, "%d ENERGY = %s %s:%d-%d\n", n, stEnergyText(r.Energy, "NA"), gene, start, start+n-1)
		for pos := 1; pos <= n; pos++ {
			next := pos + 1
			if pos == n {
				next = 0
			}
			fmt.Fprintf(&b, "%d\t%c\t%d\t%d\t%d\t%d\n", pos, r.Sequence[pos-1], pos-1, next, partner[pos], pos)
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
	cplfold := engine == "CPLfold"
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
		start, _ := r.span()
		base := func(pos int) string {
			if i := pos - start; i >= 0 && i < len(r.Sequence) {
				return string(r.Sequence[i])
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
		windows = append(windows, map[string]any{"start": start, "end": end, "sequence": r.Sequence, "dot_bracket": r.DBN,
			"energy_kcal_per_mol": r.Energy, "base_pairs": pairs, "pseudoknotted_pairs": knotted})
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
