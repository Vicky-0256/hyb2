package views

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// dataCall is one ProjectOp the page made.
type dataCall struct {
	op      string
	payload core.Obj
}

// dataFake records ProjectOp payloads; sheetErrors makes a sheet's dry run
// fail as the bridge does for a bad sample sheet.
type dataFake struct {
	*fakeBackend
	opCalls     []dataCall
	sheetErrors map[string]string
	// updateErrors makes update_sample fail for these sample ids.
	updateErrors map[string]string
}

func (f *dataFake) ProjectOp(path, operation string, payload core.Obj) (core.Obj, error) {
	f.mu.Lock()
	f.opCalls = append(f.opCalls, dataCall{operation, payload})
	f.mu.Unlock()
	if operation == "import_sample_sheet" {
		if message := f.sheetErrors[fmt.Sprint(payload["sheet"])]; message != "" {
			return nil, errors.New(message)
		}
	}
	if operation == "update_sample" {
		if message := f.updateErrors[fmt.Sprint(payload["sample_id"])]; message != "" {
			return nil, errors.New(message)
		}
	}
	return f.fakeBackend.ProjectOp(path, operation, payload)
}

func (f *dataFake) callsOf(op string) []core.Obj {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.Obj
	for _, call := range f.opCalls {
		if call.op == op {
			out = append(out, call.payload)
		}
	}
	return out
}

// dataOpen opens the fixture project with list (a testdata file) as
// list_samples' answer and the real analysis_summary for its ready samples.
func dataOpen(t *testing.T, list string) (*App, *dataFake) {
	t.Helper()
	f := newFake(t)
	f.ops["list_samples"] = fixture(t, list)
	f.ops["disk_estimate"] = fixture(t, "data_disk_estimate.json")
	f.ops["check_inputs"] = fixture(t, "data_check_inputs.json")
	f.ops["import_sample_sheet"] = fixture(t, "data_sheet_preview.json")
	for _, op := range []string{"remove_sample", "update_sample", "add_sample", "update_reference", "add_reference"} {
		f.ops[op] = core.Obj{"ok": true}
	}
	summary := fixture(t, "qc_analysis_summary.json")["result"].(map[string]any)
	if list == "data_list_samples_mixed.json" {
		// SUB_REP1 and SUB_REP2 are not analysed in this variant.
		indexes := summary["indexes"].([]any)
		summary = map[string]any{"indexes": indexes[2:]}
	}
	f.queries["analysis_summary"] = summary
	df := &dataFake{fakeBackend: f, sheetErrors: map[string]string{}, updateErrors: map[string]string{}}
	a := New(df, nil, "")
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	if a.Project == nil {
		t.Fatalf("project did not open: %q", a.start.status)
	}
	a.Navigate("data", nil)
	waitFlush(a)
	return a, df
}

// dataPick makes the file dialog answer paths.
func dataPick(t *testing.T, paths ...string) {
	old := PickFiles
	PickFiles = func(string, bool) []string { return paths }
	t.Cleanup(func() { PickFiles = old })
}

func dataExpect(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !tt.HasText(text) {
			t.Fatalf("page lacks %q: %q", text, tt.Texts())
		}
	}
}

func dataClick(t *testing.T, tt *ui.Tester, a *App, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	tt.Frame()
}

func TestDataPageSamplesAndRun(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Samples (6)", "References", "ZAFR", "ZIKV_African_KX377335-1_virusRNA · 10,807 nt",
		"SUB_REP1", "ZBRA_REP2", "ZAFR_REP1_sub.fastq", "fastq · 29.1 MB · linked", "map done", "hyb done", "index done",
		"map —", "Run all pending (0)", "224 k", "757", "Explore")
	snapshot(t, tt, "data")
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "data-dark")
	tt.SetDark(false)

	// Select two samples and run them: the disk estimate comes first.
	dataClick(t, tt, a, "Select SUB_REP1")
	dataClick(t, tt, a, "Select SUB_REP2")
	dataExpect(t, tt, "Run selected (2)", "Remove selected")
	dataClick(t, tt, a, "Run selected (2)")
	dataExpect(t, tt, "Run 2 samples", "SUB_REP1, SUB_REP2", "Input", "57.1 MB", "Temporary peak", "34.9 MB",
		"Results", "1.7 MB", "36.6 MB needed for outputs+temp on /projects/zikv.hyb2proj, 220 GB free.", "Start")
	snapshot(t, tt, "data-run")
	estimates := f.callsOf("disk_estimate")
	if len(estimates) != 1 || fmt.Sprint(estimates[0]["sample_ids"]) != "[SUB_REP1 SUB_REP2]" || estimates[0]["include_completed"] != true {
		t.Fatalf("disk_estimate %v", estimates)
	}
	if len(f.started) != 0 {
		t.Fatalf("started before confirming: %+v", f.started)
	}
	dataClick(t, tt, a, "Start")
	if len(f.started) != 2 || f.started[0].Kind != "sample_pipeline" || *f.started[0].SampleID != "SUB_REP1" || *f.started[1].SampleID != "SUB_REP2" {
		t.Fatalf("started %+v", f.started)
	}
	if got := a.TaskLabel(a.Tasks["sample_pipeline-1"]); !strings.HasPrefix(got, "Pipeline · SUB_REP") {
		t.Fatalf("label %q", got)
	}
	// Live progress replaces the stage chips of the running samples.
	dataExpect(t, tt, "map · bowtie2 · 25%", "Cancel", "Run all pending (0)")
	if tt.HasText("Run selected") {
		t.Fatalf("selection not cleared: %q", tt.Texts())
	}
	snapshot(t, tt, "data-running")
}

func TestDataPageResumeAndPending(t *testing.T) {
	a, _ := dataOpen(t, "data_list_samples_mixed.json")
	tt := ui.NewTester(a.View, 1100, 720)
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Run all pending (2)", "Retry failed (1)", "hyb failed", "map pending", "Resume", "Run")
	snapshot(t, tt, "data-mixed")
	// The fake refuses to resume: the page says so.
	dataClick(t, tt, a, "Resume")
	tt.Frame()
	dataExpect(t, tt, "Could not resume SUB_REP2: not resumable")
}

// A sample of reads without a reference cannot run (the pipeline refuses
// it): the row says so and offers to set one, and Run leaves it out.
func TestDataPageMissingReference(t *testing.T) {
	a, f := dataOpen(t, "data_list_samples_mixed.json")
	f.ops["list_samples"]["samples"].([]any)[0].(map[string]any)["reference_id"] = nil // SUB_REP1
	a.RefreshProject()
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataExpect(t, tt, "No reference", "Set reference…", "Run all pending (2)")
	snapshot(t, tt, "data-no-reference")

	// Run all pending: only SUB_REP2 starts, and the dialog says why.
	dataClick(t, tt, a, "Run all pending (2)")
	dataExpect(t, tt, "Run 1 sample", "SUB_REP1 is left out: it has no reference. Use Set reference… in its row.")
	dataClick(t, tt, a, "Start")
	if len(f.started) != 1 || *f.started[0].SampleID != "SUB_REP2" {
		t.Fatalf("started %+v", f.started)
	}

	// Only SUB_REP1 selected: nothing starts.
	dataClick(t, tt, a, "Select SUB_REP1")
	dataClick(t, tt, a, "Run selected (1)")
	dataExpect(t, tt, "SUB_REP1 has no reference. Use Set reference… in its row first.")
	if len(f.started) != 1 || a.pages["data"].(*dataPage).run.open {
		t.Fatalf("started %+v, dialog open", f.started)
	}

	// Set reference… opens the sample's edit dialog.
	dataClick(t, tt, a, "Set reference…")
	dataExpect(t, tt, "Sample SUB_REP1")

	// Check inputs counts it too.
	checked := fixture(t, "data_check_inputs.json")
	checked["no_reference"] = []any{"SUB_REP1"}
	f.ops["check_inputs"] = checked
	a.pages["data"].(*dataPage).edit.open = false
	tt.Frame()
	dataClick(t, tt, a, "Check inputs")
	dataExpect(t, tt, "Inputs checked: 8 ok, 0 missing, 0 changed. 1 sample has no reference.")
}

// Samples imported without a reference get one in a single step, not one
// edit dialog per sample; a sample whose update fails stays listed.
func TestDataPageSetReferenceForMany(t *testing.T) {
	a, f := dataOpen(t, "data_list_samples_mixed.json")
	samples := f.ops["list_samples"]["samples"].([]any)
	samples[0].(map[string]any)["reference_id"] = nil // SUB_REP1
	samples[1].(map[string]any)["reference_id"] = nil // SUB_REP2
	a.RefreshProject()
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataClick(t, tt, a, "Set reference (2)…")
	dataExpect(t, tt, "Set the reference of 2 samples", "SUB_REP1, SUB_REP2")
	snapshot(t, tt, "data-set-reference")

	f.updateErrors["SUB_REP2"] = "manifest is locked"
	dataClick(t, tt, a, "Set reference")
	calls := f.callsOf("update_sample")
	if len(calls) != 2 {
		t.Fatalf("update_sample calls %+v", calls)
	}
	for i, id := range []string{"SUB_REP1", "SUB_REP2"} {
		changes := calls[i]["changes"].(core.Obj)
		if calls[i]["sample_id"] != id || changes["reference_id"] != "ZAFR" || len(changes) != 1 {
			t.Fatalf("call %d: %+v", i, calls[i])
		}
	}
	// The failure is shown and only the failed sample is left in the dialog.
	dataExpect(t, tt, "Set the reference of SUB_REP1. Could not set it for SUB_REP2: manifest is locked",
		"Set the reference of 1 sample")

	// Only samples that have none are offered: with a selection, its
	// samples without one.
	a.pages["data"].(*dataPage).setRef.open = false
	samples[0].(map[string]any)["reference_id"] = "ZAFR"
	a.RefreshProject()
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Set reference (1)…")
	dataClick(t, tt, a, "Select AFR_REP1")
	if tt.HasText("Set reference (1)…") {
		t.Fatalf("offered for a selection that has a reference: %q", tt.Texts())
	}
	dataClick(t, tt, a, "Select SUB_REP2")
	dataExpect(t, tt, "Set reference (1)…")
}

// Opening a project checks its input files quietly: a file that went
// missing since the last session is marked without clicking Check inputs
// (its Resume would only fail again), and all-fine says nothing.
func TestDataPageChecksInputsOnOpen(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	if n := len(f.callsOf("check_inputs")); n != 1 {
		t.Fatalf("check_inputs called %d times on open", n)
	}
	if tt.HasText("Inputs checked") || tt.HasText("present and unchanged") || tt.HasText("Relink…") {
		t.Fatalf("all-fine check is not quiet: %q", tt.Texts())
	}
	tt.Frame()
	if n := len(f.callsOf("check_inputs")); n != 1 {
		t.Fatalf("checked again on a later frame: %d", n)
	}

	// The same project with a moved file, opened again.
	checked := fixture(t, "data_check_inputs.json")
	checked["items"].([]any)[2].(map[string]any)["status"] = "missing"
	f.ops["check_inputs"] = checked
	a.Close()
	waitFlush(a)
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	a.Navigate("data", nil)
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Inputs checked: 7 ok, 1 missing, 0 changed.", "missing", "Relink…")
	if tt.HasText("Use Relink on the marked rows") {
		t.Fatalf("automatic check toasts: %q", tt.Texts())
	}

	// A check that cannot run says so; it is not an all-fine silence.
	delete(f.ops, "check_inputs")
	a.Close()
	waitFlush(a)
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	a.Navigate("data", nil)
	tt.Frame()
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Could not check the input files: unsupported project operation check_inputs")
}

func TestDataPageImportSheet(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	dataPick(t, "/data/zikv/samples.tsv")
	dataClick(t, tt, a, "Import sample sheet…")
	dataExpect(t, tt, "Import samples.tsv", "4 samples, 1 reference(s). Nothing has been imported yet.",
		"AFR_REP2", "ZAFR_REP1.hyb", "ZAFR.fasta", "Import 4 samples")
	snapshot(t, tt, "data-sheet")
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "data-sheet-dark")
	tt.SetDark(false)
	dataClick(t, tt, a, "Import 4 samples")
	calls := f.callsOf("import_sample_sheet")
	if len(calls) != 2 || calls[0]["dry_run"] != true || calls[1]["dry_run"] != nil || calls[1]["mode"] != "linked" ||
		calls[1]["sheet"] != "/data/zikv/samples.tsv" {
		t.Fatalf("import calls %v", calls)
	}
	tt.Frame()
	dataExpect(t, tt, "Imported 4 samples.")
	if tt.HasText("Nothing has been imported yet") {
		t.Fatal("preview still open")
	}

	// A sheet with errors shows them instead of a preview.
	f.sheetErrors["/data/bad.tsv"] = "row 3: file not found: /data/zikv/x.fastq; row 4: no read files"
	dataPick(t, "/data/bad.tsv")
	dataClick(t, tt, a, "Import sample sheet…")
	dataExpect(t, tt, "Sample sheet problems", "bad.tsv cannot be imported:", "row 3: file not found: /data/zikv/x.fastq", "row 4: no read files")
	snapshot(t, tt, "data-sheet-problems")
}

func TestDataPageRemoveConfirm(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataClick(t, tt, a, "Select AFR_REP2")
	dataClick(t, tt, a, "Remove selected")
	dataExpect(t, tt, "Remove samples", "Remove AFR_REP2 from the project?")
	snapshot(t, tt, "data-remove")
	if len(f.callsOf("remove_sample")) != 0 {
		t.Fatal("removed before confirming")
	}
	dataClick(t, tt, a, "Remove")
	calls := f.callsOf("remove_sample")
	if len(calls) != 1 || calls[0]["sample_id"] != "AFR_REP2" {
		t.Fatalf("remove calls %v", calls)
	}
	tt.Frame()
	dataExpect(t, tt, "Removed 1 sample.")
}

// A sample whose task is queued or running cannot be removed: its task
// would keep writing into a sample re-added under the same name.
func TestDataPageRemoveDisabledWhileRunning(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	waitFlush(a)
	tt.Frame()
	dataClick(t, tt, a, "Select SUB_REP1")
	dataClick(t, tt, a, "Run selected (1)")
	dataClick(t, tt, a, "Start")
	if len(f.started) != 1 {
		t.Fatalf("started %+v", f.started)
	}
	dataClick(t, tt, a, "Select SUB_REP1")
	dataClick(t, tt, a, "Select AFR_REP2")
	dataExpect(t, tt, "Remove selected")
	_ = tt.Click("Remove selected")
	waitFlush(a)
	tt.Frame()
	if tt.HasText("Remove samples") || len(f.callsOf("remove_sample")) != 0 {
		t.Fatal("remove offered for a running sample")
	}
	// Without the running sample in the selection it can be removed.
	dataClick(t, tt, a, "Select SUB_REP1")
	dataClick(t, tt, a, "Remove selected")
	dataExpect(t, tt, "Remove samples", "Remove AFR_REP2 from the project?")
}

func TestDataPageCheckInputsAndRelink(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataClick(t, tt, a, "Check inputs")
	dataExpect(t, tt, "Inputs checked: 8 ok, 0 missing, 0 changed.", "All 8 input files are present and unchanged.")
	if tt.HasText("Relink…") {
		t.Fatal("relink offered for ok inputs")
	}
	// One file moved: it is marked and can be relinked.
	checked := fixture(t, "data_check_inputs.json")
	items := checked["items"].([]any)
	items[2].(map[string]any)["status"] = "missing"
	f.ops["check_inputs"] = checked
	dataClick(t, tt, a, "Check inputs")
	tt.Frame()
	dataExpect(t, tt, "Inputs checked: 7 ok, 1 missing, 0 changed.", "missing", "Relink…")
	snapshot(t, tt, "data-missing")
	dataPick(t, "/data/moved/ZAFR_REP1_sub.fastq")
	dataClick(t, tt, a, "Relink…")
	calls := f.callsOf("update_sample")
	if len(calls) != 1 || calls[0]["sample_id"] != "SUB_REP1" ||
		calls[0]["changes"].(core.Obj)["reads_path"] != "/data/moved/ZAFR_REP1_sub.fastq" {
		t.Fatalf("relink %v", calls)
	}
}

func TestDataPageEditSample(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataClick(t, tt, a, "Edit sample SUB_REP1")
	dataExpect(t, tt, "Sample SUB_REP1", "Replicate", "Gene", "/data/zikv/ZAFR_REP1_sub.fastq")
	snapshot(t, tt, "data-edit")
	page := a.PageFor("data").(*dataPage)
	page.edit.replicate = "x"
	tt.Frame()
	dataClick(t, tt, a, "Save")
	dataExpect(t, tt, "Replicate must be a positive whole number.")
	page.edit.replicate = "3"
	page.edit.condition = "AFR"
	tt.Frame()
	dataClick(t, tt, a, "Save")
	calls := f.callsOf("update_sample")
	if len(calls) != 1 {
		t.Fatalf("update calls %v", calls)
	}
	changes := calls[0]["changes"].(core.Obj)
	if calls[0]["sample_id"] != "SUB_REP1" || changes["replicate"] != 3 || changes["condition"] != "AFR" ||
		changes["reference_id"] != "ZAFR" || changes["gene_id"] != "ZIKV_African_KX377335-1_virusRNA" || changes["name"] != "SUB_REP1" {
		t.Fatalf("changes %v", changes)
	}
	if page.edit.open {
		t.Fatal("dialog still open")
	}
}

// Two references with one label stay two choices, and saving a sample keeps
// its reference by id (it used to switch to the first "genome").
func TestDataPageDuplicateReferenceLabels(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	for i := range a.Project.References {
		a.Project.References[i].Label = "genome"
	}
	page := a.PageFor("data").(*dataPage)
	if got := strings.Join(page.refLabels(), "|"); got != "genome · ZAFR.fasta|genome · ZBRA.fasta" {
		t.Fatalf("labels %q", got)
	}
	if got := dataRefLabels([]model.Reference{{ID: "a", Label: "g", Path: "/x/r.fa"}, {ID: "b", Label: "g", Path: "/y/r.fa"}}); strings.Join(got, "|") != "g · r.fa|g · b" {
		t.Fatalf("same file names: %q", got)
	}
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataClick(t, tt, a, "Edit sample ZBRA_REP1")
	if page.edit.reference != "genome · ZBRA.fasta" || page.edit.refID != "ZBRA" {
		t.Fatalf("edit reference %q %q", page.edit.reference, page.edit.refID)
	}
	dataClick(t, tt, a, "Save")
	calls := f.callsOf("update_sample")
	if len(calls) != 1 || calls[0]["changes"].(core.Obj)["reference_id"] != "ZBRA" {
		t.Fatalf("update %v", calls)
	}
	// a label that stops naming a reference (renamed meanwhile) is shown
	// again from the id; "—" clears it
	page.edit = dataEditDialog{reference: "old name", refID: "ZBRA"}
	page.syncRef(&page.edit.reference, &page.edit.refID, "—")
	if page.edit.refID != "ZBRA" || page.edit.reference != dataRefLabel(a, "ZBRA") {
		t.Fatalf("resync %+v", page.edit)
	}
	page.edit.reference = "—"
	page.syncRef(&page.edit.reference, &page.edit.refID, "—")
	if page.edit.refID != "" {
		t.Fatalf("none kept %q", page.edit.refID)
	}
}

// Relinking a reference loads the name matching again (as Replace), the
// rename dialog sends one update however often Save is clicked, and an
// earlier Check inputs answer does not end a later one's busy state.
func TestDataPageRelinkRenameAndChecks(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	page := a.PageFor("data").(*dataPage)
	q := qcFor(a)
	seq := q.match.seq
	dataPick(t, "/data/moved/ZAFR.fasta")
	page.relink(dataInputItem{Kind: "reference", ID: "ZAFR", Path: "/data/zikv/ZAFR.fasta"})
	waitFlush(a)
	if q.match.seq == seq {
		t.Fatal("relinking a reference kept the name matching")
	}
	if calls := f.callsOf("update_reference"); len(calls) != 1 || calls[0]["source"] != "/data/moved/ZAFR.fasta" {
		t.Fatalf("relink %v", calls)
	}

	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	page.rename = dataRenameDialog{open: true, id: "ZAFR", label: "Africa"}
	page.rename.busy = true // an update in flight
	tt.Frame()
	_ = tt.Click("Save")
	waitFlush(a)
	if calls := f.callsOf("update_reference"); len(calls) != 1 {
		t.Fatalf("busy rename sent another update: %v", calls)
	}
	page.rename.busy = false
	tt.Frame()
	dataClick(t, tt, a, "Save")
	if calls := f.callsOf("update_reference"); len(calls) != 2 || calls[1]["label"] != "Africa" || page.rename.busy {
		t.Fatalf("rename %v busy %v", calls, page.rename.busy)
	}

	page.runCheck(false)
	page.runCheck(false)
	if page.checking != 2 {
		t.Fatalf("checking %d", page.checking)
	}
	waitFlush(a)
	if page.checking != 0 {
		t.Fatalf("checking %d after both answers", page.checking)
	}
}

func TestDataPageImportRawFiles(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataPick(t, "/data/zikv/new_REP1.fastq.gz", "/data/zikv/new_REP2.fq")
	dataClick(t, tt, a, "Import raw files…")
	dataExpect(t, tt, "Import 2 files", "Reference", "First replicate", "/data/zikv/new_REP1.fastq.gz")
	page := a.PageFor("data").(*dataPage)
	if strings.Join(page.add.names, ",") != "new_REP1,new_REP2" || page.add.reference != "ZAFR" {
		t.Fatalf("add dialog %+v", page.add)
	}
	snapshot(t, tt, "data-add")
	page.add.condition = "SUB"
	tt.Frame()
	dataClick(t, tt, a, "Import")
	calls := f.callsOf("add_sample")
	if len(calls) != 2 {
		t.Fatalf("add calls %v", calls)
	}
	// SUB has replicates 1 and 2: the new files continue the numbering.
	for i, call := range calls {
		if call["replicate"] != 3+i || call["condition"] != "SUB" || call["reference_id"] != "ZAFR" || call["mode"] != "linked" ||
			call["gene_id"] != nil || call["name"] != fmt.Sprintf("new_REP%d", i+1) {
			t.Fatalf("add call %d: %v", i, call)
		}
	}
	tt.Frame()
	dataExpect(t, tt, "Added 2 samples.")
}

func TestDataFormatting(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 30519358: "29.1 MB", 235819716608: "220 GB"} {
		if got := dataFormatBytes(n); got != want {
			t.Errorf("bytes %d: %q, want %q", n, got, want)
		}
	}
	for n, want := range map[int64]string{757: "757", 9999: "9,999", 224154: "224 k", 1302094: "1.3 M", 12_000_000: "12 M",
		// a count that rounds into the next unit shows in it
		999_499: "999 k", 999_600: "1.0 M", 9_940_000: "9.9 M", 9_960_000: "10 M"} {
		if got := dataFormatCompact(n); got != want {
			t.Errorf("compact %d: %q, want %q", n, got, want)
		}
	}
}

func TestDataPageImportFolder(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	f.ops["scan_data_folder"] = fixture(t, "data_folder_scan.json")
	f.ops["import_sample_rows"] = core.Obj{"ok": true, "samples": []any{1, 2}, "skipped": []any{}}
	old := PickFolder
	PickFolder = func(string) string { return "/data/zika_files" }
	t.Cleanup(func() { PickFolder = old })
	tt := ui.NewTester(a.View, 1280, 820)
	dataClick(t, tt, a, "Import data folder…")
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Import a data folder", "/data/zika_files",
		"Proposed from the file names: 14 samples in 6 conditions, using 5 references. Nothing is imported until you click Import.",
		"Not imported: 9 uncompressed copies of .gz files (the .gz is used); 5 .tab files; 30 Bowtie2 index files (HYB2 builds its own); 1 named FASTA but not FASTA content.",
		"ZAFR_REP1.fastq.gz", "1.6 GB", "ZAFR.fasta", "⚠ reference guessed (no file name matched); check it", "Import 14 samples")
	snapshot(t, tt, "data-folder")
	if calls := f.callsOf("scan_data_folder"); len(calls) != 1 || calls[0]["folder"] != "/data/zika_files" {
		t.Fatalf("scan calls %v", calls)
	}
	// Edit: Aag is the insect condition, drop the second Aag file.
	d := &a.pages["data"].(*dataPage).folder
	d.rows[0].condition = "insect"
	d.rows[1].include = false
	tt.Frame()
	dataExpect(t, tt, "Import 13 samples")
	// A row without a reference may be imported, with a warning.
	saved := d.rows[2].reference
	d.rows[2].reference = dataNoReference
	tt.Frame()
	dataExpect(t, tt, "1 sample has no reference: it can be imported but not run until you set one.", "Import 13 samples")
	d.rows[2].reference = saved
	tt.Frame()
	if tt.HasText("has no reference") {
		t.Fatal("warning stays after the reference is set")
	}
	dataClick(t, tt, a, "Import 13 samples")
	calls := f.callsOf("import_sample_rows")
	if len(calls) != 1 || calls[0]["mode"] != "linked" {
		t.Fatalf("import calls %v", calls)
	}
	rows := calls[0]["rows"].([]core.Obj)
	if len(rows) != 13 || rows[0]["condition"] != "insect" || rows[0]["reference"] != "/data/zika_files/Zika.fasta" ||
		rows[0]["replicate"] != 1 || rows[0]["name"] != "Aag-1" {
		t.Fatalf("rows %v", rows[:2])
	}
	last := rows[12]
	if last["name"] != "ZSIN_REP3" || last["condition"] != "ZSIN" || last["replicate"] != 3 || last["reference"] != "/data/zika_files/ZSIN.fasta" {
		t.Fatalf("last row %v", last)
	}
	tt.Frame()
	dataExpect(t, tt, "Imported 2 samples. Next: Run all pending.")
}

// A .hyb or .hyb.gz is already mapped: it needs no reference.
func TestDataFolderHybNeedsNoReference(t *testing.T) {
	d := dataFolderDialog{rows: []dataFolderRow{
		{include: true, path: "/d/A_REP1.hyb", reference: dataNoReference},
		{include: true, path: "/d/A_REP2.HYB.GZ", reference: dataNoReference},
		{include: true, path: "/d/A_REP3.fastq.gz", reference: dataNoReference},
	}}
	if n := d.withoutReference(); n != 1 {
		t.Fatalf("%d rows without reference", n)
	}
}

// An empty project offers the example data set (add_example_data).
func TestDataPageAddsExample(t *testing.T) {
	f := newFake(t)
	f.ops["list_samples"] = core.Obj{"ok": true, "path": "/projects/zikv.hyb2proj", "samples": []any{}, "conditions": []any{},
		"references": []any{}, "normalized": []any{}}
	f.ops["add_example_data"] = core.Obj{"ok": true, "samples": []any{1, 2, 3, 4}}
	a := New(f, nil, "")
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	a.Navigate("data", nil)
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 900)
	dataExpect(t, tt, "No samples yet.", "No data yet? Try the ZIKV virion example, ZAFR vs ZASI.")
	if err := tt.Click("Try example"); err != nil {
		t.Fatal(err)
	}
	waitFlush(a)
	tt.Frame()
	if !strings.Contains(strings.Join(f.calls, ","), "op:add_example_data") {
		t.Fatalf("calls %v", f.calls)
	}
	dataExpect(t, tt, "Example added: 4 samples. Run all pending indexes them.")
}

// TestDataPageSampleProgress: a running sample's bar covers the whole
// pipeline, so it moves while mapping reports no bytes, with the time left.
func TestDataPageSampleProgress(t *testing.T) {
	a, f := dataOpen(t, "data_list_samples_mixed.json")
	f.mu.Lock()
	f.tasks = append(f.tasks, core.Obj{"task_id": "sample-pipeline-1791990260455", "kind": "sample_pipeline",
		"status": "running", "started_at": "2026-10-14T15:00:00Z", "fraction_done": 0.4, "seconds_left": 125.0,
		"progress": map[string]any{"stage": "map", "detail": ""}})
	f.mu.Unlock()
	a.RefreshTasks()
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	dataExpect(t, tt, "40% done · about 2 min 5 s left · map")
	snapshot(t, tt, "data-sample-progress")

	// Past its estimate: no time left, the bar keeps its share.
	f.mu.Lock()
	task := f.tasks[len(f.tasks)-1]
	task["fraction_done"], task["over_estimate"] = 0.74, true
	delete(task, "seconds_left")
	progress := task["progress"].(map[string]any)
	progress["updated_at"] = time.Now().Add(-150 * time.Second).UTC().Format(time.RFC3339)
	f.mu.Unlock()
	a.RefreshTasks()
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "74% done · map 2 min 30 s so far, longer than estimated")
	snapshot(t, tt, "data-sample-over-estimate")
}
