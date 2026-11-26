package views

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// The web's COMRADES result: its four stage cards and the bar chart of
// the top 12 COMRADES scores, the selected run marked.

// stCountLabel is the web's countLabel: "1 stem", "3 stems".
func stCountLabel(n int, noun string) string {
	n = max(n, 0)
	return FormatNumber(int64(n)) + " " + noun + plural(n)
}

// stComradesStages are the web's stage cards: title, value and note.
func stComradesStages(cm *stComrades) [][3]string {
	ev, rnd := &cm.Evidence, &cm.Randomisation
	enforced := 0
	for _, s := range ev.Accepted {
		enforced += s.Length
	}
	folds := max(len(rnd.Structures)-1, 0)
	return [][3]string{
		{"1 · RNAcofold evidence", stCountLabel(ev.UniqueBasePairs, "unique pair"), stCountLabel(ev.InputRecords, "eligible HYB row") + "; one observation per row"},
		{"2 · Ranked stems", stCountLabel(ev.RankedStems, "stem"), "Top " + FormatNumber(int64(ev.RequestedStemConstraints)) + " sent to compatibility fitting"},
		{"3 · Accepted constraints", stCountLabel(len(ev.Accepted), "stem"), stCountLabel(enforced, "enforced base pair")},
		{"4 · Structure ensemble", stCountLabel(folds+1, "structure"), "ranked fold + " + stCountLabel(folds, "seeded random order")},
	}
}

// stEnsembleBarLabel is a bar's label: "#k · ranked" or "#k · random r".
func stEnsembleBarLabel(k int, e stEnsembleEntry) string {
	what := "ranked"
	if e.Randomised {
		what = "random " + strconv.Itoa(e.Run)
	}
	return fmt.Sprintf("#%d · %s", k, what)
}

// stComradesBars are the web's "Top COMRADES structure scores": the
// ensemble's best 12, bars as long as the score over the best, the fold
// shown marked.
func stComradesBars(c *ui.Context, cm *stComrades) {
	t := c.Theme()
	rnd := &cm.Randomisation
	if len(rnd.Structures) == 0 {
		return
	}
	// as the web: score, then lower energy, then earlier run
	structures := slices.Clone(rnd.Structures)
	energy := func(e stEnsembleEntry) float64 {
		if e.Energy == nil {
			return math.Inf(1)
		}
		return *e.Energy
	}
	slices.SortStableFunc(structures, func(x, y stEnsembleEntry) int {
		switch {
		case x.Score != y.Score:
			return cmp.Compare(y.Score, x.Score)
		case energy(x) != energy(y):
			return cmp.Compare(energy(x), energy(y))
		}
		return cmp.Compare(x.Run, y.Run)
	})
	top := math.Max(1, structures[0].Score)
	ui.Column(c).Gap(Space1).Role(ui.RoleGroup).Label("Top COMRADES structure scores").Children(func() {
		for k, e := range structures[:min(12, len(structures))] {
			selected := e.Run == rnd.SelectedRun
			row := ui.Row(c.Key(fmt.Sprint("bar", k))).Gap(Space3).AlignItems(ui.Center).Padding(2, 6).Radius(RadiusSmall)
			if selected {
				row.Background(t.Accent.Alpha(0.12))
			}
			row.Children(func() {
				label := ui.Text(c, stEnsembleBarLabel(k+1, e)).FontSize(FontSmall).Width(130).SingleLine()
				fraction := float32(math.Max(2, e.Score/top*100) / 100)
				ui.Box(c).Height(8).Grow(1).Radius(4).Background(t.Border).Draw(func(p *ui.Painter, r ui.Rect) {
					fill := t.Accent.Alpha(0.6)
					if selected {
						fill = t.Accent
					}
					p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * min(1, fraction), H: r.H}, fill, 4)
				})
				value := ui.Text(c, jsNumber(e.Score)).FontSize(FontSmall).Bold().Width(70).TextAlign(ui.End)
				if selected {
					label.Bold()
					value.TextColor(t.Accent)
				}
			})
		}
	})
}
