package widgets

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
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
	// Styles, aligned with Pairs, draw each pair in its own colour,
	// opacity, width and dash (the web's structurePairStyle); without them
	// pairs are coloured by layer.
	Styles []PairStyle
	// Markers draws a marker per nucleotide (the web's nucleotide
	// markers: on the ring, under the matrix, the folded bases), filled
	// with Marks[position-Start] when set (evidence colours), else with
	// the background. Above MarkerMax nucleotides the ring and matrix
	// views draw no markers but take a click anywhere along the ring or
	// the track under the matrix, as the web's inspection track.
	Markers bool
	Marks   []ui.Color
	// Selected is the inspected position (0 for none): an accent marker,
	// a ray from the ring's centre, a crosshair in the matrix.
	Selected int
	// Legend is drawn under the title of the SVG and PNG exports.
	Legend []LegendItem
	// Zoom, for the folded view, zooms and pans it (nil: fitted).
	Zoom *StructZoom
}

// MarkerMax is the longest RNA drawn with a marker per nucleotide on the
// ring and under the matrix (the web's 700 nt).
const MarkerMax = 700

// LayeredPair is a base pair with its bracket layer.
type LayeredPair struct{ I, J, Layer int }

// PairStyle is how one pair is drawn: Width is the web's stroke width
// (its 680-unit drawings), Dash the web's "5 3" dash of crossing layers.
type PairStyle struct {
	Color ui.Color
	Alpha float64
	Width float64
	Dash  bool
}

// LegendItem is one entry of an export's legend.
type LegendItem struct {
	Color ui.Color
	Label string
}

// StructMode is a StructView drawing.
type StructMode int

const (
	ModeCircular StructMode = iota
	ModeRadial
	ModeMatrix
	ModeFolded
)

// Colors are the structure palette: Line for the backbone and frame, then
// the primary pairs and the pseudoknot layers; Selected marks the
// inspected nucleotide.
type StructColors struct {
	Background, Line, Text, Primary, Pseudoknot, Other, Selected ui.Color
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
	dash           bool
	// pos is the nucleotide a marker stands for, pair the index+1 of the
	// pair a stroke or matrix point draws (mirror: the (j, i) point).
	pos, pair int
	mirror    bool
	// fine shapes (folded bases and labels) are left out where they would
	// be too small to see; a circle is drawn at least min DIPs wide (the
	// selected folded base).
	fine bool
	min  float64
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

// pairLook is pair i's colour, opacity, width (for base, the width of an
// unstyled pair) and dash.
func (v *StructView) pairLook(i int, c StructColors, top, base float64) (ui.Color, float64, float64, bool) {
	if len(v.Styles) == len(v.Pairs) {
		s := v.Styles[i]
		return s.Color, s.Alpha, base * s.Width / 1.6, s.Dash
	}
	return v.pairColor(v.Pairs[i], c), v.pairAlpha(i, top), base, false
}

// markerRadius is the web's nucleotide marker radius by RNA length.
func markerRadius(n int) float64 {
	switch {
	case n <= 120:
		return 4.5
	case n <= 360:
		return 3
	}
	return 1.9
}

// marker is position pos's marker at (x, y): filled by its evidence
// colour, or the accent when selected.
func (v *StructView) marker(pos int, x, y, radius float64, c StructColors, fine bool) []shape {
	fill, ring, width := c.Background, c.Line, 1.1
	if k := pos - v.Start; k >= 0 && k < len(v.Marks) && v.Marks[k].A > 0 {
		fill = v.Marks[k]
	}
	least := 0.0
	if pos == v.Selected {
		fill, ring, width, fine, least = c.Selected, c.Selected, 2.3, false, 4
	}
	return []shape{{kind: shapeCircle, x0: x, y0: y, x1: radius, color: fill, alpha: 1, pos: pos, fine: fine, min: least},
		{kind: shapeRing, x0: x, y0: y, x1: radius, width: width, color: ring, alpha: 1, fine: fine, min: least}}
}

// matrixGeometry is the matrix's plot: left, top and side, and the y of
// the markers or track under it.
func matrixGeometry(size float64) (left, top, plot, track float64) {
	return size * 0.1, size * 0.06, size * 0.84, size*0.9 + 30
}

// ringGeometry is the ring's centre and radius.
func ringGeometry(size float64) (cx, cy, r float64) {
	return size / 2, size / 2, size * 0.42
}

// shapes lays the view out in a size×size square.
func (v *StructView) shapes(size float64, c StructColors) []shape {
	n := max(v.Length, 1)
	top := v.topWeight()
	var out []shape
	switch v.Mode {
	case ModeCircular, ModeRadial:
		cx, cy, r := ringGeometry(size)
		angle := func(pos int) float64 { return -math.Pi/2 + 2*math.Pi*float64(pos-v.Start)/float64(n) }
		point := func(pos int, radius float64) (float64, float64) {
			a := angle(pos)
			return cx + math.Cos(a)*radius, cy + math.Sin(a)*radius
		}
		out = append(out, shape{kind: shapeRing, x0: cx, y0: cy, x1: r, width: 1.5, color: c.Line, alpha: 1})
		if v.Mode == ModeRadial {
			for _, f := range []float64{0.25, 0.5, 0.75} {
				out = append(out, shape{kind: shapeRing, x0: cx, y0: cy, x1: r * f, width: 1, color: c.Line, alpha: 0.5, dash: len(v.Styles) > 0})
			}
		}
		var depth []int
		if v.Mode == ModeRadial {
			depth = nestingDepths(v.Pairs)
		}
		for i, p := range v.Pairs {
			x0, y0 := point(p.I, r)
			x1, y1 := point(p.J, r)
			col, alpha, width, dash := v.pairLook(i, c, top, 1.4)
			if len(v.Styles) == len(v.Pairs) {
				width = v.Styles[i].Width
			}
			s := shape{kind: shapeLine, x0: x0, y0: y0, x1: x1, y1: y1, width: width, color: col, alpha: alpha, dash: dash, pair: i + 1}
			if v.Mode == ModeRadial {
				pull := math.Max(0.16, 0.64-math.Min(float64(depth[i]), 9)*0.045)
				s.kind = shapeQuad
				s.cx, s.cy = cx+((x0+x1)/2-cx)*pull, cy+((y0+y1)/2-cy)*pull
			}
			out = append(out, s)
		}
		if v.Selected >= v.Start && v.Selected < v.Start+n {
			x, y := point(v.Selected, r-8)
			out = append(out, shape{kind: shapeLine, x0: cx, y0: cy, x1: x, y1: y, width: 1.2, color: c.Selected, alpha: 0.45, dash: true})
		}
		for _, tick := range ticks(v.Start, n) {
			a := angle(tick)
			x, y := cx+math.Cos(a)*(r+16), cy+math.Sin(a)*(r+16)
			out = append(out, shape{kind: shapeText, x0: x, y0: y, text: strconv.Itoa(tick), size: 10, color: c.Text, alpha: 1})
		}
		if v.Markers && n <= MarkerMax {
			radius := markerRadius(n)
			for pos := v.Start; pos < v.Start+n; pos++ {
				x, y := point(pos, r)
				out = append(out, v.marker(pos, x, y, radius, c, false)...)
			}
		} else if v.Markers && v.Selected >= v.Start && v.Selected < v.Start+n {
			x, y := point(v.Selected, r)
			out = append(out, v.marker(v.Selected, x, y, 4.5, c, false)...)
		}
	case ModeMatrix:
		left, top0, plot, track := matrixGeometry(size)
		scale := func(pos int) float64 { return float64(pos-v.Start) / float64(n) * plot }
		out = append(out,
			shape{kind: shapeLine, x0: left, y0: top0, x1: left + plot, y1: top0, width: 1, color: c.Line, alpha: 1},
			shape{kind: shapeLine, x0: left + plot, y0: top0, x1: left + plot, y1: top0 + plot, width: 1, color: c.Line, alpha: 1},
			shape{kind: shapeLine, x0: left + plot, y0: top0 + plot, x1: left, y1: top0 + plot, width: 1, color: c.Line, alpha: 1},
			shape{kind: shapeLine, x0: left, y0: top0 + plot, x1: left, y1: top0, width: 1, color: c.Line, alpha: 1},
			shape{kind: shapeLine, x0: left, y0: top0, x1: left + plot, y1: top0 + plot, width: 1, color: c.Line, alpha: 0.5, dash: len(v.Styles) > 0})
		radius := math.Max(1, math.Min(4, plot/float64(n)*1.5))
		if len(v.Styles) > 0 && n > 180 {
			radius = math.Min(radius, 3) // the web's 4, or 3 above 180 nt
		}
		for i, p := range v.Pairs {
			col, a, _, _ := v.pairLook(i, c, top, 1)
			out = append(out,
				shape{kind: shapeCircle, x0: left + scale(p.I), y0: top0 + scale(p.J), x1: radius, color: col, alpha: a, pair: i + 1},
				shape{kind: shapeCircle, x0: left + scale(p.J), y0: top0 + scale(p.I), x1: radius, color: col, alpha: a, pair: i + 1, mirror: true})
		}
		if v.Selected >= v.Start && v.Selected < v.Start+n {
			x, y := left+scale(v.Selected), top0+scale(v.Selected)
			out = append(out,
				shape{kind: shapeLine, x0: x, y0: top0, x1: x, y1: top0 + plot, width: 1.2, color: c.Selected, alpha: 0.55},
				shape{kind: shapeLine, x0: left, y0: y, x1: left + plot, y1: y, width: 1.2, color: c.Selected, alpha: 0.55})
		}
		for _, tick := range ticks(v.Start, n) {
			out = append(out,
				shape{kind: shapeText, x0: left + scale(tick), y0: top0 + plot + 14, text: strconv.Itoa(tick), size: 10, color: c.Text, alpha: 1},
				shape{kind: shapeText, x0: left - 18, y0: top0 + scale(tick), text: strconv.Itoa(tick), size: 10, color: c.Text, alpha: 1})
		}
		if v.Markers && n <= MarkerMax {
			radius := markerRadius(n)
			for pos := v.Start; pos < v.Start+n; pos++ {
				x := left + scale(pos)
				out = append(out, shape{kind: shapeLine, x0: x, y0: track - 7, x1: x, y1: track + 5, width: 1, color: c.Line, alpha: 1})
				out = append(out, v.marker(pos, x, track, radius, c, false)...)
			}
		} else if v.Markers {
			// the web's inspection track: a line with the selected base
			out = append(out, shape{kind: shapeLine, x0: left, y0: track, x1: left + plot, y1: track, width: 2, color: c.Line, alpha: 1})
			if v.Selected >= v.Start && v.Selected < v.Start+n {
				out = append(out, shape{kind: shapeCircle, x0: left + scale(v.Selected), y0: track, x1: 6, color: c.Selected, alpha: 1, pos: v.Selected})
			}
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
				col, alpha, width, dash := v.pairLook(i, c, top, backbone*1.4)
				out = append(out, shape{kind: shapeLine, x0: x0, y0: y0, x1: x1, y1: y1, width: width, color: col, alpha: alpha, dash: dash, pair: i + 1})
			}
		}
		dot := k * 0.35
		// The bases: drawn when large enough at this zoom (fine), with
		// their letters, and numbered every 10 (every 25 above 200 nt) as
		// the web's forna labels, set off from the backbone.
		if dot >= 2 || v.Zoom != nil || v.Markers {
			interval := 10
			if len(v.Coords) > 200 {
				interval = 25
			}
			for pos := v.Start; pos < v.Start+len(v.Coords); pos++ {
				x, y, _ := at(pos)
				out = append(out, v.marker(pos, x, y, dot, c, true)...)
				if i := pos - v.Start; i < len(v.Sequence) {
					col := c.Text
					if pos == v.Selected {
						col = c.Background
					}
					out = append(out, shape{kind: shapeText, x0: x, y0: y, text: v.Sequence[i : i+1], size: dot * 1.2, color: col, alpha: 1, fine: true})
				}
				if i := pos - v.Start + 1; i%interval == 0 {
					dx, dy := v.labelDirection(pos, at)
					out = append(out, shape{kind: shapeText, x0: x + dx*dot*3, y0: y + dy*dot*3, text: strconv.Itoa(pos), size: dot * 1.4, color: c.Line, alpha: 1, fine: true})
				}
			}
		}
		x, y, _ := at(v.Start)
		out = append(out, shape{kind: shapeText, x0: x - 14, y0: y, text: "5′", size: 11, color: c.Text, alpha: 1})
	}
	return out
}

// labelDirection is the unit vector from the folded base at pos away from
// its backbone neighbours, where its number goes.
func (v *StructView) labelDirection(pos int, at func(int) (float64, float64, bool)) (float64, float64) {
	x, y, _ := at(pos)
	px, py, okp := at(pos - 1)
	nx, ny, okn := at(pos + 1)
	if !okp {
		px, py = x, y
	}
	if !okn {
		nx, ny = x, y
	}
	dx, dy := x-(px+nx)/2, y-(py+ny)/2
	if l := math.Hypot(dx, dy); l > 1e-6 {
		return dx / l, dy / l
	}
	// a straight stretch: perpendicular to the backbone
	dx, dy = -(ny - py), nx-px
	if l := math.Hypot(dx, dy); l > 1e-6 {
		return dx / l, dy / l
	}
	return 0, -1
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
	styles, marks         uint64
	npairs                int
	selected              int
	markers, zoomable     bool
}

func (v *StructView) cacheKey(size float64, c StructColors) shapeKey {
	h := fnv.New64a()
	var buf [8]byte
	word := func(x uint64) { binary.LittleEndian.PutUint64(buf[:], x); h.Write(buf[:]) }
	color := func(c ui.Color) { word(uint64(c.R)<<24 | uint64(c.G)<<16 | uint64(c.B)<<8 | uint64(c.A)) }
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
	for _, s := range v.Styles {
		color(s.Color)
		word(math.Float64bits(s.Alpha))
		word(math.Float64bits(s.Width))
		if s.Dash {
			word(1)
		}
	}
	styles := h.Sum64()
	h.Reset()
	for _, m := range v.Marks {
		color(m)
	}
	marks := h.Sum64()
	h.Reset()
	for _, xy := range v.Coords {
		word(math.Float64bits(xy[0]))
		word(math.Float64bits(xy[1]))
	}
	return shapeKey{size: size, colors: c, mode: v.Mode, start: v.Start, length: v.Length, shade: v.Shade,
		sequence: v.Sequence, pairs: pairs, weights: weights, place: h.Sum64(), styles: styles, marks: marks,
		npairs: len(v.Pairs), selected: v.Selected, markers: v.Markers, zoomable: v.Zoom != nil}
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

func hex(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
