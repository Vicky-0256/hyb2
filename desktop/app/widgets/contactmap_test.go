package widgets

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Input is handled after Show returns, so a click's change is reported by
// the next Show, once.
func TestContactMapReportsChangesOnTheNextShow(t *testing.T) {
	m := NewContactMap()
	var cells []Cell
	for x := 0; x < 100; x += 10 {
		for y := 0; y < 100; y += 10 {
			cells = append(cells, Cell{X: float64(x), Y: float64(y), W: 10, H: 10, Color: ui.RGB(200, 0, 0)})
		}
	}
	m.SetCells(cells, 10, BinView{XStart: 0, XEnd: 90, YStart: 0, YEnd: 90})
	var events []ContactEvent
	tt := ui.NewTester(func(c *ui.Context) { events = append(events, m.Show(c, 400, 400, "map")) }, 400, 400)
	tt.Frame()
	if events[len(events)-1].Changed {
		t.Fatal("changed before any input")
	}
	g := m.Geometry(400, 400)
	tt.ClickAt(g.Left+g.PlotWidth/2, g.Top+g.PlotHeight/2)
	tt.Frame()
	if m.Selection == nil {
		t.Fatal("the click selected nothing")
	}
	changed := 0
	for _, ev := range events {
		if ev.Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("%d frames reported the change", changed)
	}
	tt.Frame()
	if events[len(events)-1].Changed {
		t.Fatal("the change was reported again")
	}
}
