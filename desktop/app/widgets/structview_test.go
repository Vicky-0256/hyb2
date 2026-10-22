package widgets

import (
	"bytes"
	"image/png"
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
