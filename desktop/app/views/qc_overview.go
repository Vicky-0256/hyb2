package views

// ② QC, Overview: the web's Overview page for the top bar's selection
// (analysis_overview): four metric cards, the top RNAs and RNA pairs by
// cluster support, chimera types and data quality, with the web's exports
// (summary JSON, rna_counts.csv, rna_pairs.csv; the CSVs quote every cell,
// as the web writes them).

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// ovTop is how many RNAs and pairs the page loads (the web keeps ten).
const ovTop = 10

type ovCount struct {
	Name           string  `json:"name,omitempty"`
	Gene1          string  `json:"gene1,omitempty"`
	Gene2          string  `json:"gene2,omitempty"`
	Records        int64   `json:"records"`
	Support        int64   `json:"support"`
	Reads          int64   `json:"reads"`
	ReadRecords    int64   `json:"read_records"`
	OverlapTotal   float64 `json:"overlap_total"`
	OverlapRecords int64   `json:"overlap_records"`
}

type ovType struct {
	Name    string `json:"name"`
	Records int64  `json:"records"`
	Support int64  `json:"support"`
}

// ovData is analysis_overview's result.
type ovData struct {
	Records          int64     `json:"records"`
	Support          int64     `json:"support"`
	Reads            int64     `json:"reads"`
	ReadRecords      int64     `json:"read_records"`
	WithDG           int64     `json:"with_dg"`
	OverlapRecords   int64     `json:"overlap_records"`
	HomodimerRecords int64     `json:"homodimer_records"`
	HomodimerReads   int64     `json:"homodimer_reads"`
	IntraRecords     int64     `json:"intra_records"`
	InterRecords     int64     `json:"inter_records"`
	Invalid          int64     `json:"invalid"`
	Skipped          int64     `json:"skipped"`
	UniqueRNAs       int64     `json:"unique_rnas"`
	UniquePairs      int64     `json:"unique_pairs"`
	RNAs             []ovCount `json:"rnas"`
	Pairs            []ovCount `json:"pairs"`
	Types            []ovType  `json:"types"`
}

// qcOverview is the Overview's state; key is the project, selection and
// sample ids it was loaded for.
type qcOverview struct {
	key, loadingKey string
	data            *ovData
	err             string
}

func (p *qcPage) overviewKey() string {
	a := p.a
	return a.Project.Path + "|" + a.Selected.Key + "|" + strings.Join(a.SelectionIDs(a.Selected.Key), ",")
}

// ensureOverview loads the selection's counts once per key; a guarded job,
// safe to call from View.
func (p *qcPage) ensureOverview() {
	a := p.a
	o := &p.ov
	if a.Project == nil || len(a.SelectionIDs(a.Selected.Key)) == 0 {
		return
	}
	key := p.overviewKey()
	if key == o.key || key == o.loadingKey {
		return
	}
	o.loadingKey, o.err = key, ""
	path, ids := a.Project.Path, a.SelectionIDs(a.Selected.Key)
	a.Go(func() func() {
		data, err := ovQuery(a, path, ids, ovTop)
		return func() {
			if o.loadingKey != key {
				return // a newer selection is loading
			}
			o.loadingKey, o.key = "", key
			o.data = data
			if err != nil {
				o.data, o.err = nil, err.Error()
			}
		}
	})
}

// ovQuery runs analysis_overview; limit 0 loads every RNA and pair.
func ovQuery(a *App, path string, ids []string, limit int) (*ovData, error) {
	payload := core.Obj{"sample_ids": ids}
	if limit > 0 {
		payload["limit"] = limit
	}
	result, err := a.B.Query(path, "analysis_overview", payload)
	if err != nil {
		return nil, err
	}
	var data ovData
	if err := model.Decode(result, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func (p *qcPage) overview(c *ui.Context) {
	a := p.a
	t := c.Theme()
	o := &p.ov
	label := a.SelectionLabel(a.Selected.Key)
	current := o.key == p.overviewKey()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, "Overview · "+label).FontSize(16).Bold()
			ui.Text(c, "The samples chosen in the top bar; replicates of a condition are added up.").FontSize(12).TextColor(t.TextMuted)
		})
		ready := current && o.data != nil
		if ui.Button(c, "Summary JSON…").Tooltip("Save the counts below as JSON").Disabled(!ready).Clicked() && ready {
			p.exportOverviewJSON()
		}
		if ui.Button(c, "RNA counts CSV…").Tooltip("Every RNA: arm occurrences, reads and cluster support (the web's rna_counts.csv)").Disabled(!ready).Clicked() && ready {
			p.exportOverviewCSV(false)
		}
		if ui.Button(c, "RNA pairs CSV…").Tooltip("Every RNA pair: records, reads and cluster support (the web's rna_pairs.csv)").Disabled(!ready).Clicked() && ready {
			p.exportOverviewCSV(true)
		}
	})
	switch {
	case o.err != "" && current:
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, "Could not load the overview: "+ShortError(o.err)).TextColor(t.Danger).Grow(1).Shrink(1).MinWidth(0)
			if ui.Button(c, "Retry").Clicked() {
				o.key, o.err = "", ""
			}
		})
		return
	case !current || o.data == nil:
		ui.Row(c).Gap(8).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "Loading the overview…").TextColor(t.TextMuted)
		})
		return
	}
	d := o.data
	ui.Row(c).Gap(12).Wrap().Children(func() {
		reads, readsNote := "—", "No collapsed-read counts in the sequence IDs"
		if d.ReadRecords > 0 {
			reads, readsNote = FormatNumber(d.Reads), "Recovered from "+FormatNumber(d.ReadRecords)+" sequence IDs"
		}
		ovMetric(c, "HYB records", FormatNumber(d.Records), "One valid record per row")
		ovMetric(c, "Source reads", reads, readsNote)
		ovMetric(c, "Unique RNAs", FormatNumber(d.UniqueRNAs), "Distinct RNA names across both arms")
		ovMetric(c, "RNA pairs", FormatNumber(d.UniquePairs), "Arm order normalised")
	})
	ui.Row(c).Gap(12).Wrap().AlignItems(ui.Stretch).Children(func() {
		Card(c, "Top interacting RNAs", func() { ovTopRNAs(c, d) }).Grow(1).MinWidth(300).Basis(0)
		Card(c, "Chimera types", func() { p.ovTypes(c, d) }).Grow(1).MinWidth(300).Basis(0)
	})
	ui.Row(c).Gap(12).Wrap().AlignItems(ui.Stretch).Children(func() {
		Card(c, "Top RNA pairs", func() { ovTopPairs(c, d) }).Grow(2).MinWidth(420).Basis(0)
		Card(c, "Data quality", func() { ovQuality(c, d) }).Grow(1).MinWidth(260).Basis(0)
	})
}

func ovMetric(c *ui.Context, label, value, note string) {
	t := c.Theme()
	ui.Column(c).Gap(4).Padding(12, 14).Radius(8).Border(1, t.Border).Background(t.Surface).Grow(1).MinWidth(180).Basis(0).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(22).Bold().SingleLine()
		ui.Text(c, note).FontSize(11).TextColor(t.TextMuted)
	})
}

func ovTopRNAs(c *ui.Context, d *ovData) {
	t := c.Theme()
	ui.Text(c, "Cluster support, per arm: a record with both arms on one RNA counts twice.").FontSize(12).TextColor(t.TextMuted)
	rows := d.RNAs
	if len(rows) > 5 {
		rows = rows[:5]
	}
	maximum := int64(1)
	if len(rows) > 0 && rows[0].Support > 0 {
		maximum = rows[0].Support
	}
	for _, r := range rows {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, r.Name).SingleLine().Tooltip(r.Name).Width(170).Shrink(1).MinWidth(0)
			ui.Row(c).Grow(1).Height(8).Radius(4).Background(t.Border).Children(func() {
				ui.Row(c).FillHeight().Radius(4).Background(t.Accent).
					WidthPercent(float32(max(3, 100*float64(r.Support)/float64(maximum))))
			})
			ui.Text(c, FormatNumber(r.Support)).SingleLine()
		})
	}
	if len(rows) == 0 {
		ui.Text(c, "No records.").TextColor(t.TextMuted)
	}
}

func (p *qcPage) ovTypes(c *ui.Context, d *ovData) {
	t := c.Theme()
	types := d.Types
	if len(types) > 4 {
		types = types[:4]
	}
	total := max(d.Records, 1)
	for _, ty := range types {
		share := float64(ty.Records) / float64(total)
		ui.Column(c).Gap(3).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, strings.ReplaceAll(ty.Name, "_", " ")).Bold().Grow(1).SingleLine()
				ui.Text(c, fmt.Sprintf("%s records · %s", FormatNumber(ty.Records), qcPercent(ty.Records, d.Records))).
					FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			ui.Row(c).Height(6).Radius(3).Background(t.Border).Children(func() {
				ui.Row(c).FillHeight().Radius(3).Background(t.Accent).WidthPercent(float32(100 * share))
			})
		})
	}
	ui.Row(c).Children(func() {
		if ui.Link(c, "View interactions", "").Clicked() {
			p.a.Navigate("explore", map[string]any{"tab": "interactions"})
		}
	})
}

func ovTopPairs(c *ui.Context, d *ovData) {
	t := c.Theme()
	rows := d.Pairs
	if len(rows) > 6 {
		rows = rows[:6]
	}
	ui.Row(c).Gap(8).Children(func() {
		for _, h := range []string{"RNA 1", "RNA 2"} {
			ui.Text(c, h).FontSize(12).Bold().TextColor(t.TextMuted).Grow(1).Basis(0).MinWidth(0)
		}
		for _, h := range []string{"HYB records", "Source reads", "Cluster support"} {
			ui.Text(c, h).FontSize(12).Bold().TextColor(t.TextMuted).Width(96).TextAlign(ui.End)
		}
	})
	for _, r := range rows {
		reads := "—"
		if r.ReadRecords > 0 {
			reads = FormatNumber(r.Reads)
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, r.Gene1).SingleLine().Tooltip(r.Gene1).Grow(1).Basis(0).MinWidth(0)
			ui.Text(c, r.Gene2).SingleLine().Tooltip(r.Gene2).Grow(1).Basis(0).MinWidth(0)
			for _, v := range []string{FormatNumber(r.Records), reads, FormatNumber(r.Support)} {
				ui.Text(c, v).Width(96).TextAlign(ui.End).SingleLine()
			}
		})
	}
}

func ovQuality(c *ui.Context, d *ovData) {
	t := c.Theme()
	valid := qcPercent(d.Records, d.Records+d.Invalid)
	item := func(value, label string) {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, value).FontSize(18).Bold()
			ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		})
	}
	item(valid, "valid data rows")
	if d.WithDG > 0 {
		item(qcPercent(d.WithDG, d.Records), "include a numeric ΔG value")
	} else {
		item("Not provided", "numeric ΔG values in these files")
	}
	item(FormatNumber(d.HomodimerRecords), "homodimer-proxy records (overlap ≥ 5)")
	item(FormatNumber(d.Invalid), "invalid rows skipped")
}

// ---- exports

func (p *qcPage) overviewExportName(ext string, parts ...string) string {
	return exportName(ext, append([]string{selectionName(p.a.Selected.Key)}, parts...)...)
}

func (p *qcPage) exportOverviewJSON() {
	a := p.a
	d := *p.ov.data
	payload := map[string]any{
		"application": "HYB2 Desktop", "version": nameOr(reportAppVersion(), "development build"),
		"analysis": "overview", "selection": a.SelectionLabel(a.Selected.Key),
		"sample_ids": a.SelectionIDs(a.Selected.Key), "generated": time.Now().Format(time.RFC3339),
		"top": ovTop, "summary": d,
	}
	render := func() ([]byte, error) {
		out, err := json.MarshalIndent(payload, "", "  ")
		return append(out, '\n'), err
	}
	saveExport(a, "Export the overview", p.overviewExportName(".json", "overview", "summary"), render)
}

// exportOverviewCSV writes the web's rna_counts.csv (pairs false) or
// rna_pairs.csv from every RNA or pair (the page loads only the top ten).
func (p *qcPage) exportOverviewCSV(pairs bool) {
	a := p.a
	path, ids := a.Project.Path, a.SelectionIDs(a.Selected.Key)
	name, title := p.overviewExportName(".csv", "rna_counts"), "Export the RNA counts"
	if pairs {
		name, title = p.overviewExportName(".csv", "rna_pairs"), "Export the RNA pairs"
	}
	saveExport(a, title, name, func() ([]byte, error) {
		d, err := ovQuery(a, path, ids, 0)
		if err != nil {
			return nil, err
		}
		if pairs {
			return ovPairsCSV(d.Pairs), nil
		}
		return ovRNAsCSV(d.RNAs), nil
	})
}

func ovReads(r ovCount) string {
	if r.ReadRecords == 0 {
		return ""
	}
	return strconv.FormatInt(r.Reads, 10)
}

func ovOverlap(r ovCount) string {
	if r.OverlapRecords == 0 {
		return ""
	}
	return strconv.FormatFloat(r.OverlapTotal, 'f', -1, 64)
}

func ovRNAsCSV(rows []ovCount) []byte {
	out := [][]string{{"rna", "arm_occurrences", "source_reads", "source_read_count_arms", "cluster_support", "overlap_score_total", "overlap_score_arms"}}
	for _, r := range rows {
		out = append(out, []string{r.Name, itoa64(r.Records), ovReads(r), itoa64(r.ReadRecords), itoa64(r.Support), ovOverlap(r), itoa64(r.OverlapRecords)})
	}
	return quotedCSV(out)
}

func ovPairsCSV(rows []ovCount) []byte {
	out := [][]string{{"rna_1", "rna_2", "hyb_records", "source_reads", "source_read_count_records", "cluster_support", "overlap_score_total", "overlap_score_records"}}
	for _, r := range rows {
		out = append(out, []string{r.Gene1, r.Gene2, itoa64(r.Records), ovReads(r), itoa64(r.ReadRecords), itoa64(r.Support), ovOverlap(r), itoa64(r.OverlapRecords)})
	}
	return quotedCSV(out)
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// quotedCSV is the web's csvRows: every cell quoted, rows joined by "\n"
// with no final line break.
func quotedCSV(rows [][]string) []byte {
	lines := make([]string, len(rows))
	for i, row := range rows {
		cells := make([]string, len(row))
		for j, cell := range row {
			cells[j] = `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
		}
		lines[i] = strings.Join(cells, ",")
	}
	return []byte(strings.Join(lines, "\n"))
}
