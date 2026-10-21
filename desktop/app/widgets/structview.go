package widgets

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// StructView draws one structure as the web app's other views: circular
// (chords), radial (curves pulled towards the centre, less for nested
// pairs), a base-pair matrix, or folded (a 2-D layout given in Coords).
// The drawing is a list of shapes in a size×size square, so the same
// geometry paints on screen and writes SVG.
type StructView struct {
	Mode StructMode
	// Pairs are 1-based positions from Start; Layer 0 is the primary
	// (nested) structure, 1 and up pseudoknot layers.
	Pairs  []LayeredPair
	Start  int
	Length int
	// Sequence, when set, labels the nucleotides of a short folded view.
	Sequence string
	// Weights, aligned with Pairs, are the pairs' chimera evidence; with
	// Shade a pair's opacity follows its weight.
	Weights []float64
	Shade   bool
	// Coords are the folded layout, one point per nucleotide (any units).
	Coords [][2]float64
}

// LayeredPair is a base pair with its bracket layer.
type LayeredPair struct{ I, J, Layer int }

// StructMode is a StructView drawing.
type StructMode int

const (
	ModeCircular StructMode = iota
	ModeRadial
	ModeMatrix
	ModeFolded
)

// Colors are the structure palette: Line for the backbone and frame, then
// the primary pairs and the pseudoknot layers.
type StructColors struct {
	Background, Line, Text, Primary, Pseudoknot, Other ui.Color
}

type shapeKind int

const (
	shapeLine shapeKind = iota
	shapeQuad
	shapeCircle // filled
	shapeRing   // stroked circle
	shapeText
)

type shape struct {
	kind           shapeKind
	x0, y0, x1, y1 float64 // line ends; quad ends; circle centre (x0, y0) and radius x1
	cx, cy         float64 // quad control point
	width          float64
	color          ui.Color
	alpha          float64
	text           string
	size           float64
}

func (v *StructView) pairColor(p LayeredPair, c StructColors) ui.Color {
	switch p.Layer {
	case 0:
		return c.Primary
	case 1:
		return c.Pseudoknot
	}
	return c.Other
}

func (v *StructView) pairAlpha(i int) float64 {
	if !v.Shade || len(v.Weights) == 0 {
		return 0.9
	}
	top := 0.0
	for _, w := range v.Weights {
		top = math.Max(top, w)
	}
	w := 0.0
	if i < len(v.Weights) {
		w = v.Weights[i]
	}
	// as the web's CPLfold evidence opacity
	return math.Max(0.32, math.Min(0.96, 0.32+w/math.Max(top, 1e-12)*0.64))
}

// shapes lays the view out in a size×size square.
func (v *StructView) shapes(size float64, c StructColors) []shape {
	n := max(v.Length, 1)
	var out []shape
	switch v.Mode {
	case ModeCircular, ModeRadial:
		cx, cy, r := size/2, size/2, size*0.42
		point := func(pos int) (float64, float64) {
			a := -math.Pi/2 + 2*math.Pi*float64(pos-v.Start)/float64(n)
			return cx + math.Cos(a)*r, cy + math.Sin(a)*r
		}
		out = append(out, shape{kind: shapeRing, x0: cx, y0: cy, x1: r, width: 1.5, color: c.Line, alpha: 1})
		if v.Mode == ModeRadial {
			for _, f := range []float64{0.25, 0.5, 0.75} {
				out = append(out, shape{kind: shapeRing, x0: cx, y0: cy, x1: r * f, width: 1, color: c.Line, alpha: 0.5})
			}
		}
		for i, p := range v.Pairs {
			x0, y0 := point(p.I)
			x1, y1 := point(p.J)
			s := shape{kind: shapeLine, x0: x0, y0: y0, x1: x1, y1: y1, width: 1.4, color: v.pairColor(p, c), alpha: v.pairAlpha(i)}
			if v.Mode == ModeRadial {
				depth := 0
				for _, o := range v.Pairs {
					if o.I < p.I && o.J > p.J {
						depth++
					}
				}
				pull := math.Max(0.16, 0.64-math.Min(float64(depth), 9)*0.045)
				s.kind = shapeQuad
				s.cx, s.cy = cx+((x0+x1)/2-cx)*pull, cy+((y0+y1)/2-cy)*pull
			}
			out = append(out, s)
		}
		for _, tick := range ticks(v.Start, n) {
			a := -math.Pi/2 + 2*math.Pi*float64(tick-v.Start)/float64(n)
			x, y := cx+math.Cos(a)*(r+16), cy+math.Sin(a)*(r+16)
			out = append(out, shape{kind: shapeText, x0: x, y0: y, text: strconv.Itoa(tick), size: 10, color: c.Text, alpha: 1})
		}
	case ModeMatrix:
		left, top, plot := size*0.1, size*0.06, size*0.84
		scale := func(pos int) float64 { return float64(pos-v.Start) / float64(n) * plot }
		out = append(out,
			shape{kind: shapeLine, x0: left, y0: top, x1: left + plot, y1: top, width: 1, color: c.Line, alpha: 1},
			shape{kind: shapeLine, x0: left + plot, y0: top, x1: left + plot, y1: top + plot, width: 1, color: c.Line, alpha: 1},
			shape{kind: shapeLine, x0: left + plot, y0: top + plot, x1: left, y1: top + plot, width: 1, color: c.Line, alpha: 1},
			shape{kind: shapeLine, x0: left, y0: top + plot, x1: left, y1: top, width: 1, color: c.Line, alpha: 1},
			shape{kind: shapeLine, x0: left, y0: top, x1: left + plot, y1: top + plot, width: 1, color: c.Line, alpha: 0.5})
		radius := math.Max(1, math.Min(4, plot/float64(n)*1.5))
		for i, p := range v.Pairs {
			col, a := v.pairColor(p, c), v.pairAlpha(i)
			out = append(out,
				shape{kind: shapeCircle, x0: left + scale(p.I), y0: top + scale(p.J), x1: radius, color: col, alpha: a},
				shape{kind: shapeCircle, x0: left + scale(p.J), y0: top + scale(p.I), x1: radius, color: col, alpha: a})
		}
		for _, tick := range ticks(v.Start, n) {
			out = append(out,
				shape{kind: shapeText, x0: left + scale(tick), y0: top + plot + 14, text: strconv.Itoa(tick), size: 10, color: c.Text, alpha: 1},
				shape{kind: shapeText, x0: left - 18, y0: top + scale(tick), text: strconv.Itoa(tick), size: 10, color: c.Text, alpha: 1})
		}
	case ModeFolded:
		if len(v.Coords) == 0 {
			return nil
		}
		minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, p := range v.Coords {
			minX, maxX = math.Min(minX, p[0]), math.Max(maxX, p[0])
			minY, maxY = math.Min(minY, p[1]), math.Max(maxY, p[1])
		}
		span := math.Max(maxX-minX, maxY-minY)
		if span <= 0 {
			span = 1
		}
		k := size * 0.9 / span
		ox, oy := (size-(maxX-minX)*k)/2, (size-(maxY-minY)*k)/2
		at := func(pos int) (float64, float64, bool) {
			i := pos - v.Start
			if i < 0 || i >= len(v.Coords) {
				return 0, 0, false
			}
			return ox + (v.Coords[i][0]-minX)*k, oy + (v.Coords[i][1]-minY)*k, true
		}
		backbone := math.Max(0.6, math.Min(2, k*0.15))
		for pos := v.Start + 1; pos < v.Start+len(v.Coords); pos++ {
			x0, y0, _ := at(pos - 1)
			x1, y1, _ := at(pos)
			out = append(out, shape{kind: shapeLine, x0: x0, y0: y0, x1: x1, y1: y1, width: backbone, color: c.Line, alpha: 1})
		}
		for i, p := range v.Pairs {
			x0, y0, ok0 := at(p.I)
			x1, y1, ok1 := at(p.J)
			if ok0 && ok1 {
				out = append(out, shape{kind: shapeLine, x0: x0, y0: y0, x1: x1, y1: y1, width: backbone * 1.4, color: v.pairColor(p, c), alpha: v.pairAlpha(i)})
			}
		}
		if dot := k * 0.35; dot >= 2 {
			for pos := v.Start; pos < v.Start+len(v.Coords); pos++ {
				x, y, _ := at(pos)
				out = append(out, shape{kind: shapeCircle, x0: x, y0: y, x1: dot, color: c.Background, alpha: 1},
					shape{kind: shapeRing, x0: x, y0: y, x1: dot, width: 1, color: c.Line, alpha: 1})
				if i := pos - v.Start; dot >= 5 && i < len(v.Sequence) {
					out = append(out, shape{kind: shapeText, x0: x, y0: y, text: v.Sequence[i : i+1], size: dot * 1.2, color: c.Text, alpha: 1})
				}
			}
		}
		x, y, _ := at(v.Start)
		out = append(out, shape{kind: shapeText, x0: x - 14, y0: y, text: "5′", size: 11, color: c.Text, alpha: 1})
	}
	return out
}

// ticks are a few round positions (1, 2 or 5 × 10^k apart) from start
// over n nucleotides.
func ticks(start, n int) []int {
	step := tickStep(n)
	var out []int
	for p := (start + step - 1) / step * step; p < start+n; p += step {
		if p >= 1 {
			out = append(out, p)
		}
	}
	return out
}

func tickStep(n int) int {
	for m := 1; ; m *= 10 {
		for _, s := range []int{1, 2, 5} {
			if n/(s*m) <= 6 {
				return s * m
			}
		}
	}
}

// Show draws the view in a square as wide as the space, at most maxSide
// DIPs.
func (v *StructView) Show(c *ui.Context, maxSide float32, colors StructColors, label string) {
	box := ui.Box(c.Key("structview")).FillWidth().MaxWidth(maxSide).AspectRatio(1).Role(ui.RoleImage).Label(label)
	box.Draw(func(p *ui.Painter, full ui.Rect) {
		side := min(full.W, full.H)
		r := ui.Rect{X: full.X + (full.W-side)/2, Y: full.Y, W: side, H: side}
		p.Fill(r, colors.Background, 6)
		for _, s := range v.shapes(float64(r.W), colors) {
			col := s.color.Alpha(float32(s.alpha))
			switch s.kind {
			case shapeLine: // Painter.Line draws axis-aligned lines only
				path := (&ui.Path{}).MoveTo(r.X+float32(s.x0), r.Y+float32(s.y0)).LineTo(r.X+float32(s.x1), r.Y+float32(s.y1))
				p.StrokePath(path, float32(s.width), col)
			case shapeQuad:
				path := (&ui.Path{}).MoveTo(r.X+float32(s.x0), r.Y+float32(s.y0)).
					QuadTo(r.X+float32(s.cx), r.Y+float32(s.cy), r.X+float32(s.x1), r.Y+float32(s.y1))
				p.StrokePath(path, float32(s.width), col)
			case shapeCircle:
				d := float32(s.x1)
				p.Fill(ui.Rect{X: r.X + float32(s.x0) - d, Y: r.Y + float32(s.y0) - d, W: 2 * d, H: 2 * d}, col, d)
			case shapeRing:
				d := float32(s.x1)
				p.Stroke(ui.Rect{X: r.X + float32(s.x0) - d, Y: r.Y + float32(s.y0) - d, W: 2 * d, H: 2 * d}, col, d, float32(s.width))
			case shapeText:
				size := float32(s.size)
				w := float32(len([]rune(s.text))) * size * 0.55
				p.Text(r.X+float32(s.x0)-w/2, r.Y+float32(s.y0)-size*0.6, s.text, size, col)
			}
		}
	})
}

// SVG is the view as an SVG document side units wide, with a title.
func (v *StructView) SVG(side float64, colors StructColors, title string) string {
	var b strings.Builder
	head := 28.0
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%g" height="%g" viewBox="0 0 %g %g" font-family="Helvetica, Arial, sans-serif">`,
		side, side+head, side, side+head)
	fmt.Fprintf(&b, `<rect width="%g" height="%g" fill="%s"/>`, side, side+head, hex(colors.Background))
	fmt.Fprintf(&b, `<text x="8" y="18" font-size="13" fill="%s">%s</text><g transform="translate(0 %g)">`, hex(colors.Text), escape(title), head)
	for _, s := range v.shapes(side, colors) {
		paint := fmt.Sprintf(`stroke="%s" stroke-opacity="%.2f"`, hex(s.color), s.alpha)
		switch s.kind {
		case shapeLine:
			fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke-width="%.2f" %s stroke-linecap="round"/>`, s.x0, s.y0, s.x1, s.y1, s.width, paint)
		case shapeQuad:
			fmt.Fprintf(&b, `<path d="M%.1f %.1fQ%.1f %.1f %.1f %.1f" fill="none" stroke-width="%.2f" %s stroke-linecap="round"/>`, s.x0, s.y0, s.cx, s.cy, s.x1, s.y1, s.width, paint)
		case shapeCircle:
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.2f" fill="%s" fill-opacity="%.2f"/>`, s.x0, s.y0, s.x1, hex(s.color), s.alpha)
		case shapeRing:
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.2f" fill="none" stroke-width="%.2f" %s/>`, s.x0, s.y0, s.x1, s.width, paint)
		case shapeText:
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="%.1f" fill="%s" text-anchor="middle" dominant-baseline="central">%s</text>`, s.x0, s.y0, s.size, hex(s.color), escape(s.text))
		}
	}
	b.WriteString("</g></svg>\n")
	return b.String()
}

func hex(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
