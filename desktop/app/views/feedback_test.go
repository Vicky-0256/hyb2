package views

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

const pyTraceback = `compute sidecar exited unexpectedly: Traceback (most recent call last):
  File "/app/desktop/runtime/bridge.py", line 812, in serve
    response = handle(request)
  File "/app/pipeline/hyb2analysis/query.py", line 77, in summary
    raise ValueError("index of SUB_REP1 is unreadable")
ValueError: index of SUB_REP1 is unreadable`

func TestShortError(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"boom":                   "boom",
		"first line\nsecond":     "first line",
		"\n  \nlate start\nmore": "late start",
		pyTraceback:              "compute sidecar exited unexpectedly: ValueError: index of SUB_REP1 is unreadable",
		strings.Repeat("x", 300): strings.Repeat("x", 160) + "…",
	} {
		if got := ShortError(in); got != want {
			t.Errorf("ShortError(%.40q) = %q, want %q", in, got, want)
		}
	}
}

// TestToastAsReplacesItsEarlierMessage: a toast with an id replaces the
// queued one of the same id (the outcome of the same action, redone).
func TestToastAsReplacesItsEarlierMessage(t *testing.T) {
	a := &App{}
	a.ToastAs("check", "All %d input files are present and unchanged.", 8)
	a.Toast("Saved x")
	a.ToastAs("check", "%d missing, %d changed.", 1, 0)
	var texts []string
	for _, m := range a.toasts {
		texts = append(texts, m.Text)
	}
	if strings.Join(texts, "|") != "Saved x|1 missing, 0 changed." {
		t.Fatalf("toasts %q", texts)
	}
}

// TestFailedTaskToastSaysWhy: the finish toast of a failed task carries the
// first line of its error.
func TestFailedTaskToastSaysWhy(t *testing.T) {
	a := &App{Tasks: map[string]model.Task{}}
	a.storeTask(model.Task{TaskID: "t1", Kind: "validate_project", Status: "running"})
	a.storeTask(model.Task{TaskID: "t1", Kind: "validate_project", Status: "failed", Error: pyTraceback})
	last := a.toasts[len(a.toasts)-1].Text
	if !strings.HasSuffix(last, " failed: compute sidecar exited unexpectedly: ValueError: index of SUB_REP1 is unreadable") {
		t.Fatalf("toast %q", last)
	}
}

// TestDataPageFailedStageSaysWhy: a failed stage shows its task's reason on
// the row, and the toolbar button names failed samples as such.
func TestDataPageFailedStageSaysWhy(t *testing.T) {
	a, f := dataOpen(t, "data_list_samples_mixed.json")
	f.mu.Lock()
	f.tasks = append(f.tasks, core.Obj{"task_id": "sample-pipeline-1791990260455", "kind": "sample_pipeline",
		"status": "failed", "error": "Traceback (most recent call last):\n  File \"x.py\"\nRuntimeError: bowtie2 index is missing",
		"started_at": "2026-10-14T15:00:00Z"})
	f.mu.Unlock()
	a.RefreshTasks()
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataExpect(t, tt, "Retry failed (1)", "hyb failed", "RuntimeError: bowtie2 index is missing")
	snapshot(t, tt, "data-failed-reason")
	if tt.HasText("Resume interrupted (1)") {
		t.Fatal("failed sample called interrupted")
	}
}

// TestQCErrorShowsOneLine: a traceback does not reach the page.
func TestQCErrorShowsOneLine(t *testing.T) {
	a, f := qcOpen(t, nil, false)
	a.Navigate("qc", nil)
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 720)
	waitFlush(a)
	tt.Frame()
	f.mu.Lock()
	f.queryErr = errors.New(pyTraceback)
	f.mu.Unlock()
	dataClick(t, tt, a, "Retry")
	dataExpect(t, tt, "Could not load the counts: compute sidecar exited unexpectedly: ValueError: index of SUB_REP1 is unreadable", "Retry")
	for _, text := range tt.Texts() {
		if strings.Contains(text, "Traceback") {
			t.Fatalf("traceback on the page: %q", text)
		}
	}
}

// TestCompareReferenceNotice: A and B on different references get a notice
// that positions are compared 1:1; same reference, none.
func TestCompareReferenceNotice(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("compare", nil)
	csSettle(a, tt)
	p := a.PageFor("compare").(*comparePage)
	notice := "A is mapped to ZAFR and B to ZBRA: positions are compared 1:1, which assumes the references are aligned position by position."
	if tt.HasText(notice) {
		t.Fatalf("notice for %s vs %s", p.keyA, p.keyB)
	}
	p.keyA, p.keyB = "c:AFR", "c:BRA"
	tt.Frame()
	dataExpect(t, tt, notice)
	snapshot(t, tt, "compare-reference-notice")
}

// TestCompareListErrorIsNotAnEmptyList: a failed listing offers Retry and
// does not claim there are no tests.
func TestCompareListErrorIsNotAnEmptyList(t *testing.T) {
	a, f := openCS(t)
	delete(f.ops, "list_comparisons")
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("compare", nil)
	csSettle(a, tt)
	dataExpect(t, tt, "Retry")
	if tt.HasText("No differential tests yet. Pick A and B, then Run differential test.") {
		t.Fatalf("empty state with a list error: %q", tt.Texts())
	}
	f.ops["list_comparisons"] = fixture(t, "compare_list.json")
	dataClick(t, tt, a, "Retry")
	csSettle(a, tt)
	dataExpect(t, tt, "AFR (2 merged) vs BRA (2 merged)")
}

// TestQCTypeColumnsAreTheLargest: the four type columns are the most
// common types over the samples, largest first, not the first ones met.
func TestQCTypeColumnsAreTheLargest(t *testing.T) {
	p := &qcPage{stats: map[string]qcStats{
		"s1": {Types: []qcCount{{"Type 9", 1}, {"Type 8", 2}, {"Type 7", 3}, {"Type 6", 4}, {"Type 2", 50}}},
		"s2": {Types: []qcCount{{"Type 1", 40}, {"Type 2", 60}, {"Type 13", 9}}},
	}}
	if got := strings.Join(p.qcTypeNames([]string{"s1", "s2"}), ","); got != "Type 2,Type 1,Type 13,Type 6" {
		t.Fatalf("columns %s", got)
	}
}

// gatedResearch holds DescribeRNA until gate is closed.
type gatedResearch struct {
	*researchFake
	gate chan struct{}
}

func (g *gatedResearch) DescribeRNA(path, id, taxon string, confirmed, force bool) (model.EvidenceCard, error) {
	<-g.gate
	return rsCard(id, taxon, model.EvidenceSuccess, rsSummary), nil
}

// TestLookupDoesNotCrossAResultSwitch: a summary asked for while one
// structure was shown does not appear (and cannot be saved) under the next.
func TestLookupDoesNotCrossAResultSwitch(t *testing.T) {
	a, f, tt, p := openResearch(t, true)
	g := &gatedResearch{researchFake: f, gate: make(chan struct{})}
	a.B = g
	r := p.research
	r.identifier, r.taxon, r.confirmed = "hsa-mir-21", "9606", true
	tt.Frame()
	mustClick(t, tt, "View existing summary")
	tt.Frame()
	a.Navigate("structure", map[string]any{"structureId": structB})
	csSettle(a, tt)
	close(g.gate)
	rsSettle(a, tt)
	if tgt := p.researchTarget(); tgt == nil || tgt.StructureID != structB {
		t.Fatalf("target %+v", tgt)
	}
	if r.card != nil || r.running != "" {
		t.Fatalf("lookup of the previous result shown: card %v running %q", r.card != nil, r.running)
	}
	if tt.HasText("Save to project") {
		t.Fatal("the previous result's card can be saved here")
	}
}

// TestMapSpeciesMismatchIsShown: the ID mapping's species warning (kept
// as status_detail of a success card) is shown on the card and in the
// report, and a mapping is not labelled a summary.
func TestMapSpeciesMismatchIsShown(t *testing.T) {
	a, _, tt, p := openResearch(t, true)
	card := rsCard("hsa-mir-21", "10090", model.EvidenceSuccess, "URS000075A685_9606 hsa-mir-21")
	card.SourceTool = core.ToolMap
	detail := "returned taxon 9606 differs from requested 10090"
	card.StatusDetail = &detail
	p.research.card = &card
	tt.Frame()
	dataExpect(t, tt, "Mapping found", "Detail: "+detail)
	if tt.HasText("Summary found") {
		t.Fatal("a mapping labelled as a summary")
	}
	_ = a
	if e := newReportEvidence(&card, nil); !strings.Contains(e.Status, detail) {
		t.Fatalf("report status %q", e.Status)
	}
}
