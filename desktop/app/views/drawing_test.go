package views

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// drawGolden is one case of testdata/drawing_golden.json, written by the
// web editor (testdata/drawing_golden.js): a prediction, the drawing made
// from it (and edited), its layouts and the SHA-256 of each export.
type drawGolden struct {
	Name   string `json:"name"`
	Result struct {
		Sequence   string     `json:"sequence"`
		DotBracket string     `json:"dotBracket"`
		Pairs      []drawPair `json:"pairs"`
		Label      string     `json:"label"`
		Cutpoint   int        `json:"cutpoint"`
		Evidence   *struct {
			Pairs []stCount `json:"pairs"`
		} `json:"comradesEvidence"`
	} `json:"result"`
	Drawing json.RawMessage         `json:"drawing"`
	Layouts map[string][][2]float64 `json:"layouts"`
	SHA     map[string]string       `json:"sha"`
}

func drawGoldens(t *testing.T) []drawGolden {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "drawing_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []drawGolden
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// source is the case's prediction, with COMRADES support scored as the
// web's comradesSupport (RNAcofold counts of the pair each base forms).
func (g drawGolden) source() *drawingSource {
	r := g.Result
	src := &drawingSource{Sequence: r.Sequence, DBN: r.DotBracket, Pairs: drSortedPairs(r.Pairs), Label: r.Label, Cutpoint: r.Cutpoint}
	if r.Evidence != nil {
		counts := map[[2]int]float64{}
		for _, e := range r.Evidence.Pairs {
			counts[[2]int{min(e.One, e.Two), max(e.One, e.Two)}] += float64(e.Count)
		}
		src.Support = make([]float64, len(r.Sequence))
		for _, p := range r.Pairs {
			if c := counts[[2]int{p.Left, p.Right}]; c > 0 {
				src.Support[p.Left-1], src.Support[p.Right-1] = c, c
			}
		}
		src.EvidencePairs = len(r.Evidence.Pairs)
	}
	return src
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return nearlyEqual(x, y)
}

// nearlyEqual compares decoded JSON, numbers to 1e-9 (Go's and
// JavaScript's sine and arctangent may differ in the last bit).
func nearlyEqual(x, y any) bool {
	switch x := x.(type) {
	case float64:
		y, ok := y.(float64)
		return ok && math.Abs(x-y) <= 1e-9*math.Max(1, math.Abs(x))
	case []any:
		y, ok := y.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !nearlyEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := y.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if !nearlyEqual(v, y[k]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(x, y)
}

// The port writes the web editor's files byte for byte: SVG (with and
// without a selection), EPS, CT, BPSEQ, DBN, the VARNA command and the
// drawing JSON; its layouts (RADIATE flat and round, the ViennaRNA
// polygon, circular, linear) put every base where the web's do; and a new
// drawing (with COMRADES evidence colours) is the web's.
func TestDrawingMatchesWeb(t *testing.T) {
	for _, g := range drawGoldens(t) {
		var d drawing
		if err := json.Unmarshal(g.Drawing, &d); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{"svg": drawingSVG(&d, nil, nil), "svgSel": drawingSVG(&d, &[2]int{3, 7}, nil), "eps": drawingEPS(&d),
			"ct": drawingCT(&d), "bpseq": drawingBPSEQ(&d)}
		got["dbn"], _ = drawingDBN(&d)
		got["varna"], _ = drawingVARNA(&d)
		for key, want := range g.SHA {
			if key == "json" {
				continue
			}
			if sha(got[key]) != want {
				t.Errorf("%s: %s differs from the web's (%.200q)", g.Name, key, got[key])
			}
		}
		// the JSON holds the same values (Go orders the keys its own way)
		out, err := drawingJSON(&d)
		if err != nil || !jsonEqual(t, out, g.Drawing) {
			t.Errorf("%s: drawing JSON %v %.300s", g.Name, err, out)
		}
		for key, want := range g.Layouts {
			parts := strings.Split(key, "/")
			var spacing float64
			fmt.Sscan(parts[2], &spacing)
			points := drawLayoutPoints(len(d.Sequence), d.Pairs, parts[0], spacing, parts[1] == "true")
			for i, p := range want {
				if math.Abs(points[i].X-p[0]) > 1e-5 || math.Abs(points[i].Y-p[1]) > 1e-5 {
					t.Errorf("%s %s: base %d at %v, the web's at %v", g.Name, key, i+1, points[i], p)
					break
				}
			}
		}
		switch g.Name {
		case "plain", "evidence":
			first := initialDrawing(g.source())
			if g.Name == "evidence" {
				first.Title = "Ev"
			}
			out, _ := drawingJSON(first)
			if !jsonEqual(t, out, g.Drawing) {
				t.Errorf("%s: the first drawing differs from the web's: %.400s", g.Name, out)
			}
		}
	}
}

// The web's messages, verbatim.
func TestDrawingMessages(t *testing.T) {
	g := drawGoldens(t)[0]
	d := initialDrawing(g.source())
	errText := func(err error) string {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	values := func(text string, n int) string {
		v, err := drParseValues(text, n)
		if err != nil {
			return err.Error()
		}
		out := make([]string, len(v))
		for i, x := range v {
			out[i] = "null"
			if x != nil {
				out[i] = drJsNum(*x)
			}
		}
		return strings.Join(out, ",")
	}
	for _, c := range [][2]string{
		{values("1 2\n3", 3), "1,2,3"},
		{values("1 2 3", 4), `Enter 4 values (one per base) or "position value" lines; found 3.`},
		{values("1 x 3", 3), "Value 2 is not a number."},
		{values("4 1\n1 2", 3), `Each "position value" line needs a position from 1 to 3 and a number.`},
		{values("# only", 3), `Enter 3 values (one per base) or "position value" lines; found 0.`},
		{values("0x10,2;3", 3), "16,2,3"},
		{values("2 5\n# c\n3 1.5", 3), "null,5,1.5"},
	} {
		if c[0] != c[1] {
			t.Errorf("parse values: %q, want %q", c[0], c[1])
		}
	}
	notes, err := drAutoAnnotations(d, "hairpins")
	if err != nil || len(notes) != 5 || notes[1].Text != "Hairpin 2" || notes[1].Base != 14 || !reflect.DeepEqual(notes[1].Bases, []int{14, 15, 16, 17}) {
		t.Fatalf("hairpins %v %+v", err, notes)
	}
	crossed := d.clone()
	crossed.Pairs = append(crossed.Pairs, drawPair{Left: 4, Right: 12, Color: "#3C5488", Width: 2})
	if _, err := drAutoAnnotations(crossed, "hairpins"); errText(err) != "Automatic hairpin labels require non-crossing pairs." {
		t.Fatal(err)
	}
	if _, err := drHelixOf(d, 4); errText(err) != "Base 4 is unpaired; choose a paired base to anchor a helix annotation." {
		t.Fatal(err)
	}
	if h, _ := drHelixOf(d, 2); !reflect.DeepEqual(h, []int{1, 9, 2, 8, 3, 7}) {
		t.Fatal(h)
	}
	if _, err := drLoopOf(d, 81); errText(err) != "Base 81 lies in the exterior loop; choose a base inside a loop." {
		t.Fatal(err)
	}
	if l, _ := drLoopOf(d, 5); !reflect.DeepEqual(l, []int{3, 4, 5, 6, 7}) {
		t.Fatal(l)
	}
	if l, _ := drLoopOf(d, 1); !reflect.DeepEqual(l, []int{1, 2, 8, 9}) {
		t.Fatal(l)
	}
	if r, ok := drDomainOf(d, 5); !ok || r != [2]int{3, 7} {
		t.Fatal(r)
	}
	if _, ok := drDomainOf(d, 80); ok {
		t.Fatal("domain in the exterior loop")
	}
	if r, ok := drParentOf(d, 13, 18); !ok || r != [2]int{12, 19} {
		t.Fatal(r)
	}
	cross := func(k int) []drawPair {
		var p []drawPair
		for i := 1; i <= k; i++ {
			p = append(p, drawPair{Left: i, Right: i + 5})
		}
		return p
	}
	if s, err := drawDotBracket(10, cross(4)); err != nil || s != "([{<.)]}>." {
		t.Fatal(s, err)
	}
	if _, err := drawDotBracket(10, cross(5)); errText(err) != "These pairs cross too often for bracket notation." {
		t.Fatal(err)
	}
	if err := drApplyEvidence(d.clone(), nil, "lau"); errText(err) != "This prediction has no experimental support scores." {
		t.Fatal(err)
	}
	if err := drColorMap(d.clone(), "1 2", drColorMapOptions{Minimum: "5", Maximum: "1", Palette: "lau"}); errText(err) != "The colour map needs a maximum above its minimum." {
		t.Fatal(err)
	}
	// a loaded drawing: another prediction, too large, broken pairs
	src := g.source()
	raw, _ := drawingJSON(d)
	if _, err := parseDrawingJSON(raw, src.Sequence+"A", src.DBN+"."); errText(err) != "This drawing belongs to a different prediction." {
		t.Fatal(err)
	}
	if _, err := parseDrawingJSON(make([]byte, 5_000_001), src.Sequence, src.DBN); errText(err) != "Drawing JSON exceeds 5 MB." {
		t.Fatal(err)
	}
	bad := d.clone()
	bad.Pairs = append(bad.Pairs, drawPair{Left: 1, Right: 80, Color: "#3C5488", Width: 2})
	raw, _ = drawingJSON(bad)
	if _, err := parseDrawingJSON(raw, src.Sequence, src.DBN); errText(err) != "Invalid or overlapping base pairs." {
		t.Fatal(err)
	}
	if got, err := parseDrawingJSON(func() []byte { b, _ := drawingJSON(d); return b }(), src.Sequence, src.DBN); err != nil || !reflect.DeepEqual(got, d) {
		t.Fatalf("round trip %v", err)
	}
}

// The editor's edits: pair validation, undo and redo, selection, styles,
// annotations and colour maps, each with the web's message.
func TestDrawingEditorEdits(t *testing.T) {
	g := drawGoldens(t)[0]
	e := newDrawingEditor(nil, g.source())
	if e.message != drawStatusUnchanged || e.data.TopologyEdited {
		t.Fatal(e.message)
	}
	sel := func(a, b int) { e.from, e.to = float64(a), float64(b) }
	// Pair the two ends: one base, a paired end, then a new pair
	sel(4, 4)
	e.act("pair")
	if e.message != "Select two or more bases: the first and last are paired." || len(e.undo) != 0 {
		t.Fatal(e.message)
	}
	sel(1, 5)
	e.act("pair")
	if e.message != "Base 1 or 5 is already paired. Remove its pair first." {
		t.Fatal(e.message)
	}
	sel(4, 52)
	e.act("pair")
	if e.message != "Pair 4–52 added. "+drawStatusEdited || !e.data.TopologyEdited || len(e.data.Pairs) != len(g.Result.Pairs)+1 || len(e.undo) != 1 {
		t.Fatalf("%q pairs %d", e.message, len(e.data.Pairs))
	}
	if s, _ := drawingDBN(e.data); !strings.Contains(s, "(((...)))") || !strings.HasPrefix(s, ">ZIKV:1–82 test (edited drawing; energy not recalculated)\n") {
		t.Fatal(s)
	}
	// the prediction is untouched
	if len(g.source().Pairs) != len(g.Result.Pairs) {
		t.Fatal("source changed")
	}
	// Remove pairs: none touches 74-81, then 1-9 has three
	sel(74, 81)
	e.act("unpair")
	if e.message != "No pair touches the selection." {
		t.Fatal(e.message)
	}
	sel(4, 9)
	e.act("unpair")
	if e.message != "Pairs removed. "+drawStatusEdited || len(e.data.Pairs) != len(g.Result.Pairs)-3 {
		t.Fatalf("%q %d", e.message, len(e.data.Pairs))
	}
	// Undo twice is the prediction again; redo
	e.act("undo")
	e.act("undo")
	if e.message != "Undone." || e.data.TopologyEdited || len(e.data.Pairs) != len(g.Result.Pairs) || len(e.redo) != 2 {
		t.Fatalf("%q %d", e.message, len(e.data.Pairs))
	}
	e.act("redo")
	if e.message != "Redone." || len(e.data.Pairs) != len(g.Result.Pairs)+1 {
		t.Fatal(e.message)
	}
	// a new edit drops the redo; the history keeps 50 steps
	before := e.data.Points[0]
	sel(1, 9)
	for range 72 {
		e.act("rotR")
	}
	if len(e.undo) != 50 || len(e.redo) != 0 {
		t.Fatalf("undo %d redo %d", len(e.undo), len(e.redo))
	}
	if p := e.data.Points[0]; math.Abs(p.X-before.X) > 1e-6 || math.Abs(p.Y-before.Y) > 1e-6 {
		t.Fatalf("72 × 15° turns back: %v vs %v", p, before)
	}
	// rotate about the closing pair's middle; mirror twice is the same
	pivot := drawPoint{(e.data.Points[0].X + e.data.Points[8].X) / 2, (e.data.Points[0].Y + e.data.Points[8].Y) / 2}
	e.act("rot90")
	if q := (drawPoint{(e.data.Points[0].X + e.data.Points[8].X) / 2, (e.data.Points[0].Y + e.data.Points[8].Y) / 2}); math.Hypot(q.X-pivot.X, q.Y-pivot.Y) > 1e-9 {
		t.Fatal("pivot moved")
	}
	points := append([]drawPoint{}, e.data.Points...)
	e.act("mirror")
	e.act("mirror")
	if !reflect.DeepEqual(points, e.data.Points) && math.Abs(points[4].X-e.data.Points[4].X) > 1e-9 {
		t.Fatal("mirror twice")
	}
	// selection: an invalid interval, the domain and its parent, all
	sel(9, 3)
	e.act("domain")
	if e.message != "Select a valid nucleotide interval." {
		t.Fatal(e.message)
	}
	sel(14, 14)
	e.act("domain")
	if e.selection != [2]int{13, 18} {
		t.Fatal(e.selection)
	}
	e.act("parent")
	if e.selection != [2]int{12, 19} {
		t.Fatal(e.selection)
	}
	sel(80, 80)
	e.act("domain")
	if e.message != "Base 80 lies in the exterior loop, outside any domain." {
		t.Fatal(e.message)
	}
	sel(1, 9)
	e.act("parent")
	if e.message != "No pair encloses the selection." {
		t.Fatal(e.message)
	}
	e.act("all")
	if e.selection != [2]int{1, 81} || e.from != 1 || e.to != 81 {
		t.Fatal(e.selection)
	}
	// selection colours; a colour drag is one undo step
	sel(3, 5)
	e.undo = e.undo[:10] // below the cap of 50, to count the steps
	steps := len(e.undo)
	for _, c := range []string{"#ff0000", "#ee0000", "#dd0000"} {
		e.fill = ui.Hex(c)
		e.styleField("fill")
	}
	if e.message != "Applied to 3–5. "+drawStatusEdited || len(e.undo) != steps+1 || e.data.Styles[4].Fill != "#dd0000" || e.data.Styles[6].Fill != "" {
		t.Fatalf("%q %d %+v", e.message, len(e.undo)-steps, e.data.Styles[4])
	}
	sel(75, 77)
	e.pairWidth = 3.5
	e.styleField("pairWidth")
	if e.message != "No pair touches the selection." {
		t.Fatal(e.message)
	}
	sel(1, 2)
	e.styleField("pairWidth")
	if e.data.Pairs[0].Width != 3.5 || e.data.Pairs[1].Width != 3.5 || e.data.Pairs[2].Width != 2 {
		t.Fatalf("%+v", e.data.Pairs[:3])
	}
	sel(3, 5)
	e.act("resetStyle")
	if _, ok := e.data.Styles[4]; ok {
		t.Fatal("reset")
	}
	// drawing style
	e.title, e.pairStyle, e.numbering = "Edited", "lw", "false"
	e.globalStyle("title")
	if e.message != "Style updated. "+drawStatusEdited || e.data.Title != "Edited" || e.data.PairStyle != "lw" || *e.data.Numbering {
		t.Fatal(e.message)
	}
	// annotations
	sel(10, 12)
	e.noteText = " "
	e.act("annotation")
	if e.message != "Enter annotation text first." {
		t.Fatal(e.message)
	}
	e.noteKind, e.anchorType = "region", "free"
	e.act("annotation")
	if e.message != "Only text annotations can take a free position." {
		t.Fatal(e.message)
	}
	e.anchorType = "base"
	e.act("annotation")
	if a := e.data.Annotations; e.message != "Annotation added. "+drawStatusEdited || len(a) != 1 || a[0].End != 12 || *a[0].Opacity != .25 || *a[0].Width != 32 {
		t.Fatalf("%q %+v", e.message, a)
	}
	e.noteKind, e.anchorType, e.noteText = "text", "helix", "stem"
	sel(4, 4)
	e.act("annotation")
	if e.message != "Base 4 is unpaired; choose a paired base to anchor a helix annotation." {
		t.Fatal(e.message)
	}
	sel(2, 2)
	e.act("annotation")
	if a := e.data.Annotations[1]; a.Base != 1 || a.AnchorType != "helix" || len(a.Bases) != 6 {
		t.Fatalf("%+v", a)
	}
	e.act("updateAnnotation")
	if e.message != "Select a saved annotation first." {
		t.Fatal(e.message)
	}
	e.selectAnnotation(0)
	if e.noteKind != "region" || e.selection != [2]int{10, 12} {
		t.Fatal(e.noteKind, e.selection)
	}
	e.noteKind, e.noteText = "probe", "1M7"
	e.act("updateAnnotation")
	if a := e.data.Annotations[0]; e.message != "Annotation updated. "+drawStatusEdited || a.Kind != "probe" || a.Marker != "arrow" || *a.Intensity != 1 {
		t.Fatalf("%q %+v", e.message, a)
	}
	e.act("ends")
	e.act("ends")
	if len(e.data.Annotations) != 4 || e.data.Annotations[3].Text != "3′" {
		t.Fatalf("%+v", e.data.Annotations)
	}
	e.act("removeAnnotation")
	if len(e.data.Annotations) != 3 || e.message != "Annotation removed. "+drawStatusEdited {
		t.Fatal(e.message)
	}
	e.act("clearAnnotations")
	if len(e.data.Annotations) != 0 {
		t.Fatal("clear")
	}
	// colour maps
	e.act("evidence")
	if e.message != "This prediction has no experimental support scores." {
		t.Fatal(e.message)
	}
	e.mapValues, e.palette, e.mapCaption = "1 0\n81 10", "grey", "SHAPE"
	e.act("colormap")
	if ev := e.data.Evidence; e.message != "Colour map applied. "+drawStatusEdited || ev == nil || *ev.Minimum != 0 || ev.Maximum != 10 || e.data.Styles[81].Fill != "#252525" || e.data.Styles[81].Text != "#ffffff" {
		t.Fatalf("%q %+v", e.message, e.data.Styles[81])
	}
	if !strings.Contains(drawingSVG(e.data, nil, nil), ">SHAPE · user values · 0–10</text>") {
		t.Fatal("caption")
	}
	e.act("clearColors")
	if e.data.Evidence != nil || e.data.Styles[81].Fill != "#91D1C2" {
		t.Fatal("clear colours")
	}
	// layouts
	for _, kind := range []string{"simple", "circular", "linear", "radiate"} {
		e.layoutKind = kind
		e.act("layout")
		if e.message != "Positions recomputed. "+drawStatusEdited || e.data.Layout.Kind != kind || (e.data.PairCurve == "arc") != (kind == "linear") {
			t.Fatal(kind, e.message)
		}
	}
	e.spacing = 2
	e.relayout("Layout applied.")
	if e.data.Layout.Spacing != 2 {
		t.Fatal(e.data.Layout)
	}
	// a saved drawing loads back; another prediction's is refused
	raw, _ := drawingJSON(e.data)
	e.data.Title = "changed"
	e.loadBytes(raw, nil)
	if e.message != "Drawing loaded." || e.data.Title != "Edited" || !e.data.TopologyEdited {
		t.Fatal(e.message)
	}
	other := initialDrawing(&drawingSource{Sequence: "GGGAAACCC", DBN: "(((...)))", Pairs: drawPairsFrom("(((...)))")})
	raw, _ = drawingJSON(other)
	e.loadBytes(raw, nil)
	if e.message != "This drawing belongs to a different prediction." {
		t.Fatal(e.message)
	}
}

// COMRADES support colours a new drawing; with no matching pair the
// editor says why it stays uncoloured.
func TestDrawingEvidenceMessage(t *testing.T) {
	g := drawGoldens(t)[3]
	if g.Name != "evidence" {
		t.Fatal(g.Name)
	}
	src := g.source()
	e := newDrawingEditor(nil, src)
	if e.data.Evidence == nil || e.data.Styles[12].Fill != "#b64747" || e.message != drawStatusUnchanged {
		t.Fatalf("%q %+v", e.message, e.data.Styles[12])
	}
	src.Support = make([]float64, len(src.Sequence))
	e = newDrawingEditor(nil, src)
	if e.data.Evidence != nil || e.message != "COMRADES support: none of the 27 pairs in this structure matches any of the 4 RNAcofold evidence pairs, so every base scores 0 and keeps the default colour." {
		t.Fatalf("%q", e.message)
	}
}

// The editor on the Structure page: "Edit drawing" opens it on the window
// shown, the canvas selects and moves bases, the exports save the web's
// files, a saved drawing loads, and "Close editor" returns to the view.
func TestStructureDrawingEditor(t *testing.T) {
	a, f := openCS(t)
	f.ops["read_structure"] = fixture(t, "structure_read_cofold.json")
	tt := ui.NewTester(a.View, 1300, 1500)
	a.Navigate("structure", map[string]any{"structureId": "structure-cofold1"})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	// a two-strand result is not edited, as on the web
	_ = tt.Click("Edit drawing")
	tt.Frame()
	if p.editing || p.drawing != nil || tt.HasText(drawCopy) {
		t.Fatal("a two-strand result opened the editor")
	}
	// the same 201 nt as one strand (one window)
	one := fixture(t, "structure_read_cofold.json")
	rec := one["records"].([]any)[0].(map[string]any)
	for _, key := range []string{"positions", "cutpoint", "segments"} {
		delete(rec, key)
	}
	rec["window_start"], rec["window_end"] = 1, len(rec["sequence"].(string))
	f.ops["read_structure"] = one
	delete(p.cache, "structure-cofold1")
	p.arcsKey = ""
	a.Navigate("structure", map[string]any{"structureId": "structure-cofold1"})
	csSettle(a, tt)
	if err := tt.Click("Edit drawing"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	e := p.drawing
	r := p.cache["structure-cofold1"].Records[0]
	if e == nil || e.data.Sequence != r.Sequence || e.data.Cutpoint != 0 || !tt.HasText("Close editor") || !tt.HasText(drawCopy) ||
		!tt.HasText("Selected: base 1 ("+r.Sequence[:1]+")") || !tt.HasText(drawStatusUnchanged) {
		t.Fatalf("editor %q", tt.Texts())
	}
	snapshot(t, tt, "structure-drawing-editor")
	if svg := drawingSVG(e.data, nil, nil); strings.Count(svg, `stroke="#8491B4"`) != len(r.Sequence)-1 {
		t.Fatalf("backbone lines %d", strings.Count(svg, `stroke="#8491B4"`))
	}
	canvas, ok := tt.Find("Editable RNA drawing")
	if !ok || e.canvasW == 0 {
		t.Fatalf("no canvas %v", canvas)
	}
	at := func(i int) (float32, float32) {
		k, ox, oy := fitBox(e.bounds(), e.canvasW, e.canvasH)
		q := e.data.Points[i-1]
		return canvas.X + float32(ox+q.X*k), canvas.Y + float32(oy+q.Y*k)
	}
	// click base 40, Shift-click 45: 40-45
	x, y := at(40)
	tt.ClickAt(x, y)
	tt.Frame()
	if e.selection != [2]int{40, 40} {
		t.Fatalf("click %v", e.selection)
	}
	x, y = at(45)
	tt.ClickAtWith(ui.Shift, x, y)
	tt.Frame()
	if e.selection != [2]int{40, 45} || !tt.HasText("Selected: 40–45 (6 nt)") {
		t.Fatalf("shift click %v", e.selection)
	}
	// drag a selected base: the selection moves, one undo step
	before := e.data.Points[41]
	x, y = at(42)
	tt.Press(x, y)
	tt.Move(x+20, y+10)
	tt.Move(x+40, y+20)
	tt.Release(x+40, y+20)
	tt.Frame()
	k, _, _ := fitBox(e.bounds(), e.canvasW, e.canvasH)
	moved := e.data.Points[41]
	if math.Abs(moved.X-before.X-40/k) > 0.5 || math.Abs(moved.Y-before.Y-20/k) > 0.5 || len(e.undo) != 1 || e.message != "Moved. "+drawStatusUnchanged {
		t.Fatalf("moved %v -> %v (k %v) undo %d %q", before, moved, k, len(e.undo), e.message)
	}
	// arrow keys nudge the selection (Shift: 10)
	tt.Key(ui.Shift, ui.KeyRight)
	tt.Frame()
	if d := e.data.Points[41].X - moved.X; math.Abs(d-10) > 1e-9 {
		t.Fatalf("nudge %v", d)
	}
	// ⌘Z undoes the nudge and the drag
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if e.data.Points[41] != before {
		t.Fatalf("undo %v", e.data.Points[41])
	}
	// Ctrl/⌘ + wheel zooms; double click selects the domain
	view := e.bounds()
	// the platform sends the modifiers with the wheel; the tester cannot
	e.input(ui.InputEvent{Kind: ui.InputScroll, X: e.canvasW / 2, Y: e.canvasH / 2, DY: -10, Mods: ui.Cmd})
	if e.input(ui.InputEvent{Kind: ui.InputScroll, DY: 10}) {
		t.Fatal("a plain wheel zooms")
	}
	tt.Frame()
	if got := e.bounds(); got[2] >= view[2] {
		t.Fatalf("zoom %v -> %v", view, got)
	}
	e.act("fit")
	tt.Frame()
	x, y = at(5)
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	tt.Frame()
	if want, ok := drDomainOf(e.data, 5); !ok || e.selection != want {
		t.Fatalf("double click %v want %v", e.selection, want)
	}
	// Pair the two ends with the buttons; the status follows
	e.from, e.to = 1, 2
	if err := tt.Click("Pair the two ends"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasSuffix(e.message, drawStatusEdited) && !strings.HasPrefix(e.message, "Base 1 or 2 is already paired") {
		t.Fatal(e.message)
	}
	// the selects set their values
	if err := tt.Click("Export format"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Edited CT"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if e.exportFormat != "ct" {
		t.Fatalf("export format %q", e.exportFormat)
	}
	// exports: the web's file names and headers
	for _, c := range []struct{ format, name, head string }{
		{"svg", "rna-edited-drawing.svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="`},
		{"eps", "rna-edited-drawing.eps", "%!PS-Adobe-3.0 EPSF-3.0\n"},
		{"json", "rna-drawing.json", "{\n  \"schema\": \"hyb2-drawing-1\","},
		{"ct", "rna-edited-drawing.ct", "201 HYB2 manually edited drawing; energy not recalculated\n1\t"},
		{"bpseq", "rna-edited-drawing.bpseq", "# HYB2 manually edited drawing; energy not recalculated\n1 "},
		{"dbn", "rna-edited-drawing.dbn", ">" + stRecordLabel(stGene(p.cache["structure-cofold1"]), r) + " (edited drawing; energy not recalculated)\n" + r.Sequence + "\n"},
		{"varna", "rna-drawing-varna.sh", "#!/bin/sh\n# VARNA command generated by HYB2 Web Lite."},
	} {
		e.exportFormat = c.format
		name, text := exportClick(t, a, tt, "Save…")
		if name != c.name || !strings.HasPrefix(text, c.head) {
			t.Fatalf("%s: %s %.120q", c.format, name, text)
		}
		if c.format == "varna" && (e.message != "VARNA command saved. Run it next to VARNA.jar to redraw this structure in VARNA.") {
			t.Fatal(e.message)
		}
		if c.format == "json" {
			// Load JSON reads it back
			path := filepath.Join(t.TempDir(), "saved.json")
			_ = os.WriteFile(path, []byte(text), 0o644)
			previous := PickFiles
			PickFiles = func(string, bool) []string { return []string{path} }
			e.data.Title = "changed"
			if err := tt.Click("Load JSON…"); err != nil {
				t.Fatal(err)
			}
			waitFlush(a)
			tt.Frame()
			PickFiles = previous
			if e.message != "Drawing loaded." || e.data.Title == "changed" {
				t.Fatalf("load %q", e.message)
			}
		}
	}
	// the prediction's own exports are unchanged
	if !strings.Contains(p.dbnText, r.DBN) {
		t.Fatal("prediction changed")
	}
	if err := tt.Click("Close editor"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.editing || !tt.HasText("Edit drawing") || tt.HasText(drawCopy) {
		t.Fatal("editor not closed")
	}
	// reopening keeps the session
	if err := tt.Click("Edit drawing"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.drawing != e {
		t.Fatal("session lost")
	}
}
