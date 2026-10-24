package widgets

import "testing"

// RNAs shorter than the 20-nt minimum zoom show whole, not past their ends.
func TestArcsShortRNARange(t *testing.T) {
	s := &Arcs{}
	s.Set(nil, nil, 10)
	s.setRange(1, 10)
	if s.Start != 1 || s.End != 10 {
		t.Fatalf("range %v..%v for a 10-nt RNA", s.Start, s.End)
	}
	s.Set(nil, nil, 100)
	s.setRange(90, 95) // a 5-nt zoom widens to 20, inside the RNA
	if s.End-s.Start != 20 || s.End > 100 {
		t.Fatalf("range %v..%v", s.Start, s.End)
	}
}

func TestHeatmapShortMapView(t *testing.T) {
	h := NewHeatmap()
	h.SetCells(nil, 8, 8)
	h.SetView(h.View())
	if v := h.View(); v.X1 > 8 || v.Y1 > 8 || h.Zoomed() {
		t.Fatalf("view %+v zoomed=%v for an 8-nt map", v, h.Zoomed())
	}
}
