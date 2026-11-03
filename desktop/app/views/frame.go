package views

// The app frame, as the web app has it: the theme preference (system,
// light or dark), the ⌘K command palette, the Help panel for the page shown,
// and the Methods and File format pages.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// themes are the "theme" preference's values (core.Themes) as Preferences
// shows them.
var (
	themeValues = []string{"system", "light", "dark"}
	themeLabels = []string{"Match the system", "Light", "Dark"}
)

// densityLabels are the web's table densities; compact fits more rows.
var densityLabels = []string{"Comfortable rows", "Compact rows"}

func (a *App) compact() bool { return a.Density == densityLabels[1] }

func (a *App) setDensity(label string) {
	a.Density = label
	a.saveUIState()
}

// HelpPages open from Help and the palette; they are not in the sidebar.
var HelpPages = []PageInfo{
	{"methods", "", "Methods"},
	{"file-format", "", "File format"},
	{"privacy", "", "Privacy"},
}

func init() {
	RegisterPageFactory("methods", func(a *App) Page {
		return &textPage{a: a, title: "Methods", lede: "How each number is calculated, stated for reproducibility.", sections: methodsText}
	})
	RegisterPageFactory("file-format", func(a *App) Page { return fileFormatPage{a: a} })
	RegisterPageFactory("privacy", func(a *App) Page {
		return &textPage{a: a, title: "Privacy", lede: privacyLede, sections: privacyText}
	})
}

// ---- theme

// loadTheme reads the stored theme once per run.
func (a *App) loadTheme() {
	a.themeOnce.Do(func() {
		a.Go(func() func() {
			got, err := a.B.Preferences()
			return func() {
				if err == nil {
					a.setTheme(got)
				}
			}
		})
	})
}

// setTheme records the theme of a preferences answer.
func (a *App) setTheme(got core.Obj) {
	if theme, ok := got["theme"].(string); ok {
		a.theme = theme
	}
}

// applyTheme replaces the system appearance when the preference is light
// or dark.
func (a *App) applyTheme(c *ui.Context) {
	switch a.theme {
	case "light":
		if a.lightTheme == nil {
			a.lightTheme = ui.LightTheme()
		}
		c.SetTheme(a.lightTheme)
	case "dark":
		if a.darkTheme == nil {
			a.darkTheme = ui.DarkTheme()
		}
		c.SetTheme(a.darkTheme)
	}
}

// chooseTheme applies and saves a theme preference.
func (a *App) chooseTheme(theme string) {
	a.theme = theme
	a.Go(func() func() {
		_, err := a.B.SetPreferences(core.Obj{"theme": theme})
		return func() {
			if err != nil {
				a.Toast("Could not save the appearance: %v", err)
			}
		}
	})
}

func (a *App) themePref(c *ui.Context) {
	current := themeLabels[0]
	for i, v := range themeValues {
		if v == a.theme {
			current = themeLabels[i]
		}
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Appearance").Width(120)
		ui.Select(c.Key("pref-theme"), &current, themeLabels).Label("Appearance").Width(180).OnChange(func() {
			for i, label := range themeLabels {
				if label == current {
					a.chooseTheme(themeValues[i])
				}
			}
		})
	})
}

// ---- motion

// The Motion preference is the web's Display › Motion: Animated or
// Reduced, following the system's Reduce Motion until chosen. It belongs
// to this computer, so it is kept in DataDir/display.json.
const (
	motionFull    = "full"
	motionReduced = "reduced"
)

var motionLabels = []string{"Animated", "Reduced"}

func (a *App) loadMotion() string {
	if a.DataDir == "" {
		return ""
	}
	var stored struct {
		Motion string `json:"motion"`
	}
	raw, err := os.ReadFile(filepath.Join(a.DataDir, "display.json"))
	if err != nil || json.Unmarshal(raw, &stored) != nil {
		return ""
	}
	if stored.Motion == motionFull || stored.Motion == motionReduced {
		return stored.Motion
	}
	return ""
}

func (a *App) chooseMotion(motion string) {
	a.motion = motion
	if a.DataDir == "" {
		return
	}
	raw, _ := json.MarshalIndent(map[string]string{"motion": motion}, "", "  ")
	err := os.MkdirAll(a.DataDir, 0o755)
	if err == nil {
		err = core.WriteFileAtomic(filepath.Join(a.DataDir, "display.json"), raw)
	}
	if err != nil {
		a.Toast("Could not save the motion setting: %v", err)
	}
}

// reduceMotion reports whether changes show at once: Reduced chosen, or
// nothing chosen and the system asks for less motion. (Animated cannot
// undo the system's setting: mygo's own transitions follow it.)
func (a *App) reduceMotion(c *ui.Context) bool {
	return a.motionReducedWith(c.Preferences().ReduceMotion)
}

// motionReducedWith is reduceMotion for the system's setting.
func (a *App) motionReducedWith(system bool) bool {
	return a.motion == motionReduced || a.motion == "" && system
}

// applyMotion makes this frame's animations go to their targets at once
// when motion is reduced.
func (a *App) applyMotion(c *ui.Context) {
	a.systemReduce = c.Preferences().ReduceMotion
	if a.reduceMotion(c) {
		c.DisableAnimations()
	}
}

func (a *App) motionPref(c *ui.Context) {
	t := c.Theme()
	selected := 0
	if a.reduceMotion(c) {
		selected = 1
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Motion").Width(120)
		if ui.Segmented(c.Key("pref-motion"), &selected, motionLabels...).Label("Motion").Changed() {
			a.chooseMotion([]string{motionFull, motionReduced}[selected])
		}
	})
	ui.Text(c, "Reduced keeps every change instant; it follows your system setting until you choose here.").FontSize(12).TextColor(t.TextMuted)
}

// ---- command palette

// The palette is the web's (web/interaction.js): every entry is scored as
// a fuzzy match of the query, ↑/↓ and Tab move the highlighted entry, and
// Enter runs it.

type paletteState struct {
	open  bool
	query string
	// active is the highlighted entry of the matches of shownFor.
	active   int
	shownFor string
	// reveal scrolls the highlighted entry into view in the next frame.
	reveal bool
}

// command is one palette entry: its group, title, the shortcut that does
// the same (hint) and whether it is where the window is (current).
type command struct {
	group, title, hint string
	current            bool
	run                func()
}

func (a *App) openPalette() {
	a.palette = paletteState{open: true}
	a.chord = false
}

// commands are the palette's entries for the current state, by group.
func (a *App) commands() []command {
	var out []command
	if a.Project == nil {
		out = append(out, a.startCommands()...)
	} else {
		place := a.place()
		for _, d := range pageOrder {
			d := d
			if d.page == "explore" && d.tab == "interactions" {
				// Explore as it was left, before its tabs
				out = append(out, command{group: "Go to", title: "Go to Explore", run: func() { a.Navigate("explore", nil) }})
			}
			title := "Go to " + d.title
			if d.tab != "" {
				title = "Explore › " + d.title
			}
			out = append(out, command{group: "Go to", title: title, hint: "g " + d.chord, current: place == d, run: func() { a.goTo(d) }})
		}
		for _, p := range HelpPages {
			id := p.ID
			out = append(out, command{group: "Go to", title: "Open " + p.Title, current: a.Page == id, run: func() { a.Navigate(id, nil) }})
		}
		// the top bar's samples: all merged, a condition or one sample
		options := a.SelectionOptions()
		for i, label := range SelectionLabels(options) {
			key := options[i].Key
			out = append(out, command{group: "Datasets", title: "Analyse " + label, current: key == a.Selected.Key, run: func() { a.selectKey(key) }})
		}
		out = append(out, a.pageCommands()...)
		out = append(out, command{group: "General", title: "Show tasks", run: func() { a.drawerOpen = true }})
		for _, label := range densityLabels {
			label := label
			out = append(out, command{group: "General", title: "Interactions: " + label, current: nameOr(a.Density, densityLabels[0]) == label,
				run: func() { a.setDensity(label) }})
		}
	}
	out = append(out, command{group: "General", title: "Help for this page", run: func() { a.helpOpen = true }},
		command{group: "General", title: "Preferences…", run: a.openPrefs})
	if a.Project == nil {
		// with a project, Privacy is among Go to's help pages
		out = append(out, command{group: "General", title: "Privacy", run: a.openPrivacy})
	}
	for i, v := range themeValues {
		theme := v
		out = append(out, command{group: "General", title: "Appearance: " + themeLabels[i], current: a.theme == theme, run: func() { a.chooseTheme(theme) }})
	}
	// the effective setting, the system's Reduce Motion included (the
	// web's root.dataset.motion)
	if a.motionReducedWith(a.systemReduce) {
		out = append(out, command{group: "General", title: "Turn motion on", run: func() { a.chooseMotion(motionFull) }})
	} else {
		out = append(out, command{group: "General", title: "Reduce motion", run: func() { a.chooseMotion(motionReduced) }})
	}
	if a.Project != nil {
		// the web's "Start over…"
		out = append(out, command{group: "General", title: "Close project", run: a.Close})
	}
	return out
}

// startCommands are the start page's actions.
func (a *App) startCommands() []command {
	s := &a.start
	var out []command
	if !s.preparing {
		out = append(out, command{group: "Start", title: "Open project…", run: a.chooseProject})
	}
	out = append(out, command{group: "Start", title: "New project", run: func() { s.focusName = true }})
	if !s.creating && !s.preparing && s.newParent != "" {
		out = append(out, command{group: "Start", title: "Try example", run: func() { a.createExample(newProjectPath(s.newParent, ExampleProjectName)) }})
	}
	if !s.preparing {
		for _, r := range a.recent {
			path := r.Path
			out = append(out, command{group: "Recent projects", title: "Open " + nameOr(r.Name, filepath.Base(r.Path)), run: func() { a.Open(path) }})
		}
	}
	return out
}

// selectKey chooses the top bar's samples, as its picker does.
func (a *App) selectKey(key string) {
	a.Selected.Key, a.autoKey = key, ""
	a.saveUIState()
}

// paletteScore is the web's fuzzy match (interaction.js score): the query,
// spaces removed, is a subsequence of the title (any case); consecutive
// letters and letters starting a word score higher. ok is false when the
// title does not hold the query.
func paletteScore(title, query string) (score float64, ok bool) {
	text := []rune(strings.ToLower(title))
	q := []rune(strings.Join(strings.Fields(strings.ToLower(query)), ""))
	from, streak := 0, 0
	for i, ch := range q {
		at := -1
		for j := from; j < len(text); j++ {
			if text[j] == ch {
				at = j
				break
			}
		}
		if at < 0 {
			return 0, false
		}
		wordStart := at == 0 || strings.ContainsRune(" \t\n-·/(", text[at-1])
		if at == from && i > 0 {
			streak++
		} else {
			streak = 0
		}
		score += 1 + float64(streak)*2 - float64(min(at-from, 6))*0.15
		if wordStart {
			score += 3
		}
		from = at + 1
	}
	return score, true
}

// matchCommands keeps the commands matching query, best first (in their
// order for an empty query or a tie).
func matchCommands(all []command, query string) []command {
	type scored struct {
		cmd   command
		score float64
	}
	var hits []scored
	for _, cmd := range all {
		if score, ok := paletteScore(cmd.title, query); ok {
			hits = append(hits, scored{cmd, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]command, len(hits))
	for i, h := range hits {
		out[i] = h.cmd
	}
	return out
}

func (a *App) paletteDialog(c *ui.Context) {
	s := &a.palette
	if !s.open {
		return
	}
	t := c.Theme()
	query := strings.TrimSpace(s.query)
	matches := matchCommands(a.commands(), query)
	if query != s.shownFor {
		s.shownFor, s.active = query, 0
	}
	s.active = max(0, min(s.active, len(matches)-1))
	run := func(cmd command) {
		s.open = false
		cmd.run()
	}
	move := func(by int) {
		if n := len(matches); n > 0 {
			s.active = (s.active + by + n) % n
			s.reveal = true
		}
	}
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(480).Gap(8).Children(func() {
			input := ui.TextInput(c.Key("palette-query"), &s.query).Label("Search pages and actions").Placeholder("Search pages and actions").
				FillWidth().AutoFocus().OnSubmit(func() {
				if s.active < len(matches) {
					run(matches[s.active])
				}
			})
			// the keys move the highlight while the query keeps the focus;
			// Ctrl+N and Ctrl+P as the web's, ⌘K closes it again
			if input.Shortcut(0, ui.KeyDown) || input.Shortcut(0, ui.KeyTab) || input.Shortcut(ui.Ctrl, ui.KeyN) {
				move(1)
			}
			if input.Shortcut(0, ui.KeyUp) || input.Shortcut(ui.Shift, ui.KeyTab) || input.Shortcut(ui.Ctrl, ui.KeyP) {
				move(-1)
			}
			if input.Shortcut(ui.Cmd, ui.KeyK) {
				s.open = false
			}
			reveal := s.reveal
			s.reveal = false
			ui.Scroll(c).MaxHeight(380).Gap(2).Children(func() {
				group := ""
				for i, cmd := range matches {
					if query == "" && cmd.group != group {
						group = cmd.group
						ui.Text(c, group).FontSize(11).Bold().TextColor(t.TextMuted).Padding(6, 8, 2, 8)
					}
					i, cmd := i, cmd
					a.paletteItem(c.Key(cmd.group+"|"+cmd.title), cmd, i == s.active, query != "", reveal,
						func() { run(cmd) }, func() { s.active = i })
				}
				if len(matches) == 0 {
					ui.Text(c, "No matching page or action").TextColor(t.TextMuted).Padding(8)
				}
			})
			ui.Text(c, "↑ ↓ move · ↵ run · g then a letter jumps · [ ] previous / next page").FontSize(11).TextColor(t.TextMuted)
		})
	})
}

// paletteItem is one entry; the highlighted one has the accent's tint.
func (a *App) paletteItem(c *ui.Context, cmd command, active, showGroup, reveal bool, run, hover func()) {
	t := c.Theme()
	item := ui.ButtonBase(c).FillWidth().Justify(ui.Start).Gap(8).Padding(6, 8).Radius(6).Label(cmd.title).Selected(active)
	if active {
		item.Background(t.Accent.Alpha(0.14))
		if reveal {
			item.ScrollIntoView()
		}
	}
	item.Children(func() {
		ui.Text(c, cmd.title).Grow(1).Shrink(1).MinWidth(0).SingleLine()
		if cmd.current {
			ui.Text(c, "Current").FontSize(11).TextColor(t.TextMuted)
		}
		if cmd.hint != "" {
			ui.Text(c, cmd.hint).FontSize(11).Padding(1, 5).Radius(4).Border(1, t.Border).TextColor(t.TextMuted)
		}
		if showGroup {
			ui.Text(c, cmd.group).FontSize(11).TextColor(t.TextMuted)
		}
	})
	if item.Hovered() && !active {
		hover()
	}
	if item.Clicked() {
		run()
	}
}

// ---- shortcuts

// pageDest is a page of the navigation, or a tab of Explore, with the
// letter that goes to it after "g" (the web's chords, interaction.js).
type pageDest struct {
	page, tab, title, chord string
	key                     ui.Key
}

// pageOrder is the order [ and ] step through, Explore's tabs in place of
// Explore.
var pageOrder = []pageDest{
	{"data", "", "Data", "d", ui.KeyD},
	{"qc", "", "QC", "o", ui.KeyO},
	{"explore", "interactions", "Interactions", "i", ui.KeyI},
	{"explore", "contact", "Contact map", "c", ui.KeyC},
	{"explore", "viewpoint", "Viewpoint", "v", ui.KeyV},
	{"explore", "region", "Region", "r", ui.KeyR},
	{"explore", "energies", "Binding energies", "b", ui.KeyB},
	{"compare", "", "Compare", "p", ui.KeyP},
	{"structure", "", "Structure", "s", ui.KeyS},
	{"report", "", "Report", "e", ui.KeyE},
}

// chordOtherKeys end a waiting "g" without going anywhere: the keys that
// type, and Enter, Esc, Backspace and Delete. (Tab, Space and the arrows
// keep moving the focus and scrolling instead; they let the "g" time out.)
var chordOtherKeys = func() []ui.Key {
	keys := []ui.Key{ui.KeyEnter, ui.KeyEscape, ui.KeyBackspace, ui.KeyDelete,
		ui.KeyMinus, ui.KeyEqual, ui.KeyComma, ui.KeyPeriod, ui.KeySlash, ui.KeySemicolon, ui.KeyQuote,
		ui.KeyBracketLeft, ui.KeyBracketRight, ui.KeyBackslash, ui.KeyBackquote,
		ui.Key0, ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}
	letters := []ui.Key{ui.KeyA, ui.KeyB, ui.KeyC, ui.KeyD, ui.KeyE, ui.KeyF, ui.KeyG, ui.KeyH, ui.KeyI, ui.KeyJ, ui.KeyK, ui.KeyL, ui.KeyM,
		ui.KeyN, ui.KeyO, ui.KeyP, ui.KeyQ, ui.KeyR, ui.KeyS, ui.KeyT, ui.KeyU, ui.KeyV, ui.KeyW, ui.KeyX, ui.KeyY, ui.KeyZ}
	for _, key := range letters {
		if !slices.ContainsFunc(pageOrder, func(d pageDest) bool { return d.key == key }) {
			keys = append(keys, key)
		}
	}
	return keys
}()

// chordWait is how long a "g" waits for its letter.
const chordWait = 1600 * time.Millisecond

// place is the page shown, with Explore's tab.
func (a *App) place() pageDest {
	for _, d := range pageOrder {
		if d.page != a.Page {
			continue
		}
		if d.tab == "" {
			return d
		}
		if p, ok := a.pages["explore"].(*explorePage); ok && p.tab < len(exploreTabIDs) && exploreTabIDs[p.tab] == d.tab {
			return d
		}
	}
	return pageDest{page: a.Page}
}

func (a *App) goTo(d pageDest) {
	if d.tab != "" {
		a.Navigate(d.page, map[string]any{"tab": d.tab})
		return
	}
	a.Navigate(d.page, nil)
}

// shortcuts handles the window's keys: ⌘K, / and ? open the palette; with
// a project, g then a letter goes to a page and [ ] to the previous or next
// one. A text field being typed in takes the plain keys first, and none
// works behind a dialog.
func (a *App) shortcuts(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyK) || c.Shortcut(0, ui.KeySlash) || c.Shortcut(ui.Shift, ui.KeySlash) {
		a.openPalette()
		return
	}
	if a.Project == nil {
		a.chord = false
		return
	}
	if a.chord {
		if wait := chordWait - c.Now().Sub(a.chordAt); wait > 0 {
			c.After(wait)
		} else {
			a.chord = false
		}
	}
	if a.chord {
		// a page's letter (Shift too, as the web lower-cases the key) goes
		// there; any other key ends the chord
		for _, d := range pageOrder {
			if c.Shortcut(0, d.key) || c.Shortcut(ui.Shift, d.key) {
				a.chord = false
				a.goTo(d)
				return
			}
		}
		for _, key := range chordOtherKeys {
			if c.Shortcut(0, key) || c.Shortcut(ui.Shift, key) {
				a.chord = false
				return
			}
		}
	}
	if c.Shortcut(0, ui.KeyG) {
		a.chord, a.chordAt = true, c.Now()
		c.After(chordWait)
	}
	if c.Shortcut(0, ui.KeyBracketLeft) {
		a.stepPage(-1)
	}
	if c.Shortcut(0, ui.KeyBracketRight) {
		a.stepPage(1)
	}
}

// stepPage goes to the previous (by -1) or next (1) page of pageOrder,
// round; a page outside it (Methods…) stays.
func (a *App) stepPage(by int) {
	place := a.place()
	for i, d := range pageOrder {
		if d == place {
			a.goTo(pageOrder[(i+by+len(pageOrder))%len(pageOrder)])
			return
		}
	}
}

// chordHint lists the letters while a "g" waits for one, as the web's.
func (a *App) chordHint(c *ui.Context) {
	if !a.chord || a.Project == nil {
		return
	}
	t := c.Theme()
	var parts []string
	for _, d := range pageOrder {
		parts = append(parts, d.chord+" "+d.title)
	}
	ui.Overlay(c, func() {
		ui.Row(c).Absolute().Left(200).Right(24).Bottom(24).Children(func() {
			ui.Text(c, "g then: "+strings.Join(parts, " · ")).FontSize(12).Padding(8, 12).Radius(8).
				Background(t.Background).Border(1, t.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
		})
	})
}

// ---- help

// pageHelp says what each page shows, how to read it and where to go next.
var pageHelp = map[string][]string{
	"start": {"Open a project, or make a new one in a folder of your choice.",
		"Add a reference FASTA and samples (raw reads or .hyb files) on the Data page, then run them."},
	"data": {"References and samples of this project; raw files stay where they are unless copied.",
		"Each sample runs mapping → .hyb → analysis index as one task; tasks wait while the CPU or memory budget (Preferences) is used up.",
		"Next: QC for each sample's counts, or Explore once a sample is analysed."},
	"qc": {"The Overview of the samples chosen in the top bar: records, reads, RNAs, pairs, chimera types and data quality, with the web app's exports.",
		"Below it, every analysed sample and each condition with its replicates added up.",
		"Next: Explore to filter the records or map where on the RNA they fall."},
	"explore": {"Interactions lists every record; filter by scope, type, search, support, ΔG or arm position, and click a row for its sequence.",
		"Contact map: each cell counts the records whose two arms fall in that pair of bins; a diagonal band is local folding, off-diagonal spots are long-range.",
		"Viewpoint shows where along the RNA the arms bind; Region lists what touches a range and folds it.",
		"Binding energies recalculates the ΔG column of one sample's .hyb with RNAcofold or CPLcofold, as a task; the sample's own file is not changed."},
	"compare": {"Contrast two conditions with replicates: the differential test (DESeq2) says whether a contact differs; the quick view only describes normalised counts.",
		"Pick a merged interaction to locate it, open its region or fold both conditions."},
	"structure": {"Predict a secondary structure with CPLfold, CPLcofold, ViennaRNA MFE, COMRADES(ViennaRNA) or IRIS; all but ViennaRNA MFE can be guided by the selected samples' chimeric reads.",
		"Long RNAs fold in windows; the page says how long a fold should take before it starts."},
	"report":      {"The project at a glance: data, counts, differential tests and structures, as one HTML file to share, a PDF to print, or a ZIP for an AI assistant or for further analysis."},
	"methods":     {"How each number on the analysis pages is calculated, in the order of the pages."},
	"file-format": {"The columns HYB2 reads in a .hyb file, and which optional columns it uses."},
	"privacy":     {"Where your data goes (nowhere outside this computer) and the one optional public lookup."},
}

// helpKeys are the Keyboard section's keys and what they do; the page keys
// need a project.
func helpKeys(project bool) [][2]string {
	keys := [][2]string{{cmdName() + "K, / or ?", "Search pages and actions"}}
	if project {
		var letters []string
		for _, d := range pageOrder {
			letters = append(letters, d.chord+" "+d.title)
		}
		keys = append(keys, [2]string{"g then a letter", "Go to a page: " + strings.Join(letters, ", ")},
			[2]string{"[ and ]", "Previous / next page"})
	}
	return append(keys, [2]string{"Esc", "Close this panel"})
}

// helpLinks are the Reference section's pages (the web's; Import check is
// a card of the QC page).
var helpLinks = []PageInfo{{"methods", "", "Methods"}, {"file-format", "", "File format"}, {"qc", "", "Import check"}, {"privacy", "", "Privacy"}}

func (a *App) helpDialog(c *ui.Context) {
	if !a.helpOpen {
		return
	}
	t := c.Theme()
	page, title := a.Page, ""
	if a.Project == nil {
		page = "start"
	}
	for _, p := range append(append([]PageInfo{{"start", "", "Start"}}, Pages...), HelpPages...) {
		if p.ID == page {
			title = p.Title
		}
	}
	ui.Modal(c, &a.helpOpen, func() {
		ui.Column(c).Width(460).Gap(12).Children(func() {
			ui.Text(c, "Help").FontSize(18).Bold()
			ui.Text(c, "On this page: "+title).Bold()
			for i, line := range pageHelp[page] {
				ui.Text(c, string(rune('1'+i))+". "+line)
			}
			ui.Text(c, "Keyboard").Bold()
			for _, k := range helpKeys(a.Project != nil) {
				ui.Row(c).Gap(10).Children(func() {
					ui.Text(c, k[0]).FontSize(12).Bold().Width(120)
					ui.Text(c, k[1]).FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1).MinWidth(0)
				})
			}
			if a.Project != nil {
				ui.Text(c, "Reference").Bold()
				ui.Row(c).Gap(8).Wrap().Children(func() {
					for _, p := range helpLinks {
						id := p.ID
						if ui.Button(c, p.Title).Clicked() {
							a.helpOpen = false
							a.Navigate(id, nil)
						}
					}
				})
			}
		})
	})
}

// ---- Methods and File format

type textSection struct{ title, body string }

// methodsText states the desktop's rules; they are the web app's (the
// alignment rounds made them so) unless a section says otherwise.
var methodsText = []textSection{
	{"HYB file parsing", "Lines beginning with # and blank lines are skipped. A valid record has at least 15 tab-separated columns, a name for each RNA arm, and positive one-based RNA start/end coordinates in columns 7–8 and 13–14."},
	{"Counts", "Records are valid rows. The second underscore-delimited field of a collapsed HYB2 ID is the source read count (Reads); it never weights a map unless Reads is chosen as the measure. Cluster support is 1 per record, except legacy 15/16-column files, where a count_total=… annotation or a positive integer in column 16 gives it. In the 17- and 18-column layouts, the overlap score is not a count."},
	{"Chimera type", "The type column is kept as given. Records without one get the legacy Type_1–Type_13 or Intermolecular label from their two arms, as hyb_chim_types.awk."},
	{"Homodimer proxy", "A record is a homodimer proxy when both arms are on the same RNA and its overlap score is at least 5. It is independent of the chimera type, and Interactions, the contact map and Viewpoint can keep only these records or leave them out."},
	{"Overview", "Top RNAs count each record once per arm, so a record with both arms on one RNA counts twice. RNA pairs are counted in name order, so A–B and B–A are one pair. RNAs and pairs are ranked by cluster support, then name."},
	{"Contact map", "Every bin covered by arm 1 is paired with every bin covered by arm 2, and the record's weight (1, its support or its reads) is added to each pair. The bin starts at 10 nt and doubles until the map has at most 250,000 non-empty cells. Colours saturate at a quantile of the displayed values (95th by default) or a custom value; the Log scale shows log(1 + value)."},
	{"Viewpoint and Region", "Viewpoint adds each arm on the RNA to the nucleotides it covers and reports the mean per nucleotide of each bin. Region keeps the records with an arm in the range (any overlap, or fully inside), profiles them by cluster support and lists the partners by support, then name."},
	{"Compare", "The differential test is a port of the DESeq2 1.46 Wald test on the 10-nt contact maps of the replicates, followed by hyb2_compare's merge of significant cells into interactions. The quick view is descriptive: each dataset is scaled to counts per million of its library, each condition is the mean of its datasets, and the effect is log2((A + 1) / (B + 1)); it has no statistics."},
	{"RNA structure", "CPLfold predicts the structure with the web app's defaults: beam 20, 3 phase-1 candidates, an energy window of 5 kcal/mol, evidence weight α 0.5, pseudoknot penalty β 0, pseudoknots off (candidates ranked with Turner 2004; the DP09 model ranks them when pseudoknots are on); Advanced changes them. Evidence comes from the selected samples' chimeric reads on the folded region. Long RNAs fold in windows. " +
		"ViennaRNA MFE (2.7.2, Turner 2004 parameters) folds the sequence alone at a chosen temperature and minimum hairpin loop, with optional hard base pairs enforced in every loop. " +
		"COMRADES(ViennaRNA) cofolds each chimera inside the region with RNAcofold, merges the strongest 1,001 intermolecular pairs into stems, fits up to 75 stems as hard pairs in ranked and seeded random orders, and keeps the structure whose pairs carry the most evidence (at most 3,000 nt)."},
}

// privacyText is what the desktop does with your data, as the web's
// Privacy notes say it for the web app; the lookup section is the
// Preferences notice.
var privacyText = []textSection{
	{"What happens to your files", "Reads, references and .hyb files are processed on this computer by the analysis runtime bundled with the app; " +
		"nothing is uploaded. Raw files stay where they are unless you copy them into the project. " +
		"Results, task logs and the page you were on are kept in the project folder; Preferences and the list of recent projects stay in the app's settings on this computer. " +
		"The app has no analytics."},
	{"The one exception you control", "Allow external public-data lookups (RNAcentral), in Preferences: " + lookupOffText + " " +
		lookupNoticeText + " " + lookupCaveatText + " Test connection only starts the helper on this computer, and Clear cache forgets the answers kept here."},
	{"Links", "Links to sources and licences open in your web browser only when you click them."},
}

// privacyLede is the Privacy page's lede.
const privacyLede = "Your data stays on this computer: HYB2 Desktop has no server and no upload step."

// openPrivacy shows Privacy: the page with a project, over the start page
// without one.
func (a *App) openPrivacy() {
	if a.Project == nil {
		a.privacyOpen = true
		return
	}
	a.Navigate("privacy", nil)
}

// localChipWidth is the narrowest window whose top bar has room for the
// chip on its one line.
const localChipWidth = 1280

// localChip is the web's "Local only" chip: it opens Privacy.
func (a *App) localChip(c *ui.Context) {
	t := c.Theme()
	chip := ui.ButtonBase(c).Label("Local only").Tooltip("What stays on this device").Gap(7).AlignItems(ui.Center).
		Padding(4, 10).Radius(999).Border(1, t.Success.Alpha(0.3)).Background(t.Success.Alpha(0.1))
	chip.Children(func() {
		ui.Row(c).Width(7).Height(7).Radius(4).Background(t.Success)
		ui.Text(c, "Local only").FontSize(12).Bold().TextColor(t.Success)
	})
	if chip.Clicked() {
		a.openPrivacy()
	}
}

// privacyDialog is the Privacy page over the start page.
func (a *App) privacyDialog(c *ui.Context) {
	if !a.privacyOpen || a.Project != nil {
		a.privacyOpen = false
		return
	}
	t := c.Theme()
	ui.Modal(c, &a.privacyOpen, func() {
		ui.Column(c).Width(560).Gap(12).Children(func() {
			ui.Text(c, "Privacy").FontSize(18).Bold()
			ui.Text(c, privacyLede).TextColor(t.TextMuted)
			ui.Scroll(c).MaxHeight(440).Gap(12).Children(func() {
				for _, s := range privacyText {
					ui.Text(c, s.title).Bold()
					ui.Text(c, s.body)
				}
			})
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, "Close").Clicked() {
					a.privacyOpen = false
				}
			})
		})
	})
}

type textPage struct {
	a           *App
	title, lede string
	sections    []textSection
}

func (p *textPage) Show() {}

func (p *textPage) View(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(820).Padding(24).Gap(16).Children(func() {
			PageHeader(c, p.title, p.lede)
			for _, s := range p.sections {
				Card(c, s.title, func() {
					ui.Text(c, s.body).TextColor(t.Text)
				})
			}
		})
	})
}

// fileFormatRows are the .hyb columns (1-indexed).
var fileFormatRows = [][2]string{
	{"1", "Sequence / read ID; collapsed HYB2 IDs keep the source read count in the second underscore-delimited field"},
	{"2", "Hybrid sequence"},
	{"3", "ΔG, or \".\""},
	{"4", "Arm 1 RNA"},
	{"5–6", "Arm 1 read start / end"},
	{"7–8", "Arm 1 RNA start / end"},
	{"9", "Arm 1 e-value"},
	{"10", "Arm 2 RNA"},
	{"11–12", "Arm 2 read start / end"},
	{"13–14", "Arm 2 RNA start / end"},
	{"15", "Arm 2 e-value"},
	{"16", "Empty in HYB2 Desktop's 18-column output; the overlap score in standard 17-column HYB2; a legacy cluster count or count_total=… in older 16-column files"},
	{"17", "Overlap score (18 columns), or the chimera type (17 columns)"},
	{"18", "Chimera type (18 columns)"},
}

type fileFormatPage struct{ a *App }

func (fileFormatPage) Show() {}

func (fileFormatPage) View(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(820).Padding(24).Gap(16).Children(func() {
			PageHeader(c, "File format", "The tab-separated .hyb layouts HYB2 reads, as the web app does.")
			Card(c, "From raw reads to .hyb", func() {
				ui.Text(c, "Samples added as reads are mapped and called into .hyb files with 18 columns; .hyb files from the hyb pipeline (17 columns) or older ones (15 or 16) are read as they are.")
			})
			Card(c, "Columns (1-indexed)", func() {
				for _, row := range fileFormatRows {
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, row[0]).Bold().Width(56)
						ui.Text(c, row[1]).Grow(1).Shrink(1).MinWidth(0)
					})
				}
				ui.Text(c, "Records with fewer than 15 columns, a missing RNA name or invalid coordinates are counted as invalid and skipped.").
					FontSize(12).TextColor(t.TextMuted)
			})
		})
	})
}
