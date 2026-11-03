package views

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// ③ Explore: the selected samples' hybrids as an interactions table, a
// contact map, a viewpoint (coverage along the gene) and a region view.
// Everything is computed by the backend's analysis queries over the
// samples' indexes (replicates merged by summing); this page only shows it.
//
// Queries: analysis_summary (genes and chimera types for the pickers),
// analysis_contact_bins, analysis_hybrids, analysis_viewpoint and
// analysis_region, each with sample_ids from the top bar's selection.
//
// Navigate("explore", {"tab": "interactions" | "contact" | "viewpoint" |
// "region" | "energies"}) opens a sub-page; "energies" is the HYB binding
// energies batch tool (batch_energy.go).
//
// "Fold this region" goes to ⑤ Structure with
//
//	{"prefill": {"gene": string, "windows": []map[string]int{{"start": s, "end": e}},
//	             "selectionKey": "all" | "c:<condition>" | "s:<sample>", "source": "contact map" | "region",
//	             "sampleIds": []string (only while Explore shows pinned samples, see pinned)}}
func init() {
	RegisterPageFactory("explore", func(a *App) Page { return newExplorePage(a) })
}

const (
	tabInteractions = iota
	tabContact
	tabViewpoint
	tabRegion
	tabEnergies
)

var exploreTabs = []string{"Interactions", "Contact map", "Viewpoint", "Region", "Binding energies"}
var exploreTabIDs = []string{"interactions", "contact", "viewpoint", "region", "energies"}

const (
	allTypes    = "All types"
	allPartners = "All partners"
	anyGene     = "Any gene"
)

type explorePage struct {
	a   *App
	tab int

	// sel identifies what the data shown comes from (project, samples and
	// their index stamps, gene); a change resets the sub-pages' choices
	// that depend on it. Each sub-page's loader key includes it.
	sel  string
	ids  []string
	gene string
	// pinned are the samples Compare asked for ("sampleIds"), or the
	// contact map's dataset for its drill-downs, when they are not what the
	// top bar's selection (pinnedKey) holds, e.g. a tested group that is not
	// one condition; they are used while that selection stays chosen.
	// pinnedFrom says where they came from ("Compare", "the contact map").
	pinned     []string
	pinnedKey  string
	pinnedFrom string

	summary exploreSummary
	sumLoad loader

	ix interactionsState
	cm contactState
	vp viewpointState
	rg regionState
	be batchEnergyState // batch_energy.go
}

// exploreSummary is what the pickers need from analysis_summary.
type exploreSummary struct {
	genes   []string
	types   []string
	records int64
	reads   int64
	// extents: the observed arm range of each gene {smallest start, largest end}
	extents map[string][2]int
}

func newExplorePage(a *App) *explorePage {
	p := &explorePage{a: a, tab: tabContact}
	p.cm.init()
	p.ix.init()
	p.vp.init()
	p.rg.init()
	return p
}

// Show applies navigation options; data loads as the view needs it.
//
// Options: {"tab": "interactions" | "contact" | "viewpoint" | "region" |
// "energies"},
// or Compare's {"view": "contact-map" | "region", "binSize": n, "x", "y",
// "xEnd", "yEnd", "gene2"} (the caller sets a.Selected first): the contact
// map picks and zooms to the cell (x, y); the region opens arm 1 x..xEnd
// against arm 2 y..yEnd, on gene2 for an inter-RNA result, else on the same
// gene (without "yEnd", any partner range is cleared). Compare's
// "sampleIds" are the tested group's samples: when the selection does not
// hold exactly those, Explore queries them instead (pinSamples).
func (p *explorePage) Show() {
	opts := p.a.NavOptions
	if opts == nil {
		return
	}
	p.a.NavOptions = nil
	if raw, ok := opts["sampleIds"]; ok {
		p.pinSamples(sampleIDList(raw), "Compare")
	}
	p.track()
	tab, _ := opts["tab"].(string)
	switch view, _ := opts["view"].(string); view {
	case "contact-map":
		tab = "contact"
	case "region":
		tab = "region"
	}
	for i, id := range exploreTabIDs {
		if id == tab {
			p.tab = i
		}
	}
	_, hasX := opts["x"]
	x, y := int(num(opts["x"])), int(num(opts["y"]))
	xEnd, yEnd := int(num(opts["xEnd"])), int(num(opts["yEnd"]))
	if bin := int(num(opts["binSize"])); bin > 0 && xEnd == 0 {
		xEnd, yEnd = x+bin-1, y+bin-1
	}
	if !hasX {
		return
	}
	gene2, _ := opts["gene2"].(string) // y is on RNA Y
	if gene2 == "" {
		gene2 = p.gene
	}
	switch p.tab {
	case tabContact:
		if bin := int(num(opts["binSize"])); bin > 0 {
			p.cm.bin = binDefault
			for _, option := range binOptions {
				if option == fmt.Sprintf("%d nt", bin) {
					p.cm.bin = option
				}
			}
		}
		p.cm.gene2 = gene2
		p.cm.focus, p.cm.focusZoom = &[4]int{x, y, xEnd, yEnd}, true
		p.cm.load.reset()
	case tabRegion:
		s := &p.rg
		s.applyRange(max(1, x), max(xEnd, x))
		if yEnd > 0 {
			s.setPartner(gene2, max(1, y), yEnd)
		} else {
			// no partner range: none is left from before (an inter-RNA
			// result keeps its RNA Y as the partner)
			partner, _ := opts["gene2"].(string)
			s.setPartner(partner, 0, 0)
		}
	}
}

// ---- loading

// loader tracks one query: the key of the data shown and of the one in
// flight. A result whose key is no longer the one wanted is dropped, so a
// slow answer for an old selection never replaces a newer one.
type loader struct {
	shown   string
	pending string
	err     string
}

func (l *loader) busy() bool { return l.pending != "" }

// reset makes the next ensure load again.
func (l *loader) reset() { l.shown, l.pending, l.err = "", "", "" }

// ensure starts work for key unless it is shown or in flight. work runs off
// the main thread and returns the function applying its result.
func (p *explorePage) ensure(l *loader, key string, work func() (func(), error)) {
	if key == l.pending || (l.pending == "" && key == l.shown) {
		return
	}
	l.pending = key
	path := p.a.Project.Path
	p.a.Go(func() func() {
		apply, err := work()
		return func() {
			if l.pending != key || p.a.Project == nil || p.a.Project.Path != path {
				return // superseded
			}
			l.pending, l.shown = "", key
			if err != nil {
				l.err = err.Error()
				return
			}
			l.err = ""
			if apply != nil {
				apply()
			}
		}
	})
}

// query runs an analysis query for the current samples (call it from work).
func (p *explorePage) query(path, operation string, ids []string, payload core.Obj) (core.Obj, error) {
	payload["sample_ids"] = append([]string(nil), ids...)
	raw, err := p.a.B.Query(path, operation, payload)
	if err != nil {
		return nil, err
	}
	out, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s returned no result", operation)
	}
	return out, nil
}

// track follows the top bar: a new selection, gene or rebuilt index resets
// what depends on it.
func (p *explorePage) track() {
	a := p.a
	ids := a.SelectionIDs(a.Selected.Key)
	if p.pinned != nil {
		if a.Selected.Key == p.pinnedKey {
			ids = p.pinned
		} else {
			p.pinned = nil // the selection changed in the top bar
		}
	}
	stamp := []string{a.Project.Path, a.Selected.Gene}
	for _, id := range ids {
		updated := ""
		if s := a.Project.Sample(id); s != nil {
			updated = s.Stages["index"].UpdatedAt + s.Outputs.Index
		}
		stamp = append(stamp, id+"@"+updated)
	}
	sel := strings.Join(stamp, "|")
	if sel == p.sel {
		return
	}
	geneChanged := a.Selected.Gene != p.gene
	p.sel, p.ids, p.gene = sel, ids, a.Selected.Gene
	// the contact map draws one dataset: a change of the others keeps it
	if stamp := p.contactStamp(); geneChanged || stamp != p.cm.stamp {
		p.cm.stamp = stamp
		p.cm.selectionChanged(p.gene, geneChanged)
	}
	p.ix.selectionChanged()
	p.vp.selectionChanged(geneChanged)
	p.rg.selectionChanged(geneChanged, p.geneLength(p.gene))
}

// pinSamples uses ids (their analysed samples, from source) instead of
// the selection's when the selection does not hold exactly them; selection
// keys are every sample ("all"), a condition or one sample, so a group of
// samples cannot be one.
func (p *explorePage) pinSamples(ids []string, source string) {
	a := p.a
	var ready []string
	for _, id := range ids {
		if s := a.Project.Sample(id); s != nil && s.IsReady() && !slices.Contains(ready, id) {
			ready = append(ready, id)
		}
	}
	selected := a.SelectionIDs(a.Selected.Key)
	if len(ready) == 0 || (len(ready) == len(selected) && cmpSameSet(ready, selected)) {
		p.pinned, p.pinnedKey, p.pinnedFrom = nil, "", ""
		return
	}
	p.pinned, p.pinnedKey, p.pinnedFrom = ready, a.Selected.Key, source
}

// sampleIDList reads a "sampleIds" option ([]string, or []any from JSON).
func sampleIDList(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// samplesLabel names the samples queried: the selection's label, or the
// pinned samples' names.
func (p *explorePage) samplesLabel() string {
	if p.pinned == nil {
		return p.a.SelectionLabel(p.a.Selected.Key)
	}
	names := make([]string, len(p.pinned))
	for i, id := range p.pinned {
		names[i] = id
		if s := p.a.Project.Sample(id); s != nil {
			names[i] = s.Label()
		}
	}
	return strings.Join(names, ", ")
}

func (p *explorePage) loadSummary() {
	path, ids, key := p.a.Project.Path, p.ids, p.sel
	p.ensure(&p.sumLoad, key, func() (func(), error) {
		got, err := p.query(path, "analysis_summary", ids, core.Obj{"pairs": false})
		if err != nil {
			return nil, err
		}
		merged, _ := got["merged"].(map[string]any)
		var s exploreSummary
		var genes []struct {
			Name string `json:"name"`
		}
		var types []struct {
			Name string `json:"name"`
		}
		_ = model.Decode(merged["genes"], &genes)
		_ = model.Decode(merged["types"], &types)
		for _, g := range genes {
			s.genes = append(s.genes, g.Name)
		}
		for _, t := range types {
			s.types = append(s.types, t.Name)
		}
		s.records, s.reads = int64(num(merged["records"])), int64(num(merged["reads"]))
		s.extents = map[string][2]int{}
		if extents, ok := merged["extents"].(map[string]any); ok {
			for name, v := range extents {
				if e, ok := v.(map[string]any); ok && e["min"] != nil {
					s.extents[name] = [2]int{int(num(e["min"])), int(num(e["max"]))}
				}
			}
		}
		return func() { p.summary = s }, nil
	})
}

// geneLength is the reference length of a gene (0 when unknown).
func (p *explorePage) geneLength(gene string) int {
	if p.a.Project == nil {
		return 0
	}
	return p.a.Project.GeneLength(gene)
}

// genes for the pickers: the summary's, else the reference's.
func (p *explorePage) genes() []string {
	if len(p.summary.genes) > 0 {
		return p.summary.genes
	}
	var out []string
	for _, g := range p.a.Genes() {
		out = append(out, g.Name)
	}
	return out
}

// ---- view

func (p *explorePage) View(c *ui.Context) {
	a := p.a
	t := c.Theme()
	p.track()
	ui.Column(c).Fill().Padding(20, 24).Gap(14).Children(func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Column(c).Grow(1).Basis(320).Gap(2).Children(func() {
				ui.Text(c, "Explore").FontSize(24).Bold()
				what, change := "Selection: "+a.SelectionLabel(a.Selected.Key), " · change it in the top bar."
				if p.pinned != nil {
					what, change = "Samples from "+nameOr(p.pinnedFrom, "Compare")+": "+p.samplesLabel(), ""
				}
				note := what + change
				if p.summary.records > 0 {
					note = fmt.Sprintf("%s · %s hybrids, %s reads%s", what, FormatNumber(p.summary.records), FormatNumber(p.summary.reads), change)
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, note).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
					if p.pinned != nil && ui.Button(c, "Use the selection").
						Tooltip("Show "+a.SelectionLabel(a.Selected.Key)+", the top bar's selection, instead").Clicked() {
						p.pinned, p.pinnedKey, p.pinnedFrom = nil, "", ""
						p.track()
					}
				})
			})
			ui.Segmented(c.Key("explore-tabs"), &p.tab, exploreTabs...).Label("Explore views")
		})
		// the binding energies need a sample, not an RNA
		if len(p.ids) == 0 || (p.gene == "" && p.tab != tabEnergies) {
			p.emptyState(c)
			return
		}
		p.loadSummary()
		if p.sumLoad.err != "" {
			errorLine(c, "Could not read the samples' analysis summary: "+p.sumLoad.err)
		}
		switch p.tab {
		case tabInteractions:
			p.interactionsView(c.Key("tab-interactions"))
		case tabContact:
			p.contactView(c.Key("tab-contact"))
		case tabViewpoint:
			p.viewpointView(c.Key("tab-viewpoint"))
		case tabRegion:
			p.regionView(c.Key("tab-region"))
		case tabEnergies:
			p.batchEnergyView(c.Key("tab-energies"))
		}
	})
}

// emptyState says what is missing: an analysed sample in the project, one
// in the selection, or an RNA.
func (p *explorePage) emptyState(c *ui.Context) {
	Card(c, "", func() {
		muted := c.Theme().TextMuted
		switch {
		case len(p.a.SelectionIDs(AllSelection)) == 0:
			ui.Text(c, "No analysed sample yet. Run a sample on the Data page; it appears here when its analysis index is built.").TextColor(muted)
			if ui.PrimaryButton(c, "Go to Data").Clicked() {
				p.a.Navigate("data", nil)
			}
		case len(p.ids) == 0:
			ui.Text(c, "The selection has no analysed sample. Choose another selection in the top bar.").TextColor(muted)
		default:
			ui.Text(c, "Choose an RNA in the top bar to explore it.").TextColor(muted)
		}
	})
}

// ---- shared bits

// loadingLine shows a spinner while l is busy, or its error.
func loadingLine(c *ui.Context, l *loader, what string) {
	if l.busy() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label("Loading")
			ui.Text(c, "Loading "+what+"…").TextColor(c.Theme().TextMuted)
		})
	} else if l.err != "" {
		errorLine(c, "Could not load "+what+": "+l.err)
	}
}

func errorLine(c *ui.Context, text string) {
	ui.Text(c, text).TextColor(c.Theme().Danger)
}

// notice is a muted one-line remark, e.g. a truncation or adjusted bin.
func notice(c *ui.Context, text string) {
	t := c.Theme()
	ui.Row(c).Padding(6, 10).Radius(6).Background(t.Warning.Alpha(0.12)).Children(func() {
		ui.Text(c, text).FontSize(12)
	})
}

// stat is a label over a number.
func stat(c *ui.Context, label, value string) {
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
		ui.Text(c, value).FontSize(16).Bold()
	})
}

// picker is a labelled select over options.
func picker(c *ui.Context, key, label string, value *string, options []string, width float32) ui.Element {
	var el ui.Element
	Field(c, label, func() {
		el = ui.Select(c.Key(key), value, options).Label(label).Width(width)
	})
	return el
}

// coordInput is a labelled text field for a coordinate (empty = unset).
func coordInput(c *ui.Context, key, label string, value *string, placeholder string) ui.Element {
	var el ui.Element
	Field(c, label, func() {
		el = ui.TextInput(c.Key(key), value).Placeholder(placeholder).Label(label).Width(96)
	})
	return el
}

// parseCoord reads a coordinate field: (0, true) when empty.
func parseCoord(s string) (int, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func coordText(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// span formats an inclusive range: 1,230–1,239.
func span(start, end int) string {
	return FormatNumber(int64(start)) + "–" + FormatNumber(int64(end))
}

// num reads a JSON number.
func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func ints(v any) []int {
	list, _ := v.([]any)
	out := make([]int, len(list))
	for i, x := range list {
		out[i] = int(num(x))
	}
	return out
}

func floats(v any) []float64 {
	list, _ := v.([]any)
	out := make([]float64, len(list))
	for i, x := range list {
		out[i] = num(x)
	}
	return out
}

func withAll(all string, names []string) []string {
	return append([]string{all}, names...)
}

// typesPayload: nil for all types, else the one chosen.
func typesPayload(choice string) []string {
	if choice == "" || choice == allTypes || choice == allPattern {
		return nil
	}
	return []string{choice}
}

// shortGene keeps long reference names readable in headings.
func shortGene(name string) string {
	if len(name) > 28 {
		return name[:26] + "…"
	}
	return name
}

// foldPrefill navigates to ⑤ Structure with a window to fold.
func (p *explorePage) foldPrefill(gene string, windows [][2]int, source string) {
	list := make([]map[string]int, 0, len(windows))
	for _, w := range windows {
		list = append(list, map[string]int{"start": w[0], "end": w[1]})
	}
	p.toStructure(map[string]any{"gene": gene, "windows": list, "source": source})
}

// toStructure navigates to ⑤ Structure with prefill, plus the selection
// key and, while Explore shows pinned samples, their ids ("sampleIds").
func (p *explorePage) toStructure(prefill map[string]any) {
	prefill["selectionKey"] = p.a.Selected.Key
	if p.pinned != nil {
		prefill["sampleIds"] = append([]string(nil), p.pinned...)
	}
	p.a.Navigate("structure", map[string]any{"prefill": prefill})
}

// profileColor is the bar color of coverage plots.
func profileColor(t *ui.Theme) ui.Color { return t.Accent }

// webFormat is the web Explore's format(): at most two decimals (rounded
// half away from zero), thousands grouped, as Intl.NumberFormat("en-US",
// {maximumFractionDigits: 2}); "—" for none.
func webFormat(v *float64) string {
	if v == nil {
		return "—"
	}
	text := jsFixed(*v, 2)
	if strings.ContainsAny(text, "eIN") { // past 1e21, or not a number
		return text
	}
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	sign := ""
	if rest, ok := strings.CutPrefix(text, "-"); ok {
		sign, text = "-", rest
	}
	whole, fraction, _ := strings.Cut(text, ".")
	n, _ := strconv.ParseInt(whole, 10, 64)
	out := FormatNumber(n)
	if fraction != "" {
		out += "." + fraction
	}
	if out == "0" {
		sign = ""
	}
	return sign + out
}

// webFormatValue is webFormat of a value.
func webFormatValue(v float64) string { return webFormat(&v) }
