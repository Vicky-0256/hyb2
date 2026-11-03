package views

import (
	"fmt"
	"strings"
	"testing"
)

// The contact map draws one dataset, as the web's: the selection's first by
// default, another from the Dataset picker; its drill-downs use it.
func TestContactMapDrawsOneDataset(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1400, 1400)
	settle(a, tt)
	if got := fmt.Sprint(f.mapQuery()["sample_ids"]); got != "[SUB_REP1]" {
		t.Fatalf("default dataset %s", got)
	}
	for _, text := range []string{"Dataset", "Contact Map draws one dataset of its choice."} {
		if !tt.HasText(text) {
			t.Fatalf("no %q: %q", text, tt.Texts())
		}
	}
	p.cm.dataset = "AFR_REP1" // another analysed sample, outside the selection
	settle(a, tt)
	if got := fmt.Sprint(f.mapQuery()["sample_ids"]); got != "[AFR_REP1]" || p.contactDatasetID() != "AFR_REP1" {
		t.Fatalf("chosen dataset %s", got)
	}
	d := p.cm.data
	if fmt.Sprint(d.ids) != "[AFR_REP1]" {
		t.Fatalf("map of %v", d.ids)
	}
	if got := fmt.Sprint(p.contactDatasetJSON(d)); got != "map[fileName: id:AFR_REP1 label:AFR_REP1]" {
		t.Fatalf("parameters dataset %s", got)
	}
	// the other samples of the selection do not matter to the map
	n := f.count("analysis_contact_bins")
	sel := [2]int{d.x[0], d.y[0]}
	p.cm.cmap.Selection = &sel
	p.exploreCellRegion(d, sel)
	settle(a, tt)
	if got := fmt.Sprint(f.last("analysis_region")["sample_ids"]); got != "[AFR_REP1]" || fmt.Sprint(p.pinned) != "[AFR_REP1]" {
		t.Fatalf("region of %s, pinned %v", got, p.pinned)
	}
	if f.count("analysis_contact_bins") != n || p.cm.cmap.Selection == nil {
		t.Fatal("pinning the map's dataset reloaded the map")
	}
	// one sample selected: no picker note, and that sample is the map's
	p.pinned, p.cm.dataset, p.tab = nil, "", tabContact
	a.Selected.Key = "s:SUB_REP2"
	settle(a, tt)
	if got := fmt.Sprint(f.mapQuery()["sample_ids"]); got != "[SUB_REP2]" || tt.HasText("Contact Map draws one dataset of its choice.") {
		t.Fatalf("one-sample selection: %s", got)
	}
}

// Viewpoint's Explore region takes the bin as it is, the viewpoint's
// partner and no partner range; Compare's region without yEnd clears it.
func TestRegionNavigationClearsThePartnerRange(t *testing.T) {
	a, _, p, tt := openExplore(t)
	s := &p.rg
	s.setPartner("OTHER", 5000, 5100)
	p.vp.partner = zikvGene
	p.exploreRegion(101, 105)
	if f := s.applied; f.start != 101 || f.end != 105 || f.partner != zikvGene || f.partnerStart != 0 || f.pEnd != 0 ||
		s.partner != zikvGene || s.partnerStart != "" || s.partnerEnd != "" || p.tab != tabRegion {
		t.Fatalf("region %+v inputs %q %q %q", f, s.partner, s.partnerStart, s.partnerEnd)
	}
	p.vp.partner = allPartners
	p.exploreRegion(101, 105)
	if s.applied.partner != "" || s.partner != allPartners {
		t.Fatalf("partner %q", s.applied.partner)
	}
	s.setPartner(zikvGene, 5000, 5100)
	a.Navigate("explore", map[string]any{"view": "region", "x": 3000, "xEnd": 3100})
	settle(a, tt)
	if f := s.applied; f.start != 3000 || f.end != 3100 || f.partner != "" || f.partnerStart != 0 || f.pEnd != 0 {
		t.Fatalf("Compare region %+v", f)
	}
}

// Region's "Show 100 records" is for one region: a new one starts short.
func TestRegionRecordsShortAgainForANewRegion(t *testing.T) {
	a, _, p, tt := openExplore(t)
	tt.SetSize(1100, 2200)
	p.exploreRegion(3000, 3600)
	settle(a, tt)
	p.rg.showAll = true
	p.exploreRegion(4000, 4600)
	p.rg.applied.overlap = overlapRules[1] // the fixture answers every range with its own
	settle(a, tt)
	if p.rg.showAll {
		t.Fatal("the long list stayed for a new region")
	}
}

// Binding energies need a sample, not an RNA; the empty state says what
// is missing.
func TestBindingEnergiesWithoutAnRNA(t *testing.T) {
	a, _, p, tt := openExplore(t)
	a.Selected.Gene = ""
	p.tab = tabEnergies
	settle(a, tt)
	if !tt.HasText("Calculate binding energies") {
		t.Fatalf("energies blocked: %q", tt.Texts())
	}
	p.tab = tabContact
	tt.Frame()
	if !tt.HasText("Choose an RNA in the top bar to explore it.") || tt.HasText("No analysed sample yet. Run a sample on the Data page; it appears here when its analysis index is built.") {
		t.Fatalf("empty state %q", tt.Texts())
	}
}

// The web's run-time estimate, CPLcofold hint and missing-reference state.
func TestBindingEnergiesEstimateAndReference(t *testing.T) {
	a, _, p, tt := openExplore(t)
	a.Navigate("explore", map[string]any{"tab": "energies"})
	settle(a, tt)
	// 1,724 records at the web's 15 ms on the budget's 10 CPUs
	note := batchEngines[0][2] + " All 1,724 records: about 3 s with 10 parallel workers here."
	if !tt.HasText(note) {
		t.Fatalf("no estimate %q: %q", note, tt.Texts())
	}
	p.be.engine = batchEngines[1][1]
	tt.Frame()
	if !tt.HasText(batchEngines[1][2] + " All 1,724 records: about 5 min with 10 parallel workers here. For many records, filter first or use the cplcofold command.") {
		t.Fatalf("no CPLcofold hint: %q", tt.Texts())
	}
	for _, c := range []struct {
		engine  string
		records int64
		cpus    int
		want    string
	}{{"rnacofold", 10, 8, " All 10 records: about 1 s with 1 worker here."}, {"cplcofold-pk", 100_000, 4, " All 100,000 records: about 16.0 h with 4 parallel workers here."},
		{"rnacofold", 0, 4, ""}} {
		if got := batchEstimate(c.engine, c.records, c.cpus); got != c.want {
			t.Fatalf("%v: %q", c, got)
		}
	}
	if !tt.HasText("Unmapped, antisense, ambiguous, over-limit or unselected rows retain their original energy and are listed in the audit.") {
		t.Fatalf("no audit note: %q", tt.Texts())
	}
	for i := range a.Project.Samples {
		if a.Project.Samples[i].ID == p.ids[0] {
			a.Project.Samples[i].ReferenceID = ""
		}
	}
	tt.Frame()
	if !tt.HasText(batchNoReference) {
		t.Fatalf("no reference note: %q", tt.Texts())
	}
	if err := tt.Click("Calculate binding energies"); err == nil && len(a.TaskList()) > 0 {
		t.Fatal("Calculate ran without a reference")
	}
}

// The Sample column names the dataset, not its id.
func TestInteractionsSampleColumnShowsTheLabel(t *testing.T) {
	a, _, p, tt := openExplore(t)
	for i := range a.Project.Samples {
		if a.Project.Samples[i].ID == "SUB_REP1" {
			a.Project.Samples[i].Name = "Subgenomic one"
		}
	}
	p.tab = tabInteractions
	settle(a, tt)
	texts := "|" + strings.Join(tt.Texts(), "|") + "|"
	if !strings.Contains(texts, "|Subgenomic one|") || strings.Contains(texts, "|SUB_REP1|") {
		t.Fatalf("sample column %q", tt.Texts())
	}
}

// With samples pinned from Compare, Structure gets them with the window.
func TestFoldPrefillCarriesPinnedSamples(t *testing.T) {
	a, _, p, tt := openExplore(t)
	a.Navigate("explore", map[string]any{"view": "region", "selectionKey": "c:SUB", "sampleIds": []string{"SUB_REP1", "AFR_REP1"},
		"x": 3000, "xEnd": 3100})
	settle(a, tt)
	p.foldPrefill(zikvGene, [][2]int{{3000, 3100}}, "region")
	prefill := structureOptions(a)["prefill"].(map[string]any)
	if fmt.Sprint(prefill["sampleIds"]) != "[SUB_REP1 AFR_REP1]" || prefill["selectionKey"] != "c:SUB" {
		t.Fatalf("prefill %v", prefill)
	}
	// nothing pinned: no sampleIds
	a.Navigate("explore", nil)
	p.pinned = nil
	p.track()
	p.foldPrefill(zikvGene, [][2]int{{3000, 3100}}, "region")
	if prefill := structureOptions(a)["prefill"].(map[string]any); prefill["sampleIds"] != nil {
		t.Fatalf("prefill %v", prefill)
	}
}
