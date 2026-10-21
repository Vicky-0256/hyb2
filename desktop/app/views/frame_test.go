package views

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// themeFake stores the theme preference.
type themeFake struct {
	*fakeBackend
	theme   string
	patches []core.Obj
}

func (f *themeFake) Preferences() (core.Obj, error) {
	got, err := f.fakeBackend.Preferences()
	got["theme"] = f.theme
	return got, err
}

func (f *themeFake) SetPreferences(patch core.Obj) (core.Obj, error) {
	f.patches = append(f.patches, patch)
	if theme, ok := patch["theme"].(string); ok {
		f.theme = theme
	}
	return f.Preferences()
}

func openThemed(t *testing.T, theme string) (*App, *themeFake, *ui.Tester) {
	t.Helper()
	f := &themeFake{fakeBackend: newFake(t), theme: theme}
	for op, name := range map[string]string{"analysis_summary": "explore_summary.json", "analysis_contact_bins": "explore_contact.json"} {
		f.queries[op] = fixture(t, name)
	}
	a := New(f, nil, "")
	a.Open("/projects/zikv.hyb2proj")
	waitFlush(a)
	tt := ui.NewTester(a.View, 1100, 720)
	waitFlush(a)
	tt.Frame()
	return a, f, tt
}

func TestThemePreference(t *testing.T) {
	a, f, tt := openThemed(t, "dark")
	a.Navigate("explore", nil)
	settle(a, tt)
	p := a.PageFor("explore").(*explorePage)
	if a.theme != "dark" || !p.cm.dark {
		t.Fatalf("theme %q, contact map dark %v", a.theme, p.cm.dark)
	}
	snapshot(t, tt, "theme-dark")
	// the system appearance no longer matters
	tt.SetDark(false)
	tt.Frame()
	if !p.cm.dark {
		t.Fatal("dark preference lost to the system appearance")
	}
	a.chooseTheme("light")
	waitFlush(a)
	tt.SetDark(true)
	tt.Frame()
	if p.cm.dark || f.theme != "light" || len(f.patches) != 1 || f.patches[0]["theme"] != "light" {
		t.Fatalf("dark %v stored %q patches %v", p.cm.dark, f.theme, f.patches)
	}
	a.chooseTheme("system")
	waitFlush(a)
	tt.Frame()
	if !p.cm.dark {
		t.Fatal("system theme does not follow the system")
	}
	// Preferences shows the choice
	a.openPrefs()
	waitFlush(a)
	tt.Frame()
	if !tt.HasText("Appearance") || !tt.HasText("Match the system") {
		t.Fatalf("prefs %q", tt.Texts())
	}
}

func TestCommandPalette(t *testing.T) {
	a, _, tt := openThemed(t, "system")
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !a.palette.open || !tt.HasText("Go to Compare") || !tt.HasText("Explore › Viewpoint") {
		t.Fatalf("palette %v: %q", a.palette.open, tt.Texts())
	}
	snapshot(t, tt, "palette")
	tt.Type("explore view")
	tt.Frame()
	if tt.HasText("Go to Compare") || !tt.HasText("Explore › Viewpoint") {
		t.Fatalf("filtered %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	waitFlush(a)
	tt.Frame()
	p := a.PageFor("explore").(*explorePage)
	if a.palette.open || a.Page != "explore" || p.tab != tabViewpoint {
		t.Fatalf("open %v page %q tab %d", a.palette.open, a.Page, p.tab)
	}
	// the top bar's button opens it too
	if err := tt.Click("Search pages and actions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.palette.open || a.palette.query != "" {
		t.Fatalf("palette %+v", a.palette)
	}

	names := func(cmds []command) string {
		var out []string
		for _, c := range cmds {
			out = append(out, c.title)
		}
		return strings.Join(out, "|")
	}
	if got := names(matchCommands(a.commands(), " APPEARANCE  dark ")); got != "Appearance: Dark" {
		t.Fatalf("match %q", got)
	}
	if got := matchCommands(a.commands(), "zzz"); len(got) != 0 {
		t.Fatalf("match %q", names(got))
	}
}

func TestHelpAndReferencePages(t *testing.T) {
	a, _, tt := openThemed(t, "system")
	a.Navigate("compare", nil)
	tt.Frame()
	if err := tt.Click("Help"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("On this page: Compare") || !tt.HasText("Keyboard") {
		t.Fatalf("help %q", tt.Texts())
	}
	snapshot(t, tt, "help")
	if err := tt.Click("Methods"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.Page != "methods" || a.helpOpen {
		t.Fatalf("page %q help %v", a.Page, a.helpOpen)
	}
	for _, s := range methodsText {
		if !tt.HasText(s.title) {
			t.Fatalf("methods lacks %q: %q", s.title, tt.Texts())
		}
	}
	snapshot(t, tt, "methods")
	a.Navigate("file-format", nil)
	tt.Frame()
	if !tt.HasText("Columns (1-indexed)") || !tt.HasText("Chimera type (18 columns)") {
		t.Fatalf("file format %q", tt.Texts())
	}
	snapshot(t, tt, "file-format")
}

func TestTableDensity(t *testing.T) {
	a, f, tt := openThemed(t, "system")
	f.queries["analysis_hybrids"] = fixture(t, "explore_hybrids.json")
	a.Navigate("explore", map[string]any{"tab": "interactions"})
	settle(a, tt)
	box := func() float32 {
		r, ok := tt.Find("2")
		if !ok {
			t.Fatalf("no row: %q", tt.Texts())
		}
		return r.H
	}
	comfortable := box()
	a.setDensity(densityLabels[1])
	tt.Frame()
	if compact := box(); compact >= comfortable {
		t.Fatalf("compact row %g, comfortable %g", compact, comfortable)
	}
	if desktop := a.Project.UIState["desktop"].(map[string]any); desktop["density"] != "Compact rows" {
		t.Fatalf("saved %v", desktop)
	}
	a.Project.UIState["desktop"].(map[string]any)["density"] = "Comfortable rows"
	a.restoreUIState()
	if a.compact() {
		t.Fatal("density not restored")
	}
}
