package views

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// exploreFake answers the analysis queries from testdata/explore_*.json
// (captured from the ZIKV GUI project, selection SUB) and records payloads.
type exploreFake struct {
	*fakeBackend
	mu       sync.Mutex
	payloads map[string][]core.Obj
	// answer, when set, answers a query before the fixtures (ok false: the
	// fixture answers).
	answer func(operation string, payload core.Obj) (any, bool)
}

func (f *exploreFake) Query(path, operation string, payload core.Obj) (any, error) {
	f.mu.Lock()
	copied := core.Obj{}
	raw, _ := json.Marshal(payload)
	_ = json.Unmarshal(raw, &copied)
	f.payloads[operation] = append(f.payloads[operation], copied)
	answer := f.answer
	f.mu.Unlock()
	if answer != nil {
		if got, ok := answer(operation, copied); ok {
			return got, nil
		}
	}
	return f.fakeBackend.Query(path, operation, payload)
}

func (f *exploreFake) all(operation string) []core.Obj {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.Obj(nil), f.payloads[operation]...)
}

// mapQuery is the last contact map query (not one for a measure on its bins).
func (f *exploreFake) mapQuery() core.Obj {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.payloads["analysis_contact_bins"]
	for i := len(list) - 1; i >= 0; i-- {
		if list[i]["fixed_bin"] != true {
			return list[i]
		}
	}
	return nil
}

func (f *exploreFake) last(operation string) core.Obj {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.payloads[operation]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

func (f *exploreFake) count(operation string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.payloads[operation])
}

func openExplore(t *testing.T) (*App, *exploreFake, *explorePage, *ui.Tester) {
	t.Helper()
	base := newFake(t)
	for op, name := range map[string]string{"analysis_summary": "explore_summary.json", "analysis_contact_bins": "explore_contact.json",
		"analysis_hybrids": "explore_hybrids.json", "analysis_viewpoint": "explore_viewpoint.json", "analysis_region": "explore_region.json"} {
		base.queries[op] = fixture(t, name)
	}
	f := &exploreFake{fakeBackend: base, payloads: map[string][]core.Obj{}}
	a := New(f, nil, "")
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	if a.Project == nil {
		t.Fatalf("project did not open: %q", a.start.status)
	}
	a.RegisterPage("structure", &navSpy{a: a})
	a.Navigate("explore", nil)
	p := a.PageFor("explore").(*explorePage)
	tt := ui.NewTester(a.View, 1100, 720)
	settle(a, tt)
	return a, f, p, tt
}

// navSpy stands in for ⑤ Structure and keeps the options it was shown with.
type navSpy struct {
	a       *App
	options map[string]any
}

func (n *navSpy) Show()            { n.options = n.a.NavOptions }
func (n *navSpy) View(*ui.Context) {}

func structureOptions(a *App) map[string]any { return a.PageFor("structure").(*navSpy).options }

// settle draws, lets queries answer, and draws again.
func settle(a *App, tt *ui.Tester) {
	for range 3 {
		tt.Frame()
		waitFlush(a)
	}
	tt.Frame()
}

const zikvGene = "ZIKV_African_KX377335-1_virusRNA"

func TestExploreContactMapPickShowHybridsAndFold(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1400, 1300)
	settle(a, tt)
	if p.tab != tabContact || p.cm.data == nil {
		t.Fatalf("tab %d data %v err %q texts %q", p.tab, p.cm.data, p.cm.load.err, tt.Texts())
	}
	sent := f.mapQuery()
	if fmt.Sprint(sent["sample_ids"]) != "[SUB_REP1]" || sent["gene"] != zikvGene || sent["max_cells"] != float64(contactMaxCells) {
		t.Fatalf("contact payload %v", sent)
	}
	d := p.cm.data
	if len(d.count) != 2601 || d.bin != 100 || len(p.cm.cmap.Cells()) != 2601 {
		t.Fatalf("cells %d bin %d", len(d.count), d.bin)
	}
	for _, text := range []string{"Explore", "Contact map", "SELECTED REGION", "Select a heatmap cell", "Bin size", "Colour cap", "Records in map", "1,724", "Full extent"} {
		if !tt.HasText(text) && !strings.Contains(strings.Join(tt.Texts(), "\n"), text) {
			t.Fatalf("contact map lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "explore-contact")

	// Click the strongest cell: it is selected and the map zooms one step
	// around it, as the web's.
	best := 0
	for i, n := range d.count {
		if n > d.count[best] {
			best = i
		}
	}
	x, y := contactCellPoint(t, p, tt, d.x[best], d.y[best])
	tt.ClickAt(x, y)
	tt.Frame()
	if p.cm.pickedIndex() != best || !p.cm.cmap.Zoomed() || !tt.HasText("Zoomed extent") {
		t.Fatalf("picked %d, want %d, zoomed %v", p.cm.pickedIndex(), best, p.cm.cmap.Zoomed())
	}
	x0, x1 := cellRange(d.x[best], d.bin)
	if !tt.HasText(d.gene+":"+span(x0, x1)) || !tt.HasText("Explore region") {
		t.Fatalf("details missing: %q", tt.Texts())
	}
	x, y = contactCellPoint(t, p, tt, d.x[best], d.y[best])
	tt.Move(x, y)
	tt.Frame()
	if !strings.Contains(p.cm.hover, fmt.Sprintf("%s records · %s cluster support", FormatNumber(int64(d.records[best])), FormatNumber(int64(d.support[best])))) {
		t.Fatalf("hover %q", p.cm.hover)
	}
	snapshot(t, tt, "explore-contact-picked")
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "explore-contact-dark")
	tt.SetDark(false)

	// Show hybrids: Interactions filtered to the cell's ranges.
	if err := tt.Click("Show hybrids"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if p.tab != tabInteractions {
		t.Fatalf("tab %d", p.tab)
	}
	y0, y1 := cellRange(d.y[best], d.bin)
	sentHybrids := f.last("analysis_hybrids")
	filters := sentHybrids["filters"].(map[string]any)
	if fmt.Sprint(filters["region"]) != fmt.Sprintf("map[x:[%d %d] y:[%d %d]]", x0, x1, y0, y1) || filters["gene1"] != zikvGene {
		t.Fatalf("hybrid filters %v", filters)
	}
	// the map's one dataset, not the selection's two
	if fmt.Sprint(sentHybrids["sample_ids"]) != "[SUB_REP1]" || !strings.Contains(strings.Join(tt.Texts(), "|"), "Samples from the contact map: SUB_REP1") {
		t.Fatalf("hybrids of %v: %q", sentHybrids["sample_ids"], tt.Texts())
	}

	// Back to the map: the pick survived; fold its region.
	if err := tt.Click("Contact map"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if p.cm.pickedIndex() != best {
		t.Fatalf("pick lost: %d", p.cm.pickedIndex())
	}
	if err := tt.Click("Fold this region"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.Page != "structure" {
		t.Fatalf("page %q", a.Page)
	}
	pad := max(50, 2*d.bin)
	start := max(1, min(d.x[best], d.y[best])-pad)
	end := min(10807, max(d.x[best], d.y[best])+d.bin-1+pad)
	prefill := structureOptions(a)["prefill"].(map[string]any)
	// the samples Explore shows (pinned by Show hybrids) go along
	want := fmt.Sprint(map[string]any{"gene": zikvGene, "windows": []map[string]int{{"start": start, "end": end}},
		"selectionKey": "c:SUB", "source": "contact map", "sampleIds": []string{"SUB_REP1"}})
	if fmt.Sprint(prefill) != want {
		t.Fatalf("prefill %v, want %v", prefill, want)
	}
}

func TestExploreInteractionsTableAndLoadMore(t *testing.T) {
	a, f, p, tt := openExplore(t)
	if err := tt.Click("Interactions"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if len(p.ix.rows) != 40 || p.ix.total != 1724 {
		t.Fatalf("rows %d total %d err %q", len(p.ix.rows), p.ix.total, p.ix.load.err)
	}
	for _, text := range []string{"Showing 40 of 1,724 hybrids", "Partner", "Choose an RNA in the filter bar to see its interaction partners.", "Sequence ID", "100001_1", "Region 2 (nt)", "Reads", "Load more", "Sample"} {
		if !tt.HasText(text) {
			t.Fatalf("interactions lack %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "explore-interactions")
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "explore-interactions-dark")
	tt.SetDark(false)
	if err := tt.Click("Load more"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if len(p.ix.rows) != 80 || f.last("analysis_hybrids")["offset"] != float64(40) {
		t.Fatalf("after load more: rows %d payload %v", len(p.ix.rows), f.last("analysis_hybrids"))
	}
	// Filters apply to the query (and reset the paging).
	p.ix.minReads = "2"
	p.ix.order = orderLabels[1]
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	sent := f.last("analysis_hybrids")
	if sent["order"] != "reads" || sent["offset"] != float64(0) || fmt.Sprint(sent["filters"]) != "map[min_reads:2]" {
		t.Fatalf("payload %v", sent)
	}
	if len(p.ix.rows) != 40 {
		t.Fatalf("rows %d", len(p.ix.rows))
	}
}

func TestExploreViewpointAndRegion(t *testing.T) {
	a, f, p, tt := openExplore(t)
	// Tall enough that "Explore region" stays in view with any platform's
	// fonts (Windows renders taller and pushed it below a 720 px window).
	tt.SetSize(1100, 1400)
	if err := tt.Click("Viewpoint"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if p.vp.data == nil || p.vp.profile.Len() == 0 {
		t.Fatalf("viewpoint %+v err %q", p.vp.data, p.vp.load.err)
	}
	if !tt.HasText("Partners in range") || !tt.HasText("Selected interval") {
		t.Fatalf("viewpoint texts %q", tt.Texts())
	}
	if sent := f.last("analysis_viewpoint"); sent["homodimer"] != nil {
		t.Fatalf("payload %v", sent)
	}
	p.vp.homodimer = homodimerOptions[2]
	settle(a, tt)
	if sent := f.last("analysis_viewpoint"); sent["homodimer"] != "exclude" {
		t.Fatalf("payload %v", sent)
	}
	snapshot(t, tt, "explore-viewpoint")
	// Pick a bin and explore its region.
	box, ok := tt.Find("Viewpoint coverage of " + zikvGene)
	if !ok {
		t.Fatalf("no profile: %q", tt.Texts())
	}
	tt.ClickAt(box.X+profileAxisForTest+(box.W-profileAxisForTest)/2, box.Y+60)
	tt.Frame()
	if p.vp.profile.Picked < 0 {
		t.Fatal("no bin picked")
	}
	start, end, _ := p.vp.profile.Bin(p.vp.profile.Picked)
	if err := tt.Click("Explore region"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	sent := f.last("analysis_region")
	if p.tab != tabRegion || sent["start"] != float64(start) || sent["end"] != float64(end) || sent["gene"] != zikvGene {
		t.Fatalf("tab %d region payload %v (bin %d-%d)", p.tab, sent, start, end)
	}
	for _, text := range []string{"Matching records", "Top partners", "Show hybrids", "Fold this region", "Showing 20 of 1,724. Export includes all local matches."} {
		if !tt.HasText(text) {
			t.Fatalf("region lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "explore-region")
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "explore-region-dark")
	tt.SetDark(false)
	if err := tt.Click("Fold this region"); err != nil {
		t.Fatal(err)
	}
	prefill := structureOptions(a)["prefill"].(map[string]any)
	// the fixture answers 3000-3600 whatever was asked; the fold uses the range shown
	if fmt.Sprint(prefill["windows"]) != "[map[end:3600 start:3000]]" || prefill["source"] != "region" || prefill["gene"] != zikvGene {
		t.Fatalf("prefill %v", prefill)
	}
}

const profileAxisForTest = 48

func TestExploreFollowsSelection(t *testing.T) {
	a, f, p, tt := openExplore(t)
	before := f.count("analysis_contact_bins")
	p.cm.cmap.Selection = &[2]int{p.cm.data.x[3], p.cm.data.y[3]}
	a.Selected.Key = "c:AFR"
	settle(a, tt)
	sent := f.mapQuery()
	// the map of records, and the cluster support on its bins, of the new
	// selection's first sample (one dataset per map)
	if f.count("analysis_contact_bins") != before+2 || fmt.Sprint(sent["sample_ids"]) != "[AFR_REP1]" {
		t.Fatalf("no reload for the new selection: %d calls, last %v", f.count("analysis_contact_bins"), sent)
	}
	if p.cm.cmap.Selection != nil {
		t.Fatalf("pick kept across selections: %v", *p.cm.cmap.Selection)
	}
	if fmt.Sprint(f.last("analysis_summary")["sample_ids"]) != "[AFR_REP1 AFR_REP2]" {
		t.Fatalf("summary not reloaded: %v", f.last("analysis_summary"))
	}
	// Redrawing the same selection does not query again.
	n := f.count("analysis_contact_bins")
	settle(a, tt)
	if f.count("analysis_contact_bins") != n {
		t.Fatal("redraw queried again")
	}
}

func TestExploreDropsStaleResults(t *testing.T) {
	a, _, p, _ := openExplore(t)
	l := &p.cm.load
	l.reset()
	p.ensure(l, "old", func() (func(), error) { return func() { t.Error("stale result applied") }, nil })
	p.ensure(l, "new", func() (func(), error) { return nil, nil })
	waitFlush(a)
	if l.shown != "new" || l.busy() {
		t.Fatalf("loader %+v", *l)
	}
}

func TestExploreNavigationFromCompare(t *testing.T) {
	a, f, p, tt := openExplore(t)
	a.Navigate("explore", map[string]any{"view": "region", "selectionKey": "c:SUB", "gene": zikvGene,
		"binSize": 10, "x": 3000, "y": 5000, "xEnd": 3009, "yEnd": 5009})
	settle(a, tt)
	sent := f.last("analysis_region")
	if p.tab != tabRegion || sent["start"] != float64(3000) || sent["end"] != float64(3009) ||
		sent["partner_start"] != float64(5000) || sent["partner_end"] != float64(5009) || a.NavOptions != nil {
		t.Fatalf("tab %d payload %v", p.tab, sent)
	}
	a.Navigate("explore", map[string]any{"view": "contact-map", "binSize": 100, "x": 3000, "y": 1800, "xEnd": 3099, "yEnd": 1899})
	settle(a, tt)
	d := p.cm.data
	if i := p.cm.pickedIndex(); p.tab != tabContact || p.cm.bin != "100 nt" || d == nil || i < 0 || d.x[i] != 3000 || d.y[i] != 1800 {
		t.Fatalf("tab %d bin %q picked %d", p.tab, p.cm.bin, i)
	}
	if !p.cm.cmap.Zoomed() {
		t.Fatal("not zoomed to the cell")
	}
	snapshot(t, tt, "explore-contact-focus")
}

// An inter-RNA cell from Compare puts y on RNA Y: the region's partner and
// the contact map's Y gene follow "gene2".
func TestExploreNavigationInterRNA(t *testing.T) {
	a, f, p, tt := openExplore(t)
	a.Navigate("explore", map[string]any{"view": "region", "selectionKey": "c:SUB", "gene": zikvGene, "gene2": "RNA_Y",
		"binSize": 10, "x": 3000, "y": 50, "xEnd": 3009, "yEnd": 59})
	settle(a, tt)
	if p.rg.applied.partner != "RNA_Y" || f.last("analysis_region")["partner"] != "RNA_Y" {
		t.Fatalf("partner %q payload %v", p.rg.applied.partner, f.last("analysis_region"))
	}
	a.Navigate("explore", map[string]any{"view": "contact-map", "gene2": "RNA_Y", "binSize": 100, "x": 3000, "y": 100})
	settle(a, tt)
	if p.tab != tabContact || p.contactGene2() != "RNA_Y" {
		t.Fatalf("tab %d gene2 %q", p.tab, p.contactGene2())
	}
}

// The web's Interactions filters reach analysis_hybrids; a row opens its
// record card with the .hyb name and sequence.
func TestExploreInteractionFiltersAndRecord(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1100, 1000)
	if err := tt.Click("Interactions"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	s := &p.ix
	s.scope, s.search, s.minSupport, s.minDG, s.maxDG, s.order = scopeLabels[3], " ggct ", "2", "−30", "-10", "Support (most first)"
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	sent := f.last("analysis_hybrids")
	filters := sent["filters"].(map[string]any)
	if filters["homodimer"] != "only" || filters["search"] != "ggct" || filters["min_support"] != float64(2) ||
		filters["min_dg"] != float64(-30) || filters["max_dg"] != float64(-10) || sent["order"] != "support" || sent["with_sequence"] != true {
		t.Fatalf("payload %v", sent)
	}
	if !strings.Contains(strings.Join(tt.Texts(), "|"), `homodimer proxy (overlap ≥ 5), matching "ggct", support ≥ 2, ΔG -30 to -10`) {
		t.Fatalf("summary %q", tt.Texts())
	}
	s.minDG, s.maxDG = "-5", "-20"
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("minimum dG must not exceed maximum dG") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if s.scope != scopeLabels[0] || s.search != "" || s.minDG != "" {
		t.Fatalf("not cleared: %+v", s.applied)
	}
	// the record card
	s.chosen = 0
	tt.Frame()
	r := s.rows[0]
	for _, text := range []string{"Interaction record", r.Name, r.Sequence, "Cluster support", "Overlap score", "Copy sequence"} {
		if !tt.HasText(text) {
			t.Fatalf("record card lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "explore-record")
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.chosen != -1 {
		t.Fatal("card still open")
	}
}

// "Records in map" counts hybrids whatever the cells weigh: a support map
// asks for the records on its bins too.
func TestExploreContactRecordsInMap(t *testing.T) {
	a, f, p, tt := openExplore(t)
	before := f.count("analysis_contact_bins")
	p.cm.measure = cmSupport
	settle(a, tt)
	sent := f.last("analysis_contact_bins")
	if f.count("analysis_contact_bins") != before+2 || sent["measure"] != "records" || sent["fixed_bin"] != true {
		t.Fatalf("%d queries, last %v", f.count("analysis_contact_bins")-before, sent)
	}
	if p.cm.data.recordsTotal != 1724 || !tt.HasText("Records in map") {
		t.Fatalf("records %d, texts %q", p.cm.data.recordsTotal, tt.Texts())
	}
}

// The web's homodimer subsets and custom colour cap on the contact map.
func TestExploreContactHomodimerAndCustomCap(t *testing.T) {
	a, f, p, tt := openExplore(t)
	s := &p.cm
	s.homodimer = cmHomodimerOptions[1]
	settle(a, tt)
	if sent := f.last("analysis_contact_bins"); sent["homodimer"] != "only" {
		t.Fatalf("payload %v", sent)
	}
	d := s.data
	if d.homodimer != cmHomodimerOptions[1] {
		t.Fatalf("data subset %q", d.homodimer)
	}
	p.showCellHybrids(d, 1, 100, 1, 100)
	if p.ix.applied.scope != scopeLabels[3] || p.ix.applied.payload()["homodimer"] != "only" {
		t.Fatalf("interactions %+v", p.ix.applied)
	}
	p.tab = tabContact
	s.homodimer = cmHomodimerOptions[2]
	settle(a, tt)
	if sent := f.last("analysis_contact_bins"); sent["homodimer"] != "exclude" {
		t.Fatalf("payload %v", sent)
	}

	// custom: a displayed value; nothing usable is the maximum
	maximum := float64(d.sorted[len(d.sorted)-1])
	for _, c := range []struct {
		custom, scale string
		want          float64
	}{{"3", scaleLinear, 3}, {"", scaleLinear, maximum}, {"-2", scaleLinear, maximum}, {"x", scaleLinear, maximum},
		{"2", scaleLog, 2}, {"", scaleLog, math.Log1p(maximum)}} {
		if got := d.colorCap(capCustom+":"+c.custom, c.scale); got != c.want {
			t.Fatalf("custom %q %s: %g, want %g", c.custom, c.scale, got, c.want)
		}
	}
	s.colorCap = capCustom
	tt.Frame()
	if !tt.HasText("Custom cap") {
		t.Fatalf("no custom input: %q", tt.Texts())
	}
	s.customCap = "1"
	p.recolor()
	tt.Frame()
	// the colour bar, drawn on the map: 0 to the cap, the cap marked ≥
	l := s.cmap.Legend
	if last := l.Ticks[len(l.Ticks)-1]; l.Title != "Record count (linear)" || l.Max != 1 || l.Format(last) != "≥ 1" {
		t.Fatalf("legend %q max %g last %q", l.Title, l.Max, l.Format(last))
	}
}

// contactCellPoint is where the bin at (x, y) is drawn on the contact map.
func contactCellPoint(t *testing.T, p *explorePage, tt *ui.Tester, x, y int) (float32, float32) {
	t.Helper()
	d := p.cm.data
	box, ok := tt.Find("Contact map of " + d.gene + " × " + d.gene2 + ". Drag to select a region. Click a cell to zoom and inspect it.")
	if !ok {
		t.Fatalf("no contact map in %q", tt.Texts())
	}
	side := p.contactSide() // box is clipped to what shows
	g := p.cm.cmap.Geometry(side, side)
	bin := float32(d.bin)
	return box.X + g.Left + (float32(x-g.View.XStart)/bin+0.5)*g.Cell, box.Y + g.Top + (float32(g.View.YEnd-y)/bin+0.5)*g.Cell
}

// The pickers are the web's RNA and partner (either arm order), also with
// arm positions, which apply to arm 1 and arm 2 on their own; only a
// contact map cell's drill-down (exactArms) makes them arm 1 and arm 2.
func TestInteractionFiltersMatchEitherArm(t *testing.T) {
	f := ixFilters{gene1: "A", gene2: "B", minReads: 1, minSupport: 1}
	if got := f.payload(); got["gene"] != "A" || got["partner"] != "B" || got["gene1"] != nil {
		t.Fatalf("payload %v", got)
	}
	if got := describeFilters(f); !strings.Contains(got, "RNA A") || !strings.Contains(got, "partner B") {
		t.Fatalf("describe %q", got)
	}
	f.x0, f.x1, f.y0 = 10, 20, 5
	got := f.payload()
	if got["gene"] != "A" || got["partner"] != "B" || got["gene1"] != nil || fmt.Sprint(got["region"]) != "map[x:[10 20] y:[5 1000000000000]]" {
		t.Fatalf("payload with positions %v", got)
	}
	if !f.withPartners() || f.oriented() {
		t.Fatal("positions dropped the partner panel")
	}
	if got := describeFilters(f); !strings.Contains(got, "RNA A") || !strings.Contains(got, "arm 1 in 10–20") {
		t.Fatalf("describe %q", got)
	}
	f.exactArms = true
	if got := f.payload(); got["gene1"] != "A" || got["gene2"] != "B" || got["gene"] != nil {
		t.Fatalf("cell payload %v", got)
	}
	// arm positions cleared: the RNA matches either arm again
	f.x0, f.x1, f.y0 = 0, 0, 0
	if got := f.payload(); got["gene"] != "A" || got["gene1"] != nil {
		t.Fatalf("cell payload without positions %v", got)
	}
}

// The Interactions page's labels are the web's (analysis-pages.js), and the
// pickers stay RNA and Partner with arm positions.
func TestInteractionLabelsMatchWeb(t *testing.T) {
	a, _, p, tt := openExplore(t)
	tt.SetSize(1400, 1000)
	if err := tt.Click("Interactions"); err != nil {
		t.Fatal(err)
	}
	p.ix.moreOpen = true
	p.ix.x0 = "10"
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	for _, text := range []string{"Arm 1 start", "Arm 1 end", "Arm 2 start", "Arm 2 end", "Minimum dG", "Maximum dG",
		"Maximum e-value", "Minimum support", "RNA", "Partner"} {
		if !tt.HasText(text) {
			t.Fatalf("no %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("Arm 1 RNA") {
		t.Fatal("arm positions renamed the RNA picker")
	}
	if box, ok := tt.Find("Search"); !ok || !strings.Contains(fmt.Sprint(tt.Texts()), "Search") {
		t.Fatalf("no search field %v", box)
	}
}

// A picker applies its value at once, even while a text draft is invalid.
func TestInteractionPickerIgnoresBadDrafts(t *testing.T) {
	var s interactionsState
	s.init()
	s.applied.search = "kept"
	s.minReads, s.gene2, s.scope = "abc", "B", scopeLabels[2]
	s.applyPickers()
	if f := s.applied; f.gene2 != "B" || f.gene1 != "" || f.scope != scopeLabels[2] || f.search != "kept" || f.minReads != 1 {
		t.Fatalf("applied %+v", f)
	}
}

// Valid unapplied drafts go along with a picker change.
func TestInteractionPickerKeepsValidDrafts(t *testing.T) {
	var s interactionsState
	s.init()
	s.search, s.minReads, s.x0, s.x1 = " new ", "5", "100", "200"
	s.gene1 = "A"
	s.applyPickers()
	if f := s.applied; f.gene1 != "A" || f.search != "new" || f.minReads != 5 || f.x0 != 100 || f.x1 != 200 {
		t.Fatalf("applied %+v", f)
	}
}

// The arrow keys move the selected cell after the Export menu, the buttons
// beside the map or a Display choice; typing a custom cap keeps the focus.
func TestContactMapKeysAfterOtherControls(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1400, 1400) // the whole map shows under the Data card
	settle(a, tt)
	s := &p.cm
	d := s.data
	x, y := contactCellPoint(t, p, tt, d.x[0], d.y[0])
	tt.ClickAt(x, y)
	tt.Frame()
	right := func(what string) {
		t.Helper()
		before := *s.cmap.Selection
		tt.Key(0, ui.KeyRight)
		tt.Frame()
		if got := *s.cmap.Selection; got[0] != before[0]+d.bin || got[1] != before[1] {
			t.Fatalf("after %s: Right moved %v to %v", what, before, got)
		}
	}
	right("a click on the map")

	exportMenuClick(t, a, tt, "Parameters · JSON…")
	right("the Export menu")

	f.setAnswer(syntheticHybrids(2))
	exportWith(t, a, tt, func() {
		if err := tt.Click("Export records"); err != nil {
			t.Fatal(err)
		}
	})
	right("Export records")

	if err := tt.Click("Open data table"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	right("Open data table")

	// a Display choice made with the keys, as a keyboard user would
	r, ok := tt.Find("Mint–navy (follows theme)")
	if !ok {
		t.Fatal("no colour style")
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if s.palette != "White–red (Juicebox style)" {
		t.Fatalf("style %q", s.palette)
	}
	right("a colour style")

	// a custom cap: its field keeps the focus while the value is typed
	s.colorCap = capCustom
	tt.Frame()
	r, ok = tt.Find("Custom cap")
	if !ok {
		t.Fatalf("no custom cap field: %q", tt.Texts())
	}
	tt.ClickAt(r.X+20, r.Y+r.H+14) // the input under its label
	tt.Type("12")
	tt.Frame()
	if s.customCap != "12" {
		t.Fatalf("custom cap %q", s.customCap)
	}
}

// The web's names and codes of the chimera patterns, however the type is
// written; an unknown type keeps its own name.
func TestChimeraPatternNames(t *testing.T) {
	for _, c := range []struct{ code, name, label string }{
		{"Type_3", "Overlap · 5′ arm starts first", "Type 3"}, {"Type 3", "Overlap · 5′ arm starts first", "Type 3"},
		{"type3", "Overlap · 5′ arm starts first", "Type 3"}, {"Type_13", "Touching · 3′ arm first", "Type 13"},
		{"Intermolecular", "Different RNAs", "Intermolecular"}, {"Odd_type", "Odd_type", "Odd type"},
	} {
		if got := patternName(c.code); got != c.name {
			t.Errorf("%s: name %q, want %q", c.code, got, c.name)
		}
		if got := patternCodeLabel(c.code); got != c.label {
			t.Errorf("%s: code %q, want %q", c.code, got, c.label)
		}
	}
	types := []string{"Type_1", "Type_2", "Intermolecular"}
	if patternCode("Separated · 3′ arm first", types) != "Type_2" || patternCode(allPattern, types) != allTypes || patternCode("Different RNAs", types) != "Intermolecular" {
		t.Fatal("names do not map back to their codes")
	}
	if len(chimeraPatterns) != 14 {
		t.Fatalf("%d patterns", len(chimeraPatterns))
	}
}
