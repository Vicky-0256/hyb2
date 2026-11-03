package views

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/model"
)

// View builds the window: the start page, or the project shell.
func (a *App) View(c *ui.Context) {
	a.loadTheme()
	a.applyTheme(c)
	a.applyMotion(c)
	a.shortcuts(c)
	if a.clipboard != nil {
		c.WriteClipboard(*a.clipboard)
		a.clipboard = nil
	}
	for _, m := range a.toasts {
		c.AddToast(ui.Toast{ID: m.ID, Title: m.Text})
	}
	a.toasts = nil
	a.toastsView(c)
	if a.Project == nil {
		a.startView(c)
		a.paletteDialog(c)
		a.helpDialog(c)
		a.prefsDialog(c)
		a.privacyDialog(c)
		return
	}
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		a.topBar(c)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.navigation(c)
			content := ui.Column(c).Grow(1).MinWidth(0).ClipX().Background(t.Surface).Children(func() {
				if p := a.pages[a.Page]; p != nil {
					p.View(c.Key("page-" + a.Page))
				}
			})
			a.content = content.LayoutBounds()
		})
	})
	a.chordHint(c)
	a.tasksDrawer(c)
	a.prefsDialog(c)
	a.paletteDialog(c)
	a.helpDialog(c)
}

// toastsView shows the toasts in the bottom right corner, with the theme's
// look: centred, they covered the pinned Run button of the Structure form
// in the left column, and a click on them stops their time.
func (a *App) toastsView(c *ui.Context) {
	t := c.Theme()
	fill, text := t.Inverse, t.InverseText
	if fill == ui.Transparent {
		fill = t.Text
	}
	if text == ui.Transparent {
		text = t.Background
	}
	// half the page, so that a toast stays clear of the form column
	width := float32(520)
	if a.content.W > 0 {
		width = min(width, max(260, a.content.W/2))
	}
	still := a.reduceMotion(c)
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(0, 24, 24, 0).AlignItems(ui.End).Gap(8)
		for _, ts := range toasts {
			p := ui.ToastBase(c, ts)
			box := p.Root.Row().AlignItems(ui.Center).Gap(16).Padding(10, 16).Radius(8).MaxWidth(width).
				Background(fill).TextColor(text).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.25))
			// it fades out at the end, unless motion is reduced
			const fade = 200 * time.Millisecond
			if left := p.Left(); still {
				if left > 0 {
					c.After(left)
				}
			} else if left > 0 && left < fade {
				box.Opacity(float32(left) / float32(fade))
				c.AnimationFrame()
			} else if left > 0 {
				c.After(left - fade)
			}
			box.Children(func() {
				ui.Text(c, ts.Title).Shrink(1)
				if ts.Action != "" {
					p.ActionButton().Padding(4, 10).Radius(6).TextColor(t.Accent.Mix(text, 0.35)).Children(func() {
						ui.Text(c, ts.Action).Bold()
					})
				}
			})
		}
	})
}

// navigation: the logo and project, the pages, and closing the project.
func (a *App) navigation(c *ui.Context) {
	t := c.Theme()
	page := a.Page
	ui.Column(c).Width(184).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c).Gap(8).Padding(14, 14, 10, 14).Children(func() {
			Logo(c, 26)
			ui.Text(c, a.Project.Name).Bold().MaxLines(2).Tooltip(a.Project.Path)
		})
		ui.Sidebar(c, &page, func() {
			for _, p := range Pages {
				item := ui.SidebarItem(c, p.ID, nil, p.Number+"  "+p.Title)
				if p.ID == "data" && len(a.Project.Samples) > 0 {
					item.Children(func() { ui.Badge(c, fmt.Sprint(len(a.Project.Samples))) })
				}
			}
		}).Grow(1).FillWidth().OnChange(func() { a.Navigate(page, nil) })
		ui.Column(c).Padding(10, 14, 14, 14).Children(func() {
			if ui.Button(c, "Close project").FillWidth().Tooltip("Back to the start page").Clicked() {
				a.Close()
			}
		})
	})
}

// topBar: project menu, reference, gene, selection, tasks and preferences.
func (a *App) topBar(c *ui.Context) {
	t := c.Theme()
	// it wraps in a narrow window rather than hiding its last buttons
	ui.Row(c).Gap(8).Wrap().Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		a.referencePicker(c)
		a.genePicker(c)
		a.selectionPicker(c)
		ui.Spacer(c)
		// a narrow window drops it, as the web's header; known widths only,
		// so that the bar does not wrap for a frame and move the page
		if a.content.W > 0 && a.content.W+184 >= localChipWidth {
			a.localChip(c)
		}
		active := len(a.ActiveTasks())
		label := "Tasks idle"
		if active > 0 {
			label = fmt.Sprintf("%d task%s running", active, plural(active))
		}
		if ui.Button(c, label).Clicked() {
			a.drawerOpen = !a.drawerOpen
		}
		if ui.Button(c, "⌘K").Label("Search pages and actions").Tooltip("Search pages and actions (⌘K or Ctrl+K)").Clicked() {
			a.openPalette()
		}
		if ui.Button(c, "Help").Tooltip("What this page shows and how to read it").Clicked() {
			a.helpOpen = true
		}
		if ui.Button(c, "Preferences").Clicked() {
			a.openPrefs()
		}
	})
}

// barWidths are the top bar's drop-downs' widths (reference, RNA,
// samples) for the window's width, and whether their labels show: a narrow
// window drops the labels so the bar keeps to one line; a wide one shows
// more of the long RNA names.
func (a *App) barWidths() (reference, rna, samples float32, labels bool) {
	w := a.content.W + 184 // the window: the page and the navigation
	switch {
	case a.content.W > 0 && w < 1180:
		return 90, 140, 170, false
	case w >= 1700:
		return 140, 300, 260, true
	}
	return 110, 180, 210, true
}

func (a *App) referencePicker(c *ui.Context) {
	var labels []string
	ids := map[string]string{}
	current := ""
	for _, r := range a.Project.References {
		label := r.Label
		if label == "" {
			label = r.ID
		}
		labels = append(labels, label)
		ids[label] = r.ID
		if r.ID == a.Selected.ReferenceID {
			current = label
		}
	}
	if len(labels) == 0 {
		return
	}
	width, _, _, showLabel := a.barWidths()
	if showLabel {
		barLabel(c, "Reference")
	}
	ui.Select(c.Key("reference"), &current, labels).Label("Reference").Width(width).Tooltip("Reference: " + current).OnChange(func() {
		a.Selected.ReferenceID = ids[current]
		a.normalizeSelection()
		a.saveUIState()
		// another reference changes the RNAs: the page takes them in again
		a.NavOptions = nil
		a.showPage()
	})
}

func (a *App) genePicker(c *ui.Context) {
	var names []string
	for _, g := range a.Genes() {
		names = append(names, g.Name)
	}
	if len(names) == 0 {
		return
	}
	gene := a.Selected.Gene
	_, width, _, showLabel := a.barWidths()
	if showLabel {
		barLabel(c, "RNA")
	}
	ui.Select(c.Key("gene"), &gene, names).Label("RNA").Width(width).Tooltip(gene).OnChange(func() {
		a.Selected.Gene = gene
		a.saveUIState()
	})
}

func (a *App) selectionPicker(c *ui.Context) {
	options := a.SelectionOptions()
	if len(options) == 0 {
		ui.Text(c, "No analysed samples yet").TextColor(c.Theme().TextMuted)
		return
	}
	// samples labelled alike stay apart
	labels := SelectionLabels(options)
	keys := map[string]string{}
	current := ""
	for i, o := range options {
		keys[labels[i]] = o.Key
		if o.Key == a.Selected.Key {
			current = labels[i]
		}
	}
	_, _, width, showLabel := a.barWidths()
	if showLabel {
		barLabel(c, "Samples")
	}
	ui.Select(c.Key("selection"), &current, labels).Label("Samples").Width(width).Tooltip("The samples, all merged, a condition with its replicates merged or one sample, that QC, Explore and Structure show").OnChange(func() {
		a.selectKey(keys[current])
	})
}

// ---- tasks drawer

func (a *App) tasksDrawer(c *ui.Context) {
	if !a.drawerOpen {
		return
	}
	t := c.Theme()
	ui.Overlay(c, func() {
		back := ui.Row(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Justify(ui.End).
			Background(ui.RGBA(0, 0, 0, 0.25)).Modal()
		back.Children(func() {
			panel := ui.Column(c).Width(380).FillHeight().Padding(16).Gap(10).Background(t.Background).
				Role(ui.RoleDialog).Label("Tasks")
			panel.Children(func() {
				ui.Row(c).Children(func() {
					ui.Text(c, "Tasks").FontSize(18).Bold().Grow(1)
					if ui.Button(c, "Close").Clicked() {
						a.drawerOpen = false
					}
				})
				tasks := a.TaskList()
				if len(tasks) == 0 {
					ui.Text(c, "No tasks in this project yet.").TextColor(t.TextMuted)
				}
				ui.Scroll(c).Grow(1).Gap(8).Children(func() {
					for _, task := range tasks {
						a.taskItem(c.Key(task.TaskID), task)
					}
				})
			})
			if panel.PressedOutside() || back.OverlayShortcut(0, ui.KeyEscape) {
				a.drawerOpen = false
			}
		})
	})
}

func (a *App) taskItem(c *ui.Context, task model.Task) {
	t := c.Theme()
	ui.Column(c).Gap(4).Padding(8).Radius(8).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.TaskLabel(task)).Bold().Grow(1).SingleLine()
			StatusChip(c, task.Status)
		})
		if task.Active() {
			ProgressBar(c, task)
		}
		meta := []string{FormatTime(task.StartedAt)}
		if task.FinishedAt != "" {
			meta = append(meta, "finished "+FormatTime(task.FinishedAt))
		}
		ui.Text(c, strings.Join(meta, " · ")).FontSize(12).TextColor(t.TextMuted)
		if e := task.ErrorText(); e != "" {
			ui.Text(c, truncate(e, 400)).FontSize(12).TextColor(t.Danger)
		}
		ui.Row(c).Gap(6).Children(func() {
			if task.Active() && task.Status != "cancelling" {
				if ui.Button(c, "Cancel").Clicked() {
					a.CancelTask(task.TaskID)
				}
			}
			if task.Status == "interrupted" || task.Status == "failed" || task.Status == "cancelled" {
				if ui.Button(c, "Resume").Clicked() {
					a.ResumeTask(task.TaskID)
				}
			}
			if task.Status == "succeeded" {
				if label, page, options := taskView(task); label != "" && ui.Button(c, label).Clicked() {
					a.drawerOpen = false
					a.Navigate(page, options)
				}
			}
		})
	})
}

// taskView is the drawer's button for a succeeded task: its label, and
// the page (with Navigate options) showing the task's results; no label
// when no page does.
func taskView(task model.Task) (label, page string, options map[string]any) {
	switch task.Kind {
	case "structure":
		return "View structure", "structure", map[string]any{"structureId": task.TaskID}
	case "compare":
		return "View results", "compare", map[string]any{"comparisonId": task.TaskID}
	case "energy":
		// its output is comparisons/<comparison id>/energy_L<n>.tsv
		if output, _ := task.Output.(string); output != "" {
			if dir := filepath.Dir(output); filepath.Base(filepath.Dir(dir)) == "comparisons" {
				return "View results", "compare", map[string]any{"comparisonId": filepath.Base(dir)}
			}
		}
		return "View results", "compare", nil
	case "batch_energy":
		return "View results", "explore", map[string]any{"tab": "energies"}
	case "cplfold":
		return "View structure", "structure", nil
	case "pipeline":
		return "View data", "data", nil
	}
	return "", "", nil
}

// ---- shared small widgets

// barLabel names the top bar control after it.
func barLabel(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted)
}

// StatusChip shows a task or stage status.
func StatusChip(c *ui.Context, status string) {
	t := c.Theme()
	b := ui.Badge(c, status)
	switch status {
	case "succeeded", "done", "available":
		b.Background(t.Success.Alpha(0.15)).TextColor(t.Success)
	case "failed", "interrupted", "missing", "changed":
		b.Background(t.Danger.Alpha(0.15)).TextColor(t.Danger)
	case "cancelled", "cancelling", "skipped", "pending":
		b.Background(t.TextMuted.Alpha(0.12)).TextColor(t.TextMuted)
	case "running", "queued":
		b.Background(t.Accent.Alpha(0.15)).TextColor(t.Accent)
	}
}

// ProgressBar shows a task's progress.json: a fraction when known. A sample
// pipeline's bar covers all its stages (fraction_done), so it moves while
// a stage that reports no bytes runs.
func ProgressBar(c *ui.Context, task model.Task) {
	t := c.Theme()
	fraction := task.Progress.Fraction()
	if task.FractionDone != nil {
		fraction = *task.FractionDone
	}
	ui.Progress(c, fraction).Label(a11yProgress(task))
	if task.Progress != nil && task.FractionDone != nil {
		// share done and time left first: the sample table's column is narrow
		detail := fmt.Sprintf("%.0f%% done", fraction*100)
		at, err := time.Parse(time.RFC3339, task.Progress.UpdatedAt)
		switch {
		case task.OverEstimate && err == nil:
			// the stage's running time keeps changing, so the row shows it is alive
			detail += " · " + task.Progress.Stage + " " + FormatDuration(time.Since(at).Seconds()) + " so far, longer than estimated"
		case task.OverEstimate:
			detail += " · taking longer than estimated · " + task.Progress.Stage
		default:
			if task.SecondsLeft != nil {
				detail += " · about " + FormatDuration(*task.SecondsLeft) + " left"
			}
			if task.Progress.Stage != "" {
				detail += " · " + task.Progress.Stage
			}
		}
		ui.Text(c, detail).Tooltip(detail).FontSize(12).TextColor(t.TextMuted).SingleLine()
	} else if task.Progress != nil {
		detail := task.Progress.Stage
		if task.Progress.Detail != "" {
			detail += " · " + task.Progress.Detail
		}
		if fraction >= 0 {
			detail += fmt.Sprintf(" · %.0f%%", fraction*100)
		}
		if task.SecondsLeft != nil {
			detail += " · about " + FormatDuration(*task.SecondsLeft) + " left"
		}
		ui.Text(c, detail).FontSize(12).TextColor(t.TextMuted).SingleLine()
	} else if task.Status == "queued" {
		ui.Text(c, "Waiting for CPU or memory budget").FontSize(12).TextColor(t.TextMuted)
	}
}

func a11yProgress(task model.Task) string {
	if task.Progress != nil && task.Progress.Stage != "" {
		return "Progress: " + task.Progress.Stage
	}
	return "Progress"
}

// FormatTime shows an RFC 3339 time in local time.
func FormatTime(value string) string {
	if value == "" {
		return ""
	}
	when, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return when.Local().Format("2006-01-02 15:04")
}

// FormatNumber groups thousands: 1234567 -> 1,234,567.
func FormatNumber(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	s := fmt.Sprint(n)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return sign + string(out)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// truncate keeps at most n bytes of s, cut before a character, not inside
// one.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// Field shows a visible label above a control (labels set with
// Element.Label are for assistive technology only).
func Field(c *ui.Context, label string, control func()) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(12).Bold().TextColor(c.Theme().TextMuted)
		control()
	})
}

// PageHeader is the title and subtitle every page starts with.
func PageHeader(c *ui.Context, title, subtitle string) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(24).Bold()
		if subtitle != "" {
			ui.Text(c, subtitle).TextColor(c.Theme().TextMuted)
		}
	})
}

// ---- placeholder for pages not built yet

type placeholder struct {
	a    *App
	info PageInfo
}

func (p *placeholder) Show() {}

func (p *placeholder) View(c *ui.Context) {
	ui.Column(c).Fill().Padding(24).Gap(8).Children(func() {
		ui.Text(c, p.info.Title).FontSize(24).Bold()
		ui.Text(c, "This page is being rebuilt in the native interface.").TextColor(c.Theme().TextMuted)
	})
}

// FormatDurationRange is "3–7 min" for a range, FormatDuration for one
// value (the ends within 10% of each other).
func FormatDurationRange(low, high float64) string {
	if high <= low*1.1 {
		return FormatDuration((low + high) / 2)
	}
	if low >= 60 && high < 3600 {
		return fmt.Sprintf("%.0f–%.0f min", math.Max(math.Round(low/60), 1), math.Round(high/60))
	}
	return FormatDuration(low) + " – " + FormatDuration(high)
}

// FormatDuration is a rounded duration: "40 s", "1 min 25 s", "12 min",
// "2 h 05 min".
func FormatDuration(seconds float64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%.0f s", math.Max(seconds, 1))
	case seconds < 600:
		s := int(math.Round(seconds/5)) * 5
		if s%60 == 0 {
			return fmt.Sprintf("%d min", s/60)
		}
		return fmt.Sprintf("%d min %d s", s/60, s%60)
	case seconds < 3600:
		return fmt.Sprintf("%.0f min", math.Round(seconds/60))
	}
	minutes := int(math.Round(seconds / 60))
	return fmt.Sprintf("%d h %02d min", minutes/60, minutes%60)
}
