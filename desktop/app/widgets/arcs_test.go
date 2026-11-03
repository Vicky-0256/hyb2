package widgets

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestArcsSharedAndRender(t *testing.T) {
	var a, b []Pair
	for i := 0; i < 40; i++ {
		a = append(a, Pair{10 + i*20, 400 + i*15})
		if i%2 == 0 {
			b = append(b, Pair{10 + i*20, 400 + i*15})
		} else {
			b = append(b, Pair{30 + i*20, 200 + i*10})
		}
	}
	s := &Arcs{}
	s.Set(a, b, 1200)
	if s.Shared() != 20 {
		t.Fatalf("shared %d", s.Shared())
	}
	view := func(c *ui.Context) {
		th := c.Theme()
		ui.Column(c).Padding(10).Width(700).Children(func() {
			s.Show(c, 280, ui.RGB(214, 69, 65), ui.RGB(43, 108, 196), th.Text, "arcs")
		})
	}
	tt := ui.NewTester(view, 720, 320)
	snapshot(t, tt, "arcs")
	s.setRange(100, 500)
	tt.Frame()
	if s.Start != 100 || s.End != 500 {
		t.Fatalf("range %v-%v", s.Start, s.End)
	}
	snapshot(t, tt, "arcs-zoom")
}

// Strand B (100-200) drawn before strand A (500-600): the gap still
// collapses 201-499, stepping crosses it, and a pair given I > J still
// draws and is not culled.
func TestArcsGapStrandBFirst(t *testing.T) {
	s := &Arcs{Gap: Gap{From: 500, To: 200}}
	s.Set([]Pair{{148, 529}, {529, 148}}, nil, 600)
	if lo, hi := s.Gap.Bounds(); lo != 200 || hi != 500 || !s.Gap.on() || s.Gap.Label() != "B | A" || (Gap{From: 81, To: 1052}).Label() != "A | B" {
		t.Fatalf("bounds %d-%d", lo, hi)
	}
	if s.Gap.Axis(500) != 201 || s.Gap.Position(201) != 500 || s.Gap.Axis(600) != 301 {
		t.Fatalf("axis %v", s.Gap.Axis(500))
	}
	s.setRange(100, 600)
	if s.Start != 100 || s.End != 600 {
		t.Fatalf("range %v-%v", s.Start, s.End)
	}
	s.Selected = 200
	s.Step(1)
	if s.Selected != 500 {
		t.Fatalf("stepped into the gap: %d", s.Selected)
	}
	s.Step(-1)
	if s.Selected != 200 {
		t.Fatalf("stepped back into the gap: %d", s.Selected)
	}
	s.Start, s.End = 500, 600
	if !s.visible(529, 148) || !s.visible(148, 529) || s.visible(100, 140) {
		t.Fatal("visible")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		th := c.Theme()
		s.Show(c, 280, ui.RGB(214, 69, 65), ui.RGB(43, 108, 196), th.Text, "arcs")
	}, 720, 320)
	tt.Frame()
}

func TestArcsInspectorStep(t *testing.T) {
	s := &Arcs{}
	s.Set(nil, nil, 1000)
	s.setRange(100, 200)
	s.Step(1) // nothing selected: the first position shown
	if s.Selected != 100 {
		t.Fatalf("selected %d", s.Selected)
	}
	s.Selected = 200
	s.Step(1) // past the view: pans to keep it shown
	if s.Selected != 201 || s.Start > 201 || s.End < 201 {
		t.Fatalf("selected %d in %v-%v", s.Selected, s.Start, s.End)
	}
	s.Selected = 1
	s.Step(-1)
	if s.Selected != 1 {
		t.Fatalf("below 1: %d", s.Selected)
	}
	// a shorter structure drops a selection beyond it
	s.Selected = 80
	s.Set(nil, nil, 50)
	if s.Selected != 0 {
		t.Fatalf("kept %d", s.Selected)
	}
}
