package views

// ⑤ Structure: the result details the web shows beside the diagram — the
// CPLfold candidates (pick one to display it; candidates TSV/JSON and the
// bonus matrix for a local CPLfold run), the COMRADES(ViennaRNA) score,
// ensemble and stem tables with their downloads, and the nucleotide
// inspector's text.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// stCandidate is one CPLfold candidate of a record (runtime
// structure._candidate): Bonus is each pair's HYB bonus, aligned with the
// dot-bracket's pairs sorted by their left end.
type stCandidate struct {
	Rank            int        `json:"rank"`
	Type            string     `json:"type"`
	Topology        string     `json:"topology"`
	Energy          *float64   `json:"energy"`
	EffectiveEnergy *float64   `json:"effective_energy"`
	Phase1Energy    *float64   `json:"phase1_energy"`
	Score           *float64   `json:"score"`
	Structure       string     `json:"structure"`
	Pairs           int        `json:"pairs"`
	PseudoknotPairs int        `json:"pseudoknot_pairs"`
	PairsWithBonus  int        `json:"pairs_with_bonus"`
	Bonus           []*float64 `json:"bonus"`
}

// stComrades is a COMRADES(ViennaRNA) window's evidence and structure
// ensemble (runtime comrades.fold), in region positions.
type stComrades struct {
	Evidence struct {
		InputRecords             int       `json:"inputRecords"`
		UniqueBasePairs          int       `json:"uniqueBasePairs"`
		SelectedBasePairs        int       `json:"selectedBasePairs"`
		RankedStems              int       `json:"rankedStems"`
		RequestedStemConstraints int       `json:"requestedStemConstraints"`
		Accepted                 []stStem  `json:"acceptedStemConstraints"`
		Rejected                 []stStem  `json:"rejectedStemConstraints"`
		Score                    float64   `json:"comradesScore"`
		MatchedPairs             int       `json:"matchedEvidencePairs"`
		Pairs                    []stCount `json:"selectedEvidencePairs"`
	} `json:"evidence"`
	Randomisation struct {
		RequestedFolds int    `json:"requestedFolds"`
		Seed           string `json:"seed"`
		SelectedRun    int    `json:"selectedRun"`
		// Structures are best first: score, matched pairs, energy, run.
		Structures []stEnsembleEntry `json:"structures"`
	} `json:"randomisation"`
}

// stStem is an F stem constraint (accepted), or a rejected one's id, rank
// and reason.
type stStem struct {
	ID      string  `json:"id"`
	Rank    int     `json:"rank"`
	Left    int     `json:"left"`
	Right   int     `json:"right"`
	Length  int     `json:"length"`
	Support float64 `json:"support"`
	Reason  string  `json:"reason"`
}

// stCount is an RNAcofold evidence pair and its HYB-row observations.
type stCount struct {
	One   int `json:"one"`
	Two   int `json:"two"`
	Count int `json:"count"`
}

type stEnsembleEntry struct {
	Run          int      `json:"run"`
	Randomised   bool     `json:"randomised"`
	Energy       *float64 `json:"energy"`
	Score        float64  `json:"comradesScore"`
	MatchedPairs int      `json:"matchedEvidencePairs"`
	Accepted     []string `json:"acceptedStemConstraints"`
	Rejected     int      `json:"rejectedStemConstraints"`
	DotBracket   string   `json:"dotBracket"`
}

// selectCandidate shows candidate k of the record: its dot-bracket, pairs,
// HYB bonus and energy replace the record's (candidate 0 is the best, the
// record as folded). false when k is not a candidate of this record.
func (r *structureRecord) selectCandidate(k int) bool {
	if k < 0 || k >= len(r.Candidates) || len(r.Candidates[k].Structure) != len(r.Sequence) || r.mapped != nil {
		return false
	}
	c := r.Candidates[k]
	pairs := stParseDBN(c.Structure, 1)
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a].I != pairs[b].I {
			return pairs[a].I < pairs[b].I
		}
		return pairs[a].J < pairs[b].J
	})
	r.Pairs = make([][2]int, len(pairs))
	for i, pr := range pairs {
		r.Pairs[i] = [2]int{r.positionOf(pr.I), r.positionOf(pr.J)}
	}
	r.Bonus = nil
	if len(c.Bonus) == len(pairs) {
		r.Bonus = c.Bonus
	}
	r.DBN, r.Energy, r.SelectedCandidate = c.Structure, c.Energy, k
	return true
}

// shownRecord is the index in A's records of the one window shown, -1
// when several are.
func (p *structurePage) shownRecord(a *structureResult) int {
	if len(a.Records) == 1 {
		return 0
	}
	if p.record == "all" {
		return -1
	}
	for i, r := range a.Records {
		if fmt.Sprintf("%d-%d", r.WindowStart, r.WindowEnd) == p.record {
			return i
		}
	}
	return -1
}

// stOptional is a number as the web's TSV cell (String(number)), "" for none.
func stOptional(v *float64) string {
	if v == nil || math.IsNaN(*v) {
		return ""
	}
	return jsNumber(*v)
}

// stEnergyCell is an energy for a table, two decimals, "—" for none.
func stEnergyCell(v *float64) string {
	return stEnergyText(v, "—")
}

// stRecordLabel names a record's region: "gene:start-end", or the two
// strands of a CPLcofold record.
func stRecordLabel(gene string, r structureRecord) string {
	if r.Cutpoint > 0 && len(r.Segments) == 2 {
		return stRegionsLabel(r.Segments)
	}
	start, end := r.span()
	return fmt.Sprintf("%s:%d-%d", nameOr(gene, "RNA"), start, end)
}

// stCandidatesHeader is the web's cplfold-candidates.tsv header, with
// pseudoknot_pairs (pairs outside the primary layer) for crossing_pairs and
// the region folded; a CPLcofold record adds the cutpoint as the web.
var stCandidatesHeader = []string{"rank", "selected", "type", "topology", "energy_kcal_per_mol", "effective_energy_kcal_per_mol",
	"phase1_energy_kcal_per_mol", "linear_fold_score", "base_pairs", "pseudoknot_pairs", "dot_bracket", "region"}

func stCandidatesRows(gene string, records []structureRecord) [][]string {
	cofold := false
	for _, r := range records {
		cofold = cofold || r.Cutpoint > 0
	}
	header := stCandidatesHeader
	if cofold {
		header = append(append([]string{}, header...), "cutpoint")
	}
	rows := [][]string{header}
	for _, r := range records {
		for k, c := range r.Candidates {
			selected := "no"
			if k == r.SelectedCandidate {
				selected = "yes"
			}
			dot := c.Structure
			if r.Cutpoint > 0 && r.Cutpoint < len(dot) {
				dot = dot[:r.Cutpoint] + "&" + dot[r.Cutpoint:] // the web's withStrandBreak
			}
			row := []string{strconv.Itoa(k + 1), selected, c.Type, c.Topology, stOptional(c.Energy), stOptional(c.EffectiveEnergy),
				stOptional(c.Phase1Energy), stOptional(c.Score), strconv.Itoa(c.Pairs), strconv.Itoa(c.PseudoknotPairs), dot,
				stRecordLabel(gene, r)}
			if cofold {
				row = append(row, strconv.Itoa(r.Cutpoint))
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// stCandidatesJSON is every candidate of the records, each with its
// pairs' HYB bonus, as JSON.
func stCandidatesJSON(id, engine, gene string, records []structureRecord) ([]byte, error) {
	type window struct {
		Region            string        `json:"region"`
		Start             int           `json:"window_start"`
		End               int           `json:"window_end"`
		Cutpoint          int           `json:"cutpoint,omitempty"`
		SelectedCandidate int           `json:"selected_candidate"`
		Candidates        []stCandidate `json:"candidates"`
	}
	body := struct {
		Structure string   `json:"structure_id"`
		Engine    string   `json:"engine"`
		RNA       string   `json:"rna"`
		Note      string   `json:"note"`
		Windows   []window `json:"windows"`
	}{Structure: id, Engine: engine, RNA: gene,
		Note: "candidates best first (rank 0 is the structure as folded); energies in kcal/mol, effective_energy is the beta-adjusted rank; structure positions are 1-based within the region; bonus is each pair's log1p HYB bonus, pairs ordered by their left end"}
	for _, r := range records {
		if len(r.Candidates) == 0 {
			continue
		}
		start, end := r.span()
		body.Windows = append(body.Windows, window{Region: stRecordLabel(gene, r), Start: start, End: end, Cutpoint: r.Cutpoint,
			SelectedCandidate: r.SelectedCandidate, Candidates: r.Candidates})
	}
	out, err := json.MarshalIndent(body, "", "  ")
	return append(out, '\n'), err
}

// ---- the candidate table

// candidatesPanel lists the CPLfold candidates of the window shown; a
// row shows that candidate in every view, the statistics and the exports.
func (p *structurePage) candidatesPanel(c *ui.Context, a *structureResult) {
	t := c.Theme()
	records := stPickRecords(a.Records, p.record)
	total := 0
	for _, r := range records {
		total += len(r.Candidates)
	}
	if total == 0 {
		return
	}
	gene := stGene(a)
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, "CPLfold candidates").Bold().Grow(1)
			if ui.Button(c, "Candidates TSV…").Tooltip("Every candidate: energy, effective (beta-adjusted) energy, pairs and dot-bracket").Clicked() {
				rows := stCandidatesRows(gene, records)
				saveExport(p.a, "Save the CPLfold candidates", p.exportName(a, "_candidates.tsv"), func() ([]byte, error) {
					return webTSV(rows), nil
				})
			}
			if ui.Button(c, "Candidates JSON…").Tooltip("Every candidate with each pair's HYB bonus").Clicked() {
				id, engine := p.viewA, nameOr(a.Meta.Engine, "CPLfold")
				saveExport(p.a, "Save the CPLfold candidates", p.exportName(a, "_candidates.json"), func() ([]byte, error) {
					return stCandidatesJSON(id, engine, gene, records)
				})
			}
			p.bonusMatrixButton(c, a)
		})
		index := p.shownRecord(a)
		if index < 0 {
			ui.Text(c, fmt.Sprintf("%d candidates in %d windows: choose a window above to browse them.", total, len(records))).
				FontSize(12).TextColor(t.TextMuted)
			return
		}
		r := &a.Records[index]
		if len(r.Candidates) == 0 {
			return
		}
		ui.Text(c, "Choose a row to show that candidate in the diagram, the statistics and the exports.").FontSize(12).TextColor(t.TextMuted)
		cols := []ui.TableColumn{{Title: "Rank", Width: 52, Align: ui.End}, {Title: "Topology", Width: 110},
			{Title: "Energy", Width: 76, Align: ui.End}, {Title: "Effective", Width: 76, Align: ui.End},
			{Title: "Pairs", Width: 60, Align: ui.End}, {Title: "Status"}}
		p.candRow = r.SelectedCandidate
		p.candTable.Selected = &p.candRow
		key := fmt.Sprintf("st-cands-%s-%d", p.viewA, index)
		table := ui.Table(c.Key(key), &p.candTable, cols, len(r.Candidates), func(i, col int) {
			cand := r.Candidates[i]
			switch col {
			case 0:
				ui.Text(c, "#"+strconv.Itoa(i+1))
			case 1:
				ui.Text(c, cand.Topology).SingleLine()
			case 2:
				ui.Text(c, stEnergyCell(cand.Energy))
			case 3:
				ui.Text(c, stEnergyCell(cand.EffectiveEnergy))
			case 4:
				ui.Text(c, FormatNumber(int64(cand.Pairs)))
			case 5:
				state := "Show"
				if i == r.SelectedCandidate {
					state = "Shown"
				}
				ui.Text(c, state).SingleLine()
			}
		}).Height(float32(min(len(r.Candidates), 8))*28 + 34).Label("CPLfold candidate structures")
		if table.Changed() && p.candRow != r.SelectedCandidate && r.selectCandidate(p.candRow) {
			p.arcsKey = "" // redraw with the candidate
			p.a.Toast("CPLfold candidate %d shown", p.candRow+1)
		}
	})
}

// bonusMatrixButton saves the window's HYB bonus matrix as the sparse
// upper-triangle TSV that bin/cplfold --bonus-matrix-file reads.
func (p *structurePage) bonusMatrixButton(c *ui.Context, a *structureResult) {
	index := p.shownRecord(a)
	if !stHybEvidence(a) {
		return
	}
	path := ""
	if index >= 0 {
		path = a.Records[index].BonusMatrixPath
	}
	tip := "The HYB bonus matrix CPLfold folded this window with, for bin/cplfold --bonus-matrix-file"
	switch {
	case index < 0:
		tip = "Choose a window: each window has its own bonus matrix"
	case path == "":
		tip = "Not written for this result (more than 2,000,000 cells, or folded by an older version)"
	}
	if ui.Button(c, "Bonus matrix TSV…").Disabled(path == "").Tooltip(tip).Clicked() {
		r := a.Records[index]
		name := exportName("_bonus_matrix.tsv", nameOr(stGene(a), "structure"), p.viewA)
		if len(a.Records) > 1 {
			name = exportName("_bonus_matrix.tsv", nameOr(stGene(a), "structure"), p.viewA, fmt.Sprintf("%d-%d", r.WindowStart, r.WindowEnd))
		}
		saveExport(p.a, "Save the CPLfold bonus matrix", name, func() ([]byte, error) { return os.ReadFile(path) })
	}
}

// ---- COMRADES(ViennaRNA)

// stEvidenceRows is the web's rna-cofold-evidence.tsv (no record ids are
// kept on the desktop).
func stEvidenceRows(cm *stComrades) [][]string {
	rows := [][]string{{"prepared_position_1", "prepared_position_2", "hyb_row_observations", "example_record_ids"}}
	for _, e := range cm.Evidence.Pairs {
		rows = append(rows, []string{strconv.Itoa(e.One), strconv.Itoa(e.Two), strconv.Itoa(e.Count), ""})
	}
	return rows
}

// stConstraintRows is the web's hyb-guided-folding-constraints.tsv.
func stConstraintRows(cm *stComrades) [][]string {
	rows := [][]string{{"constraint_id", "original_rank", "command", "left", "right", "stem_length", "evidence_support", "accepted"}}
	for _, s := range cm.Evidence.Accepted {
		rows = append(rows, []string{s.ID, strconv.Itoa(s.Rank), fmt.Sprintf("F %d %d %d", s.Left, s.Right, s.Length),
			strconv.Itoa(s.Left), strconv.Itoa(s.Right), strconv.Itoa(s.Length), jsNumber(s.Support), "yes"})
	}
	for _, s := range cm.Evidence.Rejected {
		rows = append(rows, []string{s.ID, strconv.Itoa(s.Rank), "", "", "", "", "", "no:" + s.Reason})
	}
	return rows
}

// stEnsembleRows is the web's hyb-guided-structure-ensemble.tsv.
func stEnsembleRows(cm *stComrades) [][]string {
	rows := [][]string{{"run", "randomised", "selected", "mfe_kcal_per_mol", "comrades_score", "matched_evidence_pairs",
		"accepted_stem_constraints", "rejected_stem_constraints", "dot_bracket"}}
	yes := map[bool]string{true: "yes", false: "no"}
	for _, e := range cm.Randomisation.Structures {
		accepted := ""
		for i, id := range e.Accepted {
			if i > 0 {
				accepted += ","
			}
			accepted += id
		}
		rows = append(rows, []string{strconv.Itoa(e.Run), yes[e.Randomised], yes[e.Run == cm.Randomisation.SelectedRun],
			stOptional(e.Energy), jsNumber(e.Score), strconv.Itoa(e.MatchedPairs), accepted, strconv.Itoa(e.Rejected), e.DotBracket})
	}
	return rows
}

// comradesPanel shows the COMRADES(ViennaRNA) result of the window shown:
// score and supported pairs, the four stages, the ensemble, the accepted
// stems and the evidence pairs, with the web's three downloads.
func (p *structurePage) comradesPanel(c *ui.Context, a *structureResult) {
	t := c.Theme()
	if a.Meta.Engine != "COMRADES(ViennaRNA)" {
		return
	}
	index := p.shownRecord(a)
	if index < 0 {
		ui.Text(c, "COMRADES: choose a window above to see its score, ensemble and stem constraints.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if index >= len(a.Meta.Records) || a.Meta.Records[index].Comrades == nil {
		return
	}
	cm := a.Meta.Records[index].Comrades
	ev, rnd := &cm.Evidence, &cm.Randomisation
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, "COMRADES result").Bold().Grow(1)
			base := nameOr(stGene(a), "structure")
			for _, d := range []struct {
				label, tip, suffix string
				rows               func(*stComrades) [][]string
			}{
				{"Evidence TSV…", "RNAcofold evidence pairs (region positions) and their HYB-row observations", "_rna_cofold_evidence.tsv", stEvidenceRows},
				{"Constraints TSV…", "The F stem constraints, accepted and rejected", "_folding_constraints.tsv", stConstraintRows},
				{"Ensemble TSV…", "Every fold of the ensemble with its COMRADES score", "_structure_ensemble.tsv", stEnsembleRows},
			} {
				if ui.Button(c.Key(d.label), d.label).Tooltip(d.tip).Clicked() {
					rows := d.rows(cm)
					saveExport(p.a, "Save the COMRADES "+d.label[:len(d.label)-len(" TSV…")], exportName(d.suffix, base, p.viewA),
						func() ([]byte, error) { return webTSV(rows), nil })
				}
			}
		})
		ui.Text(c, fmt.Sprintf("COMRADES score %s · %s supported structure pair%s · selected fold: %s",
			jsNumber(ev.Score), FormatNumber(int64(ev.MatchedPairs)), plural(ev.MatchedPairs), stRunLabel(rnd.SelectedRun))).FontSize(13)
		ui.Text(c, fmt.Sprintf("1 · RNAcofold evidence: %s unique pairs from %s eligible HYB rows · 2 · ranked stems: %s, top %s fitted · "+
			"3 · accepted constraints: %s stems · 4 · ensemble: %s structures (ranked fold + %s seeded random orders, seed %s)",
			FormatNumber(int64(ev.UniqueBasePairs)), FormatNumber(int64(ev.InputRecords)), FormatNumber(int64(ev.RankedStems)),
			FormatNumber(int64(ev.RequestedStemConstraints)), FormatNumber(int64(len(ev.Accepted))),
			FormatNumber(int64(len(rnd.Structures))), FormatNumber(int64(max(len(rnd.Structures)-1, 0))), nameOr(rnd.Seed, "—"))).
			FontSize(12).TextColor(t.TextMuted)
		stSmallTable(c, "Structure ensemble (best first)", []string{"Fold", "Score", "Matched", "Energy", "Stems"}, min(len(rnd.Structures), 12),
			func(i int) []string {
				e := rnd.Structures[i]
				label := stRunLabel(e.Run)
				if e.Run == rnd.SelectedRun {
					label += " (shown)"
				}
				return []string{label, jsNumber(e.Score), strconv.Itoa(e.MatchedPairs), stEnergyCell(e.Energy), strconv.Itoa(len(e.Accepted))}
			})
		if len(rnd.Structures) > 12 {
			ui.Text(c, fmt.Sprintf("The top 12 of %d folds; Ensemble TSV has them all.", len(rnd.Structures))).FontSize(11).TextColor(t.TextMuted)
		}
		ui.Collapsible(c, fmt.Sprintf("Accepted stem constraints (%d)", len(ev.Accepted)), &p.stemsOpen, func() {
			if len(ev.Accepted) == 0 {
				ui.Text(c, "No evidence stem was compatible with this sequence.").TextColor(t.TextMuted)
				return
			}
			stSmallTable(c, "", []string{"Rank", "F constraint", "Length", "Evidence support"}, min(len(ev.Accepted), 75), func(i int) []string {
				s := ev.Accepted[i]
				return []string{strconv.Itoa(s.Rank), fmt.Sprintf("F %d %d %d", s.Left, s.Right, s.Length), strconv.Itoa(s.Length), jsNumber(s.Support)}
			})
		})
		ui.Collapsible(c, fmt.Sprintf("Top RNAcofold evidence pairs (%d)", ev.SelectedBasePairs), &p.evidenceOpen, func() {
			if len(ev.Pairs) == 0 {
				ui.Text(c, "No evidence pair list in this result (folded by an older version); the count is above.").TextColor(t.TextMuted)
				return
			}
			stSmallTable(c, "", []string{"Region left", "Region right", "HYB-row observations"}, min(len(ev.Pairs), 100), func(i int) []string {
				e := ev.Pairs[i]
				return []string{strconv.Itoa(e.One), strconv.Itoa(e.Two), strconv.Itoa(e.Count)}
			})
		})
	})
}

func stRunLabel(run int) string {
	if run == 0 {
		return "ranked"
	}
	return "random " + strconv.Itoa(run)
}

// stSmallTable is a short bordered table: a header and n rows of cells.
func stSmallTable(c *ui.Context, title string, header []string, n int, row func(i int) []string) {
	t := c.Theme()
	if title != "" {
		ui.Text(c, title).FontSize(12).Bold().TextColor(t.TextMuted)
	}
	ui.Column(c).Gap(0).Radius(8).Border(1, t.Border).Children(func() {
		line := func(cells []string, bold bool) {
			ui.Row(c).Padding(4, 10).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				for k, cell := range cells {
					text := ui.Text(c, cell).FontSize(12).SingleLine()
					if k == 0 {
						text.Grow(1)
					} else {
						text.Width(110).TextAlign(ui.End)
					}
					if bold {
						text.Bold()
					}
				}
			})
		}
		line(header, true)
		for i := range n {
			line(row(i), false)
		}
	})
}

// ---- the nucleotide inspector

// stNucleotideText describes position pos of the records shown, as the
// web's nucleotide inspector: its base, its partner (base, pair type,
// CPLfold layer) and its chimera support — the pair's HYB bonus (CPLfold)
// or RNAcofold evidence count (COMRADES), or for an unpaired base its
// strongest HYB bonus. "" when no record shown holds pos.
func stNucleotideText(records []structureRecord, pos int, engine string) string {
	for _, r := range records {
		k := r.localOf(pos)
		if k < 1 || k > len(r.Sequence) {
			continue
		}
		base := func(at int) string {
			if j := r.localOf(at); j >= 1 && j <= len(r.Sequence) {
				return string(r.Sequence[j-1])
			}
			return "?"
		}
		text := "Nucleotide " + FormatNumber(int64(pos)) + " " + base(pos)
		supportLabel := "chimera support"
		if engine == "COMRADES(ViennaRNA)" {
			supportLabel = "RNAcofold evidence"
		}
		for _, pr := range stRecordPairs(r) {
			if pr.I != pos && pr.J != pos {
				continue
			}
			other := pr.I + pr.J - pos
			text += fmt.Sprintf(" · paired with %s %s (%s–%s", FormatNumber(int64(other)), base(other), base(pr.I), base(pr.J))
			if engine == "CPLfold" || engine == "CPLcofold" {
				text += ", " + stLayerName(pr.Layer)
			}
			text += ")"
			for i, rp := range r.Pairs {
				if rp == [2]int{pr.I, pr.J} && i < len(r.Bonus) && r.Bonus[i] != nil {
					text += " · " + supportLabel + " " + stSupportText(*r.Bonus[i])
					break
				}
			}
			return text
		}
		text += " · unpaired"
		if k <= len(r.NucleotideSupport) {
			text += " · strongest chimera support " + stSupportText(r.NucleotideSupport[k-1])
		}
		return text
	}
	return ""
}

// stSupportText is a support value to three decimals, trailing zeros dropped.
func stSupportText(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "—"
	}
	return jsNumber(math.Round(v*1000) / 1000)
}
