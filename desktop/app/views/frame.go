package views

// The app frame, as the web app has it: the theme preference (system,
// light or dark), the ⌘K command palette, the Help panel for the page shown,
// and the Methods and File format pages.

import (
	"strings"

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
}

func init() {
	RegisterPageFactory("methods", func(a *App) Page {
		return &textPage{a: a, title: "Methods", lede: "How each number is calculated, stated for reproducibility.", sections: methodsText}
	})
	RegisterPageFactory("file-format", func(a *App) Page { return fileFormatPage{a: a} })
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

// ---- command palette

type paletteState struct {
	open  bool
	query string
}

type command struct {
	title string
	run   func()
}

func (a *App) openPalette() {
	a.palette = paletteState{open: true}
}

// commands are the palette's entries for the current state.
func (a *App) commands() []command {
	var out []command
	if a.Project != nil {
		for _, p := range Pages {
			id := p.ID
			out = append(out, command{"Go to " + p.Title, func() { a.Navigate(id, nil) }})
		}
		for _, tab := range [][2]string{{"interactions", "Interactions"}, {"contact", "Contact map"}, {"viewpoint", "Viewpoint"}, {"region", "Region"}} {
			id := tab[0]
			out = append(out, command{"Explore › " + tab[1], func() { a.Navigate("explore", map[string]any{"tab": id}) }})
		}
		for _, p := range HelpPages {
			id := p.ID
			out = append(out, command{"Open " + p.Title, func() { a.Navigate(id, nil) }})
		}
		out = append(out, command{"Show tasks", func() { a.drawerOpen = true }})
		for _, label := range densityLabels {
			label := label
			out = append(out, command{"Interactions: " + label, func() { a.setDensity(label) }})
		}
	}
	out = append(out, command{"Help for this page", func() { a.helpOpen = true }}, command{"Preferences…", a.openPrefs})
	for i, v := range themeValues {
		theme := v
		out = append(out, command{"Appearance: " + themeLabels[i], func() { a.chooseTheme(theme) }})
	}
	if a.Project != nil {
		out = append(out, command{"Close project", a.Close})
	}
	return out
}

// matchCommands keeps the commands whose title holds every word of query
// (any case).
func matchCommands(all []command, query string) []command {
	words := strings.Fields(strings.ToLower(query))
	var out []command
	for _, cmd := range all {
		title := strings.ToLower(cmd.title)
		ok := true
		for _, w := range words {
			if !strings.Contains(title, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, cmd)
		}
	}
	return out
}

func (a *App) paletteDialog(c *ui.Context) {
	s := &a.palette
	if !s.open {
		return
	}
	t := c.Theme()
	matches := matchCommands(a.commands(), s.query)
	run := func(cmd command) {
		s.open = false
		cmd.run()
	}
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(440).Gap(8).Children(func() {
			ui.TextInput(c.Key("palette-query"), &s.query).Label("Search pages and actions").Placeholder("Search pages and actions").
				FillWidth().AutoFocus().OnSubmit(func() {
				if len(matches) > 0 {
					run(matches[0])
				}
			})
			ui.Text(c, "Enter runs the first match.").FontSize(11).TextColor(t.TextMuted)
			ui.Scroll(c).MaxHeight(360).Gap(2).Children(func() {
				for _, cmd := range matches {
					cmd := cmd
					if ui.Button(c.Key(cmd.title), cmd.title).FillWidth().Clicked() {
						run(cmd)
					}
				}
				if len(matches) == 0 {
					ui.Text(c, "Nothing matches.").TextColor(t.TextMuted)
				}
			})
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
		"Viewpoint shows where along the RNA the arms bind; Region lists what touches a range and folds it."},
	"compare": {"Contrast two conditions with replicates: the differential test (DESeq2) says whether a contact differs; the quick view only describes normalised counts.",
		"Pick a merged interaction to locate it, open its region or fold both conditions."},
	"structure": {"Predict a secondary structure with CPLfold, optionally guided by the selected samples' chimeric reads.",
		"Long RNAs fold in windows; the page says how long a fold should take before it starts."},
	"report":      {"The project at a glance: data, counts, differential tests and structures, as one HTML file to share."},
	"methods":     {"How each number on the analysis pages is calculated, in the order of the pages."},
	"file-format": {"The columns HYB2 reads in a .hyb file, and which optional columns it uses."},
}

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
			ui.Text(c, "⌘K (Ctrl+K): search pages and actions · Esc: close this panel").FontSize(12).TextColor(t.TextMuted)
			if a.Project != nil {
				ui.Text(c, "Reference").Bold()
				ui.Row(c).Gap(8).Children(func() {
					for _, p := range HelpPages {
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
	{"RNA structure", "CPLfold predicts the structure with the web app's defaults: beam 20, 3 phase-1 candidates, an energy window of 5 kcal/mol, evidence weight α 0.5, pseudoknot penalty β 0, the DP09 energy model and pseudoknots off; Advanced changes them. Evidence comes from the selected samples' chimeric reads on the folded region. Long RNAs fold in windows."},
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
