package views

// ⑤ Structure, the ViennaRNA engines of the web RNA Structure page: the
// ViennaRNA MFE mode (temperature, minimum hairpin loop, manual hard base
// pairs; engine "viennarna", desktop/runtime/vienna.py) and COMRADES
// (ViennaRNA), guided by RNAcofold of the chimeras (engine "comrades",
// desktop/runtime/comrades.py), each run as a structure task.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	engineCPLfold  = "cplfold"
	engineVienna   = "viennarna"
	engineComrades = "comrades"
	// the web's COMRADES(ViennaRNA) defaults and limits
	stDefaultStems, stDefaultSeed = 75, "HYB2-Web"
	stComradesMaxNT               = 3000
	// the web's ViennaRNA defaults
	stDefaultTemp = 37
	stDefaultLoop = 3
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
	if p.engine == engineComrades {
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

// comradesPlan: the web's COMRADES(ViennaRNA) settings and limits.
func (p *structurePage) comradesPlan(pl *foldPlan) {
	seed := strings.TrimSpace(p.seed)
	pl.req.Parameters = map[string]any{"temperature": p.temperature, "min_loop": int(p.minLoop),
		"constraint_limit": int(p.stems), "random_folds": int(p.randomFolds), "seed": seed, "homodimer_only": p.homodimerOnly}
	pl.limits = []string{"Each chimera inside a window is cofolded (RNAcofold); its intermolecular pairs are the evidence, merged into stems.",
		"Stems are added as hard pairs one by one, ranked by support, then in seeded random orders; the structure matching most evidence wins.",
		"No pseudoknots: ViennaRNA returns a nested structure."}
	if seed == "" {
		pl.problems = append(pl.problems, "Enter a reproducibility seed for the randomised constraint orders.")
	}
	if p.randomFolds > stLargeEnsemble && !p.allowLargeEnsemble {
		pl.problems = append(pl.problems, "Confirm the large ensemble under ViennaRNA settings (more than 100 randomised folds) to run it.")
	}
	for _, n := range pl.lengths {
		if n > stComradesMaxNT {
			pl.problems = append(pl.problems, fmt.Sprintf("COMRADES(ViennaRNA) folds at most %s nt at a time, as the web: fold windows of that size.",
				FormatNumber(stComradesMaxNT)))
			break
		}
	}
	viennaMemory(pl)
}

// comradesSeconds is a rough upper bound: one MFE per stem tried, for the
// ranked order and each randomised one.
func (p *structurePage) comradesSeconds(lengths []int) float64 {
	return viennaSeconds(lengths) * p.stems * (1 + p.randomFolds)
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

func (p *structurePage) viennaSettingsChanged() bool {
	changed := p.temperature != stDefaultTemp || int(p.minLoop) != stDefaultLoop
	if p.engine == engineComrades {
		changed = changed || int(p.stems) != stDefaultStems || p.randomFolds != 0 || p.seed != stDefaultSeed || p.homodimerOnly
	}
	return changed
}

// viennaAdvanced: the ViennaRNA settings, then the hard pairs (ViennaRNA
// MFE) or the evidence settings (COMRADES).
func (p *structurePage) viennaAdvanced(c *ui.Context) {
	t := c.Theme()
	changed := p.viennaSettingsChanged()
	comrades := p.engine == engineComrades
	ui.Collapsible(c.Key("st-vienna"), "ViennaRNA settings", &p.advancedOpen, func() {
		ui.Column(c).Gap(10).Padding(6, 0, 0, 0).Children(func() {
			row := func(key, label string, value *float64, lo, hi, step float64, help string) {
				ui.Column(c).Gap(2).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, label).FontSize(12).Bold().Width(150)
						ui.NumberInput(c.Key(key), value, lo, hi, step).Grow(1).Label(label)
					})
					ui.Text(c, help).FontSize(12).TextColor(t.TextMuted)
				})
			}
			row("st-temp", "Temperature (°C)", &p.temperature, 0, 100, 1, "Folding temperature, 0 to 100 °C. Default 37.")
			row("st-loop", "Minimum hairpin loop", &p.minLoop, 0, 12, 1, "Fewest unpaired bases closing a hairpin, 0 to 12. Default 3.")
			if comrades {
				row("st-stems", "Evidence stem constraints", &p.stems, 1, 75, 1, "Strongest RNAcofold stems tried as hard pairs, 1 to 75. Default 75.")
				row("st-random", "Randomised folds", &p.randomFolds, 0, 1000, 1,
					"Extra fits with the stems in seeded random orders, 0 to 1000; each takes about as long as the ranked fit. Default 0.")
				if p.randomFolds > stLargeEnsemble {
					ui.Checkbox(c.Key("st-large"), &p.allowLargeEnsemble,
						"I understand that up to 1,000 serial folds may take a long time; Cancel stops them.")
				}
				Field(c, "Seed", func() {
					ui.TextInput(c.Key("st-seed"), &p.seed).Label("Seed").Width(200)
					ui.Text(c, "The same seed gives the same random orders. Default HYB2-Web.").FontSize(12).TextColor(t.TextMuted)
				})
				ui.Checkbox(c.Key("st-homodimer"), &p.homodimerOnly, "Homodimer proxies only (both arms on one RNA, overlap ≥ 5)")
			}
			if ui.Button(c, "Reset to defaults").Disabled(!changed).Clicked() {
				p.temperature, p.minLoop = stDefaultTemp, stDefaultLoop
				p.stems, p.randomFolds, p.seed, p.homodimerOnly, p.allowLargeEnsemble = stDefaultStems, 0, stDefaultSeed, false, false
			}
		})
	})
	if comrades {
		summary := fmt.Sprintf("%g °C · minimum loop %d · %d stems · %d randomised folds · seed %s",
			p.temperature, int(p.minLoop), int(p.stems), int(p.randomFolds), p.seed)
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
		ui.Text(c, summary).FontSize(12).TextColor(color)
		return
	}
	Field(c, "Hard base pairs (optional): i-j, one per line, RNA positions", func() {
		ui.TextArea(c.Key("st-hard"), &p.hardPairs).Lines(2, 6).Font("monospace").Placeholder("e.g.\n120-188").Label("Hard base pairs")
		ui.Text(c, "Each pair is enforced: A-U, G-C or G-U, not crossing, at least the minimum loop apart.").FontSize(12).TextColor(t.TextMuted)
	})
	summary := fmt.Sprintf("%g °C · minimum loop %d", p.temperature, int(p.minLoop))
	color := t.TextMuted
	if changed {
		summary += " · changed from the defaults"
		color = t.Warning
	} else {
		summary += " · defaults"
	}
	ui.Text(c, summary).FontSize(12).TextColor(color)
}
