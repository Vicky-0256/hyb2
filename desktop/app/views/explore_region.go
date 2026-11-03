package views

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
	"hyb2desktop/widgets"
)

// Region: hybrids with an arm on gene:[start, end] (either arm order),
// optionally with the other arm on a partner range (analysis_region), the
// region's reference sequence, and its matching records (analysis_hybrids
// with the region's anchor filter, so the same set the region counts).

const regionRows = 200

// regionRecordRows are the web's matching records: 20 shown, 100 at most.
const (
	regionRecordsShort = 20
	regionRecordsLong  = 100
)

// regionSequencePreview is the web's SEQUENCE_PREVIEW: the first 300 nt
// are shown (Copy takes them all).
const regionSequencePreview = 300

var overlapRules = []string{"Any overlap", "Fully contained"}

type regionFilters struct {
	start, end         int
	partner            string // "" = any
	partnerStart, pEnd int
	overlap, typ       string
}

type regionState struct {
	applied regionFilters
	// inputs
	start, end, partnerStart, partnerEnd string
	partner, overlap, typ                string
	inputErr                             string

	load    loader
	data    *regionData
	profile *widgets.Profile
	hover   int

	// the matching records (the first regionRecordsLong) and their total
	recLoad  loader
	records  []hybridRow
	recTotal int
	showAll  bool
	recList  ui.ListState

	// seq is the region's reference sequence (seqErr when unavailable),
	// read for seqKey; seqWant is the key the region wants now, so the
	// panel never shows an old region's (or reference's) bases.
	seqLoad loader
	seq     string
	seqErr  string
	seqKey  string
	seqWant string
}

type regionData struct {
	gene               string
	f                  regionFilters
	records, reads     int64
	support            int64
	distinct           int64
	hasDistinct, exact bool
	partners           []partnerTotal
	bin                int
	adjusted           bool
	rows               []regionRow
}

type regionRow struct {
	Gene1   string `json:"gene1"`
	Start1  int    `json:"start1"`
	End1    int    `json:"end1"`
	Gene2   string `json:"gene2"`
	Start2  int    `json:"start2"`
	End2    int    `json:"end2"`
	Type    string `json:"type"`
	Records int64  `json:"records"`
	Reads   int64  `json:"reads"`
}

func (s *regionState) init() {
	s.partner, s.overlap, s.typ = allPartners, overlapRules[0], allTypes
	s.applied = regionFilters{overlap: overlapRules[0], typ: allTypes}
	s.profile = widgets.NewProfile()
	s.hover = -1
}

// selectionChanged starts a new gene at the web's default range, set by
// defaultRange once the summary gives the gene's observed extent.
func (s *regionState) selectionChanged(geneChanged bool, length int) {
	if geneChanged || s.applied.end == 0 {
		s.applied.start, s.applied.end = 0, 0
		s.start, s.end, s.inputErr = "", "", ""
		s.partner, s.partnerStart, s.partnerEnd = allPartners, "", ""
		s.applied.partner, s.applied.partnerStart, s.applied.pEnd = "", 0, 0
	}
}

// defaultRange is the web Region Explorer's: the observed start to 200 nt
// after it (within the observed end), else 1–201 of the reference.
func (s *regionState) defaultRange(extent [2]int, known bool, length int) {
	if known && extent[0] > 0 {
		s.applyRange(extent[0], min(extent[1], extent[0]+200))
		return
	}
	end := 201
	if length > 0 {
		end = min(end, length)
	}
	s.applyRange(1, end)
}

// applyRange sets the anchor range (inputs and applied).
func (s *regionState) applyRange(start, end int) {
	s.applied.start, s.applied.end = start, end
	s.start, s.end = coordText(start), coordText(end)
	s.inputErr = ""
}

// setPartner sets the partner ("" = any) and its range (0, 0 = none),
// inputs and applied.
func (s *regionState) setPartner(partner string, start, end int) {
	s.partner = partner
	if partner == "" {
		s.partner = allPartners
	}
	s.partnerStart, s.partnerEnd = coordText(start), coordText(end)
	s.applied.partner, s.applied.partnerStart, s.applied.pEnd = partner, start, end
}

func (s *regionState) fromInputs() (regionFilters, error) {
	f := regionFilters{overlap: s.overlap, typ: s.typ}
	var ok [4]bool
	f.start, ok[0] = parseCoord(s.start)
	f.end, ok[1] = parseCoord(s.end)
	f.partnerStart, ok[2] = parseCoord(s.partnerStart)
	f.pEnd, ok[3] = parseCoord(s.partnerEnd)
	for _, good := range ok {
		if !good {
			return f, fmt.Errorf("positions must be whole numbers")
		}
	}
	if f.start <= 0 || f.end <= 0 || f.start > f.end {
		return f, fmt.Errorf("enter a start and an end, start not after end")
	}
	if s.partner != allPartners {
		f.partner = s.partner
	}
	if (f.partnerStart > 0) != (f.pEnd > 0) || f.partnerStart > f.pEnd {
		return f, fmt.Errorf("a partner range needs both a start and an end, start not after end")
	}
	if f.partnerStart > 0 && f.partner == "" {
		return f, fmt.Errorf("choose a partner gene for the partner range")
	}
	return f, nil
}

func (p *explorePage) regionKey() string {
	return fmt.Sprintf("%s\x00%+v", p.sel, p.rg.applied)
}

func (p *explorePage) loadRegion() {
	s := &p.rg
	if s.applied.end == 0 {
		if p.summary.extents == nil {
			return // the summary (and the gene's extent) is still loading
		}
		extent, known := p.summary.extents[p.gene]
		s.defaultRange(extent, known, p.geneLength(p.gene))
	}
	path, ids, gene, f := p.a.Project.Path, p.ids, p.gene, s.applied
	p.ensure(&s.load, p.regionKey(), func() (func(), error) {
		payload := core.Obj{"gene": gene, "start": f.start, "end": f.end, "limit": regionRows, "overlap": "any"}
		if f.overlap == overlapRules[1] {
			payload["overlap"] = "contained"
		}
		if f.partner != "" {
			payload["partner"] = f.partner
		}
		if f.partnerStart > 0 {
			payload["partner_start"], payload["partner_end"] = f.partnerStart, f.pEnd
		}
		if types := typesPayload(f.typ); types != nil {
			payload["types"] = types
		}
		got, err := p.query(path, "analysis_region", ids, payload)
		if err != nil {
			return nil, err
		}
		if v := int(num(got["start"])); v > 0 {
			f.start, f.end = v, int(num(got["end"])) // as answered
		}
		d := &regionData{gene: gene, f: f, records: int64(num(got["records"])), reads: int64(num(got["reads"])),
			support: int64(num(got["support"])), exact: got["rows_exact"] != false}
		if v, ok := got["distinct"].(float64); ok {
			d.distinct, d.hasDistinct = int64(v), true
		}
		_ = model.Decode(got["partners"], &d.partners)
		_ = model.Decode(got["rows"], &d.rows)
		profile, _ := got["profile"].(map[string]any)
		d.bin, d.adjusted = int(num(profile["bin_size"])), profile["adjusted"] == true
		starts, ends, values := ints(profile["start"]), ints(profile["end"]), floats(profile["value"])
		return func() {
			s.data = d
			s.profile.Set(starts, ends, values)
		}, nil
	})
}

// loadRegionRecords lists the region's records (the first 100, in file
// order as the web's) with the anchor filter of the set the region counts.
func (p *explorePage) loadRegionRecords(d *regionData) {
	s := &p.rg
	path, ids, filters := p.a.Project.Path, p.ids, regionAnchor(d)
	key := fmt.Sprintf("%s\x00%s\x00%+v", p.sel, d.gene, d.f)
	p.ensure(&s.recLoad, key, func() (func(), error) {
		rows, total, err := p.fetchRecords(path, ids, filters)
		if err != nil {
			return nil, err
		}
		return func() {
			// a new region starts with the short list again
			s.records, s.recTotal, s.showAll = rows, total, false
			s.recList.ScrollTo(0, ui.Start)
		}, nil
	})
}

func (p *explorePage) fetchRecords(path string, ids []string, filters core.Obj) ([]hybridRow, int, error) {
	got, err := p.query(path, "analysis_hybrids", ids, core.Obj{
		"filters": filters, "limit": regionRecordsLong, "offset": 0, "order": "file", "with_sequence": true}) // the .hyb names
	if err != nil {
		return nil, 0, err
	}
	var rows []hybridRow
	if err := model.Decode(got["rows"], &rows); err != nil {
		return nil, 0, err
	}
	return rows, int(num(got["total"])), nil
}

// regionSequenceRange is the region clamped to its gene's reference.
func (p *explorePage) regionSequenceRange(d *regionData) (int, int) {
	start, end := max(1, d.f.start), d.f.end
	if length := p.geneLength(d.gene); length > 0 {
		end = min(end, length)
	}
	return start, end
}

// regionRefStamp is the version of reference ref: its file (replaced or
// relinked through update_reference) and the name matching's invalidation
// count (set_reference_match), so a change reads the sequence again.
func regionRefStamp(a *App, ref string) string {
	r := a.Project.Reference(ref)
	if r == nil {
		return ""
	}
	match := 0
	if q := qcFor(a); q != nil {
		match = q.match.seq
	}
	return fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%d", r.Path, r.SizeBytes, r.QuickHash, r.SHA256, match)
}

// loadRegionSequence reads the region's reference sequence.
func (p *explorePage) loadRegionSequence(d *regionData) {
	s := &p.rg
	a := p.a
	ref := stReferenceOf(a.Project, d.gene)
	start, end := p.regionSequenceRange(d)
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d-%d", a.Project.Path, ref, regionRefStamp(a, ref), d.gene, start, end)
	if key != s.seqWant {
		// a new region or reference: drop the old bases until it loads
		s.seqWant, s.seq, s.seqErr, s.seqKey = key, "", "", ""
	}
	path, gene := a.Project.Path, d.gene
	p.ensure(&s.seqLoad, key, func() (func(), error) {
		seq, problem := "", ""
		if ref == "" {
			problem = "no reference"
		} else if raw, err := a.B.ProjectOp(path, "read_reference_sequence", core.Obj{"reference_id": ref, "name": gene}); err != nil {
			problem = err.Error()
		} else {
			whole, _ := raw["sequence"].(string)
			whole = strings.ToUpper(strings.Join(strings.Fields(whole), ""))
			if start <= end && end <= len(whole) {
				seq = whole[start-1 : end]
			} else {
				problem = fmt.Sprintf("the reference sequence of %s (%d nt) does not cover the region", gene, len(whole))
			}
		}
		return func() { s.seq, s.seqErr, s.seqKey = seq, problem, key }, nil
	})
}

// retryRegionSequence reads the sequence again after a failed read.
func (p *explorePage) retryRegionSequence() {
	s := &p.rg
	s.seqLoad.reset()
	s.seqWant, s.seq, s.seqErr, s.seqKey = "", "", "", ""
}

func (p *explorePage) regionView(c *ui.Context) {
	s := &p.rg
	t := c.Theme()
	p.loadRegion()
	submit := func() {
		f, err := s.fromInputs()
		if err != nil {
			s.inputErr = err.Error()
			return
		}
		s.inputErr, s.applied = "", f
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(14).Children(func() {
			ui.Row(c).Gap(12).Wrap().AlignItems(ui.End).Children(func() {
				coordInput(c, "rg-start", "Start", &s.start, "Start").OnSubmit(submit)
				coordInput(c, "rg-end", "End", &s.end, "End").OnSubmit(submit)
				picker(c, "rg-overlap", "Overlap rule", &s.overlap, overlapRules, 150)
				picker(c, "rg-partner", "Partner", &s.partner, withAll(allPartners, p.genes()), 190)
				coordInput(c, "rg-pstart", "Partner start", &s.partnerStart, "Optional").OnSubmit(submit)
				coordInput(c, "rg-pend", "Partner end", &s.partnerEnd, "Optional").OnSubmit(submit)
				p.patternPicker(c, "rg-type", &s.typ, 230, nil)
				if ui.PrimaryButton(c, "Explore").Clicked() {
					submit()
				}
				if d := s.data; d != nil && ui.Button(c, "Fold this region").Clicked() {
					p.foldRegion(d)
				}
			})
			ui.Text(c, "Coordinates remain inclusive, matching the HYB record display.").FontSize(12).TextColor(t.TextMuted)
			if s.inputErr != "" {
				errorLine(c, s.inputErr)
			}
			loadingLine(c, &s.load, "the region")
			d := s.data
			if d == nil {
				return
			}
			p.loadRegionRecords(d)
			p.loadRegionSequence(d)
			Card(c, "", func() {
				ui.Row(c).Gap(24).Wrap().AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Text(c, d.gene+":"+span(d.f.start, d.f.end)).FontSize(16).Bold()
						note := fmt.Sprintf("%s matching records · profile bins %s nt", FormatNumber(d.records), FormatNumber(int64(d.bin)))
						if d.f.partner != "" && d.f.partnerStart > 0 {
							note += " · " + d.f.partner + ":" + span(d.f.partnerStart, d.f.pEnd)
						}
						ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
						ui.Text(c, regionSubtitle(d.f)).FontSize(12).TextColor(t.TextMuted)
					})
					stat(c, "Cluster support", FormatNumber(d.support))
					stat(c, "Partner RNAs", FormatNumber(int64(len(d.partners))))
				})
				if d.adjusted {
					notice(c, fmt.Sprintf("The profile resolution was increased to %s nt per bin to keep this plot responsive.", FormatNumber(int64(d.bin))))
				}
				ui.Row(c).Gap(16).Wrap().AlignItems(ui.Start).Children(func() {
					ui.Column(c).Grow(2).Basis(420).Gap(6).Children(func() {
						ev := s.profile.Show(c, 160, "Cluster support along "+d.gene, profileColor(t))
						s.hover = ev.Hover
						readout := fmt.Sprintf("Cluster support of the matching hybrids per %d nt bin of the anchor arm.", d.bin)
						if i := s.hover; i >= 0 && i < s.profile.Len() {
							start, end, v := s.profile.Bin(i)
							readout = fmt.Sprintf("%s · %s support", span(start, end), webFormatValue(v))
						}
						ui.Text(c, readout).FontSize(12).TextColor(t.TextMuted)
					})
					partnerCard(c, "Top partners", d.partners, measureSupport, 8).Grow(1).Basis(300)
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "Show hybrids").Tooltip("The region's records in Interactions").Clicked() {
						p.showRegionHybrids(d)
					}
					ui.Row(c).Grow(1)
					exportMenu(c,
						exportItem{"Records · CSV…", d.records == 0, func() { p.exportRegionRecords(d, false) }},
						exportItem{"Records · HYB…", d.records == 0, func() { p.exportRegionRecords(d, true) }},
						exportItem{"Profile · SVG…", false, func() { p.exportRegionProfileSVG(d) }},
						exportItem{"Profile · TSV…", false, func() { p.exportRegionProfileTSV(d) }},
						exportItem{"Strongest interactions · TSV…", len(d.rows) == 0, func() { p.exportRegionInteractionsTSV(d) }},
						exportItem{"Sequence · FASTA…", stReferenceOf(p.a.Project, d.gene) == "", func() { p.exportRegionFASTA(d) }},
						exportItem{"Parameters · JSON…", false, func() { p.exportRegionParams(d) }})
				})
			})
			p.regionReference(c, d)
			p.regionRecords(c, d)
		})
	})
}

// regionSubtitle says which hybrids the region holds.
func regionSubtitle(f regionFilters) string {
	parts := []string{"either arm"}
	if f.overlap == overlapRules[1] {
		parts[0] = "either arm fully inside"
	}
	if f.partner != "" {
		partner := "partner " + shortGene(f.partner)
		if f.partnerStart > 0 {
			partner += ":" + span(f.partnerStart, f.pEnd)
		}
		parts = append(parts, partner)
	}
	if f.typ != "" && f.typ != allTypes && f.typ != allPattern {
		parts = append(parts, patternName(f.typ))
	}
	return strings.Join(parts, " · ")
}

// regionReference is the web's reference panel: the region's sequence in
// groups, with Copy, Structure and FASTA actions.
func (p *explorePage) regionReference(c *ui.Context, d *regionData) {
	s := &p.rg
	t := c.Theme()
	Card(c, "", func() {
		if s.seqKey == "" || s.seqKey != s.seqWant {
			// still loading: no bases, so Copy and Send act on nothing old
			loadingLine(c, &s.seqLoad, "the reference sequence")
			return
		}
		if s.seq == "" && s.seqErr != "" && s.seqErr != "no reference" {
			ui.Text(c, "Reference sequence unavailable").FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, "Could not read the reference sequence: "+ShortError(s.seqErr)).TextColor(t.Danger)
			if ui.Button(c, "Retry").Tooltip("Read the reference sequence again").Clicked() {
				p.retryRegionSequence()
			}
			return
		}
		if s.seq == "" {
			ui.Text(c, "Reference sequence unavailable").FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, "Add a reference FASTA to extract this region.").FontSize(16).Bold()
			ui.Text(c, "Observed HYB coordinates remain available without a FASTA, but they do not provide the full reference sequence.").
				FontSize(12).TextColor(t.TextMuted)
			return
		}
		start, end := p.regionSequenceRange(d)
		ui.Text(c, "Reference sequence").FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, d.gene+":"+span(start, end)).FontSize(16).Bold()
		lines, more := groupedSequence(s.seq, start)
		ui.Column(c).Gap(2).Children(func() {
			for _, line := range lines {
				ui.Row(c).Gap(10).Children(func() {
					ui.Text(c, line[0]).FontSize(12).Font("monospace").TextColor(t.TextMuted).Width(64).TextAlign(ui.End)
					ui.Text(c, line[1]).FontSize(12).Font("monospace").Selectable()
				})
			}
		})
		if more != "" {
			ui.Text(c, more).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.Button(c, "Copy sequence").Clicked() {
				c.WriteClipboard(s.seq)
				p.a.Toast("Sequence copied")
			}
			if d.f.partner != "" && d.f.partnerStart > 0 {
				if ui.Button(c, "Send both regions to CPLcofold").Tooltip("Fold the region and the partner range as two strands").Clicked() {
					p.cofoldRegion(d)
				}
			} else if ui.Button(c, "Send to RNA Structure").Clicked() {
				p.foldRegion(d)
			}
			if ui.Button(c, "Download FASTA").Clicked() {
				p.exportRegionFASTA(d)
			}
		})
	})
}

// groupedSequence is the web's renderGroupedSequence: the first 300 nt in
// lines of 50, groups of 10, each line led by its first coordinate
// ({position, groups}); more is the note when the region is longer.
func groupedSequence(seq string, start int) (lines [][2]string, more string) {
	shown := seq[:min(len(seq), regionSequencePreview)]
	for offset := 0; offset < len(shown); offset += 50 {
		var groups []string
		for group := offset; group < min(len(shown), offset+50); group += 10 {
			groups = append(groups, shown[group:min(group+10, offset+50, len(shown))])
		}
		lines = append(lines, [2]string{FormatNumber(int64(start + offset)), strings.Join(groups, " ")})
	}
	if len(seq) > len(shown) {
		more = fmt.Sprintf("Showing the first %s of %s nt. Copy sequence and Download FASTA use the whole region.",
			FormatNumber(int64(len(shown))), FormatNumber(int64(len(seq))))
	}
	return lines, more
}

// regionRecords is the web's Matching records: the region's records, 20
// shown, 100 on request.
func (p *explorePage) regionRecords(c *ui.Context, d *regionData) {
	s := &p.rg
	t := c.Theme()
	Card(c, "Matching records", func() {
		loadingLine(c, &s.recLoad, "the matching records")
		limit := regionRecordsShort
		if s.showAll {
			limit = regionRecordsLong
		}
		rows := s.records[:min(len(s.records), limit)]
		cols := []ui.TableColumn{
			{Title: "Sequence ID", Width: 190}, {Title: "RNA 1", MinWidth: 80}, {Title: "Region 1 (nt)", Width: 130, Align: ui.End},
			{Title: "RNA 2", MinWidth: 80}, {Title: "Region 2 (nt)", Width: 130, Align: ui.End},
			{Title: "Chimera type", Width: 200}, {Title: "Support", Width: 80, Align: ui.End},
		}
		ui.Table(c.Key("rg-records"), &s.recList, cols, len(rows), func(row, col int) {
			r := rows[row]
			text, tip := "", ""
			switch col {
			case 0:
				text = r.Name
				if text == "" {
					text = "#" + FormatNumber(r.ID)
				}
				tip = text
			case 1:
				text, tip = r.Gene1, r.Gene1
			case 2:
				text = span(r.Start1, r.End1)
			case 3:
				text, tip = r.Gene2, r.Gene2
			case 4:
				text = span(r.Start2, r.End2)
			case 5:
				text, tip = patternName(r.Type), patternCodeLabel(r.Type)
			case 6:
				text = FormatNumber(int64(r.Support))
			}
			el := ui.Text(c, text).SingleLine().Padding(3, 6)
			if tip != "" {
				el.Tooltip(tip)
			}
		}).Height(min(float32(len(rows)+1)*30+12, 420)).Label("Matching records").Children(func() {
			if len(rows) == 0 && !s.recLoad.busy() {
				ui.Text(c, "No matching records.").TextColor(t.TextMuted).Padding(12)
			}
		})
		if s.recTotal > regionRecordsShort {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				label := fmt.Sprintf("Show %s records", FormatNumber(int64(min(s.recTotal, regionRecordsLong))))
				if s.showAll {
					label = "Show fewer records"
				}
				if ui.Button(c, label).Clicked() {
					s.showAll = !s.showAll
				}
				note := fmt.Sprintf("Showing %s of %s. Export includes all local matches.",
					FormatNumber(int64(min(limit, s.recTotal))), FormatNumber(int64(s.recTotal)))
				if s.showAll && s.recTotal > regionRecordsLong {
					note = fmt.Sprintf("Showing the first %s of %s matching records. Export includes all local matches.",
						FormatNumber(regionRecordsLong), FormatNumber(int64(s.recTotal)))
				}
				ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
			})
		}
	})
}

// showRegionHybrids lists the region's own hybrids (either arm, by its
// overlap rule, the other on the partner range) in Interactions.
func (p *explorePage) showRegionHybrids(d *regionData) {
	anchor := ixAnchor{gene: d.gene, start: d.f.start, end: d.f.end, partner: d.f.partner,
		pStart: d.f.partnerStart, pEnd: d.f.pEnd, contained: d.f.overlap == overlapRules[1]}
	p.ix.apply(ixFilters{typ: d.f.typ, minReads: 1, minSupport: 1, anchor: anchor})
	p.tab = tabInteractions
}

// foldRegion opens Structure with the region (and the partner range when
// it is on the same gene), clamped to the gene.
func (p *explorePage) foldRegion(d *regionData) {
	length := p.geneLength(d.gene)
	clamp := func(start, end int) [2]int {
		if length > 0 {
			end = min(end, length)
		}
		return [2]int{max(1, start), end}
	}
	windows := [][2]int{clamp(d.f.start, d.f.end)}
	if d.f.partner == d.gene && d.f.partnerStart > 0 {
		windows = append(windows, clamp(d.f.partnerStart, d.f.pEnd))
	}
	p.foldPrefill(d.gene, windows, "region")
}

// cofoldRegion opens ⑤ Structure with the region and the partner range as
// the two strands of a CPLcofold fold (the web's "Send both regions to
// CPLcofold"), each clamped to its gene.
func (p *explorePage) cofoldRegion(d *regionData) {
	clamp := func(gene string, start, end int) core.Region {
		if length := p.geneLength(gene); length > 0 {
			end = min(end, length)
		}
		return core.Region{GeneID: gene, Start: uint64(max(1, start)), End: uint64(max(1, end))}
	}
	p.toStructure(map[string]any{
		"regions": []core.Region{clamp(d.gene, d.f.start, d.f.end), clamp(d.f.partner, d.f.partnerStart, d.f.pEnd)},
		"source":  "region",
	})
}
