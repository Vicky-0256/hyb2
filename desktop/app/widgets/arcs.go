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
	// Start/End are the visible range (1-based, inclusive).
	Start, End float64
	// Region, when End > Start, shades a region (e.g. a folded window).
	RegionStart, RegionEnd int

	shared map[Pair]bool
	keyed  struct{ a, b int }
}

// Set replaces the structures and shows the whole gene.
func (s *Arcs) Set(a, b []Pair, length int) {
	s.A, s.B, s.Length = a, b, length
	s.Start, s.End = 1, float64(length)
	s.shared = nil
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
	box := ui.Box(c.Key("arcs")).FillWidth().Height(h).Cursor(ui.CursorCrosshair).Role(ui.RoleImage).Label(label)
	var width float32
	if b := box.Bounds(); b.W > 0 {
		width = b.W
	}
	box.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputScroll || width <= 0 {
			return false
		}
		at := s.Start + float64(ev.X/width)*(s.End-s.Start)
		factor := math.Pow(1.0015, float64(ev.DY))
		s.setRange(at-(at-s.Start)*factor, at+(s.End-at)*factor)
		return true
	})
	if dx, _, ok := box.Dragged(); ok && dx != 0 && width > 0 {
		shift := -float64(dx) / float64(width) * (s.End - s.Start)
		s.setRange(s.Start+shift, s.End+shift)
	}
	if box.DoubleClicked() {
		s.setRange(1, float64(s.Length))
	}
	event := ArcEvent{}
	if px, _, over := box.PointerPosition(); over && width > 0 {
		event.Position = int(math.Round(s.Start + float64(px/width)*(s.End-s.Start)))
		event.Hover = true
	}
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, t.Surface, 6)
		base := r.Y + r.H/2
		if len(s.B) == 0 {
			base = r.Y + r.H - 22
		}
		span := s.End - s.Start
		x := func(pos int) float32 { return r.X + float32((float64(pos)-s.Start)/span)*r.W }
		p.Clip(r, 6, func() {
			if s.RegionEnd > s.RegionStart {
				x0, x1 := x(s.RegionStart), x(s.RegionEnd)
				p.Fill(ui.Rect{X: x0, Y: r.Y, W: x1 - x0, H: r.H}, t.Accent.Alpha(0.08), 0)
			}
			maxUp := base - r.Y - 6
			maxDown := r.Y + r.H - base - 22
			draw := func(pairs []Pair, up bool, color ui.Color, onlyShared bool) {
				var path ui.Path
				n := 0
				for _, pr := range pairs {
					if float64(pr.J) < s.Start || float64(pr.I) > s.End {
						continue
					}
					if s.shared[pr] != onlyShared {
						continue
					}
					x0, x1 := x(pr.I), x(pr.J)
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
					path.MoveTo(x0, base).
						CubeTo(x0, base+dir*height*k, mid-rx*k, base+dir*height, mid, base+dir*height).
						CubeTo(mid+rx*k, base+dir*height, x1, base+dir*height*k, x1, base)
					n++
				}
				if n > 0 {
					p.StrokePath(&path, 1, color)
				}
			}
			draw(s.A, true, colorA, false)
			draw(s.B, false, colorB, false)
			draw(s.A, true, colorShared, true)
			draw(s.B, false, colorShared, true)
			p.Line(r.X, base, r.X+r.W, base, 1, t.Border)
		})
		// Ticks along the bottom.
		step := math.Pow(10, math.Floor(math.Log10(span/5)))
		for _, m := range []float64{1, 2, 5, 10} {
			if span/(step*m) <= 8 {
				step *= m
				break
			}
		}
		for pos := math.Ceil(s.Start/step) * step; pos <= s.End; pos += step {
			px := x(int(pos))
			p.Line(px, r.Y+r.H-18, px, r.Y+r.H-14, 1, t.Border)
			label := fmt.Sprint(int(pos))
			p.Text(px-float32(len(label))*3, r.Y+r.H-3, label, 10, t.TextMuted)
		}
	})
	return event
}

func (s *Arcs) setRange(start, end float64) {
	length := math.Max(float64(s.Length), 2)
	span := math.Max(20, math.Min(end-start, length-1))
	if start < 1 {
		start = 1
	}
	if start+span > length {
		start = length - span
	}
	s.Start, s.End = start, start+span
}
