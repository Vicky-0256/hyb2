package views

// ② QC: per-sample counts from the analysis indexes (one analysis_summary
// call for all analysed samples, as HD.loadStats does), with the replicates
// of each condition merged below them: the web Overview's per-dataset
// table, its columns first. The counts are also what the Data page's
// "Hybrids" column shows.

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

func init() {
	RegisterPageFactory("qc", func(a *App) Page { return &qcPage{a: a, stats: map[string]qcStats{}} })
	// A finished run changes a sample's index: load the counts again.
	OnTaskFinished(func(a *App, t model.Task) {
		if t.Kind != "sample_pipeline" && t.Kind != "analysis_index" {
			return
		}
		if q := qcFor(a); q != nil {
			q.invalidate()
		}
	})
}

// qcStats is one index's part of analysis_summary (or a merged sum).
// ReadRecords counts the records whose ID carries a collapsed-read count
// (the web's rawReadCountRecords): with none, Reads is shown as "—".
// UniqueRNAs and UniquePairs are one index's own (nil when merged: they
// are not additive).
type qcStats struct {
	Records     int64     `json:"records"`
	Reads       int64     `json:"reads"`
	ReadRecords int64     `json:"records_with_reads"`
	WithDG      int64     `json:"with_dg"`
	Intra       int64     `json:"intra_records"`
	Inter       int64     `json:"inter_records"`
	Types       []qcCount `json:"types"`
	UniqueRNAs  *int64    `json:"unique_rnas"`
	UniquePairs *int64    `json:"unique_pairs"`
}

type qcCount struct {
	Name    string `json:"name"`
	Records int64  `json:"records"`
}

func (s qcStats) typeRecords(name string) int64 {
	for _, t := range s.Types {
		if t.Name == name {
			return t.Records
		}
	}
	return 0
}

// qcMerge sums the counts of replicates (what analysis_summary's "merged"
// reports for the same indexes).
func qcMerge(parts []qcStats) qcStats {
	var out qcStats
	index := map[string]int{}
	for _, p := range parts {
		out.Records += p.Records
		out.Reads += p.Reads
		out.ReadRecords += p.ReadRecords
		out.WithDG += p.WithDG
		out.Intra += p.Intra
		out.Inter += p.Inter
		for _, t := range p.Types {
			i, ok := index[t.Name]
			if !ok {
				i = len(out.Types)
				index[t.Name] = i
				out.Types = append(out.Types, qcCount{Name: t.Name})
			}
			out.Types[i].Records += t.Records
		}
	}
	return out
}

// qcRow is a sample, or a condition's analysed samples added up (cond).
type qcRow struct {
	sample string // sample id
	cond   string // condition name (merged row)
}

type qcPage struct {
	a *App
	// tableRoom is the table's scroller width last frame: the table fills
	// it, or scrolls when its columns need more.
	tableRoom float32
	// guideOpen: the chimera patterns' "How to read these patterns".
	guideOpen bool
	// stats by sample id; loadedKey is the project path and the ready ids
	// they were loaded for. gen counts invalidations: an answer to a load
	// started before one (a run finished meanwhile) is dropped.
	stats     map[string]qcStats
	loadedKey string
	loading   bool
	gen       int
	err       string
	table     ui.ListState
	ov        qcOverview
	// match is the reference name matching (data_matching.go), shared
	// with the Data page.
	match refMatching
}

func qcFor(a *App) *qcPage {
	q, _ := a.pages["qc"].(*qcPage)
	return q
}

func qcReadyIDs(a *App) []string {
	var ids []string
	if a.Project != nil {
		for _, s := range a.Project.Samples {
			if s.IsReady() {
				ids = append(ids, s.ID)
			}
		}
	}
	return ids
}

func (p *qcPage) Show() { p.ensureStats() }

// ensureStats loads the counts once per project and set of analysed
// samples; it is a guarded job, safe to call from View.
func (p *qcPage) ensureStats() {
	a := p.a
	if a.Project == nil || p.loading {
		return
	}
	ids := qcReadyIDs(a)
	key := a.Project.Path + "|" + strings.Join(ids, ",")
	if key == p.loadedKey {
		return
	}
	if p.loadedKey != "" && !strings.HasPrefix(p.loadedKey, a.Project.Path+"|") {
		p.stats = map[string]qcStats{} // another project
	}
	if len(ids) == 0 {
		p.loadedKey, p.err = key, ""
		return
	}
	p.loading, p.err = true, ""
	path, gen := a.Project.Path, p.gen
	a.Go(func() func() {
		result, err := a.B.Query(path, "analysis_summary", core.Obj{"sample_ids": ids, "pairs": false})
		var parts []qcStats
		if err == nil {
			object, _ := result.(map[string]any)
			err = model.Decode(object["indexes"], &parts)
		}
		return func() {
			p.loading = false
			// another project, or counts from before a run finished: the
			// next frame loads them again
			if a.Project == nil || a.Project.Path != path || gen != p.gen {
				return
			}
			p.loadedKey = key
			if err != nil {
				p.err = err.Error()
				return
			}
			for i, part := range parts {
				if i < len(ids) {
					p.stats[ids[i]] = part
				}
			}
		}
	})
}

// invalidate makes the counts, the Overview and the name matching load
// again; answers to loads already running are dropped.
func (p *qcPage) invalidate() {
	p.gen++
	p.loadedKey = ""
	p.ov.invalidate()
	p.match.invalidate()
}

func (p *qcPage) refresh() {
	p.stats = map[string]qcStats{}
	p.invalidate()
	p.ensureStats()
}

func (p *qcPage) View(c *ui.Context) {
	a := p.a
	p.ensureStats()
	p.ensureOverview()
	p.ensureMatching(a.SelectionIDs(a.Selected.Key))
	ready := qcReadyIDs(a)
	// The page scrolls: the Overview comes first and the table below it
	// takes the height of its rows.
	ui.Scroll(c).Fill().Children(func() { p.page(c, ready) })
}

func (p *qcPage) page(c *ui.Context, ready []string) {
	a := p.a
	t := c.Theme()
	ui.Column(c).FillWidth().MinWidth(0).Padding(24).Gap(16).Children(func() {
		ui.Row(c).Gap(12).Wrap().Children(func() {
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Basis(320).Children(func() {
				PageHeader(c, "Quality control", "Analysed samples: library size and chimera composition.")
			})
			if len(ready) > 0 {
				if ui.Button(c, "Refresh").Disabled(p.loading).Clicked() {
					p.refresh()
				}
			}
		})
		if len(ready) == 0 {
			Card(c, "", func() {
				ui.Text(c, "No analysed samples yet.")
				ui.Text(c, "Run the samples on the Data page; their counts appear here.").TextColor(t.TextMuted)
				ui.Row(c).Children(func() {
					if ui.PrimaryButton(c, "Go to Data").Clicked() {
						a.Navigate("data", nil)
					}
				})
			})
			return
		}
		if p.err != "" {
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, "Could not load the counts: "+ShortError(p.err)).TextColor(t.Danger).Grow(1).Shrink(1).MinWidth(0)
				if ui.Button(c, "Retry").Clicked() {
					p.refresh()
				}
			})
		}
		if p.loading && len(p.stats) == 0 {
			ui.Row(c).Gap(8).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "Loading counts…").TextColor(t.TextMuted)
			})
		}
		if len(a.SelectionIDs(a.Selected.Key)) > 0 {
			p.overview(c)
		}
		Card(c, "", func() {
			selection := a.SelectionIDs(a.Selected.Key)
			analysed := 0
			for _, id := range ready {
				if slices.Contains(selection, id) {
					analysed++
				}
			}
			qcCardTitle(c, "Per-dataset summary", FormatNumber(int64(analysed))+" of "+FormatNumber(int64(len(ready)))+" datasets analysed")
			ui.Text(c, "This page, Explore and Structure's evidence merge the datasets marked Yes; choose others in the top bar (Compare and the Report choose their own). "+
				"Condition rows add up their datasets (RNAs and pairs are not additive).").FontSize(12).TextColor(t.TextMuted)
			p.summaryTable(c, ready)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Hybrids / reads divides HYB records by source reads; the percentages are of HYB records.").
					FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1).MinWidth(0)
				if ui.Button(c, "Export TSV…").Tooltip("Save the table, every chimera type as a column").Disabled(len(p.stats) == 0).Clicked() && len(p.stats) > 0 {
					p.exportTable(ready)
				}
			})
		}).MinWidth(0)
		p.importCheck(c)
	})
}

// qcTypeNames: the four most common hybrid types over the samples, largest
// first (ties in the order met).
func (p *qcPage) qcTypeNames(ready []string) []string {
	var names []string
	total := map[string]int64{}
	for _, id := range ready {
		for _, t := range p.stats[id].Types {
			if _, seen := total[t.Name]; !seen {
				names = append(names, t.Name)
			}
			total[t.Name] += t.Records
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return total[names[i]] > total[names[j]] })
	if len(names) > 4 {
		names = names[:4]
	}
	return names
}

// rows: every analysed sample, then each condition with more than one of
// them added up (the web's per-dataset table).
func (p *qcPage) rows(ready []string) []qcRow {
	var rows []qcRow
	for _, id := range ready {
		rows = append(rows, qcRow{sample: id})
	}
	for _, cond := range p.a.Project.Conditions {
		if len(p.a.SelectionIDs("c:"+cond.Name)) > 1 {
			rows = append(rows, qcRow{cond: cond.Name})
		}
	}
	return rows
}

// members are a row's samples.
func (p *qcPage) members(r qcRow) []string {
	if r.sample != "" {
		return []string{r.sample}
	}
	return p.a.SelectionIDs("c:" + r.cond)
}

// qcAnalysed is the web's Analysed column: whether the top bar's
// selection merges a row's samples.
func qcAnalysed(selection, members []string) string {
	in := 0
	for _, id := range members {
		if slices.Contains(selection, id) {
			in++
		}
	}
	switch {
	case in == 0:
		return "No"
	case in == len(members):
		return "Yes"
	}
	return "Partly"
}

// statsFor returns a row's counts; ok is false while any part is missing.
func (p *qcPage) statsFor(r qcRow) (qcStats, bool) {
	if r.sample != "" {
		s, ok := p.stats[r.sample]
		return s, ok
	}
	var parts []qcStats
	for _, id := range p.members(r) {
		s, ok := p.stats[id]
		if !ok {
			return qcStats{}, false
		}
		parts = append(parts, s)
	}
	return qcMerge(parts), len(parts) > 0
}

// qcPercent is part/total with one decimal ("—" for no total); the report
// pages use it. The QC page writes shares as the web does (webPercent).
func qcPercent(part, total int64) string {
	if total == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(part)/float64(total))
}

// qcShare is webPercent of part/total, "—" for no total.
func qcShare(part, total int64) string {
	if total == 0 {
		return "—"
	}
	return webPercent(float64(part) / float64(total))
}

// webPercent formats a fraction as the web's Intl percent with at most one
// decimal: "50%", "54.7%", "0.1%", "1,250%".
func webPercent(fraction float64) string {
	tenths := int64(fraction*1000 + 0.5)
	if fraction < 0 {
		tenths = -int64(-fraction*1000 + 0.5)
	}
	whole, decimal := tenths/10, tenths%10
	if decimal < 0 {
		decimal = -decimal
	}
	out := FormatNumber(whole)
	if tenths < 0 && whole == 0 {
		out = "-0"
	}
	if decimal != 0 {
		out += "." + strconv.FormatInt(decimal, 10)
	}
	return out + "%"
}

func (p *qcPage) summaryTable(c *ui.Context, ready []string) {
	types := p.qcTypeNames(ready)
	rows := p.rows(ready)
	cols := []ui.TableColumn{
		{Title: "Dataset", MinWidth: 150},
		{Title: "Condition", Width: 84},
		{Title: "HYB records", Width: 128, Align: ui.End},
		{Title: "Source reads", Width: 112, Align: ui.End},
		{Title: "RNAs", Width: 64, Align: ui.End},
		{Title: "RNA pairs", Width: 92, Align: ui.End},
		{Title: "Analysed", Width: 92},
		{Title: "Hybrids / reads", Width: 132, Align: ui.End},
		{Title: "With ΔG", Width: 76, Align: ui.End},
		{Title: "Intra", Width: 72, Align: ui.End},
		{Title: "Inter", Width: 64, Align: ui.End},
	}
	for _, name := range types {
		cols = append(cols, qcTableColumn(name))
	}
	// The inline bar is scaled within samples and within conditions (merged
	// rows are sums).
	maxSample, maxCond := int64(1), int64(1)
	for _, r := range rows {
		s, ok := p.statsFor(r)
		switch {
		case !ok:
		case r.sample != "" && s.Records > maxSample:
			maxSample = s.Records
		case r.cond != "" && s.Records > maxCond:
			maxCond = s.Records
		}
	}
	p.table.Key = func(i int) any {
		r := rows[i]
		return r.sample + "|" + r.cond
	}
	p.table.Label = func(i int) string {
		if r := rows[i]; r.cond != "" {
			return r.cond
		}
		return rows[i].sample
	}
	// The scroller keeps the columns' widths out of the page's minimum
	// width: a narrow window scrolls the table, not the page.
	scroller := ui.ScrollHorizontal(c).AlignItems(ui.Stretch).Height(qcTableHeight(len(rows))).Children(func() { p.summaryRows(c, cols, rows, types, maxSample, maxCond) })
	p.tableRoom = scroller.LayoutBounds().W
}

func (p *qcPage) summaryRows(c *ui.Context, cols []ui.TableColumn, rows []qcRow, types []string, maxSample, maxCond int64) {
	a := p.a
	t := c.Theme()
	selection := a.SelectionIDs(a.Selected.Key)
	ui.Table(c, &p.table, cols, len(rows), func(row, col int) {
		r := rows[row]
		var sample *model.Sample
		if r.sample != "" {
			sample = a.Project.Sample(r.sample)
		}
		s, ok := p.statsFor(r)
		switch col {
		case 0:
			ui.Column(c).Gap(2).MinWidth(0).Children(func() {
				if sample != nil {
					id := sample.ID
					if ui.Link(c, sample.Label(), "").Label("Explore " + sample.Label()).Tooltip("Select and explore").Clicked() {
						a.Selected.Key = "s:" + id
						a.Navigate("explore", nil)
					}
					if sample.Replicate != nil {
						ui.Text(c, "rep "+dataReplicate(sample.Replicate)).FontSize(11).TextColor(t.TextMuted).SingleLine()
					}
				} else {
					label := r.cond + " · " + FormatNumber(int64(len(p.members(r)))) + " merged"
					ui.Text(c, label).Bold().SingleLine().Tooltip(label)
				}
			})
		case 1:
			cond := r.cond
			if sample != nil {
				cond = nameOr(sample.Condition, "—")
			}
			ui.Text(c, cond).SingleLine().Tooltip(cond)
		case 2:
			if !ok {
				label := "—"
				if p.loading {
					label = "loading…"
				}
				ui.Text(c, label).TextColor(t.TextMuted)
				return
			}
			scale := maxSample
			if r.cond != "" {
				scale = maxCond
			}
			width := float32(48 * float64(s.Records) / float64(scale))
			if width < 2 {
				width = 2
			}
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Width(width).Height(8).Radius(2).Background(t.Accent.Alpha(0.7))
				ui.Text(c, FormatNumber(s.Records)).SingleLine()
			})
		case qcColAnalysed:
			ui.Text(c, qcAnalysed(selection, p.members(r))).SingleLine()
		default:
			if !ok {
				ui.Text(c, "—").TextColor(t.TextMuted)
				return
			}
			ui.Text(c, qcCell(s, col, types)).SingleLine()
		}
	}).Width(max(dataColumnsWidth(cols), p.tableRoom)).Grow(1)
}

// qcTableHeight fits the table's rows (two-line cells) up to 560 px; more
// rows scroll inside it.
func qcTableHeight(rows int) float32 {
	return min(float32(40+43*rows), 560)
}

func qcTableColumn(name string) ui.TableColumn {
	return ui.TableColumn{Title: strings.ReplaceAll(name, "_", " "), ID: "type:" + name, Width: 64, Align: ui.End}
}

// The columns after Dataset, Condition and HYB records, in qcCell's
// numbering: the web's columns, then the desktop's.
const (
	qcColReads = 3 + iota
	qcColRNAs
	qcColPairs
	qcColAnalysed
	qcColHybridsPerRead
	qcColDG
	qcColIntra
	qcColInter
	qcColTypes
)

// qcReads is a row's source reads, "—" when no ID carries a read count
// (the web's Overview).
func qcReads(s qcStats) string {
	if s.ReadRecords == 0 {
		return "—"
	}
	return FormatNumber(s.Reads)
}

func qcOptional(v *int64) string {
	if v == nil {
		return "—"
	}
	return FormatNumber(*v)
}

func qcCell(s qcStats, col int, types []string) string {
	switch col {
	case qcColReads:
		return qcReads(s)
	case qcColRNAs:
		return qcOptional(s.UniqueRNAs)
	case qcColPairs:
		return qcOptional(s.UniquePairs)
	case qcColHybridsPerRead:
		if s.ReadRecords == 0 || s.Reads == 0 {
			return "—"
		}
		return fmt.Sprintf("%.2f", float64(s.Records)/float64(s.Reads))
	case qcColDG:
		return qcShare(s.WithDG, s.Records)
	case qcColIntra:
		return qcShare(s.Intra, s.Records)
	case qcColInter:
		return qcShare(s.Inter, s.Records)
	}
	if i := col - qcColTypes; i >= 0 && i < len(types) {
		return qcShare(s.typeRecords(types[i]), s.Records)
	}
	return ""
}

// exportTable writes the table as TSV with raw counts beside the shares,
// and every chimera type (the page shows the four largest). Reads are
// empty when no ID carries a read count, RNAs and pairs for merged rows.
func (p *qcPage) exportTable(ready []string) {
	a := p.a
	var types []string
	seen := map[string]bool{}
	for _, id := range ready {
		for _, ty := range p.stats[id].Types {
			if !seen[ty.Name] {
				seen[ty.Name] = true
				types = append(types, ty.Name)
			}
		}
	}
	optional := func(v *int64) string {
		if v == nil {
			return ""
		}
		return itoa64(*v)
	}
	selection := a.SelectionIDs(a.Selected.Key)
	header := []string{"row", "sample", "condition", "replicates", "hybrids", "reads", "rnas", "rna_pairs", "analysed", "with_dg", "intra", "inter"}
	header = append(header, types...)
	var rows [][]string
	for _, r := range p.rows(ready) {
		s, ok := p.statsFor(r)
		if !ok {
			continue
		}
		kind, sample, cond, n := "sample", r.sample, r.cond, 1
		if r.sample != "" {
			if m := a.Project.Sample(r.sample); m != nil {
				cond = m.Condition
			}
		} else {
			kind, n = "condition", len(p.members(r))
		}
		reads := ""
		if s.ReadRecords > 0 {
			reads = itoa64(s.Reads)
		}
		row := []string{kind, sample, cond, strconv.Itoa(n), itoa64(s.Records), reads, optional(s.UniqueRNAs), optional(s.UniquePairs),
			qcAnalysed(selection, p.members(r)), itoa64(s.WithDG), itoa64(s.Intra), itoa64(s.Inter)}
		for _, name := range types {
			row = append(row, itoa64(s.typeRecords(name)))
		}
		rows = append(rows, row)
	}
	comments := []string{"HYB2 quality control · " + a.Project.Name,
		"Counts of hybrids (records); condition rows add up their analysed replicates; one column per chimera type"}
	saveExport(a, "Export the QC table", exportName(".tsv", a.Project.Name, "qc"), func() ([]byte, error) {
		return tsvTable{Comments: comments, Header: header, Rows: rows}.Bytes()
	})
}
