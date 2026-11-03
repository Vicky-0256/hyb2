package views

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// The chimera patterns as the web names and draws them (web/ui-kit.js
// chimeraPatterns): each type's HYB2 code, its name, where its two arms lie
// in the 270 x 62 diagram, and what it means.

type chimeraPattern struct {
	code, name string
	spans      [4]float32 // 5′ arm start, end, 3′ arm start, end
	meaning    string
}

var chimeraPatterns = []chimeraPattern{
	{"Type_1", "Separated · 5′ arm first", [4]float32{20, 85, 125, 195}, "Same RNA; the first arm ends before the second starts."},
	{"Type_2", "Separated · 3′ arm first", [4]float32{125, 195, 20, 85}, "Same RNA; the first arm starts after the second ends."},
	{"Type_3", "Overlap · 5′ arm starts first", [4]float32{20, 130, 85, 195}, "Same RNA; the first arm starts earlier and ends inside the second."},
	{"Type_4", "Shared end · 5′ arm longer", [4]float32{20, 195, 85, 195}, "Same RNA; the first arm starts earlier; both end together."},
	{"Type_5", "5′ arm contains 3′ arm", [4]float32{20, 195, 70, 145}, "Same RNA; the second interval lies strictly inside the first."},
	{"Type_6", "Shared start · 5′ arm longer", [4]float32{20, 195, 20, 125}, "Same RNA; both start together; the first ends later."},
	{"Type_7", "Overlap · 3′ arm starts first", [4]float32{85, 195, 20, 130}, "Same RNA; the second arm starts earlier and ends inside the first."},
	{"Type_8", "Identical intervals", [4]float32{20, 195, 20, 195}, "Same RNA; both start and end coordinates match."},
	{"Type_9", "Shared end · 3′ arm longer", [4]float32{85, 195, 20, 195}, "Same RNA; the second arm starts earlier; both end together."},
	{"Type_10", "Shared start · 3′ arm longer", [4]float32{20, 125, 20, 195}, "Same RNA; both start together; the second ends later."},
	{"Type_11", "3′ arm contains 5′ arm", [4]float32{70, 145, 20, 195}, "Same RNA; the first interval lies strictly inside the second."},
	{"Type_12", "Touching · 5′ arm first", [4]float32{20, 105, 105, 195}, "Same RNA; the first arm's end equals the second arm's start."},
	{"Type_13", "Touching · 3′ arm first", [4]float32{105, 195, 20, 105}, "Same RNA; the first arm's start equals the second arm's end."},
	{"Intermolecular", "Different RNAs", [4]float32{20, 145, 70, 195}, "The two arms map to different RNA identifiers."},
}

// chimeraPatternOf is a chimera type's pattern ("Type 3" and "type3" read
// as Type_3), or false for a type the web does not name.
func chimeraPatternOf(code string) (chimeraPattern, bool) {
	key := code
	if rest, ok := strings.CutPrefix(strings.ToLower(code), "type"); ok {
		key = "Type_" + strings.TrimLeft(rest, "_ ")
	}
	for _, p := range chimeraPatterns {
		if p.code == key {
			return p, true
		}
	}
	return chimeraPattern{}, false
}

// patternName is a chimera type's name, or the type itself.
func patternName(code string) string {
	if p, ok := chimeraPatternOf(code); ok {
		return p.name
	}
	return code
}

// patternCode is how a chimera type's code reads: "Type 3",
// "Intermolecular".
func patternCodeLabel(code string) string {
	if p, ok := chimeraPatternOf(code); ok {
		code = p.code
	}
	return strings.ReplaceAll(code, "_", " ")
}

// patternCode is the chimera type of a name in a picker.
func patternCode(name string, types []string) string {
	if name == allPattern {
		return allTypes
	}
	for _, code := range types {
		if patternName(code) == name {
			return code
		}
	}
	return name
}

var (
	diagramArm1 = ui.RGB(0x3c, 0x54, 0x88)
	diagramArm2 = ui.RGB(0xe6, 0x4b, 0x35)
)

// chimeraDiagram draws a pattern's two arms on the reference, as the web's
// chimeraDiagram, width DIPs wide; nothing for a type the web does not name.
func chimeraDiagram(c *ui.Context, code string, width float32) {
	p, ok := chimeraPatternOf(code)
	if !ok {
		return
	}
	t := c.Theme()
	k := width / 270
	ui.Box(c).Size(width, 62*k).Role(ui.RoleImage).Label(p.name + ". " + p.meaning).Draw(func(pt *ui.Painter, r ui.Rect) {
		at := func(x, y float32) (float32, float32) { return r.X + x*k, r.Y + y*k }
		size := max(10*k, 9) // readable however small the diagram
		x, y := at(0, 11)
		pt.Text(x, y, "5′ arm", size, t.TextMuted)
		x, y = at(0, 35)
		pt.Text(x, y, "3′ arm", size, t.TextMuted)
		for _, line := range []float32{17, 41} {
			x0, y0 := at(65, line)
			x1, _ := at(265, line)
			pt.Line(x0, y0, x1, y0, 1, t.Text.Alpha(0.2))
		}
		bar := func(start, end, top float32, color ui.Color) {
			x, y := at(start+60, top)
			pt.Fill(ui.Rect{X: x, Y: y, W: (end - start) * k, H: 12 * k}, color, 3*k)
		}
		bar(p.spans[0], p.spans[1], 11, diagramArm1)
		bar(p.spans[2], p.spans[3], 35, diagramArm2)
		axis := "Reference position →"
		if p.code == "Intermolecular" {
			axis = "Separate RNA references"
		}
		x, y = at(65, 51)
		pt.Text(x, y, axis, size, t.TextMuted)
	})
}

// chimeraGuide is the web's "How to read these patterns": every pattern
// with its HYB2 code, name, diagram and meaning.
func chimeraGuide(c *ui.Context, open *bool) {
	t := c.Theme()
	ui.Collapsible(c.Key("chimera-guide"), "How to read these patterns", open, func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, "5′ and 3′ identify the two arms in the read. Their positions are shown on the reference, not as a predicted fold. Shared boundaries follow the source coordinate comparisons exactly.").
				FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(12).Wrap().Children(func() {
				for _, p := range chimeraPatterns {
					ui.Column(c.Key(p.code)).Gap(4).Padding(12).Radius(6).Border(1, t.Border).Grow(1).Basis(230).MinWidth(220).Children(func() {
						ui.Text(c, patternCodeLabel(p.code)).FontSize(11).Bold().TextColor(t.TextMuted)
						ui.Text(c, p.name).Bold()
						chimeraDiagram(c, p.code, 230)
						ui.Text(c, p.meaning).FontSize(12).TextColor(t.TextMuted)
					})
				}
			})
			ui.Text(c, "Source: HYB2 bin/chim_types and bin/hyb_chim_types.awk (Lau). Each pattern shows its HYB2 classification code (Type 1 to Type 13, Intermolecular), the code the data exports, the .hyb files and HYB2 Desktop use.").
				FontSize(12).TextColor(t.TextMuted)
		})
	})
}

// patternPicker is the web's "Chimera pattern" drop-down: the patterns by
// name, *code keeping the chosen type's code (allTypes for all of them).
func (p *explorePage) patternPicker(c *ui.Context, key string, code *string, width float32, changed func()) {
	names := make([]string, len(p.summary.types))
	for i, t := range p.summary.types {
		names[i] = patternName(t)
	}
	shown := allPattern
	if *code != "" && *code != allTypes {
		shown = patternName(*code)
	}
	picker(c, key, "Chimera pattern", &shown, withAll(allPattern, names), width).OnChange(func() {
		*code = patternCode(shown, p.summary.types)
		if changed != nil {
			changed()
		}
	})
}
