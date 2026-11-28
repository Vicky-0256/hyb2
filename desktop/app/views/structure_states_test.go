package views

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// stLongGene is a reference sequence name as long as some annotations make.
const stLongGene = "ENSG00000223972.5_DDX11L1_transcribed_unprocessed_pseudogene_chr1_11869-14409_plus_strand_isoform_long_name_RNA"

// stStatesFixture opens the Structure page with synthetic content: a long
// RNA name next to the real ones and many results.
func stStatesFixture(t *testing.T, results int) (*App, *csFake, *ui.Tester) {
	t.Helper()
	a, f := openCS(t)
	ref := &a.Project.References[0]
	ref.Sequences = append(ref.Sequences, model.Sequence{Name: stLongGene, Length: 2534})
	list := fixture(t, "structure_list.json")
	base := list["structures"].([]any)[0].(map[string]any)
	items := []any{}
	for i := 0; i < results; i++ {
		e := map[string]any{}
		for k, v := range base {
			e[k] = v
		}
		e["id"], e["task_id"] = fmt.Sprintf("structure-%03d", i), fmt.Sprintf("structure-%03d", i)
		e["created_at"] = fmt.Sprintf("2026-10-%02dT10:00:00Z", 15-i%14)
		if i%3 == 1 {
			e["gene_id"], e["mode"], e["windows"] = stLongGene, "genome", []any{}
			e["summary"] = map[string]any{"pairs": 712, "pseudoknot_pairs": 0, "energy": -811.4, "length": 2534}
		}
		if i == 2 {
			e["available"] = false
		}
		items = append(items, e)
	}
	list["structures"] = items
	f.ops["list_structures"] = list
	tt := ui.NewTester(a.View, 1100, 1100)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	return a, f, tt
}

// stTaskState gives the fold started from the form the status.
func stTaskState(a *App, tt *ui.Tester, task model.Task) {
	task.TaskID, task.Kind = "structure-1", "structure"
	if previous, ok := a.Tasks[task.TaskID]; ok && taskStage(task.Status) < taskStage(previous.Status) {
		delete(a.Tasks, task.TaskID) // a new fold under the same id (storeTask keeps the later state)
	}
	a.storeTask(task)
	csSettle(a, tt)
}

// TestStructureStateSnapshots renders each state of the New fold task. It
// asserts nothing so it also runs against earlier versions of the page for
// before/after comparisons (HYB2_SNAPSHOT_DIR).
func TestStructureStateSnapshots(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	f64 := func(v float64) *float64 { return &v }

	a, _, tt := stStatesFixture(t, 30)
	p := a.PageFor("structure").(*structurePage)
	p.gene = stLongGene
	csSettle(a, tt)
	snapshot(t, tt, "states-long-many")

	a, f, tt := stStatesFixture(t, 0)
	f.ops["list_structures"] = core.Obj{"structures": []any{}}
	a.PageFor("structure").(*structurePage).loadList()
	csSettle(a, tt)
	snapshot(t, tt, "states-empty")

	p = a.PageFor("structure").(*structurePage)
	p.structures, p.loading = nil, true
	tt.Frame()
	snapshot(t, tt, "states-loading")
	p.loading = false

	for i := range a.Project.Samples {
		a.Project.Samples[i].Ready, a.Project.Samples[i].Outputs.Index = false, ""
	}
	p.evidence = ""
	tt.Frame()
	tt.Frame()
	snapshot(t, tt, "states-no-samples")

	states := []struct {
		name string
		task model.Task
	}{
		{"queued", model.Task{Status: "queued", StartedAt: at(-5 * time.Second)}},
		{"partial", model.Task{Status: "running", LaunchedAt: at(-30 * time.Second), SecondsLeft: f64(40),
			Progress: &model.Progress{Stage: "fold", Detail: "3/8 windows on 4 workers", BytesDone: f64(3), BytesTotal: f64(8), Units: "windows"}}},
		{"failed-memory", model.Task{Status: "failed", FinishedAt: at(0),
			Error: "10807 nt exceeds the CPLfold limit of 5331 nt for this memory budget (pseudoknot-free). Use windows mode for long RNAs."}},
		{"failed-evidence", model.Task{Status: "failed", FinishedAt: at(0),
			Error: "ValueError: no intramolecular ZIKV_African_KX377335-1_virusRNA chimeras fall inside the requested region"}},
		{"cancelled", model.Task{Status: "cancelled", FinishedAt: at(0)}},
		{"interrupted", model.Task{Status: "interrupted", FinishedAt: at(0)}},
		{"succeeded", model.Task{Status: "succeeded", FinishedAt: at(0)}},
	}
	for _, s := range states {
		a, f, tt := stStatesFixture(t, 3)
		if s.name == "succeeded" {
			list := f.ops["list_structures"]
			items := list["structures"].([]any)
			items[0].(map[string]any)["task_id"] = "structure-1"
		}
		if err := tt.Click("Run fold"); err != nil {
			t.Fatal(err)
		}
		csSettle(a, tt)
		stTaskState(a, tt, s.task)
		if s.name == "succeeded" {
			a.PageFor("structure").(*structurePage).loadList()
			csSettle(a, tt)
		}
		snapshot(t, tt, "states-"+s.name)
		if s.name == "partial" || s.name == "failed-memory" {
			tt.SetDark(true)
			tt.Frame()
			snapshot(t, tt, "states-"+s.name+"-dark")
			tt.SetDark(false)
		}
	}
}

// TestStructureFoldStates checks what each state says and offers.
func TestStructureFoldStates(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	f64 := func(v float64) *float64 { return &v }
	expect := func(tt *ui.Tester, texts ...string) {
		t.Helper()
		for _, text := range texts {
			if !tt.HasText(text) {
				t.Fatalf("lacks %q: %q", text, tt.Texts())
			}
		}
	}

	// Long name, many results: the name in full, counts and units.
	a, _, tt := stStatesFixture(t, 30)
	p := a.PageFor("structure").(*structurePage)
	p.gene = stLongGene
	csSettle(a, tt)
	expect(tt, stLongGene+" · 2,534 nt", "Results (30)", "whole RNA · 2,534 nt", "712 base pairs · -811.4 kcal/mol",
		"Its files are missing from the project folder; it cannot be shown. Run the fold again to recreate it.")

	// Advanced settings: folded, values always shown, flagged when changed.
	expect(tt, "Advanced settings", "Evidence weight 0.50 · beam 200 · phase 1 1 · energy window 5 · DP09 · beta 0.00 · defaults")
	if tt.HasText("Beam size") {
		t.Fatal("advanced settings open by default")
	}
	p.beam = 100
	tt.Frame()
	expect(tt, "Evidence weight 0.50 · beam 100 · phase 1 1 · energy window 5 · DP09 · beta 0.00 · changed from the defaults")
	p.beam = stDefaultBeam

	// Memory over the budget blocks the run and says what to do; the
	// longest fold the budget holds is said up front (detector.py
	// _max_length_for, see core's TestCPLfoldMemoryMatchesDetector).
	p.gene = africaGene
	p.budgetMB = 8192
	tt.Frame()
	expect(tt, "Fits in memory: up to 7,428 nt per fold",
		"Cannot run yet:", "• This fold needs about 16.7 GB of memory but tasks may use 8.0 GB. "+
			"Fold shorter windows, fewer windows at once (Advanced), or raise Memory in Preferences.")
	// It is where the budget check draws the line, for windows folded at
	// once with pseudoknots too.
	fits := func(windows string) bool {
		p.windows = windows
		return !slices.ContainsFunc(p.plan().problems, func(s string) bool { return strings.HasPrefix(s, "This fold needs about") })
	}
	p.mode = "windows"
	if !fits("1-7428") || fits("1-7429") {
		t.Fatal("8 GB does not hold 7,428 nt exactly")
	}
	p.budgetMB, p.pseudoknot, p.workers = 24576, true, 2
	if !fits("1-7885\n8001-8400") || fits("1-7886\n8001-8400") {
		t.Fatal("24 GB does not hold two 7,885 nt windows with pseudoknots exactly")
	}
	tt.Frame()
	expect(tt, "Fits in memory: up to 7,885 nt per window with pseudoknots, 2 windows at once")
	p.budgetMB = 0
	tt.Frame()
	if tt.HasText("Fits in memory") {
		t.Fatal("a limit while the budget is unknown")
	}
	p.budgetMB = 24576

	// Empty and loading.
	a, f, tt := stStatesFixture(t, 0)
	f.ops["list_structures"] = core.Obj{"structures": []any{}}
	p = a.PageFor("structure").(*structurePage)
	p.loadList()
	csSettle(a, tt)
	expect(tt, "No structures yet. Choose an RNA and its evidence above and press Run fold; results appear here and in the Report.")
	p.loading = true
	tt.Frame()
	expect(tt, "Loading results…")
	p.loading, p.listErr = false, "project.json is locked"
	tt.Frame()
	expect(tt, "Could not read the project's structures: project.json is locked. Press Refresh to try again.")

	// The fold started here, through its states.
	states := []struct {
		task  model.Task
		texts []string
	}{
		{model.Task{Status: "queued"}, []string{"Waiting to start", "Cancel", "Fold running…"}},
		{model.Task{Status: "running", LaunchedAt: at(-30 * time.Second), SecondsLeft: f64(40),
			Progress: &model.Progress{Stage: "fold", BytesDone: f64(3), BytesTotal: f64(8), Units: "windows"}},
			[]string{"Folding: 3 of 8 windows done", "about 40 s left. You can keep working; the fold continues in the background.", "Cancel"}},
		{model.Task{Status: "running", Progress: &model.Progress{Stage: "evidence"}}, []string{"Reading the chimeric reads…"}},
		{model.Task{Status: "failed", FinishedAt: at(0), Error: "10807 nt exceeds the CPLfold limit of 5331 nt for this memory budget (pseudoknot-free)."},
			[]string{"The fold failed", "The region is longer than CPLfold can fold with the memory tasks may use.",
				"Next: fold shorter windows, or raise Memory in Preferences.", "Technical details", "Run fold"}},
		{model.Task{Status: "failed", FinishedAt: at(0), Error: "ValueError: no intramolecular X chimeras fall inside the requested region"},
			[]string{"No chimeric read of these samples has both arms inside the folded region, so there is no evidence to guide the fold."}},
		{model.Task{Status: "failed", FinishedAt: at(0), Error: "Segmentation fault"},
			[]string{"The fold stopped with an error.", "Next: run it again; if it fails again, send the technical details below to the HYB2 team."}},
		{model.Task{Status: "cancelled"}, []string{"Fold cancelled", "Nothing was saved, including windows that had already been folded. Change the form if needed, then run it again."}},
		{model.Task{Status: "interrupted"}, []string{"Fold stopped", "Run it again", "HYB2 Desktop closed while it ran. Nothing was saved; it starts from the beginning."}},
		{model.Task{Status: "succeeded"}, []string{"Fold finished", "Computation finished, but result registration is pending or unavailable. Refresh Results to check again."}},
	}
	a, _, tt = stStatesFixture(t, 3)
	if err := tt.Click("Run fold"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	for _, s := range states {
		stTaskState(a, tt, s.task)
		expect(tt, s.texts...)
	}
	// A running fold cannot be started twice; Cmd+Enter does nothing then.
	stTaskState(a, tt, model.Task{Status: "running"})
	p = a.PageFor("structure").(*structurePage)
	before := len(a.TaskList())
	p.runFold()
	csSettle(a, tt)
	if len(a.TaskList()) != before {
		t.Fatal("a second fold started while one runs")
	}
}

// TestStructureNewResultFocus: as on the web's Structure page, a fold
// started here takes the keyboard focus to the viewer's heading when it
// arrives and is announced once; a result shown by hand does neither.
func TestStructureNewResultFocus(t *testing.T) {
	a, _, tt := stStatesFixture(t, 3)
	p := a.PageFor("structure").(*structurePage)
	// structure-001 is the fixture's whole-RNA result of stLongGene
	heading := "Viewer: " + stLongGene + " · whole RNA · 2,534 nt"
	readyCount := func() int {
		n := 0
		for _, text := range tt.Announcements() {
			if text == "Structure ready · 712 base pairs · -811.4 kcal/mol" {
				n++
			}
		}
		return n
	}
	p.show("structure-001")
	csSettle(a, tt)
	if tt.Focused(heading) || readyCount() != 0 {
		t.Fatal("a result shown by hand takes the focus or is announced")
	}
	p.show("structure-000")
	csSettle(a, tt)
	p.pendingShow = "structure-001"
	p.resolvePair()
	csSettle(a, tt)
	if !tt.Focused(heading) {
		t.Fatalf("the new result's heading %q has no focus", heading)
	}
	tt.Frame()
	if n := readyCount(); n != 1 {
		t.Fatalf("announced %d times", n)
	}
	// Another result shown before it arrived: neither then nor later.
	p.pendingShow = "structure-001"
	p.resolvePair()
	p.show("structure-000")
	csSettle(a, tt)
	p.show("structure-001")
	csSettle(a, tt)
	if readyCount() != 0 {
		t.Fatal("a fold replaced by another result is announced")
	}
}

func TestStructureFailedTaskEngineNeutral(t *testing.T) {
	a, f, tt, p := stSetupPage(t, 1800)
	stPick(t, tt, engineVienna, false)
	mustClick(t, tt, "Run fold")
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || started[0].Engine == nil || *started[0].Engine != engineVienna || p.lastTask != "structure-1" {
		t.Fatalf("expected one ViennaRNA task: requests=%+v, lastTask=%q", started, p.lastTask)
	}
	const taskLabel = "Structure (ViennaRNA) · whole RNA"
	const referenceError = "the selected samples must share one reference"
	// Replay the recorded runtime error in the component fixture; no engine runs here.
	stTaskState(a, tt, model.Task{Status: "failed", Error: referenceError})
	mustClick(t, tt, "Technical details")

	for _, tc := range []struct{ engine string }{{engineVienna}, {engineCPLfold}} {
		t.Run(tc.engine, func(t *testing.T) {
			stPick(t, tt, tc.engine, false)
			csSettle(a, tt)
			task := a.Tasks["structure-1"]
			if p.engine != tc.engine || p.lastTask != task.TaskID || a.TaskLabel(task) != taskLabel ||
				task.Status != "failed" || task.ErrorText() != referenceError || len(f.startedKinds()) != 1 {
				t.Fatalf("form change must preserve the failed ViennaRNA task: engine=%q, lastTask=%q, label=%q, task=%+v",
					p.engine, p.lastTask, a.TaskLabel(task), task)
			}
			snapshot(t, tt, "states-vienna-reference-failed-form-"+tc.engine)
			for _, want := range []string{
				"The fold failed",
				"The fold stopped with an error.",
				"Next: run it again; if it fails again, send the technical details below to the HYB2 team.",
				referenceError + " (log: runs/structure-1/stderr.log)",
			} {
				if !slices.Contains(tt.Texts(), want) {
					t.Errorf("missing exact rendered text %q: %q", want, tt.Texts())
				}
			}
			if tt.HasText("CPLfold stopped with an error.") {
				t.Error("the historical ViennaRNA failure is incorrectly attributed to CPLfold")
			}
			t.Logf("form engine=%s; task=%s; historical label=%q; raw error=%q; legacy fallback shown=%t",
				p.engine, task.TaskID, a.TaskLabel(task), task.ErrorText(), tt.HasText("CPLfold stopped with an error."))
		})
	}
}

func TestStructureSucceededRegistrationStatus(t *testing.T) {
	a, _, tt, p := stSetupPage(t, 1500)
	saved := p.structures[0]
	p.lastTask = "finished-computation"
	p.structures = nil
	for _, registration := range []string{"", "pending", "failed", "registered"} {
		task := model.Task{TaskID: p.lastTask, Kind: "structure", Status: "succeeded", RegistrationStatus: registration}
		if registration == "failed" {
			task.RegistrationError = "project manifest is locked"
		}
		a.Tasks[p.lastTask] = task
		tt.Frame()
		if registration == "pending" {
			snapshot(t, tt, "structure-registration-pending")
		} else if registration == "registered" {
			snapshot(t, tt, "structure-registration-flag-without-entry")
		}
		mustHave(t, tt, "Fold finished")
		if tt.HasText("It is shown in the viewer and listed under Results.") || tt.HasText("The fold failed") || task.ErrorText() != "" {
			t.Errorf("registration %q must not invent a result or a fold failure: %q", registration, tt.Texts())
		}
		if !tt.HasText("Computation finished, but result registration is pending or unavailable. Refresh Results to check again.") || p.entryForTask(p.lastTask) != nil || p.viewA != "" {
			t.Errorf("registration %q without a visible result needs an honest status: entry=%v, view=%q, texts=%q", registration, p.entryForTask(p.lastTask), p.viewA, tt.Texts())
		}
		t.Logf("no result: task=%s, registration=%q, registration_error=%q, entry=%v, view=%q", task.Status, registration, task.RegistrationError, p.entryForTask(p.lastTask), p.viewA)
	}

	// Even a registered task waits for its actual result entry to arrive.
	saved.TaskID = p.lastTask
	p.structures = []structureEntry{saved}
	p.show(saved.ID)
	csSettle(a, tt)
	// The Arc view is rendered locally; this fixture has no external layout
	// service. Show the registered result rather than a missing-layout error.
	p.view = "Arc"
	tt.Frame()
	snapshot(t, tt, "structure-registration-complete")
	mustHave(t, tt, "Fold finished", "It is shown in the viewer and listed under Results.")
	if tt.HasText("result registration is pending or unavailable") || p.entryForTask(p.lastTask) == nil || p.viewA != saved.ID {
		t.Fatalf("registered result not reflected in status: entry=%v, view=%q, texts=%q", p.entryForTask(p.lastTask), p.viewA, tt.Texts())
	}
	t.Logf("registered: task=%s, registration=%s, entry=%s, view=%s", a.Tasks[p.lastTask].Status, a.Tasks[p.lastTask].RegistrationStatus, p.entryForTask(p.lastTask).ID, p.viewA)
}
