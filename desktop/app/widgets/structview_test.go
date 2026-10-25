package widgets

import (
	"bytes"
	"image/png"
	"math/rand"
	"reflect"
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
