package views

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/widgets"
)

// A single result (A, no B) shown as the web's RNA Structure result: pairs
// styled by engine (structurePairStyle), the raw HYB arms under the arcs,
// the metrics, the mode note, the nucleotide inspector, the sequence /
// dot-bracket alignment, the base-pair list and "How this was computed".

// stArms is a window's HYB evidence for display (runtime arms.json).
type stArms struct {
	Rows   int      `json:"rows"`
	Unique int      `json:"unique"`
	Shown  [][5]int `json:"shown"`
}

// stEngine is a result's engine as the web names it.
func stEngine(a *structureResult) string { return nameOr(a.Meta.Engine, "CPLfold") }

func stIsCPL(a *structureResult) bool {
	e := stEngine(a)
	return e == "CPLfold" || e == "CPLcofold"
}

// stGuided: the fold used HYB evidence (a CPLfold bonus matrix, or
// COMRADES' RNAcofold evidence).
func stGuided(a *structureResult) bool {
	if stEngine(a) == "COMRADES(ViennaRNA)" {
		return true
	}
	return stIsCPL(a) && a.Meta.Parameters["evidence"] != "none"
}

var stViolet = ui.RGB(106, 79, 242)

// stPairInfo is one shown pair of A with what styles and describes it.
type stPairInfo struct {
	I, J   int
	Layer  int
	BaseI  byte
	BaseJ  byte
	Bonus  float64 // HYB bonus (CPLfold) or RNAcofold count (COMRADES)
	Hard   bool
	record int
}

// stShownPairs are A's pairs in the records shown, with their bases.
func stShownPairs(records []structureRecord, hard map[[2]int]bool) []stPairInfo {
	var out []stPairInfo
	for ri, r := range records {
		weights := stWeights([]structureRecord{r})
		base := func(pos int) byte {
			if k := r.localOf(pos); k >= 1 && k <= len(r.Sequence) {
				return r.Sequence[k-1]
			}
			return '?'
		}
		for _, pr := range stRecordPairs(r) {
			out = append(out, stPairInfo{I: pr.I, J: pr.J, Layer: pr.Layer, BaseI: base(pr.I), BaseJ: base(pr.J),
				Bonus: weights[[2]int{pr.I, pr.J}], Hard: hard[[2]int{pr.I, pr.J}], record: ri})
		}
	}
	return out
}

// stPairType is "G–C", "A–U", "G–U" or the bases joined.
func stPairType(x, y byte) string {
	s := string([]byte{x}) + "–" + string([]byte{y})
	switch s {
	case "C–G":
		return "G–C"
	case "U–A":
		return "A–U"
	case "U–G":
		return "G–U"
	}
	return s
}

// stMetaIndex is the index in a.Meta.Records (and a.Records) of the
// shown record r, the window with r's span; -1 when there is none.
func stMetaIndex(a *structureResult, r structureRecord) int {
	for k := range a.Records {
		if a.Records[k].WindowStart == r.WindowStart && a.Records[k].WindowEnd == r.WindowEnd {
			if k < len(a.Meta.Records) {
				return k
			}
			return -1
		}
	}
	return -1
}

// stCPLEvidence is the web's cplfoldEvidence for each shown record: a
// CPLfold record folded with HYB evidence (evidence.source
// "hyb-block-intervals"; without its summary, a pair with a bonus).
func stCPLEvidence(a *structureResult, records []structureRecord) []bool {
	out := make([]bool, len(records))
	if !stIsCPL(a) {
		return out
	}
	for i, r := range records {
		if k := stMetaIndex(a, r); k >= 0 {
			out[i] = a.Meta.Records[k].Evidence.Source == "hyb-block-intervals"
		} else {
			out[i] = stHybEvidence(&structureResult{Records: []structureRecord{r}})
		}
	}
	return out
}

// stBonusScale is what each shown record's HYB bonus opacity is relative
// to, as the web's: evidence.maximumBonus, the bonus matrix maximum (the
// strongest pair bonus for a result without it).
func stBonusScale(a *structureResult, records []structureRecord) []float64 {
	out := make([]float64, len(records))
	for i, r := range records {
		if k := stMetaIndex(a, r); k >= 0 && a.Meta.Records[k].Evidence.MaximumBonus > 0 {
			out[i] = a.Meta.Records[k].Evidence.MaximumBonus
			continue
		}
		for _, w := range stWeights([]structureRecord{r}) {
			out[i] = math.Max(out[i], w)
		}
	}
	return out
}

// stBonusAlpha is the web's CPLfold evidence opacity of a pair: 0.32 for
// no bonus to 0.96 at the scale.
func stBonusAlpha(bonus, scale float64) float64 {
	return math.Max(0.32, math.Min(0.96, 0.32+bonus/math.Max(scale, 1e-12)*0.64))
}

// stArcStyles are the web's pair styles by engine for A's arcs, aligned
// with pairs (stShownPairs of records): CPLfold by layer (opacity by HYB
// bonus when it folded with HYB evidence), COMRADES by RNAcofold
// evidence, ViennaRNA by pair type.
func stArcStyles(a *structureResult, records []structureRecord, pairs []stPairInfo, t *ui.Theme) []widgets.ArcStyle {
	out := make([]widgets.ArcStyle, len(pairs))
	engine := stEngine(a)
	top := 0.0
	for _, pr := range pairs {
		top = math.Max(top, pr.Bonus)
	}
	evidence, scale := stCPLEvidence(a, records), stBonusScale(a, records)
	typeWidth := func(pr stPairInfo) float32 {
		if stPairType(pr.BaseI, pr.BaseJ) == "G–C" {
			return 1
		}
		return 0.75
	}
	for i, pr := range pairs {
		switch {
		case engine == "COMRADES(ViennaRNA)":
			color, width, alpha := t.TextMuted, typeWidth(pr), float32(0.34)
			if pr.Bonus > 0 {
				color, width, alpha = t.Accent, 1.6, 0.9
				if top > 0 && pr.Bonus/top >= 0.55 {
					color = t.Warning
				}
			}
			if pr.Hard {
				width = 2.2
			}
			out[i] = widgets.ArcStyle{Color: color.Alpha(alpha), Width: width}
		case stIsCPL(a):
			color, width := t.Accent, typeWidth(pr)
			if pr.Layer == 1 {
				color, width = stViolet, 1.4
			} else if pr.Layer > 1 {
				color, width = t.Warning, 1.4
			}
			alpha := float32(0.9)
			if pr.record < len(evidence) && evidence[pr.record] {
				// a few opacity steps keep one path per style
				alpha = float32(math.Round(stBonusAlpha(pr.Bonus, scale[pr.record])*10) / 10)
			}
			out[i] = widgets.ArcStyle{Color: color.Alpha(alpha), Width: width}
		default:
			color := t.TextMuted
			switch stPairType(pr.BaseI, pr.BaseJ) {
			case "G–C":
				color = t.Accent
			case "A–U":
				color = stViolet
			}
			width := typeWidth(pr)
			if pr.Hard {
				width = 2.2
			}
			out[i] = widgets.ArcStyle{Color: color.Alpha(0.9), Width: width}
		}
	}
	return out
}

// stArcLegend is the web's legend for A's pair styles.
func stArcLegend(c *ui.Context, a *structureResult) {
	t := c.Theme()
	switch {
	case stEngine(a) == "COMRADES(ViennaRNA)":
		stLegend(c, t.Warning, "high evidence")
		stLegend(c, t.Accent, "low evidence")
		stLegend(c, t.TextMuted, "MFE only")
	case stIsCPL(a):
		stLegend(c, t.Accent, "phase 1")
		if on, _ := a.Meta.Parameters["allow_pseudoknot"].(bool); on {
			stLegend(c, stViolet, "pseudoknot")
			stLegend(c, t.Warning, "deeper layer")
		}
	default:
		stLegend(c, t.Accent, "G–C")
		stLegend(c, stViolet, "A–U")
		stLegend(c, t.TextMuted, "G–U")
	}
}

// stRawArms are the raw HYB interactions under the arcs: one arc per arm
// block joining the arms' midpoints (the web's "Raw HYB interactions ·
// below"), and the label saying how many records they stand for. The
// blocks are in the record's prepared-sequence positions (a window's, or
// a two-strand record's A+B indices), drawn where the record draws them.
func stRawArms(records []structureRecord, t *ui.Theme) ([]widgets.OverlayArc, string, string) {
	var arcs []widgets.OverlayArc
	attached, rows, shownRows, more := false, 0, 0, false
	for _, r := range records {
		if r.Arms == nil {
			continue
		}
		attached = true
		rows += r.Arms.Rows
		more = more || r.Arms.Unique > len(r.Arms.Shown)
		n := len(r.Sequence)
		for _, b := range r.Arms.Shown {
			if b[0] < 1 || b[0] > b[1] || b[1] > n || b[2] < 1 || b[2] > b[3] || b[3] > n {
				continue // as the web's, only arms inside the sequence
			}
			one := r.positionOf((b[0] + b[1]) / 2)
			two := r.positionOf((b[2] + b[3]) / 2)
			if one > two {
				one, two = two, one
			}
			if one == two {
				continue
			}
			shownRows += b[4]
			arcs = append(arcs, widgets.OverlayArc{I: one, J: two})
		}
	}
	if !attached {
		return nil, "Raw HYB data not attached to this result", "No synthetic or predicted arcs are substituted for missing raw data."
	}
	sub := "Arm midpoints on prepared-sequence coordinates; eligible mapped input records only."
	if rows == 0 {
		return nil, "No eligible HYB records mapped to this structure", sub
	}
	alpha := float32(0.6)
	if rows >= 100 {
		alpha = 0.3
	}
	for i := range arcs {
		arcs[i].Color, arcs[i].Width = t.Accent.Alpha(alpha), 1.1
	}
	if more {
		// the runtime keeps the stRawArmsShown most frequent arm blocks
		sub += fmt.Sprintf(" First %s displayed.", FormatNumber(stRawArmsShown))
	}
	return arcs, fmt.Sprintf("Raw HYB interactions · below · %s / %s mapped records shown", FormatNumber(int64(shownRows)), FormatNumber(int64(rows))), sub
}

// stRawArmsShown is how many arm blocks the runtime keeps per window
// (structure.py ARMS_SHOWN), as the web draws its first 2,000 records.
const stRawArmsShown = 2000

// ---- the result details under the diagram (A alone)

func (p *structurePage) resultDetails(c *ui.Context, a *structureResult) {
	t := c.Theme()
	records := p.shownA
	pairs := stShownPairs(records, stHardPairs(a))
	length := 0
	for _, r := range records {
		length += len(r.Sequence)
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Text(c, "Structure result").FontSize(15).Bold()
		ui.Text(c, stResultLabel(a, records)+" · "+FormatNumber(int64(length))+" nt").FontSize(12).TextColor(t.TextMuted)
	})
	if note := stChainBreakNote(a, records); note != "" {
		ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
	}
	if warning := stEnergyWarning(a); warning != "" {
		ui.Row(c).Padding(8, 12).Radius(8).Background(t.Warning.Alpha(0.12)).Border(1, t.Warning.Alpha(0.5)).Children(func() {
			ui.Text(c, warning).FontSize(12).Grow(1)
		})
	}
	p.resultMetrics(c, a, records, pairs, length)
	if note, text := stModeNote(a, records); note != "" {
		ui.Column(c).Gap(2).Padding(10, 12).Radius(8).Background(t.Surface).Children(func() {
			ui.Text(c, note).Bold().FontSize(13)
			ui.Text(c, text).FontSize(12).TextColor(t.TextMuted)
		})
	}
	p.nucleotideInspector(c, a, records)
	p.sequenceAlignment(c, records)
	p.pairList(c, a, pairs)
	p.howComputed(c, a, records)
}

// stResultLabel is the web's result label (the sequence folded) of the
// records shown: "RNA:start-end", a two-strand fold's two regions, or the
// RNA and how many windows.
func stResultLabel(a *structureResult, records []structureRecord) string {
	gene := nameOr(stGene(a), "RNA")
	switch {
	case len(records) == 1 && records[0].Cutpoint > 0 && len(records[0].Segments) == 2:
		return stRegionsLabel(records[0].Segments)
	case len(records) == 1:
		start, end := records[0].span()
		return fmt.Sprintf("%s:%d-%d", gene, start, end)
	}
	return fmt.Sprintf("%s · %s windows", gene, FormatNumber(int64(len(records))))
}

// stChainBreakNote is the web's note on a CPLcofold result's two strands.
func stChainBreakNote(a *structureResult, records []structureRecord) string {
	if stEngine(a) != "CPLcofold" || len(records) != 1 || records[0].Cutpoint <= 0 {
		return ""
	}
	cut, n := records[0].Cutpoint, len(records[0].Sequence)
	return fmt.Sprintf("CPLcofold · two RNA molecules · A %s nt + B %s nt. Chain break after position %s; coordinates exclude &. Arc view shows the separate backbones.",
		FormatNumber(int64(cut)), FormatNumber(int64(n-cut)), FormatNumber(int64(cut)))
}

// stEnergyWarning is the web's warning for an experimental pseudoknot
// energy (energy_kind "heuristic"), else "".
func stEnergyWarning(a *structureResult) string {
	md := a.Meta.EnergyMetadata
	if kind, _ := md["energy_kind"].(string); kind != "heuristic" {
		return ""
	}
	model, _ := md["energy_model"].(string)
	return "Experimental pseudoknot energy (" + nameOr(model, "Turner/DP") + "). This ranking score is not a validated binding free energy."
}

// resultMetrics are the web's metric cards.
func (p *structurePage) resultMetrics(c *ui.Context, a *structureResult, records []structureRecord, pairs []stPairInfo, length int) {
	t := c.Theme()
	energy, effective, haveEnergy := 0.0, 0.0, false
	for _, r := range records {
		if r.Energy != nil {
			energy += *r.Energy
			haveEnergy = true
		}
		if k := r.SelectedCandidate; k >= 0 && k < len(r.Candidates) && r.Candidates[k].EffectiveEnergy != nil {
			effective += *r.Candidates[k].EffectiveEnergy
		} else if r.Energy != nil {
			effective += *r.Energy
		}
	}
	paired := map[int]bool{}
	knotted := 0
	for _, pr := range pairs {
		paired[pr.I], paired[pr.J] = true, true
		if pr.Layer > 0 {
			knotted++
		}
	}
	metric := func(label, value, note string) {
		ui.Column(c).Gap(2).Padding(10, 12).Radius(8).Border(1, t.Border).Grow(1).Basis(150).MinWidth(140).Children(func() {
			ui.Text(c, label).FontSize(11).Bold().TextColor(t.TextMuted)
			ui.Text(c, value).FontSize(18).Bold()
			if note != "" {
				ui.Text(c, note).FontSize(11).TextColor(t.TextMuted)
			}
		})
	}
	engine := stEngine(a)
	ui.Row(c).Gap(10).Wrap().Children(func() {
		label, model := "MFE", ""
		if engine == "IRIS" {
			label = "Energy" // of the member shown
		}
		if stIsCPL(a) {
			label = "Energy"
			model = "Turner 2004"
			if on, _ := a.Meta.Parameters["allow_pseudoknot"].(bool); on {
				model = fmt.Sprint(nameOr(fmt.Sprint(a.Meta.Parameters["energy_model"]), "DP09"))
			}
			if m, _ := a.Meta.EnergyMetadata["energy_model"].(string); m != "" {
				model = m // the web names the model the energy came from first
			}
		}
		value := "—"
		if haveEnergy {
			value = strconv.FormatFloat(energy, 'f', 2, 64)
		}
		metric(label, value, strings.TrimSuffix("kcal/mol · "+model, " · "))
		topology := "non-crossing pairs"
		if stIsCPL(a) {
			topology = "nested topology"
			if knotted > 0 {
				topology = "pseudoknotted topology"
			}
		}
		metric("Base pairs", FormatNumber(int64(len(pairs))), topology)
		metric("Unpaired bases", FormatNumber(int64(length-len(paired))), "of "+FormatNumber(int64(length))+" nt")
		switch {
		case stIsCPL(a) && len(records) == 1 && len(records[0].Candidates) > 0:
			r := records[0]
			phase := "phase 1 secondary"
			if on, _ := a.Meta.Parameters["allow_pseudoknot"].(bool); on {
				phase = "phase 1 nested"
				if knotted > 0 {
					phase = "phase 1 + crossing phase 2"
				}
			}
			metric("Candidate", "#"+strconv.Itoa(r.SelectedCandidate+1), phase)
		case engine == "COMRADES(ViennaRNA)":
			n := 0
			for _, pr := range pairs {
				if pr.Hard {
					n++
				}
			}
			metric("Constraint mode", "HYB-guided", fmt.Sprintf("%s enforced pair%s", FormatNumber(int64(n)), plural(n)))
			score, supported := 0.0, 0
			for _, pr := range pairs {
				if pr.Bonus > 0 {
					score += 2 * pr.Bonus
					supported++
				}
			}
			metric("COMRADES score", strconv.FormatFloat(score, 'f', -1, 64), FormatNumber(int64(supported))+" supported structure pairs")
		case engine == "IRIS":
			n := len(stHardPairs(a))
			metric("Constraint mode", "IRIS ensemble", fmt.Sprintf("%s enforced pair%s", FormatNumber(int64(n)), plural(n)))
		case !stIsCPL(a):
			mode, n := "None", len(stHardPairs(a))
			if n > 0 {
				mode = "Manual hard"
			}
			metric("Constraint mode", mode, fmt.Sprintf("%s enforced pair%s", FormatNumber(int64(n)), plural(n)))
		}
		if stIsCPL(a) && haveEnergy && math.Abs(effective-energy) > 1e-9 {
			metric("Effective energy", strconv.FormatFloat(effective, 'f', 2, 64), "kcal/mol · beta-adjusted rank")
		}
		if s := a.Meta.Timings.TotalS; s > 0 {
			metric("Elapsed", FormatDuration(s), "on this computer")
		}
	})
}

// stCandidatesFound counts the shown records' candidates, from the
// candidates read, else from the result's summaries.
func stCandidatesFound(a *structureResult, records []structureRecord) int {
	found := 0
	for _, r := range records {
		n := len(r.Candidates)
		if k := stMetaIndex(a, r); n == 0 && k >= 0 {
			n = a.Meta.Records[k].Candidates
		}
		found += n
	}
	return found
}

// stModeNote is the web's mode note: its title and text.
func stModeNote(a *structureResult, records []structureRecord) (string, string) {
	switch {
	case stIsCPL(a):
		found := stCandidatesFound(a, records)
		on, _ := a.Meta.Parameters["allow_pseudoknot"].(bool)
		count := func(noun string) string {
			return fmt.Sprintf("%s ranked %s %s%s. ", stEngine(a), FormatNumber(int64(found)), noun, plural(found))
		}
		if !on {
			return "Pseudoknot search disabled", count("phase-1 secondary-structure candidate") +
				"No crossing Phase 2 layer was generated. Only Phase 1 secondary structures are ranked; beta is ignored."
		}
		ranked := count("unique candidate")
		title := "Nested candidate"
		for _, r := range records {
			if strings.ContainsAny(r.DBN, "[]{}<>") {
				title = "Pseudoknot candidate"
			}
		}
		return title, ranked + "Parentheses show phase-1 pairs; square brackets show the crossing phase-2 layer."
	case stEngine(a) == "COMRADES(ViennaRNA)":
		return "HYB-evidence-selected structure", "The selected ensemble member maximises the original nucleotide-summed COMRADES support score. Supported arcs are coloured by RNAcofold evidence; fitted hard-pair arcs remain thicker."
	case stEngine(a) == "IRIS":
		return "", "" // the weighted ensemble table is its note (structure_iris.go)
	case len(stHardPairs(a)) > 0:
		n := len(stHardPairs(a))
		return "Manual hard-pair result", fmt.Sprintf("ViennaRNA enforced %d user-entered pair%s. These pairs were not generated from HYB interaction evidence.", n, plural(n))
	}
	return "", ""
}

// nucleotideInspector is the web's inspector card for the selected base.
func (p *structurePage) nucleotideInspector(c *ui.Context, a *structureResult, records []structureRecord) {
	t := c.Theme()
	sel := p.arcs.Selected
	what := "ViennaRNA MFE structure"
	if stIsCPL(a) {
		what = stEngine(a) + " candidate"
	} else if stEngine(a) == "COMRADES(ViennaRNA)" {
		what = "COMRADES(ViennaRNA) structure"
	} else if stEngine(a) == "IRIS" {
		what = "IRIS ensemble member"
	}
	ui.Column(c).Gap(4).Padding(10, 12).Radius(8).Border(1, t.Border).Children(func() {
		ui.Text(c, "NUCLEOTIDE INSPECTOR").FontSize(11).Bold().TextColor(t.TextMuted)
		if sel <= 0 {
			ui.Text(c, "Click the diagram, or focus it and use the arrow keys, to inspect a base and its "+what+" pairing partner.").FontSize(12).TextColor(t.TextMuted)
			return
		}
		text := stNucleotideText(records, sel, stEngine(a))
		if text == "" {
			ui.Text(c, "Position "+FormatNumber(int64(sel))+" is outside the folded region.").FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Text(c, "Nucleotide "+FormatNumber(int64(sel))).FontSize(16).Bold()
		detail := strings.TrimPrefix(text, "Nucleotide "+FormatNumber(int64(sel))+" ")
		if strings.Contains(detail, "unpaired") {
			detail += " · unpaired in this " + what
		}
		ui.Text(c, detail).FontSize(12)
	})
}

// sequenceAlignment is the web's "Sequence / dot-bracket": 50 nt a line in
// groups of 10, each line led by its position, the structure under the
// sequence, the inspected base marked and "&" at a two-strand break.
func (p *structurePage) sequenceAlignment(c *ui.Context, records []structureRecord) {
	t := c.Theme()
	sel := p.arcs.Selected
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Sequence / dot-bracket").Bold().Grow(1)
			if ui.Button(c, "Copy dot-bracket").Clicked() {
				var parts []string
				for _, r := range records {
					parts = append(parts, stWithBreak(r.DBN, r.Cutpoint))
				}
				c.WriteClipboard(strings.Join(parts, "\n"))
				p.a.Toast("Dot-bracket notation copied to the clipboard.")
			}
		})
		ui.Column(c).Gap(0).Padding(8, 10).Radius(6).Background(t.Surface).MaxHeight(360).Children(func() {
			ui.Scroll(c.Key("st-seq")).MaxHeight(340).Children(func() {
				for ri, r := range records {
					if len(records) > 1 {
						s, e := r.span()
						ui.Text(c.Key(fmt.Sprint("w", ri)), fmt.Sprintf("Window %s–%s", FormatNumber(int64(s)), FormatNumber(int64(e)))).FontSize(11).Bold().TextColor(t.TextMuted)
					}
					for at := 0; at < len(r.Sequence); at += 50 {
						end := min(at+50, len(r.Sequence))
						p.alignmentLine(c.Key(fmt.Sprint(ri, ":", at)), r, at, end, sel)
					}
				}
			})
		})
	})
}

func (p *structurePage) alignmentLine(c *ui.Context, r structureRecord, at, end, sel int) {
	t := c.Theme()
	row := func(text string, from int, numbered bool) {
		ui.RichText(c).Font("monospace").FontSize(11).Children(func() {
			label := ""
			if numbered {
				label = FormatNumber(int64(r.positionOf(from + 1)))
			}
			ui.Text(c, fmt.Sprintf("%7s  ", label))
			for k := from; k < end; k++ {
				if r.Cutpoint > 0 && k == r.Cutpoint {
					ui.Text(c, "&").TextColor(t.Warning).Tooltip(fmt.Sprintf("Chain break: strand A ends at %d, strand B starts at %d", r.Cutpoint, r.Cutpoint+1))
				}
				ch := string(text[k])
				el := ui.Text(c, ch)
				if sel > 0 && r.positionOf(k+1) == sel {
					el.Background(t.Accent.Alpha(0.3))
				}
				if (k+1-at)%10 == 0 && k+1 < end {
					ui.Text(c, " ")
				}
			}
		})
	}
	ui.Column(c).Gap(0).Padding(0, 0, 4, 0).Children(func() {
		row(r.Sequence, at, true)
		if len(r.DBN) == len(r.Sequence) {
			row(r.DBN, at, false)
		}
	})
}

// stWithBreak puts "&" at a two-strand result's cutpoint.
func stWithBreak(s string, cut int) string {
	if cut > 0 && cut < len(s) {
		return s[:cut] + "&" + s[cut:]
	}
	return s
}

// pairList is the web's "Base-pair list (N)".
func (p *structurePage) pairList(c *ui.Context, a *structureResult, pairs []stPairInfo) {
	t := c.Theme()
	ui.Collapsible(c.Key("st-pair-list"), fmt.Sprintf("Base-pair list (%s)", FormatNumber(int64(len(pairs)))), &p.pairListOpen, func() {
		if len(pairs) == 0 {
			ui.Text(c, "The selected predictor did not place any base pairs.").TextColor(t.TextMuted)
			return
		}
		ui.Scroll(c.Key("st-pairs")).MaxHeight(320).Children(func() {
			for i, pr := range pairs {
				label := "MFE pair"
				switch {
				case stIsCPL(a):
					label = stLayerName(pr.Layer)
				case pr.Hard:
					label = "Hard pair"
				}
				if pr.Bonus > 0 {
					label += " · evidence " + strconv.FormatFloat(pr.Bonus, 'f', 3, 64)
				}
				text := fmt.Sprintf("%s %c | %s | %s %c | %s", FormatNumber(int64(pr.I)), pr.BaseI, stPairType(pr.BaseI, pr.BaseJ),
					FormatNumber(int64(pr.J)), pr.BaseJ, label)
				el := ui.Text(c.Key(fmt.Sprint(i)), fmt.Sprintf("%d. %s", i+1, text)).Font("monospace").FontSize(11)
				if pr.Hard {
					el.Bold()
				}
			}
		})
	})
}

// stComputedScope is the web's scope sentence under "How this was
// computed" for the ViennaRNA engines ("" for CPLfold and IRIS, which have
// their own). The web's "in a local WebAssembly worker" is "on this
// computer" here.
func stComputedScope(a *structureResult) string {
	switch stEngine(a) {
	case "COMRADES(ViennaRNA)":
		return "This reproduces the ViennaRNA path after a HYB file: RNAcofold evidence, ranked F-stem constraints, iterative compatibility fitting, seeded randomised folds, COMRADES scoring, and evidence colouring."
	case "ViennaRNA":
		if len(stHardPairs(a)) > 0 {
			return "ViennaRNA minimum-free-energy fold on this computer, with the hard pairs you entered."
		}
		return "ViennaRNA minimum-free-energy fold on this computer, without base-pair constraints."
	}
	return ""
}

// howComputed is the web's "How this was computed": CPLfold's stages and
// its bonus matrix heatmap; for ViennaRNA and COMRADES the method's scope
// (COMRADES' stages are in its result panel).
func (p *structurePage) howComputed(c *ui.Context, a *structureResult, records []structureRecord) {
	t := c.Theme()
	if scope := stComputedScope(a); scope != "" {
		ui.Collapsible(c.Key("st-computed"), "How this was computed", &p.computedOpen, func() {
			ui.Text(c, scope).FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	if !stIsCPL(a) {
		return
	}
	ui.Collapsible(c.Key("st-computed"), "How this was computed", &p.computedOpen, func() {
		s := stStagesOf(a, records)
		stage := func(title, value, note string) {
			ui.Column(c).Gap(2).Padding(10, 12).Radius(8).Background(t.Surface).Grow(1).Basis(180).MinWidth(170).Children(func() {
				ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted)
				ui.Text(c, value).FontSize(15).Bold()
				ui.Text(c, note).FontSize(11).TextColor(t.TextMuted)
			})
		}
		ui.Row(c).Gap(10).Wrap().Children(func() {
			for _, card := range s {
				stage(card[0], card[1], card[2])
			}
		})
		if guided := slices.Contains(stCPLEvidence(a, records), true); guided && len(records) == 1 {
			p.bonusHeatmap(c, records[0])
		}
	})
}

// stShownCrossingPairs is the web's crossingPairs (cplfold_web.py
// _crossing_pair_count) of the records shown: the pairs that cross
// another pair of their record, both of each crossing counted once.
func stShownCrossingPairs(records []structureRecord) int {
	n := 0
	for _, r := range records {
		pairs := stRecordPairs(r)
		slices.SortFunc(pairs, func(x, y stPair) int { return x.I - y.I })
		crossing := make([]bool, len(pairs))
		for i, x := range pairs {
			// a later pair opening inside x crosses it when it closes after x
			for k := i + 1; k < len(pairs) && pairs[k].I < x.J; k++ {
				if y := pairs[k]; x.I < y.I && y.J > x.J {
					crossing[i], crossing[k] = true, true
				}
			}
		}
		for _, on := range crossing {
			if on {
				n++
			}
		}
	}
	return n
}

// stStagesOf are the web's CPLfold stage cards (renderCplfoldStages) of
// the records shown: title, value and note of each.
func stStagesOf(a *structureResult, records []structureRecord) [][3]string {
	params := a.Meta.Parameters
	rows, unique, cells, oldCells, maxBonus := 0, 0, 0, 0, 0.0
	upper, guided := true, false
	for _, r := range records {
		k := stMetaIndex(a, r)
		if k < 0 {
			if r.Arms != nil { // no summary: the arms read
				rows, unique = rows+r.Arms.Rows, unique+r.Arms.Unique
			}
			continue
		}
		ev := a.Meta.Records[k].Evidence
		guided = guided || ev.Source == "hyb-block-intervals"
		rows, unique = rows+ev.InputRecords, unique+ev.UniqueBlocks
		maxBonus = math.Max(maxBonus, ev.MaximumBonus)
		oldCells += ev.NonzeroBonusCells
		if ev.NonzeroUpperTriangleCells != nil {
			cells += *ev.NonzeroUpperTriangleCells
		} else if ev.NonzeroBonusCells > 0 {
			upper = false
		}
	}
	on, _ := params["allow_pseudoknot"].(bool)
	blocks := "sequence-only run"
	if guided {
		blocks = FormatNumber(int64(unique)) + " unique prepared interval blocks"
	}
	// a result folded before the upper-triangle count was kept has only
	// the matrix's nonzero cells (both triangles and the diagonal)
	matrix := FormatNumber(int64(cells)) + " upper-triangle cells"
	if !upper {
		matrix = FormatNumber(int64(oldCells)) + " nonzero cells"
	}
	search, searchNote := "3 · Secondary search", "Phase 2 skipped · beta ignored"
	if on {
		beam, _ := params["beam"].(float64)
		delta, _ := params["energy_delta"].(float64)
		search, searchNote = "3 · Two-phase search", "beam "+FormatNumber(int64(beam))+" · ΔE "+jsFixed(delta, 3)
	}
	crossing := stShownCrossingPairs(records)
	topology, topologyNote := "nested", "pseudoknot-free candidate"
	if crossing > 0 {
		topology = "pseudoknotted"
	}
	if on {
		topologyNote = FormatNumber(int64(crossing)) + " base pairs participate in crossings"
	}
	return [][3]string{
		{"1 · HYB blocks", stCountLabel(rows, "row"), blocks},
		{"2 · Bonus matrix", matrix, "maximum log1p bonus " + jsFixed(maxBonus, 3)},
		{search, stCountLabel(stCandidatesFound(a, records), "candidate"), searchNote},
		{"4 · Selected topology", topology, topologyNote},
	}
}

// stHeatBins is the bonus heatmap's side in cells.
const stHeatBins = 120

// bonusHeatmap is the web's "HYB-derived CPLfold bonus matrix": the upper
// triangle of the window's bonus matrix, binned to stHeatBins² cells (the
// strongest bonus of each), colour intensity by log1p bonus.
func (p *structurePage) bonusHeatmap(c *ui.Context, r structureRecord) {
	t := c.Theme()
	if r.BonusMatrixPath == "" {
		ui.Text(c, "The bonus matrix was not written for this result (more than 2,000,000 cells), so its heatmap is not drawn.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	heat := p.loadHeat(r.BonusMatrixPath, len(r.Sequence))
	if heat == nil {
		ui.Spinner(c).Label("Reading the bonus matrix…")
		return
	}
	if heat.err != "" {
		ui.Text(c, "Could not read the bonus matrix: "+heat.err).FontSize(12).TextColor(t.Danger)
		return
	}
	color := ui.RGB(156, 5, 248)
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "HYB-derived CPLfold bonus matrix").Bold().FontSize(13)
		ui.Text(c, "Upper triangle · folded-window coordinates · colour intensity is log1p Gaussian block support.").FontSize(11).TextColor(t.TextMuted)
		const side = 240
		ui.Box(c).Size(side, side).Role(ui.RoleImage).Label("HYB bonus matrix heatmap").Draw(func(pt *ui.Painter, rect ui.Rect) {
			pt.Fill(rect, t.Background, 0)
			cell := float32(side) / stHeatBins
			for y := 0; y < stHeatBins; y++ {
				for x := y; x < stHeatBins; x++ {
					v := heat.cells[y*stHeatBins+x]
					if v <= 0 || heat.max <= 0 {
						continue
					}
					alpha := float32(math.Max(0.08, v/heat.max))
					pt.Fill(ui.Rect{X: rect.X + float32(x)*cell, Y: rect.Y + float32(y)*cell, W: cell + 0.3, H: cell + 0.3}, color.Alpha(alpha), 0)
				}
			}
			pt.Stroke(rect, t.Border, 0, 1)
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "log1p bonus · low").FontSize(11).TextColor(t.TextMuted)
			ui.Box(c).Size(120, 8).Draw(func(pt *ui.Painter, rect ui.Rect) {
				for i := range 24 {
					w := rect.W / 24
					pt.Fill(ui.Rect{X: rect.X + float32(i)*w, Y: rect.Y, W: w + 0.5, H: rect.H}, color.Alpha(float32(0.08+0.92*float64(i)/23)), 0)
				}
			})
			ui.Text(c, "high · max "+strconv.FormatFloat(heat.max, 'f', 3, 64)).FontSize(11).TextColor(t.TextMuted)
		})
	})
}

type stHeat struct {
	cells []float64
	max   float64
	err   string
}

// loadHeat is the binned bonus matrix at path of an n-nt window, read off
// the UI thread (a matrix holds up to 2,000,000 cells) and kept; nil while
// it is read.
func (p *structurePage) loadHeat(path string, n int) *stHeat {
	key := fmt.Sprintf("%s\n%d", path, n)
	if heat := p.heatCache[key]; heat != nil {
		return heat
	}
	if p.heatCache == nil {
		p.heatCache, p.heatReading = map[string]*stHeat{}, map[string]bool{}
	}
	if p.heatReading[key] {
		return nil
	}
	p.heatReading[key] = true
	a, project := p.a, p.project
	a.Go(func() func() {
		heat := stReadHeat(path, n)
		return func() {
			if p.project != project || p.heatCache == nil {
				return // another project was opened meanwhile
			}
			delete(p.heatReading, key)
			p.heatCache[key] = heat
		}
	})
	return nil
}

// stReadHeat bins a bonus matrix TSV (position, position, value; # comments)
// of an n-nt window into stHeatBins² cells, keeping each cell's maximum.
func stReadHeat(path string, n int) *stHeat {
	h := &stHeat{cells: make([]float64, stHeatBins*stHeatBins)}
	f, err := os.Open(path)
	if err != nil {
		h.err = err.Error()
		return h
	}
	defer f.Close()
	n = max(n, 1)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		i, err1 := strconv.Atoi(parts[0])
		j, err2 := strconv.Atoi(parts[1])
		v, err3 := strconv.ParseFloat(parts[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		if i > j {
			i, j = j, i
		}
		x, y := min(stHeatBins-1, (j-1)*stHeatBins/n), min(stHeatBins-1, (i-1)*stHeatBins/n)
		if k := y*stHeatBins + x; v > h.cells[k] {
			h.cells[k] = v
		}
		h.max = math.Max(h.max, v)
	}
	if err := sc.Err(); err != nil {
		h.err = err.Error()
	}
	return h
}
