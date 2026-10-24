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
}

func (f *exploreFake) Query(path, operation string, payload core.Obj) (any, error) {
	f.mu.Lock()
	copied := core.Obj{}
	raw, _ := json.Marshal(payload)
	_ = json.Unmarshal(raw, &copied)
	f.payloads[operation] = append(f.payloads[operation], copied)
	f.mu.Unlock()
	return f.fakeBackend.Query(path, operation, payload)
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
	if p.tab != tabContact || p.cm.data == nil {
		t.Fatalf("tab %d data %v err %q texts %q", p.tab, p.cm.data, p.cm.load.err, tt.Texts())
	}
	sent := f.last("analysis_contact_bins")
	if fmt.Sprint(sent["sample_ids"]) != "[SUB_REP1 SUB_REP2]" || sent["gene"] != zikvGene || sent["max_cells"] != float64(contactMaxCells) {
		t.Fatalf("contact payload %v", sent)
	}
	d := p.cm.data
	if len(d.count) != 2601 || d.bin != 100 || len(p.cm.heat.Cells()) != 2601 {
		t.Fatalf("cells %d bin %d", len(d.count), d.bin)
	}
	for _, text := range []string{"Explore", "Contact map", "Selected cell", "Bin size", "Colour cap", "1,724"} {
		if !tt.HasText(text) && !strings.Contains(strings.Join(tt.Texts(), "\n"), text) {
			t.Fatalf("contact map lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "explore-contact")

	// Click the strongest cell: the plot starts 44 DIPs right of the map box.
	best := 0
	for i, n := range d.count {
		if n > d.count[best] {
			best = i
		}
	}
	box, ok := tt.Find("Contact map of " + zikvGene)
	if !ok {
		t.Fatalf("no heatmap in %q", tt.Texts())
	}
	gx := (float64(d.x[best]) + float64(d.bin)/2) / float64(d.lengthX)
	gy := (float64(d.y[best]) + float64(d.bin)/2) / float64(d.lengthY)
	tt.ClickAt(box.X+44+float32(gx)*mapSizeTarget, box.Y+float32(gy)*mapSizeTarget)
	tt.Frame()
	if p.cm.picked != best {
		t.Fatalf("picked %d, want %d (box %+v)", p.cm.picked, best, box)
	}
	x0, x1 := cellRange(d.x[best], d.bin)
	if !tt.HasText("Arm 1 (X)") || !tt.HasText(span(x0, x1)) {
		t.Fatalf("details missing: %q", tt.Texts())
	}
	tt.Move(box.X+44+float32(gx)*mapSizeTarget, box.Y+float32(gy)*mapSizeTarget)
	tt.Frame()
	if !strings.Contains(p.cm.hover, fmt.Sprintf("%d records", d.count[best])) {
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
	filters := f.last("analysis_hybrids")["filters"].(map[string]any)
	if fmt.Sprint(filters["region"]) != fmt.Sprintf("map[x:[%d %d] y:[%d %d]]", x0, x1, y0, y1) || filters["gene1"] != zikvGene {
		t.Fatalf("hybrid filters %v", filters)
	}

	// Back to the map: the pick survived; fold its region.
	if err := tt.Click("Contact map"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if p.cm.picked != best {
		t.Fatalf("pick lost: %d", p.cm.picked)
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
	want := fmt.Sprint(map[string]any{"gene": zikvGene, "windows": []map[string]int{{"start": start, "end": end}},
		"selectionKey": "c:SUB", "source": "contact map"})
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
	for _, text := range []string{"Showing 40 of 1,724 hybrids", "Partner", "Arm 2", "Reads", "Load more", "Sample"} {
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
	for _, text := range []string{"Strongest interactions (25)", "Partners", "Show hybrids", "Fold this region"} {
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
	p.cm.picked = 3
	a.Selected.Key = "c:AFR"
	settle(a, tt)
	sent := f.last("analysis_contact_bins")
	if f.count("analysis_contact_bins") != before+1 || fmt.Sprint(sent["sample_ids"]) != "[AFR_REP1 AFR_REP2]" {
		t.Fatalf("no reload for the new selection: %d calls, last %v", f.count("analysis_contact_bins"), sent)
	}
	if p.cm.picked != -1 {
		t.Fatalf("pick kept across selections: %d", p.cm.picked)
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
	if p.tab != tabContact || p.cm.bin != "100 nt" || d == nil || p.cm.picked < 0 || d.x[p.cm.picked] != 3000 || d.y[p.cm.picked] != 1800 {
		t.Fatalf("tab %d bin %q picked %d", p.tab, p.cm.bin, p.cm.picked)
	}
	if !p.cm.heat.Zoomed() {
		t.Fatal("not zoomed to the cell")
	}
	snapshot(t, tt, "explore-contact-focus")
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
	if !strings.Contains(strings.Join(tt.Texts(), "|"), `homodimer proxy, matching "ggct", support ≥ 2, ΔG -30 to -10`) {
		t.Fatalf("summary %q", tt.Texts())
	}
	s.minDG, s.maxDG = "-5", "-20"
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("the ΔG range's lower bound must not exceed its upper bound") {
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
	for _, text := range []string{"Hybrid " + FormatNumber(r.ID) + " · " + r.Name, r.Sequence, "Cluster support", "Overlap score", "Copy sequence"} {
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

// The web's homodimer subsets and custom colour cap on the contact map.
func TestExploreContactHomodimerAndCustomCap(t *testing.T) {
	a, f, p, tt := openExplore(t)
	s := &p.cm
	s.homodimer = homodimerOptions[1]
	settle(a, tt)
	if sent := f.last("analysis_contact_bins"); sent["homodimer"] != "only" {
		t.Fatalf("payload %v", sent)
	}
	d := s.data
	if d.homodimer != homodimerOptions[1] {
		t.Fatalf("data subset %q", d.homodimer)
	}
	p.showCellHybrids(d, 1, 100, 1, 100)
	if p.ix.applied.scope != scopeLabels[3] || p.ix.applied.payload()["homodimer"] != "only" {
		t.Fatalf("interactions %+v", p.ix.applied)
	}
	p.tab = tabContact
	s.homodimer = homodimerOptions[2]
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
	if !tt.HasText("Cap (count)") {
		t.Fatalf("no custom input: %q", tt.Texts())
	}
	s.customCap = "1"
	p.recolor()
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "≥ 1 records") {
		t.Fatalf("legend %q", tt.Texts())
	}
}

// The pickers are the web's RNA and partner (either arm order); a drill-down
// with arm positions keeps them as arm 1 and arm 2.
func TestInteractionFiltersMatchEitherArm(t *testing.T) {
	f := ixFilters{gene1: "A", gene2: "B", minReads: 1, minSupport: 1}
	if got := f.payload(); got["gene"] != "A" || got["partner"] != "B" || got["gene1"] != nil {
		t.Fatalf("payload %v", got)
	}
	if got := describeFilters(f); !strings.Contains(got, "RNA A") || !strings.Contains(got, "partner B") {
		t.Fatalf("describe %q", got)
	}
	f.x0, f.x1 = 10, 20
	if got := f.payload(); got["gene1"] != "A" || got["gene2"] != "B" || got["gene"] != nil {
		t.Fatalf("oriented payload %v", got)
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
