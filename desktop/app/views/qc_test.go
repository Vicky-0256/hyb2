package views

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// qcFake holds analysis_summary until gate is closed (loading state).
type qcFake struct {
	*fakeBackend
	gate chan struct{}
}

func (f *qcFake) Query(path, operation string, payload core.Obj) (any, error) {
	if f.gate != nil {
		<-f.gate
	}
	return f.fakeBackend.Query(path, operation, payload)
}

// qcMatchingFixture is an analysis_reference_matching answer: of two RNA
// names, one matches a sequence of ZAFR; one FASTA record holds an N.
func qcMatchingFixture() map[string]any {
	virus := "ZIKV_African_KX377335-1_virusRNA"
	return map[string]any{
		"references": []any{map[string]any{
			"reference_id": "ZAFR", "label": "ZAFR", "sequences": 2, "sequence_names": []any{virus, "18S_ribosomal_RNA"},
			"sample_ids": []any{"SUB_REP1", "SUB_REP2"}, "matched": 1,
			"rnas": []any{
				map[string]any{"name": virus, "match": virus, "manual": false},
				map[string]any{"name": "RNA18S", "match": nil, "manual": false},
			},
			"unsupported_entries": 1, "unsupported_characters": []any{"N"},
		}},
		"rna_names": 2, "matched": 1, "sequences": 2, "unreferenced_sample_ids": []any{},
		"rna_name_list": []any{"RNA18S", virus},
	}
}

func qcOpen(t *testing.T, gate chan struct{}, withSummary bool) (*App, *qcFake) {
	t.Helper()
	f := newFake(t)
	f.queries["analysis_overview"] = fixture(t, "qc_analysis_overview.json")["result"]
	f.queries["analysis_reference_matching"] = qcMatchingFixture()
	if withSummary {
		f.queries["analysis_summary"] = fixture(t, "qc_analysis_summary.json")["result"]
	}
	qf := &qcFake{fakeBackend: f, gate: gate}
	a := New(qf, nil, "")
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	if a.Project == nil {
		t.Fatalf("project did not open: %q", a.start.status)
	}
	return a, qf
}

func TestQCPageCounts(t *testing.T) {
	gate := make(chan struct{})
	a, _ := qcOpen(t, gate, true)
	a.Navigate("qc", nil)
	tt := ui.NewTester(a.View, 1100, 1500) // tall enough for the Overview and the table
	dataExpect(t, tt, "Quality control", "Loading counts…", "loading…")
	close(gate)
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt,
		// The web's per-dataset table: its columns first, then the desktop's.
		"Per-dataset summary", "2 of 6 datasets analysed", "Dataset", "Condition", "HYB records", "Source reads", "RNAs", "RNA pairs",
		"Analysed", "Hybrids / reads", "757", "224,154", "239,156", "0.94", "349,387", "411,891", "Yes", "No", "rep 2",
		// Shares as the web's Intl percent: all intra-molecular, no ΔG; the four largest types as columns.
		"100%", "0%", "Type 2", "Type 1", "Type 7", "Type 13", "55.5%", "56%",
		// Conditions with more than one dataset, added up: AFR = 224,154 + 349,387; BRA = 266,248 + 286,509.
		"AFR · 2 merged", "573,541", "651,047", "552,757", "1,724")
	if tt.HasText("loading…") || tt.HasText("100.0%") || tt.HasText("Conditions (replicates merged)") {
		t.Fatalf("still loading or old labels: %q", tt.Texts())
	}
	// The web's Overview for the top bar's selection.
	dataExpect(t, tt, "Overview", "2 datasets · loaded locally · 44,832 valid records", "include a numeric dG value", "HYB records", "44,832", "One valid tab-delimited record per row", "Source reads", "46,005",
		"Recovered from 43,118 sequence IDs", "Unique RNAs", "RNA pairs", "RNA arm order is normalised for summary",
		"Top interacting RNAs", "Cluster support, per arm", "87,440", "4,204", "Chimera patterns", "24,528 records · 54.7%",
		"Top RNA pairs", "Normalised pairs", "41,618", "Data quality", "Local validation", "2.9%", "613",
		"homodimer-proxy records (overlap ≥ 5)", "Summary JSON…", "RNA counts CSV…", "RNA pairs CSV…",
		// the reference callout (analysis_reference_matching)
		"Reference sequences loaded", "2 FASTA sequences · 1 of 2 HYB RNA names matched locally.", "Review mapping")
	if tt.HasText("Legacy column 16 detected.") {
		t.Fatal("no legacy column 16 rows in this selection")
	}
	snapshot(t, tt, "qc")
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "qc-dark")
	tt.SetDark(false)
	tt.Frame()

	// The Data page shows the same counts, compactly.
	q := qcFor(a)
	if q.stats["AFR_REP2"].Records != 349387 || q.stats["SUB_REP1"].Reads != 757 {
		t.Fatalf("stats %+v", q.stats)
	}
	// A sample's name selects it and opens Explore.
	if err := tt.Click("Explore AFR_REP1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.Page != "explore" || a.Selected.Key != "s:AFR_REP1" {
		t.Fatalf("page %q selection %q", a.Page, a.Selected.Key)
	}
}

func TestQCPageError(t *testing.T) {
	a, f := qcOpen(t, nil, false)
	a.Navigate("qc", nil)
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 720)
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Could not load the counts: no fixture for analysis_summary", "Retry")
	snapshot(t, tt, "qc-error")
	f.queries["analysis_summary"] = fixture(t, "qc_analysis_summary.json")["result"]
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "224,154")
	if tt.HasText("Could not load") {
		t.Fatalf("error still shown: %q", tt.Texts())
	}
}

func TestQCPageEmpty(t *testing.T) {
	f := newFake(t)
	listed := fixture(t, "list_samples.json")
	for _, s := range listed["samples"].([]any) {
		sample := s.(map[string]any)
		sample["ready"] = false
		sample["outputs"].(map[string]any)["index"] = nil
	}
	f.ops["list_samples"] = listed
	a := New(f, nil, "")
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	a.Navigate("qc", nil)
	tt := ui.NewTester(a.View, 1100, 720)
	dataExpect(t, tt, "No analysed samples yet.", "Go to Data")
	snapshot(t, tt, "qc-empty")
	if err := tt.Click("Go to Data"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.Page != "data" {
		t.Fatalf("page %q", a.Page)
	}
}

func TestOverviewCSV(t *testing.T) {
	rows := []ovCount{{Name: `a"b`, Records: 3, Support: 4, OverlapTotal: 2.5, OverlapRecords: 1}}
	want := `"rna","arm_occurrences","source_reads","source_read_count_arms","cluster_support","overlap_score_total","overlap_score_arms"` +
		"\n" + `"a""b","3","","0","4","2.5","1"`
	if got := string(ovRNAsCSV(rows)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	pairs := []ovCount{{Gene1: "A", Gene2: "B", Records: 2, Support: 2, Reads: 5, ReadRecords: 2}}
	if got := string(ovPairsCSV(pairs)); !strings.HasSuffix(got, "\n"+`"A","B","2","5","2","2","","0"`) {
		t.Fatalf("pairs %q", got)
	}
}

func TestQCExports(t *testing.T) {
	a, _ := qcOpen(t, nil, true)
	a.Navigate("qc", nil)
	tt := ui.NewTester(a.View, 1100, 2300)
	waitFlush(a)
	tt.Frame()
	waitFlush(a)
	tt.Frame()

	name, text := exportClick(t, a, tt, "RNA counts CSV…")
	if name != "SUB_rna_counts.csv" || !strings.HasPrefix(text, `"rna","arm_occurrences",`) ||
		!strings.Contains(text, "\n"+`"RNA18S","4024","3966","3716","4204","-8714152","3838"`) {
		t.Fatalf("%s:\n%s", name, text)
	}
	name, text = exportClick(t, a, tt, "RNA pairs CSV…")
	if name != "SUB_rna_pairs.csv" || !strings.Contains(text, `"RNA18S","ZIKV_African_KX377335-1_virusRNA","4024"`) {
		t.Fatalf("%s:\n%s", name, text)
	}
	// the web's hyb2-overview-summary.json: sourceFile and the parser's
	// camelCase summary
	name, text = exportClick(t, a, tt, "Summary JSON…")
	var summary struct {
		Analysis   string `json:"analysis"`
		SourceFile struct {
			Name     string           `json:"name"`
			Status   string           `json:"sha256Status"`
			Datasets []map[string]any `json:"datasets"`
		} `json:"sourceFile"`
		Summary map[string]any `json:"summary"`
	}
	if err := json.Unmarshal([]byte(text), &summary); err != nil {
		t.Fatal(err)
	}
	s := summary.Summary
	// rnaNames as the web parser's (from the name matching); a file not
	// fingerprinted yet is "pending"
	if names, _ := s["rnaNames"].([]any); len(names) != 2 || names[0] != "RNA18S" || summary.SourceFile.Datasets[0]["sha256Status"] != "pending" {
		t.Fatalf("rnaNames / sha256Status:\n%s", text)
	}
	if name != "SUB_hyb2-overview-summary.json" || summary.Analysis != "overview" || summary.SourceFile.Status != "per-dataset" ||
		len(summary.SourceFile.Datasets) != 2 || summary.SourceFile.Datasets[1]["validRecords"] != 24832.0 ||
		s["uniquePairs"] != 2.0 || s["validRecords"] != 44832.0 || s["rawReadCountRecords"] != 43118.0 ||
		s["skippedCommentLines"] != 1.0 || s["errorSampleTruncated"] != false || len(s["rnaCounts"].([]any)) != 2 {
		t.Fatalf("%s:\n%s", name, text)
	}
	name, text = exportClick(t, a, tt, "Export TSV…")
	rows := tsvRecords(t, text)
	if !strings.HasSuffix(name, "_qc.tsv") || len(rows) != 10 || rows[0][4] != "hybrids" || rows[0][8] != "analysed" ||
		rows[3][1] != "AFR_REP1" || rows[3][4] != "224154" || rows[3][6] != "1" || rows[3][8] != "No" || rows[1][8] != "Yes" ||
		rows[7][0] != "condition" || rows[8][4] != "573541" || rows[8][6] != "" {
		t.Fatalf("%s: %q", name, rows)
	}
}

// Shares are written as the web's Intl percent (at most one decimal).
func TestWebPercent(t *testing.T) {
	for fraction, want := range map[float64]string{0: "0%", 0.5: "50%", 0.547: "54.7%", 0.5474: "54.7%", 0.00049: "0%",
		0.0005: "0.1%", 1: "100%", 12.5: "1,250%", 0.9996: "100%"} {
		if got := webPercent(fraction); got != want {
			t.Errorf("%v: %q, want %q", fraction, got, want)
		}
	}
	if qcShare(1, 0) != "—" || qcShare(1, 3) != "33.3%" {
		t.Fatal(qcShare(1, 3))
	}
}

// Without a collapsed-read count in any ID, reads are "—", as the web's
// Source reads card; the TSV leaves them empty.
func TestQCReadsWithoutCounts(t *testing.T) {
	s := qcStats{Records: 10, Reads: 0}
	if qcCell(s, qcColReads, nil) != "—" || qcCell(s, qcColHybridsPerRead, nil) != "—" {
		t.Fatalf("%q %q", qcCell(s, qcColReads, nil), qcCell(s, qcColHybridsPerRead, nil))
	}
	s.Reads, s.ReadRecords = 20, 8
	if qcCell(s, qcColReads, nil) != "20" || qcCell(s, qcColHybridsPerRead, nil) != "0.50" {
		t.Fatal(qcCell(s, qcColReads, nil))
	}
	if merged := qcMerge([]qcStats{s, {Records: 1}}); merged.ReadRecords != 8 || merged.UniqueRNAs != nil {
		t.Fatalf("%+v", merged)
	}
	if v, note := ovSourceReads(&ovData{Reads: 0}); v != "—" || note != "No HYB2 collapsed-read counts found in sequence IDs" {
		t.Fatal(v, note)
	}
	if qcAnalysed([]string{"a"}, []string{"a", "b"}) != "Partly" || qcAnalysed(nil, []string{"a"}) != "No" {
		t.Fatal("analysed")
	}
}

// A run that finishes while counts are loading drops that answer: the
// counts load again instead of keeping the stale ones.
func TestQCStaleLoadIsDropped(t *testing.T) {
	gate := make(chan struct{})
	a, f := qcOpen(t, gate, true)
	a.Navigate("qc", nil)
	tt := ui.NewTester(a.View, 1100, 900)
	tt.Frame()
	q := qcFor(a)
	if !q.loading {
		t.Fatal("not loading")
	}
	q.invalidate() // what a finished run does
	close(gate)
	waitFlush(a)
	if q.loadedKey != "" || len(q.stats) != 0 {
		t.Fatalf("stale answer kept: %q %d", q.loadedKey, len(q.stats))
	}
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	if q.loadedKey == "" || q.stats["AFR_REP1"].Records != 224154 {
		t.Fatalf("not reloaded: %q", q.loadedKey)
	}
	f.mu.Lock()
	calls := strings.Join(f.calls, ",")
	f.mu.Unlock()
	if n := strings.Count(calls, "query:analysis_summary"); n != 2 {
		t.Fatalf("%d summary queries", n)
	}
}

// A failed overview query shows its error and a Retry, not a spinner.
func TestQCOverviewShowsLoadError(t *testing.T) {
	a, f := qcOpen(t, nil, true)
	delete(f.queries, "analysis_overview")
	a.Navigate("qc", nil)
	tt := ui.NewTester(a.View, 1100, 1500)
	for i := 0; i < 5 && !tt.HasText("Could not load the overview"); i++ {
		waitFlush(a)
		tt.Frame()
	}
	if !tt.HasText("Could not load the overview") || tt.HasText("Loading the overview…") {
		t.Fatalf("no overview error: %q", tt.Texts())
	}
	f.queries["analysis_overview"] = fixture(t, "qc_analysis_overview.json")["result"]
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5 && tt.HasText("Could not load the overview"); i++ {
		waitFlush(a)
		tt.Frame()
	}
	if tt.HasText("Could not load the overview") {
		t.Fatal("Retry did not reload the overview")
	}
}

// The Import check lists the invalid rows the indexes kept and exports
// the web's validation report.
func TestQCImportCheck(t *testing.T) {
	a, _ := qcOpen(t, nil, true)
	a.Navigate("qc", nil)
	// tall: the chimera patterns, with their diagrams, come before the check
	tt := ui.NewTester(a.View, 1100, 2800)
	waitFlush(a)
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Import check", "2 datasets are ready (counts added up)", "Valid records", "Skipped comment lines", "Skipped blank lines", "Invalid records",
		"Standard 17-column records", "44,830", "Records with overlap score", "43,805", "IDs with collapsed-read count",
		"Homodimer-proxy records",
		// each file's own counts
		"File", "Valid records", "Invalid rows", "Homodimer-proxy", "20,000", "24,832", "313",
		"2 invalid rows were skipped. The files remain usable with their valid records.",
		"invalid RNA coordinates", "SUB_REP1", "AFR_REP1", "Download validation report…")
	if tt.HasText("additional invalid-row details were omitted") || tt.HasText("Skipped comment and blank lines") {
		t.Fatalf("no details were omitted: %q", tt.Texts())
	}
	snapshot(t, tt, "qc-import-check")
	name, text := exportClick(t, a, tt, "Download validation report…")
	want := `"line","error","content","sample"` + "\n" +
		`"12","expected at least 15 tab-separated columns, got 3","bad` + "\trow\there" + `","SUB_REP1"` + "\n" +
		`"40","invalid RNA coordinates","x_1` + "\tAC\t1\t2\tZIKV\t0\t0" + `","AFR_REP1"`
	if name != "SUB_hyb2-validation-report.csv" || text != want {
		t.Fatalf("%s:\n%s", name, text)
	}

	// Details beyond the bounded sample: the web's error-sample wording.
	d := qcFor(a).ov.data
	d.OmittedErrors = 5
	// an index built before comment and blank lines were told apart; legacy
	// column-16 rows get the web's warnings
	d.SkippedComment, d.CanonicalRecords = nil, nil
	legacy := int64(3)
	d.LegacyColumn16 = &legacy
	tt.Frame()
	dataExpect(t, tt, "The downloadable error sample contains the first 2 invalid rows; 5 additional invalid-row details were omitted",
		"Download error sample…", "Skipped comment and blank lines",
		"3 rows use a positive-integer legacy column 16. They are interpreted as legacy cluster counts; the input has no column 17 type for those rows.",
		"Legacy column 16 detected. 3 16-column rows contain a positive integer in column 16 without a chimera-type column. "+
			"They are treated as legacy cluster support, following the bundled HYB2 merge format. If these rows are instead truncated "+
			"standard 17-column records, add column 17 before analysis.")
	if tt.HasText("Skipped blank lines") || tt.HasText("Standard 17-column records") {
		t.Fatalf("split counts of an old index: %q", tt.Texts())
	}
	// rows of one sample: exactly the web's three columns
	if got := string(ovValidationCSV(nil, nil)); got != `"line","error","content"` {
		t.Fatalf("empty report %q", got)
	}
	one := []ovError{{Line: 3, Error: "bad", Content: "x", SampleID: "SUB_REP1"}, {Line: 5, Error: "bad", Content: "y", SampleID: "SUB_REP1"}}
	if got := string(ovValidationCSV(one, []string{"SUB_REP1", "SUB_REP1"})); got != `"line","error","content"`+"\n"+`"3","bad","x"`+"\n"+`"5","bad","y"` {
		t.Fatalf("one sample's report %q", got)
	}
}

// One sample selected: the web's single-file wording (its file name in
// the headings, "in this file").
func TestQCSingleFileWording(t *testing.T) {
	a, _ := qcOpen(t, nil, true)
	a.Selected.Key = "s:SUB_REP1"
	a.Navigate("qc", nil)
	tt := ui.NewTester(a.View, 1100, 2800)
	for i := 0; i < 5 && !tt.HasText("ZAFR_REP1_sub.fastq is ready"); i++ {
		waitFlush(a)
		tt.Frame()
	}
	dataExpect(t, tt, "Overview", "ZAFR_REP1_sub.fastq · loaded locally · 44,832 valid records", "ZAFR_REP1_sub.fastq is ready")
	qcFor(a).ov.data.WithDG = 0
	tt.Frame()
	dataExpect(t, tt, "Not provided", "numeric dG values in this file")
	if got := ovHeading(a, []string{"SUB_REP1", "SUB_REP2"}, nil); got != "2 datasets · loaded locally" {
		t.Fatal(got)
	}
	if got := ovReadyHeading(a, []string{"SUB_REP1", "SUB_REP2"}); got != "2 datasets are ready (counts added up)" {
		t.Fatal(got)
	}
}

// The reference callout: samples without a reference in a project with
// references are told to set one; a failed matching shows its error and
// Retry.
func TestQCReferenceCallout(t *testing.T) {
	a, f := qcOpen(t, nil, true)
	f.queries["analysis_reference_matching"] = map[string]any{"references": []any{}, "rna_names": 2, "matched": 0, "sequences": 0,
		"unreferenced_sample_ids": []any{"SUB_REP1", "SUB_REP2"}, "rna_name_list": []any{}}
	a.Navigate("qc", nil)
	tt := ui.NewTester(a.View, 1100, 1500)
	for i := 0; i < 5 && !tt.HasText("Reference sequences are not set for these samples"); i++ {
		waitFlush(a)
		tt.Frame()
	}
	dataExpect(t, tt, "Reference sequences are not set for these samples", "Set reference on Data")
	if tt.HasText("Add reference FASTA") {
		t.Fatalf("offers a FASTA though the project has references: %q", tt.Texts())
	}

	q := qcFor(a)
	delete(f.queries, "analysis_reference_matching")
	q.match.invalidate()
	q.match.stale = nil
	for i := 0; i < 5 && !tt.HasText("Name matching unavailable: no fixture for analysis_reference_matching"); i++ {
		waitFlush(a)
		tt.Frame()
	}
	dataExpect(t, tt, "Name matching unavailable: no fixture for analysis_reference_matching", "Retry")
	f.queries["analysis_reference_matching"] = qcMatchingFixture()
	if err := tt.Click("Retry name matching"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5 && !tt.HasText("Reference sequences loaded"); i++ {
		waitFlush(a)
		tt.Frame()
	}
	dataExpect(t, tt, "Reference sequences loaded")
}
