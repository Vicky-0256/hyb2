package views

// ⑤ Structure, the ViennaRNA engines of the web RNA Structure page: the
// ViennaRNA MFE mode (temperature, minimum hairpin loop, manual hard base
// pairs; engine "viennarna", pipeline/hyb2analysis/vienna.py), RNAcofold of
// two regions from their sequences alone (engine "viennarna", mode
// "cofold") and COMRADES (ViennaRNA), guided by RNAcofold of the chimeras
// (engine "comrades", pipeline/hyb2analysis/comrades.py), each run as a
// structure task.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

const (
	engineCPLfold   = "cplfold"
	engineCPLcofold = "cplcofold"
	engineVienna    = "viennarna"
	// engineRNAcofold is the form's RNAcofold of two regions; its task is
	// engine engineVienna in mode "cofold".
	engineRNAcofold = "rnacofold"
	engineComrades  = "comrades"
	// the COMRADES folding programs (comrades.py FOLD_PROGRAMS)
	foldViennaRNA   = "viennarna"
	foldMfold       = "mfold"
	stDefaultSeed   = "HYB2-Web"
	stComradesMaxNT = 3000
	// a measured ViennaRNA MFE of 10,807 nt, for the check before running:
	// memory is quadratic in the length, time grows as length^2.5 at these
	// lengths (measured 0.81 s at 1,000 nt, 10.9 s at 3,000 nt; cubic from
	// this anchor was 1.8-3.5x low there)
	viennaRefNT, viennaRefSeconds, viennaRefMB = 10_807, 285.0, 670.0
	viennaTimeExponent                         = 2.5
)

var stHardPair = regexp.MustCompile(`^([1-9][0-9]*)\s*-\s*([1-9][0-9]*)$`)

// stParseHardPairs reads "i-j" lines (RNA positions), as the web's manual
// constraints; the runtime checks the bases, loops and crossings.
func stParseHardPairs(text string) ([][2]int, []string) {
	var pairs [][2]int
	var errs []string
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := stHardPair.FindStringSubmatch(line)
		if m == nil {
			errs = append(errs, fmt.Sprintf("Hard pair line %d must be one pair as i-j, for example 4-18.", n+1))
			continue
		}
		i, _ := strconv.Atoi(m[1])
		j, _ := strconv.Atoi(m[2])
		if i >= j {
			errs = append(errs, fmt.Sprintf("Hard pair line %d must have i < j.", n+1))
			continue
		}
		pairs = append(pairs, [2]int{i, j})
	}
	return pairs, errs
}

// viennaPlan replaces the CPLfold parameters, limits and memory of a plan.
func (p *structurePage) viennaPlan(pl *foldPlan) {
	engine := p.engine
	pl.req.Engine = &engine
	pl.pk = false
	if p.engine == engineIRIS {
		p.irisPlan(pl) // structure_iris.go
		return
	}
	r := engineSettings(p.engine).Ranges
	r["temperature"].check(pl, "Temperature", p.temperature, false)
	r["minLoopSize"].check(pl, "Minimum hairpin loop", p.minLoop, true)
	if p.engine == engineComrades {
		r["constraintLimit"].check(pl, "Ranked stem constraints", p.stems, true)
		r["randomFoldCount"].check(pl, "Randomised folds", p.randomFolds, true)
		p.comradesPlan(pl)
		return
	}
	pairs, errs := stParseHardPairs(p.hardPairs)
	pl.problems = append(pl.problems, errs...)
	list := make([][2]int, len(pairs))
	copy(list, pairs)
	pl.req.Parameters = map[string]any{"temperature": p.temperature, "min_loop": int(p.minLoop), "hard_pairs": list}
	pl.limits = []string{"ViennaRNA MFE folds the sequence alone: the chimeric reads are not used, the samples only give the reference.",
		"No pseudoknots: ViennaRNA returns a nested structure."}
	if p.mode == "windows" {
		pl.limits = append(pl.limits, "Each window is folded on its own; a hard pair applies to the windows holding both its ends.")
	}
	for _, pr := range pairs {
		inside := pl.length == 0
		for _, n := range pl.windowsOrWhole() {
			inside = inside || (pr[0] >= n[0] && pr[1] <= n[1])
		}
		if !inside {
			pl.problems = append(pl.problems, fmt.Sprintf("Hard pair %d-%d is not inside a folded window.", pr[0], pr[1]))
		}
	}
	viennaMemory(pl)
}

// rnacofoldPlan: RNAcofold of the plan's two strands, two regions or two
// pasted sequences, from their sequences alone (vienna.py cofold).
func (p *structurePage) rnacofoldPlan(pl *foldPlan) {
	engine, mode := engineVienna, "cofold"
	pl.req.Engine, pl.req.Mode, pl.pk = &engine, &mode, false
	r := engineSettings(engineRNAcofold).Ranges
	r["temperature"].check(pl, "Temperature", p.temperature, false)
	r["minLoopSize"].check(pl, "Minimum hairpin loop", p.minLoop, true)
	pl.req.Parameters = map[string]any{"temperature": p.temperature, "min_loop": int(p.minLoop)}
	pl.limits = append(pl.limits, "RNAcofold folds the two strands from their sequences alone: the chimeric reads are not used.",
		"No pseudoknots: RNAcofold returns a nested structure.")
	if len(pl.lengths) != 2 {
		return
	}
	lengths := pl.lengths
	if n := lengths[0] + lengths[1]; n > stViennaMaxNT {
		pl.problems = append(pl.problems, fmt.Sprintf("Both strands are %s nt; RNAcofold folds at most %s nt in all.",
			FormatNumber(int64(n)), FormatNumber(stViennaMaxNT)))
	}
	// the memory of the joined fold; the form shows the two strands
	pl.lengths = []int{lengths[0] + lengths[1]}
	viennaMemory(pl)
	pl.lengths = lengths
}

// viennaMemory: one window at a time, quadratic in its length.
func viennaMemory(pl *foldPlan) {
	pl.memMB = 0
	for _, n := range pl.lengths {
		r := float64(n) / viennaRefNT
		pl.memMB = max(pl.memMB, uint64(60+viennaRefMB*r*r))
	}
	pl.req.MemMB = &pl.memMB
	one := uint32(1)
	pl.req.Workers = &one
}

// stLargeEnsemble is the most randomised folds run without confirming.
const stLargeEnsemble = 100

// comradesProgram is the program the COMRADES stems are fitted with.
func (p *structurePage) comradesProgram() string {
	if p.foldProgram == foldMfold {
		return foldMfold
	}
	return foldViennaRNA
}

// comradesName is the engine's name for the folding program, as the
// runtime names its results (comrades.engine_name).
func (p *structurePage) comradesName() string {
	if p.comradesProgram() == foldMfold {
		return "COMRADES(mfold)"
	}
	return "COMRADES(ViennaRNA)"
}

// comradesProgramName names the fitting program in sentences.
func (p *structurePage) comradesProgramName() string {
	if p.comradesProgram() == foldMfold {
		return "mfold"
	}
	return "ViennaRNA"
}

// comradesMfoldProblems: mfold must be found, at 37 °C and minimum loop 3.
func (p *structurePage) comradesMfoldProblems(pl *foldPlan) {
	if p.comradesProgram() != foldMfold {
		return
	}
	if _, problem := p.a.appMfold(); problem != "" {
		pl.problems = append(pl.problems, problem)
	}
	r := engineSettings(foldMfold).Ranges
	if temp := r["temperature"][0]; p.temperature != temp {
		pl.problems = append(pl.problems, fmt.Sprintf("mfold folds at %[1]g °C only (its version 3.0 energies): set Temperature to %[1]g, or fold with ViennaRNA.", temp))
	}
	if loop := r["minLoopSize"][0]; p.minLoop != loop {
		pl.problems = append(pl.problems, fmt.Sprintf("mfold's minimum hairpin loop is fixed at %[1]g: set Minimum hairpin loop to %[1]g, or fold with ViennaRNA.", loop))
	}
}

// comradesPlan: the web's COMRADES(ViennaRNA) settings and limits, or
// COMRADES(mfold)'s.
func (p *structurePage) comradesPlan(pl *foldPlan) {
	seed := strings.TrimSpace(p.seed)
	pl.req.Parameters = map[string]any{"temperature": p.temperature, "min_loop": int(p.minLoop),
		"constraint_limit": int(p.stems), "random_folds": int(p.randomFolds), "seed": seed, "homodimer_only": p.homodimerOnly,
		"fold_program": p.comradesProgram()}
	pl.limits = []string{"Each chimera inside a window is cofolded (RNAcofold); its intermolecular pairs are the evidence, merged into stems; weak stem ends are trimmed.",
		"Stems are added as hard pairs one by one, ranked by support, then in seeded random orders, each fitted with " + p.comradesProgramName() +
			"; the structure matching most evidence wins.",
		"No pseudoknots: " + p.comradesProgramName() + " returns a nested structure."}
	p.comradesMfoldProblems(pl)
	if seed == "" {
		pl.problems = append(pl.problems, "Enter a reproducibility seed for the randomised constraint orders.")
	}
	if p.randomFolds > stLargeEnsemble && !p.allowLargeEnsemble {
		pl.problems = append(pl.problems, "Confirm the large ensemble under COMRADES settings (more than 100 randomised folds) to run it.")
	}
	if p.mode == "paired" {
		p.pairedPlan(pl)
		return
	}
	for _, n := range pl.lengths {
		if n > stComradesMaxNT {
			pl.problems = append(pl.problems, fmt.Sprintf("%s folds at most %s nt at a time, as the web: fold windows of that size.",
				p.comradesName(), FormatNumber(stComradesMaxNT)))
			break
		}
	}
	viennaMemory(pl)
}

// stSpacerNT is the web's spacer between two COMRADES regions (50 A + 50 U).
const stSpacerNT = 100

// pairedPlan: COMRADES(ViennaRNA) of two regions joined by the spacer, the
// web's long-range, intermolecular and homodimer layouts.
func (p *structurePage) pairedPlan(pl *foldPlan) {
	r1, r2, ok := p.twoRegions(pl)
	mode := "paired"
	pl.req.Mode, pl.req.Windows, pl.req.Regions = &mode, nil, []core.Region{r1, r2}
	pl.what, pl.windows, pl.lengths = "2 regions joined by the spacer", nil, nil
	pl.limits = []string{"The regions are joined by the web's 100-nt A/U spacer and folded together; the result keeps the two regions.",
		"Only chimeras with one arm in each region are evidence; each is cofolded (RNAcofold).",
		"No pseudoknots: " + p.comradesProgramName() + " returns a nested structure."}
	if !ok {
		return
	}
	n1, n2 := int(r1.End-r1.Start+1), int(r2.End-r2.Start+1)
	if n := n1 + stSpacerNT + n2; n > stComradesMaxNT {
		pl.problems = append(pl.problems, fmt.Sprintf("Both regions and the 100-nt spacer are %s nt; %s folds at most %s nt, as the web.",
			FormatNumber(int64(n)), p.comradesName(), FormatNumber(stComradesMaxNT)))
	}
	// the memory of the joined fold; the form shows the two regions
	pl.lengths = []int{n1 + stSpacerNT + n2}
	viennaMemory(pl)
	pl.lengths = []int{n1, n2}
}

// comradesSeconds is a rough upper bound: one MFE per stem tried, for the
// ranked order and each randomised one.
func (p *structurePage) comradesSeconds(lengths []int) float64 {
	if p.comradesProgram() == foldMfold {
		return mfoldSeconds(lengths) * p.stems * (1 + p.randomFolds)
	}
	return viennaSeconds(lengths) * p.stems * (1 + p.randomFolds)
}

// mfoldSeconds is the time of one mfold fold of each length (measured
// anchor, plus mfold's start-up of about a second).
func mfoldSeconds(lengths []int) float64 {
	total := 0.0
	for _, n := range lengths {
		total += 1 + mfoldRefSeconds*math.Pow(float64(n)/mfoldRefNT, mfoldTimeExponent)
	}
	return total
}

// windowsOrWhole are the folded ranges.
func (pl foldPlan) windowsOrWhole() [][2]int {
	if len(pl.windows) == 0 {
		return [][2]int{{1, pl.length}}
	}
	out := make([][2]int, len(pl.windows))
	for i, w := range pl.windows {
		out[i] = [2]int{int(w.Start), int(w.End)}
	}
	return out
}

// viennaSeconds is the rough time of folding these lengths one after another.
func viennaSeconds(lengths []int) float64 {
	total := 0.0
	for _, n := range lengths {
		total += viennaRefSeconds * math.Pow(float64(n)/viennaRefNT, viennaTimeExponent)
	}
	return total
}

// resetViennaSettings also initializes the shared fields before either Vienna engine is selected.
func (p *structurePage) resetViennaSettings() {
	cm := engineSettings(engineComrades).Defaults
	thermo := engineSettings(engineVienna).Defaults
	if p.engine == engineComrades {
		thermo = cm
	}
	p.temperature, p.minLoop = thermo.Temperature, thermo.MinLoopSize
	p.stems, p.randomFolds = cm.ConstraintLimit, cm.RandomFoldCount
	p.seed, p.homodimerOnly, p.allowLargeEnsemble, p.foldProgram = stDefaultSeed, false, false, foldViennaRNA
}

func (p *structurePage) viennaSettingsChanged() bool {
	d := engineSettings(p.engine).Defaults
	changed := p.temperature != d.Temperature || p.minLoop != d.MinLoopSize
	if p.engine == engineComrades {
		changed = changed || p.stems != d.ConstraintLimit || p.randomFolds != d.RandomFoldCount || p.seed != stDefaultSeed || p.homodimerOnly ||
			p.comradesProgram() != foldViennaRNA
	}
	return changed
}

// viennaAdvanced: the ViennaRNA settings, then the hard pairs (ViennaRNA
// MFE) or the evidence settings (COMRADES).
func (p *structurePage) viennaAdvanced(c *ui.Context) {
	t := c.Theme()
	changed := p.viennaSettingsChanged()
	comrades := p.engine == engineComrades
	title := "ViennaRNA settings"
	switch p.engine {
	case engineComrades:
		title = "COMRADES settings"
	case engineRNAcofold:
		title = "RNAcofold settings"
	}
	ui.Collapsible(c.Key("st-vienna"), title, &p.advancedOpen, func() {
		ui.Column(c).Gap(Space3).Padding(6, 0, 0, 0).Children(func() {
			if comrades {
				p.foldProgramField(c)
			}
			settings := engineSettings(p.engine)
			r, d := settings.Ranges, settings.Defaults
			temp, loop := r["temperature"], r["minLoopSize"]
			tempHelp := fmt.Sprintf("Folding temperature, %s °C. Default %g.", temp, d.Temperature)
			loopHelp := fmt.Sprintf("Fewest unpaired bases closing a hairpin, %s. Default %g.", loop, d.MinLoopSize)
			if comrades && p.comradesProgram() == foldMfold {
				fixed := engineSettings(foldMfold).Ranges
				temp, loop = fixed["temperature"], fixed["minLoopSize"]
				tempHelp = fmt.Sprintf("mfold folds at %g °C only.", temp[0])
				loopHelp = fmt.Sprintf("mfold's minimum hairpin loop is fixed at %g.", loop[0])
			}
			foldNumberRow(c, "st-temp", "Temperature (°C)", &p.temperature, temp, 0.1, tempHelp)
			foldNumberRow(c, "st-loop", "Minimum hairpin loop", &p.minLoop, loop, 1, loopHelp)
			if comrades {
				foldNumberRow(c, "st-stems", "Ranked stem constraints", &p.stems, r["constraintLimit"], 1,
					fmt.Sprintf("Strongest RNAcofold stems tried as hard pairs, %s. Default %g.", r["constraintLimit"], d.ConstraintLimit))
				foldNumberRow(c, "st-random", "Randomised folds", &p.randomFolds, r["randomFoldCount"], 1,
					fmt.Sprintf("Extra fits with the stems in seeded random orders, %s; each takes about as long as the ranked fit. Default %g.", r["randomFoldCount"], d.RandomFoldCount))
				if p.randomFolds > stLargeEnsemble {
					ui.Checkbox(c.Key("st-large"), &p.allowLargeEnsemble,
						"I understand that up to "+FormatNumber(int64(r["randomFoldCount"][1]))+" serial folds may take a long time; Cancel stops them.")
				}
				Field(c, "Seed", func() {
					ui.TextInput(c.Key("st-seed"), &p.seed).Label("Seed").Width(200)
					ui.Text(c, "The same seed gives the same random orders. Default HYB2-Web.").FontSize(FontSmall).TextColor(t.TextMuted)
				})
				ui.Checkbox(c.Key("st-homodimer"), &p.homodimerOnly, "Homodimer proxies only (both arms on one RNA, overlap ≥ 5)")
			}
			if ui.Button(c, "Reset to defaults").Disabled(!changed).Clicked() {
				p.resetViennaSettings()
			}
		})
	})
	if comrades {
		summary := fmt.Sprintf("%s · %g °C · minimum loop %d · %d stems · %d randomised folds · seed %s",
			p.comradesProgramName(), p.temperature, int(p.minLoop), int(p.stems), int(p.randomFolds), p.seed)
		if p.homodimerOnly {
			summary += " · homodimer proxies only"
		}
		color := t.TextMuted
		if changed {
			summary += " · changed from the defaults"
			color = t.Warning
		} else {
			summary += " · defaults"
		}
		ui.Text(c, summary).FontSize(FontSmall).TextColor(color)
		return
	}
	if p.engine == engineVienna { // ViennaRNA MFE of one region
		Field(c, "Hard base pairs (optional): i-j, one per line, RNA positions", func() {
			ui.TextArea(c.Key("st-hard"), &p.hardPairs).Lines(2, 6).Font("monospace").Placeholder("e.g.\n120-188").Label("Hard base pairs")
			ui.Text(c, "Each pair is enforced: A-U, G-C or G-U, not crossing, at least the minimum loop apart.").FontSize(FontSmall).TextColor(t.TextMuted)
		})
	}
	summary := fmt.Sprintf("%g °C · minimum loop %d", p.temperature, int(p.minLoop))
	color := t.TextMuted
	if changed {
		summary += " · changed from the defaults"
		color = t.Warning
	} else {
		summary += " · defaults"
	}
	ui.Text(c, summary).FontSize(FontSmall).TextColor(color)
}

// foldProgramField: the program the COMRADES stems are fitted with,
// ViennaRNA (bundled) or mfold 3.6 (installed separately; 37 °C, minimum
// loop 3), and whether mfold is found.
func (p *structurePage) foldProgramField(c *ui.Context) {
	t := c.Theme()
	Field(c, "Folding program", func() {
		p.foldProgram = p.comradesProgram()
		r := engineSettings(foldMfold).Ranges
		temp, loop := r["temperature"][0], r["minLoopSize"][0]
		changed := func() {
			if p.foldProgram == foldMfold {
				// mfold's fixed settings
				p.temperature, p.minLoop = temp, loop
			}
		}
		ui.Radio(c, &p.foldProgram, foldViennaRNA, "ViennaRNA (bundled)").OnChange(changed)
		ui.Radio(c, &p.foldProgram, foldMfold, "mfold 3.6 (installed separately)").OnChange(changed)
		if p.comradesProgram() != foldMfold {
			ui.Text(c, "Fits the stems with ViennaRNA's constrained MFE, as the web.").FontSize(FontSmall).TextColor(t.TextMuted)
			return
		}
		path, problem := p.a.appMfold()
		if path != "" {
			ui.Text(c, fmt.Sprintf("Using %s. mfold applies each stem as forced base pairs; it folds at %g °C with minimum loop %g, ", path, temp, loop)+
				"and is slower than ViennaRNA (about 7 s per fold at 1,000 nt).").FontSize(FontSmall).TextColor(t.TextMuted)
			return
		}
		ui.Text(c, problem).FontSize(FontSmall).TextColor(t.Danger)
	})
}

// stIsComrades: a result engine name of COMRADES, with either folding
// program ("COMRADES(ViennaRNA)", "COMRADES(mfold)").
func stIsComrades(engine string) bool {
	return strings.HasPrefix(engine, "COMRADES(")
}
