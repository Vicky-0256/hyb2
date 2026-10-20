package views

import (
	"math"
	"sync"

	"github.com/egoist/mygo/ui"
)

// The web app's colour scales (web/analysis-ui.js), so a map looks the same
// in both apps: the contact map's teal–violet sequential ramp, interpolated in
// CIELAB (one ramp per theme), and the Compare effect map's violet (A) / teal
// (B) diverging scale around the surface colour. Exports always use the light
// ramp, as the web's exports do.

var (
	contactStopsLight = [][3]float64{{150, 215, 210}, {0, 160, 157}, {106, 79, 242}, {45, 27, 105}}
	contactStopsDark  = [][3]float64{{46, 104, 130}, {0, 176, 172}, {200, 190, 255}}
	labWhite          = [3]float64{0.95047, 1, 1.08883}
)

const scaleSteps = 1024

var (
	rampOnce            [2]sync.Once
	rampLight, rampDark [scaleSteps][3]uint8
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

func contactRamp(dark bool) *[scaleSteps][3]uint8 {
	k, ramp, stops := 0, &rampLight, contactStopsLight
	if dark {
		k, ramp, stops = 1, &rampDark, contactStopsDark
	}
	rampOnce[k].Do(func() {
		labs := make([][3]float64, len(stops))
		for i, s := range stops {
			labs[i] = srgbToLab(s)
		}
		for index := range scaleSteps {
			scaled := float64(index) / (scaleSteps - 1) * float64(len(labs)-1)
			at := min(len(labs)-2, int(math.Floor(scaled)))
			t := scaled - float64(at)
			var lab [3]float64
			for c := range 3 {
				lab[c] = labs[at][c] + (labs[at+1][c]-labs[at][c])*t
			}
			ramp[index] = labToSrgb(lab)
		}
	})
	return ramp
}

// contactColor is the web's viridis()/viridisRgb(): ratio in [0, 1] on the
// contact ramp of the theme.
func contactColor(ratio float64, dark bool) ui.Color {
	if math.IsNaN(ratio) {
		ratio = 0
	}
	ratio = math.Max(0, math.Min(1, ratio))
	rgb := contactRamp(dark)[int(jsRound(ratio*(scaleSteps-1)))]
	return ui.RGB(rgb[0], rgb[1], rgb[2])
}

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
