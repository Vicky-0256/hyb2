package views

// ⑤ Structure: the web app's other views of structure A — circular,
// radial, base-pair matrix and folded (ViennaRNA's naview layout, from
// structure_layout) — next to the arc diagram, with an SVG export of each.
// A alone is drawn as the web's: pairs styled by engine
// (structurePairStyle), its legend, nucleotide markers filled by evidence,
// the pair and nucleotide readouts, and the folded view's zoom.

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/widgets"
)

var (
	stViewLabels = []string{"Arc", "Circular", "Radial", "Matrix", "Folded"}
	stViewModes  = map[string]widgets.StructMode{"Circular": widgets.ModeCircular, "Radial": widgets.ModeRadial,
		"Matrix": widgets.ModeMatrix, "Folded": widgets.ModeFolded}
	// stViewTitles are the web's diagram titles.
	stViewTitles = map[widgets.StructMode]string{widgets.ModeCircular: "Circular contact view", widgets.ModeRadial: "Radial diagram",
		widgets.ModeMatrix: "Base-pair matrix", widgets.ModeFolded: "Folded RNA structure"}
)

// The web's folded-view hints (forna's, without its name: the desktop
// draws ViennaRNA's naview layout).
const (
	stFoldHint     = "Scroll or pinch to zoom. Drag the canvas to pan. Keyboard: + / − to zoom, arrows to pan, 0 to fit."
	stExpandedHint = "Expanded within this page. Press Escape or Exit expanded view to return. Scroll to zoom; drag to pan."
)

// stViewState is the views' state on the page: the result the default
// view was chosen for, the folded view's zoom (and the layout it is for)
// and its expanded view.
type stViewState struct {
	defaultFor string
	zoom       widgets.StructZoom
	zoomFor    string
	expanded   bool
}

// stFoldedMax is the longest window the folded view lays out.
const stFoldedMax = 30_000

// stLinker unpaired nucleotides join the two strands of a CPLcofold result
// for the folded layout (as the figure scripts' cofold_windows.py).
const stLinker = 3

// stDropLinker removes the linker's points from a layout of two strands
// joined after cut nucleotides.
func stDropLinker(coords [][2]float64, cut int) [][2]float64 {
	if cut <= 0 || len(coords) < cut+stLinker {
		return coords
	}
	return append(append([][2]float64{}, coords[:cut]...), coords[cut+stLinker:]...)
}

// stLayout is a folded layout, or why there is none.
type stLayout struct {
	coords  [][2]float64
	err     string
	loading bool
}

// stTwoStrand: a CPLcofold result, which the web shows as Arc or Matrix
// only.
func stTwoStrand(a *structureResult) bool {
	for _, r := range a.Records {
		if r.Cutpoint > 0 {
			return true
		}
	}
	return false
}

// stViewChoices are the views offered for A alone: Arc and Matrix for two
// strands, as the web.
func stViewChoices(a *structureResult) []string {
	if stTwoStrand(a) {
		return []string{"Arc", "Matrix"}
	}
	return stViewLabels
}

// defaultView shows a new result A as the web does: folded for one
// strand (one window the folded view can lay out), arcs for two strands
// or several windows; a view not offered falls back to arcs.
func (p *structurePage) defaultView(a *structureResult) {
	if p.sv.defaultFor != p.viewA {
		p.sv.defaultFor, p.sv.expanded = p.viewA, false
		p.view = "Arc"
		if records := stPickRecords(a.Records, p.record); !stTwoStrand(a) && len(records) == 1 && len(records[0].Sequence) <= stFoldedMax {
			p.view = "Folded"
		}
	}
	if !slices.Contains(stViewChoices(a), p.view) {
		p.view = "Arc"
	}
}

// stViewData is what the other views draw: the pairs of the windows shown,
// from the first position they cover.
func stViewData(a *structureResult, choice string) (pairs []widgets.LayeredPair, weights []float64, start, length int, seq string) {
	records := stPickRecords(a.Records, choice)
	w := stWeights(records)
	if stLocalView(records) {
		// CPLcofold: drawn on A+B indices, strand A then strand B
		r := records[0]
		for _, pr := range stParseDBN(r.DBN, 1) {
			pairs = append(pairs, widgets.LayeredPair{I: pr.I, J: pr.J, Layer: pr.Layer})
			weights = append(weights, w[stPairKey(r.positionOf(pr.I), r.positionOf(pr.J))])
		}
		return pairs, weights, 1, len(r.Sequence), r.Sequence
	}
	start, end := 0, 0
	for _, r := range records {
		s, e := r.span()
		if start == 0 || s < start {
			start = s
		}
		end = max(end, e)
		for _, pr := range stRecordPairs(r) {
			pairs = append(pairs, widgets.LayeredPair{I: pr.I, J: pr.J, Layer: pr.Layer})
			weights = append(weights, w[[2]int{pr.I, pr.J}])
		}
	}
	if len(records) == 1 {
		seq = records[0].Sequence
	}
	return pairs, weights, start, end - start + 1, seq
}

// stLocalView: the records are a CPLcofold result the views draw on its
// A+B indices rather than the arcs' positions.
func stLocalView(records []structureRecord) bool {
	return len(records) == 1 && records[0].Cutpoint > 0 && records[0].mapped == nil
}

// stArcPosition is the arcs' (and the inspector's) position of view
// position pos; stViewPosition the reverse (0 when not drawn).
func stArcPosition(records []structureRecord, pos int) int {
	if stLocalView(records) {
		return records[0].positionOf(pos)
	}
	return pos
}

func stViewPosition(records []structureRecord, pos int) int {
	if pos > 0 && stLocalView(records) {
		return records[0].localOf(pos)
	}
	return pos
}

// stNestedDBN is the primary (layer 0) pairs as dot-bracket over length
// nucleotides from start: the folded layout draws the other layers over it.
func stNestedDBN(pairs []widgets.LayeredPair, start, length int) string {
	b := []byte(strings.Repeat(".", length))
	for _, p := range pairs {
		i, j := p.I-start, p.J-start
		if p.Layer == 0 && i >= 0 && j < length && i < j {
			b[i], b[j] = '(', ')'
		}
	}
	return string(b)
}

// stComradesResult: a COMRADES(ViennaRNA) result (the web's "hyb-guided").
func stComradesResult(a *structureResult) bool { return stEngine(a) == "COMRADES(ViennaRNA)" }

// stEvidenceColor is the web's evidenceColor: high evidence (at least 0.55
// of the strongest) in the warning colour, low in the primary colour, none
// in the muted line colour.
func stEvidenceColor(support, top float64, t *ui.Theme) ui.Color {
	switch {
	case support <= 0 || top <= 0:
		return t.TextMuted
	case support/top >= 0.55:
		return t.Warning
	}
	return t.Accent
}

// stTopBonus is the strongest evidence of the pairs.
func stTopBonus(pairs []stPairInfo) float64 {
	top := 0.0
	for _, pr := range pairs {
		top = math.Max(top, pr.Bonus)
	}
	return top
}

// stViewStyles are the web's structurePairStyle for A's pairs in the
// other views, aligned with pairs: CPLfold by layer (crossing layers
// dashed and thicker, opacity by HYB bonus when it folded with HYB
// evidence), COMRADES by RNAcofold evidence (fitted hard pairs thickest),
// ViennaRNA by pair type. Colours as the arc view's (stArcStyles).
func stViewStyles(a *structureResult, records []structureRecord, pairs []stPairInfo, t *ui.Theme) []widgets.PairStyle {
	out := make([]widgets.PairStyle, len(pairs))
	top := stTopBonus(pairs)
	cpl, comrades := stIsCPL(a), stComradesResult(a)
	// as the arcs': per record, opacity relative to its matrix maximum
	evidence, scale := stCPLEvidence(a, records), stBonusScale(a, records)
	for i, pr := range pairs {
		gc := stPairType(pr.BaseI, pr.BaseJ) == "G–C"
		s := widgets.PairStyle{Alpha: 0.9, Width: 1.6}
		if gc {
			s.Width = 2.1
		}
		switch {
		case cpl:
			s.Color = t.Accent
			if pr.Layer == 1 {
				s.Color = stViolet
			} else if pr.Layer > 1 {
				s.Color = t.Warning
			}
			if pr.Layer > 0 {
				s.Width, s.Dash = 3, true
			}
			if pr.record < len(evidence) && evidence[pr.record] {
				s.Alpha = stBonusAlpha(pr.Bonus, scale[pr.record])
			}
		case comrades:
			s.Color = stEvidenceColor(pr.Bonus, top, t)
			if pr.Bonus > 0 {
				s.Width = 2.5
			} else {
				s.Alpha = 0.34
			}
			if pr.Hard {
				s.Width = 3.7
			}
		default:
			switch stPairType(pr.BaseI, pr.BaseJ) {
			case "G–C":
				s.Color = t.Accent
			case "A–U":
				s.Color = stViolet
			default:
				s.Color = t.TextMuted
			}
			if pr.Hard {
				s.Width = 3.7
			}
		}
		out[i] = s
	}
	return out
}

// stLegendItems are the web's legend for A's pair styles.
func stLegendItems(a *structureResult, t *ui.Theme) []widgets.LegendItem {
	switch {
	case stIsCPL(a):
		items := []widgets.LegendItem{{Color: t.Accent, Label: "phase 1"}}
		if on, _ := a.Meta.Parameters["allow_pseudoknot"].(bool); on {
			items = append(items, widgets.LegendItem{Color: stViolet, Label: "pseudoknot"}, widgets.LegendItem{Color: t.Warning, Label: "deeper layer"})
		}
		return items
	case stComradesResult(a):
		return []widgets.LegendItem{{Color: t.Warning, Label: "high evidence"}, {Color: t.Accent, Label: "low evidence"}, {Color: t.TextMuted, Label: "MFE only"}}
	}
	return []widgets.LegendItem{{Color: t.Accent, Label: "G–C"}, {Color: stViolet, Label: "A–U"}, {Color: t.TextMuted, Label: "G–U"}}
}

// stViewMarks fill the nucleotide markers of view positions start… (length
// of them) by evidence, as the web's for evidence-driven results: COMRADES
// by the RNAcofold count of the pair a base forms, CPLfold with HYB
// evidence by the base's strongest log1p bonus (its bonus matrix row
// maximum); nil for the others (plain markers).
func stViewMarks(a *structureResult, records []structureRecord, pairs []stPairInfo, start, length int, t *ui.Theme) []ui.Color {
	comrades := stComradesResult(a)
	if !comrades && !(stIsCPL(a) && stHybEvidence(a)) {
		return nil
	}
	support := make([]float64, length)
	if comrades {
		for _, pr := range pairs {
			for _, pos := range []int{pr.I, pr.J} {
				if k := stViewPosition(records, pos) - start; k >= 0 && k < length {
					support[k] = math.Max(support[k], pr.Bonus)
				}
			}
		}
	} else {
		if !slices.ContainsFunc(records, func(r structureRecord) bool { return len(r.NucleotideSupport) > 0 }) {
			return nil // folded before the per-base support was kept
		}
		for k := range support {
			pos := stArcPosition(records, start+k)
			for _, r := range records {
				if j := r.localOf(pos); j >= 1 && j <= len(r.NucleotideSupport) {
					support[k] = r.NucleotideSupport[j-1]
					break
				}
			}
		}
	}
	top := slices.Max(append([]float64{0}, support...))
	out := make([]ui.Color, length)
	for k, s := range support {
		out[k] = stEvidenceColor(s, top, t)
	}
	return out
}

// stPairTitle is the web's pair tooltip (structurePairTitle).
func stPairTitle(a *structureResult, pr stPairInfo) string {
	prefix := "MFE pair: "
	switch {
	case stIsCPL(a):
		prefix = stLayerName(pr.Layer) + " CPLfold pair: "
	case pr.Hard && stComradesResult(a):
		prefix = "HYB-fitted hard pair: "
	case pr.Hard:
		prefix = "Manual hard pair: "
	}
	text := fmt.Sprintf("%s%d %c paired with %d %c (%c–%c)", prefix, pr.I, pr.BaseI, pr.J, pr.BaseJ, pr.BaseI, pr.BaseJ)
	switch {
	case stComradesResult(a):
		text += "; RNAcofold evidence " + jsNumber(pr.Bonus)
	case stIsCPL(a) && stHybEvidence(a):
		text += "; HYB bonus " + jsFixed(pr.Bonus, 4)
	}
	return text
}

// stMarkerTitle is the web's nucleotide tooltip: "i X: paired with j
// (X–Y)" or "i X: unpaired".
func stMarkerTitle(records []structureRecord, pairs []stPairInfo, pos int) string {
	base := byte('?')
	for _, r := range records {
		if k := r.localOf(pos); k >= 1 && k <= len(r.Sequence) {
			base = r.Sequence[k-1]
			break
		}
	}
	for _, pr := range pairs {
		if pr.I == pos || pr.J == pos {
			return fmt.Sprintf("%d %c: paired with %d (%c–%c)", pos, base, pr.I+pr.J-pos, pr.BaseI, pr.BaseJ)
		}
	}
	return fmt.Sprintf("%d %c: unpaired", pos, base)
}

// stStructView is A in the chosen view, styled as the web's, in the
// theme's colours; false (with a note shown) when it cannot be drawn.
func (p *structurePage) stStructView(c *ui.Context, a *structureResult, mode widgets.StructMode, t *ui.Theme, note bool) (*widgets.StructView, bool) {
	records := stPickRecords(a.Records, p.record)
	pairs, weights, start, length, seq := stViewData(a, p.record)
	infos := stShownPairs(records, stHardPairs(a))
	v := &widgets.StructView{Mode: mode, Pairs: pairs, Weights: weights, Shade: p.shade, Start: start, Length: length, Sequence: seq,
		Markers: true, Selected: stViewPosition(records, p.arcs.Selected), Legend: stLegendItems(a, t)}
	if len(infos) == len(pairs) {
		v.Styles = stViewStyles(a, records, infos, t)
	}
	v.Marks = stViewMarks(a, records, infos, start, length, t)
	if mode != widgets.ModeFolded {
		return v, true
	}
	say := func(text string, color ui.Color) {
		if note {
			ui.Text(c, text).TextColor(color)
		}
	}
	switch {
	case len(records) != 1:
		say("The folded view draws one window: choose it above.", t.TextMuted)
		return nil, false
	case length > stFoldedMax:
		say(fmt.Sprintf("The folded view draws at most %s nt.", FormatNumber(stFoldedMax)), t.TextMuted)
		return nil, false
	}
	dbn, cut := stNestedDBN(pairs, start, length), records[0].Cutpoint
	if cut > 0 {
		// two strands: laid out joined by an unpaired linker, drawn without it
		dbn = dbn[:cut] + strings.Repeat(".", stLinker) + dbn[cut:]
	}
	l := p.layout(dbn)
	switch {
	case l.err != "":
		say("Could not lay out the structure: "+ShortError(l.err), t.Danger)
		return nil, false
	case l.coords == nil:
		say("Laying out the structure…", t.TextMuted)
		return nil, false
	}
	v.Coords = stDropLinker(l.coords, cut)
	if p.sv.zoomFor != dbn {
		p.sv.zoomFor = dbn
		p.sv.zoom.Fit()
	}
	return v, true
}

// otherView draws A in the chosen view; false for Arcs (the caller draws
// those).
func (p *structurePage) otherView(c *ui.Context, a *structureResult) bool {
	mode, ok := stViewModes[p.view]
	if !ok {
		return false
	}
	t := c.Theme()
	p.syncArcs(a, nil) // the records shown, for the inspector
	ui.Text(c, stViewTitles[mode]).FontSize(14).Bold()
	v, ok := p.stStructView(c, a, mode, t, true)
	if !ok {
		return true
	}
	records := stPickRecords(a.Records, p.record)
	infos := stShownPairs(records, stHardPairs(a))
	ui.Row(c).Gap(14).Wrap().AlignItems(ui.Center).Children(func() {
		for _, item := range v.Legend {
			stLegend(c, item.Color, item.Label)
		}
	})
	if mode == widgets.ModeFolded {
		v.Zoom = &p.sv.zoom
		p.foldControls(c, false)
	}
	if mode == widgets.ModeFolded && p.sv.expanded {
		// drawn once, in the expanded view
		ui.Text(c, "Shown in the expanded view: press Escape or Exit expanded view to return.").FontSize(12).TextColor(t.TextMuted)
	} else {
		ev := v.Show(c.Key("st-view-"+p.view), 640, stColors(t), p.view+" view of the base pairs")
		p.viewEvent(c, a, v, ev, records, infos)
	}
	if mode == widgets.ModeFolded {
		ui.Text(c, stFoldHint).FontSize(11).TextColor(t.TextMuted)
		p.expandedFold(c, a)
	}
	ui.Text(c, stViewCopy(a, strings.ToLower(p.view))).FontSize(11).TextColor(t.TextMuted)
	return true
}

// stViewCopy is the web's structureViewCopy: what the view ("folded",
// "arc", "radial", "circular" or "matrix") shows of the result.
func stViewCopy(a *structureResult, view string) string {
	switch view {
	case "folded":
		return "The RNA backbone is laid out as a folded molecule: paired bases form stem rungs while unpaired bases open into hairpin and internal loops."
	case "radial":
		return "Pairs are drawn from the sequence perimeter toward the centre so long-range contacts and nested depth can be compared at a glance."
	case "circular":
		return "The sequence runs around a circular backbone; chords make long-range and crossing contacts easy to scan."
	case "matrix":
		return "The symmetric contact matrix places each base pair at its two sequence coordinates; mirrored points preserve the full interaction map."
	}
	on, _ := a.Meta.Parameters["allow_pseudoknot"].(bool)
	switch {
	case stIsCPL(a) && on:
		return "Arc layers distinguish nested phase-1 pairs from crossing pseudoknot pairs; opacity reflects HYB bonus support when enabled."
	case stIsCPL(a):
		return "The arc diagram shows the pseudoknot-free Phase-1 candidate; opacity reflects HYB bonus support when enabled."
	case stComradesResult(a):
		return "Arc colour intensity shows aggregated RNAcofold evidence; fitted constraint arcs are thicker."
	case stEngine(a) == "ViennaRNA" && len(stHardPairs(a)) > 0:
		return "Manual hard-pair arcs are thicker and marked in the base-pair list; all other arcs minimise free energy around them."
	}
	return "Each arc represents a ViennaRNA MFE base pair."
}

// viewEvent takes a view's clicks into the shared selection (the arcs'
// Selected, which the nucleotide inspector shows) and shows what is under
// the pointer as the web's tooltips, or how to inspect a nucleotide.
func (p *structurePage) viewEvent(c *ui.Context, a *structureResult, v *widgets.StructView, ev widgets.StructEvent,
	records []structureRecord, infos []stPairInfo) {
	t := c.Theme()
	if ev.Select > 0 {
		p.arcs.Selected = stArcPosition(records, ev.Select)
	}
	info := "Click a nucleotide marker to inspect it."
	switch {
	case v.Mode == widgets.ModeFolded:
		info = "Click a nucleotide to inspect it."
	case v.Length > widgets.MarkerMax && v.Mode == widgets.ModeMatrix:
		info = "Click along the track under the matrix, or focus it and use the arrow keys, to inspect a nucleotide."
	case v.Length > widgets.MarkerMax:
		info = "Click along the ring, or focus it and use the arrow keys, to inspect a nucleotide."
	}
	switch {
	case ev.Hover.Position > 0:
		info = stMarkerTitle(records, infos, stArcPosition(records, ev.Hover.Position))
	case ev.Hover.Pair >= 0 && ev.Hover.Pair < len(infos):
		info = stPairTitle(a, infos[ev.Hover.Pair])
		if v.Mode == widgets.ModeMatrix {
			info += map[bool]string{false: " · matrix point", true: " · mirrored matrix point"}[ev.Hover.Mirror]
		}
	}
	ui.Text(c, info).FontSize(11).TextColor(t.TextMuted)
}

// foldControls are the web's folded-view buttons: Zoom in, Zoom out, Fit
// to view, and Full screen (the expanded view) or its exit.
func (p *structurePage) foldControls(c *ui.Context, expanded bool) {
	z := &p.sv.zoom
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Role(ui.RoleGroup).Label("Folded structure view controls").Children(func() {
		if ui.Button(c, "Zoom in").Clicked() {
			z.By(widgets.ZoomIn, -1, -1)
		}
		if ui.Button(c, "Zoom out").Clicked() {
			z.By(widgets.ZoomOut, -1, -1)
		}
		if ui.Button(c, "Fit to view").Clicked() {
			z.Fit()
		}
		if expanded {
			if ui.Button(c, "Exit expanded view").Clicked() {
				p.sv.expanded = false
			}
		} else if ui.Button(c, "Full screen").Clicked() {
			p.sv.expanded = true
		}
	})
}

// expandedFold is the folded view over the whole window (the web's
// expanded view when native full screen is unavailable); Escape exits.
func (p *structurePage) expandedFold(c *ui.Context, a *structureResult) {
	if !p.sv.expanded {
		return
	}
	ui.Modal(c.Key("st-fold-expanded"), &p.sv.expanded, func() {
		t := c.Theme()
		w, h := c.Size()
		side := max(320, min(w-80, h-200))
		ui.Text(c, stViewTitles[widgets.ModeFolded]).FontSize(14).Bold()
		p.foldControls(c, true)
		v, ok := p.stStructView(c, a, widgets.ModeFolded, t, true)
		if !ok {
			return
		}
		v.Zoom = &p.sv.zoom
		records := stPickRecords(a.Records, p.record)
		ui.Box(c).Width(side).Children(func() {
			ev := v.Show(c.Key("st-view-expanded"), side, stColors(t), "Expanded folded view of the base pairs")
			p.viewEvent(c, a, v, ev, records, stShownPairs(records, stHardPairs(a)))
		})
		ui.Text(c, stExpandedHint).FontSize(11).TextColor(t.TextMuted)
	})
}

func stColors(t *ui.Theme) widgets.StructColors {
	return widgets.StructColors{Background: t.Surface, Line: t.TextMuted.Alpha(0.6), Text: t.Text,
		Primary: cmpBlue, Pseudoknot: ui.RGB(106, 79, 242), Other: t.Warning, Selected: stViolet}
}

// layout returns the folded layout of dbn, asking for it once.
func (p *structurePage) layout(dbn string) stLayout {
	if l, ok := p.layouts[dbn]; ok {
		return l
	}
	p.layouts[dbn] = stLayout{loading: true}
	a, path := p.a, p.a.Project.Path
	a.Go(func() func() {
		got, err := a.B.ProjectOp(path, "structure_layout", core.Obj{"dbn": dbn})
		l := stLayout{}
		if err != nil {
			l.err = err.Error()
		} else {
			xs, ys := floats(got["x"]), floats(got["y"])
			l.coords = make([][2]float64, min(len(xs), len(ys)))
			for i := range l.coords {
				l.coords[i] = [2]float64{xs[i], ys[i]}
			}
		}
		return func() { p.layouts[dbn] = l }
	})
	return p.layouts[dbn]
}

// exportViewSVG writes the view shown, styled as on screen in light
// colours (the web exports its light theme); with png, as a PNG at twice
// the SVG's size, as the web's. The folded view is written fitted.
func (p *structurePage) exportViewSVG(a *structureResult, asPNG bool) {
	mode := stViewModes[p.view]
	light := ui.LightTheme()
	v, ok := p.stStructView(nil, a, mode, light, false)
	if !ok {
		return
	}
	title := fmt.Sprintf("%s · %s:%s · %s view", p.viewA, nameOr(stGene(a), "RNA"), span(v.Start, v.Start+v.Length-1), strings.ToLower(p.view))
	colors := stColors(light)
	if asPNG {
		saveExport(p.a, "Export the "+strings.ToLower(p.view)+" view", stViewFile(p.view, ".png"),
			func() ([]byte, error) { return v.PNG(640, colors, title, 2) })
		return
	}
	saveExport(p.a, "Export the "+strings.ToLower(p.view)+" view", stViewFile(p.view, ".svg"),
		func() ([]byte, error) {
			return []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + v.SVG(640, colors, title)), nil
		})
}
