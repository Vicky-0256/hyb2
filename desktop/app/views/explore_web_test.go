package views

import (
	"fmt"
	"strings"
	"testing"

	"hyb2desktop/core"
)

// The web Explore's format(): two decimals at most, thousands grouped.
func TestWebFormat(t *testing.T) {
	for v, want := range map[float64]string{-23.456: "-23.46", -23.4: "-23.4", 12: "12", 1234.5: "1,234.5", 0.004: "0",
		-0.004: "0", 1e-9: "0", 0.125: "0.13", -1234567.891: "-1,234,567.89"} {
		if got := webFormatValue(v); got != want {
			t.Errorf("webFormat(%v) = %q, want %q", v, got, want)
		}
	}
	if webFormat(nil) != "—" {
		t.Fatal("nil")
	}
}

// The table's first column is the .hyb name; the drawer has the web's
// per-arm read coordinates and e-values, source reads, homodimer proxy,
// type source, dataset and source line.
func TestInteractionRecordDrawerMatchesWeb(t *testing.T) {
	a, f, p, tt := openInteractions(t)
	s := &p.ix
	r := &s.rows[0]
	r.Raw = "100001_1\tACGU"
	start, end, evalue := 3.0, 42.0, 1.23456e-7
	overlap := 7.0
	dg := -23.456
	r.ReadStart1, r.ReadEnd1, r.EValue1, r.TypeSource, r.Overlap, r.DG = &start, &end, &evalue, "column_17", &overlap, &dg
	f.setAnswer(func(operation string, payload core.Obj) (any, bool) {
		if operation == "analysis_hybrids" && payload["with_line"] == true {
			return map[string]any{"total": 1.0, "rows": []any{map[string]any{"id": float64(r.ID), "line": 1234.0}}}, true
		}
		return nil, false
	})
	s.chosen = 0
	settle(a, tt)
	sent := f.last("analysis_hybrids")
	if fmt.Sprint(sent["sample_ids"]) != "[SUB_REP1]" || fmt.Sprint(sent["filters"]) != fmt.Sprintf("map[id:%d]", r.ID) {
		t.Fatalf("line query %v", sent)
	}
	for _, text := range []string{"Interaction record", "100001_1", "Read coordinates", "3–42", "E-value", "0", "Source reads", "1",
		"Homodimer proxy", "Yes (overlap ≥ 5)", "Type source", "Column 17", "Dataset", "SUB_REP1 · SUB_REP1.hyb",
		"Source line", "1,234", "ΔG (kcal/mol)", "-23.46"} {
		if !tt.HasText(text) {
			t.Fatalf("drawer lacks %q: %q", text, tt.Texts())
		}
	}
	if got := sourceReads(hybridRow{Name: "abc_x"}); got != "—" {
		t.Fatalf("source reads %q", got)
	}
	if got := sourceReads(hybridRow{Name: "abc_12_3"}); got != "12" {
		t.Fatalf("source reads %q", got)
	}
}

// Region's reference panel: a read error is shown with Retry (not "Add a
// reference FASTA"), a replaced reference is read again, and a new region
// shows no old bases while it loads.
func TestRegionReferenceErrorRetryAndVersion(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1100, 2200)
	p.exploreRegion(3000, 3600)
	settle(a, tt)
	if !tt.HasText("Could not read the reference sequence: unsupported project operation read_reference_sequence") ||
		!tt.HasText("Retry") || tt.HasText("Add a reference FASTA to extract this region.") {
		t.Fatalf("read error: %q", tt.Texts())
	}
	f.fakeBackend.ops["read_reference_sequence"] = map[string]any{"ok": true, "sequence": strings.Repeat("acgu", 3000)}
	settle(a, tt)
	if !tt.HasText("Retry") {
		t.Fatal("a failed read retried by itself")
	}
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if !tt.HasText("UACGUACGUA CGUACGUACG UACGUACGUA CGUACGUACG UACGUACGUA") || tt.HasText("Retry") {
		t.Fatalf("after retry: %q", tt.Texts())
	}
	// update_reference: same id, a new file
	ref := a.Project.Reference(stReferenceOf(a.Project, zikvGene))
	if ref == nil {
		t.Fatal("no reference")
	}
	ref.QuickHash += "-replaced"
	f.fakeBackend.ops["read_reference_sequence"] = map[string]any{"ok": true, "sequence": strings.Repeat("g", 12000)}
	settle(a, tt)
	if !tt.HasText("GGGGGGGGGG GGGGGGGGGG GGGGGGGGGG GGGGGGGGGG GGGGGGGGGG") {
		t.Fatalf("replaced reference not read again: %q", tt.Texts())
	}
	// set_reference_match (or Relink) invalidates the name matching
	qcFor(a).match.invalidate()
	f.fakeBackend.ops["read_reference_sequence"] = map[string]any{"ok": true, "sequence": strings.Repeat("c", 12000)}
	settle(a, tt)
	if !tt.HasText("CCCCCCCCCC CCCCCCCCCC CCCCCCCCCC CCCCCCCCCC CCCCCCCCCC") {
		t.Fatalf("new matching not read again: %q", tt.Texts())
	}
	// a new region: the old bases go at once, so Copy cannot copy them
	d := *p.rg.data
	d.f.start, d.f.end = 100, 200
	p.loadRegionSequence(&d)
	if p.rg.seq != "" || p.rg.seqKey != "" {
		t.Fatalf("old bases kept while the new region loads: %q", p.rg.seq)
	}
}

// Region: the matching records (the region's own anchor set, 20 shown,
// 100 on request), Show hybrids with that set, the reference panel and the
// CPLcofold request; the partner bars show their values.
func TestRegionRecordsReferenceAndShowHybrids(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1100, 2200)
	f.fakeBackend.ops["read_reference_sequence"] = map[string]any{"ok": true, "sequence": strings.Repeat("acgu", 3000)}
	p.exploreRegion(3000, 3600)
	p.rg.overlap = overlapRules[1]
	p.rg.partner, p.rg.partnerStart, p.rg.partnerEnd = zikvGene, "5000", "5100"
	applied, err := p.rg.fromInputs()
	if err != nil {
		t.Fatal(err)
	}
	p.rg.applied = applied
	settle(a, tt)
	var records core.Obj
	for _, sent := range f.all("analysis_hybrids") {
		if _, ok := sent["filters"].(map[string]any)["anchor"]; ok {
			records = sent
		}
	}
	anchor, _ := records["filters"].(map[string]any)["anchor"].(map[string]any)
	if anchor["overlap"] != "contained" || anchor["partner_start"] != 5000.0 || records["limit"] != 100.0 || records["order"] != "file" {
		t.Fatalf("records query %v", records)
	}
	if len(p.rg.records) != 40 || !tt.HasText("Show 100 records") {
		t.Fatalf("records %d: %q", len(p.rg.records), tt.Texts())
	}
	for _, text := range []string{"Reference sequence", "3,000", "UACGUACGUA CGUACGUACG UACGUACGUA CGUACGUACG UACGUACGUA",
		"Showing the first 300 of 601 nt. Copy sequence and Download FASTA use the whole region.", "Send both regions to CPLcofold",
		"Coordinates remain inclusive, matching the HYB record display.", "either arm fully inside · partner " + shortGene(zikvGene) + ":5,000–5,100"} {
		if !tt.HasText(text) {
			t.Fatalf("region lacks %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("Show 100 records"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !p.rg.showAll || !tt.HasText("Show fewer records") {
		t.Fatalf("toggle: %q", tt.Texts())
	}
	if err := tt.Click("Send both regions to CPLcofold"); err != nil {
		t.Fatal(err)
	}
	prefill := structureOptions(a)["prefill"].(map[string]any)
	if fmt.Sprint(prefill["regions"]) != fmt.Sprintf("[{%s 3000 3600} {%s 5000 5100}]", zikvGene, zikvGene) || prefill["source"] != "region" {
		t.Fatalf("prefill %v", prefill)
	}
	a.Navigate("explore", nil)
	p.tab = tabRegion
	settle(a, tt)
	if err := tt.Click("Show hybrids"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	filters := f.last("analysis_hybrids")["filters"].(map[string]any)
	got, _ := filters["anchor"].(map[string]any)
	if p.tab != tabInteractions || got["gene"] != zikvGene || got["overlap"] != "contained" || got["partner_end"] != 5100.0 ||
		filters["region"] != nil || filters["gene1"] != nil {
		t.Fatalf("show hybrids %v", filters)
	}
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "region "+shortGene(zikvGene)+":3,000–3,600 (either arm, fully contained") {
		t.Fatalf("summary %q", tt.Texts())
	}
	if err := tt.Click("Remove region"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if _, ok := f.last("analysis_hybrids")["filters"].(map[string]any)["anchor"]; ok {
		t.Fatal("region filter kept")
	}
}

// A normalised inter-RNA contact cell lists both arm orders, as it counts.
func TestContactCellHybridsBothOrders(t *testing.T) {
	_, _, p, _ := openExplore(t)
	d := &contactData{gene: "A", gene2: "B", bin: 10, orientation: orientBoth, typ: allPattern, homodimer: cmHomodimerOptions[0]}
	p.showCellHybrids(d, 100, 109, 200, 209)
	got := p.ix.applied.payload()
	either, _ := got["either"].([]core.Obj)
	if len(either) != 2 || got["gene1"] != nil || got["region"] != nil ||
		fmt.Sprint(either[0]) != "map[gene1:A gene2:B region:map[x:[100 109] y:[200 209]]]" ||
		fmt.Sprint(either[1]) != "map[gene1:B gene2:A region:map[x:[200 209] y:[100 109]]]" {
		t.Fatalf("payload %v", got)
	}
	if !strings.Contains(describeFilters(p.ix.applied), "either arm order") {
		t.Fatal(describeFilters(p.ix.applied))
	}
	d.orientation = orientOriented
	p.showCellHybrids(d, 100, 109, 200, 209)
	if got := p.ix.applied.payload(); got["either"] != nil || got["gene1"] != "A" {
		t.Fatalf("oriented payload %v", got)
	}
}

// Viewpoint: the observed range by default, the full mapped reference on
// request, the web's data table, selection stats and notes; a new gene
// starts again.
func TestViewpointWebControls(t *testing.T) {
	a, f, p, tt := openExplore(t)
	tt.SetSize(1100, 2400)
	if err := tt.Click("Viewpoint"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	sent := f.last("analysis_viewpoint")
	if sent["start"] != nil || sent["end"] != nil || sent["full_reference"] != nil {
		t.Fatalf("payload %v", sent)
	}
	for _, text := range []string{"Coordinate range", "Selected coordinates", "1 nt (legacy viewpoint)", "Record count", "All records",
		"At 1 nt resolution, each plotted value is the number of overlapping interaction arms, matching the legacy viewpoint interpretation.",
		zikvGene + " interaction coverage", "Select a viewpoint bar", "Maximum coverage", "57.56", "Resolution", "25 nt", "Method"} {
		if !tt.HasText(text) {
			t.Fatalf("viewpoint lacks %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("Open data table"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Mean interaction coverage") || !tt.HasText("Hide data table") {
		t.Fatalf("no table: %q", tt.Texts())
	}
	p.vp.rangeMode = rangeModes[1]
	p.vp.from, p.vp.to = 10, 20
	settle(a, tt)
	sent = f.last("analysis_viewpoint")
	if sent["full_reference"] != true || sent["start"] != nil {
		t.Fatalf("payload %v", sent)
	}
	if !tt.HasText("The full mapped reference uses coordinates 1 through its FASTA length; Start and End are ignored.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	p.vp.profile.Picked = 3
	tt.Frame()
	if !tt.HasText("Mean coverage") || !tt.HasText("Mapped arms") {
		t.Fatalf("selection: %q", tt.Texts())
	}
	if err := tt.Click("Reset selection"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.vp.profile.Picked != -1 {
		t.Fatal("selection kept")
	}
	// the backend widened the bins: the picker and a notice say so
	p.vp.data.adjusted, p.vp.data.requested, p.vp.data.bin = true, 1, 2
	tt.Frame()
	if !tt.HasText("1 nt (legacy viewpoint) · using 2 nt") || !tt.HasText("Requested 1 nt resolution; the plot uses 2 nt to keep this plot responsive.") {
		t.Fatalf("adjusted: %q", tt.Texts())
	}
	p.vp.partner, p.vp.start = "X", "50"
	p.vp.selectionChanged(true)
	if p.vp.from != 0 || p.vp.to != 0 || p.vp.start != "" || p.vp.partner != allPartners || p.vp.fullReference() {
		t.Fatalf("kept %+v", p.vp)
	}
}

// Compare's tested group: Explore queries those samples when the
// selection does not hold exactly them, and says which.
func TestExploreUsesCompareSamples(t *testing.T) {
	a, f, p, tt := openExplore(t)
	a.Navigate("explore", map[string]any{"view": "region", "selectionKey": "c:SUB", "sampleIds": []string{"SUB_REP1", "AFR_REP1"},
		"binSize": 10, "x": 3000, "y": 5000})
	settle(a, tt)
	if got := fmt.Sprint(f.last("analysis_region")["sample_ids"]); got != "[SUB_REP1 AFR_REP1]" {
		t.Fatalf("samples %s", got)
	}
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "Samples from Compare: SUB_REP1, AFR_REP1") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Use the selection"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if got := fmt.Sprint(f.last("analysis_region")["sample_ids"]); got != "[SUB_REP1 SUB_REP2]" || p.pinned != nil {
		t.Fatalf("samples %s", got)
	}
	// the selection's own samples: nothing pinned
	a.Navigate("explore", map[string]any{"view": "region", "sampleIds": []any{"SUB_REP2", "SUB_REP1"}, "x": 3000})
	if p.pinned != nil {
		t.Fatalf("pinned %v", p.pinned)
	}
}
