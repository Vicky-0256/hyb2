package views

// ⑤ Structure: the drawing editor ("Edit drawing"), the web's
// structure-editor.js on the desktop. It edits a copy of result A's shown
// window (positions, pairs, colours, labels); the prediction and its
// exports stay unchanged. The model and the file writers are drawing.go.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// drawingEditor is one drawing session: the drawing, its undo history, the
// selection, the view and the panels' inputs. It is kept while the
// prediction (sequence and dot-bracket) stays the same, as the web's.
type drawingEditor struct {
	a         *App
	src       *drawingSource
	key       string
	data      *drawing
	undo      []*drawing
	redo      []*drawing
	selection [2]int
	// view is the part of the drawing shown (x, y, w, h); nil fits it.
	view    *[4]float64
	message string
	// coalesce names the field whose edits merge into one undo step while
	// it is edited (typing a title, dragging a colour).
	coalesce string

	// toolbar
	layoutKind, exportFormat string
	// selection and its colours
	from, to                   float64
	fill, text, outline        ui.Color
	radius, pairWidth          float64
	pairColor                  ui.Color
	pairStyle, pairCurve, font string
	letterSize, numbers        float64
	numbering                  string
	numberColor, backbone      ui.Color
	backboneWidth              float64
	background, titleColor     ui.Color
	titleSize                  float64
	title                      string
	spacing                    float64
	flat                       string
	// annotations
	noteText, noteKind, anchorType string
	noteColor                      ui.Color
	noteSize                       float64
	marker, direction              string
	intensity, opacity, regionW    float64
	noteIndex                      int // -1: none chosen
	// colour map
	palette, mapCaption, mapMin, mapMax, mapValues string

	open struct{ selection, colours, style, notes, colourMap bool }

	// the canvas: its size in the last frame, a drag, the last press (for
	// double clicks) and the scene drawn, rebuilt when version changes
	canvasW, canvasH float32
	drag             *drawDrag
	lastPress        struct {
		base int
		at   time.Time
	}
	version      int
	scene        []sceneItem
	sceneVersion int
	sceneSel     [2]int
}

type drawDrag struct {
	kind        string // base, annotation, box, pan
	annotation  int
	start, last drawPoint
	moved       bool
}

var (
	drawLayoutChoices = [][2]string{{"radiate", "Radiate (VARNA)"}, {"simple", "RNA polygon (ViennaRNA)"}, {"circular", "Circular"}, {"linear", "Linear with arcs"}}
	drawExportChoices = [][2]string{{"svg", "SVG image"}, {"eps", "EPS (PostScript)"}, {"json", "Drawing JSON (re-loadable)"}, {"ct", "Edited CT"},
		{"bpseq", "Edited BPSEQ"}, {"dbn", "Edited DBN"}, {"varna", "VARNA command"}}
	drawPairStyleChoices = [][2]string{{"simple", "Simple line"}, {"lw", "Leontis–Westhof"}, {"rnaviz", "RNAViz"}, {"none", "None"}}
	drawCurveChoices     = [][2]string{{"line", "Straight"}, {"arc", "Arc"}}
	drawFontChoices      = [][2]string{{"sans-serif", "Sans serif"}, {"serif", "Serif"}, {"monospace", "Monospace"}}
	drawShownChoices     = [][2]string{{"true", "Shown"}, {"false", "Hidden"}}
	drawFlatChoices      = [][2]string{{"true", "Flat"}, {"false", "Circle"}}
	drawNoteKindChoices  = [][2]string{{"text", "Text"}, {"region", "Region highlight"}, {"probe", "Chemical probe"}}
	drawAnchorChoices    = [][2]string{{"base", "Selected bases"}, {"helix", "Helix of “From”"}, {"loop", "Loop of “From”"}, {"free", "Free position"}}
	drawMarkerChoices    = [][2]string{{"arrow", "Arrow"}, {"dot", "Dot"}, {"triangle", "Triangle"}}
	drawDirectionChoices = [][2]string{{"in", "Toward base"}, {"out", "Away from base"}}
	drawPaletteChoices   = [][2]string{{"lau", "Lau / VARNA"}, {"brand", "Website palette"}, {"heat", "Heat"}, {"bluered", "Blue–white–red"},
		{"grey", "Greyscale"}, {"viridis", "Viridis"}}
	drawExportFiles = map[string]string{"svg": "rna-edited-drawing.svg", "eps": "rna-edited-drawing.eps", "json": "rna-drawing.json",
		"ct": "rna-edited-drawing.ct", "bpseq": "rna-edited-drawing.bpseq", "dbn": "rna-edited-drawing.dbn", "varna": "rna-drawing-varna.sh"}
)

const (
	drawStatusEdited    = "Manually edited pairs: prediction energy does not describe this drawing."
	drawStatusUnchanged = "Prediction and energy are unchanged."
	drawCopy            = "Edit a copy of this prediction: layout, colours, labels and pairs. The prediction and its exports stay unchanged."
	drawHelp            = "Click a base to select it · Shift-click or Shift-drag a box to extend the selection · double-click selects the base’s domain · drag a selected base to move the selection · drag the background to pan · Ctrl/⌘ + scroll to zoom · arrow keys nudge the selection (Shift: 10 units). Layout, styles and annotations are saved separately from the prediction; edited pairs do not recalculate its energy."
)

func drawColor(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// newDrawingEditor starts a session on src, as the web's mount.
func newDrawingEditor(a *App, src *drawingSource) *drawingEditor {
	d := initialDrawing(src)
	e := &drawingEditor{a: a, src: src, key: src.Sequence + "\n" + src.DBN, data: d, selection: [2]int{1, 1},
		layoutKind: "radiate", exportFormat: "svg", noteIndex: -1}
	e.open.selection, e.open.colours = true, true
	// the panels start from the drawing's style
	e.pairStyle, e.pairCurve, e.font = drOrS(d.PairStyle, "simple"), drOrS(d.PairCurve, "line"), drOrS(d.Font, "sans-serif")
	e.letterSize, e.numbers = drOrNZ(d.BaseFontSize, 12), float64(d.NumberInterval)
	e.numbering = strconv.FormatBool(d.Numbering == nil || *d.Numbering)
	e.numberColor, e.backbone = ui.Hex(drOrS(d.NumberColor, "#444444")), ui.Hex(d.Backbone)
	e.backboneWidth = drOrF(d.BackboneWidth, 1.5)
	e.background, e.titleColor = ui.Hex(d.Background), ui.Hex(drOrS(d.TitleColor, "#222222"))
	e.titleSize, e.title = drOrNZ(d.TitleSize, 18), d.Title
	e.spacing, e.flat = 1, "true"
	e.fill, e.text, e.outline, e.radius = ui.Hex("#91D1C2"), ui.Hex("#222222"), ui.Hex("#3C5488"), 9
	e.pairColor, e.pairWidth = ui.Hex("#3C5488"), 2
	e.noteKind, e.anchorType, e.noteColor, e.noteSize = "text", "base", ui.Hex("#3C5488"), 14
	e.marker, e.direction, e.intensity, e.opacity, e.regionW = "arrow", "in", 1, 0.25, 32
	e.palette = "lau"
	if d.Evidence != nil {
		e.palette = d.Evidence.Palette
	}
	e.sync()
	// Say why a structure with HYB evidence stays uncoloured.
	e.message = e.status()
	if src.Support != nil {
		some := false
		for _, v := range src.Support {
			some = some || v > 0
		}
		if !some {
			against := "the RNAcofold evidence"
			if src.EvidencePairs > 0 {
				against = fmt.Sprintf("any of the %d RNAcofold evidence pairs", src.EvidencePairs)
			}
			e.message = fmt.Sprintf("COMRADES support: none of the %d pairs in this structure matches %s, so every base scores 0 and keeps the default colour.", len(src.Pairs), against)
		}
	}
	return e
}

func (e *drawingEditor) n() int { return len(e.data.Sequence) }

func (e *drawingEditor) status() string {
	if e.data.TopologyEdited {
		return drawStatusEdited
	}
	return drawStatusUnchanged
}

func (e *drawingEditor) changed() { e.version++ }

func (e *drawingEditor) checkpoint() {
	e.undo = append(e.undo, e.data.clone())
	if len(e.undo) > 50 {
		e.undo = e.undo[1:]
	}
	e.redo = nil
}

func (e *drawingEditor) undoStep() {
	if len(e.undo) == 0 {
		return
	}
	e.redo = append(e.redo, e.data.clone())
	e.data = e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.view, e.coalesce = nil, ""
	e.changed()
}

func (e *drawingEditor) redoStep() {
	if len(e.redo) == 0 {
		return
	}
	e.undo = append(e.undo, e.data.clone())
	e.data = e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.view, e.coalesce = nil, ""
	e.changed()
}

// sync sets the selection fields and colour inputs from the first
// selected base.
func (e *drawingEditor) sync() {
	e.from, e.to = float64(e.selection[0]), float64(e.selection[1])
	s := e.data.style(e.selection[0])
	e.fill, e.text, e.outline, e.radius = ui.Hex(s.Fill), ui.Hex(s.Text), ui.Hex(drOrS(s.Outline, "#3C5488")), s.Radius
	for _, p := range e.data.Pairs {
		if p.Left == e.selection[0] || p.Right == e.selection[0] {
			e.pairColor, e.pairWidth = ui.Hex(p.Color), p.Width
			break
		}
	}
}

// selected reads From and To.
func (e *drawingEditor) selected() ([2]int, error) {
	a, b := e.from, e.to
	if a != math.Trunc(a) || b != math.Trunc(b) || a < 1 || b < a || b > float64(e.n()) {
		return [2]int{}, errors.New("Select a valid nucleotide interval.")
	}
	e.selection = [2]int{int(a), int(b)}
	return e.selection, nil
}

func drRange(v, lo, hi float64, label string) (float64, error) {
	if !drFinite(v) || v < lo || v > hi {
		return 0, fmt.Errorf("%s must be %s–%s.", label, drJsNum(lo), drJsNum(hi))
	}
	return v, nil
}

// commit makes every edit one validated, undoable step; edits of the
// field named coalesce while it stays the one edited merge into one step.
func (e *drawingEditor) commit(change func(next *drawing) error, note, coalesce string) {
	next := e.data.clone()
	err := change(next)
	if err == nil {
		next.TopologyEdited = drPairSignature(next.Pairs) != drPairSignature(e.src.Pairs)
		err = validateDrawing(next, e.src.Sequence, e.src.DBN)
	}
	if err != nil {
		e.message = err.Error()
		return
	}
	if coalesce == "" || coalesce != e.coalesce || len(e.undo) == 0 {
		e.checkpoint()
	} else {
		e.redo = nil
	}
	e.coalesce = coalesce
	e.data = next
	e.changed()
	e.sync()
	if note != "" {
		note += " "
	}
	e.message = note + e.status()
}

func (e *drawingEditor) selectRange(r [2]int, note string) {
	e.selection = r
	e.coalesce = ""
	e.sync()
	e.message = note
}

func (e *drawingEditor) moveSelection(dx, dy float64) {
	for i := e.selection[0] - 1; i < e.selection[1]; i++ {
		e.data.Points[i].X += dx
		e.data.Points[i].Y += dy
	}
	e.changed()
}

// pivot: a selected domain turns about the middle of its closing pair, as
// a VARNA helix does.
func (e *drawingEditor) pivot(d *drawing, a, b int) drawPoint {
	pt := drPairTable(len(d.Sequence), d.Pairs)
	if pt[a] == b {
		return drawPoint{(d.Points[a-1].X + d.Points[b-1].X) / 2, (d.Points[a-1].Y + d.Points[b-1].Y) / 2}
	}
	return drCentroid(d.Points[a-1 : b])
}

func (e *drawingEditor) relayout(note string) {
	e.commit(func(next *drawing) error {
		spacing, err := drRange(e.spacing, .5, 3, "Base spacing")
		if err != nil {
			return err
		}
		flat := e.flat == "true"
		next.Points = drawLayoutPoints(len(next.Sequence), next.Pairs, e.layoutKind, spacing, flat)
		next.Layout = &drawLayout{Kind: e.layoutKind, Spacing: spacing, Flat: flat}
		next.PairCurve = "line"
		if e.layoutKind == "linear" {
			next.PairCurve = "arc"
		}
		e.pairCurve = next.PairCurve
		e.view = nil
		return nil
	}, note, "")
}

func (e *drawingEditor) bounds() [4]float64 {
	if e.view != nil {
		return *e.view
	}
	return drawBounds(e.data)
}

func (e *drawingEditor) zoom(factor float64) {
	box := e.bounds()
	c := drawPoint{box[0] + box[2]/2, box[1] + box[3]/2}
	if box[2]*factor < 50 || box[2]*factor > 1e7 {
		return
	}
	e.view = &[4]float64{c.X - box[2]*factor/2, c.Y - box[3]*factor/2, box[2] * factor, box[3] * factor}
}

// styleField applies a "Selection colours" field to the selected bases
// and their pairs.
func (e *drawingEditor) styleField(name string) {
	r, err := e.selected()
	if err != nil {
		e.message = err.Error()
		return
	}
	a, b := r[0], r[1]
	e.commit(func(next *drawing) error {
		switch name {
		case "pairColor", "pairWidth":
			width, err := drRange(e.pairWidth, .5, 10, "Pair width")
			if err != nil {
				return err
			}
			touched := false
			for i, p := range next.Pairs {
				if (p.Left >= a && p.Left <= b) || (p.Right >= a && p.Right <= b) {
					touched = true
					if name == "pairColor" {
						next.Pairs[i].Color = drawColor(e.pairColor)
					} else {
						next.Pairs[i].Width = width
					}
				}
			}
			if !touched {
				return errors.New("No pair touches the selection.")
			}
			return nil
		}
		radius, err := drRange(e.radius, 3, 24, "Base radius")
		if err != nil {
			return err
		}
		if name == "fill" && next.Evidence != nil {
			next.Evidence.Overrides = true
		}
		for i := a; i <= b; i++ {
			s := next.style(i)
			switch name {
			case "fill":
				s.Fill = drawColor(e.fill)
			case "text":
				s.Text = drawColor(e.text)
			case "outline":
				s.Outline = drawColor(e.outline)
			case "radius":
				s.Radius = radius
			}
			next.Styles[i] = s
		}
		return nil
	}, fmt.Sprintf("Applied to %d–%d.", a, b), fmt.Sprintf("style:%s:%d-%d", name, a, b))
}

// globalStyle applies the "Drawing style" panel.
func (e *drawingEditor) globalStyle(field string) {
	e.commit(func(next *drawing) error {
		letter, err := drRange(e.letterSize, 6, 30, "Letter size")
		if err != nil {
			return err
		}
		numbers, err := drRange(e.numbers, 1, 1000, "Number interval")
		if err != nil {
			return err
		}
		if numbers != math.Trunc(numbers) {
			return errors.New("Invalid drawing style.")
		}
		backbone, err := drRange(e.backboneWidth, 0, 10, "Backbone width")
		if err != nil {
			return err
		}
		titleSize, err := drRange(e.titleSize, 8, 48, "Title size")
		if err != nil {
			return err
		}
		next.PairStyle, next.PairCurve, next.Font, next.BaseFontSize = e.pairStyle, e.pairCurve, e.font, drFptr(letter)
		next.NumberInterval, next.Numbering, next.NumberColor = int(numbers), drBptr(e.numbering == "true"), drawColor(e.numberColor)
		next.Backbone, next.BackboneWidth, next.Background = drawColor(e.backbone), drFptr(backbone), drawColor(e.background)
		next.Title, next.TitleColor, next.TitleSize = e.title, drawColor(e.titleColor), drFptr(titleSize)
		return nil
	}, "Style updated.", "global:"+field)
}

// act runs a toolbar or panel button, as the web's click handler.
func (e *drawingEditor) act(action string) {
	if err := e.do(action); err != nil {
		e.message = err.Error()
	}
}

func (e *drawingEditor) do(action string) error {
	n := e.n()
	switch action {
	case "undo", "redo":
		if action == "undo" {
			e.undoStep()
			e.message = "Undone."
		} else {
			e.redoStep()
			e.message = "Redone."
		}
		e.sync()
		return nil
	case "zoomIn":
		e.zoom(1 / 1.25)
		return nil
	case "zoomOut":
		e.zoom(1.25)
		return nil
	case "fit":
		e.view = nil
		return nil
	case "layout":
		e.relayout("Positions recomputed.")
		return nil
	case "all":
		e.selectRange([2]int{1, n}, "")
		return nil
	case "domain", "parent":
		r, err := e.selected()
		if err != nil {
			return err
		}
		var found [2]int
		var ok bool
		if action == "domain" {
			found, ok = drDomainOf(e.data, r[0])
		} else {
			found, ok = drParentOf(e.data, r[0], r[1])
		}
		if !ok {
			if action == "domain" {
				return fmt.Errorf("Base %d lies in the exterior loop, outside any domain.", r[0])
			}
			return errors.New("No pair encloses the selection.")
		}
		e.selectRange(found, "")
		return nil
	}
	r, err := e.selected()
	if err != nil {
		return err
	}
	a, b := r[0], r[1]
	rotate := func(degrees float64) {
		e.commit(func(next *drawing) error {
			drTurnPoints(next.Points, a, b, degrees, e.pivot(next, a, b))
			return nil
		}, "", "")
	}
	switch action {
	case "rotL":
		rotate(-15)
	case "rotR":
		rotate(15)
	case "rot90":
		rotate(90)
	case "mirror":
		e.commit(func(next *drawing) error {
			drMirrorPoints(next.Points, a, b, e.pivot(next, a, b))
			return nil
		}, "", "")
	case "resetStyle":
		e.commit(func(next *drawing) error {
			for i := a; i <= b; i++ {
				delete(next.Styles, i)
			}
			if next.Evidence != nil {
				next.Evidence.Overrides = true
			}
			return nil
		}, "", "")
	case "evidence":
		e.commit(func(next *drawing) error { return drApplyEvidence(next, drEvidenceFor(e.src.Support), e.palette) }, "Evidence colours restored.", "")
	case "colormap":
		e.commit(func(next *drawing) error {
			return drColorMap(next, e.mapValues, drColorMapOptions{Minimum: e.mapMin, Maximum: e.mapMax, Palette: e.palette, Caption: e.mapCaption})
		}, "Colour map applied.", "")
	case "clearColors":
		e.commit(func(next *drawing) error { drClearColors(next); return nil }, "Colours cleared.", "")
	case "ends", "helices", "hairpins":
		e.commit(func(next *drawing) error {
			notes, err := drAutoAnnotations(next, action)
			if err != nil {
				return err
			}
			kept := []drawAnnotation{}
			for _, note := range next.Annotations {
				if note.Auto != action {
					kept = append(kept, note)
				}
			}
			next.Annotations = append(kept, notes...)
			e.view = nil
			return nil
		}, "", "")
	case "annotation", "updateAnnotation":
		note := "Annotation added."
		if action == "updateAnnotation" {
			note = "Annotation updated."
		}
		e.commit(func(next *drawing) error {
			text := strings.TrimSpace(e.noteText)
			if e.noteKind == "text" && text == "" {
				return errors.New("Enter annotation text first.")
			}
			if e.anchorType == "free" && e.noteKind != "text" {
				return errors.New("Only text annotations can take a free position.")
			}
			size, err := drRange(e.noteSize, 6, 48, "Text size")
			if err != nil {
				return err
			}
			an := drawAnnotation{Kind: e.noteKind, Base: a, Text: text, DX: 25, DY: -35, Color: drawColor(e.noteColor), Size: drFptr(size)}
			switch {
			case e.noteKind == "region":
				an.End = b
				for i := an.Base; i <= b; i++ {
					an.Bases = append(an.Bases, i)
				}
				opacity, err := drRange(e.opacity, .05, 1, "Highlight opacity")
				if err != nil {
					return err
				}
				width, err := drRange(e.regionW, 2, 80, "Highlight width")
				if err != nil {
					return err
				}
				an.Opacity, an.Width = drFptr(opacity), drFptr(width)
			case e.anchorType == "helix" || e.anchorType == "loop":
				var bases []int
				if e.anchorType == "helix" {
					bases, err = drHelixOf(next, an.Base)
				} else {
					bases, err = drLoopOf(next, an.Base)
				}
				if err != nil {
					return err
				}
				an.Bases, an.AnchorType = bases, e.anchorType
				for _, i := range bases {
					an.Base = min(an.Base, i)
				}
			case e.anchorType == "free":
				p := next.anchor(drawAnnotation{Base: an.Base})
				an.Free, an.X, an.Y, an.DX, an.DY = drBptr(true), drFptr(p.X+25), drFptr(p.Y-35), 0, 0
			case b > an.Base:
				for i := an.Base; i <= b; i++ {
					an.Bases = append(an.Bases, i)
				}
			}
			if e.noteKind == "probe" {
				intensity, err := drRange(e.intensity, .25, 4, "Probe intensity")
				if err != nil {
					return err
				}
				an.Marker, an.Direction, an.Intensity = e.marker, e.direction, drFptr(intensity)
			}
			if action == "annotation" {
				next.Annotations = append(next.Annotations, an)
				return nil
			}
			if e.noteIndex < 0 || e.noteIndex >= len(next.Annotations) {
				return errors.New("Select a saved annotation first.")
			}
			if an.Free == nil {
				an.DX, an.DY = next.Annotations[e.noteIndex].DX, next.Annotations[e.noteIndex].DY
			}
			next.Annotations[e.noteIndex] = an
			return nil
		}, note, "")
	case "removeAnnotation":
		e.commit(func(next *drawing) error {
			if e.noteIndex < 0 || e.noteIndex >= len(next.Annotations) {
				return errors.New("Select a saved annotation first.")
			}
			next.Annotations = append(next.Annotations[:e.noteIndex], next.Annotations[e.noteIndex+1:]...)
			return nil
		}, "Annotation removed.", "")
		if e.message != "Select a saved annotation first." {
			e.noteIndex = -1
		}
	case "clearAnnotations":
		e.commit(func(next *drawing) error { next.Annotations = []drawAnnotation{}; return nil }, "Annotations cleared.", "")
		e.noteIndex = -1
	case "pair":
		e.commit(func(next *drawing) error {
			if a == b {
				return errors.New("Select two or more bases: the first and last are paired.")
			}
			for _, p := range next.Pairs {
				if p.Left == a || p.Left == b || p.Right == a || p.Right == b {
					return fmt.Errorf("Base %d or %d is already paired. Remove its pair first.", a, b)
				}
			}
			width, err := drRange(e.pairWidth, .5, 10, "Pair width")
			if err != nil {
				return err
			}
			next.Pairs = append(next.Pairs, drawPair{Left: a, Right: b, Color: drawColor(e.pairColor), Width: width})
			return nil
		}, fmt.Sprintf("Pair %d–%d added.", a, b), "")
	case "unpair":
		e.commit(func(next *drawing) error {
			kept := []drawPair{}
			for _, p := range next.Pairs {
				if !((p.Left >= a && p.Left <= b) || (p.Right >= a && p.Right <= b)) {
					kept = append(kept, p)
				}
			}
			if len(kept) == len(next.Pairs) {
				return errors.New("No pair touches the selection.")
			}
			next.Pairs = kept
			return nil
		}, "Pairs removed.", "")
	}
	return nil
}

// selectAnnotation shows a saved annotation in the panel and selects its
// bases.
func (e *drawingEditor) selectAnnotation(i int) {
	if i < 0 || i >= len(e.data.Annotations) {
		return
	}
	a := e.data.Annotations[i]
	e.noteText, e.noteKind, e.marker, e.noteColor = a.Text, drOrS(a.Kind, "text"), drOrS(a.Marker, "arrow"), ui.Hex(a.Color)
	e.anchorType = drOrS(a.AnchorType, "base")
	if a.Free != nil && *a.Free {
		e.anchorType = "free"
	}
	e.noteSize, e.direction, e.intensity = drOrNZ(a.Size, 14), drOrS(a.Direction, "in"), drOrNZ(a.Intensity, 1)
	e.opacity, e.regionW = drOrF(a.Opacity, .25), drOrNZ(a.Width, 32)
	end := a.Base
	if a.End != 0 {
		end = a.End
	}
	e.selection = [2]int{a.Base, end}
	e.sync()
	e.noteIndex = i
}

// ---- exports and loading

func (e *drawingEditor) exportBytes(format string) ([]byte, error) {
	d := e.data
	switch format {
	case "svg":
		return []byte(drawingSVG(d, nil, nil)), nil
	case "eps":
		return []byte(drawingEPS(d)), nil
	case "json":
		return drawingJSON(d)
	case "ct":
		return []byte(drawingCT(d)), nil
	case "bpseq":
		return []byte(drawingBPSEQ(d)), nil
	case "dbn":
		s, err := drawingDBN(d)
		return []byte(s), err
	case "varna":
		s, err := drawingVARNA(d)
		return []byte(s), err
	}
	return nil, errors.New("Unknown export format.")
}

// export saves the drawing in format under the web's file name.
func (e *drawingEditor) export(format string) {
	name := drawExportFiles[format]
	data, err := e.exportBytes(format)
	if err != nil {
		e.message = err.Error()
		return
	}
	a := e.a
	a.Go(func() func() {
		path := PickSave("Save the edited drawing", name)
		if path == "" {
			return nil
		}
		err := os.WriteFile(path, data, 0o644)
		return func() {
			if err != nil {
				a.Toast("Could not export: %v", err)
				e.message = err.Error()
				return
			}
			a.Toast("Saved %s", path)
			if format == "varna" {
				e.message = "VARNA command saved. Run it next to VARNA.jar to redraw this structure in VARNA."
			} else {
				e.message = filepath.Base(path) + " saved."
			}
		}
	})
}

// load reads a saved drawing JSON for this prediction.
func (e *drawingEditor) load() {
	a := e.a
	a.Go(func() func() {
		paths := PickFiles("Load a saved drawing JSON", false)
		if len(paths) == 0 {
			return nil
		}
		var data []byte
		info, err := os.Stat(paths[0])
		if err == nil && info.Size() > 5_000_000 {
			err = errors.New("Drawing JSON exceeds 5 MB.")
		}
		if err == nil {
			data, err = os.ReadFile(paths[0])
		}
		return func() { e.loadBytes(data, err) }
	})
}

func (e *drawingEditor) loadBytes(raw []byte, err error) {
	var d *drawing
	if err == nil {
		d, err = parseDrawingJSON(raw, e.src.Sequence, e.src.DBN)
	}
	if err != nil {
		e.message = err.Error()
		return
	}
	d.TopologyEdited = drPairSignature(d.Pairs) != drPairSignature(e.src.Pairs)
	e.checkpoint()
	e.data, e.view, e.coalesce = d, nil, ""
	e.changed()
	e.sync()
	e.message = "Drawing loaded."
}

// ---- the canvas

// fitBox maps the box onto a w×h canvas as SVG's xMidYMid meet: scale
// and the canvas point of the box's origin.
func fitBox(box [4]float64, w, h float32) (k, ox, oy float64) {
	k = math.Min(float64(w)/box[2], float64(h)/box[3])
	ox = (float64(w) - box[2]*k) / 2
	oy = (float64(h) - box[3]*k) / 2
	return k, ox - box[0]*k, oy - box[1]*k
}

// toDrawing is the drawing point under canvas point (x, y).
func (e *drawingEditor) toDrawing(box [4]float64, x, y float32) drawPoint {
	k, ox, oy := fitBox(box, e.canvasW, e.canvasH)
	return drawPoint{(float64(x) - ox) / k, (float64(y) - oy) / k}
}

func (e *drawingEditor) sceneItems() []sceneItem {
	if e.scene == nil || e.sceneVersion != e.version || e.sceneSel != e.selection {
		e.scene = drawScene(e.data, &e.selection, drawBounds(e.data))
		e.sceneVersion, e.sceneSel = e.version, e.selection
	}
	return e.scene
}

// hit is the annotation (index+1) or base (1-based) under p, the one drawn
// last first.
func (e *drawingEditor) hit(p drawPoint) (base, annotation int) {
	d := e.data
	for i := len(d.Annotations) - 1; i >= 0; i-- {
		a := d.Annotations[i]
		at := d.anchor(a)
		q := drawPoint{at.X + a.DX, at.Y + a.DY}
		size := drOrNZ(a.Size, 14)
		ty := q.Y - 8
		if a.Free != nil && *a.Free {
			ty = q.Y
		}
		w := float64(drJsLen(a.Text)) * size * 0.6
		if (p.X >= q.X-4 && p.X <= q.X+w+4 && p.Y >= ty-size-2 && p.Y <= ty+4) || math.Hypot(p.X-q.X, p.Y-q.Y) <= 8 {
			return 0, i + 1
		}
	}
	for i := len(d.Points) - 1; i >= 0; i-- {
		r := d.style(i + 1).Radius
		if math.Hypot(p.X-d.Points[i].X, p.Y-d.Points[i].Y) <= r {
			return i + 1, 0
		}
	}
	return 0, 0
}

func (e *drawingEditor) input(ev ui.InputEvent) bool {
	if e.canvasW <= 0 || e.canvasH <= 0 {
		return false
	}
	shift := ev.Mods&ui.Shift != 0
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		box := e.bounds()
		e.view = &box
		p := e.toDrawing(box, ev.X, ev.Y)
		base, annotation := e.hit(p)
		if annotation > 0 {
			e.selectAnnotation(annotation - 1)
		}
		if base > 0 {
			if shift {
				e.selection = [2]int{min(e.selection[0], base), max(e.selection[1], base)}
			} else if base < e.selection[0] || base > e.selection[1] {
				e.selection = [2]int{base, base}
			}
			e.sync()
		}
		// A double click (two presses on one base within 400 ms) selects
		// its domain.
		if base > 0 && !shift {
			now := time.Now()
			if ev.Clicks >= 2 || (e.lastPress.base == base && now.Sub(e.lastPress.at) < 400*time.Millisecond) {
				e.lastPress.base = 0
				if found, ok := drDomainOf(e.data, base); ok {
					e.selectRange(found, fmt.Sprintf("Selected the domain %d–%d.", found[0], found[1]))
					e.drag = nil
					return true
				}
			} else {
				e.lastPress.base, e.lastPress.at = base, now
			}
		}
		kind := "pan"
		switch {
		case annotation > 0:
			kind = "annotation"
		case base > 0:
			kind = "base"
		case shift:
			kind = "box"
		}
		e.drag = &drawDrag{kind: kind, annotation: annotation - 1, start: p, last: p}
		return true
	case ui.InputPointerMove:
		g := e.drag
		if g == nil || e.view == nil {
			return false
		}
		p := e.toDrawing(*e.view, ev.X, ev.Y)
		dx, dy := p.X-g.last.X, p.Y-g.last.Y
		if !g.moved && math.Hypot(p.X-g.start.X, p.Y-g.start.Y) < 1 {
			return true
		}
		// One undo step per drag, taken when the drag really moves
		// something.
		if !g.moved && (g.kind == "base" || g.kind == "annotation") {
			e.checkpoint()
			e.coalesce = ""
		}
		g.moved = true
		switch g.kind {
		case "annotation":
			e.data.Annotations[g.annotation].DX += dx
			e.data.Annotations[g.annotation].DY += dy
			g.last = p
			e.changed()
		case "base":
			e.moveSelection(dx, dy)
			g.last = p
		case "pan":
			e.view[0] -= dx
			e.view[1] -= dy
		default:
			g.last = p
		}
		return true
	case ui.InputPointerUp:
		g := e.drag
		e.drag = nil
		if g == nil {
			return false
		}
		switch {
		case g.kind == "box" && g.moved:
			x0, x1 := math.Min(g.start.X, g.last.X), math.Max(g.start.X, g.last.X)
			y0, y1 := math.Min(g.start.Y, g.last.Y), math.Max(g.start.Y, g.last.Y)
			lo, hi := 0, 0
			for i, p := range e.data.Points {
				if p.X >= x0 && p.X <= x1 && p.Y >= y0 && p.Y <= y1 {
					if lo == 0 {
						lo = i + 1
					}
					hi = i + 1
				}
			}
			if lo > 0 {
				e.selectRange([2]int{lo, hi}, fmt.Sprintf("Selected %d–%d (first to last base inside the box).", lo, hi))
			}
		case g.moved && g.kind != "pan":
			e.message = "Moved. " + e.status()
		}
		return true
	case ui.InputScroll:
		// Plain scrolling keeps scrolling the page; Ctrl/⌘ + scroll zooms.
		if ev.Mods&(ui.Ctrl|ui.Super) == 0 || ev.DY == 0 {
			return false
		}
		box := e.bounds()
		p := e.toDrawing(box, ev.X, ev.Y)
		factor := 1 / 1.1
		if ev.DY > 0 {
			factor = 1.1
		}
		if box[2]*factor < 50 || box[2]*factor > 1e7 {
			return true
		}
		e.view = &[4]float64{p.X + (box[0]-p.X)*factor, p.Y + (box[1]-p.Y)*factor, box[2] * factor, box[3] * factor}
		return true
	case ui.InputKeyDown:
		if ev.Mods&(ui.Ctrl|ui.Super) != 0 && (ev.Key == ui.KeyZ || ev.Key == ui.KeyY) {
			if ev.Key == ui.KeyY || shift {
				e.redoStep()
			} else {
				e.undoStep()
			}
			e.sync()
			return true
		}
		step := 1.0
		if shift {
			step = 10
		}
		var dx, dy float64
		switch ev.Key {
		case ui.KeyLeft:
			dx = -step
		case ui.KeyRight:
			dx = step
		case ui.KeyUp:
			dy = -step
		case ui.KeyDown:
			dy = step
		default:
			return false
		}
		if ev.Mods&^ui.Shift != 0 {
			return false
		}
		e.checkpoint()
		e.coalesce = ""
		e.moveSelection(dx, dy)
		e.sync()
		e.message = "Moved. " + e.status()
		return true
	}
	return false
}

// paint draws the scene into r.
func (e *drawingEditor) paint(p *ui.Painter, r ui.Rect) {
	e.canvasW, e.canvasH = r.W, r.H
	d := e.data
	p.Fill(r, ui.Hex(d.Background), 6)
	box := e.bounds()
	k, ox, oy := fitBox(box, r.W, r.H)
	X := func(x float64) float32 { return r.X + float32(ox+x*k) }
	Y := func(y float64) float32 { return r.Y + float32(oy+y*k) }
	font := map[string]string{"serif": "serif", "monospace": "monospace"}[d.Font]
	color := func(c string, o *float64) ui.Color {
		col := ui.Hex(c)
		if o != nil {
			col = col.Alpha(float32(*o))
		}
		return col
	}
	clip := r
	var draw func(it sceneItem)
	draw = func(it sceneItem) {
		switch it.t {
		case "g":
			for _, c := range it.items {
				draw(c)
			}
		case "rect":
			p.Fill(ui.Rect{X: X(it.x), Y: Y(it.y), W: float32(it.w * k), H: float32(it.h * k)}, ui.Hex(it.fill), 0)
		case "line":
			p.Line(X(it.x1), Y(it.y1), X(it.x2), Y(it.y2), float32(math.Max(.5, it.width*k)), color(it.stroke, it.opacity))
		case "quad":
			path := (&ui.Path{}).MoveTo(X(it.x1), Y(it.y1)).QuadTo(X(it.cx), Y(it.cy), X(it.x2), Y(it.y2))
			p.StrokePath(path, float32(math.Max(.5, it.width*k)), ui.Hex(it.stroke))
		case "path":
			path := &ui.Path{}
			for i, q := range it.points {
				if i == 0 {
					path.MoveTo(X(q.X), Y(q.Y))
				} else {
					path.LineTo(X(q.X), Y(q.Y))
				}
			}
			if it.closed {
				path.Close()
			}
			if it.fill != "" {
				p.FillPath(path, ui.Hex(it.fill))
			}
			if it.stroke != "" && len(it.points) > 1 {
				p.StrokePath(path, float32(it.width*k), color(it.stroke, it.opacity))
			}
		case "circle":
			rad := float32(it.r * k)
			cx, cy := X(it.cx), Y(it.cy)
			if cx+rad < clip.X || cx-rad > clip.X+clip.W || cy+rad < clip.Y || cy-rad > clip.Y+clip.H {
				return
			}
			rect := ui.Rect{X: cx - rad, Y: cy - rad, W: 2 * rad, H: 2 * rad}
			p.Fill(rect, color(it.fill, it.fillOpacity), rad)
			if it.stroke != "" {
				p.Stroke(rect, ui.Hex(it.stroke), rad, float32(math.Max(.5, it.strokeWidth*k)))
			}
		case "text":
			size := float32(it.size * k)
			x, y := X(it.x), Y(it.y)
			if size < 3 || it.text == "" || y < clip.Y-size || y > clip.Y+clip.H+size {
				return
			}
			span := ui.Span{Text: it.text, Font: font, Size: size, Color: ui.Hex(it.fill)}
			w, h := p.MeasureText(0, span)
			if it.anchor == "middle" {
				x -= w / 2
			}
			// SVG places the baseline at y; the painter the top of the line
			p.RichText(x, y-size*0.36-h/2, 0, span)
		}
	}
	p.Clip(r, 6, func() {
		for _, it := range e.sceneItems() {
			draw(it)
		}
		if g := e.drag; g != nil && g.kind == "box" && g.moved {
			x0, y0 := X(math.Min(g.start.X, g.last.X)), Y(math.Min(g.start.Y, g.last.Y))
			sel := ui.Rect{X: x0, Y: y0, W: float32(math.Abs(g.last.X-g.start.X) * k), H: float32(math.Abs(g.last.Y-g.start.Y) * k)}
			p.Fill(sel, ui.Hex("#4DBBD5").Alpha(0.12), 0)
			p.StrokeDashed(sel, ui.Hex("#4DBBD5"), 0, 1)
		}
	})
}

// ---- the editor's view

// drawChoice is a select of values shown by their labels; it reports a
// change.
func drawChoice(c *ui.Context, key, label string, value *string, choices [][2]string, width float32) bool {
	labels := make([]string, len(choices))
	current := ""
	for i, ch := range choices {
		labels[i] = ch[1]
		if ch[0] == *value {
			current = ch[1]
		}
	}
	if current == "" && len(labels) > 0 {
		current = labels[0]
	}
	// Changed applies the choice to current now, while it is in scope
	ui.Select(c.Key(key), &current, labels).Width(width).Label(label).Changed()
	for _, ch := range choices {
		if ch[1] == current && ch[0] != *value {
			*value = ch[0]
			return true
		}
	}
	return false
}

// drawField is a small label above a control.
func drawField(c *ui.Context, label string, control func()) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
		control()
	})
}

func (e *drawingEditor) buttons(c *ui.Context, actions [][3]string) {
	ui.Row(c).Gap(6).Wrap().Children(func() {
		for _, b := range actions {
			btn := ui.Button(c.Key("dr-"+b[0]), b[1])
			if b[2] != "" {
				btn = btn.Tooltip(b[2])
			}
			if btn.Clicked() {
				e.act(b[0])
			}
		}
	})
}

func (e *drawingEditor) colorField(c *ui.Context, key, label string, value *ui.Color) bool {
	changed := false
	drawField(c, label, func() {
		changed = ui.ColorWell(c.Key(key), value).Label(label).Changed()
	})
	return changed
}

func (e *drawingEditor) numberField(c *ui.Context, key, label string, value *float64, lo, hi, step float64) bool {
	changed := false
	drawField(c, label, func() {
		changed = ui.NumberInput(c.Key(key), value, lo, hi, step).Width(110).Label(label).Changed()
	})
	return changed
}

func (e *drawingEditor) choiceField(c *ui.Context, key, label string, value *string, choices [][2]string) bool {
	changed := false
	drawField(c, label, func() { changed = drawChoice(c, key, label, value, choices, 170) })
	return changed
}

// View draws the editor: toolbar, status, canvas, help and panels.
func (e *drawingEditor) View(c *ui.Context) {
	t := c.Theme()
	n := e.n()
	root := ui.Column(c.Key("drawing-editor")).Gap(10).Children(func() {
		ui.Text(c, drawCopy).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Wrap().AlignItems(ui.End).Children(func() {
			ui.Row(c).Gap(4).Children(func() {
				if ui.Button(c.Key("dr-undo"), "↶ Undo").Tooltip("Undo (Ctrl/⌘ Z)").Disabled(len(e.undo) == 0).Clicked() {
					e.act("undo")
				}
				if ui.Button(c.Key("dr-redo"), "↷ Redo").Tooltip("Redo (Ctrl/⌘ Shift Z)").Disabled(len(e.redo) == 0).Clicked() {
					e.act("redo")
				}
			})
			ui.Row(c).Gap(4).Children(func() {
				for _, z := range [][3]string{{"zoomOut", "−", "Zoom out"}, {"zoomIn", "+", "Zoom in"}, {"fit", "Fit", "Fit the drawing to the view"}} {
					if ui.Button(c.Key("dr-"+z[0]), z[1]).Tooltip(z[2]).Label(z[2]).Clicked() {
						e.act(z[0])
					}
				}
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Layout").FontSize(12).TextColor(t.TextMuted)
				if drawChoice(c, "dr-layout", "Layout", &e.layoutKind, drawLayoutChoices, 200) {
					e.relayout("Layout applied.")
				}
				if ui.Button(c.Key("dr-relayout"), "Re-layout").Tooltip("Recompute all positions with this layout").Clicked() {
					e.act("layout")
				}
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Export").FontSize(12).TextColor(t.TextMuted)
				drawChoice(c, "dr-export", "Export format", &e.exportFormat, drawExportChoices, 200)
				if ui.Button(c.Key("dr-save"), "Save…").Tooltip("Save the edited drawing in this format").Clicked() {
					e.export(e.exportFormat)
				}
				if ui.Button(c.Key("dr-load"), "Load JSON…").Tooltip("Load a saved drawing JSON").Clicked() {
					e.load()
				}
			})
		})
		ui.Row(c).Gap(12).Wrap().Children(func() {
			a, b := e.selection[0], e.selection[1]
			sel := fmt.Sprintf("Selected: %d–%d (%d nt)", a, b, b-a+1)
			if a == b {
				sel = fmt.Sprintf("Selected: base %d (%c)", a, e.data.Sequence[a-1])
			}
			ui.Text(c, sel).FontSize(12).Bold()
			if e.message != "" {
				ui.Text(c, e.message).FontSize(12).TextColor(t.TextMuted).Grow(1)
			}
		})
		ui.Box(c.Key("drawing-canvas")).FillWidth().Height(560).Focusable().Border(1, t.Border).Radius(6).
			Role(ui.RoleImage).Label("Editable RNA drawing").HandleInput(e.input).Draw(e.paint)
		ui.Text(c, drawHelp).FontSize(11).TextColor(t.TextMuted)
		e.panels(c, n)
	})
	for _, k := range []struct {
		mods ui.Modifiers
		key  ui.Key
		redo bool
	}{{ui.Cmd, ui.KeyZ, false}, {ui.Cmd | ui.Shift, ui.KeyZ, true}, {ui.Cmd, ui.KeyY, true}, {ui.Ctrl, ui.KeyZ, false}, {ui.Ctrl | ui.Shift, ui.KeyZ, true}, {ui.Ctrl, ui.KeyY, true}} {
		if root.Shortcut(k.mods, k.key) {
			if k.redo {
				e.redoStep()
			} else {
				e.undoStep()
			}
			e.sync()
		}
	}
}

func (e *drawingEditor) panels(c *ui.Context, n int) {
	t := c.Theme()
	grid := func(fn func()) { ui.Row(c).Gap(10).Wrap().AlignItems(ui.End).Children(fn) }
	note := func(s string) { ui.Text(c, s).FontSize(11).TextColor(t.TextMuted) }
	ui.Collapsible(c.Key("dr-sec-selection"), "Selection", &e.open.selection, func() {
		ui.Column(c).Gap(8).Children(func() {
			grid(func() {
				for _, f := range []struct {
					key, label string
					v          *float64
				}{{"dr-from", "From", &e.from}, {"dr-to", "To", &e.to}} {
					if e.numberField(c, f.key, f.label, f.v, 1, float64(n), 1) {
						if _, err := e.selected(); err != nil {
							e.message = err.Error()
						} else {
							e.coalesce = ""
							e.sync()
						}
					}
				}
			})
			e.buttons(c, [][3]string{{"domain", "Domain", "Select the helix and everything it closes"}, {"parent", "Enclosing domain", "Expand to the next enclosing domain"}, {"all", "All", "Select every base"}})
			e.buttons(c, [][3]string{{"rotL", "⟲ 15°", "Rotate the selection 15° anticlockwise"}, {"rotR", "⟳ 15°", "Rotate the selection 15° clockwise"},
				{"rot90", "⟳ 90°", "Rotate the selection 90° clockwise"}, {"mirror", "Mirror", "Mirror the selection"}})
			e.buttons(c, [][3]string{{"pair", "Pair the two ends", "Add a pair between the first and last selected bases"}, {"unpair", "Remove pairs", "Remove pairs that touch the selection"}})
		})
	})
	ui.Collapsible(c.Key("dr-sec-colours"), "Selection colours", &e.open.colours, func() {
		ui.Column(c).Gap(8).Children(func() {
			grid(func() {
				for _, f := range []struct {
					key, label, name string
					v                *ui.Color
				}{{"dr-fill", "Fill", "fill", &e.fill}, {"dr-text", "Letter", "text", &e.text}, {"dr-outline", "Outline", "outline", &e.outline}} {
					if e.colorField(c, f.key, f.label, f.v) {
						e.styleField(f.name)
					}
				}
				if e.numberField(c, "dr-radius", "Radius", &e.radius, 3, 24, 1) {
					e.styleField("radius")
				}
				if e.colorField(c, "dr-pair-color", "Pair colour", &e.pairColor) {
					e.styleField("pairColor")
				}
				if e.numberField(c, "dr-pair-width", "Pair width", &e.pairWidth, .5, 10, .5) {
					e.styleField("pairWidth")
				}
			})
			note("Changes apply to the selected bases and their pairs at once.")
			e.buttons(c, [][3]string{{"resetStyle", "Default colours for selection", ""}})
		})
	})
	ui.Collapsible(c.Key("dr-sec-style"), "Drawing style", &e.open.style, func() {
		ui.Column(c).Gap(8).Children(func() {
			global := func(name string, changed bool) {
				if changed {
					e.globalStyle(name)
				}
			}
			grid(func() {
				global("pairStyle", e.choiceField(c, "dr-pair-style", "Pair style", &e.pairStyle, drawPairStyleChoices))
				global("pairCurve", e.choiceField(c, "dr-pair-curve", "Pair shape", &e.pairCurve, drawCurveChoices))
				global("font", e.choiceField(c, "dr-font", "Font", &e.font, drawFontChoices))
				global("letterSize", e.numberField(c, "dr-letter", "Letter size", &e.letterSize, 6, 30, 1))
				global("numbers", e.numberField(c, "dr-numbers", "Number every", &e.numbers, 1, 1000, 1))
				global("numbering", e.choiceField(c, "dr-numbering", "Numbering", &e.numbering, drawShownChoices))
				global("numberColor", e.colorField(c, "dr-number-color", "Number colour", &e.numberColor))
				global("backbone", e.colorField(c, "dr-backbone", "Backbone", &e.backbone))
				global("backboneWidth", e.numberField(c, "dr-backbone-width", "Backbone width", &e.backboneWidth, 0, 10, .5))
				global("background", e.colorField(c, "dr-background", "Background", &e.background))
				global("titleColor", e.colorField(c, "dr-title-color", "Title colour", &e.titleColor))
				global("titleSize", e.numberField(c, "dr-title-size", "Title size", &e.titleSize, 8, 48, 1))
			})
			drawField(c, "Title", func() {
				if drJsLen(e.title) > 500 {
					e.title = string([]rune(e.title)[:500])
				}
				global("title", ui.TextInput(c.Key("dr-title"), &e.title).Label("Title").Width(420).Changed())
			})
			grid(func() {
				if e.numberField(c, "dr-spacing", "Base spacing", &e.spacing, .5, 3, .1) {
					e.relayout("Layout applied.")
				}
				if e.choiceField(c, "dr-flat", "Exterior loop", &e.flat, drawFlatChoices) {
					e.relayout("Layout applied.")
				}
			})
			note("Style changes apply at once. Spacing and exterior loop re-run the layout.")
		})
	})
	ui.Collapsible(c.Key("dr-sec-notes"), "Annotations", &e.open.notes, func() {
		ui.Column(c).Gap(8).Children(func() {
			drawField(c, "Text", func() {
				ui.TextInput(c.Key("dr-note-text"), &e.noteText).Placeholder("Label text").Label("Annotation text").Width(420)
			})
			grid(func() {
				e.choiceField(c, "dr-note-kind", "Type", &e.noteKind, drawNoteKindChoices)
				e.choiceField(c, "dr-anchor", "Anchor", &e.anchorType, drawAnchorChoices)
				e.colorField(c, "dr-note-color", "Colour", &e.noteColor)
				e.numberField(c, "dr-note-size", "Text size", &e.noteSize, 6, 48, 1)
				e.choiceField(c, "dr-marker", "Probe marker", &e.marker, drawMarkerChoices)
				e.choiceField(c, "dr-direction", "Probe direction", &e.direction, drawDirectionChoices)
				e.numberField(c, "dr-intensity", "Probe intensity", &e.intensity, .25, 4, .25)
				e.numberField(c, "dr-opacity", "Highlight opacity", &e.opacity, .05, 1, .05)
				e.numberField(c, "dr-region-width", "Highlight width", &e.regionW, 2, 80, 1)
			})
			drawField(c, "Saved annotations", func() {
				labels := []string{"Select annotation"}
				for i, a := range e.data.Annotations {
					labels = append(labels, fmt.Sprintf("%d: %s · %s", i+1, drOrS(a.Kind, "text"), drOrS(a.Text, "untitled")))
				}
				if e.noteIndex >= len(e.data.Annotations) {
					e.noteIndex = -1
				}
				current := labels[e.noteIndex+1]
				if ui.Select(c.Key("dr-note-list"), &current, labels).Width(320).Label("Saved annotations").Changed() {
					for i, l := range labels {
						if l == current {
							e.noteIndex = i - 1
						}
					}
					if e.noteIndex >= 0 {
						e.selectAnnotation(e.noteIndex)
					}
				}
			})
			e.buttons(c, [][3]string{{"annotation", "Add", ""}, {"updateAnnotation", "Update", ""}, {"removeAnnotation", "Remove", ""}, {"clearAnnotations", "Clear all", ""}})
			e.buttons(c, [][3]string{{"ends", "5′ / 3′ labels", ""}, {"helices", "Helix labels", ""}, {"hairpins", "Hairpin labels", ""}})
		})
	})
	ui.Collapsible(c.Key("dr-sec-map"), "Colour map", &e.open.colourMap, func() {
		ui.Column(c).Gap(8).Children(func() {
			grid(func() {
				if e.choiceField(c, "dr-palette", "Palette", &e.palette, drawPaletteChoices) && e.data.Evidence != nil {
					e.commit(func(next *drawing) error { return drApplyEvidence(next, next.Evidence, e.palette) }, "Palette changed.", "")
				}
				for _, f := range []struct {
					key, label, place string
					v                 *string
				}{{"dr-map-caption", "Caption", "User values", &e.mapCaption}, {"dr-map-min", "Minimum", "auto", &e.mapMin}, {"dr-map-max", "Maximum", "auto", &e.mapMax}} {
					drawField(c, f.label, func() {
						ui.TextInput(c.Key(f.key), f.v).Placeholder(f.place).Label(f.label).Width(140)
					})
				}
			})
			drawField(c, "Values", func() {
				ui.TextArea(c.Key("dr-map-values"), &e.mapValues).Placeholder(fmt.Sprintf("%d values (one per base), or “position value” lines, e.g. SHAPE reactivities", n)).
					Label("Colour-map values").Height(70).Font("monospace").FontSize(11)
			})
			e.buttons(c, [][3]string{{"colormap", "Apply values", ""}, {"evidence", "Evidence colours", ""}, {"clearColors", "Clear colours", ""}})
		})
	})
}
