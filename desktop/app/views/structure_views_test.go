package views

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/widgets"
)

func stFloat64s(values ...float64) []*float64 {
	out := make([]*float64, len(values))
	for i := range values {
		out[i] = &values[i]
	}
	return out
}

// The web's structurePairStyle, legend, marker colours and tooltips, per
// engine.
func TestStructureViewStylesByEngine(t *testing.T) {
	th := ui.LightTheme()
	// COMRADES: pairs and bases by RNAcofold evidence (0.55 of the best is high)
	cm := &structureResult{}
	cm.Meta.Engine = "COMRADES(ViennaRNA)"
	cm.Records = []structureRecord{{WindowStart: 1, WindowEnd: 10, Sequence: "GGGAAACCCU", DBN: "(((...))).",
		Pairs: [][2]int{{1, 9}, {2, 8}, {3, 7}}, Bonus: stFloat64s(5, 1, 0)}}
	pairs := stShownPairs(cm.Records, map[[2]int]bool{{1, 9}: true})
	styles := stViewStyles(cm, cm.Records, pairs, th)
	want := map[[2]int]widgets.PairStyle{
		{1, 9}: {Color: th.Warning, Alpha: 0.9, Width: 3.7},
		{2, 8}: {Color: th.Accent, Alpha: 0.9, Width: 2.5},
		{3, 7}: {Color: th.TextMuted, Alpha: 0.34, Width: 2.1},
	}
	for i, pr := range pairs {
		if styles[i] != want[[2]int{pr.I, pr.J}] {
			t.Fatalf("COMRADES pair %d–%d: %+v", pr.I, pr.J, styles[i])
		}
	}
	marks := stViewMarks(cm, cm.Records, pairs, 1, 10, th)
	if marks[0] != th.Warning || marks[1] != th.Accent || marks[2] != th.TextMuted || marks[3] != th.TextMuted || marks[8] != th.Warning {
		t.Fatalf("COMRADES marks %v", marks)
	}
	if got := stPairTitle(cm, pairs[2]); got != "HYB-fitted hard pair: 1 G paired with 9 C (G–C); RNAcofold evidence 5" {
		t.Fatalf("title %q", got)
	}
	if got := stMarkerTitle(cm.Records, pairs, 2); got != "2 G: paired with 8 (G–C)" {
		t.Fatalf("marker %q", got)
	}
	if got := stMarkerTitle(cm.Records, pairs, 5); got != "5 A: unpaired" {
		t.Fatalf("marker %q", got)
	}
	if got := stLegendItems(cm, th); len(got) != 3 || got[0].Label != "high evidence" || got[2].Label != "MFE only" {
		t.Fatalf("legend %+v", got)
	}

	// CPLfold with HYB evidence: layers, dashes, opacity by bonus; bases by
	// their strongest bonus
	cpl := &structureResult{}
	if err := json.Unmarshal([]byte(`{"meta": {"engine": "CPLfold", "parameters": {"allow_pseudoknot": true},
		"records": [{"evidence": {"source": "hyb-block-intervals"}}]}}`), cpl); err != nil {
		t.Fatal(err)
	}
	cpl.Records = []structureRecord{{WindowStart: 1, WindowEnd: 9, Sequence: "GGAGACCAC", DBN: "((.[.)).]",
		Pairs: [][2]int{{1, 7}, {2, 6}, {4, 9}}, Bonus: stFloat64s(2, 0, 1),
		NucleotideSupport: []float64{2, 0.5, 0, 1.2, 0, 0.5, 2, 0, 1}}}
	pairs = stShownPairs(cpl.Records, nil)
	styles = stViewStyles(cpl, cpl.Records, pairs, th)
	for i, pr := range pairs {
		s := styles[i]
		switch [2]int{pr.I, pr.J} {
		case [2]int{1, 7}:
			if s.Color != th.Accent || s.Dash || s.Alpha != 0.96 || s.Width != 2.1 {
				t.Fatalf("primary %+v", s)
			}
		case [2]int{2, 6}:
			if s.Alpha != 0.32 {
				t.Fatalf("no bonus %+v", s)
			}
		case [2]int{4, 9}:
			if s.Color != stViolet || !s.Dash || s.Width != 3 || math.Abs(s.Alpha-0.64) > 1e-9 {
				t.Fatalf("pseudoknot %+v", s)
			}
		}
	}
	if got := stPairTitle(cpl, pairs[len(pairs)-1]); got != "pseudoknot-1 CPLfold pair: 4 G paired with 9 C (G–C); HYB bonus 1.0000" {
		t.Fatalf("title %q", got)
	}
	marks = stViewMarks(cpl, cpl.Records, pairs, 1, 9, th)
	// 1.2 / 2 = 0.6 ≥ 0.55: high; 1 / 2 low; 0 none
	if marks[0] != th.Warning || marks[3] != th.Warning || marks[8] != th.Accent || marks[2] != th.TextMuted {
		t.Fatalf("CPLfold marks %v", marks)
	}
	if got := stLegendItems(cpl, th); len(got) != 3 || got[1].Label != "pseudoknot" || got[2].Label != "deeper layer" {
		t.Fatalf("legend %+v", got)
	}

	// ViennaRNA: by pair type, hard pairs thickest, plain markers
	vr := &structureResult{}
	vr.Meta.Engine = "ViennaRNA"
	vr.Records = []structureRecord{{WindowStart: 1, WindowEnd: 8, Sequence: "GAGAACUU", DBN: "(((..)))"}}
	pairs = stShownPairs(vr.Records, map[[2]int]bool{{1, 8}: true})
	styles = stViewStyles(vr, vr.Records, pairs, th)
	for i, pr := range pairs {
		s := styles[i]
		switch [2]int{pr.I, pr.J} {
		case [2]int{1, 8}: // G–U, hard
			if s.Color != th.TextMuted || s.Width != 3.7 {
				t.Fatalf("hard G–U %+v", s)
			}
		case [2]int{2, 7}: // A–U
			if s.Color != stViolet || s.Width != 1.6 {
				t.Fatalf("A–U %+v", s)
			}
		}
	}
	if stViewMarks(vr, vr.Records, pairs, 1, 8, th) != nil {
		t.Fatal("ViennaRNA markers coloured by evidence")
	}
	if got := stPairTitle(vr, pairs[2]); !strings.HasPrefix(got, "Manual hard pair: 1 G paired with 8 U") {
		t.Fatalf("title %q", got)
	}
}

// One strand opens in the folded view, which zooms, pans, expands and
// inspects a nucleotide as the web's forna view; its export carries the
// web's styles and legend in light colours.
func TestStructureFoldedViewInteraction(t *testing.T) {
	a, f := openCS(t)
	read := fixture(t, "structure_read.json")
	record := read["records"].([]any)[0].(map[string]any)
	n := len(record["sequence"].(string))
	support := make([]any, n)
	for i := range support {
		support[i] = float64(i % 7)
	}
	record["nucleotide_support"] = support
	f.structures[structA] = read
	xs, ys := make([]any, n), make([]any, n)
	for i := range xs {
		angle := 2 * math.Pi * float64(i) / float64(n)
		xs[i], ys[i] = 1000*math.Cos(angle), 1000*math.Sin(angle)
	}
	f.ops["structure_layout"] = core.Obj{"ok": true, "x": xs, "y": ys}
	tt := ui.NewTester(a.View, 1100, 1800)
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.view != "Folded" {
		t.Fatalf("view %q", p.view)
	}
	for _, text := range []string{"Folded RNA structure", "Zoom in", "Zoom out", "Fit to view", "Full screen", stFoldHint,
		"phase 1", "pseudoknot", "deeper layer", "Click a nucleotide to inspect it.", "NUCLEOTIDE INSPECTOR", "Base-pair list (1,055)"} {
		if !tt.HasText(text) {
			t.Fatalf("folded view lacks %q: %q", text, tt.Texts())
		}
	}
	for _, step := range []struct {
		button string
		scale  float64
	}{{"Zoom in", 1.25}, {"Zoom in", 1.5625}, {"Zoom out", 1.25}, {"Fit to view", 1}} {
		if err := tt.Click(step.button); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if math.Abs(p.sv.zoom.Scale-step.scale) > 1e-9 {
			t.Fatalf("%s: scale %v", step.button, p.sv.zoom.Scale)
		}
	}
	snapshot(t, tt, "structure-folded-default")
	// click the base at the bottom of the circle layout (a quarter round)
	r, ok := tt.Find("Folded view of the base pairs")
	if !ok {
		t.Fatal("no folded view")
	}
	side := min(r.W, r.H)
	x, y := r.X+(r.W-side)/2+side*0.5, r.Y+side*0.95
	tt.Move(x, y)
	tt.Frame()
	if !slices.ContainsFunc(tt.Texts(), regexp.MustCompile(`^\d+ [ACGU]: (paired with \d+ \([ACGU]–[ACGU]\)|unpaired)$`).MatchString) {
		t.Fatalf("no nucleotide readout: %q", tt.Texts())
	}
	tt.Press(x, y)
	tt.Release(x, y)
	tt.Frame()
	tt.Frame()
	want := 3170 + n/4
	if sel := p.arcs.Selected; sel < want-25 || sel > want+25 {
		t.Fatalf("selected %d, want about %d", sel, want)
	}
	if !tt.HasText("Nucleotide " + FormatNumber(int64(p.arcs.Selected))) {
		t.Fatalf("inspector: %q", tt.Texts())
	}
	// the view has the keyboard: + zooms, the arrows pan, 0 fits
	tt.Key(ui.Shift, ui.KeyEqual)
	tt.Frame()
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if p.sv.zoom.Scale != 1.25 || math.Abs(p.sv.zoom.X-(-0.125+widgets.PanStep/float64(side))) > 1e-6 {
		t.Fatalf("keys: %+v", p.sv.zoom)
	}
	tt.Key(0, ui.Key0)
	tt.Frame()
	if !p.sv.zoom.Fitted() {
		t.Fatalf("0: %+v", p.sv.zoom)
	}
	tt.Scroll(x, y-side*0.45, 0, -200)
	tt.Frame()
	if p.sv.zoom.Scale <= 1 {
		t.Fatalf("wheel: %+v", p.sv.zoom)
	}
	tt.SetDark(true)
	tt.Frame()
	snapshot(t, tt, "structure-folded-zoomed-dark")
	tt.SetDark(false)
	// Full screen: the expanded view, which Escape leaves
	if err := tt.Click("Full screen"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !p.sv.expanded || !tt.HasText("Exit expanded view") || !tt.HasText(stExpandedHint) {
		t.Fatalf("expanded %v: %q", p.sv.expanded, tt.Texts())
	}
	// drawn once: the page shows where it went
	if _, inline := tt.Find("Folded view of the base pairs"); inline {
		t.Fatalf("drawn twice: %q", tt.Texts())
	}
	if _, ok := tt.Find("Expanded folded view of the base pairs"); !ok {
		t.Fatalf("expanded views: %q", tt.Texts())
	}
	snapshot(t, tt, "structure-folded-expanded")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if p.sv.expanded {
		t.Fatal("Escape did not leave the expanded view")
	}
	// the export: fitted, the web's styles and legend, light colours
	_, svg := exportClick(t, a, tt, "Export SVG…")
	xmlWellFormed(t, svg)
	light := ui.LightTheme()
	for _, want := range []string{">phase 1</text>", ">pseudoknot</text>", ">deeper layer</text>", stHex(light.Accent)} {
		if !strings.Contains(svg, want) {
			t.Fatalf("folded svg lacks %q", want)
		}
	}
	if strings.Contains(svg, stHex(ui.DarkTheme().Surface)) {
		t.Fatal("dark colours in the export")
	}
	// the other views share the selection and the styles
	for _, view := range []string{"Circular", "Radial", "Matrix"} {
		p.view = view
		tt.Frame()
		if !tt.HasText(stViewTitles[stViewModes[view]]) || !tt.HasText("Click along the ring, or focus it and use the arrow keys, to inspect a nucleotide.") &&
			!tt.HasText("Click along the track under the matrix, or focus it and use the arrow keys, to inspect a nucleotide.") {
			t.Fatalf("%s: %q", view, tt.Texts())
		}
		snapshot(t, tt, "structure-styled-"+strings.ToLower(view))
	}
	p.view = "Radial"
	tt.Frame()
	sel := p.arcs.Selected
	if err := tt.Click("Radial view of the base pairs"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if p.arcs.Selected != sel+1 {
		t.Fatalf("Right stepped %d to %d", sel, p.arcs.Selected)
	}
}

func stHex(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
