package views

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/widgets"
)

// Contact map: analysis_contact_bins drawn with widgets.ContactMap, laid out
// and worded as the web page's (web/analysis-pages.js renderContactMap):
// the Data controls, the map with its Display bar, the selected region, the
// data table and the method note.
//
// As the web's, the map draws one dataset whatever the selection merges
// (contactDatasetID); its exports, Show hybrids and Explore region use that
// dataset too.

const (
	// contactMaxCells is the web page's limit (web/page-data.js): the backend
	// doubles the chosen bin until the map has at most this many non-empty
	// cells (shown as adjusted).
	contactMaxCells = 250_000
	binDefault      = "10 nt"

	orientBoth     = "Normalised pairs"
	orientOriented = "Original arms (no swap)"
	// The contact map's measures: the web's two and the raw read counts in
	// the read ids (desktop only).
	cmRecords = "Record count"
	cmSupport = "Cluster support"
	cmReads   = "Read count"
	// The measures of the other Explore views (Viewpoint, Region).
	measureRecords = "Records"
	measureSupport = "Support"
	measureReads   = "Reads"
	scaleLinear    = "Linear"
	scaleLog       = "Log1p"
	// capCustom is the web's custom cap: a value on the displayed scale
	// (log(1 + count) on the Log1p scale), the maximum when not positive.
	capCustom  = "Custom"
	capMaximum = "Maximum"
	allPattern = "All chimera patterns"
	// contactTableRows is the web data table's length: the strongest cells.
	contactTableRows = 100
)

var (
	binOptions   = []string{"5 nt", "10 nt", "25 nt", "50 nt", "100 nt"}
	capOptions   = []string{"90th percentile", "95th percentile", "99th percentile", capMaximum, capCustom}
	capFractions = []float64{0.90, 0.95, 0.99, 1}
	// homodimerOptions are the Viewpoint's subsets of the homodimer proxy
	// (both arms on one RNA, overlap score >= 5), cmHomodimerOptions the
	// contact map's; both worded as the web's.
	homodimerOptions   = []string{"All records", "Overlap score ≥ 5", "Exclude homodimer proxy"}
	cmHomodimerOptions = []string{"All records", "Overlap score ≥ 5", "Exclude homodimer proxy"}
	cmMeasures         = []string{cmRecords, cmSupport, cmReads}
	measures           = []string{measureRecords, measureSupport, measureReads}
	orientations       = []string{orientBoth, orientOriented}
	scaleOptions       = []string{scaleLinear, scaleLog}
	paperWhite         = ui.RGB(255, 255, 255)
)

// contactPalettes are the colour styles' labels, in the web's order.
func contactPalettes() []string {
	out := make([]string, len(contactStyles))
	for i, s := range contactStyles {
		out[i] = s.label
	}
	return out
}

type contactState struct {
	// dataset is the sample chosen in the Dataset picker ("" = the
	// default, see contactDatasetID); stamp is the dataset drawn and its
	// index (contactStamp), so a change of the other samples keeps the map.
	dataset     string
	stamp       string
	gene2       string // partner gene on Y (the selected gene by default)
	bin         string
	measure     string
	orientation string
	typ         string
	homodimer   string
	colorCap    string
	// capChosen: the colour cap was chosen; until then it follows the
	// scale (95th percentile for Linear, the maximum for Log1p), as the
	// web's.
	capChosen bool
	customCap string
	scale     string
	palette   string // a label of contactStyles
	dark      bool   // the theme the cells were coloured for
	showTable bool

	load loader
	data *contactData
	cmap *widgets.ContactMap
	// hover describes the cell under the pointer ("" for none).
	hover string
	// focus, when set, is a gene range {x, x end, y, y end} to select once
	// the map loads, zoomed around it when zoom is set (from Compare).
	focus     *[4]int
	focusZoom bool
}

// contactData is one analysis_contact_bins answer, ready to draw, with the
// records and the cluster support of every cell whatever the measure.
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
	records, support []int // per cell
	sorted           []int // counts, ascending (for the colour cap)
	at               map[[2]int]int
	base             widgets.BinView
	// recordsTotal is the hybrids in the map (the web's "Records in map"),
	// supportTotal their cluster support.
	recordsTotal, supportTotal int
	// what the map was queried with (for the export's other measures)
	path string
	ids  []string
}

func (s *contactState) init() {
	s.bin, s.measure, s.orientation, s.typ, s.homodimer = binDefault, cmRecords, orientBoth, allPattern, cmHomodimerOptions[0]
	s.colorCap, s.capChosen, s.customCap, s.scale, s.palette = capOptions[1], false, "", scaleLinear, contactStyles[0].label
	s.cmap = widgets.NewContactMap()
}

// resetMap is the web's Reset map: the Data controls back to their defaults
// (the partner, the colour style and the display settings stay).
func (s *contactState) resetMap() {
	s.bin, s.measure, s.orientation, s.typ, s.homodimer = binDefault, cmRecords, orientBoth, allPattern, cmHomodimerOptions[0]
	s.cmap.Reset()
	s.load.reset()
}

func (s *contactState) selectionChanged(gene string, geneChanged bool) {
	if geneChanged || s.gene2 == "" {
		s.gene2 = gene
	}
	s.cmap.Reset()
	s.hover = ""
}

// pickedIndex is the selected cell's index in d, or -1 for none or an
// empty bin.
func (s *contactState) pickedIndex() int {
	d, sel := s.data, s.cmap.Selection
	if d == nil || sel == nil {
		return -1
	}
	if i, ok := d.at[*sel]; ok {
		return i
	}
	return -1
}

func (p *explorePage) contactGene2() string {
	if p.cm.gene2 == "" {
		return p.gene
	}
	return p.cm.gene2
}

func (p *explorePage) contactKey() string {
	s := &p.cm
	return strings.Join([]string{p.contactStamp(), p.contactGene2(), s.bin, s.measure, s.orientation, s.typ, s.homodimer}, "\x00")
}

// contactDatasets are the datasets the Dataset picker offers: every
// analysed sample of the project (the web's loaded datasets).
func (p *explorePage) contactDatasets() []string {
	if p.a.Project == nil {
		return nil
	}
	return p.a.SelectionIDs(AllSelection)
}

// contactDatasetID is the one sample the map draws, as the web's
// contactDataset: the chosen one, else the selection's when it is one
// sample, else the selection's first (the desktop has no primary file).
func (p *explorePage) contactDatasetID() string {
	if p.cm.dataset != "" && slicesIndex(p.contactDatasets(), p.cm.dataset) >= 0 {
		return p.cm.dataset
	}
	if len(p.ids) > 0 {
		return p.ids[0]
	}
	return ""
}

// contactStamp identifies the map's data: the project, the gene and the
// dataset with its index.
func (p *explorePage) contactStamp() string {
	a := p.a
	if a.Project == nil {
		return ""
	}
	id := p.contactDatasetID()
	updated := ""
	if s := a.Project.Sample(id); s != nil {
		updated = s.Stages["index"].UpdatedAt + s.Outputs.Index
	}
	return strings.Join([]string{a.Project.Path, a.Selected.Gene, id + "@" + updated}, "|")
}

// sampleLabels are the samples' names for a picker (a repeated name gets
// its id) and the sample of each.
func (p *explorePage) sampleLabels(ids []string) ([]string, map[string]string) {
	labels := make([]string, 0, len(ids))
	byLabel := map[string]string{}
	for _, id := range ids {
		label := id
		if s := p.a.Project.Sample(id); s != nil {
			label = s.Label()
		}
		if _, taken := byLabel[label]; taken {
			label += " (" + id + ")"
		}
		labels = append(labels, label)
		byLabel[label] = id
	}
	return labels, byLabel
}

// contactDatasetJSON is the web's parameters "dataset" of the map: its
// sample's id, label and .hyb file name (null for none).
func (p *explorePage) contactDatasetJSON(d *contactData) any {
	if len(d.ids) != 1 {
		return nil
	}
	s := p.a.Project.Sample(d.ids[0])
	if s == nil {
		return nil
	}
	file := ""
	if s.Outputs.Hyb != "" {
		file = filepath.Base(s.Outputs.Hyb)
	}
	return map[string]any{"id": s.ID, "label": s.Label(), "fileName": file}
}

// pinContactDataset makes the map's dataset the samples Explore shows (for
// a drill-down into Interactions or Region), unless the selection is it.
func (p *explorePage) pinContactDataset(d *contactData) {
	if len(d.ids) == 0 {
		return
	}
	p.pinSamples(d.ids, "the contact map")
	p.track()
}

// style is the colour style chosen.
func (s *contactState) style() contactStyle { return contactStyleOf(s.palette) }

// capChoice is the colour cap for colorCap: an option, or "Custom:<value>".
func (s *contactState) capChoice() string {
	if s.colorCap == capCustom {
		return capCustom + ":" + strings.TrimSpace(s.customCap)
	}
	return s.colorCap
}

// scaleChanged keeps the cap with the scale until one is chosen: most bins
// hold one or two records, so a percentile cap saturates the map at a few
// records and log1p barely changes it.
func (s *contactState) scaleChanged() {
	if s.capChosen {
		return
	}
	s.colorCap = capOptions[1]
	if s.scale == scaleLog {
		s.colorCap = capMaximum
	}
}

func homodimerPayload(option string) string {
	switch option {
	case homodimerOptions[1], cmHomodimerOptions[1]:
		return "only"
	case homodimerOptions[2], cmHomodimerOptions[2]:
		return "exclude"
	}
	return ""
}

func (p *explorePage) loadContact() {
	s := &p.cm
	id := p.contactDatasetID()
	if id == "" {
		return
	}
	s.stamp = p.contactStamp() // a dataset chosen here is the one drawn
	path, ids, gene, gene2 := p.a.Project.Path, []string{id}, p.gene, p.contactGene2()
	bin, measure, orientation, typ, homodimer := s.bin, s.measure, s.orientation, patternCode(s.typ, p.summary.types), s.homodimer
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
		d.path, d.ids, d.homodimer = path, append([]string(nil), ids...), homodimer
		// every cell's records and cluster support, whatever the measure
		records, err := p.contactCells(d, cmRecords)
		if err != nil {
			return nil, err
		}
		support, err := p.contactCells(d, cmSupport)
		if err != nil {
			return nil, err
		}
		d.setCounts(records, support)
		return func() {
			s.data = d
			p.recolor()
			s.cmap.Reset()
			if f := s.focus; f != nil {
				s.focus = nil
				p.focusCell(f[0], f[1], f[2], f[3], s.focusZoom)
			}
		}, nil
	})
}

// setCounts fills each cell's records and cluster support.
func (d *contactData) setCounts(records, support contactMeasure) {
	d.records, d.support = make([]int, len(d.x)), make([]int, len(d.x))
	for i := range records.x {
		if k, ok := d.at[[2]int{records.x[i], records.y[i]}]; ok {
			d.records[k] = records.count[i]
		}
	}
	for i := range support.x {
		if k, ok := d.at[[2]int{support.x[i], support.y[i]}]; ok {
			d.support[k] = support.count[i]
		}
	}
	d.recordsTotal, d.supportTotal = records.total, support.total
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
		if i == 0 {
			d.base = widgets.BinView{XStart: d.x[0], XEnd: d.x[0], YStart: d.y[0], YEnd: d.y[0]}
		}
		d.base.XStart, d.base.XEnd = min(d.base.XStart, d.x[i]), max(d.base.XEnd, d.x[i])
		d.base.YStart, d.base.YEnd = min(d.base.YStart, d.y[i]), max(d.base.YEnd, d.y[i])
	}
	d.sorted = append([]int(nil), d.count...)
	sort.Ints(d.sorted)
	return d
}

func str(v any) string { s, _ := v.(string); return s }

// measurePayload is the bridge's name for a measure label.
func measurePayload(measure string) string {
	switch measure {
	case measureSupport, cmSupport:
		return "support"
	case measureReads, cmReads:
		return "reads"
	}
	return "records"
}

// unit is the measure's unit in a sentence ("records", "cluster support").
func (d *contactData) unit() string {
	switch d.measure {
	case cmSupport:
		return "cluster support"
	case cmReads:
		return "reads"
	}
	return "records"
}

// display is a count as the colour scale sees it: log1p for the Log1p scale.
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

// cells colours every cell by its displayed value over the cap, in a colour
// style (the theme's for the theme-following one).
func (d *contactData) cells(choice, scale string, dark bool, style string) []widgets.Cell {
	limit := d.colorCap(choice, scale)
	out := make([]widgets.Cell, len(d.count))
	for i, n := range d.count {
		ratio := 1.0
		if limit > 0 {
			ratio = display(n, scale) / limit
		}
		out[i] = widgets.Cell{X: float64(d.x[i]), Y: float64(d.y[i]), W: float64(d.bin), H: float64(d.bin), Color: contactStyleColor(ratio, dark, style)}
	}
	return out
}

// legend is the colour bar over the plot (contactLegendOptions): 0 to the
// cap in the measure's units; on Log1p linear in log1p(value) with ticks
// at 1, 2, 5 x 10^n labelled by count; a cap below the maximum is "≥".
func (d *contactData) legend(choice, scale string, dark bool, style string, plotWidth float32) widgets.Legend {
	limit := d.colorCap(choice, scale)
	log := scale == scaleLog
	clipped := display(d.max, scale) > limit*(1+1e-9)
	raw := func(v float64) float64 {
		if log {
			return math.Expm1(v)
		}
		return v
	}
	label := func(v float64) string {
		prefix := ""
		if clipped && math.Abs(v-limit) < 1e-9 {
			prefix = "≥ "
		}
		return prefix + widgets.FormatTick(raw(v))
	}
	width := float64(widgets.LegendWidth(plotWidth))
	var ticks []float64
	if log && limit > 0 {
		top := math.Expm1(limit)
		ticks = []float64{0}
		for magnitude := 1.0; magnitude < top; magnitude *= 10 {
			for _, multiple := range []float64{1, 2, 5} {
				position := math.Log1p(multiple*magnitude) / limit
				if multiple*magnitude < top && position-ticks[len(ticks)-1]/limit >= 0.16 && 1-position >= 0.12 {
					ticks = append(ticks, math.Log1p(multiple*magnitude))
				}
			}
		}
		ticks = append(ticks, limit)
	} else {
		ticks = widgets.SpacedTicks(widgets.NiceTicks(0, limit, 4), 0, limit, width, label)
	}
	title := map[string]string{cmRecords: "Record count", cmSupport: "Cluster support", cmReads: "Read count"}[d.measure]
	how := "linear"
	if log {
		how = "log1p"
	}
	return widgets.Legend{Title: title + " (" + how + ")", Max: limit, Ticks: ticks, Format: label,
		ColorAt: func(t float64) ui.Color { return contactStyleColor(t, dark, style) }}
}

// cellRange is the inclusive gene range of a bin starting at x.
func cellRange(x, bin int) (int, int) { return max(1, x), x + bin - 1 }

// contactSide is the map's side: square, as wide as the page allows next
// to the side panel and no taller than the window under the controls (the
// web's min(100%, max(420px, 100vh - header - 190px))).
func (p *explorePage) contactSide() float32 {
	w, h := p.a.ContentSize()
	if w <= 0 || h <= 0 {
		return 560
	}
	// the side panel (300), the card's padding (32), the gap (16) and the
	// page's padding
	return clampF(min(w-420, h-190), 420, 1400)
}

func (p *explorePage) contactView(c *ui.Context) {
	s := &p.cm
	t := c.Theme()
	if s.dark != t.Dark {
		s.dark = t.Dark
		p.recolor()
	}
	p.loadContact()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(14).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Fixed-width binning mirrors the current HYB2 coverage accumulation rule.").TextColor(t.TextMuted).Grow(1)
				p.contactExportMenu(c)
			})
			p.contactControls(c)
			loadingLine(c, &s.load, "the contact map")
			d := s.data
			if d == nil {
				return
			}
			if d.adjusted {
				notice(c, fmt.Sprintf("Requested %s nt bins; the map uses %s nt so it stays within the cell budget.",
					FormatNumber(int64(d.requested)), FormatNumber(int64(d.bin))))
			}
			if len(d.count) == 0 {
				ui.Text(c, "No records between these RNAs with these settings.").TextColor(t.TextMuted)
				return
			}
			ui.Row(c).Gap(16).AlignItems(ui.Start).Wrap().Children(func() {
				p.contactStage(c, d)
				ui.Column(c).Gap(12).Width(300).Children(func() {
					p.contactSelection(c, d)
					p.contactInfoCard(c, d)
				})
			})
			if s.showTable {
				Card(c, "", func() { contactTable(c, d) })
			}
			ui.Row(c).Gap(8).Padding(10, 12).Radius(8).Background(t.Surface).Children(func() {
				ui.Text(c, "Method").Bold().FontSize(12)
				ui.Text(c, "Each RNA arm covers every fixed-width bin between its start and end coordinates. The chosen measure is added to each bin pair: one per row for record count, or legacy cluster support when count metadata exists. Standard HYB2 overlap scores are preserved as annotations; overlap ≥ 5 is available separately as the original homodimer proxy.").
					FontSize(12).TextColor(t.TextMuted).Grow(1)
			})
		})
	})
}

// contactControls is the Data card: what the map counts.
func (p *explorePage) contactControls(c *ui.Context) {
	s := &p.cm
	t := c.Theme()
	genes := p.genes()
	Card(c, "Data", func() {
		ui.Row(c).Gap(12).Wrap().AlignItems(ui.End).Children(func() {
			if datasets := p.contactDatasets(); len(datasets) > 1 {
				labels, byLabel := p.sampleLabels(datasets)
				chosen := ""
				for _, label := range labels {
					if byLabel[label] == p.contactDatasetID() {
						chosen = label
					}
				}
				picker(c, "cm-dataset", "Dataset", &chosen, labels, 200).OnChange(func() { s.dataset = byLabel[chosen] })
			}
			if len(genes) > 0 {
				gene := p.gene
				picker(c, "cm-gene1", "RNA X", &gene, genes, 200).OnChange(func() { p.selectGene(gene) })
				picker(c, "cm-gene2", "RNA Y", &s.gene2, genes, 200)
			}
			bins := binOptions
			if d := s.data; d != nil && d.adjusted && !s.load.busy() {
				bins = make([]string, len(binOptions))
				for i, option := range binOptions {
					bins[i] = option
					if option == s.bin {
						bins[i] = fmt.Sprintf("%s · using %d nt", option, d.bin)
					}
				}
			}
			bin := s.bin
			if i := slicesIndex(binOptions, s.bin); i >= 0 {
				bin = bins[i]
			}
			picker(c, "cm-bin", "Bin size", &bin, bins, 150).OnChange(func() {
				if i := slicesIndex(bins, bin); i >= 0 {
					s.bin = binOptions[i]
				}
			})
			picker(c, "cm-measure", "Measure", &s.measure, cmMeasures, 150)
			picker(c, "cm-orientation", "Orientation", &s.orientation, orientations, 190)
			patterns := make([]string, len(p.summary.types))
			for i, code := range p.summary.types {
				patterns[i] = patternName(code)
			}
			picker(c, "cm-type", "Chimera pattern", &s.typ, withAll(allPattern, patterns), 230)
			picker(c, "cm-homodimer", "Homodimer subset", &s.homodimer, cmHomodimerOptions, 200)
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "Reset map").Clicked() {
				s.resetMap()
			}
			ui.Text(c, "Drag across the map to zoom to a region, or click a cell to zoom one step. Double-click the map, or Reset zoom above it, to return to the full extent.").
				FontSize(12).TextColor(t.TextMuted).Grow(1)
		})
		if len(p.ids) > 1 {
			ui.Text(c, "Contact Map draws one dataset of its choice.").FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// contactStage is the map card: its extent, the Display bar, the map and
// the hover line.
func (p *explorePage) contactStage(c *ui.Context, d *contactData) {
	s := &p.cm
	t := c.Theme()
	side := p.contactSide()
	Card(c, "", func() {
		ui.Row(c).Gap(12).AlignItems(ui.Start).Width(side).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "CONTACT MAP").FontSize(11).Bold().TextColor(t.TextMuted)
				heading := "Full extent"
				if s.cmap.Zoomed() {
					heading = "Zoomed extent"
				}
				ui.Text(c, heading).FontSize(18).Bold()
			})
			ui.Column(c).Gap(4).AlignItems(ui.End).Children(func() {
				rangeText := "Waiting for selection"
				if sel := s.cmap.Selection; sel != nil {
					rangeText = d.cellName(sel[0], sel[1])
				}
				ui.Text(c, rangeText).FontSize(12).TextColor(t.TextMuted)
				// the buttons beside the map leave the keyboard focus on it
				if ui.Button(c, "Reset zoom").KeepFocus().Clicked() {
					s.cmap.Reset()
				}
			})
		})
		p.contactDisplay(c)
		ui.Text(c, "Drag to select a region. Click a cell to zoom and inspect it.").FontSize(12).TextColor(t.TextMuted)
		ev := s.cmap.Show(c, side, side, "Contact map of "+d.gene+" × "+d.gene2+". Drag to select a region. Click a cell to zoom and inspect it.")
		s.hover = ""
		if ev.Hover {
			s.hover = d.readout(ev.X, ev.Y)
		}
		hint := "Hover a cell for coordinates and counts."
		if s.hover != "" {
			hint = s.hover
		}
		ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted).SingleLine().MaxWidth(side)
		if s.load.busy() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Label("Updating map")
				ui.Text(c, "Updating map…").FontSize(12).TextColor(t.TextMuted)
			})
		}
	}).Width(side + 32)
}

// contactDisplay is the Display bar: how the map is drawn. Changes apply at
// once, keep the zoom and the selected cell, and recolour the bins without
// querying them again.
func (p *explorePage) contactDisplay(c *ui.Context) {
	s := &p.cm
	t := c.Theme()
	ui.Column(c).Gap(6).Padding(10).Radius(8).Background(t.Surface).Children(func() {
		ui.Text(c, "Display").FontSize(12).Bold()
		ui.Row(c).Gap(10).Wrap().AlignItems(ui.End).Children(func() {
			// a choice here gives the keyboard focus back to the map, so the
			// arrow keys move the selection again
			picker(c, "cm-palette", "Colour style", &s.palette, contactPalettes(), 220).OnChange(func() {
				p.recolor()
				s.cmap.RequestFocus()
			})
			picker(c, "cm-scale", "Scale", &s.scale, scaleOptions, 100).OnChange(func() {
				s.scaleChanged()
				p.recolor()
				s.cmap.RequestFocus()
			})
			picker(c, "cm-cap", "Colour cap", &s.colorCap, capOptions, 150).OnChange(func() {
				s.capChosen = true
				p.recolor()
				if s.colorCap != capCustom { // the custom value is typed next
					s.cmap.RequestFocus()
				}
			})
			if s.colorCap == capCustom {
				Field(c, "Custom cap", func() {
					ui.TextInput(c.Key("cm-custom-cap"), &s.customCap).Label("Custom cap").Placeholder("Value").Width(100).OnChange(p.recolor)
				})
			}
		})
		ui.Text(c, "Applies at once and keeps the zoom; exports use these settings.").FontSize(11).TextColor(t.TextMuted)
	})
}

// recolor draws the cells, the colour bar and the axes for the display
// settings, keeping the view and the selection.
func (p *explorePage) recolor() {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	style := s.style().value
	s.cmap.SetCells(d.cells(s.capChoice(), s.scale, s.dark, style), d.bin, d.base)
	s.cmap.Colors = contactPaper(style)
	side := p.contactSide()
	g := s.cmap.Geometry(side, side)
	s.cmap.Legend = d.legend(s.capChoice(), s.scale, s.dark, style, g.PlotWidth)
	s.cmap.XTitle, s.cmap.YTitle = d.gene+" position (nt)", d.gene2+" position (nt)"
}

// cellName is "RNA X:a–b × RNA Y:c–d" for the bin at (x, y).
func (d *contactData) cellName(x, y int) string {
	return d.gene + ":" + span(x, x+d.bin-1) + " × " + d.gene2 + ":" + span(y, y+d.bin-1)
}

// readout describes the bin under the pointer (updateContactStageTooltip).
func (d *contactData) readout(x, y int) string {
	prefix := d.cellName(x, y)
	i, ok := d.at[[2]int{x, y}]
	if !ok {
		return prefix + " · no records"
	}
	text := prefix + " · " + FormatNumber(int64(d.records[i])) + " records · " + FormatNumber(int64(d.support[i])) + " cluster support"
	if d.measure == cmReads {
		text += " · " + FormatNumber(int64(d.count[i])) + " reads"
	}
	return text
}

// contactSelection is the Selected region card.
func (p *explorePage) contactSelection(c *ui.Context, d *contactData) {
	s := &p.cm
	t := c.Theme()
	Card(c, "", func() {
		ui.Text(c, "SELECTED REGION").FontSize(11).Bold().TextColor(t.TextMuted)
		sel := s.cmap.Selection
		if sel == nil {
			ui.Text(c, "Select a heatmap cell").FontSize(16).Bold()
			ui.Text(c, "Hover to inspect a bin. Click a cell to load its local record summary and continue into the Region Explorer.").
				FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(16).Wrap().Children(func() {
				stat(c, "Records in map", FormatNumber(int64(d.recordsTotal)))
				stat(c, "Cluster support", FormatNumber(int64(d.supportTotal)))
			})
			return
		}
		ui.Text(c, d.gene+":"+span(sel[0], sel[0]+d.bin-1)).FontSize(15).Bold()
		ui.Text(c, d.gene2+":"+span(sel[1], sel[1]+d.bin-1)).FontSize(15).Bold()
		records, support, reads := 0, 0, 0
		if i, ok := d.at[*sel]; ok {
			records, support, reads = d.records[i], d.support[i], d.count[i]
		}
		ui.Row(c).Gap(16).Wrap().Children(func() {
			stat(c, "Records", FormatNumber(int64(records)))
			stat(c, "Cluster support", FormatNumber(int64(support)))
			if d.measure == cmReads {
				stat(c, "Reads", FormatNumber(int64(reads)))
			}
		})
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.PrimaryButton(c, "Explore region").Clicked() {
				p.exploreCellRegion(d, *sel)
			}
			if ui.Button(c, "Fold this region").Tooltip("Open Structure with a window around this cell").Clicked() {
				p.foldCell(d, *sel)
			}
			if ui.Button(c, "Export records").KeepFocus().Clicked() {
				p.exportContactCellRecords()
			}
		})
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.Button(c, "Show hybrids").Tooltip("Interactions filtered to this cell").Clicked() {
				x0, x1 := cellRange(sel[0], d.bin)
				y0, y1 := cellRange(sel[1], d.bin)
				p.showCellHybrids(d, x0, x1, y0, y1)
			}
			if ui.Button(c, "Fold both arms (CPLcofold)").Tooltip("Open Structure with the two arms' ranges as two strands").Clicked() {
				p.cofoldCell(d, *sel)
			}
		})
	})
}

// contactInfoCard says how the map is drawn and opens the data table.
func (p *explorePage) contactInfoCard(c *ui.Context, d *contactData) {
	s := &p.cm
	t := c.Theme()
	Card(c, "", func() {
		how := "linear display"
		if s.scale == scaleLog {
			how = "log1p display"
		}
		ui.Text(c, strings.ToUpper(s.style().kicker+" · "+how)).FontSize(11).Bold().TextColor(t.TextMuted)
		ui.Text(c, "Image exports use the currently visible extent.").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(10).Wrap().Children(func() {
			ui.Text(c, d.gene+" · X axis").FontSize(12)
			ui.Text(c, d.gene2+" · Y axis").FontSize(12)
		})
		label := "Open data table"
		if s.showTable {
			label = "Hide data table"
		}
		if ui.Button(c, label).KeepFocus().Clicked() {
			s.showTable = !s.showTable
		}
	})
}

// contactTable is the web's data table: the strongest cells by the measure.
func contactTable(c *ui.Context, d *contactData) {
	t := c.Theme()
	order := make([]int, len(d.count))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return d.count[order[i]] > d.count[order[j]] })
	if len(order) > contactTableRows {
		order = order[:contactTableRows]
	}
	columns := []string{"X position (nt)", "Y position (nt)", "Records", "Cluster support"}
	widths := []float32{160, 160, 100, 120}
	ui.Column(c).Gap(0).Children(func() {
		ui.Row(c).Gap(0).Padding(6, 8).Children(func() {
			for i, name := range columns {
				ui.Text(c, name).FontSize(12).Bold().TextColor(t.TextMuted).Width(widths[i]).TextAlign(ui.End)
			}
		})
		for _, i := range order {
			row := []string{span(d.x[i], d.x[i]+d.bin-1), span(d.y[i], d.y[i]+d.bin-1),
				FormatNumber(int64(d.records[i])), FormatNumber(int64(d.support[i]))}
			ui.Row(c.Key(fmt.Sprint(i))).Gap(0).Padding(4, 8).Border(0, t.Border).Children(func() {
				for k, v := range row {
					ui.Text(c, v).FontSize(12).Width(widths[k]).TextAlign(ui.End)
				}
			})
		}
	})
}

// showCellHybrids switches to Interactions filtered to the cell's ranges,
// in both arm orders when the map counts them (normalised pairs of two
// RNAs, as contactCellFilters), so the list matches the cell's count.
func (p *explorePage) showCellHybrids(d *contactData, x0, x1, y0, y1 int) {
	p.pinContactDataset(d)
	typ := d.typ
	f := ixFilters{gene1: d.gene, gene2: d.gene2, typ: typ, x0: x0, x1: x1, y0: y0, y1: y1, minReads: 1, minSupport: 1,
		exactArms: true, bothOrders: d.orientation == orientBoth && d.gene != d.gene2}
	switch homodimerPayload(d.homodimer) {
	case "only":
		f.scope = scopeLabels[3]
	case "exclude":
		f.scope = scopeLabels[4]
	}
	p.ix.apply(f)
	p.tab = tabInteractions
}

// exploreCellRegion opens Region on the cell's X range with its Y range as
// the partner (the web's Explore region), on the map's dataset.
func (p *explorePage) exploreCellRegion(d *contactData, sel [2]int) {
	p.pinContactDataset(d)
	s := &p.rg
	x0, x1 := cellRange(sel[0], d.bin)
	y0, y1 := cellRange(sel[1], d.bin)
	s.applyRange(x0, x1)
	s.setPartner(d.gene2, y0, y1)
	s.load.reset()
	p.tab = tabRegion
}

// foldCell opens Structure with each arm's bin padded by max(50, 2 bins)
// and clamped to the gene: one window spanning both arms on one gene, else
// arm 1's window (as the web page's fold request).
func (p *explorePage) foldCell(d *contactData, sel [2]int) {
	first, second := p.cellArms(d, sel)
	window := first
	if d.gene == d.gene2 {
		window = [2]int{min(first[0], second[0]), max(first[1], second[1])}
	}
	p.foldPrefill(d.gene, [][2]int{window}, "contact map")
}

// cellArms are the cell's two arm ranges, each padded by max(50, 2 bins)
// and clamped to its gene.
func (p *explorePage) cellArms(d *contactData, sel [2]int) (first, second [2]int) {
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
	return clamp(sel[0]-pad, sel[0]+d.bin-1+pad, lengthX), clamp(sel[1]-pad, sel[1]+d.bin-1+pad, lengthY)
}

// cofoldCell opens ⑤ Structure with the cell's two arm ranges (cellArms)
// as the two strands of a CPLcofold fold (the web's "Send both regions to
// CPLcofold"). Ranges that overlap are folded as two copies.
func (p *explorePage) cofoldCell(d *contactData, sel [2]int) {
	first, second := p.cellArms(d, sel)
	p.toStructure(map[string]any{
		"regions": []core.Region{{GeneID: d.gene, Start: uint64(first[0]), End: uint64(first[1])},
			{GeneID: d.gene2, Start: uint64(second[0]), End: uint64(second[1])}},
		"source": "the contact map",
	})
}

// focusCell selects the bin holding (x, y); with zoom, it shows the range
// around it (at least 20 bins or 400 nt).
func (p *explorePage) focusCell(x, y, xEnd, yEnd int, zoom bool) {
	s := &p.cm
	d := s.data
	if d == nil {
		return
	}
	sel := [2]int{x / d.bin * d.bin, y / d.bin * d.bin}
	s.cmap.Selection = &sel
	if zoom {
		span := max(20, 400/d.bin, 2*(xEnd-x)/d.bin+2, 2*(yEnd-y)/d.bin+2)
		s.cmap.Focus(sel[0], sel[1], span)
	}
}
