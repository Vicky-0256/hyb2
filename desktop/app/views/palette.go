package views

import (
	"math"
	"sync"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/widgets"
)

// The web app's colour scales (web/analysis-ui.js), so a map looks the same
// in both apps: the contact map's colour styles, interpolated in CIELAB
// (the theme-following mint–navy ramp reversed in the dark theme), and the
// Compare effect map's violet (A) / teal (B) diverging scale around the
// surface colour. Exports always use the light ramp, as the web's exports do.

// contactStyle is one of the contact map's colour styles (CONTACT_PALETTES).
type contactStyle struct {
	value, label, kicker string
	stops                [][3]float64
}

var (
	brandStops     = [][3]float64{{145, 209, 194}, {77, 187, 213}, {0, 160, 135}, {60, 84, 136}}
	brandStopsDark = [][3]float64{{60, 84, 136}, {0, 160, 135}, {77, 187, 213}, {145, 209, 194}}
	labWhite       = [3]float64{0.95047, 1, 1.08883}
	// contactStyles are the web's, low to high counts: "colour" follows
	// the theme; the others are drawn on white (or black) paper on screen
	// and in exports. ColorBrewer Reds/Blues/YlOrRd and the matplotlib
	// viridis/magma anchors.
	contactStyles = []contactStyle{
		{"colour", "Mint–navy (follows theme)", "Mint–navy sequential", nil},
		{"reds", "White–red (Juicebox style)", "White–red sequential", [][3]float64{{255, 255, 255}, {252, 187, 161}, {251, 106, 74}, {203, 24, 29}, {103, 0, 13}}},
		{"blues", "White–blue", "White–blue sequential", [][3]float64{{247, 251, 255}, {198, 219, 239}, {107, 174, 214}, {33, 113, 181}, {8, 48, 107}}},
		{"heat", "Heat (yellow–red)", "Yellow–red heat", [][3]float64{{255, 255, 204}, {254, 217, 118}, {253, 141, 60}, {227, 26, 28}, {128, 0, 38}}},
		{"viridis", "Viridis (colour-blind safe)", "Viridis", [][3]float64{{68, 1, 84}, {59, 82, 139}, {33, 145, 140}, {94, 201, 98}, {253, 231, 37}}},
		{"magma", "Magma, reversed", "Magma (reversed)", [][3]float64{{252, 253, 191}, {252, 137, 97}, {183, 55, 121}, {81, 18, 124}, {0, 0, 4}}},
		{"monochrome", "White and black", "White and black", nil},
		{"greys-inverted", "Black and white (inverted)", "Black and white", nil},
	}
)

// contactStyleOf is the style with this label or value, else the first.
func contactStyleOf(name string) contactStyle {
	for _, s := range contactStyles {
		if s.label == name || s.value == name {
			return s
		}
	}
	return contactStyles[0]
}

const scaleSteps = 1024

var (
	rampMu sync.Mutex
	ramps  = map[string]*[scaleSteps][3]uint8{}
)

func srgbToLab(rgb [3]float64) [3]float64 {
	var linear [3]float64
	for i, channel := range rgb {
		v := channel / 255
		if v <= 0.04045 {
			linear[i] = v / 12.92
		} else {
			linear[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	xyz := [3]float64{
		0.4124564*linear[0] + 0.3575761*linear[1] + 0.1804375*linear[2],
		0.2126729*linear[0] + 0.7151522*linear[1] + 0.0721750*linear[2],
		0.0193339*linear[0] + 0.1191920*linear[1] + 0.9503041*linear[2],
	}
	for i := range xyz {
		t := xyz[i] / labWhite[i]
		if t > 216.0/24389 {
			xyz[i] = math.Cbrt(t)
		} else {
			xyz[i] = (24389.0/27*t + 16) / 116
		}
	}
	return [3]float64{116*xyz[1] - 16, 500 * (xyz[0] - xyz[1]), 200 * (xyz[1] - xyz[2])}
}

func labToSrgb(lab [3]float64) [3]uint8 {
	fy := (lab[0] + 16) / 116
	f := [3]float64{fy + lab[1]/500, fy, fy - lab[2]/200}
	var xyz [3]float64
	for i, v := range f {
		cube := v * v * v
		if cube > 216.0/24389 {
			xyz[i] = labWhite[i] * cube
		} else {
			xyz[i] = labWhite[i] * (116*v - 16) / (24389.0 / 27)
		}
	}
	linear := [3]float64{
		3.2404542*xyz[0] - 1.5371385*xyz[1] - 0.4985314*xyz[2],
		-0.9692660*xyz[0] + 1.8760108*xyz[1] + 0.0415560*xyz[2],
		0.0556434*xyz[0] - 0.2040259*xyz[1] + 1.0572252*xyz[2],
	}
	var out [3]uint8
	for i, v := range linear {
		encoded := 12.92 * v
		if v > 0.0031308 {
			encoded = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		out[i] = uint8(math.Max(0, math.Min(255, jsRound(encoded*255))))
	}
	return out
}

// jsRound is JavaScript's Math.round (halves round up, also below zero).
func jsRound(v float64) float64 { return math.Floor(v + 0.5) }

// contactRamp is a style's 1024 steps (scaleLut): the theme's mint–navy
// ramp for "colour", the white–black greys, or the style's stops.
func contactRamp(dark bool, style string) *[scaleSteps][3]uint8 {
	entry := contactStyleOf(style)
	key := entry.value
	if key == "colour" && dark {
		key = "colour-dark"
	}
	rampMu.Lock()
	defer rampMu.Unlock()
	if ramp, ok := ramps[key]; ok {
		return ramp
	}
	stops := entry.stops
	if stops == nil {
		stops = brandStops
		if dark {
			stops = brandStopsDark
		}
	}
	labs := make([][3]float64, len(stops))
	for i, s := range stops {
		labs[i] = srgbToLab(s)
	}
	ramp := new([scaleSteps][3]uint8)
	for index := range scaleSteps {
		grey := uint8(jsRound(255 * (1 - float64(index)/(scaleSteps-1))))
		switch entry.value {
		case "monochrome":
			ramp[index] = [3]uint8{grey, grey, grey}
			continue
		case "greys-inverted":
			ramp[index] = [3]uint8{255 - grey, 255 - grey, 255 - grey}
			continue
		}
		scaled := float64(index) / (scaleSteps - 1) * float64(len(labs)-1)
		at := min(len(labs)-2, int(math.Floor(scaled)))
		t := scaled - float64(at)
		var lab [3]float64
		for c := range 3 {
			lab[c] = labs[at][c] + (labs[at+1][c]-labs[at][c])*t
		}
		ramp[index] = labToSrgb(lab)
	}
	ramps[key] = ramp
	return ramp
}

// contactColor is the web's viridis()/viridisRgb(): ratio in [0, 1] on the
// mint–navy ramp of the theme.
func contactColor(ratio float64, dark bool) ui.Color {
	return contactStyleColor(ratio, dark, "colour")
}

// contactStyleColor is ratio in [0, 1] on a colour style's ramp; the theme
// matters only for the theme-following style.
func contactStyleColor(ratio float64, dark bool, style string) ui.Color {
	if math.IsNaN(ratio) {
		ratio = 0
	}
	ratio = math.Max(0, math.Min(1, ratio))
	rgb := contactRamp(dark, style)[int(jsRound(ratio*(scaleSteps-1)))]
	return ui.RGB(rgb[0], rgb[1], rgb[2])
}

// contactPaper are a style's map colours (contactColors): white paper for
// every style but the theme's, black for the inverted greys; zero (the
// theme's) for "colour".
func contactPaper(style string) widgets.MapColors {
	switch contactStyleOf(style).value {
	case "colour":
		return widgets.MapColors{}
	case "monochrome":
		return widgets.MapColors{Surface: ui.Hex("#ffffff"), Ink: ui.Hex("#222222"), Muted: ui.Hex("#666666"), Line: ui.Hex("#cccccc")}
	case "greys-inverted":
		return widgets.MapColors{Surface: ui.Hex("#000000"), Ink: ui.Hex("#f2f2f2"), Muted: ui.Hex("#b3b3b3"), Line: ui.Hex("#4d4d4d")}
	}
	return lightExportColors
}

// lightExportColors are the web's exportColors("light").
var lightExportColors = widgets.MapColors{Surface: ui.Hex("#ffffff"), Ink: ui.Hex("#132c47"), Muted: ui.Hex("#5b6c80"), Line: ui.Hex("#dce7ec")}

// effectColor is the web's effectColor(): value in [-1, 1] (the effect over
// its cap); positive (higher in A) violet, negative (higher in B) teal.
func effectColor(value float64, dark bool) ui.Color {
	bounded := math.Max(-1, math.Min(1, value))
	center := [3]float64{245, 248, 250}
	target := [3]float64{0, 160, 157}
	if dark {
		center = [3]float64{19, 44, 71}
	}
	if bounded >= 0 {
		target = [3]float64{106, 79, 242}
		if dark {
			target = [3]float64{154, 136, 255}
		}
	}
	ratio := math.Abs(bounded)
	mix := func(i int) uint8 { return uint8(jsRound(center[i] + (target[i]-center[i])*ratio)) }
	return ui.RGB(mix(0), mix(1), mix(2))
}

// quantile is the web's quantile(): linear interpolation between ranks of
// ascending values.
func quantile(sorted []float64, fraction float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	position := math.Max(0, math.Min(float64(len(sorted)-1), float64(len(sorted)-1)*fraction))
	lower, upper := int(math.Floor(position)), int(math.Ceil(position))
	return sorted[lower] + (sorted[upper]-sorted[lower])*(position-float64(lower))
}
