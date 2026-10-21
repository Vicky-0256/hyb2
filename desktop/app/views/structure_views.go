package views

// ⑤ Structure: the web app's other views of structure A — circular,
// radial, base-pair matrix and folded (ViennaRNA's naview layout, from
// structure_layout) — next to the arc diagram, with an SVG export of each.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/widgets"
)

var (
	stViewLabels = []string{"Arcs", "Circular", "Radial", "Matrix", "Folded"}
	stViewModes  = map[string]widgets.StructMode{"Circular": widgets.ModeCircular, "Radial": widgets.ModeRadial,
		"Matrix": widgets.ModeMatrix, "Folded": widgets.ModeFolded}
)

// stFoldedMax is the longest window the folded view lays out.
const stFoldedMax = 30_000

// stLayout is a folded layout, or why there is none.
type stLayout struct {
	coords  [][2]float64
	err     string
	loading bool
}

// stViewData is what the other views draw: the pairs of the windows shown,
// from the first position they cover.
func stViewData(a *structureResult, choice string) (pairs []widgets.LayeredPair, weights []float64, start, length int, seq string) {
	records := stPickRecords(a.Records, choice)
	w := stWeights(records)
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

// otherView draws A in the chosen view; false for Arcs (the caller draws
// those).
func (p *structurePage) otherView(c *ui.Context, a *structureResult) bool {
	mode, ok := stViewModes[p.view]
	if !ok {
		return false
	}
	t := c.Theme()
	pairs, weights, start, length, seq := stViewData(a, p.record)
	v := &widgets.StructView{Mode: mode, Pairs: pairs, Weights: weights, Shade: p.shade, Start: start, Length: length, Sequence: seq}
	if mode == widgets.ModeFolded {
		switch {
		case len(stPickRecords(a.Records, p.record)) != 1:
			ui.Text(c, "The folded view draws one window: choose it above.").TextColor(t.TextMuted)
			return true
		case length > stFoldedMax:
			ui.Text(c, fmt.Sprintf("The folded view draws at most %s nt.", FormatNumber(stFoldedMax))).TextColor(t.TextMuted)
			return true
		}
		l := p.layout(stNestedDBN(pairs, start, length))
		switch {
		case l.err != "":
			ui.Text(c, "Could not lay out the structure: "+ShortError(l.err)).TextColor(t.Danger)
			return true
		case l.coords == nil:
			ui.Text(c, "Laying out the structure…").TextColor(t.TextMuted)
			return true
		}
		v.Coords = l.coords
	}
	v.Show(c.Key("st-view-"+p.view), 640, stColors(t), p.view+" view of the base pairs")
	note := map[widgets.StructMode]string{
		widgets.ModeCircular: "Chords join paired positions around the circle, from the top clockwise.",
		widgets.ModeRadial:   "Curves join paired positions; nested pairs bend less towards the centre.",
		widgets.ModeMatrix:   "A dot at (i, j) and (j, i) for each pair i–j.",
		widgets.ModeFolded:   "ViennaRNA's naview layout of the nested pairs; pseudoknotted pairs are drawn across it.",
	}[mode]
	ui.Text(c, note+" Primary pairs in blue, the first pseudoknot layer in violet, others in orange.").FontSize(11).TextColor(t.TextMuted)
	return true
}

func stColors(t *ui.Theme) widgets.StructColors {
	return widgets.StructColors{Background: t.Surface, Line: t.TextMuted.Alpha(0.6), Text: t.Text,
		Primary: cmpBlue, Pseudoknot: ui.RGB(106, 79, 242), Other: t.Warning}
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

// exportViewSVG writes the view shown, in light colours.
func (p *structurePage) exportViewSVG(a *structureResult) {
	mode := stViewModes[p.view]
	pairs, weights, start, length, seq := stViewData(a, p.record)
	v := &widgets.StructView{Mode: mode, Pairs: pairs, Weights: weights, Shade: p.shade, Start: start, Length: length, Sequence: seq}
	if mode == widgets.ModeFolded {
		l := p.layouts[stNestedDBN(pairs, start, length)]
		if l.coords == nil {
			return
		}
		v.Coords = l.coords
	}
	title := fmt.Sprintf("%s · %s:%s · %s view", p.viewA, nameOr(stGene(a), "RNA"), span(start, start+length-1), strings.ToLower(p.view))
	colors := stColors(ui.LightTheme())
	saveExport(p.a, "Export the "+strings.ToLower(p.view)+" view", exportName(".svg", nameOr(stGene(a), "structure"), p.viewA, strings.ToLower(p.view)),
		func() ([]byte, error) {
			return []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + v.SVG(640, colors, title)), nil
		})
}
