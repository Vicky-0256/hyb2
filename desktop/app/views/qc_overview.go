package views

// ② QC, Overview: the web's Overview page for the top bar's selection
// (analysis_overview): the reference status, four metric cards, the top
// RNAs and RNA pairs by cluster support, chimera types and data quality,
// with the web's exports (hyb2-overview-summary.json, rna_counts.csv,
// rna_pairs.csv; the CSVs quote every cell, as the web writes them), and
// the web's Validation page as the Import check: the parse counts, each
// file's own counts, the invalid rows each analysis index kept with their
// reason, and its validation CSV.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// ovTop is how many RNAs and pairs the page loads (the web keeps ten).
const ovTop = 10

// ovShownErrors is how many invalid rows the Import check lists; the
// validation CSV has every row the indexes kept.
const ovShownErrors = 8

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

// ovError is one invalid .hyb line an analysis index kept: its 1-based
// line number, why it was rejected and its first 300 characters.
type ovError struct {
	Line     int64  `json:"line"`
	Error    string `json:"error"`
	Content  string `json:"content"`
	SampleID string `json:"sample_id"`
}

// ovPart is one index's own counts (analysis_overview's indexes[], in the
// order of the selection's sample ids).
type ovPart struct {
	Records          int64 `json:"records"`
	Invalid          int64 `json:"invalid"`
	HomodimerRecords int64 `json:"homodimer_records"`
}

// ovData is analysis_overview's result. Skipped counts comment and blank
// lines together; SkippedComment, SkippedBlank, CanonicalRecords and
// LegacyColumn16 are nil for indexes built before they were counted.
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
	SkippedComment   *int64    `json:"skipped_comment"`
	SkippedBlank     *int64    `json:"skipped_blank"`
	CanonicalRecords *int64    `json:"canonical_records"`
	LegacyColumn16   *int64    `json:"legacy_column16"`
	UniqueRNAs       int64     `json:"unique_rnas"`
	UniquePairs      int64     `json:"unique_pairs"`
	RNAs             []ovCount `json:"rnas"`
	Pairs            []ovCount `json:"pairs"`
	Types            []ovType  `json:"types"`
	Indexes          []ovPart  `json:"indexes"`
	// Errors: the invalid rows with a reason (a bounded sample per index);
	// OmittedErrors: invalid rows counted without one.
	Errors        []ovError `json:"errors"`
	OmittedErrors int64     `json:"omitted_errors"`
}

// qcOverview is the Overview's state; key is the project, selection and
// sample ids it was loaded for. seq counts invalidations: an answer to a
// load started before one is dropped.
type qcOverview struct {
	key, loadingKey string
	seq             int
	data            *ovData
	err             string
	// ids are the sample ids data was loaded for (indexes[] and errors
	// follow their order).
	ids []string
}

// invalidate makes the Overview load again (a run finished).
func (o *qcOverview) invalidate() {
	o.key, o.loadingKey = "", ""
	o.seq++
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
	path, ids, seq := a.Project.Path, a.SelectionIDs(a.Selected.Key), o.seq
	a.Go(func() func() {
		data, err := ovQuery(a, path, ids, ovTop)
		return func() {
			if o.loadingKey != key || o.seq != seq {
				return // a newer selection is loading, or a run finished meanwhile
			}
			o.loadingKey, o.key = "", key
			o.data, o.ids = data, ids
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

// qcCardTitle is a card's title with the web's kicker at its right.
func qcCardTitle(c *ui.Context, title, kicker string) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Text(c, title).FontSize(16).Bold().Grow(1).Shrink(1).MinWidth(0)
		if kicker != "" {
			ui.Text(c, kicker).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// qcCard is Card with a kicker.
func qcCard(c *ui.Context, title, kicker string, body func()) ui.Element {
	return Card(c, "", func() {
		qcCardTitle(c, title, kicker)
		body()
	})
}

func (p *qcPage) overview(c *ui.Context) {
	a := p.a
	t := c.Theme()
	o := &p.ov
	current := o.key == p.overviewKey()
	ready := current && o.data != nil
	ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, "Overview").FontSize(16).Bold()
			var records *int64
			if ready {
				records = &o.data.Records
			}
			ui.Text(c, ovHeading(a, a.SelectionIDs(a.Selected.Key), records)).FontSize(12).TextColor(t.TextMuted)
		})
		if ui.Button(c, "Summary JSON…").Tooltip("Save the counts below as the web's overview summary").Disabled(!ready).Clicked() && ready {
			p.exportOverviewJSON()
		}
		if ui.Button(c, "RNA counts CSV…").Tooltip("Every RNA: arm occurrences, reads and cluster support (the web's rna_counts.csv)").Disabled(!ready).Clicked() && ready {
			p.exportOverviewCSV(false)
		}
		if ui.Button(c, "RNA pairs CSV…").Tooltip("Every RNA pair: records, reads and cluster support (the web's rna_pairs.csv)").Disabled(!ready).Clicked() && ready {
			p.exportOverviewCSV(true)
		}
	})
	p.referenceStatus(c)
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
		reads, readsNote := ovSourceReads(d)
		ovMetric(c, "HYB records", FormatNumber(d.Records), "One valid tab-delimited record per row")
		ovMetric(c, "Source reads", reads, readsNote)
		ovMetric(c, "Unique RNAs", FormatNumber(d.UniqueRNAs), "Distinct RNA names across both arms")
		ovMetric(c, "RNA pairs", FormatNumber(d.UniquePairs), "RNA arm order is normalised for summary")
	})
	if n := ovInt(d.LegacyColumn16); n > 0 {
		ui.Text(c, "Legacy column 16 detected. "+FormatNumber(n)+" 16-column rows contain a positive integer in column 16 without a chimera-type column. "+
			"They are treated as legacy cluster support, following the bundled HYB2 merge format. If these rows are instead truncated standard "+
			"17-column records, add column 17 before analysis.").FontSize(12).Padding(8, 10).Radius(6).Background(t.Warning.Alpha(0.12))
	}
	ui.Row(c).Gap(12).Wrap().AlignItems(ui.Stretch).Children(func() {
		qcCard(c, "Top interacting RNAs", "Cluster support, per arm", func() { ovTopRNAs(c, d) }).Grow(1).MinWidth(300).Basis(0)
		qcCard(c, "Chimera patterns", "HYB arm relationships", func() { p.ovTypes(c, d) }).Grow(1).MinWidth(300).Basis(0)
	})
	ui.Row(c).Gap(12).Wrap().AlignItems(ui.Stretch).Children(func() {
		qcCard(c, "Top RNA pairs", "Normalised pairs", func() { ovTopPairs(c, d) }).Grow(2).MinWidth(420).Basis(0)
		qcCard(c, "Data quality", "Local validation", func() { ovQuality(c, d, len(o.ids)) }).Grow(1).MinWidth(260).Basis(0)
	})
}

// ovFilesName names samples as the web's headings do: its file for one
// sample, "N datasets" for several.
func ovFilesName(a *App, ids []string) string {
	if len(ids) == 1 {
		name, _, _ := webHybFile(a, ids[0])
		return name
	}
	return FormatNumber(int64(len(ids))) + " datasets"
}

// ovHeading is the web Overview's "<file> · loaded locally · N valid
// records" (the records once loaded).
func ovHeading(a *App, ids []string, records *int64) string {
	text := ovFilesName(a, ids) + " · loaded locally"
	if records != nil {
		text += " · " + FormatNumber(*records) + " valid records"
	}
	return text
}

// ovSourceReads is the Source reads card's value and note: "—" when no
// sequence ID carries a collapsed-read count.
func ovSourceReads(d *ovData) (string, string) {
	if d.ReadRecords == 0 {
		return "—", "No HYB2 collapsed-read counts found in sequence IDs"
	}
	return FormatNumber(d.Reads), "Recovered from " + FormatNumber(d.ReadRecords) + " sequence IDs"
}

func ovInt(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// referenceStatus is the web Overview's reference callout for the
// selection: how many of its RNA names match a reference sequence, with a
// way to the Data page's name matching (on the same selection); that the
// samples have no reference while the project has some; that no reference
// is loaded; or why the matching could not load, with Retry.
func (p *qcPage) referenceStatus(c *ui.Context) {
	a := p.a
	t := c.Theme()
	ids := a.SelectionIDs(a.Selected.Key)
	m, err, _ := p.matchingFor(ids)
	if m == nil && err == "" {
		return // loading
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Wrap().Padding(10, 14).Radius(8).Border(1, t.Border).Background(t.Surface).Children(func() {
		if m == nil {
			ui.Text(c, "Name matching unavailable: "+ShortError(err)).TextColor(t.Danger).Grow(1).Shrink(1).MinWidth(0)
			if ui.Button(c, "Retry").Label("Retry name matching").Clicked() {
				p.retryMatching(ids)
			}
			return
		}
		if len(m.References) > 0 {
			sequences := FormatNumber(m.Sequences) + " FASTA sequences · "
			if m.Sequences == 1 {
				sequences = "1 FASTA sequence · "
			}
			ui.Text(c, "Reference sequences loaded").Bold()
			ui.Text(c, sequences+FormatNumber(m.Matched)+" of "+FormatNumber(m.RNANames)+" HYB RNA names matched locally.").
				TextColor(t.TextMuted).Grow(1).Shrink(1).MinWidth(0)
			if ui.Button(c, "Review mapping").Tooltip("Open the name matching on the Data page").Clicked() {
				a.Navigate("data", map[string]any{"section": "data-mapping"})
			}
			return
		}
		if len(a.Project.References) > 0 {
			ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				ui.Text(c, "Reference sequences are not set for these samples").Bold()
				ui.Text(c, "Interaction analysis and contact maps are available now. Set a reference on these samples on the Data page to extract reference regions and prepare structure input.").
					FontSize(12).TextColor(t.TextMuted)
			})
			if ui.Button(c, "Set reference on Data").Tooltip("Select the samples on the Data page and use Set reference…").Clicked() {
				a.Navigate("data", nil)
			}
			return
		}
		ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Children(func() {
			ui.Text(c, "Reference sequences are not loaded").Bold()
			ui.Text(c, "Interaction analysis and contact maps are available now. Add a local FASTA to extract reference regions and prepare structure input.").
				FontSize(12).TextColor(t.TextMuted)
		})
		if ui.Button(c, "Add reference FASTA").Tooltip("Add a reference on the Data page and set it for these samples").Clicked() {
			a.Navigate("data", nil)
		}
	})
}

// importCheck is the Import check card under the per-sample table, once
// the Overview has loaded for the current selection; its title is the web
// Validation card's heading.
func (p *qcPage) importCheck(c *ui.Context) {
	if d := p.ov.data; d != nil && p.ov.key == p.overviewKey() && len(p.a.SelectionIDs(p.a.Selected.Key)) > 0 {
		qcCard(c, ovReadyHeading(p.a, p.ov.ids), "Import check", func() { p.ovImportCheck(c, d) }).MinWidth(0)
	}
}

// ovReadyHeading is the web's "<file> is ready" or "N datasets are ready
// (counts added up)".
func ovReadyHeading(a *App, ids []string) string {
	if len(ids) > 1 {
		return ovFilesName(a, ids) + " are ready (counts added up)"
	}
	return ovFilesName(a, ids) + " is ready"
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
	ui.Text(c, "Each record counts once per arm on the RNA, so a record with both arms on one RNA counts twice.").FontSize(12).TextColor(t.TextMuted)
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
			// a third of the card, so a wide window shows long names whole
			ui.Text(c, r.Name).SingleLine().Tooltip(r.Name).WidthPercent(34).MinWidth(120).MaxWidth(380).Shrink(1)
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
	// as the web's Overview: each type's code and name, its two arms on the
	// reference, its records and share
	ui.Row(c).Gap(12).Wrap().Children(func() {
		for _, ty := range types {
			share := float64(ty.Records) / float64(total)
			ui.Column(c.Key(ty.Name)).Gap(3).Grow(1).Basis(220).MinWidth(200).Children(func() {
				ui.Text(c, patternCodeLabel(ty.Name)).FontSize(11).Bold().TextColor(t.TextMuted)
				ui.Text(c, patternName(ty.Name)).Bold().SingleLine().Tooltip(patternName(ty.Name))
				chimeraDiagram(c, ty.Name, 220)
				ui.Text(c, fmt.Sprintf("%s records · %s", FormatNumber(ty.Records), webPercent(share))).
					FontSize(12).TextColor(t.TextMuted).SingleLine()
				ui.Row(c).Height(6).Radius(3).Background(t.Border).Children(func() {
					ui.Row(c).FillHeight().Radius(3).Background(t.Accent).WidthPercent(float32(100 * share))
				})
			})
		}
	})
	chimeraGuide(c, &p.guideOpen)
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

// ovQuality is the web's Data quality card; files is how many files the
// counts add up ("in this file" for one, "in these files" for more).
func ovQuality(c *ui.Context, d *ovData, files int) {
	t := c.Theme()
	valid := webPercent(0)
	if d.Records+d.Invalid > 0 {
		valid = webPercent(float64(d.Records) / float64(d.Records+d.Invalid))
	}
	item := func(value, label string) {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, value).FontSize(18).Bold()
			ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		})
	}
	item(valid, "valid data rows")
	if d.WithDG > 0 {
		item(qcShare(d.WithDG, d.Records), "include a numeric dG value")
	} else if files > 1 {
		item("Not provided", "numeric dG values in these files")
	} else {
		item("Not provided", "numeric dG values in this file")
	}
	item(FormatNumber(d.HomodimerRecords), "homodimer-proxy records (overlap ≥ 5)")
	item(FormatNumber(d.Invalid), "invalid rows skipped")
}

// ovImportItem is one count of the Import check.
type ovImportItem struct {
	value int64
	label string
}

// ovImportItems are the web Validation page's counts. An index built
// before comment and blank lines were told apart shows them together.
func ovImportItems(d *ovData) []ovImportItem {
	items := []ovImportItem{{d.Records, "Valid records"}}
	if d.SkippedComment != nil && d.SkippedBlank != nil {
		items = append(items, ovImportItem{*d.SkippedComment, "Skipped comment lines"}, ovImportItem{*d.SkippedBlank, "Skipped blank lines"})
	} else {
		items = append(items, ovImportItem{d.Skipped, "Skipped comment and blank lines"})
	}
	items = append(items, ovImportItem{d.Invalid, "Invalid records"})
	if d.CanonicalRecords != nil {
		items = append(items, ovImportItem{*d.CanonicalRecords, "Standard 17-column records"})
	}
	return append(items, ovImportItem{d.OverlapRecords, "Records with overlap score"},
		ovImportItem{d.ReadRecords, "IDs with collapsed-read count"}, ovImportItem{d.HomodimerRecords, "Homodimer-proxy records"})
}

// ovImportCheck is the web's Validation page: the parse counts, each
// file's own counts, the first invalid rows with their reason and the
// validation CSV.
func (p *qcPage) ovImportCheck(c *ui.Context, d *ovData) {
	t := c.Theme()
	ui.Text(c, "The parser expects at least 15 tab-separated fields, RNA names in both arms, and positive one-based RNA start/end coordinates. "+
		"Standard HYB2 17-column annotations are retained explicitly.").FontSize(12).TextColor(t.TextMuted)
	ui.Row(c).Gap(24).Wrap().Children(func() {
		for _, item := range ovImportItems(d) {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, FormatNumber(item.value)).FontSize(18).Bold()
				ui.Text(c, item.label).FontSize(12).TextColor(t.TextMuted)
			})
		}
	})
	p.ovFiles(c, d)
	if d.Invalid == 0 {
		ui.Text(c, "No invalid data rows were found during local validation.").FontSize(12).TextColor(t.TextMuted)
	} else {
		ui.Text(c, FormatNumber(d.Invalid)+" invalid rows were skipped. The files remain usable with their valid records.").FontSize(12).TextColor(t.Warning)
	}
	if n := ovInt(d.LegacyColumn16); n > 0 {
		ui.Text(c, FormatNumber(n)+" rows use a positive-integer legacy column 16. They are interpreted as legacy cluster counts; "+
			"the input has no column 17 type for those rows.").FontSize(12).TextColor(t.Warning)
	}
	if d.Invalid == 0 {
		return
	}
	if d.OmittedErrors > 0 {
		ui.Text(c, fmt.Sprintf("The downloadable error sample contains the first %s invalid rows; %s additional invalid-row details were omitted to keep the analysis index small. "+
			"The total invalid count above remains complete.", FormatNumber(int64(len(d.Errors))), FormatNumber(d.OmittedErrors))).FontSize(12).TextColor(t.Warning)
	}
	rows := d.Errors
	if len(rows) > ovShownErrors {
		rows = rows[:ovShownErrors]
	}
	if len(rows) > 0 {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Sample").FontSize(12).Bold().TextColor(t.TextMuted).Width(120)
			ui.Text(c, "Line").FontSize(12).Bold().TextColor(t.TextMuted).Width(70).TextAlign(ui.End)
			ui.Text(c, "Reason").FontSize(12).Bold().TextColor(t.TextMuted).Width(280)
			ui.Text(c, "Content").FontSize(12).Bold().TextColor(t.TextMuted).Grow(1).Basis(0).MinWidth(0)
		})
	}
	for _, e := range rows {
		sample := p.ovSampleName(e.SampleID)
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, sample).SingleLine().Tooltip(sample).Width(120)
			ui.Text(c, FormatNumber(e.Line)).Width(70).TextAlign(ui.End).SingleLine()
			ui.Text(c, e.Error).SingleLine().Tooltip(e.Error).Width(280)
			ui.Text(c, e.Content).FontSize(12).TextColor(t.TextMuted).SingleLine().Tooltip(e.Content).Grow(1).Basis(0).MinWidth(0)
		})
	}
	if len(d.Errors) > len(rows) {
		ui.Text(c, FormatNumber(int64(len(d.Errors)-len(rows)))+" more in the CSV.").FontSize(12).TextColor(t.TextMuted)
	}
	ui.Row(c).Children(func() {
		label, tip := "Download validation report…", "Every invalid row with its line number and reason"
		if d.OmittedErrors > 0 {
			label, tip = "Download error sample…", "The invalid rows kept with their line number and reason"
		}
		if ui.Button(c, label).Tooltip(tip).Clicked() {
			p.exportValidationCSV(d)
		}
	})
}

// ovFiles is the web Validation page's per-file table when several files
// are merged: each file's valid, invalid and homodimer-proxy rows.
func (p *qcPage) ovFiles(c *ui.Context, d *ovData) {
	t := c.Theme()
	ids := p.ov.ids
	if len(ids) < 2 || len(d.Indexes) != len(ids) {
		return
	}
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "File").FontSize(12).Bold().TextColor(t.TextMuted).Grow(1).Basis(0).MinWidth(0)
			ui.Text(c, "Dataset").FontSize(12).Bold().TextColor(t.TextMuted).Width(140)
			for _, h := range []string{"Valid records", "Invalid rows", "Homodimer-proxy"} {
				ui.Text(c, h).FontSize(12).Bold().TextColor(t.TextMuted).Width(110).TextAlign(ui.End)
			}
		})
		for i, part := range d.Indexes {
			file, dataset := ids[i], p.ovSampleName(ids[i])
			if s := p.a.Project.Sample(ids[i]); s != nil {
				file = dataBasename(s.Reads.Path)
			}
			ui.Row(c.Key("ov-file-" + ids[i])).Gap(8).Children(func() {
				ui.Text(c, file).SingleLine().Tooltip(file).Grow(1).Basis(0).MinWidth(0)
				ui.Text(c, dataset).SingleLine().Tooltip(dataset).Width(140)
				for _, v := range []int64{part.Records, part.Invalid, part.HomodimerRecords} {
					ui.Text(c, FormatNumber(v)).Width(110).TextAlign(ui.End).SingleLine()
				}
			})
		}
	})
}

// ovSampleName is a sample's name, or its id when it is not in the project.
func (p *qcPage) ovSampleName(id string) string {
	if p.a.Project != nil {
		if s := p.a.Project.Sample(id); s != nil && s.Name != "" {
			return s.Name
		}
	}
	return nameOr(id, "—")
}

// ---- exports

func (p *qcPage) overviewExportName(ext string, parts ...string) string {
	return exportName(ext, append([]string{selectionName(p.a.Selected.Key)}, parts...)...)
}

// ovSha256Status is the web's sha256Status of a file record: "pending"
// until the project has fingerprinted the file in full.
func ovSha256Status(sha string) any {
	if sha != "" {
		return "available"
	}
	return "pending"
}

func ovNullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ovSourceFile is the web's sourceFile (analysis-ui.js sourceHyb): one
// sample's input file, or, merged, each sample's with its record count.
func (p *qcPage) ovSourceFile(ids []string, parts []ovPart) map[string]any {
	a := p.a
	if len(ids) == 1 {
		name, size, sha := webHybFile(a, ids[0])
		return map[string]any{"name": name, "bytes": size, "sha256": ovNullable(sha), "sha256Status": ovSha256Status(sha)}
	}
	var datasets []map[string]any
	for i, id := range ids {
		var valid any
		if i < len(parts) {
			valid = parts[i].Records
		}
		datasets = append(datasets, webDataset(a, id, valid))
	}
	return map[string]any{"name": a.SelectionLabel(a.Selected.Key), "bytes": nil, "sha256": nil, "sha256Status": "per-dataset", "datasets": datasets}
}

// webHybFile is a sample's HYB file name, size (nil when unknown) and
// SHA-256 ("" when not computed yet).
func webHybFile(a *App, id string) (string, any, string) {
	if s := a.Project.Sample(id); s != nil {
		var size any
		if s.Reads.SizeBytes > 0 {
			size = s.Reads.SizeBytes
		}
		return dataBasename(s.Reads.Path), size, s.Reads.SHA256
	}
	return id, nil, ""
}

// webDataset is one entry of the web's sourceFile/sourceHyb datasets[].
func webDataset(a *App, id string, valid any) map[string]any {
	name, size, sha := webHybFile(a, id)
	var condition, label any = nil, nameOr(id, "—")
	if s := a.Project.Sample(id); s != nil {
		if s.Condition != "" {
			condition = s.Condition
		}
		if s.Name != "" {
			label = s.Name
		}
	}
	return map[string]any{"id": id, "label": label, "condition": condition, "fileName": name,
		"fileSize": size, "sha256": ovNullable(sha), "sha256Status": ovSha256Status(sha), "validRecords": valid}
}

// ovWebSummary is ovData in the web parser's summary shape (camelCase;
// data-provider.js getSummary), as hyb2-overview-summary.json holds it.
func ovWebSummary(d ovData, fileName string, ids []string) map[string]any {
	optional := func(v *int64) any {
		if v == nil {
			return nil
		}
		return *v
	}
	counts := func(r ovCount) map[string]any {
		return map[string]any{"records": r.Records, "support": r.Support, "rawReads": r.Reads,
			"rawReadCountRecords": r.ReadRecords, "overlapTotal": r.OverlapTotal, "overlapRecords": r.OverlapRecords}
	}
	rnas := []map[string]any{}
	for _, r := range d.RNAs {
		row := counts(r)
		row["name"] = r.Name
		rnas = append(rnas, row)
	}
	pairs := []map[string]any{}
	for _, r := range d.Pairs {
		row := counts(r)
		row["rnaOne"], row["rnaTwo"] = r.Gene1, r.Gene2
		pairs = append(pairs, row)
	}
	types := []map[string]any{}
	for _, ty := range d.Types {
		types = append(types, map[string]any{"name": ty.Name, "records": ty.Records, "support": ty.Support})
	}
	errors := []map[string]any{}
	for _, e := range d.Errors {
		errors = append(errors, map[string]any{"line": e.Line, "error": e.Error, "content": e.Content})
	}
	return map[string]any{
		"fileName": fileName, "datasetIds": ids,
		"validRecords": d.Records, "invalidRecords": d.Invalid,
		"skippedCommentLines": optional(d.SkippedComment), "skippedBlankLines": optional(d.SkippedBlank),
		"supportInteractions": d.Support, "rawReadInteractions": d.Reads, "rawReadCountRecords": d.ReadRecords,
		"withDg": d.WithDG, "overlapScoreRecords": d.OverlapRecords, "canonicalRecords": optional(d.CanonicalRecords),
		"ambiguousColumn16Records": optional(d.LegacyColumn16), "intraRecords": d.IntraRecords, "interRecords": d.InterRecords,
		"homodimerRecords": d.HomodimerRecords, "homodimerRawReads": d.HomodimerReads,
		"uniqueRNAs": d.UniqueRNAs, "uniquePairs": d.UniquePairs,
		"rnaCounts": rnas, "pairCounts": pairs, "chimeraTypes": types, "errors": errors,
		"errorSampleCount": len(d.Errors), "omittedErrorCount": d.OmittedErrors, "errorSampleTruncated": d.OmittedErrors > 0,
	}
}

// exportOverviewJSON writes the web's hyb2-overview-summary.json
// (app.js downloadSummary) for the selection.
func (p *qcPage) exportOverviewJSON() {
	a := p.a
	d := *p.ov.data
	ids := p.ov.ids
	source := p.ovSourceFile(ids, d.Indexes)
	summary := ovWebSummary(d, source["name"].(string), ids)
	payload := map[string]any{
		"application": "HYB2 Desktop", "version": nameOr(reportAppVersion(), "development build"), "buildCommit": nil,
		"sourceFile": source, "analysis": "overview", "generatedLocally": true, "summary": summary,
	}
	// rnaNames (the web parser's sorted distinct names) come with the name
	// matching: the loaded one, else asked for while saving.
	names := []string(nil)
	if m, _, _ := p.matchingFor(ids); m != nil && m.NameList != nil {
		names = m.NameList
	}
	path := a.Project.Path
	render := func() ([]byte, error) {
		if names == nil {
			var m refMatchData
			result, err := a.B.Query(path, "analysis_reference_matching", core.Obj{"sample_ids": ids})
			if err == nil {
				err = model.Decode(result, &m)
			}
			if err != nil {
				return nil, err
			}
			names = m.NameList
		}
		summary["rnaNames"] = append([]string{}, names...)
		return json.MarshalIndent(payload, "", "  ")
	}
	saveExport(a, "Export the overview", p.overviewExportName(".json", "hyb2-overview-summary"), render)
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

// exportValidationCSV writes the web's validation report (line, error,
// content; hyb2-validation-error-sample.csv when details were omitted).
// When the rows come from more than one sample, a last sample column says
// which file each came from (a line number alone does not); otherwise the
// columns are exactly the web's three.
func (p *qcPage) exportValidationCSV(d *ovData) {
	name := p.overviewExportName(".csv", "hyb2-validation-report")
	if d.OmittedErrors > 0 {
		name = p.overviewExportName(".csv", "hyb2-validation-error-sample")
	}
	rows := d.Errors
	samples := make([]string, len(rows))
	for i, e := range rows {
		samples[i] = p.ovSampleName(e.SampleID)
	}
	saveExport(p.a, "Export the validation report", name, func() ([]byte, error) {
		return ovValidationCSV(rows, samples), nil
	})
}

func ovValidationCSV(rows []ovError, samples []string) []byte {
	seen := map[string]bool{}
	for _, e := range rows {
		seen[e.SampleID] = true
	}
	several := len(seen) > 1
	header := []string{"line", "error", "content"}
	if several {
		header = append(header, "sample")
	}
	out := [][]string{header}
	for i, e := range rows {
		row := []string{itoa64(e.Line), e.Error, e.Content}
		if several {
			sample := ""
			if i < len(samples) {
				sample = samples[i]
			}
			row = append(row, sample)
		}
		out = append(out, row)
	}
	return quotedCSV(out)
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
