package widgets

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"image/png"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

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

// topWeight is the largest weight, which pairAlpha scales by.
func (v *StructView) topWeight() float64 {
	top := 0.0
	for _, w := range v.Weights {
		top = math.Max(top, w)
	}
	return top
}

func (v *StructView) pairAlpha(i int, top float64) float64 {
	if !v.Shade || len(v.Weights) == 0 {
		return 0.9
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
	top := v.topWeight()
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
		var depth []int
		if v.Mode == ModeRadial {
			depth = nestingDepths(v.Pairs)
		}
		for i, p := range v.Pairs {
			x0, y0 := point(p.I)
			x1, y1 := point(p.J)
			s := shape{kind: shapeLine, x0: x0, y0: y0, x1: x1, y1: y1, width: 1.4, color: v.pairColor(p, c), alpha: v.pairAlpha(i, top)}
			if v.Mode == ModeRadial {
				pull := math.Max(0.16, 0.64-math.Min(float64(depth[i]), 9)*0.045)
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
			col, a := v.pairColor(p, c), v.pairAlpha(i, top)
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
				out = append(out, shape{kind: shapeLine, x0: x0, y0: y0, x1: x1, y1: y1, width: backbone * 1.4, color: v.pairColor(p, c), alpha: v.pairAlpha(i, top)})
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

// nestingDepths counts, for each pair, the pairs that enclose it (o.I < p.I
// and o.J > p.J; pseudoknot layers included) in O(P log P): pairs are taken
// in order of I, and a Fenwick tree over the ranks of J counts the earlier
// pairs that close after this one.
func nestingDepths(pairs []LayeredPair) []int {
	order := make([]int, len(pairs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return pairs[order[a]].I < pairs[order[b]].I })
	js := make([]int, len(pairs))
	for i, p := range pairs {
		js[i] = p.J
	}
	sort.Ints(js)
	js = slices.Compact(js)
	rank := func(j int) int { r, _ := slices.BinarySearch(js, j); return r + 1 } // 1-based
	tree := make([]int, len(js)+1)
	inserted := 0
	depth := make([]int, len(pairs))
	for a := 0; a < len(order); {
		// pairs opening at the same I do not enclose each other
		b := a
		for b < len(order) && pairs[order[b]].I == pairs[order[a]].I {
			b++
		}
		for _, k := range order[a:b] {
			atMost := 0 // inserted pairs with J <= this J
			for r := rank(pairs[k].J); r > 0; r -= r & -r {
				atMost += tree[r]
			}
			depth[k] = inserted - atMost
		}
		for _, k := range order[a:b] {
			for r := rank(pairs[k].J); r < len(tree); r += r & -r {
				tree[r]++
			}
			inserted++
		}
		a = b
	}
	return depth
}

// shapeCache keeps the last layouts painted: the views rebuild their
// StructView every frame, so a layout is found again by its inputs.
var shapeCache struct {
	sync.Mutex
	entries []shapeEntry
}

type shapeEntry struct {
	key    shapeKey
	shapes []shape
}

type shapeKey struct {
	size                  float64
	colors                StructColors
	mode                  StructMode
	start, length         int
	shade                 bool
	sequence              string
	pairs, weights, place uint64 // content hashes
	npairs                int
}

func (v *StructView) cacheKey(size float64, c StructColors) shapeKey {
	h := fnv.New64a()
	var buf [8]byte
	word := func(x uint64) { binary.LittleEndian.PutUint64(buf[:], x); h.Write(buf[:]) }
	for _, p := range v.Pairs {
		word(uint64(p.I)<<32 | uint64(uint32(p.J)))
		word(uint64(p.Layer))
	}
	pairs := h.Sum64()
	h.Reset()
	for _, w := range v.Weights {
		word(math.Float64bits(w))
	}
	weights := h.Sum64()
	h.Reset()
	for _, xy := range v.Coords {
		word(math.Float64bits(xy[0]))
		word(math.Float64bits(xy[1]))
	}
	return shapeKey{size: size, colors: c, mode: v.Mode, start: v.Start, length: v.Length, shade: v.Shade,
		sequence: v.Sequence, pairs: pairs, weights: weights, place: h.Sum64(), npairs: len(v.Pairs)}
}

// cachedShapes is shapes, reused while size, colours and inputs stay the
// same (hashing the inputs is linear; laying them out is not cheap).
func (v *StructView) cachedShapes(size float64, c StructColors) []shape {
	key := v.cacheKey(size, c)
	shapeCache.Lock()
	for _, e := range shapeCache.entries {
		if e.key == key {
			shapeCache.Unlock()
			return e.shapes
		}
	}
	shapeCache.Unlock()
	out := v.shapes(size, c)
	shapeCache.Lock()
	defer shapeCache.Unlock()
	shapeCache.entries = append(shapeCache.entries, shapeEntry{key, out})
	if len(shapeCache.entries) > 4 {
		shapeCache.entries = shapeCache.entries[1:]
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
		v.paint(p, r, colors)
	})
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

// paint draws the shapes in the square r.
func (v *StructView) paint(p *ui.Painter, r ui.Rect, colors StructColors) {
	shapes := v.cachedShapes(float64(r.W), colors)
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
				key := strokeKey{s.width, s.color, int(math.Round(s.alpha * 32))}
				path := paths[key]
				if path == nil {
					path = &ui.Path{}
					paths[key] = path
					order = append(order, key)
				}
				path.MoveTo(r.X+float32(s.x0), r.Y+float32(s.y0))
				if s.kind == shapeQuad {
					path.QuadTo(r.X+float32(s.cx), r.Y+float32(s.cy), r.X+float32(s.x1), r.Y+float32(s.y1))
				} else {
					path.LineTo(r.X+float32(s.x1), r.Y+float32(s.y1))
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
		col := s.color.Alpha(float32(s.alpha))
		switch s.kind {
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
}

// PNG is the view as the SVG lays it out — side units wide under a title —
// drawn at scale pixels per unit, as the web's PNG export.
func (v *StructView) PNG(side float32, colors StructColors, title string, scale float32) ([]byte, error) {
	const head = 28
	img := ui.Render(func(c *ui.Context) {
		ui.Box(c).FillWidth().FillHeight().Draw(func(p *ui.Painter, full ui.Rect) {
			p.Fill(full, colors.Background, 0)
			p.Text(full.X+8, full.Y+6, title, 13, colors.Text)
			v.paint(p, ui.Rect{X: full.X, Y: full.Y + head, W: side, H: side}, colors)
		})
	}, int(side), int(side)+head, scale)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
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
