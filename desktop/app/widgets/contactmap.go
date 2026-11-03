package widgets

import (
	"image"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// ContactMap is the Explore page's contact map, drawn and driven as the web
// page's (web/analysis-ui.js drawContactStageCanvas): one nucleotide is the
// same length on both axes, the view is a range of bins, a drag zooms to the
// bins it covers, a click selects a cell and zooms one step, the wheel
// zooms, a double click returns to the full extent, and the arrow keys move
// the selection. A colour bar sits over the top right of the plot.
//
// Keep it in the page's state; Show builds it, Paint draws it into an
// export.
type ContactMap struct {
	bin     int
	cells   []Cell // gene coordinates, one bin square each
	version int
	base    BinView
	// view is the zoomed extent; nil shows base.
	view *BinView
	// Selection is the selected cell's bin starts, nil for none.
	Selection *[2]int
	// XTitle and YTitle name the axes; Legend is the colour bar.
	XTitle, YTitle string
	Legend         Legend
	// Colors, when Surface is set, replace the theme's: the paper styles
	// draw on white (or black) whatever the theme.
	Colors MapColors

	drag      *contactDrag
	scrollAcc float32
	// changed: the selection or the view changed in input handled since
	// the last Show, which reports it.
	changed bool
	// refocus gives the map the keyboard focus in the next frame.
	refocus bool
	// lastDown is the last press, for a double click where the platform
	// gives no click count.
	lastDown struct {
		at   time.Time
		x, y float32
	}
	index  cellIndex
	bitmap *ui.Bitmap
	pix    [2]*image.RGBA
	flip   int
	drawn  struct {
		version int
		view    BinView
		w, h    int
	}
}

// BinView is a range of bins, by their start coordinates, inclusive.
type BinView struct{ XStart, XEnd, YStart, YEnd int }

// MapColors are a map's surface, text, secondary text and frame colours.
type MapColors struct{ Surface, Ink, Muted, Line ui.Color }

// Legend is the colour bar: ColorAt over [0, 1], ticks in [0, Max]
// labelled by Format.
type Legend struct {
	Title   string
	Max     float64
	Ticks   []float64
	Format  func(float64) string
	ColorAt func(float64) ui.Color
}

type contactDrag struct {
	start, end [2]int
	x, y       float32
	moved      bool
}

// ContactEvent reports what the user did in a frame.
type ContactEvent struct {
	// Hover is true with the bin under the pointer in X, Y.
	Hover bool
	X, Y  int
	// Changed is true when the selection or the view changed since the
	// previous Show (input is handled after Show returns, so a change shows
	// in the frame after it).
	Changed bool
}

// NewContactMap makes an empty map.
func NewContactMap() *ContactMap { return &ContactMap{} }

// SetCells replaces the cells (bin squares in gene coordinates) and the
// full extent, keeping the view and the selection.
func (m *ContactMap) SetCells(cells []Cell, bin int, base BinView) {
	m.cells, m.bin, m.base = cells, max(bin, 1), base
	m.version++
}

// Reset shows the full extent with no selection.
func (m *ContactMap) Reset() {
	m.view, m.Selection, m.drag = nil, nil, nil
}

// Cells returns the cells shown.
func (m *ContactMap) Cells() []Cell { return m.cells }

// RequestFocus gives the map the keyboard focus, so the arrow keys move
// the selection again after a control elsewhere took it.
func (m *ContactMap) RequestFocus() { m.refocus = true }

// Bin is the bin size of the cells.
func (m *ContactMap) Bin() int { return m.bin }

// Base is the full extent.
func (m *ContactMap) Base() BinView { return m.base }

// View is the extent shown.
func (m *ContactMap) View() BinView {
	if m.view != nil {
		return *m.view
	}
	return m.base
}

// Zoomed reports whether a part of the map is shown.
func (m *ContactMap) Zoomed() bool { return m.view != nil }

// SetView shows v (nil: the full extent).
func (m *ContactMap) SetView(v *BinView) { m.view = v }

// Focus selects the cell at (x, y) and shows span bins around it.
func (m *ContactMap) Focus(x, y, span int) {
	p := [2]int{m.snap(x), m.snap(y)}
	m.Selection = &p
	v := m.focused(p, span)
	m.view = &v
}

func (m *ContactMap) snap(v int) int {
	if m.bin <= 0 {
		return v
	}
	return int(math.Floor(float64(v)/float64(m.bin))) * m.bin
}

// MapGeometry is where a view of the map lies in a width x height area:
// the room for the y ticks and title (left), the colour bar (top) and the
// x ticks and title (bottom), square cells, the plot centred on the longer
// side (getContactStageGeometry).
type MapGeometry struct {
	Left, Top, PlotWidth, PlotHeight float32
	View                             BinView
	XCount, YCount                   int
	Cell                             float32
}

func (m *ContactMap) geometry(width, height float32) MapGeometry {
	v := m.View()
	bin := max(m.bin, 1)
	xCount := max(1, int(math.Round(float64(v.XEnd-v.XStart)/float64(bin)))+1)
	yCount := max(1, int(math.Round(float64(v.YEnd-v.YStart)/float64(bin)))+1)
	availableWidth := max(96, width-68-16)
	availableHeight := max(96, height-52-48)
	cell := min(availableWidth/float32(xCount), availableHeight/float32(yCount))
	g := MapGeometry{Top: 52, View: v, XCount: xCount, YCount: yCount, Cell: cell,
		PlotWidth: cell * float32(xCount), PlotHeight: cell * float32(yCount)}
	g.Left = 68 + (availableWidth-g.PlotWidth)/2
	return g
}

// point is the bin under (x, y), or false outside the plot.
func (m *ContactMap) point(g MapGeometry, x, y float32) ([2]int, bool) {
	if x < g.Left || x > g.Left+g.PlotWidth || y < g.Top || y > g.Top+g.PlotHeight {
		return [2]int{}, false
	}
	xi := min(g.XCount-1, max(0, int((x-g.Left)/g.Cell)))
	yi := min(g.YCount-1, max(0, int((y-g.Top)/g.Cell)))
	return [2]int{g.View.XStart + xi*m.bin, g.View.YEnd - yi*m.bin}, true
}

func clampBin(v, lo, hi, bin int) int {
	if hi < lo {
		hi = lo
	}
	v = min(max(v, lo), hi)
	return lo + int(math.Round(float64(v-lo)/float64(bin)))*bin
}

// focused is a view of span bins (on each axis, within the full extent)
// around p (contactFocusedView).
func (m *ContactMap) focused(p [2]int, span int) BinView {
	b, bin := m.base, max(m.bin, 1)
	maxX := (b.XEnd-b.XStart)/bin + 1
	maxY := (b.YEnd-b.YStart)/bin + 1
	xCount := min(maxX, max(2, span))
	yCount := min(maxY, max(2, span))
	xStart := clampBin(p[0]-xCount/2*bin, b.XStart, b.XEnd-(xCount-1)*bin, bin)
	yStart := clampBin(p[1]-yCount/2*bin, b.YStart, b.YEnd-(yCount-1)*bin, bin)
	return BinView{xStart, xStart + (xCount-1)*bin, yStart, yStart + (yCount-1)*bin}
}

// zoom scales the view around p by factor (zoomContactStageView).
func (m *ContactMap) zoom(p [2]int, factor float64) {
	b, v, bin := m.base, m.View(), max(m.bin, 1)
	maxX := (b.XEnd-b.XStart)/bin + 1
	maxY := (b.YEnd-b.YStart)/bin + 1
	xCount := min(maxX, max(2, int(math.Round(float64((v.XEnd-v.XStart)/bin+1)*factor))))
	yCount := min(maxY, max(2, int(math.Round(float64((v.YEnd-v.YStart)/bin+1)*factor))))
	next := m.focused(p, max(xCount, yCount))
	if next == b {
		m.view = nil
		return
	}
	m.view = &next
}

// moveSelection moves the selection one bin by an arrow key, within the view.
func (m *ContactMap) moveSelection(key ui.Key) bool {
	v := m.View()
	cur := [2]int{v.XStart, v.YStart}
	if m.Selection != nil {
		cur = *m.Selection
	}
	switch key {
	case ui.KeyLeft:
		cur[0] -= m.bin
	case ui.KeyRight:
		cur[0] += m.bin
	case ui.KeyUp:
		cur[1] += m.bin
	case ui.KeyDown:
		cur[1] -= m.bin
	default:
		return false
	}
	next := [2]int{clampBin(cur[0], v.XStart, v.XEnd, m.bin), clampBin(cur[1], v.YStart, v.YEnd, m.bin)}
	m.Selection = &next
	return true
}

// Show builds the map in a width x height box.
func (m *ContactMap) Show(c *ui.Context, width, height float32, label string) ContactEvent {
	event := ContactEvent{Changed: m.changed}
	m.changed = false
	box := ui.Box(c.Key("contactmap")).Size(width, height).Cursor(ui.CursorCrosshair).
		Role(ui.RoleImage).Label(label).Focusable()
	g := m.geometry(width, height)
	box.HandleInput(func(ev ui.InputEvent) bool {
		if m.bin <= 0 || len(m.cells) == 0 {
			return false
		}
		switch ev.Kind {
		case ui.InputPointerDown:
			p, ok := m.point(g, ev.X, ev.Y)
			if !ok || ev.Button != 0 {
				return false
			}
			now := time.Now()
			double := ev.Clicks >= 2 || now.Sub(m.lastDown.at) < 400*time.Millisecond &&
				math.Hypot(float64(ev.X-m.lastDown.x), float64(ev.Y-m.lastDown.y)) <= 5
			m.lastDown.at, m.lastDown.x, m.lastDown.y = now, ev.X, ev.Y
			if double {
				m.lastDown.at = time.Time{}
				m.Reset()
				m.changed = true
				return true
			}
			m.drag = &contactDrag{start: p, end: p, x: ev.X, y: ev.Y}
			return true
		case ui.InputPointerMove:
			if m.drag == nil || ev.Button != 0 {
				return false
			}
			if p, ok := m.point(g, min(max(ev.X, g.Left), g.Left+g.PlotWidth), min(max(ev.Y, g.Top), g.Top+g.PlotHeight)); ok {
				m.drag.end = p
			}
			if math.Hypot(float64(ev.X-m.drag.x), float64(ev.Y-m.drag.y)) > 5 {
				m.drag.moved = true
			}
			return true
		case ui.InputPointerUp:
			d := m.drag
			m.drag = nil
			if d == nil {
				return false
			}
			if d.moved {
				x0, x1 := min(d.start[0], d.end[0]), max(d.start[0], d.end[0])
				y0, y1 := min(d.start[1], d.end[1]), max(d.start[1], d.end[1])
				m.view = &BinView{x0, x1, y0, y1}
				m.Selection = &[2]int{x0, y0}
			} else {
				p := d.start
				m.Selection = &p
				m.zoom(p, 0.5)
			}
			m.changed = true
			return true
		case ui.InputScroll:
			p, ok := m.point(g, ev.X, ev.Y)
			if !ok {
				return false
			}
			// a wheel notch zooms as the web's (1.35 out, 0.74 in); a
			// touchpad's small steps add up to one
			if !ev.Precise {
				if ev.DY > 0 {
					m.zoom(p, 1.35)
				} else if ev.DY < 0 {
					m.zoom(p, 0.74)
				}
				m.changed = true
				return true
			}
			const step = 40
			m.scrollAcc += ev.DY
			for m.scrollAcc >= step {
				m.scrollAcc -= step
				m.zoom(p, 1.35)
			}
			for m.scrollAcc <= -step {
				m.scrollAcc += step
				m.zoom(p, 0.74)
			}
			m.changed = true
			return true
		case ui.InputKeyDown:
			if m.moveSelection(ev.Key) {
				m.changed = true
				return true
			}
		}
		return false
	})
	if px, py, over := box.PointerPosition(); over {
		if p, ok := m.point(g, px, py); ok {
			event.Hover, event.X, event.Y = true, p[0], p[1]
		}
	}
	if m.refocus {
		m.refocus = false
		box.Focus()
	}
	focused := box.Focused()
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		m.Paint(p, r, g)
		if focused {
			p.FocusRing(ui.Rect{X: r.X + g.Left - 3, Y: r.Y + g.Top - 3, W: g.PlotWidth + 6, H: g.PlotHeight + 6}, [4]float32{3, 3, 3, 3})
		}
	})
	return event
}

// Geometry is where the map lies in a width x height area.
func (m *ContactMap) Geometry(width, height float32) MapGeometry { return m.geometry(width, height) }

func (m *ContactMap) colors(t *ui.Theme) MapColors {
	if m.Colors.Surface.A > 0 {
		return m.Colors
	}
	// the card's colour: empty bins read as zero, as on the web
	return MapColors{Surface: t.Background, Ink: t.Text, Muted: t.TextMuted, Line: t.Border}
}

// Paint draws the map at r with geometry g (from Geometry for r's size).
func (m *ContactMap) Paint(p *ui.Painter, r ui.Rect, g MapGeometry) {
	t := p.Theme()
	col := m.colors(t)
	p.Fill(r, col.Surface, 0)
	area := ui.Rect{X: r.X + g.Left, Y: r.Y + g.Top, W: g.PlotWidth, H: g.PlotHeight}
	if len(m.cells) > 0 && g.PlotWidth > 0 && g.PlotHeight > 0 {
		m.paintCells(p, area, g)
	}
	bin := float32(max(m.bin, 1))
	p.Clip(ui.Rect{X: area.X - 2, Y: area.Y - 2, W: area.W + 4, H: area.H + 4}, 0, func() {
		if s := m.Selection; s != nil && s[0] >= g.View.XStart && s[0] <= g.View.XEnd && s[1] >= g.View.YStart && s[1] <= g.View.YEnd {
			x := area.X + float32(s[0]-g.View.XStart)/bin*g.Cell
			y := area.Y + float32(g.View.YEnd-s[1])/bin*g.Cell
			p.Stroke(ui.Rect{X: x + 1, Y: y + 1, W: max(2, g.Cell-2), H: max(2, g.Cell-2)}, col.Ink, 0, 2)
		}
		if d := m.drag; d != nil && d.moved {
			x := area.X + float32(min(d.start[0], d.end[0])-g.View.XStart)/bin*g.Cell
			y := area.Y + float32(g.View.YEnd-max(d.start[1], d.end[1]))/bin*g.Cell
			w := (float32(abs(d.start[0]-d.end[0]))/bin + 1) * g.Cell
			h := (float32(abs(d.start[1]-d.end[1]))/bin + 1) * g.Cell
			sel := ui.Rect{X: x, Y: y, W: w, H: h}
			p.Fill(sel, ui.RGBA(0, 96, 254, 0.18), 0)
			p.Stroke(sel, col.Ink, 0, 2)
		}
	})
	p.Stroke(ui.Rect{X: area.X - 0.5, Y: area.Y - 0.5, W: area.W + 1, H: area.H + 1}, col.Line, 0, 1)
	m.paintAxes(p, area, g, col)
	if m.Legend.ColorAt != nil {
		m.paintLegend(p, r, g, col)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (m *ContactMap) paintCells(p *ui.Painter, area ui.Rect, g MapGeometry) {
	scale := p.Scale()
	w, h := int(math.Ceil(float64(area.W*scale))), int(math.Ceil(float64(area.H*scale)))
	if w <= 0 || h <= 0 {
		return
	}
	if m.drawn.w != w || m.drawn.h != h {
		m.pix = [2]*image.RGBA{}
		m.drawn.version = -1
	}
	bin := float64(max(m.bin, 1))
	window := Window{X0: float64(g.View.XStart), X1: float64(g.View.XEnd) + bin, Y0: float64(g.View.YStart), Y1: float64(g.View.YEnd) + bin}
	if m.bitmap == nil || m.drawn.version != m.version || m.drawn.view != g.View {
		m.drawn.version, m.drawn.view, m.drawn.w, m.drawn.h = m.version, g.View, w, h
		m.flip ^= 1
		img := m.pix[m.flip]
		if img == nil {
			img = image.NewRGBA(image.Rect(0, 0, w, h))
			m.pix[m.flip] = img
		} else {
			clear(img.Pix)
		}
		if m.index.version != m.version {
			b := m.base
			m.index = newCellIndex(m.cells, float64(b.XEnd)+bin, float64(b.YEnd)+bin, m.version)
		}
		m.index.raster(img, m.cells, window, 0)
		if m.bitmap == nil {
			m.bitmap = ui.NewBitmap(img)
		} else {
			m.bitmap.Update(img)
		}
	}
	p.Image(m.bitmap, area, ui.FillBox)
}

// ---- axes and colour bar (web K5 and K1)

const axisFont = 11

func (m *ContactMap) paintAxes(p *ui.Painter, area ui.Rect, g MapGeometry, col MapColors) {
	bin := max(m.bin, 1)
	bottom := area.Y + area.H
	xTicks := CoordinateTicks(g.View.XStart, g.View.XEnd+bin-1, area.W, false)
	for i, tick := range xTicks {
		x := float32(math.Round(float64(area.X+tick.Offset))) + 0.5
		p.Line(x, bottom, x, bottom+4, 1, col.Muted)
		w := textWidth(p, tick.Label, axisFont, 400)
		tx := area.X + tick.Offset - w/2
		if i == 0 {
			tx = area.X + tick.Offset
		} else if i == len(xTicks)-1 {
			tx = area.X + tick.Offset - w
		}
		p.Text(tx, bottom+5, tick.Label, axisFont, col.Muted)
	}
	for _, tick := range CoordinateTicks(g.View.YStart, g.View.YEnd+bin-1, area.H, true) {
		y := area.Y + area.H - tick.Offset
		yy := float32(math.Round(float64(y))) + 0.5
		p.Line(area.X-4, yy, area.X, yy, 1, col.Muted)
		w := textWidth(p, tick.Label, axisFont, 400)
		p.Text(area.X-7-w, y-7, tick.Label, axisFont, col.Muted)
	}
	title := ui.Span{Text: FitLabel(m.XTitle, area.W), Size: axisFont, Weight: 600, Color: col.Ink}
	w, _ := p.MeasureText(0, title)
	p.RichText(area.X+(area.W-w)/2, bottom+23, 0, title)
	yTitle := ui.Span{Text: FitLabel(m.YTitle, area.H), Size: axisFont, Weight: 600, Color: col.Ink}
	yw, yh := p.MeasureText(0, yTitle)
	cx, cy := area.X-g.Left+13, area.Y+area.H/2
	p.Transform(ui.Rotation(-90), cx, cy, func() {
		p.RichText(cx-yw/2, cy-yh/2, 0, yTitle)
	})
}

func (m *ContactMap) paintLegend(p *ui.Painter, r ui.Rect, g MapGeometry, col MapColors) {
	l := m.Legend
	width := LegendWidth(g.PlotWidth)
	x, y := r.X+g.Left+g.PlotWidth-width, r.Y+4
	format := l.Format
	if format == nil {
		format = func(v float64) string { return FormatTick(v) }
	}
	if l.Title != "" {
		p.RichText(x, y-1, 0, ui.Span{Text: l.Title, Size: 10, Weight: 600, Color: col.Ink})
	}
	bar := ui.Rect{X: x, Y: y + 15, W: width, H: 10}
	const steps = 64
	for i := range steps {
		w := bar.W / steps
		p.Fill(ui.Rect{X: bar.X + float32(i)*w, Y: bar.Y, W: w + 0.5, H: bar.H}, l.ColorAt(float64(i)/(steps-1)), 0)
	}
	p.Stroke(bar, col.Muted, 0, 0.5)
	for _, v := range l.Ticks {
		f := float32(0)
		if l.Max > 0 {
			f = float32(math.Min(1, math.Max(0, v/l.Max)))
		}
		tx := bar.X + bar.W*f
		p.Line(tx, bar.Y+bar.H, tx, bar.Y+bar.H+4, 1, col.Ink)
		label := format(v)
		w := textWidth(p, label, 10, 400)
		switch {
		case f <= 0.001:
		case f >= 0.999:
			tx -= w
		default:
			tx -= w / 2
		}
		p.Text(tx, bar.Y+bar.H+4, label, 10, col.Ink)
	}
}

// LegendWidth is the colour bar's width over a plot this wide.
func LegendWidth(plotWidth float32) float32 {
	return float32(math.Max(120, math.Min(220, float64(plotWidth)*0.5)))
}

func textWidth(p *ui.Painter, s string, size float32, weight int) float32 {
	w, _ := p.MeasureText(0, ui.Span{Text: s, Size: size, Weight: weight})
	return w
}

// FitLabel shortens text to about width px with a middle ellipsis, so both
// ends of a long RNA name show.
func FitLabel(text string, width float32) string {
	room := max(8, int(width/6.2))
	runes := []rune(text)
	if len(runes) <= room {
		return text
	}
	head := (room) / 2
	return string(runes[:head]) + "…" + string(runes[len(runes)-(room-1-head):])
}

// AxisTick is a tick at Offset px along an axis.
type AxisTick struct {
	Offset float32
	Label  string
}

// CoordinateTicks are the ticks of an inclusive nucleotide axis [first,
// last] length px long: the ends on the plot edges, interior ticks on the
// centre of their nucleotide.
func CoordinateTicks(first, last int, length float32, vertical bool) []AxisTick {
	room := 0.0
	if vertical {
		room = 22
	}
	values := spacedTicks(NiceTicks(float64(first), float64(last), 5), float64(first), float64(last), float64(length),
		func(v float64) string { return FormatInt(int64(v)) }, room)
	out := make([]AxisTick, 0, len(values))
	for _, v := range values {
		var offset float64
		switch {
		case v <= float64(first):
			offset = 0
		case v >= float64(last):
			offset = float64(length)
		default:
			offset = (v - float64(first) + 0.5) / float64(last-first+1) * float64(length)
		}
		out = append(out, AxisTick{float32(offset), FormatInt(int64(v))})
	}
	return out
}

// NiceTicks are 4-7 ticks over [lo, hi] with both ends, interior ones on a
// 1/2/5 x 10^n step (web/ui-kit.js niceTicks).
func NiceTicks(lo, hi float64, target int) []float64 {
	if math.IsNaN(lo) || math.IsNaN(hi) || math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return nil
	}
	if hi == lo {
		return []float64{lo}
	}
	low, high := math.Min(lo, hi), math.Max(lo, hi)
	want := max(2, min(7, target))
	span := high - low
	base := math.Pow(10, math.Floor(math.Log10(span/float64(max(1, want-1))))-1)
	var best []float64
	bestScore, bestStep := 0, 0.0
	for _, mult := range []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000} {
		step := base * mult
		ticks := []float64{low}
		for v := math.Ceil(low/step) * step; v < high; v += step {
			value := math.Round(v/step) * step
			if value-low >= step*0.5 && high-value > step*0.5 {
				value, _ = strconv.ParseFloat(strconv.FormatFloat(value, 'g', 12, 64), 64)
				ticks = append(ticks, value)
			}
		}
		ticks = append(ticks, high)
		if len(ticks) >= 4 && len(ticks) <= 7 {
			score := abs(len(ticks) - want)
			if best == nil || score < bestScore || (score == bestScore && step > bestStep) {
				best, bestScore, bestStep = ticks, score, step
			}
		}
	}
	if best != nil {
		return best
	}
	return []float64{low, (low + high) / 2, high}
}

// spacedTicks drops interior ticks whose labels would touch a neighbour's
// along an axis length px long (labels about 6.5 px per character).
func spacedTicks(ticks []float64, lo, hi, length float64, labelOf func(float64) string, fixedRoom float64) []float64 {
	if len(ticks) <= 2 || hi == lo {
		return ticks
	}
	at := func(v float64) float64 { return (v - lo) / (hi - lo) * length }
	room := func(v float64) float64 {
		if fixedRoom > 0 {
			return fixedRoom
		}
		return float64(len(labelOf(v)))*6.5 + 12
	}
	last := ticks[len(ticks)-1]
	kept := []float64{ticks[0]}
	for _, v := range ticks[1 : len(ticks)-1] {
		prev := kept[len(kept)-1]
		if at(v)-at(prev) >= (room(v)+room(prev))/2 && at(last)-at(v) >= (room(v)+room(last))/2 {
			kept = append(kept, v)
		}
	}
	return append(kept, last)
}

// SpacedTicks is spacedTicks for a colour bar's labels.
func SpacedTicks(ticks []float64, lo, hi, length float64, labelOf func(float64) string) []float64 {
	return spacedTicks(ticks, lo, hi, length, labelOf, 0)
}

// FormatInt groups thousands: 12345 -> 12,345.
func FormatInt(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}

// FormatTick is the web's formatTick: no decimals from 100, one from 10,
// two from 1, else three, with thousands grouped.
func FormatTick(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	a := math.Abs(v)
	digits := 3
	switch {
	case a == 0 || a >= 100:
		digits = 0
	case a >= 10:
		digits = 1
	case a >= 1:
		digits = 2
	}
	s := strconv.FormatFloat(v, 'f', digits, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	whole, frac, _ := strings.Cut(s, ".")
	n, _ := strconv.ParseInt(whole, 10, 64)
	out := FormatInt(n)
	if whole == "-0" {
		out = "-0"
	}
	if frac != "" {
		out += "." + frac
	}
	return out
}
