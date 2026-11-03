package views

// The drawing editor on the Structure page: "Edit drawing" beside the View
// select opens it on result A's shown window (drawing_editor.go).

import (
	"errors"
	"fmt"

	"github.com/egoist/mygo/ui"
)

// stDrawingSource is the prediction the editor draws: the window shown of
// result A, with COMRADES support for a COMRADES(ViennaRNA) result (the
// RNAcofold observations of the pair each base forms).
func stDrawingSource(a *structureResult, r structureRecord) (*drawingSource, error) {
	n := len(r.Sequence)
	switch {
	case n == 0 || len(r.DBN) != n:
		return nil, errors.New("This result has no dot-bracket structure to draw.")
	case n > stFoldedMax:
		return nil, fmt.Errorf("The drawing editor draws at most %s nt.", FormatNumber(stFoldedMax))
	}
	src := &drawingSource{Sequence: r.Sequence, DBN: r.DBN, Label: stRecordLabel(stGene(a), r)}
	if r.Cutpoint > 0 && r.Cutpoint < n {
		src.Cutpoint = r.Cutpoint
	}
	for _, pr := range stParseDBN(r.DBN, 1) {
		src.Pairs = append(src.Pairs, drawPair{Left: min(pr.I, pr.J), Right: max(pr.I, pr.J)})
	}
	src.Pairs = drSortedPairs(src.Pairs)
	if stEngine(a) == "COMRADES(ViennaRNA)" {
		src.Support = make([]float64, n)
		for _, pr := range stShownPairs([]structureRecord{r}, nil) {
			i, j := r.localOf(pr.I), r.localOf(pr.J)
			if i >= 1 && j >= 1 && pr.Bonus > 0 {
				src.Support[i-1], src.Support[j-1] = pr.Bonus, pr.Bonus
			}
		}
		for k := range a.Records {
			if a.Records[k].WindowStart == r.WindowStart && a.Records[k].WindowEnd == r.WindowEnd && k < len(a.Meta.Records) && a.Meta.Records[k].Comrades != nil {
				src.EvidencePairs = len(a.Meta.Records[k].Comrades.Evidence.Pairs)
			}
		}
	}
	return src, nil
}

// drawingButton is "Edit drawing" / "Close editor"; disabled for a
// two-strand result, which the web does not edit either.
func (p *structurePage) drawingButton(c *ui.Context, a *structureResult) {
	two := stTwoStrand(a)
	if two {
		p.editing = false
	}
	label := map[bool]string{false: "Edit drawing", true: "Close editor"}[p.editing]
	tip := "Edit a copy of this prediction's drawing: layout, colours, labels and pairs"
	if two {
		tip = "The drawing editor draws one strand; this result has two"
	}
	if ui.Button(c.Key("st-edit-drawing"), label).Disabled(two).Tooltip(tip).Clicked() {
		p.editing = !p.editing
	}
}

// drawingView is the editor in the viewer, for the one window of A shown.
func (p *structurePage) drawingView(c *ui.Context, a *structureResult) {
	t := c.Theme()
	ui.Text(c, "Drawing editor").FontSize(14).Bold()
	records := stPickRecords(a.Records, p.record)
	if len(records) != 1 {
		ui.Text(c, "The drawing editor draws one window: choose it above.").TextColor(t.TextMuted)
		return
	}
	src, err := stDrawingSource(a, records[0])
	if err != nil {
		ui.Text(c, err.Error()).TextColor(t.TextMuted)
		return
	}
	// one session per result, window and candidate drawn, kept while the
	// page holds the project: switching away and back restores the edits
	key := stDrawingKey(p.viewA, src)
	if p.drawing == nil || p.drawingKey != key {
		if p.drawings == nil {
			p.drawings = map[string]*drawingEditor{}
		}
		if p.drawings[key] == nil {
			p.drawings[key] = newDrawingEditor(p.a, src)
		}
		p.drawing, p.drawingKey = p.drawings[key], key
	}
	p.drawing.View(c)
}

// stDrawingKey names a drawing session: the result and the prediction
// drawn (its label, sequence and dot-bracket).
func stDrawingKey(result string, src *drawingSource) string {
	return result + "\n" + src.Label + "\n" + src.Sequence + "\n" + src.DBN
}
