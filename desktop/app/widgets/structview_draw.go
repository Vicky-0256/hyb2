package widgets

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
)

// StructZoom is the folded view's zoom and pan, kept by the page across
// frames (the web's forna zoom): positions in the view's square are drawn
// at position×Scale + (X, Y)×side, so it holds at any size of the square.
// The zero value is the fitted view.
type StructZoom struct {
	Scale, X, Y float64
	// side is the square's side when last shown, which the buttons zoom
	// about the centre of; dragged, that the press under way panned (its
	// release is no click).
	side    float64
	dragged bool
}

// The zoom's range, the web's forna buttons' factors and its arrow keys'
// pan.
const (
	zoomMin  = 0.25
	zoomMax  = 40
	ZoomIn   = 1.25
	ZoomOut  = 0.8
	PanStep  = 40
	dashOn   = 5
	dashOff  = 3
	hitSlack = 4
)

// transform is the zoom and the pan in DIPs of a side-wide square.
func (z *StructZoom) transform(side float64) (s, x, y float64) {
	if z == nil || z.Scale <= 0 {
		return 1, 0, 0
	}
	return z.Scale, z.X * side, z.Y * side
}

func (z *StructZoom) unit() float64 {
	if z.side > 0 {
		return z.side
	}
	return 1
}

// By zooms by factor about (cx, cy) in the square; with cx and cy
// negative, about its centre.
func (z *StructZoom) By(factor, cx, cy float64) {
	s, x, y := z.transform(1)
	cx, cy = cx/z.unit(), cy/z.unit()
	if cx < 0 || cy < 0 {
		cx, cy = 0.5, 0.5
	}
	next := math.Max(zoomMin, math.Min(zoomMax, s*factor))
	z.X, z.Y = cx-(cx-x)*next/s, cy-(cy-y)*next/s
	z.Scale = next
}

// Pan moves the drawing by (dx, dy) DIPs.
func (z *StructZoom) Pan(dx, dy float64) {
	s, x, y := z.transform(1)
	z.Scale, z.X, z.Y = s, x+dx/z.unit(), y+dy/z.unit()
}

// Fit shows the whole drawing again.
func (z *StructZoom) Fit() { z.Scale, z.X, z.Y = 1, 0, 0 }

// Fitted reports whether the view is as fitted.
func (z *StructZoom) Fitted() bool {
	s, x, y := z.transform(1)
	return s == 1 && x == 0 && y == 0
}

// StructHit is what lies under a point of the view: a nucleotide (0 for
// none) or a pair (its index in Pairs, -1 for none; Mirror for the
// matrix's (j, i) point).
type StructHit struct {
	Position int
	Pair     int
	Mirror   bool
}

// StructEvent is what the pointer and keys did this frame: Hover is
// what is under the pointer (Over when it is over the view), Select a
// nucleotide clicked or stepped to (0 for none).
type StructEvent struct {
	Over   bool
	Hover  StructHit
	Select int
}

// visible: whether a fine shape is large enough to draw at scale.
func (s shape) visible(scale float64) bool {
	if !s.fine {
		return true
	}
	switch s.kind {
	case shapeCircle, shapeRing:
		return s.x1*scale >= 2
	case shapeText:
		return s.size*scale >= 6
	}
	return true
}

// Show draws the view in a square as wide as the space, at most maxSide
// DIPs. A click picks the nucleotide under the pointer; Left, Right, Up,
// Down, Home and End step it while the view has the keyboard focus
// (the web's markers and inspection track). With Zoom (the folded view)
// the wheel or a pinch zooms, dragging pans, + and − zoom, the arrows pan
// 40 DIPs and 0 fits, as the web's forna view.
func (v *StructView) Show(c *ui.Context, maxSide float32, colors StructColors, label string) StructEvent {
	box := ui.Box(c.Key("structview")).FillWidth().MaxWidth(maxSide).AspectRatio(1).Role(ui.RoleImage).Label(label).Focusable()
	bounds := box.Bounds()
	side := min(bounds.W, bounds.H)
	offset := (bounds.W - side) / 2
	before := v.Selected
	panned := false
	if v.Zoom != nil {
		v.Zoom.side = float64(side)
		box.Cursor(ui.CursorGrab)
		zoom := v.Zoom
		box.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputScroll {
				return false
			}
			zoom.By(math.Pow(1.0015, -float64(ev.DY)), float64(ev.X-offset), float64(ev.Y))
			return true
		})
		dx, dy, pressed := box.Dragged()
		if pressed && (dx != 0 || dy != 0) {
			v.Zoom.Pan(float64(dx), float64(dy))
			v.Zoom.dragged = true
		}
		panned = v.Zoom.dragged
		if !pressed {
			v.Zoom.dragged = false
		}
		switch {
		case box.Shortcut(0, ui.KeyEqual), box.Shortcut(ui.Shift, ui.KeyEqual):
			v.Zoom.By(ZoomIn, -1, -1)
		case box.Shortcut(0, ui.KeyMinus), box.Shortcut(ui.Shift, ui.KeyMinus):
			v.Zoom.By(ZoomOut, -1, -1)
		case box.Shortcut(0, ui.Key0):
			v.Zoom.Fit()
		case box.Shortcut(0, ui.KeyLeft):
			v.Zoom.Pan(-PanStep, 0)
		case box.Shortcut(0, ui.KeyRight):
			v.Zoom.Pan(PanStep, 0)
		case box.Shortcut(0, ui.KeyUp):
			v.Zoom.Pan(0, -PanStep)
		case box.Shortcut(0, ui.KeyDown):
			v.Zoom.Pan(0, PanStep)
		}
	} else {
		box.Cursor(ui.CursorPointer)
		n := max(v.Length, 1)
		current := v.Selected
		if current < v.Start || current >= v.Start+n {
			current = 0
		}
		step := func(delta int) int {
			if current == 0 {
				return v.Start
			}
			return max(v.Start, min(v.Start+n-1, current+delta))
		}
		var next int
		switch {
		case box.Shortcut(0, ui.KeyLeft), box.Shortcut(0, ui.KeyDown):
			next = step(-1)
		case box.Shortcut(0, ui.KeyRight), box.Shortcut(0, ui.KeyUp):
			next = step(1)
		case box.Shortcut(0, ui.KeyHome):
			next = v.Start
		case box.Shortcut(0, ui.KeyEnd):
			next = v.Start + n - 1
		}
		if next > 0 && v.Markers {
			v.Selected = next
		}
	}
	event := StructEvent{Hover: StructHit{Pair: -1}}
	if px, py, over := box.PointerPosition(); over && side > 0 {
		event.Over = true
		event.Hover = v.hit(float64(side), colors, float64(px-offset), float64(py))
	}
	if box.Clicked() && !panned && event.Over && event.Hover.Position > 0 && v.Markers {
		v.Selected = event.Hover.Position
	}
	if v.Selected != before {
		event.Select = v.Selected
	}
	box.Draw(func(p *ui.Painter, full ui.Rect) {
		side := min(full.W, full.H)
		r := ui.Rect{X: full.X + (full.W-side)/2, Y: full.Y, W: side, H: side}
		p.Fill(r, colors.Background, 6)
		p.Clip(r, 6, func() { v.paint(p, r, colors) })
	})
	return event
}

// hit is what lies at (x, y) of a side×side view: the topmost marker or
// pair there, or along the ring or the matrix's track of a long RNA, the
// nucleotide at that angle or column.
func (v *StructView) hit(side float64, colors StructColors, x, y float64) StructHit {
	s, tx, ty := v.Zoom.transform(side)
	qx, qy := (x-tx)/s, (y-ty)/s
	slack := hitSlack / s
	shapes := v.cachedShapes(side, colors)
	for i := len(shapes) - 1; i >= 0; i-- {
		sh := shapes[i]
		switch {
		case sh.kind == shapeCircle && sh.pos > 0:
			if math.Hypot(qx-sh.x0, qy-sh.y0) <= math.Max(sh.x1, 3/s)+slack {
				return StructHit{Position: sh.pos, Pair: -1}
			}
		case sh.kind == shapeCircle && sh.pair > 0:
			if math.Hypot(qx-sh.x0, qy-sh.y0) <= sh.x1+slack/2 {
				return StructHit{Pair: sh.pair - 1, Mirror: sh.mirror}
			}
		case sh.kind == shapeLine && sh.pair > 0:
			if segmentDistance(qx, qy, sh.x0, sh.y0, sh.x1, sh.y1) <= sh.width/2+slack/2 {
				return StructHit{Pair: sh.pair - 1}
			}
		case sh.kind == shapeQuad && sh.pair > 0:
			pts := quadPoints(sh, 16)
			for k := 1; k < len(pts); k++ {
				if segmentDistance(qx, qy, pts[k-1][0], pts[k-1][1], pts[k][0], pts[k][1]) <= sh.width/2+slack/2 {
					return StructHit{Pair: sh.pair - 1}
				}
			}
		}
	}
	n := max(v.Length, 1)
	if !v.Markers || n <= MarkerMax {
		return StructHit{Pair: -1}
	}
	switch v.Mode {
	case ModeCircular, ModeRadial:
		cx, cy, r := ringGeometry(side)
		if math.Abs(math.Hypot(qx-cx, qy-cy)-r) <= 14 {
			a := math.Atan2(qy-cy, qx-cx) + math.Pi/2
			if a < 0 {
				a += 2 * math.Pi
			}
			pos := v.Start + int(math.Round(a/(2*math.Pi)*float64(n)))%n
			return StructHit{Position: pos, Pair: -1}
		}
	case ModeMatrix:
		left, _, plot, track := matrixGeometry(side)
		if math.Abs(qy-track) <= 14 && qx >= left-6 && qx <= left+plot+6 {
			pos := v.Start + int(math.Round(math.Max(0, math.Min(1, (qx-left)/plot))*float64(n-1)))
			return StructHit{Position: pos, Pair: -1}
		}
	}
	return StructHit{Pair: -1}
}

func segmentDistance(x, y, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, ((x-x0)*dx+(y-y0)*dy)/l))
	}
	return math.Hypot(x-(x0+t*dx), y-(y0+t*dy))
}

// quadPoints are n+1 points along a quad shape.
func quadPoints(s shape, n int) [][2]float64 {
	out := make([][2]float64, n+1)
	for k := 0; k <= n; k++ {
		t := float64(k) / float64(n)
		u := 1 - t
		out[k] = [2]float64{u*u*s.x0 + 2*u*t*s.cx + t*t*s.x1, u*u*s.y0 + 2*u*t*s.cy + t*t*s.y1}
	}
	return out
}

// dashed adds the dashes (on, off long) of the polyline pts to path.
func dashed(path *ui.Path, pts [][2]float64, on, off float64) {
	drawing, left := true, on
	for k := 1; k < len(pts); k++ {
		x0, y0 := pts[k-1][0], pts[k-1][1]
		x1, y1 := pts[k][0], pts[k][1]
		seg := math.Hypot(x1-x0, y1-y0)
		at := 0.0
		for at < seg {
			step := math.Min(left, seg-at)
			if drawing {
				fx, fy := x0+(x1-x0)*at/seg, y0+(y1-y0)*at/seg
				tx, ty := x0+(x1-x0)*(at+step)/seg, y0+(y1-y0)*(at+step)/seg
				path.MoveTo(float32(fx), float32(fy)).LineTo(float32(tx), float32(ty))
			}
			at += step
			left -= step
			if left <= 1e-9 {
				drawing = !drawing
				left = map[bool]float64{true: on, false: off}[drawing]
			}
		}
	}
}

// strokeKey groups strokes painted as one path: one mask the painter keeps
// across frames, where thousands of long curves, one mask each, overflow
// its cache and are rasterized again every frame. Opacity is grouped in
// 1/32 steps (shaded pairs vary continuously).
type strokeKey struct {
	width float64
	color ui.Color
	alpha int
}

// paint draws the shapes in the square r, zoomed by Zoom.
func (v *StructView) paint(p *ui.Painter, r ui.Rect, colors StructColors) {
	shapes := v.cachedShapes(float64(r.W), colors)
	scale, tx, ty := v.Zoom.transform(float64(r.W))
	at := func(x, y float64) (float64, float64) {
		return float64(r.X) + x*scale + tx, float64(r.Y) + y*scale + ty
	}
	for i := 0; i < len(shapes); {
		if k := shapes[i].kind; k == shapeLine || k == shapeQuad {
			// A run of strokes (nothing painted between them to keep
			// above or below), grouped by look in order of appearance.
			end := i
			for end < len(shapes) && (shapes[end].kind == shapeLine || shapes[end].kind == shapeQuad) {
				end++
			}
			paths := map[strokeKey]*ui.Path{}
			var order []strokeKey
			for _, s := range shapes[i:end] {
				key := strokeKey{s.width * scale, s.color, int(math.Round(s.alpha * 32))}
				path := paths[key]
				if path == nil {
					path = &ui.Path{}
					paths[key] = path
					order = append(order, key)
				}
				x0, y0 := at(s.x0, s.y0)
				x1, y1 := at(s.x1, s.y1)
				switch {
				case s.dash && s.kind == shapeQuad:
					pts := quadPoints(s, 24)
					for k := range pts {
						pts[k][0], pts[k][1] = at(pts[k][0], pts[k][1])
					}
					dashed(path, pts, dashOn*scale, dashOff*scale)
				case s.dash:
					dashed(path, [][2]float64{{x0, y0}, {x1, y1}}, dashOn*scale, dashOff*scale)
				case s.kind == shapeQuad:
					cx, cy := at(s.cx, s.cy)
					path.MoveTo(float32(x0), float32(y0)).QuadTo(float32(cx), float32(cy), float32(x1), float32(y1))
				default:
					path.MoveTo(float32(x0), float32(y0)).LineTo(float32(x1), float32(y1))
				}
			}
			for _, key := range order {
				p.StrokePath(paths[key], float32(key.width), key.color.Alpha(float32(key.alpha)/32))
			}
			i = end
			continue
		}
		s := shapes[i]
		i++
		if !s.visible(scale) {
			continue
		}
		col := s.color.Alpha(float32(s.alpha))
		x, y := at(s.x0, s.y0)
		switch s.kind {
		case shapeCircle:
			d := float32(math.Max(s.x1*scale, s.min))
			p.Fill(ui.Rect{X: float32(x) - d, Y: float32(y) - d, W: 2 * d, H: 2 * d}, col, d)
		case shapeRing:
			d := float32(math.Max(s.x1*scale, s.min))
			rect := ui.Rect{X: float32(x) - d, Y: float32(y) - d, W: 2 * d, H: 2 * d}
			if s.dash {
				p.StrokeDashed(rect, col, d, float32(s.width))
			} else {
				p.Stroke(rect, col, d, float32(s.width*math.Min(scale, 2)))
			}
		case shapeText:
			size := float32(s.size * scale)
			w := float32(len([]rune(s.text))) * size * 0.55
			p.Text(float32(x)-w/2, float32(y)-size*0.6, s.text, size, col)
		}
	}
}

// legendHeight is the export's band for the legend under the title.
func (v *StructView) legendHeight() float64 {
	if len(v.Legend) == 0 {
		return 0
	}
	return 18
}

// PNG is the view as the SVG lays it out — side units wide under a title
// and the legend — drawn at scale pixels per unit, as the web's PNG export.
func (v *StructView) PNG(side float32, colors StructColors, title string, scale float32) ([]byte, error) {
	head := 28 + float32(v.legendHeight())
	fitted := *v
	fitted.Zoom = nil
	img := ui.Render(func(c *ui.Context) {
		ui.Box(c).FillWidth().FillHeight().Draw(func(p *ui.Painter, full ui.Rect) {
			p.Fill(full, colors.Background, 0)
			p.Text(full.X+8, full.Y+6, title, 13, colors.Text)
			x := full.X + 8
			for _, item := range v.Legend {
				p.Line(x, full.Y+34, x+17, full.Y+34, 2.5, item.Color)
				p.Text(x+24, full.Y+28, item.Label, 11, colors.Line)
				x += 24 + float32(len([]rune(item.Label)))*6.2 + 18
			}
			fitted.paint(p, ui.Rect{X: full.X, Y: full.Y + head, W: side, H: side}, colors)
		})
	}, int(side), int(side+head), scale)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// SVG is the view as an SVG document side units wide, with a title and
// the legend.
func (v *StructView) SVG(side float64, colors StructColors, title string) string {
	var b strings.Builder
	head := 28.0 + v.legendHeight()
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%g" height="%g" viewBox="0 0 %g %g" font-family="Helvetica, Arial, sans-serif">`,
		side, side+head, side, side+head)
	fmt.Fprintf(&b, `<rect width="%g" height="%g" fill="%s"/>`, side, side+head, hex(colors.Background))
	fmt.Fprintf(&b, `<text x="8" y="18" font-size="13" fill="%s">%s</text>`, hex(colors.Text), escape(title))
	x := 8.0
	for _, item := range v.Legend {
		fmt.Fprintf(&b, `<line x1="%g" y1="36" x2="%g" y2="36" stroke="%s" stroke-opacity="%.2f" stroke-width="2.5" stroke-linecap="round"/>`,
			x, x+17, hex(item.Color), float64(item.Color.A)/255)
		fmt.Fprintf(&b, `<text x="%g" y="40" font-size="11" fill="%s">%s</text>`, x+24, hex(colors.Line), escape(item.Label))
		x += 24 + float64(len([]rune(item.Label)))*6.2 + 18
	}
	fmt.Fprintf(&b, `<g transform="translate(0 %g)">`, head)
	for _, s := range v.shapes(side, colors) {
		if !s.visible(1) {
			continue
		}
		paint := fmt.Sprintf(`stroke="%s" stroke-opacity="%.2f"`, hex(s.color), s.alpha)
		if s.dash {
			paint += fmt.Sprintf(` stroke-dasharray="%d %d"`, dashOn, dashOff)
		}
		switch s.kind {
		case shapeLine:
			fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke-width="%.2f" %s stroke-linecap="round"/>`, s.x0, s.y0, s.x1, s.y1, s.width, paint)
		case shapeQuad:
			fmt.Fprintf(&b, `<path d="M%.1f %.1fQ%.1f %.1f %.1f %.1f" fill="none" stroke-width="%.2f" %s stroke-linecap="round"/>`, s.x0, s.y0, s.cx, s.cy, s.x1, s.y1, s.width, paint)
		case shapeCircle:
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.2f" fill="%s" fill-opacity="%.2f"/>`, s.x0, s.y0, math.Max(s.x1, s.min), hex(s.color), s.alpha)
		case shapeRing:
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.2f" fill="none" stroke-width="%.2f" %s/>`, s.x0, s.y0, math.Max(s.x1, s.min), s.width, paint)
		case shapeText:
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="%.1f" fill="%s" text-anchor="middle" dominant-baseline="central">%s</text>`, s.x0, s.y0, s.size, hex(s.color), escape(s.text))
		}
	}
	b.WriteString("</g></svg>\n")
	return b.String()
}
