package widgets

import (
	"image"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func snapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("HYB2_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestHeatmapPickZoomAndReset(t *testing.T) {
	h := NewHeatmap()
	rng := rand.New(rand.NewSource(1))
	var cells []Cell
	for i := 0; i < 200000; i++ {
		x := float64(rng.Intn(1000) * 10)
		y := float64(rng.Intn(1000) * 10)
		cells = append(cells, Cell{X: x, Y: y, W: 10, H: 10, Color: ui.RGBA(214, 69, 65, 0.2+0.8*rng.Float32())})
	}
	cells = append(cells, Cell{X: 5000, Y: 2000, W: 400, H: 400, Color: ui.RGB(43, 108, 196)})
	h.SetCells(cells, 10000, 10000)
	var last HeatEvent
	view := func(c *ui.Context) {
		ui.Column(c).Padding(10).Children(func() { last = h.Show(c, 400, "test map") })
	}
	tt := ui.NewTester(view, 500, 500)
	snapshot(t, tt, "heatmap")
	// Click the big blue cell: plot origin is (10+44, 10), 400 px for 10 kb.
	tt.ClickAt(10+44+(5200.0/10000)*400, 10+(2200.0/10000)*400)
	tt.Frame()
	if h.Picked != len(cells)-1 {
		t.Fatalf("picked %d, last event %+v", h.Picked, last)
	}
	h.SetView(Window{4000, 7000, 1000, 4000})
	tt.Frame()
	if !h.Zoomed() {
		t.Fatal("not zoomed")
	}
	snapshot(t, tt, "heatmap-zoom")
	tt.Press(10+44+200, 10+200)
	tt.Release(10+44+200, 10+200)
	h.ResetZoom()
	if h.Zoomed() {
		t.Fatal("reset failed")
	}
}

func TestMinCellIsHitWhereItShows(t *testing.T) {
	h := NewHeatmap()
	h.SetCells([]Cell{{X: 5000, Y: 5000, W: 10, H: 10}}, 10000, 10000)
	// 10 nt of 10,000 on 400 DIPs is 0.4 DIP; drawn 3 DIPs wide it spans
	// 37.5 nt around the cell, half of it on each side.
	pad := 1.5 * 10000 / 400.0
	if h.hit(5005+pad*0.9, 5005, pad, pad) != 0 || h.hit(5005+pad*1.1, 5005, pad, pad) != -1 {
		t.Fatal("padded hit")
	}
	if h.hit(5011, 5005, 0, 0) != -1 || h.hit(5009, 5005, 0, 0) != 0 {
		t.Fatal("unpadded hit")
	}
}

// Cells are written where they are, later ones on top (blended), and a
// zoomed window still shows a large cell that starts outside it.
func TestHeatmapRasterPlacesCells(t *testing.T) {
	red, blue := ui.RGB(255, 0, 0), ui.RGBA(0, 0, 255, 0.5)
	cells := []Cell{
		{X: 0, Y: 0, W: 2000, H: 2000, Color: red}, // starts far left of the zoom below
		{X: 1500, Y: 1500, W: 100, H: 100, Color: blue},
		{X: 9000, Y: 9000, W: 10, H: 10, Color: red},
	}
	ix := newCellIndex(cells, 10000, 10000, 1)
	at := func(img *image.RGBA, x, y int) [4]uint8 {
		i := img.PixOffset(x, y)
		return [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
	}
	whole := image.NewRGBA(image.Rect(0, 0, 100, 100))
	ix.raster(whole, cells, Window{0, 10000, 0, 10000}, 0)
	if got := at(whole, 5, 5); got != [4]uint8{255, 0, 0, 255} {
		t.Fatalf("big cell %v", got)
	}
	if got := at(whole, 15, 15); got != [4]uint8{127, 0, 128, 255} {
		t.Fatalf("blue over red %v", got)
	}
	if got := at(whole, 90, 90); got[3] != 255 {
		t.Fatalf("a cell smaller than a pixel is drawn one pixel wide: %v", got)
	}
	if got := at(whole, 50, 50); got[3] != 0 {
		t.Fatalf("empty space painted %v", got)
	}
	zoom := image.NewRGBA(image.Rect(0, 0, 100, 100))
	ix.raster(zoom, cells, Window{1900, 2100, 1900, 2100}, 0)
	if got := at(zoom, 10, 10); got != [4]uint8{255, 0, 0, 255} {
		t.Fatalf("zoomed: big cell from outside the window %v", got)
	}
	if got := at(zoom, 90, 90); got[3] != 0 {
		t.Fatalf("zoomed: past the big cell %v", got)
	}
}
