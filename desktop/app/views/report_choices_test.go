package views

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// openChosenReport opens the report with a purpose, S12 left out and
// tables/samples.tsv left out of the AI package.
func openChosenReport(t *testing.T) (*App, *reportPage, *ui.Tester) {
	t.Helper()
	a, f := openReport(t)
	f.queries["analysis_overview"] = fixture(t, "qc_analysis_overview.json")["result"]
	tt := ui.NewTester(a.View, 1100, 6000) // tall enough for the package list
	a.Navigate("report", nil)
	csSettle(a, tt)
	p := a.PageFor("report").(*reportPage)
	if len(p.steps) != 13 {
		t.Fatalf("%d steps", len(p.steps))
	}
	for _, text := range []string{"Steps, scope and purpose", "S12 RNA structure prediction", "Result packages", "Save PDF…", "Copy AI summary (report.md)"} {
		if !tt.HasText(text) {
			t.Fatalf("report lacks %q: %q", text, tt.Texts())
		}
	}
	p.choice.purpose = "  Which ZIKV contacts differ between lineages?  "
	*p.choice.keepBox("structure") = false
	*p.choice.includeBox("ai", "tables/samples.tsv") = false
	csSettle(a, tt)
	for _, text := range []string{"Selected results report: S12 left out.", "Include every step", "left out", reportLeftNote,
		"Purpose: Which ZIKV contacts differ between lineages?", "tables/samples.tsv"} {
		if !tt.HasText(text) {
			t.Fatalf("chosen report lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "report-choices")
	return a, p, tt
}

func TestReportSelectedResultsPackage(t *testing.T) {
	a, _, tt := openChosenReport(t)
	asked := pickSaveInto(t, t.TempDir())
	if err := tt.Click("For AI (ZIP)…"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	files := unzipExport(t, (*asked)[0])
	var summary struct {
		Scope struct {
			Kind, Label   string
			OmittedSteps  []string `json:"omittedSteps"`
			SelectedSteps []string `json:"selectedSteps"`
			Purpose       struct{ Text, Source string }
		} `json:"scope"`
		Steps   []struct{ ID string }   `json:"steps"`
		Results []struct{ Step string } `json:"results"`
	}
	if err := json.Unmarshal([]byte(files["summary.json"]), &summary); err != nil {
		t.Fatal(err)
	}
	sc := summary.Scope
	if sc.Kind != "selected_results" || sc.Label != "Selected results report" || strings.Join(sc.OmittedSteps, ",") != "S12" ||
		len(sc.SelectedSteps) != 12 || sc.Purpose.Text != "Which ZIKV contacts differ between lineages?" || sc.Purpose.Source != "researcher" {
		t.Fatalf("scope %+v", sc)
	}
	if len(summary.Steps) != 12 {
		t.Fatalf("%d steps", len(summary.Steps))
	}
	for _, r := range summary.Results {
		if r.Step == "S12" {
			t.Fatal("a result of the left-out step")
		}
	}
	for _, path := range []string{"results/structure_prediction.dbn", "results/structure_prediction.json", "tables/structures.tsv", "tables/samples.tsv"} {
		if _, ok := files[path]; ok {
			t.Fatalf("package has %s", path)
		}
	}
	if !strings.Contains(files["report.md"], "- Purpose: Which ZIKV contacts differ between lineages? (written by the researcher)") {
		t.Fatalf("report.md %.600s", files["report.md"])
	}
	readme := files["README.md"]
	if !strings.Contains(readme, "**This is a selected-results package, not the whole project.**") ||
		!strings.Contains(readme, "## Left out by the researcher\n\n- `tables/samples.tsv`") {
		t.Fatalf("README %s", readme)
	}
	if strings.Contains(files["data_dictionary.json"], "tables/samples.tsv") {
		t.Fatal("the dictionary describes a left-out table")
	}
	var manifest struct {
		Left  []string                `json:"leftOutByResearcher"`
		Files []struct{ Path string } `json:"files"`
	}
	if err := json.Unmarshal([]byte(files["manifest.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	if strings.Join(manifest.Left, ",") != "tables/samples.tsv" || len(manifest.Files) != len(files)-1 {
		t.Fatalf("manifest left %v, %d of %d files", manifest.Left, len(manifest.Files), len(files)-1)
	}
	// the analysis package keeps its own (here untouched) file list
	if err := tt.Click("For analysis (ZIP)…"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	analysis := unzipExport(t, (*asked)[1])
	if _, ok := analysis["tables/samples.tsv"]; !ok {
		t.Fatal("the analysis package lost tables/samples.tsv")
	}

	// Copy AI summary: report.md with the same choices
	if err := tt.Click("Copy AI summary (report.md)"); err != nil {
		t.Fatal(err)
	}
	if clip := tt.Clipboard(); !strings.HasPrefix(clip, "# HYB2 analysis report") || !strings.Contains(clip, "Selected results report") ||
		strings.Contains(clip, "| S12 |") {
		t.Fatalf("clipboard %.400s", clip)
	}

	// Include every step: back to the whole project
	if err := tt.Click("Include every step"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Selected results report: S12 left out.") || tt.HasText(reportLeftNote) {
		t.Fatalf("still selected: %q", tt.Texts())
	}
}

func TestReportHTMLAndPDFWithChoices(t *testing.T) {
	a, _, tt := openChosenReport(t)
	asked := pickSaveInto(t, t.TempDir())
	if err := tt.Click("Export report…"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	html := readExport(t, asked)
	for _, want := range []string{`<p class="purpose"><strong>Purpose:</strong> Which ZIKV contacts differ between lineages?`,
		"Selected results report: S12 RNA structure prediction left out.", reportLeftNote, "AFR (2 merged) vs BRA (2 merged)"} {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML lacks %q", want)
		}
	}
	if strings.Contains(html, "Arc diagram of 1,055 base pairs") {
		t.Fatal("the left-out structure is in the HTML")
	}

	// Save PDF prints that HTML
	var printed string
	previous := reportPDF
	reportPDF = func(html []byte) ([]byte, error) { printed = string(html); return []byte("%PDF-1.7 test"), nil }
	t.Cleanup(func() { reportPDF = previous })
	if err := tt.Click("Save PDF…"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	if got := readExport(t, asked); got != "%PDF-1.7 test" || !strings.HasSuffix((*asked)[1], "-report.pdf") {
		t.Fatalf("saved %q as %q", got, *asked)
	}
	if !strings.Contains(printed, "@media print") || !strings.Contains(printed, reportLeftNote) {
		t.Fatal("the PDF was not printed from the chosen HTML report")
	}
}
