package views

// ⑤ Structure, the ViennaRNA engine: the web RNA Structure page's ViennaRNA
// MFE mode (temperature, minimum hairpin loop, manual hard base pairs), run
// as a structure task with engine "viennarna" (desktop/runtime/vienna.py).

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	engineCPLfold = "cplfold"
	engineVienna  = "viennarna"
	// the web's ViennaRNA defaults
	stDefaultTemp = 37
	stDefaultLoop = 3
	// a measured ViennaRNA MFE of 10,807 nt (time is cubic in the length,
	// memory quadratic), for the check before running
	viennaRefNT, viennaRefSeconds, viennaRefMB = 10_807, 285.0, 670.0
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
	engine := engineVienna
	pl.req.Engine = &engine
	pairs, errs := stParseHardPairs(p.hardPairs)
	pl.problems = append(pl.problems, errs...)
	list := make([][2]int, len(pairs))
	copy(list, pairs)
	pl.req.Parameters = map[string]any{"temperature": p.temperature, "min_loop": int(p.minLoop), "hard_pairs": list}
	pl.pk = false
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
	pl.memMB = 0
	for _, n := range pl.lengths {
		r := float64(n) / viennaRefNT
		pl.memMB = max(pl.memMB, uint64(60+viennaRefMB*r*r))
	}
	pl.req.MemMB = &pl.memMB
	one := uint32(1)
	pl.req.Workers = &one
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
		total += viennaRefSeconds * math.Pow(float64(n)/viennaRefNT, 3)
	}
	return total
}

func (p *structurePage) viennaSettingsChanged() bool {
	return p.temperature != stDefaultTemp || int(p.minLoop) != stDefaultLoop
}

// viennaAdvanced: the ViennaRNA settings and the hard pairs.
func (p *structurePage) viennaAdvanced(c *ui.Context) {
	t := c.Theme()
	changed := p.viennaSettingsChanged()
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
			if ui.Button(c, "Reset to defaults").Disabled(!changed).Clicked() {
				p.temperature, p.minLoop = stDefaultTemp, stDefaultLoop
			}
		})
	})
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
