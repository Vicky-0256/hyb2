package views

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/model"
)

// paletteTitles are the palette's entries of group ("" for all).
func paletteTitles(a *App, group string) []string {
	var out []string
	for _, c := range a.commands() {
		if group == "" || c.group == group {
			out = append(out, c.title)
		}
	}
	return out
}

// The top bar's samples are the web's scopes: all merged by default, the
// conditions merging some of the samples, then the samples; Compare keeps
// every condition.
func TestSelectionScopes(t *testing.T) {
	a, _ := openFixture(t)
	a.Selected.Key, a.autoKey = "", ""
	a.normalizeSelection()
	if a.Selected.Key != AllSelection {
		t.Fatalf("default %q", a.Selected.Key)
	}
	if got := a.SelectionLabel("c:SUB"); got != "SUB · 2 datasets merged" {
		t.Fatalf("condition label %q", got)
	}
	// a condition of one sample, and one of every sample
	a.Project.Conditions = append(a.Project.Conditions,
		model.Condition{Name: "ONE", SampleIDs: []string{"SUB_REP1"}},
		model.Condition{Name: "EVERY", SampleIDs: a.SelectionIDs(AllSelection)})
	var keys, labels []string
	for _, o := range a.SelectionOptions() {
		keys, labels = append(keys, o.Key), append(labels, o.Label)
	}
	if slices.Contains(keys, "c:ONE") || slices.Contains(keys, "c:EVERY") || !slices.Contains(labels, "AFR · 2 datasets merged") {
		t.Fatalf("options %q %q", keys, labels)
	}
	var groups []string
	for _, o := range a.GroupOptions() {
		groups = append(groups, o.Key)
	}
	if !slices.Contains(groups, "c:ONE") || !slices.Contains(groups, "c:EVERY") || slices.Contains(groups, AllSelection) {
		t.Fatalf("Compare's groups %q", groups)
	}
	if got := a.SelectionLabel("c:ONE"); got != "ONE · 1 dataset" {
		t.Fatalf("one-sample label %q", got)
	}
	// a choice stays; a key chosen by default gives way to All
	a.selectKey("s:SUB_REP1")
	a.normalizeSelection()
	if a.Selected.Key != "s:SUB_REP1" {
		t.Fatalf("chosen %q", a.Selected.Key)
	}
	a.Selected.Key, a.autoKey = "s:AFR_REP1", "s:AFR_REP1"
	a.normalizeSelection()
	if a.Selected.Key != AllSelection {
		t.Fatalf("default kept %q", a.Selected.Key)
	}
}

// Samples labelled alike stay apart in the top bar and the palette.
func TestSelectionLabelsUnique(t *testing.T) {
	got := SelectionLabels([]SelectionOption{{Label: "A"}, {Label: "B"}, {Label: "A"}, {Label: "A (2)"}, {Label: "A"}})
	if want := []string{"A", "B", "A (3)", "A (2)", "A (4)"}; !slices.Equal(got, want) {
		t.Fatalf("labels %q, want %q", got, want)
	}
	a, _ := openFixture(t)
	for i := range a.Project.Samples {
		if a.Project.Samples[i].ID == "AFR_REP1" || a.Project.Samples[i].ID == "AFR_REP2" {
			a.Project.Samples[i].Name, a.Project.Samples[i].Condition = "rep", ""
		}
	}
	titles := strings.Join(paletteTitles(a, "Datasets"), "|")
	if !strings.Contains(titles, "Analyse rep|Analyse rep (2)") {
		t.Fatalf("palette %q", titles)
	}
	tt := ui.NewTester(a.View, 1300, 720)
	tt.Frame()
	if err := tt.Click("SUB · 2 datasets merged"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("rep (2)"); err != nil {
		if err := tt.Click("rep (2)"); err != nil {
			t.Fatalf("%v: %q", err, tt.Texts())
		}
	}
	tt.Frame()
	if a.Selected.Key != "s:AFR_REP2" {
		t.Fatalf("selected %q", a.Selected.Key)
	}
}

// Privacy is one palette entry, with or without a project, and the "Local
// only" chip opens it from the start page.
func TestPrivacyEntryAndLocalChip(t *testing.T) {
	a := New(newFake(t), nil, "")
	tt := ui.NewTester(a.View, 1100, 720)
	tt.Frame()
	if n := strings.Count(strings.Join(paletteTitles(a, ""), "\n")+"\n", "Privacy\n"); n != 1 {
		t.Fatalf("start palette has Privacy %d times", n)
	}
	if err := tt.Click("Local only"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.privacyOpen || !tt.HasText(privacyLede) || !tt.HasText("What happens to your files") {
		t.Fatalf("privacy %v: %q", a.privacyOpen, tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.privacyOpen {
		t.Fatal("privacy still open")
	}

	a, _, tt = openThemed(t, "system")
	privacy := 0
	for _, c := range a.commands() {
		if strings.Contains(c.title, "Privacy") {
			privacy++
		}
	}
	if privacy != 1 {
		t.Fatalf("project palette has Privacy %d times", privacy)
	}
	tt2 := ui.NewTester(a.View, 1300, 720)
	tt2.Frame()
	tt2.Frame()
	if err := tt2.Click("Local only"); err != nil {
		t.Fatal(err)
	}
	tt2.Frame()
	if a.Page != "privacy" {
		t.Fatalf("page %q", a.Page)
	}
}

// Ctrl+N and Ctrl+P move in the palette; ⌘K closes it again.
func TestPaletteCtrlKeysAndToggle(t *testing.T) {
	a, _, tt := openThemed(t, "system")
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !a.palette.open {
		t.Fatal("⌘K did not open the palette")
	}
	// (on macOS the text field takes Ctrl+N and Ctrl+P as its Emacs keys)
	if runtime.GOOS != "darwin" {
		tt.Key(ui.Ctrl, ui.KeyN)
		tt.Key(ui.Ctrl, ui.KeyN)
		tt.Frame()
		if a.palette.active != 2 {
			t.Fatalf("active after Ctrl+N Ctrl+N %d", a.palette.active)
		}
		tt.Key(ui.Ctrl, ui.KeyP)
		tt.Frame()
		if a.palette.active != 1 {
			t.Fatalf("active after Ctrl+P %d", a.palette.active)
		}
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if a.palette.open {
		t.Fatal("⌘K did not close the palette")
	}
}

// Any other key ends a waiting "g", as the web's.
func TestChordEndsOnOtherKey(t *testing.T) {
	a, _, tt := openThemed(t, "system")
	a.Navigate("data", nil)
	tt.Frame()
	for _, key := range []ui.Key{ui.KeyX, ui.Key1, ui.KeyG, ui.KeyBracketRight} {
		tt.Key(0, ui.KeyG)
		tt.Frame()
		if !a.chord {
			t.Fatal("no chord")
		}
		tt.Key(0, key)
		tt.Frame()
		if a.chord || a.Page != "data" {
			t.Fatalf("key %v: chord %v page %q", key, a.chord, a.Page)
		}
		// the letter of a page after the ended chord does nothing
		tt.Key(0, ui.KeyP)
		tt.Frame()
		if a.Page != "data" {
			t.Fatalf("p after key %v went to %q", key, a.Page)
		}
	}
	// Shift and a page's letter go there
	tt.Key(0, ui.KeyG)
	tt.Key(ui.Shift, ui.KeyP)
	tt.Frame()
	if a.Page != "compare" {
		t.Fatalf("g Shift+P went to %q", a.Page)
	}
	a.chord, a.chordAt = false, time.Time{}
}

// The page entries follow the buttons: Try example only while the button
// shows, the IRIS run label, and the added imports and exports.
func TestPageCommandsFollowButtons(t *testing.T) {
	a, _, tt := openThemed(t, "system")
	a.Navigate("data", nil)
	tt.Frame()
	data := strings.Join(paletteTitles(a, "On this page"), "|")
	for _, want := range []string{"Import data folder…", "Import sample sheet…", "Import raw files…", "Add reference…"} {
		if !strings.Contains(data, want) {
			t.Fatalf("data actions lack %q: %q", want, data)
		}
	}
	a.Project.References = nil
	if strings.Contains(strings.Join(paletteTitles(a, "On this page"), "|"), "Try example") {
		t.Fatal("Try example with samples in the project")
	}
	a.Project.Samples = nil
	if !strings.Contains(strings.Join(paletteTitles(a, "On this page"), "|"), "Try example") {
		t.Fatal("no Try example in an empty project")
	}
}

func TestPaletteHelpTexts(t *testing.T) {
	for _, engine := range []string{"CPLfold", "CPLcofold", "ViennaRNA MFE", "COMRADES(ViennaRNA)", "IRIS"} {
		if !strings.Contains(pageHelp["structure"][0], engine) {
			t.Errorf("Structure help lacks %s", engine)
		}
	}
	for _, what := range []string{"HTML", "PDF", "ZIP"} {
		if !strings.Contains(pageHelp["report"][0], what) {
			t.Errorf("Report help lacks %s", what)
		}
	}
}
