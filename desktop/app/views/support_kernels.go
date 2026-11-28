package views

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"

	"github.com/egoist/mygo/ui"
)

//go:embed support-kernels.json
var supportKernelsJSON []byte

type supportKernelOption struct {
	Value        string `json:"value"`
	Label        string `json:"label"`
	DesktopLabel string `json:"desktopLabel"`
	PairModel    string `json:"pairModel"`
}

func (o supportKernelOption) displayLabel() string { return nameOr(o.DesktopLabel, o.Label) }

type supportKernelCatalog struct {
	DefaultKernel string                `json:"defaultKernel"`
	Options       []supportKernelOption `json:"options"`
	Engines       map[string]foldEngine `json:"engines"`
	FoldText      struct {
		Alphabet, Header, Space, Numbers string
		Aliases                          map[string]string
		MinWindowNT                      int `json:"minWindowNt"`
		MinPastedNT                      int `json:"minPastedNt"`
		MinSequenceNT                    int `json:"minSequenceNt"`
		// LocalMaxNT is bin/cplfold's length guard (HYB2_CPLFOLD_MAX_NT raises it).
		LocalMaxNT int `json:"localMaxNt"`
	} `json:"foldText"`
	labels []string
}

type foldEngine struct {
	Defaults struct {
		Alpha, BeamSize, MaxPhase1, EnergyDelta, Beta              float64
		AllowPseudoknot                                            bool
		Temperature, MinLoopSize, ConstraintLimit, RandomFoldCount float64
		K, C, Fraction                                             float64
		KRange                                                     string `json:"kRange"`
	} `json:"defaults"`
	KRangeGrammar struct{ List, Number *regexp.Regexp } `json:"kRangeGrammar"`
	Ranges        map[string]foldRange                  `json:"ranges"`
	Models        []struct{ Value, Label string }       `json:"models"`
	DefaultModel  string                                `json:"defaultModel"`
}

func engineSettings(engine string) foldEngine {
	if engine == engineVienna || engine == engineRNAcofold { // ViennaRNA's settings, under another catalog key
		engine = "vienna"
	}
	return stKernels.Engines[engine]
}

func (e foldEngine) hasModel(value string) bool {
	for _, m := range e.Models {
		if m.Value == value {
			return true
		}
	}
	return false
}

type foldRange [2]float64

func (r foldRange) String() string { return fmt.Sprintf("%g to %g", r[0], r[1]) }

func (r foldRange) check(pl *foldPlan, label string, value float64, integer bool) {
	if !(value >= r[0] && value <= r[1]) || (integer && value != math.Trunc(value)) {
		kind := "a number"
		if integer {
			kind = "an integer"
		}
		pl.problems = append(pl.problems, fmt.Sprintf("%s must be %s from %s.", label, kind, r))
	}
}

func foldNumberRow(c *ui.Context, key, label string, value *float64, r foldRange, step float64, help string) {
	ui.Column(c).Gap(2).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(12).Bold().Width(150)
			ui.NumberInput(c.Key(key), value, r[0], r[1], step).Grow(1).Label(label)
		})
		ui.Text(c, help).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

var stKernels = parseSupportKernels(supportKernelsJSON)

func parseSupportKernels(data []byte) supportKernelCatalog {
	var catalog supportKernelCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		panic("support-kernels.json: " + err.Error())
	}
	for _, option := range catalog.Options {
		catalog.labels = append(catalog.labels, option.displayLabel())
	}
	if catalog.DefaultKernel == "" || catalog.option(catalog.DefaultKernel).Value == "" {
		panic("support-kernels.json: missing default kernel option")
	}
	return catalog
}

func (catalog supportKernelCatalog) option(value string) supportKernelOption {
	for _, option := range catalog.Options {
		if option.Value == value {
			return option
		}
	}
	return supportKernelOption{}
}
