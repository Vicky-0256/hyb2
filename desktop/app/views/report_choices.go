package views

// ⑥ Report, the researcher's choices (as web/report-page.js): the purpose
// of the analysis, which steps a selected-results report keeps, and which
// files each result package leaves out; plus "Save PDF…" (the exported
// HTML printed by a hidden web page) and "Copy AI summary" (report.md).

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// reportStepRow is one step of the whole project's record, for the list.
type reportStepRow struct {
	ID, Key, Title, Status, Detail string
}

// reportPackageFile is one file of a package, for the list.
type reportPackageFile struct {
	Path, Description string
	Bytes             int
}

// reportChoiceState is the page's choices; the pointers back its checkboxes.
type reportChoiceState struct {
	purpose  string
	keep     map[string]*bool            // step key -> in the report
	include  map[string]map[string]*bool // audience -> path -> in the package
	audience int                         // 0 = For AI, 1 = For analysis
	// the package list of the audience and choices of key
	files     []reportPackageFile
	filesKey  string
	filesBusy bool
	filesErr  string
}

var reportAudiences = []string{"ai", "analysis"}

func (s *reportChoiceState) reset() { *s = reportChoiceState{} }

// choices copies the current choices (safe to use off the main thread).
func (s *reportChoiceState) choices() reportChoices {
	ch := reportChoices{Purpose: s.purpose, Omit: map[string]bool{}, Exclude: map[string]map[string]bool{}}
	for key, kept := range s.keep {
		if !*kept {
			ch.Omit[key] = true
		}
	}
	for audience, paths := range s.include {
		ch.Exclude[audience] = map[string]bool{}
		for path, kept := range paths {
			if !*kept {
				ch.Exclude[audience][path] = true
			}
		}
	}
	return ch
}

func (s *reportChoiceState) keepBox(key string) *bool {
	if s.keep == nil {
		s.keep = map[string]*bool{}
	}
	if s.keep[key] == nil {
		kept := true
		s.keep[key] = &kept
	}
	return s.keep[key]
}

func (s *reportChoiceState) includeBox(audience, path string) *bool {
	if s.include == nil {
		s.include = map[string]map[string]*bool{}
	}
	if s.include[audience] == nil {
		s.include[audience] = map[string]*bool{}
	}
	if s.include[audience][path] == nil {
		kept := true
		s.include[audience][path] = &kept
	}
	return s.include[audience][path]
}

// reportStepRows lists the steps of the whole project's record.
func reportStepRows(d *reportData) []reportStepRow {
	r := buildResultRecord(d, time.Now())
	rows := make([]reportStepRow, len(r.Steps))
	for i, st := range r.Steps {
		rows[i] = reportStepRow{ID: st.ID, Key: st.Key, Title: st.Title, Status: st.Status, Detail: deref(st.Detail, st.StatusText)}
	}
	return rows
}

// choicesView is the "Steps, scope and purpose" card.
func (p *reportPage) choicesView(c *ui.Context) {
	t := c.Theme()
	s := &p.choice
	Field(c, "Purpose of this analysis (optional, written by you; saved in the exports as the researcher's text)", func() {
		ui.TextArea(c.Key("report-purpose"), &s.purpose).Label("Purpose of this analysis").
			Placeholder("e.g. Which ZIKV genome contacts differ between the African and Asian lineages?").Height(64)
	})
	if n := len([]rune(s.purpose)); n > rpPurposeMax {
		ui.Text(c, fmt.Sprintf("%d characters: the exports keep the first %d.", n, rpPurposeMax)).FontSize(12).TextColor(t.Warning)
	}
	ui.Text(c, "Each step has a fixed id (S01 to S13) shared with the result packages. A step that did not run shows “not_run”; its results show —, never 0. "+
		"Untick a step to make a selected-results report: the page, the HTML and PDF report and both packages then leave it out and say so.").
		FontSize(12).TextColor(t.TextMuted)
	ch := s.choices()
	if len(ch.Omit) > 0 {
		var ids []string
		for _, row := range p.steps {
			if ch.omitted(row.Key) {
				ids = append(ids, row.ID)
			}
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(6, 10).Radius(6).Background(t.Warning.Alpha(0.12)).Children(func() {
			ui.Text(c, "Selected results report: "+strings.Join(ids, ", ")+" left out.").FontSize(12).Grow(1)
			if ui.Button(c, "Include every step").Clicked() {
				for _, kept := range s.keep {
					*kept = true
				}
			}
		})
	}
	ui.Column(c).Gap(2).Children(func() {
		for _, row := range p.steps {
			ui.Row(c.Key("step-"+row.Key)).Gap(10).AlignItems(ui.Center).Padding(3, 0).Children(func() {
				ui.Checkbox(c.Key("keep"), s.keepBox(row.Key), row.ID+" "+row.Title).Disabled(row.Key == "parse").
					Label("Include " + row.ID + " " + row.Title + " in the report")
				if !*s.keepBox(row.Key) {
					ui.Text(c, "left out").FontSize(12).Italic().TextColor(t.Warning)
				}
				ui.Text(c, row.Status).FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, row.Detail).FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1).MinWidth(0).SingleLine()
			})
		}
	})
}

// packageView is the result packages' card: the files of the chosen
// audience, each of which can be left out, Save ZIP and Copy AI summary.
func (p *reportPage) packageView(c *ui.Context, d *reportData) {
	t := c.Theme()
	s := &p.choice
	audience := reportAudiences[s.audience]
	ch := s.choices()
	key := fmt.Sprintf("%p|%s|%s|%v", d, audience, ch.purposeText(), sortedKeys(ch.Omit))
	if key != s.filesKey && !s.filesBusy {
		s.filesBusy = true
		a := p.a
		a.Go(func() func() {
			r := recordFor(d, ch, time.Now())
			var files []reportPackageFile
			for _, f := range r.files(audience) {
				files = append(files, reportPackageFile{Path: f.Path, Description: f.Description, Bytes: len(f.Text)})
			}
			files = append(files, reportPackageFile{Path: "manifest.json", Description: "File list with sizes and SHA-256."})
			return func() { s.files, s.filesKey, s.filesBusy = files, key, false }
		})
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Segmented(c.Key("pkg-audience"), &s.audience, "For AI", "For further analysis").Label("Package")
		ui.Spacer(c)
		if s.audience == 0 && ui.Button(c, "Copy AI summary (report.md)").Tooltip("The template report.md, with your purpose and step selection").Clicked() {
			// recordFor reads the tests' significant.tsv: off the main
			// thread, and the clipboard is written in the next frame.
			a, project := p.a, p.project
			a.Go(func() func() {
				text := recordFor(d, ch, time.Now()).reportMarkdown()
				return func() {
					if p.project != project {
						return
					}
					a.copyText(text)
					a.Toast("AI summary copied")
				}
			})
		}
		if ui.PrimaryButton(c, "Save ZIP…").Clicked() {
			p.exportPackage(audience)
		}
	})
	ui.Text(c, map[string]string{
		"ai":       "A layered context package for an AI assistant: README, a template report.md (no language model), summary.json with every result id, status and unit, data dictionary, provenance, evidence and bounded tables.",
		"analysis": "Tables with a data dictionary for scripts and other tools: full tables, structures as dot-bracket and JSON, and the figure SVGs.",
	}[audience]).FontSize(12).TextColor(t.TextMuted)
	if s.filesKey != key {
		ui.Text(c, "Listing the files…").FontSize(12).TextColor(t.TextMuted)
	}
	left := 0
	ui.Column(c).Gap(2).Children(func() {
		for _, f := range s.files {
			box := s.includeBox(audience, f.Path)
			if !*box && !rpRequiredFiles[f.Path] {
				left++
			}
			ui.Row(c.Key(audience+"/"+f.Path)).Gap(10).AlignItems(ui.Center).Padding(2, 0).Children(func() {
				ui.Checkbox(c.Key("include"), box, f.Path).Disabled(rpRequiredFiles[f.Path]).Label("Include " + f.Path)
				if f.Bytes > 0 {
					ui.Text(c, dataFormatBytes(int64(f.Bytes))).FontSize(12).TextColor(t.TextMuted)
				}
				ui.Text(c, f.Description).FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1).MinWidth(0).SingleLine()
			})
		}
	})
	if left > 0 {
		ui.Text(c, "Files you leave out are listed in manifest.json and README.md as left out by the researcher; data_dictionary.json then describes only the tables that stay. README.md and manifest.json are always included.").
			FontSize(12).TextColor(t.TextMuted)
	}
	ui.Text(c, "Not included: the input files themselves (only names, and SHA-256 when recorded) and local paths. Everything is built on this computer; sharing a file is a separate step you take yourself.").
		FontSize(12).TextColor(t.TextMuted)
}

// exportPDF saves the report (with the choices) as a PDF.
func (p *reportPage) exportPDF() {
	d := p.data
	if d == nil {
		return
	}
	view := d.withChoices(p.choice.choices())
	saveExport(p.a, "Save the report as PDF", exportName("-report.pdf", d.Name), func() ([]byte, error) {
		html, err := renderReportHTML(view)
		if err != nil {
			return nil, err
		}
		return reportPDF(html)
	})
}

// reportPDF prints the exported HTML report to an A4 PDF in a hidden web
// page (the report's print styles apply). Tests replace it.
var reportPDF = func(html []byte) ([]byte, error) {
	win := mygo.NewWindow(mygo.WindowOptions{Title: "HYB2 report", Hidden: true, Width: 1000, Height: 1400})
	defer win.Destroy()
	page := win.Page()
	if page == nil {
		return nil, errors.New("no web page is available to print the report")
	}
	page.LoadHTML(string(html), "")
	// The report is loaded once its generator tag is in a complete document.
	ready := `return document.readyState === "complete" && !!document.querySelector('meta[name="generator"][content="HYB2 Desktop"]')`
	deadline := time.Now().Add(30 * time.Second)
	for {
		if v, err := page.Eval(ready); err == nil && v == true {
			break
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the report did not load for printing; export the HTML report and print it from a browser")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return page.PrintToPDF(mygo.PDFOptions{PageSize: mygo.PageA4, Background: true})
}
