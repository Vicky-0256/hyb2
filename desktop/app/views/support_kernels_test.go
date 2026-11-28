package views

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Change the serialized contract, not Go fields, so form tests also check decoding.
func stInjectFoldTextMinimums(t *testing.T, window, pasted, sequence int) {
	t.Helper()
	previous := stKernels
	t.Cleanup(func() { stKernels = previous })
	var catalog map[string]any
	if err := json.Unmarshal(supportKernelsJSON, &catalog); err != nil {
		t.Fatal(err)
	}
	rules := catalog["foldText"].(map[string]any)
	rules["minWindowNt"], rules["minPastedNt"], rules["minSequenceNt"] = window, pasted, sequence
	changed, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	stKernels = parseSupportKernels(changed)
}

func TestStructureInjectedEngineControlsAndRequest(t *testing.T) {
	previous := stKernels
	t.Cleanup(func() { stKernels = previous })
	stKernels = parseSupportKernels(supportKernelsJSON) // fresh maps and slices
	e := engineSettings(engineCPLfold)
	e.Models[0].Value, e.Models[0].Label = "custom-default", "Custom default"
	e.Models[1].Value, e.Models[1].Label = "custom-model", "Additional model"
	e.DefaultModel = "custom-default"
	e.Defaults.AllowPseudoknot = true
	e.Ranges["beamSize"] = foldRange{1, 21}
	stKernels.Engines[engineCPLfold] = e

	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 2200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	p.beam = 22
	if pl := p.plan(); !slices.Contains(pl.problems, "Beam size must be an integer from 1 to 21.") {
		t.Fatalf("plan ignored changed beam range: %v", pl.problems)
	}
	p.beam = 21
	if err := tt.Click("Advanced settings"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Custom default") {
		t.Fatal("picker did not use catalog default")
	}
	for _, control := range []string{"Custom default", "Additional model", "Run fold"} {
		if err := tt.Click(control); err != nil {
			t.Fatal(err)
		}
	}
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 {
		t.Fatalf("started %v; problems %v", started, p.plan().problems)
	}
	params := started[0].Parameters.(map[string]any)
	if params["energy_model"] != "custom-model" || params["beam"] != 21 || params["allow_pseudoknot"] != true {
		t.Fatalf("request: %v", params)
	}
}

func TestStructureViennaCatalogDefaults(t *testing.T) {
	previous := stKernels
	t.Cleanup(func() { stKernels = previous })
	stKernels = parseSupportKernels(supportKernelsJSON)
	for _, name := range []string{"vienna", engineComrades} {
		e := stKernels.Engines[name]
		e.Defaults.Temperature, e.Defaults.MinLoopSize = 29.5, 5
		if name == engineComrades {
			e.Defaults.ConstraintLimit, e.Defaults.RandomFoldCount = 2, 1
			e.Ranges["constraintLimit"] = foldRange{1, 50}
		}
		stKernels.Engines[name] = e
	}

	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 2400)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true}) // click Reset with the expanded panel in place
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.temperature != 29.5 || p.minLoop != 5 || p.stems != 2 || p.randomFolds != 1 {
		t.Fatalf("initial settings: %g, %g, %g, %g", p.temperature, p.minLoop, p.stems, p.randomFolds)
	}
	stPick(t, tt, engineComrades, false)
	p.advancedOpen, p.mode, p.windows, p.stems = true, "windows", "2800-3300", 75
	if pl := p.plan(); !slices.Contains(pl.problems, "Ranked stem constraints must be an integer from 1 to 50.") {
		t.Fatalf("stale stem count not rejected: %v", pl.problems)
	}
	tt.Frame()
	for _, help := range []string{"Folding temperature, 0 to 100 °C. Default 29.5.", "Fewest unpaired bases closing a hairpin, 0 to 12. Default 5.",
		"Strongest RNAcofold stems tried as hard pairs, 1 to 50. Default 2.", "Extra fits with the stems in seeded random orders, 0 to 1000; each takes about as long as the ranked fit. Default 1."} {
		if !tt.HasText(help) {
			t.Fatalf("missing catalog help: %q", help)
		}
	}
	if err := tt.Click("Reset to defaults"); err != nil {
		t.Fatal(err)
	}
	pl := p.plan()
	params := pl.req.Parameters.(map[string]any)
	if len(pl.problems) != 0 || p.viennaSettingsChanged() || params["temperature"] != 29.5 || params["min_loop"] != 5 ||
		params["constraint_limit"] != 2 || params["random_folds"] != 1 {
		t.Fatalf("reset: params %v, problems %v", params, pl.problems)
	}
}
