package widgets

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// Pair is a base pair in 1-based gene coordinates, I < J.
type Pair struct{ I, J int }

// Arcs draws one or two structures as arc diagrams over a gene: A's pairs
// above the baseline, B's below; pairs both have are drawn in the shared
// color. Wheel zooms along the gene, dragging pans, a double click resets.
type Arcs struct {
	A, B   []Pair
	Length int
	// WeightA/WeightB, aligned with A/B, are the pairs' chimera evidence
	// (HYB bonus); with Shade, an arc's opacity follows its weight.
	WeightA, WeightB []float64
	Shade            bool
	// Start/End are the visible range (1-based, inclusive).
	Start, End float64
	// Region, when End > Start, shades a region (e.g. a folded window).
	RegionStart, RegionEnd int
	// Overlay are extra arcs drawn over the structures (e.g. differential
	// interactions): Up ones above the baseline, the others below.
	Overlay []OverlayArc
	// Mono, when not transparent, draws both structures in this one colour
	// (so Overlay colours stand out, as in a PARIS-style arc map).
	Mono ui.Color
	// Features is a gene track drawn under the arcs (e.g. mature peptides).
	Features []Feature
	// StylesA, when aligned with A and there is no B, draw each of A's
	// arcs in its own colour, width and opacity (the web's pair styles by
	// engine: CPLfold layers, COMRADES evidence, ViennaRNA pair types).
	StylesA []ArcStyle
	// Selected is the inspected position (0 for none), drawn as a marker:
	// a click picks it, Left and Right step it while the diagram has the
	// keyboard focus (the web's nucleotide inspector).
	Selected int
	// Gap, when Gap.From and Gap.To are more than 1 apart, is a chain
	// break: the positions between Gap.From (strand A's position next to
	// the break) and Gap.To (strand B's) take no room, so two regions of
	// one RNA (CPLcofold) draw side by side, as the web's A+B arcs, with
	// the backbone broken there. From > To when strand B lies before
	// strand A on the RNA (region 2 before region 1).
	Gap Gap

	shared map[Pair]bool
	keyed  struct{ a, b int }
}

// ArcStyle is how one arc is drawn.
type ArcStyle struct {
	Color ui.Color
	Width float32
}

// OverlayArc is one extra arc (1-based positions, I < J).
type OverlayArc struct {
	I, J  int
	Up    bool
	Color ui.Color
	Width float32
}

// Gap is a chain break between two drawn stretches (see Arcs.Gap).
type Gap struct{ From, To int }

// Bounds are the last position before the gap and the first after it on
// the axis, whichever strand each belongs to.
func (g Gap) Bounds() (lo, hi int) { return min(g.From, g.To), max(g.From, g.To) }

// on reports whether the gap collapses anything.
func (g Gap) on() bool {
	lo, hi := g.Bounds()
	return hi > lo+1
}

// Label is the chain break's label: "A | B", or "B | A" when strand B is
// drawn first.
func (g Gap) Label() string {
	if g.From > g.To {
		return "B | A"
	}
	return "A | B"
}

// Axis is where position pos draws along the axis: pos itself, or, past a
// gap, as if the second stretch followed the first.
func (g Gap) Axis(pos float64) float64 {
	lo, hi := g.Bounds()
	switch {
	case !g.on() || pos <= float64(lo):
		return pos
	case pos >= float64(hi):
		return pos - float64(hi-lo-1)
	}
	return float64(lo) + (pos-float64(lo))/float64(hi-lo)
}

// Position is the position drawn at axis value v (Axis's inverse).
func (g Gap) Position(v float64) float64 {
	lo, hi := g.Bounds()
	switch {
	case !g.on() || v <= float64(lo):
		return v
	case v >= float64(lo+1):
		return v + float64(hi-lo-1)
	}
	return float64(lo) + (v-float64(lo))*float64(hi-lo)
}

// Ticks are round positions from start to end, about n of them, none
// inside the gap: each strand keeps its own positions.
func (g Gap) Ticks(start, end float64, n int) []float64 {
	span := g.Axis(end) - g.Axis(start)
	if span <= 0 || n < 1 {
		return nil
	}
	step := math.Pow(10, math.Floor(math.Log10(span/5)))
	for _, m := range []float64{1, 2, 5, 10} {
		if span/(step*m) <= float64(n) {
			step *= m
			break
		}
	}
	var ticks []float64
	add := func(lo, hi float64) {
		for pos := math.Ceil(lo/step) * step; pos <= hi; pos += step {
			ticks = append(ticks, pos)
		}
	}
	if lo, hi := g.Bounds(); g.on() && start < float64(hi) && end > float64(lo) {
		add(start, math.Min(end, float64(lo)))
		add(math.Max(start, float64(hi)), end)
		return ticks
	}
	add(start, end)
	return ticks
}

// Feature is one block of the gene track (1-based, inclusive).
type Feature struct {
	Start, End int
	Name       string
}

// trackHeight is the gene track's height (DIPs) when Features is set.
const trackHeight = 16

// Set replaces the structures and shows the whole gene.
func (s *Arcs) Set(a, b []Pair, length int) {
	s.A, s.B, s.Length = a, b, length
	s.Start, s.End = 1, float64(length)
	s.shared = nil
	if s.Selected > length {
		s.Selected = 0
	}
}

// Step moves Selected by delta within the RNA (from the first position
// shown when none is selected) and pans to keep it in view.
func (s *Arcs) Step(delta int) {
	if s.Length < 1 {
		return
	}
	if s.Selected < 1 {
		s.Selected = max(1, int(math.Ceil(s.Start)))
	} else {
		s.Selected = max(1, min(s.Length, s.Selected+delta))
	}
	// over the chain break: from the last position before it to the first after
	if lo, hi := s.Gap.Bounds(); s.Gap.on() && s.Selected > lo && s.Selected < hi {
		s.Selected = map[bool]int{true: hi, false: lo}[delta >= 0]
	}
	if pos := float64(s.Selected); pos < s.Start || pos > s.End {
		at, span := s.Gap.Axis(pos), s.Gap.Axis(s.End)-s.Gap.Axis(s.Start)
		s.setAxis(at-span/2, at+span/2)
	}
}

// Shared counts the pairs present in both structures.
func (s *Arcs) Shared() int {
	s.index()
	return len(s.shared)
}

func (s *Arcs) index() {
	if s.shared != nil && s.keyed.a == len(s.A) && s.keyed.b == len(s.B) {
		return
	}
	inA := make(map[Pair]bool, len(s.A))
	for _, p := range s.A {
		inA[p] = true
	}
	s.shared = map[Pair]bool{}
	for _, p := range s.B {
		if inA[p] {
			s.shared[p] = true
		}
	}
	s.keyed.a, s.keyed.b = len(s.A), len(s.B)
}

// ArcEvent reports the gene position under the pointer.
type ArcEvent struct {
	Position int
	Hover    bool
}

// Show builds the diagram, filling the width it is given, height h DIPs.
func (s *Arcs) Show(c *ui.Context, h float32, colorA, colorB, colorShared ui.Color, label string) ArcEvent {
	t := c.Theme()
	s.index()
	if s.End <= s.Start {
		s.Start, s.End = 1, math.Max(float64(s.Length), 2)
	}
	box := ui.Box(c.Key("arcs")).FillWidth().Height(h).Cursor(ui.CursorCrosshair).Role(ui.RoleImage).Label(label).Focusable()
	var width float32
	if b := box.Bounds(); b.W > 0 {
		width = b.W
	}
	box.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputScroll || width <= 0 {
			return false
		}
		g := s.Gap
		a0, a1 := g.Axis(s.Start), g.Axis(s.End)
		at := a0 + float64(ev.X/width)*(a1-a0)
		factor := math.Pow(1.0015, float64(ev.DY))
		s.setAxis(at-(at-a0)*factor, at+(a1-at)*factor)
		return true
	})
	if dx, _, ok := box.Dragged(); ok && dx != 0 && width > 0 {
		a0, a1 := s.Gap.Axis(s.Start), s.Gap.Axis(s.End)
		shift := -float64(dx) / float64(width) * (a1 - a0)
		s.setAxis(a0+shift, a1+shift)
	}
	if box.DoubleClicked() {
		s.setRange(1, float64(s.Length))
	}
	event := ArcEvent{}
	if px, _, over := box.PointerPosition(); over && width > 0 {
		a0, a1 := s.Gap.Axis(s.Start), s.Gap.Axis(s.End)
		event.Position = int(math.Round(s.Gap.Position(a0 + float64(px/width)*(a1-a0))))
		event.Hover = true
	}
	switch {
	case box.Clicked():
		if event.Hover {
			s.Selected = max(1, min(s.Length, event.Position))
		} else {
			s.Step(0) // Enter or Space: the first position shown
		}
	case box.Shortcut(0, ui.KeyLeft):
		s.Step(-1)
	case box.Shortcut(0, ui.KeyRight):
		s.Step(1)
	}
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, t.Surface, 6)
		track := float32(0)
		if len(s.Features) > 0 {
			track = trackHeight + 4
		}
		below := len(s.B) > 0
		for _, o := range s.Overlay {
			below = below || !o.Up
		}
		base := r.Y + (r.H-track)/2
		if !below {
			base = r.Y + r.H - 22 - track
		}
		g := s.Gap
		a0 := g.Axis(s.Start)
		span := g.Axis(s.End) - a0
		x := func(pos int) float32 { return r.X + float32((g.Axis(float64(pos))-a0)/span)*r.W }
		p.Clip(r, 6, func() {
			if s.RegionEnd > s.RegionStart {
				x0, x1 := x(s.RegionStart), x(s.RegionEnd)
				p.Fill(ui.Rect{X: x0, Y: r.Y, W: x1 - x0, H: r.H}, t.Accent.Alpha(0.08), 0)
			}
			maxUp := base - r.Y - 6
			maxDown := r.Y + r.H - base - 22 - track
			// With Shade, arcs go into five opacity buckets by weight (one
			// path each), from 0.12 for no evidence to 1 for the strongest.
			maxWeight := 0.0
			if s.Shade {
				for _, ws := range [][]float64{s.WeightA, s.WeightB} {
					for _, w := range ws {
						maxWeight = math.Max(maxWeight, w)
					}
				}
			}
			const buckets = 5
			bucket := func(weights []float64, index int) int {
				if maxWeight <= 0 || index >= len(weights) {
					return buckets - 1
				}
				b := int(math.Ceil(weights[index] / maxWeight * (buckets - 1)))
				return max(0, min(buckets-1, b))
			}
			// A alone with a style per arc: one path per style.
			styled := len(s.StylesA) == len(s.A) && len(s.A) > 0 && len(s.B) == 0 && s.Mono.A == 0
			drawStyled := func() {
				paths := map[ArcStyle]*ui.Path{}
				var order []ArcStyle
				for index, pr := range s.A {
					if !s.visible(pr.I, pr.J) {
						continue
					}
					style := s.StylesA[index]
					path := paths[style]
					if path == nil {
						path = &ui.Path{}
						paths[style] = path
						order = append(order, style)
					}
					x0, x1 := x(min(pr.I, pr.J)), x(max(pr.I, pr.J)) // a pair given I > J still arcs up
					height := min((x1-x0)/2, maxUp)
					k := float32(0.5523)
					mid, rx := (x0+x1)/2, (x1-x0)/2
					path.MoveTo(x0, base).
						CubeTo(x0, base-height*k, mid-rx*k, base-height, mid, base-height).
						CubeTo(mid+rx*k, base-height, x1, base-height*k, x1, base)
				}
				for _, style := range order {
					p.StrokePath(paths[style], max(style.Width, 0.5), style.Color)
				}
			}
			draw := func(pairs []Pair, up bool, color ui.Color, onlyShared bool) {
				weights := s.WeightA
				if !up {
					weights = s.WeightB
				}
				var paths [buckets]ui.Path
				var counts [buckets]int
				for index, pr := range pairs {
					if !s.visible(pr.I, pr.J) {
						continue
					}
					if s.shared[pr] != onlyShared {
						continue
					}
					x0, x1 := x(min(pr.I, pr.J)), x(max(pr.I, pr.J)) // a pair given I > J still arcs up
					height := (x1 - x0) / 2
					limit := maxUp
					if !up {
						limit = maxDown
					}
					if height > limit {
						height = limit
					}
					dir := float32(-1)
					if !up {
						dir = 1
					}
					// A half ellipse from two cubic curves.
					k := float32(0.5523)
					mid := (x0 + x1) / 2
					rx := (x1 - x0) / 2
					b := bucket(weights, index)
					paths[b].MoveTo(x0, base).
						CubeTo(x0, base+dir*height*k, mid-rx*k, base+dir*height, mid, base+dir*height).
						CubeTo(mid+rx*k, base+dir*height, x1, base+dir*height*k, x1, base)
					counts[b]++
				}
				for b := range buckets {
					if counts[b] > 0 {
						alpha := float32(0.12 + 0.88*float64(b)/(buckets-1))
						if maxWeight <= 0 {
							alpha = 1
						}
						p.StrokePath(&paths[b], 1, color.Alpha(alpha))
					}
				}
			}
			if s.Mono.A > 0 {
				colorA, colorB, colorShared = s.Mono, s.Mono, s.Mono
			}
			if styled {
				drawStyled()
			} else {
				draw(s.A, true, colorA, false)
			}
			draw(s.B, false, colorB, false)
			draw(s.A, true, colorShared, true)
			draw(s.B, false, colorShared, true)
			// Overlay arcs on top, one path per colour, width and side.
			type overlayKey struct {
				color ui.Color
				width float32
				up    bool
			}
			overlay := map[overlayKey]*ui.Path{}
			var order []overlayKey
			for _, o := range s.Overlay {
				if !s.visible(o.I, o.J) {
					continue
				}
				key := overlayKey{o.Color, max(o.Width, 1), o.Up}
				path := overlay[key]
				if path == nil {
					path = &ui.Path{}
					overlay[key] = path
					order = append(order, key)
				}
				x0, x1 := x(min(o.I, o.J)), x(max(o.I, o.J))
				limit, dir := maxUp, float32(-1)
				if !o.Up {
					limit, dir = maxDown, 1
				}
				// height grows with the span relative to the visible width,
				// so long-range interactions stay arcs instead of flat caps
				height := limit * min(1, 0.06+0.94*(x1-x0)/r.W)
				k := float32(0.5523)
				mid, rx := (x0+x1)/2, (x1-x0)/2
				path.MoveTo(x0, base).
					CubeTo(x0, base+dir*height*k, mid-rx*k, base+dir*height, mid, base+dir*height).
					CubeTo(mid+rx*k, base+dir*height, x1, base+dir*height*k, x1, base)
			}
			for _, key := range order {
				p.StrokePath(overlay[key], key.width, key.color)
			}
			if lo, hi := g.Bounds(); g.on() && float64(lo) >= s.Start && float64(hi) <= s.End {
				// the chain break, as the web's "A | B"
				nick := (x(lo) + x(hi)) / 2
				half := min(9, max(3, (x(hi)-x(lo))*0.3))
				p.Line(r.X, base, nick-half, base, 1, t.Border)
				p.Line(nick+half, base, r.X+r.W, base, 1, t.Border)
				p.Text(nick-13, base+14, g.Label(), 10, t.TextMuted)
			} else {
				p.Line(r.X, base, r.X+r.W, base, 1, t.Border)
			}
			if pos := float64(s.Selected); s.Selected > 0 && pos >= s.Start && pos <= s.End {
				sx := x(s.Selected)
				p.Line(sx, r.Y+4, sx, r.Y+r.H-22-track, 1, t.Accent.Alpha(0.7))
				p.Fill(ui.Rect{X: sx - 3, Y: base - 3, W: 6, H: 6}, t.Accent, 3)
			}
			// Gene track: alternating blocks with their names where they fit.
			if track > 0 {
				y := r.Y + r.H - 22 - trackHeight
				for index, f := range s.Features {
					if float64(f.End) < s.Start || float64(f.Start) > s.End {
						continue
					}
					x0, x1 := x(f.Start), x(f.End+1)
					fill := t.TextMuted.Alpha(0.22)
					if index%2 == 1 {
						fill = t.TextMuted.Alpha(0.38)
					}
					p.Fill(ui.Rect{X: x0, Y: y, W: max(x1-x0, 1), H: trackHeight}, fill, 0)
					if width := float32(len(f.Name)) * 6; f.Name != "" && x1-x0 > width+4 {
						p.Text((x0+x1)/2-width/2, y+trackHeight-4, f.Name, 10, t.Text)
					}
				}
			}
		})
		// Ticks along the bottom.
		for _, pos := range g.Ticks(s.Start, s.End, 8) {
			px := x(int(pos))
			if lo, hi := g.Bounds(); g.on() && math.Abs(float64(px-(x(lo)+x(hi))/2)) < 24 {
				continue // the chain break's label is there
			}
			p.Line(px, r.Y+r.H-18, px, r.Y+r.H-14, 1, t.Border)
			label := fmt.Sprint(int(pos))
			p.Text(px-float32(len(label))*3, r.Y+r.H-3, label, 10, t.TextMuted)
		}
	})
	return event
}

func (s *Arcs) setRange(start, end float64) {
	s.setAxis(s.Gap.Axis(start), s.Gap.Axis(end))
}

// setAxis shows axis values start to end (see Gap.Axis).
func (s *Arcs) setAxis(start, end float64) {
	length := math.Max(s.Gap.Axis(float64(s.Length)), 2)
	// at least 20 nt shown, but never more than the RNA
	span := math.Min(math.Max(20, end-start), length-1)
	if start+span > length {
		start = length - span
	}
	if start < 1 {
		start = 1
	}
	s.Start, s.End = s.Gap.Position(start), s.Gap.Position(start+span)
}

// visible: an arc between positions i and j (either order) reaches into
// the range shown.
func (s *Arcs) visible(i, j int) bool {
	return float64(max(i, j)) >= s.Start && float64(min(i, j)) <= s.End
}
