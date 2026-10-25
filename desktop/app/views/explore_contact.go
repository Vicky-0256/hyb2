package views

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/widgets"
)

// Contact map: analysis_contact_bins drawn with widgets.Heatmap.

const (
	// contactMaxCells is the web page's limit (web/page-data.js): the backend
	// doubles the chosen bin until the map has at most this many non-empty
	// cells (shown as adjusted).
	contactMaxCells = 250_000
	binDefault      = "10 nt"

	orientBoth     = "Both arm orders"
	orientOriented = "Arm 1 on X"
	measureRecords = "Records"
	// measureSupport is the web's cluster support; measureReads the raw
	// read counts in the read ids (desktop only).
	measureSupport = "Support"
	measureReads   = "Reads"
	scaleLinear    = "Linear"
	scaleLog       = "Log"
	// capCustom is the web's custom cap: a value on the displayed scale
	// (log(1 + count) on the Log scale), the maximum when not positive.
	capCustom = "Custom"
)

var (
	binOptions   = []string{"5 nt", "10 nt", "25 nt", "50 nt", "100 nt"}
	capOptions   = []string{"90th percentile", "95th percentile", "99th percentile", "Maximum", capCustom}
	capFractions = []float64{0.90, 0.95, 0.99, 1}
	// homodimerOptions are the web's contact-map subsets of the homodimer
	// proxy (both arms on one RNA, overlap score >= 5).
	homodimerOptions = []string{"All hybrids", "Homodimer proxy only", "Exclude homodimer proxy"}
	measures         = []string{measureRecords, measureSupport, measureReads}
	orientations     = []string{orientBoth, orientOriented}
	scaleOptions     = []string{scaleLinear, scaleLog}
	mapSizeTarget    = float32(430)
)

type contactState struct {
	gene2       string // partner gene on Y (the selected gene by default)
	bin         string
	measure     string
	orientation string
	typ         string
	homodimer   string
	colorCap    string
	customCap   string
	scale       string
	dark        bool // the theme the cells were coloured for

	load loader
	data *contactData
	heat *widgets.Heatmap
	// picked is the chosen cell (index into data), -1 for none.
	picked int
	hover  string
	// focus, when set, is a gene range {x, x end, y, y end} to pick and
	// zoom to once the map loads (from Compare).
	focus *[4]int
}

// contactData is one analysis_contact_bins answer, ready to draw.
type contactData struct {
	gene, gene2      string
	bin, requested   int
	adjusted         bool
	lengthX, lengthY int
	total, max       int
	measure          string
	orientation      string
	typ              string
	homodimer        string
	x, y, count      []int
	sorted           []int // counts, ascending (for the colour cap)
	at               map[[2]int]int
	// records is the number of hybrids in the map (the web's "Records in
	// map"): total for the Records measure, else from a records query.
	records int
	// what the map was queried with (for the export's other measures)
	path string
	ids  []string
}

func (s *contactState) init() {
	s.bin, s.measure, s.orientation, s.typ, s.homodimer = binDefault, measureRecords, orientBoth, allTypes, homodimerOptions[0]
	s.colorCap, s.scale = capOptions[1], scaleLinear
	s.heat = widgets.NewHeatmap()
	s.picked = -1
}

func (s *contactState) selectionChanged(gene string, geneChanged bool) {
	if geneChanged || s.gene2 == "" {
		s.gene2 = gene
	}
	s.picked = -1
	s.hover = ""
}

func (p *explorePage) contactGene2() string {
	if p.cm.gene2 == "" {
		return p.gene
	}
	return p.cm.gene2
}

func (p *explorePage) contactKey() string {
	s := &p.cm
	return strings.Join([]string{p.sel, p.contactGene2(), s.bin, s.measure, s.orientation, s.typ, s.homodimer}, "\x00")
}

// capChoice is the colour cap for colorCap: an option, or "Custom:<value>".
func (s *contactState) capChoice() string {
	if s.colorCap == capCustom {
		return capCustom + ":" + strings.TrimSpace(s.customCap)
	}
	return s.colorCap
}

func homodimerPayload(option string) string {
	switch option {
	case homodimerOptions[1]:
		return "only"
	case homodimerOptions[2]:
		return "exclude"
	}
	return ""
}

func (p *explorePage) loadContact() {
	s := &p.cm
	path, ids, gene, gene2 := p.a.Project.Path, p.ids, p.gene, p.contactGene2()
	bin, measure, orientation, typ, homodimer := s.bin, s.measure, s.orientation, s.typ, s.homodimer
	colorCap, scale, dark := s.capChoice(), s.scale, s.dark
	lengthX, lengthY := p.geneLength(gene), p.geneLength(gene2)
	p.ensure(&s.load, p.contactKey(), func() (func(), error) {
		var size int
		fmt.Sscanf(bin, "%d", &size)
		payload := contactPayload(gene, gene2, measure, orientation, typ, homodimer)
		payload["bin_size"], payload["max_cells"] = size, contactMaxCells
		got, err := p.query(path, "analysis_contact_bins", ids, payload)
		if err != nil {
			return nil, err
		}
		d := decodeContact(got, measure, orientation, typ, lengthX, lengthY)
		d.path, d.ids = path, append([]string(nil), ids...)
		d.homodimer, d.records = homodimer, d.total
		if measure != measureRecords {
			// the hybrids in the map, whatever the cells weigh
			recs, err := p.contactCells(d, measureRecords)
			if err != nil {
				return nil, err
			}
			d.records = recs.total
		}
		cells := d.cells(colorCap, scale, dark)
		return func() {
			s.data = d
			s.heat.SetCells(cells, float64(d.lengthX), float64(d.lengthY))
			s.picked = -1
			if s.capChoice() != colorCap || s.scale != scale || s.dark != dark {
				p.recolor() // changed while loading
			}
			if f := s.focus; f != nil {
				s.focus = nil
				p.focusCell(f[0], f[1], f[2], f[3])
			}
		}, nil
	})
}

// contactPayload is the analysis_contact_bins request for a map's filters.
func contactPayload(gene, gene2, measure, orientation, typ, homodimer string) core.Obj {
	payload := core.Obj{"gene": gene, "gene2": gene2, "measure": measurePayload(measure), "orientation": "both"}
	if orientation == orientOriented {
		payload["orientation"] = "oriented"
	}
	if types := typesPayload(typ); types != nil {
		payload["types"] = types
	}
	if h := homodimerPayload(homodimer); h != "" {
		payload["homodimer"] = h
	}
	return payload
}

// contactMeasure is a map's non-empty cells under one measure.
type contactMeasure struct {
	total       int
	x, y, count []int
}

// contactCells is d's map under measure, on d's bins: d itself for its own
// measure, else queried again with the bin fixed. Safe off the main thread.
func (p *explorePage) contactCells(d *contactData, measure string) (contactMeasure, error) {
	if measure == d.measure {
		return contactMeasure{total: d.total, x: d.x, y: d.y, count: d.count}, nil
	}
	payload := contactPayload(d.gene, d.gene2, measure, d.orientation, d.typ, d.homodimer)
	payload["bin_size"], payload["fixed_bin"] = d.bin, true
	got, err := p.query(d.path, "analysis_contact_bins", d.ids, payload)
	if err != nil {
		return contactMeasure{}, err
	}
	m := contactMeasure{total: int(num(got["total"])), x: ints(got["x"]), y: ints(got["y"]), count: ints(got["count"])}
	n := min(len(m.x), len(m.y), len(m.count))
	m.x, m.y, m.count = m.x[:n], m.y[:n], m.count[:n]
	return m, nil
}

func decodeContact(got map[string]any, measure, orientation, typ string, lengthX, lengthY int) *contactData {
	d := &contactData{
		gene: str(got["gene"]), gene2: str(got["gene2"]),
		bin: int(num(got["bin_size"])), requested: int(num(got["requested_bin_size"])),
		adjusted: got["adjusted"] == true, total: int(num(got["total"])), max: int(num(got["max"])),
		measure: measure, orientation: orientation, typ: typ,
		x: ints(got["x"]), y: ints(got["y"]), count: ints(got["count"]),
	}
	if d.bin <= 0 {
		d.bin = 10
	}
	n := min(len(d.x), len(d.y), len(d.count))
	d.x, d.y, d.count = d.x[:n], d.y[:n], d.count[:n]
	d.lengthX = max(lengthX, int(num(got["length"])))
	d.lengthY = max(lengthY, int(num(got["length2"])))
	d.at = make(map[[2]int]int, n)
	for i := range n {
		d.at[[2]int{d.x[i], d.y[i]}] = i
		d.lengthX = max(d.lengthX, d.x[i]+d.bin)
		d.lengthY = max(d.lengthY, d.y[i]+d.bin)
	}
	d.sorted = append([]int(nil), d.count...)
	sort.Ints(d.sorted)
	return d
}

func str(v any) string { s, _ := v.(string); return s }

// measurePayload is the bridge's name for a measure label.
func measurePayload(measure string) string {
	switch measure {
	case measureSupport:
		return "support"
	case measureReads:
		return "reads"
	}
	return "records"
}

// display is a count as the colour scale sees it: log1p for the Log scale.
func display(n int, scale string) float64 {
	if scale == scaleLog {
		return math.Log1p(float64(n))
	}
	return float64(n)
}

// colorCap is the displayed value at which colours saturate: the chosen
// quantile of the displayed values, or a custom value, as the web's
// resolveContactCap.
func (d *contactData) colorCap(choice, scale string) float64 {
	if len(d.sorted) == 0 {
		return 0
	}
	if custom, ok := strings.CutPrefix(choice, capCustom+":"); ok {
		if v, err := strconv.ParseFloat(custom, 64); err == nil && v > 0 && !math.IsInf(v, 0) {
			return v
		}
		return display(d.sorted[len(d.sorted)-1], scale)
	}
	fraction := 0.95
	for i, label := range capOptions {
		if label == choice {
			fraction = capFractions[i]
		}
	}
	values := make([]float64, len(d.sorted))
	for i, n := range d.sorted {
		values[i] = display(n, scale)
	}
	return quantile(values, fraction)
}

// capCount is the cap as a count, for labels.
func capCount(limit float64, scale string) float64 {
	if scale == scaleLog {
		return math.Expm1(limit)
	}
	return limit
}

// cells colours every cell by its displayed value over the cap, on the
// web's contact ramp of the theme.
func (d *contactData) cells(choice, scale string, dark bool) []widgets.Cell {
	limit := d.colorCap(choice, scale)
	out := make([]widgets.Cell, len(d.count))
	for i, n := range d.count {
		ratio := 1.0
		if limit > 0 {
			ratio = display(n, scale) / limit
		}
		out[i] = widgets.Cell{X: float64(d.x[i]), Y: float64(d.y[i]), W: float64(d.bin), H: float64(d.bin), Color: contactColor(ratio, dark)}
	}
	return out
}

// cellRange is the inclusive gene range of a bin starting at x.
func cellRange(x, bin int) (int, int) { return max(1, x), x + bin - 1 }

func (p *explorePage) contactView(c *ui.Context) {
	s := &p.cm
	t := c.Theme()
	if s.dark != t.Dark {
		s.dark = t.Dark
		p.recolor()
	}
	p.loadContact()
	genes := p.genes()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(14).Children(func() {
			ui.Row(c).Gap(12).Wrap().AlignItems(ui.End).Children(func() {
				if len(genes) > 1 {
					picker(c, "cm-gene2", "Partner on Y", &s.gene2, genes, 200)
				}
				picker(c, "cm-bin", "Bin size", &s.bin, binOptions, 92)
				picker(c, "cm-measure", "Measure", &s.measure, measures, 100)
				picker(c, "cm-orientation", "Orientation", &s.orientation, orientations, 150)
				picker(c, "cm-type", "Chimera type", &s.typ, withAll(allTypes, p.summary.types), 120)
				picker(c, "cm-homodimer", "Homodimer", &s.homodimer, homodimerOptions, 200)
			})
			loadingLine(c, &s.load, "the contact map")
			d := s.data
			if d == nil {
				return
			}
			if d.adjusted {
				notice(c, fmt.Sprintf("%d nt · using %d nt, so the map stays within %s cells.",
					d.requested, d.bin, FormatNumber(contactMaxCells)))
			}
			if len(d.count) == 0 {
				ui.Text(c, "No hybrids between these genes with these settings.").TextColor(t.TextMuted)
				return
			}
			ui.Row(c).Gap(20).AlignItems(ui.Start).Wrap().Children(func() {
				ui.Column(c).Gap(6).Children(func() {
					ev := s.heat.Show(c, mapSizeTarget, "Contact map of "+d.gene)
					if ev.Clicked >= 0 {
						s.picked = ev.Clicked
					}
					s.hover = ""
					if ev.Hover {
						s.hover = d.readout(ev.X, ev.Y)
					}
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						hint := "Wheel zooms, drag pans, double-click resets. Click a cell for details."
						if s.hover != "" {
							hint = s.hover
						}
						ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine().MaxWidth(mapSizeTarget - 40)
						if s.heat.Zoomed() && ui.Button(c, "Reset zoom").Clicked() {
							s.heat.ResetZoom()
						}
					})
				})
				ui.Column(c).Gap(12).Width(300).Children(func() {
					p.contactLegend(c, d)
					p.contactDetails(c, d)
				})
			})
		})
	})
}

func (p *explorePage) recolor() {
	s := &p.cm
	if s.data == nil {
		return
	}
	view, picked := s.heat.View(), s.heat.Picked
	s.heat.SetCells(s.data.cells(s.capChoice(), s.scale, s.dark), float64(s.data.lengthX), float64(s.data.lengthY))
	s.heat.SetView(view)
	s.heat.Picked = picked
}

// readout describes the cell under the pointer.
func (d *contactData) readout(gx, gy float64) string {
	if gx < 0 || gy < 0 {
		return ""
	}
	x := int(gx) / d.bin * d.bin
	y := int(gy) / d.bin * d.bin
	n := 0
	if i, ok := d.at[[2]int{x, y}]; ok {
		n = d.count[i]
	}
	x0, x1 := cellRange(x, d.bin)
	y0, y1 := cellRange(y, d.bin)
	return fmt.Sprintf("Arm 1 %s · arm 2 %s · %s %s", span(x0, x1), span(y0, y1), FormatNumber(int64(n)), strings.ToLower(d.measure))
}

func (p *explorePage) contactLegend(c *ui.Context, d *contactData) {
	s := &p.cm
	t := c.Theme()
	Card(c, "", func() {
		ui.Row(c).Gap(16).Wrap().Children(func() {
			stat(c, "Bin", fmt.Sprintf("%d nt", d.bin))
			stat(c, "Cells", FormatNumber(int64(len(d.count))))
			stat(c, "Records in map", FormatNumber(int64(d.records)))
		})
		ui.Row(c).Gap(10).Wrap().Children(func() {
			picker(c, "cm-cap", "Colour cap", &s.colorCap, capOptions, 145).OnChange(p.recolor)
			if s.colorCap == capCustom {
				what := "Cap (count)"
				if s.scale == scaleLog {
					what = "Cap (log(1 + count))"
				}
				Field(c, what, func() {
					ui.TextInput(c.Key("cm-custom-cap"), &s.customCap).Label(what).Placeholder("maximum").Width(110).OnChange(p.recolor)
				})
			}
			picker(c, "cm-scale", "Scale", &s.scale, scaleOptions, 84).OnChange(p.recolor)
		})
		limit := capCount(d.colorCap(s.capChoice(), s.scale), s.scale)
		ui.Column(c).Gap(4).Children(func() {
			ui.Box(c).FillWidth().Height(10).Radius(3).Draw(func(pt *ui.Painter, r ui.Rect) {
				steps := 48
				for i := range steps {
					w := r.W / float32(steps)
					pt.Fill(ui.Rect{X: r.X + float32(i)*w, Y: r.Y, W: w + 0.5, H: r.H}, contactColor(float64(i)/float64(steps-1), t.Dark), 0)
				}
			})
			ui.Row(c).Children(func() {
				ui.Text(c, "1").FontSize(11).TextColor(t.TextMuted).Grow(1)
				ui.Text(c, fmt.Sprintf("≥ %s %s (max %s)", formatCount(limit), strings.ToLower(d.measure), FormatNumber(int64(d.max)))).
					FontSize(11).TextColor(t.TextMuted)
			})
		})
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.Button(c, "Export SVG…").Tooltip("Save the map as shown (cells in view)").Clicked() {
				p.exportContactSVG()
			}
			if ui.Button(c, "Export TSV…").Tooltip("Save the cells in view as a tab-separated file").Clicked() {
				p.exportContactTSV()
			}
		})
	})
}

func formatCount(v float64) string {
	if v == math.Trunc(v) {
		return FormatNumber(int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}

func (p *explorePage) contactDetails(c *ui.Context, d *contactData) {
	s := &p.cm
	t := c.Theme()
	Card(c, "Selected cell", func() {
		if s.picked < 0 || s.picked >= len(d.count) {
			ui.Text(c, "Click a cell of the map to see its hybrids or fold its region.").TextColor(t.TextMuted)
			return
		}
		x0, x1 := cellRange(d.x[s.picked], d.bin)
		y0, y1 := cellRange(d.y[s.picked], d.bin)
		genes := shortGene(d.gene)
		if d.gene2 != d.gene {
			genes += " × " + shortGene(d.gene2)
		}
		ui.Text(c, genes).FontSize(12).TextColor(t.TextMuted)
		for _, line := range [][2]string{{"Arm 1 (X)", span(x0, x1)}, {"Arm 2 (Y)", span(y0, y1)},
			{d.measure, FormatNumber(int64(d.count[s.picked]))}} {
			ui.Row(c.Key(line[0])).Gap(8).Children(func() {
				ui.Text(c, line[0]).TextColor(t.TextMuted).Width(80)
				ui.Text(c, line[1]).Bold()
			})
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.PrimaryButton(c, "Show hybrids").Clicked() {
				p.showCellHybrids(d, x0, x1, y0, y1)
			}
			if ui.Button(c, "Fold this region").Tooltip("Open Structure with a window around this cell").Clicked() {
				p.foldCell(d, s.picked)
			}
		})
		if d.orientation == orientBoth && d.gene != d.gene2 {
			ui.Text(c, "Show hybrids lists arm 1 on "+shortGene(d.gene)+"; the map also counts the reverse order.").
				FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// showCellHybrids switches to Interactions filtered to the cell's ranges.
func (p *explorePage) showCellHybrids(d *contactData, x0, x1, y0, y1 int) {
	f := ixFilters{gene1: d.gene, gene2: d.gene2, typ: d.typ, x0: x0, x1: x1, y0: y0, y1: y1, minReads: 1, minSupport: 1}
	switch homodimerPayload(d.homodimer) {
	case "only":
		f.scope = scopeLabels[3]
	case "exclude":
		f.scope = scopeLabels[4]
	}
	p.ix.apply(f)
	p.tab = tabInteractions
}

// foldCell opens Structure with each arm's bin padded by max(50, 2 bins)
// and clamped to the gene: one window spanning both arms on one gene, else
// arm 1's window (as the web page's fold request).
func (p *explorePage) foldCell(d *contactData, i int) {
	pad := max(50, 2*d.bin)
	clamp := func(start, end, length int) [2]int {
		start = max(1, start)
		if length > 0 {
			end = min(end, length)
		}
		return [2]int{start, end}
	}
	lengthX := p.geneLength(d.gene)
	if lengthX == 0 {
		lengthX = d.lengthX
	}
	lengthY := p.geneLength(d.gene2)
	if lengthY == 0 {
		lengthY = d.lengthY
	}
	first := clamp(d.x[i]-pad, d.x[i]+d.bin-1+pad, lengthX)
	second := clamp(d.y[i]-pad, d.y[i]+d.bin-1+pad, lengthY)
	window := first
	if d.gene == d.gene2 {
		window = [2]int{min(first[0], second[0]), max(first[1], second[1])}
	}
	p.foldPrefill(d.gene, [][2]int{window}, "contact map")
}

// focusCell picks the cell holding (x, y) and zooms to the range around it.
func (p *explorePage) focusCell(x, y, xEnd, yEnd int) {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	if i, ok := d.at[[2]int{x / d.bin * d.bin, y / d.bin * d.bin}]; ok {
		s.picked, s.heat.Picked = i, i
	}
	half := float64(max(10*d.bin, 200, xEnd-x, yEnd-y))
	cx, cy := float64(x+max(xEnd, x))/2, float64(y+max(yEnd, y))/2
	s.heat.SetView(widgets.Window{X0: cx - half, X1: cx + half, Y0: cy - half, Y1: cy + half})
}
