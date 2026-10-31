package views

// ⑥ Report, what the researcher chooses for the result packages (as
// web/results.js and web/report-page.js): a stated purpose, a
// selected-results report that keeps only some steps (ids are not
// renumbered), and files left out of a package (README.md and
// manifest.json always stay; both list what was left out).

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// rpPurposeMax is the web's limit on the purpose text.
const rpPurposeMax = 1000

// rpRequiredFiles cannot be left out of a package.
var rpRequiredFiles = map[string]bool{"README.md": true, "manifest.json": true}

// reportChoices are the Report page's choices that shape the exports.
type reportChoices struct {
	// Purpose is the researcher's own text ("" = not stated).
	Purpose string
	// Omit are the step keys left out of a selected-results report (S01,
	// "parse", is always kept).
	Omit map[string]bool
	// Exclude are package paths left out, per audience ("ai", "analysis").
	Exclude map[string]map[string]bool
}

// purposeText is the trimmed purpose, at most rpPurposeMax characters.
func (ch reportChoices) purposeText() string {
	text := strings.TrimSpace(ch.Purpose)
	if runes := []rune(text); len(runes) > rpPurposeMax {
		text = string(runes[:rpPurposeMax])
	}
	return text
}

// omitted reports whether a step key is left out.
func (ch reportChoices) omitted(key string) bool { return key != "parse" && ch.Omit[key] }

// recordFor is the result record with the choices applied.
func recordFor(d *reportData, ch reportChoices, now time.Time) *rpRecord {
	r := buildResultRecord(d, now)
	r.setPurpose(ch.purposeText())
	keep := map[string]bool{}
	for _, st := range r.Steps {
		keep[st.Key] = !ch.omitted(st.Key)
	}
	r.selectSteps(keep)
	return r
}

// setPurpose records the researcher's purpose in the scope (null when empty).
func (r *rpRecord) setPurpose(text string) {
	if text == "" {
		r.Scope["purpose"] = nil
		return
	}
	r.Scope["purpose"] = map[string]any{"text": text, "source": "researcher"}
}

// purposeLine is report.md's purpose.
func (r *rpRecord) purposeLine() string {
	if p, ok := r.Scope["purpose"].(map[string]any); ok {
		return p["text"].(string) + " (written by the researcher)"
	}
	return "not stated by the researcher."
}

// selectSteps keeps only the steps whose key is true in keep (and S01):
// a selected-results report. The run id stays the whole project's.
func (r *rpRecord) selectSteps(keep map[string]bool) {
	kept := map[string]bool{}
	var omitted, chosen []string
	for _, st := range r.Steps {
		if keep[st.Key] || st.Key == "parse" {
			kept[st.ID] = true
			chosen = append(chosen, st.ID+" "+st.Title)
		} else {
			omitted = append(omitted, st.ID)
		}
	}
	if len(omitted) == 0 {
		return
	}
	selected := make([]string, 0, len(kept))
	for id := range kept {
		selected = append(selected, id)
	}
	sort.Strings(selected)
	r.Scope["kind"], r.Scope["label"] = "selected_results", "Selected results report"
	r.Scope["description"] = "Only the steps the researcher chose: " + strings.Join(chosen, "; ") + ". Left out: " +
		strings.Join(omitted, ", ") + ". Ids keep the project's numbering; this is not the whole project."
	r.Scope["selectedSteps"], r.Scope["omittedSteps"] = selected, omitted
	var steps []*rpStep
	for _, st := range r.Steps {
		if kept[st.ID] {
			steps = append(steps, st)
		}
	}
	r.Steps = steps
	var results []rpResult
	for _, res := range r.Results {
		if kept[res.Step] {
			results = append(results, res)
		}
	}
	r.Results = results
	var tables []*rpTable
	for _, t := range r.Tables {
		if kept[t.Step] {
			tables = append(tables, t)
		}
	}
	r.Tables = tables
	var figures []*rpFigure
	for _, f := range r.Figures {
		if kept[f.Step] {
			figures = append(figures, f)
		}
	}
	r.Figures = figures
	var warnings []map[string]string
	for _, w := range r.Warnings {
		if kept[w["step"]] {
			warnings = append(warnings, w)
		}
	}
	r.Warnings = warnings
	// structure files are all S12's; external sources S13's
	if !keep["structure"] {
		r.Structures = nil
	}
	if !keep["literature_search"] {
		r.Evidence = nil
	}
}

// selected reports whether this is a selected-results record.
func (r *rpRecord) selected() bool { return r.Scope["kind"] == "selected_results" }

// leftOut reports whether a package path is left out by the researcher.
func (r *rpRecord) leftOut(path string) bool { return !rpRequiredFiles[path] && r.exclude[path] }

// keepFiles drops the left-out files and records them (for README.md and
// manifest.json).
func (r *rpRecord) keepFiles(files []rpFile, tables []*rpTable) []rpFile {
	r.omitted = nil
	var kept []rpFile
	for _, f := range files {
		if r.leftOut(f.Path) {
			r.omitted = append(r.omitted, f.Path)
			continue
		}
		kept = append(kept, f)
	}
	for _, t := range tables {
		if r.leftOut(t.File) && !slices.Contains(r.omitted, t.File) {
			r.omitted = append(r.omitted, t.File)
		}
	}
	return kept
}
