package views

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

func dataMatchingOpen(t *testing.T) (*App, *dataFake, *ui.Tester) {
	t.Helper()
	a, f := dataOpen(t, "list_samples.json")
	f.queries["analysis_reference_matching"] = qcMatchingFixture()
	f.ops["set_reference_match"] = core.Obj{"ok": true}
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	return a, f, tt
}

// The web Data drawer's Name matching: its summary and FASTA warning on the
// References card, the per-RNA editor, Apply / Mark as not matched (kept on
// the project's reference) and Export matches.
func TestDataNameMatching(t *testing.T) {
	a, f, tt := dataMatchingOpen(t)
	dataExpect(t, tt, "Name matching", "1 of 2 RNA names have no reference sequence", "Review mapping…", "Reproducibility…",
		"ZAFR: 1 FASTA record contains unsupported symbol (N). Symbols were preserved so coordinates did not shift; affected regions cannot be folded.")
	snapshot(t, tt, "data-references")
	dataClick(t, tt, a, "Review mapping…")
	dataExpect(t, tt, "Each RNA name in the HYB file is paired with the reference sequence of the same name. To fix one, pick the RNA, "+
		"type the reference ID it should use, and apply.", "HYB RNA", "FASTA reference ID", "Apply mapping", "Mark as not matched",
		"Export matches", "RNA18S", "Not matched")
	snapshot(t, tt, "data-name-matching")
	p := a.pages["data"].(*dataPage)
	// the editor starts on the first RNA without a match
	if p.match.rna != "RNA18S" || p.match.sequence != "" {
		t.Fatalf("editor %q %q", p.match.rna, p.match.sequence)
	}
	p.match.sequence = "18S_ribosomal_RNA"
	dataClick(t, tt, a, "Apply mapping")
	calls := f.callsOf("set_reference_match")
	if len(calls) != 1 || calls[0]["reference_id"] != "ZAFR" || calls[0]["rna"] != "RNA18S" || calls[0]["sequence"] != "18S_ribosomal_RNA" {
		t.Fatalf("calls %v", calls)
	}
	dataExpect(t, tt, "Reference mapping updated locally.")
	virus := "ZIKV_African_KX377335-1_virusRNA"
	dataClick(t, tt, a, "Edit "+virus)
	if p.match.rna != virus || p.match.sequence != virus {
		t.Fatalf("editor %q %q", p.match.rna, p.match.sequence)
	}
	dataClick(t, tt, a, "Mark as not matched")
	if calls := f.callsOf("set_reference_match"); len(calls) != 2 || calls[1]["rna"] != virus || calls[1]["sequence"] != "" {
		t.Fatalf("calls %v", calls)
	}
	// only RNA names of the loaded files
	p.match.rna = "nope"
	dataClick(t, tt, a, "Apply mapping")
	dataExpect(t, tt, "Enter an exact RNA name from the loaded HYB file.")
	if len(f.callsOf("set_reference_match")) != 2 {
		t.Fatal("an unknown RNA was sent")
	}

	// the web's fasta-mapping.json: sourceHyb (the reference's samples),
	// sourceFasta and the mapping; the web's toast, saved
	asked := pickSaveInto(t, t.TempDir())
	before := lastToast(a)
	if err := tt.Click("Export matches"); err != nil {
		t.Fatal(err)
	}
	waitToast(a, before)
	if got := lastToast(a); got != "FASTA mapping saved locally." {
		t.Fatalf("toast %q", got)
	}
	text := readExport(t, asked)
	var out struct {
		Mapping   map[string]string `json:"mapping"`
		SourceHyb struct {
			FileName string           `json:"fileName"`
			Status   string           `json:"sha256Status"`
			Datasets []map[string]any `json:"datasets"`
		} `json:"sourceHyb"`
		SourceFasta map[string]any `json:"sourceFasta"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if name := filepath.Base((*asked)[0]); name != "ZAFR_fasta-mapping.json" || len(out.Mapping) != 1 || out.Mapping[virus] != virus ||
		out.SourceHyb.Status != "per-dataset" || len(out.SourceHyb.Datasets) != 2 || out.SourceHyb.Datasets[0]["fileName"] != "ZAFR_REP1_sub.fastq" ||
		out.SourceHyb.Datasets[0]["sha256Status"] != "pending" || out.SourceFasta["fileName"] != "ZAFR.fasta" || strings.Contains(text, `"samples"`) {
		t.Fatalf("%s:\n%s", name, text)
	}
}

// matchingByIDs answers analysis_reference_matching by the sample ids
// asked for, so the Data page's (every analysed sample) and the QC
// selection's matching differ.
type matchingByIDs struct {
	core.Backend
	byIDs map[string]any
}

func (b *matchingByIDs) Query(path, operation string, payload core.Obj) (any, error) {
	if operation == "analysis_reference_matching" {
		ids, _ := payload["sample_ids"].([]string)
		if r, ok := b.byIDs[strings.Join(ids, ",")]; ok {
			return r, nil
		}
	}
	return b.Backend.Query(path, operation, payload)
}

// allSamplesMatching is the matching of every analysed sample: ZBRA's
// samples come first, with their own unmatched RNA.
func allSamplesMatching() map[string]any {
	sub := qcMatchingFixture()
	bra := map[string]any{
		"reference_id": "ZBRA", "label": "ZBRA", "sequences": 1, "sequence_names": []any{"ZIKV_Brazil"},
		"sample_ids": []any{"ZBRA_REP1", "ZBRA_REP2"}, "matched": 0,
		"rnas":                []any{map[string]any{"name": "BRA_RNA", "match": nil, "manual": false}},
		"unsupported_entries": 0, "unsupported_characters": []any{},
	}
	sub["references"] = append([]any{bra}, sub["references"].([]any)...)
	sub["rna_names"], sub["sequences"] = 3, 3
	return sub
}

// The QC Overview's "Review mapping" opens the Data page's name matching
// of the same samples its callout counts (the top bar's selection, here
// SUB of six analysed samples), its editor on their first unmatched RNA.
func TestReviewMappingOpensNameMatching(t *testing.T) {
	a, _, tt := dataMatchingOpen(t)
	every := "SUB_REP1,SUB_REP2,AFR_REP1,AFR_REP2,ZBRA_REP1,ZBRA_REP2"
	a.B = &matchingByIDs{Backend: a.B, byIDs: map[string]any{every: allSamplesMatching(), "SUB_REP1,SUB_REP2": qcMatchingFixture()}}
	qcFor(a).match = refMatching{}
	a.Navigate("qc", nil)
	tt.Frame()
	for i := 0; i < 5 && !tt.HasText("Reference sequences loaded"); i++ {
		waitFlush(a)
		tt.Frame()
	}
	dataExpect(t, tt, "1 of 2 HYB RNA names matched locally.")
	dataClick(t, tt, a, "Review mapping")
	p := a.pages["data"].(*dataPage)
	if a.Page != "data" || !p.match.open {
		t.Fatalf("page %q", a.Page)
	}
	dataExpect(t, tt, "Apply mapping", "RNA names of the samples chosen in the top bar: SUB · 2 datasets merged.", "1 of 2 RNA names have no reference sequence")
	if strings.Join(p.match.ids, ",") != "SUB_REP1,SUB_REP2" || p.match.reference != "ZAFR" || p.match.rna != "RNA18S" || p.match.sequence != "" {
		t.Fatalf("dialog %+v", p.match)
	}
	if tt.HasText("BRA_RNA") {
		t.Fatal("the dialog lists RNAs outside the selection")
	}

	// From the Data page, with the matching not loaded yet: every analysed
	// sample, the editor placed once it arrives (ZBRA first, BRA_RNA).
	p.match.open = false
	qcFor(a).match = refMatching{}
	p.openMatching(qcReadyIDs(a), "")
	if p.match.rna != "" || !p.match.place {
		t.Fatalf("placed before loading %+v", p.match)
	}
	for i := 0; i < 5 && p.match.place; i++ {
		waitFlush(a)
		tt.Frame()
	}
	tt.Frame()
	if p.match.reference != "ZBRA" || p.match.rna != "BRA_RNA" || p.match.sequence != "" {
		t.Fatalf("dialog %+v", p.match)
	}
	dataExpect(t, tt, "RNA names of the 6 analysed samples.", "BRA_RNA")
}

// Setting a sample's reference loads the name matching again: the cache
// key holds each sample's reference and the project's references.
func TestNameMatchingFollowsReferences(t *testing.T) {
	a, f, tt := dataMatchingOpen(t)
	ids := qcReadyIDs(a)
	key := refMatchKey(a, ids)
	a.Project.Samples[0].ReferenceID = "ZBRA"
	if refMatchKey(a, ids) == key {
		t.Fatal("the key ignores a sample's reference")
	}
	before := matchingQueries(f)
	tt.Frame()
	waitFlush(a)
	if after := matchingQueries(f); after != before+1 {
		t.Fatalf("matching queries %d -> %d", before, after)
	}
	key = refMatchKey(a, ids)
	a.Project.References = append(a.Project.References, a.Project.References[0])
	a.Project.References[len(a.Project.References)-1].ID = "NEW"
	if refMatchKey(a, ids) == key {
		t.Fatal("the key ignores a new reference")
	}
}

func matchingQueries(f *dataFake) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Count(strings.Join(f.calls, ","), "query:analysis_reference_matching")
}

// Reproducibility is offered without any reference too (the web always
// shows it, with the HYB SHA-256).
func TestDataReproducibilityWithoutReferences(t *testing.T) {
	a, _, tt := dataMatchingOpen(t)
	a.Project.References = nil
	tt.Frame()
	dataExpect(t, tt, "No reference yet. Add the FASTA the reads were (or will be) mapped to; sample sheets add theirs automatically.")
	dataClick(t, tt, a, "Reproducibility…")
	dataExpect(t, tt, "Versions and file fingerprints for your methods section", "SUB_REP1 SHA-256")
}

// Reproducibility: the app version and each input file's SHA-256.
func TestDataReproducibility(t *testing.T) {
	a, _, tt := dataMatchingOpen(t)
	a.Project.Samples[2].Reads.SHA256 = "abc123"
	dataClick(t, tt, a, "Reproducibility…")
	dataExpect(t, tt, "Versions and file fingerprints for your methods section", "App version", "Git commit",
		"AFR_REP1 SHA-256", "abc123", "ZAFR FASTA SHA-256", "Not computed yet")
	snapshot(t, tt, "data-reproducibility")
}

// Reference rows: Replace for every reference, Remove only for one no
// sample uses (the runtime refuses the others).
func TestDataReferenceReplaceAndRemove(t *testing.T) {
	a, f, tt := dataMatchingOpen(t)
	f.ops["remove_reference"] = core.Obj{"ok": true}
	if tt.HasText("Remove") {
		t.Fatalf("a used reference offers Remove: %q", tt.Texts())
	}
	dataPick(t, "/data/new.fasta")
	dataClick(t, tt, a, "Replace ZBRA's FASTA")
	if calls := f.callsOf("update_reference"); len(calls) != 1 || calls[0]["reference_id"] != "ZBRA" || calls[0]["source"] != "/data/new.fasta" {
		t.Fatalf("calls %v", calls)
	}
	for i := range a.Project.Samples {
		if a.Project.Samples[i].ReferenceID == "ZBRA" {
			a.Project.Samples[i].ReferenceID = ""
		}
	}
	tt.Frame()
	dataClick(t, tt, a, "Remove reference ZBRA")
	if calls := f.callsOf("remove_reference"); len(calls) != 1 || calls[0]["reference_id"] != "ZBRA" {
		t.Fatalf("calls %v", calls)
	}
}

// The run dialog says what a run does: .hyb samples are only indexed.
func TestDataRunText(t *testing.T) {
	a, _ := dataOpen(t, "list_samples.json")
	if got := dataRunText(a.Project, []string{"AFR_REP1", "ZBRA_REP1"}); !strings.HasPrefix(got, "Each .hyb sample is indexed for analysis as one task") ||
		strings.Contains(got, "mapping") {
		t.Fatal(got)
	}
	if got := dataRunText(a.Project, []string{"SUB_REP1"}); !strings.HasPrefix(got, "Each sample runs mapping → .hyb → analysis index") {
		t.Fatal(got)
	}
	if got := dataRunText(a.Project, []string{"SUB_REP1", "AFR_REP1"}); !strings.Contains(got, "each .hyb sample is indexed for analysis") {
		t.Fatal(got)
	}
}

// The sample Remove button shows only with a selection.
func TestDataRemoveHiddenWithoutSelection(t *testing.T) {
	a, _ := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	if tt.HasText("Remove selected") || tt.HasText("Remove") {
		t.Fatalf("Remove without a selection: %q", tt.Texts())
	}
	dataClick(t, tt, a, "Select AFR_REP2")
	dataExpect(t, tt, "Remove selected")
}
