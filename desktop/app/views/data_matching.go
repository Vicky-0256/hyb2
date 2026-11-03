package views

// ① Data, Name matching and Reproducibility: the web Data drawer's folded
// sections (web/analysis-pages.js getFilesDialogBody). Name matching pairs
// each HYB RNA name of the analysed samples with the reference sequence of
// the same name (analysis_reference_matching); a manual match or "Mark as
// not matched" is kept on the project's reference (set_reference_match) and
// the runtime applies it wherever it looks the RNA up in its reference
// (Structure, Region, reference lengths). The web matches one set of RNA
// names (state.summary.rnaNames, the analysed scope) everywhere; here the
// References card's summary covers every analysed sample and the QC
// Overview's callout the top bar's selection. Its "Review mapping" opens
// the dialog on that same selection, and the dialog says which samples it
// matches.

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// dataMappingPreview is how many RNAs the dialog lists (the web's 100).
const dataMappingPreview = 100

type refMatchRNA struct {
	Name   string  `json:"name"`
	Match  *string `json:"match"`
	Manual bool    `json:"manual"`
}

type refMatchRef struct {
	ReferenceID string        `json:"reference_id"`
	Label       string        `json:"label"`
	Sequences   int64         `json:"sequences"`
	Names       []string      `json:"sequence_names"`
	SampleIDs   []string      `json:"sample_ids"`
	RNAs        []refMatchRNA `json:"rnas"`
	Matched     int64         `json:"matched"`
	// unsupported FASTA symbols (nil when the file cannot be read)
	UnsupportedEntries *int64   `json:"unsupported_entries"`
	UnsupportedChars   []string `json:"unsupported_characters"`
}

// refMatchData is analysis_reference_matching's result.
type refMatchData struct {
	References   []refMatchRef `json:"references"`
	RNANames     int64         `json:"rna_names"`
	Matched      int64         `json:"matched"`
	Sequences    int64         `json:"sequences"`
	Unreferenced []string      `json:"unreferenced_sample_ids"`
	// NameList: the distinct names in the web's order (its rnaNames)
	NameList []string `json:"rna_name_list"`
}

// refMatching caches the matching per refMatchKey (the QC selection's and
// the Data page's analysed samples). seq counts
// invalidations: an answer to a load started before one is dropped; the
// matching from before is shown (stale) until the new one arrives.
type refMatching struct {
	data    map[string]*refMatchData
	stale   map[string]*refMatchData
	errs    map[string]string
	loading map[string]bool
	seq     int
}

func (m *refMatching) invalidate() {
	if len(m.data) > 0 {
		m.stale = m.data
	}
	m.data, m.errs, m.loading = nil, nil, nil
	m.seq++
}

// refMatchKey is the project, the sample ids, the reference each of them
// uses and the project's references: setting a sample's reference (the
// sample editor, "Set reference…") or adding a reference loads the
// matching again once the project refreshes.
func refMatchKey(a *App, ids []string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id
		if s := a.Project.Sample(id); s != nil {
			parts[i] += "=" + s.ReferenceID
		}
	}
	refs := make([]string, len(a.Project.References))
	for i, r := range a.Project.References {
		refs[i] = r.ID
	}
	return a.Project.Path + "|" + strings.Join(parts, ",") + "|" + strings.Join(refs, ",")
}

// retryMatching drops the error of ids' matching so it loads again.
func (p *qcPage) retryMatching(ids []string) {
	if p.a.Project != nil {
		delete(p.match.errs, refMatchKey(p.a, ids))
	}
}

// ensureMatching loads the name matching of ids once; a guarded job, safe
// to call from View.
func (p *qcPage) ensureMatching(ids []string) {
	a := p.a
	m := &p.match
	if a.Project == nil || len(ids) == 0 {
		return
	}
	path := a.Project.Path
	key := refMatchKey(a, ids)
	if m.data[key] != nil || m.errs[key] != "" || m.loading[key] {
		return
	}
	if m.loading == nil {
		m.data, m.errs, m.loading = map[string]*refMatchData{}, map[string]string{}, map[string]bool{}
	}
	m.loading[key] = true
	seq := m.seq
	a.Go(func() func() {
		var data refMatchData
		result, err := a.B.Query(path, "analysis_reference_matching", core.Obj{"sample_ids": ids})
		if err == nil {
			err = model.Decode(result, &data)
		}
		return func() {
			if m.seq != seq {
				return
			}
			delete(m.loading, key)
			if err != nil {
				m.errs[key] = err.Error()
				return
			}
			m.data[key] = &data
		}
	})
}

// matchingFor is the loaded matching of ids, its error, and whether it is
// loading.
func (p *qcPage) matchingFor(ids []string) (*refMatchData, string, bool) {
	if p.a.Project == nil || len(ids) == 0 {
		return nil, "", false
	}
	key := refMatchKey(p.a, ids)
	data := p.match.data[key]
	if data == nil && p.match.errs[key] == "" {
		data = p.match.stale[key]
	}
	return data, p.match.errs[key], p.match.loading[key]
}

// dataMatchDialog is the Name matching dialog's state. ids are the samples
// it matches: every analysed sample from the Data page, the top bar's
// selection (named by selection) from the QC Overview. place: the editor
// goes on the first unmatched RNA once the matching has loaded.
type dataMatchDialog struct {
	open      bool
	ids       []string
	selection string
	place     bool
	reference string // reference label shown in the select
	rna       string // the editor's HYB RNA
	sequence  string // the editor's FASTA reference ID
	status    string
	busy      bool
}

// matching is the Data page's matching: every analysed sample.
func (p *dataPage) matching() (*refMatchData, string, bool) {
	return p.matchingOf(qcReadyIDs(p.a))
}

// dialogMatching is the dialog's matching: its samples.
func (p *dataPage) dialogMatching() (*refMatchData, string, bool) {
	return p.matchingOf(p.match.ids)
}

func (p *dataPage) matchingOf(ids []string) (*refMatchData, string, bool) {
	q := qcFor(p.a)
	if q == nil {
		return nil, "", false
	}
	q.ensureMatching(ids)
	return q.matchingFor(ids)
}

// dataMatchScope says which samples the dialog matches.
func dataMatchScope(d *dataMatchDialog) string {
	if d.selection != "" {
		return "RNA names of the samples chosen in the top bar: " + d.selection + "."
	}
	return "RNA names of the " + dataPlural(len(d.ids), "analysed sample", "analysed samples") + "."
}

// dataMatchSummary is the web's fold summary of the name matching.
func dataMatchSummary(m *refMatchData) string {
	if unmatched := m.RNANames - m.Matched; unmatched > 0 {
		return FormatNumber(unmatched) + " of " + FormatNumber(m.RNANames) + " RNA names have no reference sequence"
	}
	if m.RNANames == 1 {
		return "All 1 RNA name matches a reference sequence"
	}
	return "All " + FormatNumber(m.RNANames) + " RNA names match a reference sequence"
}

// dataUnsupportedText is the web's warning on a FASTA with symbols other
// than A, C, G, T and U.
func dataUnsupportedText(r refMatchRef) string {
	n := ovInt(r.UnsupportedEntries)
	if n == 0 {
		return ""
	}
	records, symbols := " FASTA records contain", " unsupported symbols"
	if n == 1 {
		records = " FASTA record contains"
	}
	if len(r.UnsupportedChars) == 1 {
		symbols = " unsupported symbol"
	}
	return FormatNumber(n) + records + symbols + " (" + strings.Join(r.UnsupportedChars, ", ") +
		"). Symbols were preserved so coordinates did not shift; affected regions cannot be folded."
}

// matchingSection is the References card's Name matching and
// Reproducibility lines.
func (p *dataPage) matchingSection(c *ui.Context) {
	t := c.Theme()
	m, err, loading := p.matching()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		switch {
		case m != nil && len(m.References) > 0:
			attention := m.RNANames > m.Matched
			ui.Text(c, "Name matching").Bold()
			summary := ui.Text(c, dataMatchSummary(m)).Grow(1).Shrink(1).MinWidth(0)
			if attention {
				summary.TextColor(t.Warning)
			} else {
				summary.TextColor(t.TextMuted)
			}
			if ui.Button(c, "Review mapping…").Tooltip("Pair HYB RNA names with reference sequences").Clicked() {
				p.openMatching(qcReadyIDs(p.a), "")
			}
		case loading:
			ui.Spinner(c)
			ui.Text(c, "Matching RNA names to the references…").TextColor(t.TextMuted).Grow(1)
		case err != "":
			ui.Text(c, "Name matching unavailable: "+ShortError(err)).FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1).MinWidth(0)
		default:
			ui.Spacer(c)
		}
		p.reproButton(c)
	})
	if m != nil {
		for _, r := range m.References {
			if text := dataUnsupportedText(r); text != "" {
				ui.Text(c.Key("unsupported-"+r.ReferenceID), dataRefLabel(p.a, r.ReferenceID)+": "+text).FontSize(12).TextColor(t.Warning)
			}
		}
	}
}

// reproButton opens the Reproducibility dialog; the References card shows
// it with or without references (the web always offers the section).
func (p *dataPage) reproButton(c *ui.Context) {
	if ui.Button(c, "Reproducibility…").Tooltip("Versions and file fingerprints for your methods section").Clicked() {
		p.reproOpen = true
	}
}

func dataRefLabel(a *App, id string) string {
	for i, label := range dataRefLabels(a.Project.References) {
		if a.Project.References[i].ID == id {
			return label
		}
	}
	return id
}

// openMatching opens the dialog on the samples ids (selection names the
// top bar's selection they are, "" for every analysed sample) at the first
// reference with an unmatched RNA, its editor on that RNA (as the web's
// renderFastaMapping); while the matching loads, the dialog places the
// editor once it arrives.
func (p *dataPage) openMatching(ids []string, selection string) {
	p.match = dataMatchDialog{open: true, ids: ids, selection: selection, place: true}
	p.placeEditor()
}

// placeEditor puts the editor on the first unmatched RNA once the
// dialog's matching has loaded.
func (p *dataPage) placeEditor() {
	d := &p.match
	if !d.place {
		return
	}
	m, err, _ := p.dialogMatching()
	if m == nil {
		if err != "" {
			d.place = false
		}
		return
	}
	d.place = false
	if len(m.References) == 0 {
		return
	}
	pick := m.References[0]
	for _, r := range m.References {
		if r.Matched < int64(len(r.RNAs)) {
			pick = r
			break
		}
	}
	d.reference = dataRefLabel(p.a, pick.ReferenceID)
	p.editRNA(pick, "")
}

// editRNA puts rna (or the first unmatched RNA) in the editor.
func (p *dataPage) editRNA(r refMatchRef, rna string) {
	d := &p.match
	if rna == "" && len(r.RNAs) > 0 {
		rna = r.RNAs[0].Name
		for _, row := range r.RNAs {
			if row.Match == nil {
				rna = row.Name
				break
			}
		}
	}
	d.rna, d.sequence = rna, ""
	for _, row := range r.RNAs {
		if row.Name == rna && row.Match != nil {
			d.sequence = *row.Match
		}
	}
}

func (p *dataPage) matchRef(m *refMatchData) (refMatchRef, bool) {
	for _, r := range m.References {
		if dataRefLabel(p.a, r.ReferenceID) == p.match.reference {
			return r, true
		}
	}
	if len(m.References) > 0 {
		return m.References[0], true
	}
	return refMatchRef{}, false
}

func (p *dataPage) matchingDialog(c *ui.Context) {
	a := p.a
	t := c.Theme()
	d := &p.match
	if !d.open {
		return
	}
	ui.Modal(c, &d.open, func() {
		ui.Column(c).Width(640).Gap(12).Children(func() {
			ui.Text(c, "Name matching").FontSize(18).Bold()
			ui.Text(c, dataMatchScope(d)).FontSize(12).TextColor(t.TextMuted)
			p.placeEditor()
			m, err, loading := p.dialogMatching()
			switch {
			case m == nil && loading:
				ui.Row(c).Gap(8).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "Matching RNA names to the references…").TextColor(t.TextMuted)
				})
				return
			case m == nil:
				ui.Text(c, "Name matching unavailable: "+nameOr(ShortError(err), "no analysed samples")).TextColor(t.Danger)
				return
			}
			r, ok := p.matchRef(m)
			if !ok {
				ui.Text(c, "None of these samples has a reference. Set one on the samples to match their RNA names.").TextColor(t.TextMuted)
				return
			}
			// the select always holds the reference shown
			d.reference = dataRefLabel(a, r.ReferenceID)
			ui.Text(c, dataMatchSummary(m)).TextColor(t.TextMuted)
			if len(m.References) > 1 {
				var labels []string
				for _, ref := range m.References {
					labels = append(labels, dataRefLabel(a, ref.ReferenceID))
				}
				Field(c, "Reference", func() {
					ui.Select(c.Key("match-ref"), &d.reference, labels).Width(260).Label("Reference").OnChange(func() {
						if ref, ok := p.matchRef(m); ok {
							p.editRNA(ref, "")
						}
					})
				})
			}
			if text := dataUnsupportedText(r); text != "" {
				ui.Text(c, text).FontSize(12).TextColor(t.Warning)
			}
			ui.Text(c, "Each RNA name in the HYB file is paired with the reference sequence of the same name. To fix one, pick the RNA, "+
				"type the reference ID it should use, and apply.").FontSize(12).TextColor(t.TextMuted)
			var rnas []string
			for _, row := range r.RNAs {
				rnas = append(rnas, row.Name)
			}
			ui.Row(c).Gap(12).Children(func() {
				ui.Column(c).Gap(4).Grow(1).Basis(0).MinWidth(0).Children(func() {
					ui.Text(c, "HYB RNA").FontSize(12).TextColor(t.TextMuted)
					ui.Autocomplete(c.Key("match-rna"), &d.rna, dataSuggestions(rnas)).Label("HYB RNA")
				})
				ui.Column(c).Gap(4).Grow(1).Basis(0).MinWidth(0).Children(func() {
					ui.Text(c, "FASTA reference ID").FontSize(12).TextColor(t.TextMuted)
					ui.Autocomplete(c.Key("match-sequence"), &d.sequence, dataSuggestions(r.Names)).Label("FASTA reference ID")
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Apply mapping").Disabled(d.busy).Clicked() {
					p.applyMatch(r, rnas, false)
				}
				if ui.Button(c, "Mark as not matched").Disabled(d.busy).Clicked() {
					p.applyMatch(r, rnas, true)
				}
				if ui.Button(c, "Export matches").Tooltip("Save the RNA → reference sequence matches as JSON").Clicked() {
					p.exportMatches(r)
				}
			})
			if d.status != "" {
				ui.Text(c, d.status).FontSize(12).TextColor(t.Danger)
			}
			preview := r.RNAs
			if len(preview) > dataMappingPreview {
				preview = preview[:dataMappingPreview]
			}
			ui.Scroll(c).MaxHeight(300).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for _, row := range preview {
						mapped, color := "Not matched", t.Warning
						if row.Match != nil {
							mapped, color = *row.Match, t.Text
						}
						if row.Manual {
							mapped += " (manual)"
						}
						name := row.Name
						ui.Row(c.Key("map-" + name)).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, name).SingleLine().Tooltip(name).Grow(1).Basis(0).MinWidth(0)
							ui.Text(c, mapped).SingleLine().Tooltip(mapped).TextColor(color).Grow(1).Basis(0).MinWidth(0)
							if ui.Button(c, "Edit").Label("Edit " + name).Clicked() {
								p.editRNA(r, name)
								d.status = ""
							}
						})
					}
				})
			})
			if len(r.RNAs) > len(preview) {
				ui.Text(c, "Showing the first "+FormatNumber(int64(len(preview)))+" of "+FormatNumber(int64(len(r.RNAs)))+
					" RNA mappings. Enter any exact RNA name above to edit it.").FontSize(12).TextColor(t.TextMuted)
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, "Close").Clicked() {
					d.open = false
				}
			})
		})
	})
}

// dataSuggestions are the editor's suggestions (the web's datalists keep
// 500).
func dataSuggestions(names []string) []string {
	if len(names) > 500 {
		return names[:500]
	}
	return names
}

// applyMatch is the web's "Apply mapping" (or "Mark as not matched").
func (p *dataPage) applyMatch(r refMatchRef, rnas []string, notMatched bool) {
	a := p.a
	d := &p.match
	rna, sequence := strings.TrimSpace(d.rna), strings.TrimSpace(d.sequence)
	if !slices.Contains(rnas, rna) {
		d.status = "Enter an exact RNA name from the loaded HYB file."
		return
	}
	if notMatched {
		sequence = ""
	} else if sequence == "" {
		d.status = "Enter an exact reference ID from the loaded FASTA file."
		return
	}
	d.busy, d.status = true, ""
	path, id := a.Project.Path, r.ReferenceID
	a.Go(func() func() {
		_, err := a.B.ProjectOp(path, "set_reference_match", core.Obj{"reference_id": id, "rna": rna, "sequence": sequence})
		return func() {
			d.busy = false
			if err != nil {
				d.status = ShortError(err.Error())
				return
			}
			d.rna, d.sequence = rna, sequence
			if q := qcFor(a); q != nil {
				q.match.invalidate()
			}
			if sequence != "" {
				a.Toast("Reference mapping updated locally.")
			} else {
				a.Toast("RNA marked as not matched.")
			}
			a.RefreshProject()
		}
	})
}

// exportMatches writes the web's fasta-mapping.json (analysis-ui.js
// export-fasta-mapping) for one reference: the HYB files of its samples
// (sourceHyb), its FASTA (sourceFasta) and every matched RNA with the
// sequence it uses.
func (p *dataPage) exportMatches(r refMatchRef) {
	a := p.a
	mapping := map[string]string{}
	for _, row := range r.RNAs {
		if row.Match != nil {
			mapping[row.Name] = *row.Match
		}
	}
	var fasta map[string]any
	if ref := a.Project.Reference(r.ReferenceID); ref != nil {
		var size any
		if ref.SizeBytes > 0 {
			size = ref.SizeBytes
		}
		fasta = map[string]any{"fileName": dataBasename(ref.Path), "fileSize": size, "sha256": ovNullable(ref.SHA256),
			"sha256Status": ovSha256Status(ref.SHA256), "mappedReferenceId": nil}
	}
	payload := map[string]any{
		"application": "HYB2 Desktop", "version": nameOr(reportAppVersion(), "development build"), "buildCommit": nil,
		"generatedLocally": true, "sourceHyb": dataSourceHyb(a, r.SampleIDs), "sourceFasta": fasta, "mapping": mapping,
	}
	name := exportName(".json", dataRefLabel(a, r.ReferenceID), "fasta-mapping")
	a.Go(func() func() {
		path := PickSave("Export the name matches", name)
		if path == "" {
			return nil
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err == nil {
			err = os.WriteFile(path, data, 0o644)
		}
		return func() {
			if err != nil {
				a.Toast("Could not export: %v", err)
				return
			}
			a.Toast("FASTA mapping saved locally.")
		}
	})
}

// dataSourceHyb is the web's sourceHyb (analysis-ui.js): one sample's HYB
// file, or, for several, each one's file with its valid records.
func dataSourceHyb(a *App, ids []string) map[string]any {
	if len(ids) == 1 {
		name, size, sha := webHybFile(a, ids[0])
		return map[string]any{"fileName": name, "fileSize": size, "sha256": ovNullable(sha), "sha256Status": ovSha256Status(sha)}
	}
	q := qcFor(a)
	datasets := []map[string]any{}
	for _, id := range ids {
		var valid any
		if q != nil {
			if s, ok := q.stats[id]; ok {
				valid = s.Records
			}
		}
		datasets = append(datasets, webDataset(a, id, valid))
	}
	return map[string]any{"fileName": dataPlural(len(ids), "dataset", "datasets"), "fileSize": nil, "sha256": nil,
		"sha256Status": "per-dataset", "datasets": datasets}
}

// reproDialog is the web Data drawer's Reproducibility section: the app
// version and the SHA-256 of every input file.
func (p *dataPage) reproDialog(c *ui.Context) {
	a := p.a
	t := c.Theme()
	if !p.reproOpen {
		return
	}
	ui.Modal(c, &p.reproOpen, func() {
		ui.Column(c).Width(640).Gap(10).Children(func() {
			ui.Text(c, "Reproducibility").FontSize(18).Bold()
			ui.Text(c, "Versions and file fingerprints for your methods section").TextColor(t.TextMuted)
			line := func(label, value string) {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, label).Width(200).SingleLine().Tooltip(label)
					ui.Text(c, value).Font("monospace").FontSize(12).Grow(1).Shrink(1).MinWidth(0).Selectable()
				})
			}
			line("App version", nameOr(reportAppVersion(), "development build"))
			line("Git commit", "Local development build")
			ui.Scroll(c).MaxHeight(360).Children(func() {
				ui.Column(c).Gap(6).Children(func() {
					for _, s := range a.Project.Samples {
						line(s.Label()+" SHA-256", dataSHA(s.Reads.SHA256))
					}
					for _, r := range a.Project.References {
						line(dataRefName(r)+" FASTA SHA-256", dataSHA(r.SHA256))
					}
				})
			})
			ui.Text(c, "A file without a SHA-256 has not been fingerprinted in full yet; the project matches it by size and a quick hash.").
				FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, "Close").Clicked() {
					p.reproOpen = false
				}
			})
		})
	})
}

func dataSHA(sha string) string { return nameOr(sha, "Not computed yet") }
