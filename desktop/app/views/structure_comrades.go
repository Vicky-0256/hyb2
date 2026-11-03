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

// stComradesStages are the web's stage cards.
func stComradesStages(c *ui.Context, cm *stComrades) {
	t := c.Theme()
	ev, rnd := &cm.Evidence, &cm.Randomisation
	enforced := 0
	for _, s := range ev.Accepted {
		enforced += s.Length
	}
	folds := max(len(rnd.Structures)-1, 0)
	stage := func(title, value, note string) {
		ui.Column(c).Gap(2).Padding(10, 12).Radius(8).Background(t.Surface).Grow(1).Basis(180).MinWidth(170).Children(func() {
			ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted)
			ui.Text(c, value).FontSize(15).Bold()
			ui.Text(c, note).FontSize(11).TextColor(t.TextMuted)
		})
	}
	ui.Row(c).Gap(10).Wrap().Children(func() {
		stage("1 · RNAcofold evidence", stCountLabel(ev.UniqueBasePairs, "unique pair"), stCountLabel(ev.InputRecords, "eligible HYB row")+"; one observation per row")
		stage("2 · Ranked stems", stCountLabel(ev.RankedStems, "stem"), "Top "+FormatNumber(int64(ev.RequestedStemConstraints))+" sent to compatibility fitting")
		stage("3 · Accepted constraints", stCountLabel(len(ev.Accepted), "stem"), stCountLabel(enforced, "enforced base pair"))
		stage("4 · Structure ensemble", stCountLabel(folds+1, "structure"), "ranked fold + "+stCountLabel(folds, "seeded random order"))
	})
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
	ui.Column(c).Gap(4).Role(ui.RoleGroup).Label("Top COMRADES structure scores").Children(func() {
		for k, e := range structures[:min(12, len(structures))] {
			selected := e.Run == rnd.SelectedRun
			row := ui.Row(c.Key(fmt.Sprint("bar", k))).Gap(10).AlignItems(ui.Center).Padding(2, 6).Radius(6)
			if selected {
				row.Background(t.Accent.Alpha(0.12))
			}
			row.Children(func() {
				label := ui.Text(c, stEnsembleBarLabel(k+1, e)).FontSize(12).Width(130).SingleLine()
				fraction := float32(math.Max(2, e.Score/top*100) / 100)
				ui.Box(c).Height(8).Grow(1).Radius(4).Background(t.Border).Draw(func(p *ui.Painter, r ui.Rect) {
					fill := t.Accent.Alpha(0.6)
					if selected {
						fill = t.Accent
					}
					p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * min(1, fraction), H: r.H}, fill, 4)
				})
				value := ui.Text(c, jsNumber(e.Score)).FontSize(12).Bold().Width(70).TextAlign(ui.End)
				if selected {
					label.Bold()
					value.TextColor(t.Accent)
				}
			})
		}
	})
}
