package views

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

func TestPaletteFuzzyKeysAndPageActions(t *testing.T) {
	a, _, tt := openThemed(t, "system")
	// "/" opens it too; with no query the entries come by group
	tt.Key(0, ui.KeySlash)
	tt.Frame()
	if !a.palette.open || !tt.HasText("Go to") || !tt.HasText("Datasets") || !tt.HasText("Analyse All 6 datasets merged") ||
		!tt.HasText("Explore › Binding energies") || !tt.HasText("Reduce motion") || !tt.HasText("Privacy") {
		t.Fatalf("palette %v: %q", a.palette.open, tt.Texts())
	}
	// a fuzzy query: the word starts of "Explore › Contact map" beat the rest
	tt.Type("ecm")
	tt.Frame()
	if got := matchCommands(a.commands(), "ecm"); len(got) == 0 || got[0].title != "Explore › Contact map" {
		t.Fatalf("best %+v", got)
	}
	// ↓ highlights the next match and Enter runs it; ↑ wraps round
	matches := matchCommands(a.commands(), "ecm")
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.palette.active != 1 {
		t.Fatalf("active %d", a.palette.active)
	}
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if a.palette.active != len(matches)-1 {
		t.Fatalf("active %d of %d", a.palette.active, len(matches))
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if a.palette.active != 0 {
		t.Fatalf("active after Tab %d", a.palette.active)
	}
	tt.Key(0, ui.KeyEnter)
	settle(a, tt)
	if a.palette.open || a.Page != "explore" || a.PageFor("explore").(*explorePage).tab != tabContact {
		t.Fatalf("open %v page %q", a.palette.open, a.Page)
	}
	// the page's actions are listed once they can run
	titles := func() string {
		var out []string
		for _, c := range a.commands() {
			if c.group == "On this page" {
				out = append(out, c.title)
			}
		}
		return strings.Join(out, "|")
	}
	if got := titles(); !strings.Contains(got, "Export map · SVG (vector)…") || !strings.Contains(got, "Export matrix · CSV (bins × bins)…") {
		t.Fatalf("contact map actions %q", got)
	}
	a.Navigate("data", nil)
	if got := titles(); !strings.Contains(got, "Add reference…") || !strings.Contains(got, "Import raw files…") {
		t.Fatalf("data actions %q", got)
	}
	// Analyse … chooses the top bar's samples
	for _, c := range a.commands() {
		if c.title == "Analyse All 6 datasets merged" {
			c.run()
		}
	}
	if a.Selected.Key != AllSelection || len(a.SelectionIDs(a.Selected.Key)) != 6 {
		t.Fatalf("selection %q", a.Selected.Key)
	}
}

func TestPaletteScore(t *testing.T) {
	if _, ok := paletteScore("Go to Compare", "gzc"); ok {
		t.Fatal("not a subsequence")
	}
	start, _ := paletteScore("Go to Compare", "com")
	middle, _ := paletteScore("Go to Report", "por")
	if start <= middle {
		t.Fatalf("word start %v, inside %v", start, middle)
	}
	if s, ok := paletteScore("Anything", "  "); !ok || s != 0 {
		t.Fatalf("empty query %v %v", s, ok)
	}
}

func TestStartPagePalette(t *testing.T) {
	a := New(newFake(t), nil, "")
	a.recent = []Recent{{Path: "/projects/zikv.hyb2proj", Name: "ZIKV GUI check"}}
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	tt.Key(ui.Shift, ui.KeySlash) // ?
	tt.Frame()
	for _, text := range []string{"Open project…", "New project", "Try example", "Recent projects", "Open ZIKV GUI check"} {
		if !tt.HasText(text) {
			t.Fatalf("start palette lacks %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("Go to Compare") || tt.HasText("Close project") {
		t.Fatalf("project entries on the start page: %q", tt.Texts())
	}
	tt.Type("open zikv")
	tt.Key(0, ui.KeyEnter)
	waitFlush(a)
	if a.Project == nil || a.Project.Path != "/projects/zikv.hyb2proj" {
		t.Fatalf("project %+v", a.Project)
	}
}

func TestPageShortcuts(t *testing.T) {
	a, _, tt := openThemed(t, "system")
	a.Navigate("data", nil)
	tt.Frame()
	press := func(key ui.Key) {
		tt.Key(0, key)
		tt.Frame()
		waitFlush(a)
	}
	press(ui.KeyG)
	if !a.chord || !tt.HasText("g then: d Data · o QC · i Interactions · c Contact map · v Viewpoint · r Region · b Binding energies · p Compare · s Structure · e Report") {
		t.Fatalf("chord %v: %q", a.chord, tt.Texts())
	}
	press(ui.KeyB)
	p := a.PageFor("explore").(*explorePage)
	if a.chord || a.Page != "explore" || p.tab != tabEnergies {
		t.Fatalf("chord %v page %q tab %d", a.chord, a.Page, p.tab)
	}
	// ] and [ step through the pages, Explore's tabs among them, round
	press(ui.KeyBracketRight)
	if a.Page != "compare" {
		t.Fatalf("after ] %q", a.Page)
	}
	press(ui.KeyBracketLeft)
	press(ui.KeyBracketLeft)
	if a.Page != "explore" || p.tab != tabRegion {
		t.Fatalf("after [ [ %q tab %d", a.Page, p.tab)
	}
	a.Navigate("data", nil)
	press(ui.KeyBracketLeft)
	if a.Page != "report" {
		t.Fatalf("[ from Data %q", a.Page)
	}
	// a letter without g does nothing; a g waits only so long
	press(ui.KeyP)
	if a.Page != "report" {
		t.Fatalf("p alone went to %q", a.Page)
	}
	a.chord, a.chordAt = true, time.Now().Add(-2*chordWait)
	tt.Frame()
	press(ui.KeyP)
	if a.chord || a.Page != "report" {
		t.Fatalf("stale chord went to %q", a.Page)
	}
	// behind a dialog, the keys are the dialog's
	a.helpOpen = true
	tt.Frame()
	press(ui.KeyBracketRight)
	if a.Page != "report" {
		t.Fatalf("] behind Help went to %q", a.Page)
	}
}

func TestHelpKeysAndReferenceLinks(t *testing.T) {
	a, _, tt := openThemed(t, "system")
	a.Navigate("explore", nil)
	a.helpOpen = true
	tt.Frame()
	for _, text := range []string{"g then a letter", "[ and ]", "Previous / next page", "Import check", "Privacy", "Binding energies recalculates"} {
		if !tt.HasText(text) && !strings.Contains(strings.Join(tt.Texts(), "\n"), text) {
			t.Fatalf("help lacks %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("Import check"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.Page != "qc" || a.helpOpen {
		t.Fatalf("page %q help %v", a.Page, a.helpOpen)
	}
	a.helpOpen = true
	tt.Frame()
	if err := tt.Click("Privacy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	all := strings.Join(tt.Texts(), "\n")
	if a.Page != "privacy" || !strings.Contains(all, "nothing is uploaded") || !strings.Contains(all, lookupNoticeText) {
		t.Fatalf("privacy page %q: %q", a.Page, tt.Texts())
	}
}

func TestTaskViewButtons(t *testing.T) {
	cases := []struct {
		task          model.Task
		label, page   string
		key, value    string
		noOptionsWant bool
	}{
		{model.Task{TaskID: "energy-1", Kind: "energy", Output: "/p/comparisons/compare-7/energy_L30.tsv"}, "View results", "compare", "comparisonId", "compare-7", false},
		{model.Task{TaskID: "energy-2", Kind: "energy"}, "View results", "compare", "", "", true},
		{model.Task{TaskID: "compare-1", Kind: "compare"}, "View results", "compare", "comparisonId", "compare-1", false},
		{model.Task{TaskID: "batch-energy-1", Kind: "batch_energy"}, "View results", "explore", "tab", "energies", false},
		{model.Task{TaskID: "cplfold-1", Kind: "cplfold"}, "View structure", "structure", "", "", true},
		{model.Task{TaskID: "pipeline-1", Kind: "pipeline"}, "View data", "data", "", "", true},
		{model.Task{TaskID: "index-1", Kind: "analysis_index"}, "", "", "", "", true},
	}
	for _, c := range cases {
		label, page, options := taskView(c.task)
		if label != c.label || page != c.page || (c.noOptionsWant && options != nil) || (!c.noOptionsWant && options[c.key] != c.value) {
			t.Errorf("%s: %q %q %v", c.task.TaskID, label, page, options)
		}
	}
	// the drawer's button opens Compare on the energies' test
	a, _, tt := openThemed(t, "system")
	a.storeTask(model.Task{TaskID: "batch-energy-9", Kind: "batch_energy", Status: "succeeded", StartedAt: "2026-10-14T10:00:00Z"})
	a.drawerOpen = true
	tt.Frame()
	if err := tt.Click("View results"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if a.drawerOpen || a.Page != "explore" || a.PageFor("explore").(*explorePage).tab != tabEnergies {
		t.Fatalf("drawer %v page %q", a.drawerOpen, a.Page)
	}
}

func TestAllDatasetsSelection(t *testing.T) {
	a, _ := openFixture(t)
	if got := a.SelectionIDs(AllSelection); len(got) != 6 {
		t.Fatalf("all ids %v", got)
	}
	if got := a.SelectionLabel(AllSelection); got != "All 6 datasets merged" {
		t.Fatalf("label %q", got)
	}
	for _, o := range a.GroupOptions() {
		if o.Key == AllSelection {
			t.Fatal("Compare's groups offer all merged")
		}
	}
	// kept across a refresh
	a.selectKey(AllSelection)
	a.normalizeSelection()
	if a.Selected.Key != AllSelection {
		t.Fatalf("selection %q", a.Selected.Key)
	}
}

// showCounter counts Show calls.
type showCounter struct{ shows int }

func (p *showCounter) Show()              { p.shows++ }
func (p *showCounter) View(c *ui.Context) {}

func TestReferenceChangeAndRefreshSave(t *testing.T) {
	a, f := openFixture(t)
	counter := &showCounter{}
	a.RegisterPage("report", counter)
	a.Navigate("report", nil)
	tt := ui.NewTester(a.View, 1300, 720)
	tt.Frame()
	before := counter.shows
	if err := tt.Click("ZAFR"); err != nil { // the picker shows its choice
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("ZBRA"); err != nil {
		if err := tt.Click("ZBRA"); err != nil {
			t.Fatalf("%v: %q", err, tt.Texts())
		}
	}
	tt.Frame()
	if a.Selected.ReferenceID == "" || a.Selected.Gene != "ZIKV_Brazilian_MF073357-1_virusRNA" || counter.shows != before+1 {
		t.Fatalf("selected %+v, shows %d -> %d", a.Selected, before, counter.shows)
	}
	// a refresh that drops the selected samples saves the new choice
	a.Selected.Key = "s:GONE"
	a.RefreshProject()
	waitFlush(a)
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		desktop, _ := f.uiState["desktop"].(map[string]any)
		f.mu.Unlock()
		if desktop["selection"] == AllSelection {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("saved %v", desktop)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// uiStateFake holds UpdateUIState until released and records the states
// written; err fails every write.
type uiStateFake struct {
	*fakeBackend
	gate    chan struct{}
	mu      sync.Mutex
	written []string
	err     error
}

func (f *uiStateFake) UpdateUIState(path string, state core.Obj) (core.Obj, error) {
	<-f.gate
	desktop, _ := state["desktop"].(map[string]any)
	f.mu.Lock()
	f.written = append(f.written, fmt.Sprint(desktop["page"]))
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return core.Obj{"ok": true}, nil
}

func TestSaveUIStateInOrderLatestWins(t *testing.T) {
	f := &uiStateFake{fakeBackend: newFake(t), gate: make(chan struct{})}
	a := New(f, nil, "")
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	close(f.gate) // the opening's saves, if any, go through
	waitIdle := func() {
		for i := 0; i < 400; i++ {
			a.uiSaves.mu.Lock()
			idle := !a.uiSaves.writing
			a.uiSaves.mu.Unlock()
			if idle {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("ui_state writes did not finish")
	}
	waitIdle()
	f.mu.Lock()
	f.written = nil
	f.mu.Unlock()
	f.gate = make(chan struct{})
	a.Navigate("qc", nil) // written first, held at the gate
	time.Sleep(20 * time.Millisecond)
	a.Navigate("compare", nil) // replaced by the next
	a.Navigate("report", nil)
	close(f.gate)
	waitIdle()
	f.mu.Lock()
	got := strings.Join(f.written, ",")
	f.mu.Unlock()
	if got != "qc,report" {
		t.Fatalf("written %q", got)
	}
	// a failed write says so
	f.err = errors.New("disk full")
	a.Navigate("data", nil)
	waitIdle()
	waitFlush(a)
	found := false
	for _, m := range a.toasts {
		found = found || strings.Contains(m.Text, "disk full")
	}
	if !found {
		t.Fatalf("toasts %+v", a.toasts)
	}
}

func TestCloseResetsAndStaleReplies(t *testing.T) {
	a, f := openFixture(t)
	a.Navigate("structure", map[string]any{"structureId": "s-1"})
	a.NavOptions = map[string]any{"left": "over"}
	a.Close()
	if a.Page != "data" || a.NavOptions != nil {
		t.Fatalf("page %q options %v", a.Page, a.NavOptions)
	}
	// a task list asked for before the project was reopened is dropped
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	f.mu.Lock()
	f.tasks = []core.Obj{{"task_id": "old-1", "kind": "structure", "status": "succeeded"}}
	f.mu.Unlock()
	a.RefreshTasks()
	a.opened++ // as a Close and reopen of the same path would
	waitFlush(a)
	if _, ok := a.Tasks["old-1"]; ok {
		t.Fatal("a reply for the earlier opening was applied")
	}
	called := false
	a.ResumeTaskThen("old-1", "", func(_ model.Task, err error) { called = errors.Is(err, errProjectChanged) })
	a.opened++
	waitFlush(a)
	if _, ok := a.Tasks["old-1"]; ok || !called {
		t.Fatalf("resume applied %v, called %v", ok, called)
	}
}

func TestMotionPreference(t *testing.T) {
	dir := t.TempDir()
	a := New(newFake(t), nil, dir)
	if a.motion != "" {
		t.Fatalf("motion %q", a.motion)
	}
	tt := ui.NewTester(a.View, 1100, 720)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if !a.motionReducedWith(true) || a.motionReducedWith(false) {
		t.Fatal("the system's Reduce Motion is not followed")
	}
	// the palette's entry follows the effective setting: the system's
	// Reduce Motion makes it "Turn motion on"
	run := func(title string) {
		t.Helper()
		for _, c := range a.commands() {
			if c.title == title {
				c.run()
				return
			}
		}
		t.Fatalf("no %q in the palette", title)
	}
	run("Turn motion on")
	if a.motion != motionFull || New(newFake(t), nil, dir).motion != motionFull {
		t.Fatalf("motion %q not kept", a.motion)
	}
	tt.Frame()
	run("Reduce motion")
	if a.motion != motionReduced || New(newFake(t), nil, dir).motion != motionReduced {
		t.Fatalf("motion %q not kept", a.motion)
	}
	a.openPrefs()
	waitFlush(a)
	tt.Frame()
	if !tt.HasText("Motion") || !tt.HasText("Animated") || !tt.HasText("Reduced") {
		t.Fatalf("prefs %q", tt.Texts())
	}
	a.chooseMotion(motionFull)
	if New(newFake(t), nil, dir).motion != motionFull {
		t.Fatal("Animated not kept")
	}
}
