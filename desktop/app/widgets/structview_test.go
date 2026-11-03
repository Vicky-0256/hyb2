package widgets

import (
	"bytes"
	"image/png"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The PNG export renders off the UI goroutine, as saveExport calls it.
func TestStructViewPNG(t *testing.T) {
	v := &StructView{Mode: ModeCircular, Start: 1, Length: 20,
		Pairs: []LayeredPair{{I: 1, J: 11, Layer: 0}, {I: 5, J: 15, Layer: 1}}}
	colors := StructColors{Background: ui.RGB(255, 255, 255), Line: ui.RGB(120, 120, 120), Text: ui.RGB(0, 0, 0),
		Primary: ui.RGB(0, 0, 255), Pseudoknot: ui.RGB(106, 79, 242), Other: ui.RGB(255, 128, 0)}
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result)
	go func() {
		data, err := v.PNG(200, colors, "test · circular view", 2)
		ch <- result{data, err}
	}()
	got := <-ch
	if got.err != nil {
		t.Fatal(got.err)
	}
	img, err := png.Decode(bytes.NewReader(got.data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 400 || b.Dy() != 456 {
		t.Fatalf("size %v, want 400×456", b)
	}
	// the chord 1–11 is a vertical line through the centre, in blue
	blue := 0
	for y := 100; y < 400; y++ {
		r, g, b, _ := img.At(200, y).RGBA()
		if b > 0xc000 && r < 0x8000 && g < 0x8000 {
			blue++
		}
	}
	if blue < 50 {
		t.Fatalf("the primary chord is missing: %d blue pixels down the centre", blue)
	}
}

// nestingDepths agrees with counting the enclosing pairs directly,
// pseudoknots and pairs sharing an end included.
func TestNestingDepthsMatchACount(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 50; round++ {
		pairs := make([]LayeredPair, rng.Intn(200))
		for i := range pairs {
			a, b := 1+rng.Intn(60), 1+rng.Intn(60)
			pairs[i] = LayeredPair{I: min(a, b), J: max(a, b) + 1, Layer: rng.Intn(2)}
		}
		got := nestingDepths(pairs)
		for i, p := range pairs {
			want := 0
			for _, o := range pairs {
				if o.I < p.I && o.J > p.J {
					want++
				}
			}
			if got[i] != want {
				t.Fatalf("round %d pair %v: depth %d, want %d", round, p, got[i], want)
			}
		}
	}
}

// A view rebuilt with the same inputs reuses its layout; any change in
// the inputs, size or colours lays it out again.
func TestStructViewReusesItsLayout(t *testing.T) {
	colors := StructColors{Primary: ui.RGB(200, 0, 0)}
	view := func() *StructView {
		return &StructView{Mode: ModeRadial, Pairs: []LayeredPair{{1, 20, 0}, {2, 19, 0}, {5, 30, 1}}, Start: 1, Length: 40}
	}
	first := view().cachedShapes(300, colors)
	if again := view().cachedShapes(300, colors); &again[0] != &first[0] {
		t.Fatal("same inputs laid out again")
	}
	changed := view()
	changed.Pairs[2].J = 31
	for name, got := range map[string][]shape{
		"pairs":  changed.cachedShapes(300, colors),
		"size":   view().cachedShapes(301, colors),
		"colors": view().cachedShapes(300, StructColors{Primary: ui.RGB(0, 0, 200)}),
	} {
		if &got[0] == &first[0] {
			t.Fatalf("a change of %s reused the old layout", name)
		}
	}
	if want := view().shapes(300, colors); !reflect.DeepEqual(first, want) {
		t.Fatal("cached layout differs from a fresh one")
	}
}

// The web's pair styles, markers, selection and legend reach the drawing
// and its SVG.
func TestStructViewStylesMarkersAndLegend(t *testing.T) {
	colors := StructColors{Background: ui.RGB(255, 255, 255), Line: ui.RGB(120, 120, 120), Text: ui.RGB(0, 0, 0),
		Primary: ui.RGB(0, 0, 255), Pseudoknot: ui.RGB(106, 79, 242), Other: ui.RGB(255, 128, 0), Selected: ui.RGB(106, 79, 242)}
	high := ui.RGB(200, 100, 0)
	v := &StructView{Mode: ModeCircular, Start: 1, Length: 20, Markers: true, Selected: 5,
		Pairs:  []LayeredPair{{I: 1, J: 11, Layer: 0}, {I: 5, J: 15, Layer: 1}},
		Styles: []PairStyle{{Color: ui.RGB(0, 0, 255), Alpha: 0.9, Width: 2.1}, {Color: high, Alpha: 0.5, Width: 3, Dash: true}},
		Marks:  make([]ui.Color, 20), Legend: []LegendItem{{Color: high, Label: "pseudoknot"}}}
	v.Marks[0] = high
	svg := v.SVG(300, colors, "t")
	for _, want := range []string{`stroke-width="2.10"`, `stroke-width="3.00" stroke="#c86400" stroke-opacity="0.50" stroke-dasharray="5 3"`,
		`<circle cx="150.0" cy="24.0" r="4.50" fill="#c86400"`, ">pseudoknot</text>", `height="346"`} {
		if !strings.Contains(svg, want) {
			t.Fatalf("svg lacks %q: %s", want, svg)
		}
	}
	// 20 markers (fill and ring), the selected one in the selection colour
	if got := strings.Count(svg, `r="4.50"`); got != 40 {
		t.Fatalf("%d marker circles, want 40", got)
	}
	if !strings.Contains(svg, `fill="#6a4ff2"`) {
		t.Fatal("selected marker not drawn in the selection colour")
	}
	// hits: a marker, then the topmost of the two chords crossing the centre
	cx, cy, r := ringGeometry(300)
	if h := v.hit(300, colors, cx, cy-r); h.Position != 1 {
		t.Fatalf("hit at the top %+v", h)
	}
	if h := v.hit(300, colors, cx, cy); h.Pair != 1 || h.Position != 0 {
		t.Fatalf("hit at the centre %+v", h)
	}
	// above 700 nt no markers; the ring itself picks the nucleotide by angle
	long := &StructView{Mode: ModeRadial, Start: 101, Length: 1000, Markers: true}
	if got := strings.Count(long.SVG(300, colors, "t"), "<circle"); got != 4 {
		t.Fatalf("long ring has %d circles, want the 4 rings", got)
	}
	if h := long.hit(300, colors, cx+r, cy); h.Position != 101+250 {
		t.Fatalf("ring hit %+v", h)
	}
	// the matrix's track
	m := &StructView{Mode: ModeMatrix, Start: 1, Length: 1000, Markers: true, Selected: 1000}
	left, _, plot, track := matrixGeometry(300)
	if h := m.hit(300, colors, left+plot, track); h.Position != 1000 {
		t.Fatalf("track hit %+v", h)
	}
}

// The folded view numbers every 10th base (every 25th above 200 nt) and
// zooms and pans as the web's forna buttons.
func TestStructViewFoldedLabelsAndZoom(t *testing.T) {
	colors := StructColors{Background: ui.RGB(255, 255, 255), Line: ui.RGB(120, 120, 120), Text: ui.RGB(0, 0, 0)}
	folded := func(n int) *StructView {
		coords := make([][2]float64, n)
		for i := range coords {
			coords[i] = [2]float64{float64(i % 10), float64(i / 10)}
		}
		return &StructView{Mode: ModeFolded, Start: 1, Length: n, Coords: coords, Markers: true}
	}
	labels := func(v *StructView) []string {
		var out []string
		for _, s := range v.shapes(600, colors) {
			if s.kind == shapeText && s.fine && s.color == colors.Line {
				out = append(out, s.text)
			}
		}
		return out
	}
	if got := labels(folded(40)); !reflect.DeepEqual(got, []string{"10", "20", "30", "40"}) {
		t.Fatalf("labels %q", got)
	}
	if got := labels(folded(210)); len(got) != 8 || got[0] != "25" {
		t.Fatalf("labels %q", got)
	}
	z := &StructZoom{side: 400}
	z.By(ZoomIn, -1, -1)
	if z.Scale != 1.25 || z.X != -0.125 || z.Y != -0.125 {
		t.Fatalf("zoom in %+v", z)
	}
	z.Pan(PanStep, 0)
	z.By(ZoomOut, -1, -1)
	if math.Abs(z.Scale-1) > 1e-9 || z.Fitted() {
		t.Fatalf("zoom out %+v", z)
	}
	z.Fit()
	if !z.Fitted() {
		t.Fatalf("fit %+v", z)
	}
	// a click through the zoom finds the base under it
	v := folded(40)
	v.Zoom = &StructZoom{Scale: 2, X: -100.0 / 600, Y: -50.0 / 600}
	var base shape
	for _, s := range v.shapes(600, colors) {
		if s.pos == 7 {
			base = s
		}
	}
	if h := v.hit(600, colors, base.x0*2-100, base.y0*2-50); h.Position != 7 {
		t.Fatalf("zoomed hit %+v", h)
	}
}
