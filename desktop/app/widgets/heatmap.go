// Package widgets holds the drawing widgets several pages share: the
// contact heatmap (Explore, Compare) and the arc diagram (Structure).
package widgets

import (
	"fmt"
	"image"
	"math"

	"github.com/egoist/mygo/ui"
)

// Cell is one square of a contact map in gene coordinates: arm 1 [X, X+W)
// on the horizontal axis, arm 2 [Y, Y+H) on the vertical one.
type Cell struct {
	X, Y, W, H float64
	Color      ui.Color
}

// Window is a range of gene coordinates on both axes.
type Window struct{ X0, X1, Y0, Y1 float64 }

// Heatmap is the state of one contact-map view: its cells, the zoom window
// and the cell picked. Keep it in the page's state; Show builds it.
//
// Cells are rasterized into a bitmap once per change of data, window or
// size, so a map of hundreds of thousands of cells costs one image a frame.
// They are written as pixels, not painted as shapes: at a million cells a
// painted rectangle each made every zoom or drag frame take 200 ms.
type Heatmap struct {
	cells   []Cell
	version int
	lenX    float64
	lenY    float64
	view    Window

	index  cellIndex // of cells, for the version drawn
	bitmap *ui.Bitmap
	pix    [2]*image.RGBA // alternate: Bitmap.Update keeps the last one shown
	flip   int
	drawn  struct {
		version int
		view    Window
		w, h    int
		dark    bool
	}
	// Picked is the index of the highlighted cell, -1 for none.
	Picked int
	// Highlight, when W > 0, outlines a region (e.g. a merged interaction).
	Highlight Cell
	// MinCell is the smallest side (DIPs) a cell is drawn with, centred on
	// it; 0 draws cells at their size (at least one pixel).
	MinCell float32
	// Background, when not transparent, replaces the theme surface under
	// the cells (e.g. white for hyb2's similarity heatmap).
	Background ui.Color

	dragging bool
	lastX    float32
	lastY    float32
}

// NewHeatmap makes an empty map.
func NewHeatmap() *Heatmap { return &Heatmap{Picked: -1} }

// SetCells replaces the data and resets the zoom to the whole lengths.
func (h *Heatmap) SetCells(cells []Cell, lengthX, lengthY float64) {
	h.cells = cells
	h.version++
	h.lenX, h.lenY = math.Max(lengthX, 1), math.Max(lengthY, 1)
	h.view = Window{0, h.lenX, 0, h.lenY}
	h.Picked = -1
}

// Cells returns the data shown.
func (h *Heatmap) Cells() []Cell { return h.cells }

// View returns the zoom window; SetView changes it (clamped).
func (h *Heatmap) View() Window { return h.view }

func (h *Heatmap) SetView(v Window) {
	h.view = h.clamp(v)
}

// ResetZoom shows the whole map.
func (h *Heatmap) ResetZoom() { h.view = Window{0, h.lenX, 0, h.lenY} }

// Zoomed reports whether a part of the map is shown.
func (h *Heatmap) Zoomed() bool {
	return h.view != (Window{0, h.lenX, 0, h.lenY})
}

func (h *Heatmap) clamp(v Window) Window {
	minSpan := 20.0
	clampAxis := func(a0, a1, length float64) (float64, float64) {
		span := math.Min(math.Max(minSpan, a1-a0), length) // never wider than the map
		if a0 < 0 {
			a0 = 0
		}
		if a0+span > length {
			a0 = math.Max(0, length-span)
		}
		return a0, a0 + span
	}
	v.X0, v.X1 = clampAxis(v.X0, v.X1, h.lenX)
	v.Y0, v.Y1 = clampAxis(v.Y0, v.Y1, h.lenY)
	return v
}

// HeatEvent reports what the user did in a frame.
type HeatEvent struct {
	// Clicked is the index of the cell clicked, -1 for none.
	Clicked int
	// X, Y are the gene coordinates under the pointer when Hover is true.
	X, Y  float64
	Hover bool
}

const axisPad = 44

// Show builds the map as a square of side size (DIPs), with axes. Wheel
// zooms around the pointer, dragging pans, a double click resets the zoom,
// and a click picks the cell under the pointer.
func (h *Heatmap) Show(c *ui.Context, size float32, label string) HeatEvent {
	t := c.Theme()
	event := HeatEvent{Clicked: -1}
	box := ui.Box(c.Key("heatmap")).Size(size+axisPad, size+axisPad).Cursor(ui.CursorCrosshair).
		Role(ui.RoleImage).Label(label)
	plot := ui.Rect{X: axisPad, Y: 0, W: size, H: size}

	box.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputScroll:
			if !plot.Contains(ev.X, ev.Y) {
				return false
			}
			factor := math.Pow(1.0015, float64(ev.DY))
			h.zoomAt(plot, ev.X, ev.Y, factor)
			return true
		}
		return false
	})
	if dx, dy, ok := box.Dragged(); ok && (dx != 0 || dy != 0) {
		h.dragging = true
		v := h.view
		sx := (v.X1 - v.X0) / float64(plot.W)
		sy := (v.Y1 - v.Y0) / float64(plot.H)
		h.SetView(Window{v.X0 - float64(dx)*sx, v.X1 - float64(dx)*sx, v.Y0 - float64(dy)*sy, v.Y1 - float64(dy)*sy})
	}
	if box.DoubleClicked() {
		h.ResetZoom()
	} else if box.Clicked() {
		if h.dragging {
			h.dragging = false
		} else if px, py, over := box.PointerPosition(); over && plot.Contains(px, py) {
			gx, gy := h.toGene(plot, px, py)
			// A cell drawn larger than it is (MinCell) is hit where it shows.
			padX := float64(h.MinCell/2) * (h.view.X1 - h.view.X0) / float64(plot.W)
			padY := float64(h.MinCell/2) * (h.view.Y1 - h.view.Y0) / float64(plot.H)
			if i := h.hit(gx, gy, padX, padY); i >= 0 {
				h.Picked = i
				event.Clicked = i
			}
		}
	}
	if !box.Pressed() {
		h.dragging = false
	}
	if px, py, over := box.PointerPosition(); over && plot.Contains(px, py) {
		event.X, event.Y = h.toGene(plot, px, py)
		event.Hover = true
	}

	dark := t.Background.R < 128
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		area := ui.Rect{X: r.X + plot.X, Y: r.Y + plot.Y, W: plot.W, H: plot.H}
		background := t.Surface
		if h.Background.A > 0 {
			background = h.Background
		}
		p.Fill(area, background, 2)
		scale := p.Scale()
		w, hgt := int(math.Ceil(float64(area.W*scale))), int(math.Ceil(float64(area.H*scale)))
		if h.drawn.w != w || h.drawn.h != hgt {
			h.pix = [2]*image.RGBA{}
			h.drawn.version = -1
		}
		if h.bitmap == nil || h.drawn.version != h.version || h.drawn.view != h.view || h.drawn.dark != dark {
			h.drawn.version, h.drawn.view, h.drawn.w, h.drawn.h, h.drawn.dark = h.version, h.view, w, hgt, dark
			h.flip ^= 1
			img := h.pix[h.flip]
			if img == nil {
				img = image.NewRGBA(image.Rect(0, 0, w, hgt))
				h.pix[h.flip] = img
			} else {
				clear(img.Pix)
			}
			if h.index.version != h.version {
				h.index = newCellIndex(h.cells, h.lenX, h.lenY, h.version)
			}
			h.index.raster(img, h.cells, h.view, float64(h.MinCell*scale))
			if h.bitmap == nil {
				h.bitmap = ui.NewBitmap(img)
			} else {
				h.bitmap.Update(img)
			}
		}
		p.Image(h.bitmap, area, ui.FillBox)
		p.Stroke(area, t.Border, 2, 1)
		h.drawMarks(p, area, t)
		h.drawAxes(p, r, area, t)
	})
	return event
}

// tileGrid is the side of cellIndex's grid of tiles.
const tileGrid = 128

// cellIndex buckets cells into a grid of tiles over the map: a zoomed frame
// tests a byte pair per cell (is its tile shown?) instead of the cell, and
// still writes the cells in their order, which decides what is on top.
type cellIndex struct {
	version      int
	tile         []uint16 // per cell
	tileW, tileH float64
	maxW, maxH   float64 // largest cell, how far one reaches past its tile
}

func newCellIndex(cells []Cell, lenX, lenY float64, version int) cellIndex {
	ix := cellIndex{version: version, tile: make([]uint16, len(cells)), tileW: lenX / tileGrid, tileH: lenY / tileGrid}
	for i, cell := range cells {
		ix.maxW, ix.maxH = math.Max(ix.maxW, cell.W), math.Max(ix.maxH, cell.H)
		ix.tile[i] = uint16(ix.row(cell.Y)*tileGrid + ix.col(cell.X))
	}
	return ix
}

func (ix *cellIndex) col(x float64) int { return min(max(int(x/ix.tileW), 0), tileGrid-1) }
func (ix *cellIndex) row(y float64) int { return min(max(int(y/ix.tileH), 0), tileGrid-1) }

// raster writes the cells shown in window v over img (rasterCells).
func (ix *cellIndex) raster(img *image.RGBA, cells []Cell, v Window, minCell float64) {
	// A cell drawn minCell pixels wide reaches past its own size.
	padX := minCell * (v.X1 - v.X0) / float64(img.Rect.Dx())
	padY := minCell * (v.Y1 - v.Y0) / float64(img.Rect.Dy())
	c0, c1 := ix.col(v.X0-ix.maxW-padX), ix.col(v.X1+padX)
	r0, r1 := ix.row(v.Y0-ix.maxH-padY), ix.row(v.Y1+padY)
	if (c1-c0+1)*(r1-r0+1) > tileGrid*tileGrid/2 {
		rasterCells(img, cells, v, minCell, nil, nil)
		return
	}
	shown := new([tileGrid * tileGrid]bool)
	for r := r0; r <= r1; r++ {
		for c := c0; c <= c1; c++ {
			shown[r*tileGrid+c] = true
		}
	}
	rasterCells(img, cells, v, minCell, ix.tile, shown)
}

// rasterCells writes the cells inside window v (with tile, only those in a
// shown tile) over img, in order (later on top, blended source-over), each
// at least minCell pixels and one pixel wide, centred on where it is.
func rasterCells(img *image.RGBA, cells []Cell, v Window, minCell float64, tile []uint16, shown *[tileGrid * tileGrid]bool) {
	w, hgt := img.Rect.Dx(), img.Rect.Dy()
	sx := float64(w) / (v.X1 - v.X0)
	sy := float64(hgt) / (v.Y1 - v.Y0)
	least := math.Max(minCell, 1)
	for i := range cells {
		if tile != nil && !shown[tile[i]] {
			continue
		}
		cell := &cells[i]
		if cell.X+cell.W < v.X0 || cell.X > v.X1 || cell.Y+cell.H < v.Y0 || cell.Y > v.Y1 || cell.Color.A == 0 {
			continue
		}
		cw, ch := math.Max(cell.W*sx, least), math.Max(cell.H*sy, least)
		x := (cell.X-v.X0)*sx - (cw-cell.W*sx)/2
		y := (cell.Y-v.Y0)*sy - (ch-cell.H*sy)/2
		x0, y0 := int(math.Round(x)), int(math.Round(y))
		x1, y1 := max(int(math.Round(x+cw)), x0+1), max(int(math.Round(y+ch)), y0+1)
		x0, y0, x1, y1 = max(x0, 0), max(y0, 0), min(x1, w), min(y1, hgt)
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		// image.RGBA holds premultiplied colour
		a := uint32(cell.Color.A)
		r, g, b := uint32(cell.Color.R)*a/255, uint32(cell.Color.G)*a/255, uint32(cell.Color.B)*a/255
		rest := 255 - a
		for py := y0; py < y1; py++ {
			row := img.Pix[py*img.Stride+x0*4 : py*img.Stride+x1*4]
			if rest == 0 {
				for i := 0; i < len(row); i += 4 {
					row[i], row[i+1], row[i+2], row[i+3] = uint8(r), uint8(g), uint8(b), 255
				}
				continue
			}
			for i := 0; i < len(row); i += 4 {
				row[i] = uint8(r + uint32(row[i])*rest/255)
				row[i+1] = uint8(g + uint32(row[i+1])*rest/255)
				row[i+2] = uint8(b + uint32(row[i+2])*rest/255)
				row[i+3] = uint8(a + uint32(row[i+3])*rest/255)
			}
		}
	}
}

func (h *Heatmap) toGene(plot ui.Rect, px, py float32) (float64, float64) {
	v := h.view
	gx := v.X0 + float64((px-plot.X)/plot.W)*(v.X1-v.X0)
	gy := v.Y0 + float64((py-plot.Y)/plot.H)*(v.Y1-v.Y0)
	return gx, gy
}

func (h *Heatmap) zoomAt(plot ui.Rect, px, py float32, factor float64) {
	gx, gy := h.toGene(plot, px, py)
	v := h.view
	h.SetView(Window{gx - (gx-v.X0)*factor, gx + (v.X1-gx)*factor, gy - (gy-v.Y0)*factor, gy + (v.Y1-gy)*factor})
}

// hit returns the last cell covering (x, y), widened by pad on each side
// up to its drawn size: drawn last, so on top.
func (h *Heatmap) hit(x, y, padX, padY float64) int {
	for i := len(h.cells) - 1; i >= 0; i-- {
		cell := h.cells[i]
		px, py := math.Max(padX-cell.W/2, 0), math.Max(padY-cell.H/2, 0)
		if x >= cell.X-px && x < cell.X+cell.W+px && y >= cell.Y-py && y < cell.Y+cell.H+py {
			return i
		}
	}
	return -1
}

func (h *Heatmap) mark(area ui.Rect, cell Cell, pad float64) (ui.Rect, bool) {
	v := h.view
	sx := float64(area.W) / (v.X1 - v.X0)
	sy := float64(area.H) / (v.Y1 - v.Y0)
	x0, x1 := cell.X-pad, cell.X+cell.W+pad
	y0, y1 := cell.Y-pad, cell.Y+cell.H+pad
	if x1 < v.X0 || x0 > v.X1 || y1 < v.Y0 || y0 > v.Y1 {
		return ui.Rect{}, false
	}
	r := ui.Rect{X: area.X + float32((x0-v.X0)*sx), Y: area.Y + float32((y0-v.Y0)*sy),
		W: float32(math.Max((x1-x0)*sx, 4)), H: float32(math.Max((y1-y0)*sy, 4))}
	return r, true
}

func (h *Heatmap) drawMarks(p *ui.Painter, area ui.Rect, t *ui.Theme) {
	p.Clip(area, 2, func() {
		if h.Highlight.W > 0 {
			if r, ok := h.mark(area, h.Highlight, 0); ok {
				p.Stroke(r, t.Text, 2, 2)
			}
		}
		if h.Picked >= 0 && h.Picked < len(h.cells) {
			if r, ok := h.mark(area, h.cells[h.Picked], 0); ok {
				r = ui.Rect{X: r.X - 3, Y: r.Y - 3, W: r.W + 6, H: r.H + 6}
				p.Stroke(r, t.Text, 2, 2)
			}
		}
	})
}

// drawAxes labels both axes with about five ticks in gene coordinates.
func (h *Heatmap) drawAxes(p *ui.Painter, r, area ui.Rect, t *ui.Theme) {
	v := h.view
	ticks := func(a0, a1 float64) []float64 {
		span := a1 - a0
		step := math.Pow(10, math.Floor(math.Log10(span/5)))
		for _, m := range []float64{1, 2, 5, 10} {
			if span/(step*m) <= 6 {
				step *= m
				break
			}
		}
		var out []float64
		for x := math.Ceil(a0/step) * step; x <= a1; x += step {
			out = append(out, x)
		}
		return out
	}
	for _, x := range ticks(v.X0, v.X1) {
		px := area.X + float32((x-v.X0)/(v.X1-v.X0))*area.W
		p.Line(px, area.Y+area.H, px, area.Y+area.H+4, 1, t.Border)
		label := fmt.Sprint(int(x))
		p.Text(px-float32(len(label))*3, area.Y+area.H+16, label, 10, t.TextMuted)
	}
	for _, y := range ticks(v.Y0, v.Y1) {
		py := area.Y + float32((y-v.Y0)/(v.Y1-v.Y0))*area.H
		p.Line(area.X-4, py, area.X, py, 1, t.Border)
		label := fmt.Sprint(int(y))
		p.Text(r.X+2, py+3, label, 10, t.TextMuted)
	}
}
