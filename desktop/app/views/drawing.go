package views

// The drawing editor's model, a port of the web's structure-editor.js: an
// editable copy of a prediction (positions, pairs, colours, labels) that
// stays apart from the prediction and its exports. The layouts (VARNA-like
// RADIATE, ViennaRNA's polygon, circular, linear), the validation and the
// writers (SVG, EPS, drawing JSON, CT, BPSEQ, DBN, VARNA command) follow
// the web's, so the files are the same. The editor itself is
// drawing_editor.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const drawingSchema = "hyb2-drawing-1"

// drawPalettes are the colour maps: Lau plot_VARNA stops (ModeleColorMap
// interpolates RGB channels and truncates to integers), the website
// palette and VARNA-style alternatives.
var drawPalettes = map[string][]drawStop{
	"lau":     {{0, "#FFFFFF"}, {.2, "#4747FF"}, {.55, "#1CFF47"}, {.65, "#FFFF47"}, {.9, "#FF4747"}, {1, "#B64747"}},
	"brand":   {{0, "#FFFFFF"}, {.2, "#91D1C2"}, {.55, "#4DBBD5"}, {.9, "#E64B35"}, {1, "#3C5488"}},
	"heat":    {{0, "#FFFFFF"}, {.35, "#FFE066"}, {.7, "#F08C00"}, {1, "#B00020"}},
	"bluered": {{0, "#2166AC"}, {.5, "#F7F7F7"}, {1, "#B2182B"}},
	"grey":    {{0, "#FFFFFF"}, {1, "#252525"}},
	"viridis": {{0, "#440154"}, {.25, "#3B528B"}, {.5, "#21918C"}, {.75, "#5EC962"}, {1, "#FDE725"}},
}

type drawStop struct {
	at    float64
	color string
}

var (
	drawPairStyles = []string{"simple", "lw", "rnaviz", "none"}
	drawFonts      = map[string]string{"sans-serif": "Helvetica", "serif": "Times-Roman", "monospace": "Courier"}
	drawLayouts    = []string{"radiate", "simple", "circular", "linear"}
	drawCanonical  = map[string]bool{"AU": true, "UA": true, "GC": true, "CG": true}
	drawWobble     = map[string]bool{"GU": true, "UG": true}
	drawDefault    = baseStyle{Fill: "#91D1C2", Text: "#222222", Radius: 9}
)

// ---- the drawing (its JSON is the web's "hyb2-drawing-1")

type drawPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type drawPair struct {
	Left  int     `json:"left"`
	Right int     `json:"right"`
	Color string  `json:"color"`
	Width float64 `json:"width"`
}

type drawLayout struct {
	Kind    string  `json:"kind"`
	Spacing float64 `json:"spacing"`
	Flat    bool    `json:"flat"`
}

type baseStyle struct {
	Radius  float64 `json:"radius"`
	Fill    string  `json:"fill"`
	Text    string  `json:"text"`
	Outline string  `json:"outline,omitempty"`
}

// drawEvidence colours the bases: engine support (no Minimum, 0 is white)
// or user values (Minimum set; null values keep their colour).
type drawEvidence struct {
	Values    []*float64 `json:"values"`
	Maximum   float64    `json:"maximum"`
	Minimum   *float64   `json:"minimum,omitempty"`
	Kind      string     `json:"kind"`
	Palette   string     `json:"palette"`
	Source    string     `json:"source"`
	Overrides bool       `json:"overrides"`
}

type drawAnnotation struct {
	Kind       string   `json:"kind,omitempty"`
	Base       int      `json:"base"`
	End        int      `json:"end,omitempty"`
	Text       string   `json:"text"`
	Bases      []int    `json:"bases,omitempty"`
	DX         float64  `json:"dx"`
	DY         float64  `json:"dy"`
	Color      string   `json:"color"`
	Size       *float64 `json:"size,omitempty"`
	Opacity    *float64 `json:"opacity,omitempty"`
	Width      *float64 `json:"width,omitempty"`
	Marker     string   `json:"marker,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	Intensity  *float64 `json:"intensity,omitempty"`
	AnchorType string   `json:"anchorType,omitempty"`
	Free       *bool    `json:"free,omitempty"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Auto       string   `json:"auto,omitempty"`
}

type drawing struct {
	Schema         string            `json:"schema"`
	Sequence       string            `json:"sequence"`
	DotBracket     string            `json:"dotBracket"`
	Points         []drawPoint       `json:"points"`
	Layout         *drawLayout       `json:"layout,omitempty"`
	Pairs          []drawPair        `json:"pairs"`
	Styles         map[int]baseStyle `json:"styles"`
	Annotations    []drawAnnotation  `json:"annotations"`
	Background     string            `json:"background"`
	Backbone       string            `json:"backbone"`
	NumberInterval int               `json:"numberInterval"`
	Title          string            `json:"title"`
	TopologyEdited bool              `json:"topologyEdited"`
	Cutpoint       int               `json:"cutpoint,omitempty"`
	Evidence       *drawEvidence     `json:"evidence,omitempty"`
	PairStyle      string            `json:"pairStyle,omitempty"`
	PairCurve      string            `json:"pairCurve,omitempty"`
	Font           string            `json:"font,omitempty"`
	BaseFontSize   *float64          `json:"baseFontSize,omitempty"`
	NumberColor    string            `json:"numberColor,omitempty"`
	Numbering      *bool             `json:"numbering,omitempty"`
	BackboneWidth  *float64          `json:"backboneWidth,omitempty"`
	TitleColor     string            `json:"titleColor,omitempty"`
	TitleSize      *float64          `json:"titleSize,omitempty"`
}

func drFptr(v float64) *float64 { return &v }
func drBptr(v bool) *bool       { return &v }

// clone is a deep copy.
func (d *drawing) clone() *drawing {
	n := *d
	n.Points = append([]drawPoint{}, d.Points...)
	n.Pairs = append([]drawPair{}, d.Pairs...)
	n.Styles = make(map[int]baseStyle, len(d.Styles))
	for k, v := range d.Styles {
		n.Styles[k] = v
	}
	n.Annotations = make([]drawAnnotation, len(d.Annotations))
	for i, a := range d.Annotations {
		a.Bases = append([]int(nil), a.Bases...)
		n.Annotations[i] = a
	}
	if d.Layout != nil {
		l := *d.Layout
		n.Layout = &l
	}
	if d.Evidence != nil {
		e := *d.Evidence
		e.Values = append([]*float64{}, d.Evidence.Values...)
		n.Evidence = &e
	}
	return &n
}

// ---- numbers and text as JavaScript writes them

// drJsNum is String(v) for the numbers the drawing holds.
func drJsNum(v float64) string {
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// drNum is the web's drNum: rounded to two decimals.
func drNum(v float64) string { return drJsNum(math.Floor(v*100+0.5) / 100) }

// drJsLen is a string's length in UTF-16 units, as JavaScript counts it.
func drJsLen(s string) int { return len(utf16.Encode([]rune(s))) }

// drJsNumber is JavaScript's Number(s) for a token: NaN when it is not a
// number.
func drJsNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "0x") || strings.HasPrefix(lower, "0o") || strings.HasPrefix(lower, "0b") {
		base := map[byte]int{'x': 16, 'o': 8, 'b': 2}[lower[1]]
		if v, err := strconv.ParseUint(s[2:], base, 64); err == nil {
			return float64(v)
		}
		return math.NaN()
	}
	switch strings.TrimLeft(s, "+-") {
	case "Infinity":
		if strings.HasPrefix(s, "-") {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	if strings.ContainsAny(lower, "_xnp") || strings.Contains(lower, "inf") {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

// drToFixed is JavaScript's v.toFixed(digits): the exact binary value
// rounded half up (Go's formatting rounds half to even).
func drToFixed(v float64, digits int) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := new(big.Float).SetFloat64(v).Text('f', digits+40)
	dot := strings.IndexByte(s, '.')
	kept := []byte(s[:dot] + s[dot+1:dot+1+digits])
	if s[dot+1+digits] >= '5' {
		i := len(kept) - 1
		for ; i >= 0 && kept[i] == '9'; i-- {
			kept[i] = '0'
		}
		if i < 0 {
			kept = append([]byte{'1'}, kept...)
		} else {
			kept[i]++
		}
	}
	whole := string(kept[:len(kept)-digits])
	out := whole
	if digits > 0 {
		out += "." + string(kept[len(kept)-digits:])
	}
	if neg {
		out = "-" + out
	}
	return out
}

func drFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func drXmlText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}

var drHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func drIsColor(s string) bool { return drHexColor.MatchString(s) }

func drHexRGB(c string) [3]int {
	var out [3]int
	for i := range out {
		v, _ := strconv.ParseUint(c[1+2*i:3+2*i], 16, 8)
		out[i] = int(v)
	}
	return out
}

// ---- colours

func drInterpolate(stops []drawStop, t float64) string {
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].at {
			f := (t - stops[i-1].at) / (stops[i].at - stops[i-1].at)
			a, b := drHexRGB(stops[i-1].color), drHexRGB(stops[i].color)
			s := "#"
			for k := range 3 {
				s += fmt.Sprintf("%02x", int(math.Floor(float64(a[k])*(1-f)+float64(b[k])*f)))
			}
			return s
		}
	}
	return stops[len(stops)-1].color
}

func drPaletteOr(name string) []drawStop {
	if p, ok := drawPalettes[name]; ok {
		return p
	}
	return drawPalettes["brand"]
}

// drSupportColor: scores are already computed by the engine; zero is white.
func drSupportColor(value, maximum float64, palette string) string {
	if !(value > 0) || !(maximum > 0) {
		return "#ffffff"
	}
	return drInterpolate(drPaletteOr(palette), math.Min(1, value/maximum))
}

// drMapColor: engine evidence spans 0–maximum; a user colour map carries its
// own minimum.
func drMapColor(e *drawEvidence, value float64) string {
	if e.Minimum == nil {
		return drSupportColor(value, e.Maximum, e.Palette)
	}
	return drInterpolate(drPaletteOr(e.Palette), math.Max(0, math.Min(1, (value-*e.Minimum)/(e.Maximum-*e.Minimum))))
}

func drTextOn(fill string) string {
	c := drHexRGB(fill)
	if .2126*float64(c[0])+.7152*float64(c[1])+.0722*float64(c[2]) < 125 {
		return "#ffffff"
	}
	return "#222222"
}

// drawSupport is COMRADES support per base: the RNAcofold supporting
// observations of the pair the base forms (0 when unpaired or
// unsupported); nil when the prediction carries no such evidence.
func drEvidenceFor(support []float64) *drawEvidence {
	if support == nil {
		return nil
	}
	top := 0.0
	values := make([]*float64, len(support))
	for i, v := range support {
		values[i] = drFptr(v)
		top = math.Max(top, v)
	}
	if top == 0 {
		return nil
	}
	return &drawEvidence{Values: values, Maximum: top, Kind: "RNAcofold supporting observations", Palette: "lau", Source: "COMRADES support"}
}

func drApplyEvidence(d *drawing, evidence *drawEvidence, palette string) error {
	if evidence == nil {
		return errors.New("This prediction has no experimental support scores.")
	}
	e := *evidence
	e.Values = append([]*float64{}, evidence.Values...)
	e.Palette, e.Overrides = palette, false
	d.Evidence = &e
	for i, v := range e.Values {
		if v == nil {
			continue
		}
		fill := drMapColor(&e, *v)
		s, ok := d.Styles[i+1]
		if !ok {
			s = baseStyle{Radius: 9}
		}
		s.Fill, s.Text = fill, drTextOn(fill)
		d.Styles[i+1] = s
	}
	return nil
}

var (
	drValueSplit = regexp.MustCompile(`[\s,;]+`)
	drLineSplit  = regexp.MustCompile(`\r?\n`)
)

// drParseValues reads a VARNA-style colour map: one value per base, or
// "position value" lines. Bases without a value keep their colour.
func drParseValues(text string, n int) ([]*float64, error) {
	var tokens [][]string
	for _, l := range drLineSplit.Split(text, -1) {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		var t []string
		for _, s := range drValueSplit.Split(l, -1) {
			if s != "" {
				t = append(t, s)
			}
		}
		tokens = append(tokens, t)
	}
	values := make([]*float64, n)
	pairs := len(tokens) > 0
	for _, t := range tokens {
		pairs = pairs && len(t) == 2
	}
	if pairs {
		for _, t := range tokens {
			i, x := drJsNumber(t[0]), drJsNumber(t[1])
			if !drFinite(i) || i != math.Trunc(i) || i < 1 || i > float64(n) || !drFinite(x) {
				return nil, fmt.Errorf(`Each "position value" line needs a position from 1 to %d and a number.`, n)
			}
			values[int(i)-1] = drFptr(x)
		}
	} else {
		var flat []string
		for _, t := range tokens {
			flat = append(flat, t...)
		}
		if len(flat) != n {
			return nil, fmt.Errorf(`Enter %d values (one per base) or "position value" lines; found %d.`, n, len(flat))
		}
		for i, v := range flat {
			x := drJsNumber(v)
			if !drFinite(x) {
				return nil, fmt.Errorf("Value %d is not a number.", i+1)
			}
			values[i] = drFptr(x)
		}
	}
	for _, v := range values {
		if v != nil {
			return values, nil
		}
	}
	return nil, errors.New("No colour-map values were entered.")
}

type drColorMapOptions struct {
	Minimum, Maximum, Palette, Caption string
}

func drColorMap(d *drawing, text string, o drColorMapOptions) error {
	values, err := drParseValues(text, len(d.Sequence))
	if err != nil {
		return err
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if v != nil {
			lo, hi = math.Min(lo, *v), math.Max(hi, *v)
		}
	}
	minimum, maximum := lo, hi
	if strings.TrimSpace(o.Minimum) != "" {
		minimum = drJsNumber(o.Minimum)
	}
	if strings.TrimSpace(o.Maximum) != "" {
		maximum = drJsNumber(o.Maximum)
	}
	if !drFinite(minimum) || !drFinite(maximum) || !(maximum > minimum) {
		return errors.New("The colour map needs a maximum above its minimum.")
	}
	if _, ok := drawPalettes[o.Palette]; !ok {
		return errors.New("Unknown colour-map palette.")
	}
	caption := o.Caption
	if caption == "" {
		caption = "User values"
	}
	if u := utf16.Encode([]rune(caption)); len(u) > 100 {
		caption = string(utf16.Decode(u[:100]))
	}
	return drApplyEvidence(d, &drawEvidence{Values: values, Minimum: drFptr(minimum), Maximum: maximum, Kind: caption, Source: "user values"}, o.Palette)
}

func drClearColors(d *drawing) {
	d.Evidence = nil
	for k, s := range d.Styles {
		s.Fill, s.Text = drawDefault.Fill, drawDefault.Text
		d.Styles[k] = s
	}
}

// ---- pairs

func drPairsCross(a, b drawPair) bool {
	return (a.Left < b.Left && b.Left < a.Right && a.Right < b.Right) || (b.Left < a.Left && a.Left < b.Right && b.Right < a.Right)
}

func drSortedPairs(pairs []drawPair) []drawPair {
	out := append([]drawPair{}, pairs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Left < out[j].Left })
	return out
}

// drPairTable is the nested subset of the pairs; crossing pairs are still
// drawn but do not shape a layout.
func drPairTable(n int, pairs []drawPair) []int {
	var kept []drawPair
	for _, p := range drSortedPairs(pairs) {
		ok := true
		for _, q := range kept {
			if drPairsCross(p, q) {
				ok = false
				break
			}
		}
		if ok {
			kept = append(kept, p)
		}
	}
	pt := make([]int, n+2)
	for _, p := range kept {
		if p.Left >= 1 && p.Right <= n {
			pt[p.Left], pt[p.Right] = p.Right, p.Left
		}
	}
	return pt
}

func drawPairsFrom(dbn string) []drawPair {
	const open, close = "([{<", ")]}>"
	stacks := [4][]int{}
	var pairs []drawPair
	for i, c := range []byte(dbn) {
		if o := strings.IndexByte(open, c); o >= 0 {
			stacks[o] = append(stacks[o], i+1)
		} else if k := strings.IndexByte(close, c); k >= 0 && len(stacks[k]) > 0 {
			l := stacks[k][len(stacks[k])-1]
			stacks[k] = stacks[k][:len(stacks[k])-1]
			pairs = append(pairs, drawPair{Left: l, Right: i + 1})
		}
	}
	return drSortedPairs(pairs)
}

// drawDotBracket writes the pairs with one bracket kind per crossing level.
func drawDotBracket(n int, pairs []drawPair) (string, error) {
	const open, close = "([{<", ")]}>"
	var levels [][]drawPair
	chars := []byte(strings.Repeat(".", n))
	for _, p := range drSortedPairs(pairs) {
		k := -1
		for li, level := range levels {
			ok := true
			for _, q := range level {
				if drPairsCross(p, q) {
					ok = false
					break
				}
			}
			if ok {
				k = li
				break
			}
		}
		if k < 0 {
			if len(levels) == len(open) {
				return "", errors.New("These pairs cross too often for bracket notation.")
			}
			levels = append(levels, nil)
			k = len(levels) - 1
		}
		levels[k] = append(levels[k], p)
		chars[p.Left-1], chars[p.Right-1] = open[k], close[k]
	}
	return string(chars), nil
}

func drPairSignature(pairs []drawPair) string {
	keys := make([][2]int, len(pairs))
	for i, p := range pairs {
		keys[i] = [2]int{p.Left, p.Right}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%d-%d", k[0], k[1])
	}
	return strings.Join(parts, ",")
}

func drawMates(pairs []drawPair) map[int]int {
	m := make(map[int]int, 2*len(pairs))
	for _, p := range pairs {
		m[p.Left], m[p.Right] = p.Right, p.Left
	}
	return m
}

// ---- layouts

func drLoopVertices(pt []int, i, j int) []int {
	v := []int{i}
	for k := i + 1; k < j; {
		v = append(v, k)
		if pt[k] > k {
			v = append(v, pt[k])
			k = pt[k] + 1
		} else {
			k++
		}
	}
	return append(v, j)
}

// drRadiate is VARNA's RADIATE idea: each loop is a circle whose chords are
// the backbone steps and closing pairs, and each helix grows straight out
// of its loop.
func drRadiate(n int, pt []int, s float64, flat bool) []drawPoint {
	w := 1.5 * s
	P := make([]*drawPoint, n+1)
	theta := func(e, r float64) float64 { return 2 * math.Asin(math.Min(1, e/(2*r))) }
	add := func(p drawPoint, u drawPoint, k float64) drawPoint { return drawPoint{p.X + u.X*k, p.Y + u.Y*k} }
	unit := func(x, y float64) drawPoint {
		l := math.Hypot(x, y)
		if l == 0 {
			l = 1
		}
		return drawPoint{x / l, y / l}
	}
	chords := func(v []int) []float64 {
		out := make([]float64, len(v)-1)
		for t := range out {
			if pt[v[t]] == v[t+1] && v[t] < v[t+1] {
				out[t] = w
			} else {
				out[t] = s
			}
		}
		return out
	}
	type circ struct {
		r       float64
		outside bool
		longest int
	}
	circle := func(edges []float64) circ {
		longest := 0
		for i, e := range edges {
			if e > edges[longest] {
				longest = i
			}
		}
		half := edges[longest] / 2
		total := func(r float64) float64 {
			a := 0.0
			for _, e := range edges {
				a += theta(e, r)
			}
			return a
		}
		// A loop too small to surround its centre puts the centre beyond
		// the longest chord.
		outside := total(half) < 2*math.Pi
		f := func(r float64) float64 { return total(r) - 2*math.Pi }
		if outside {
			f = func(r float64) float64 {
				a := 0.0
				for i, e := range edges {
					if i == longest {
						a -= theta(e, r)
					} else {
						a += theta(e, r)
					}
				}
				return a
			}
		}
		lo, hi := half, 0.0
		for _, e := range edges {
			hi += e
		}
		for range 100 {
			mid := (lo + hi) / 2
			if (outside && f(mid) < 0) || (!outside && f(mid) > 0) {
				lo = mid
			} else {
				hi = mid
			}
		}
		return circ{(lo + hi) / 2, outside, longest}
	}
	place := func(v []int, edges []float64, C drawPoint, r, start, sign float64) {
		a := start
		for t := 1; t < len(v); t++ {
			a += sign * theta(edges[t-1], r)
			if P[v[t]] == nil {
				P[v[t]] = &drawPoint{C.X + r*math.Cos(a), C.Y + r*math.Sin(a)}
			}
		}
	}
	var helix func(k, l int, u drawPoint)
	branches := func(v []int, C *drawPoint) {
		for t := 0; t+1 < len(v); t++ {
			k, l := v[t], v[t+1]
			if pt[k] == l && k < l {
				u := drawPoint{0, -1}
				if C != nil {
					u = unit((P[k].X+P[l].X)/2-C.X, (P[k].Y+P[l].Y)/2-C.Y)
				}
				helix(k, l, u)
			}
		}
	}
	loop := func(i, j int, u drawPoint) {
		if j-i < 2 {
			return
		}
		v := drLoopVertices(pt, i, j)
		edges := chords(v)
		all := append(append([]float64{}, edges...), w)
		c := circle(all)
		h := math.Sqrt(math.Max(0, c.r*c.r-w*w/4))
		if c.outside && c.longest == len(all)-1 {
			h = -h
		}
		C := add(drawPoint{(P[i].X + P[j].X) / 2, (P[i].Y + P[j].Y) / 2}, u, h)
		a0 := math.Atan2(P[i].Y-C.Y, P[i].X-C.X)
		aj := math.Atan2(P[j].Y-C.Y, P[j].X-C.X)
		walk := 0.0
		for _, e := range edges {
			walk += theta(e, c.r)
		}
		miss := func(sign float64) float64 {
			d := a0 + sign*walk - aj
			return math.Abs(math.Atan2(math.Sin(d), math.Cos(d)))
		}
		sign := -1.0
		if miss(1) <= miss(-1) {
			sign = 1
		}
		place(v, edges, C, c.r, a0, sign)
		branches(v, &C)
	}
	helix = func(k, l int, u drawPoint) {
		for pt[k+1] == l-1 && k+1 < l-1 {
			np, nq := add(*P[k], u, s), add(*P[l], u, s)
			P[k+1], P[l-1] = &np, &nq
			k++
			l--
		}
		loop(k, l, u)
	}
	var v []int
	for k := 1; k <= n; {
		v = append(v, k)
		if pt[k] > k {
			v = append(v, pt[k])
			k = pt[k] + 1
		} else {
			k++
		}
	}
	edges := chords(v)
	round := !flat && len(v) >= 3
	if round {
		c := circle(append(append([]float64{}, edges...), 2*w))
		if c.outside {
			round = false
		} else {
			C := drawPoint{}
			a0 := math.Pi/2 + math.Asin(math.Min(1, w/c.r))
			P[v[0]] = &drawPoint{c.r * math.Cos(a0), c.r * math.Sin(a0)}
			place(v, edges, C, c.r, a0, 1)
			branches(v, &C)
		}
	}
	// Flat exterior loop: each top-level domain moves right until it clears
	// the widest part of the domains before it.
	if !round {
		x, right := 0.0, math.Inf(-1)
		for t, k := range v {
			if t > 0 && pt[v[t-1]] == k && v[t-1] < k {
				continue
			}
			if !(pt[k] > k) {
				if t > 0 {
					x += s
				} else {
					x = 0
				}
				P[k] = &drawPoint{x, 0}
				right = math.Max(right, x)
				continue
			}
			l := pt[k]
			P[k], P[l] = &drawPoint{0, 0}, &drawPoint{w, 0}
			helix(k, l, drawPoint{0, -1})
			lo, hi := math.Inf(1), math.Inf(-1)
			for i := k; i <= l; i++ {
				if P[i] == nil {
					P[i] = &drawPoint{}
				}
				lo, hi = math.Min(lo, P[i].X), math.Max(hi, P[i].X)
			}
			dx := right + s - lo
			if t > 0 {
				dx = math.Max(x+s, dx)
			} else {
				dx = math.Max(0, dx)
			}
			for i := k; i <= l; i++ {
				P[i].X += dx
			}
			x = P[l].X
			right = math.Max(right, hi+dx)
		}
	}
	out := make([]drawPoint, n)
	for i := range out {
		if P[i+1] != nil {
			out[i] = *P[i+1]
		}
	}
	return out
}

// drSimpleLayout is ViennaRNA's "simple" (polygon) coordinates, plot type 0,
// as the web's Hyb2RegionPanels.simpleLayout.
func drSimpleLayout(dotBracket string) ([]float64, []float64) {
	n := len(dotBracket)
	pt := make([]int, n+3)
	var stack []int
	for k := 0; k < n; k++ {
		switch dotBracket[k] {
		case '(':
			stack = append(stack, k+1)
		case ')':
			if len(stack) > 0 {
				i := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				pt[i], pt[k+1] = k+1, i
			}
		}
	}
	pt[0] = n
	angle := make([]float64, n+6)
	var loop func(i, j int)
	loop = func(i, j int) {
		count := 2
		remember := []int{0}
		iOld := i - 1
		j++
		for i != j {
			partner := pt[i]
			if partner == 0 || i == 0 {
				i++
				count++
				continue
			}
			count += 2
			k, l := i, partner
			remember = append(remember, k, l)
			i = partner + 1
			sk, sl := k, l
			ladder := 0
			for {
				k++
				l--
				ladder++
				if !(pt[k] == l && pt[k] > k) {
					break
				}
			}
			fill := ladder - 2
			if ladder >= 2 {
				angle[sk+1+fill] += math.Pi / 2
				angle[sl-1-fill] += math.Pi / 2
				angle[sk] += math.Pi / 2
				angle[sl] += math.Pi / 2
				if ladder > 2 {
					for ; fill >= 1; fill-- {
						angle[sk+fill] = math.Pi
						angle[sl-fill] = math.Pi
					}
				}
			}
			if k <= l {
				loop(k, l)
			}
		}
		polygon := math.Pi * float64(count-2) / float64(count)
		remember = append(remember, j)
		begin := max(iOld, 0)
		for v := 1; v < len(remember); v++ {
			diff := remember[v] - begin
			for f := 0; f <= diff; f++ {
				angle[begin+f] += polygon
			}
			v++
			if v >= len(remember) {
				break
			}
			begin = remember[v]
		}
	}
	loop(0, n)
	x, y := make([]float64, n), make([]float64, n)
	alpha := 0.0
	for i := 1; i < n; i++ {
		x[i] = x[i-1] + math.Cos(alpha)
		y[i] = y[i-1] + math.Sin(alpha)
		alpha += math.Pi - angle[i+1]
	}
	return x, y
}

func drawLayoutPoints(n int, pairs []drawPair, kind string, spacing float64, flat bool) []drawPoint {
	pt := drPairTable(n, pairs)
	s := 30 * spacing
	points := make([]drawPoint, n)
	switch kind {
	case "simple":
		nested := make([]byte, n)
		for i := range nested {
			switch {
			case pt[i+1] > i+1:
				nested[i] = '('
			case pt[i+1] != 0:
				nested[i] = ')'
			default:
				nested[i] = '.'
			}
		}
		xs, ys := drSimpleLayout(string(nested))
		for i := range points {
			points[i] = drawPoint{xs[i] * s, ys[i] * s}
		}
	case "radiate":
		return drRadiate(n, pt, s, flat)
	case "linear":
		for i := range points {
			points[i] = drawPoint{float64(i) * s, 0}
		}
	default:
		r := math.Max(80, float64(n)*s/(2*math.Pi))
		for i := range points {
			a := float64(i)*2*math.Pi/float64(n) - math.Pi/2
			points[i] = drawPoint{math.Cos(a) * r, math.Sin(a) * r}
		}
	}
	return points
}

func drCentroid(points []drawPoint) drawPoint {
	var c drawPoint
	for _, p := range points {
		c.X += p.X
		c.Y += p.Y
	}
	n := float64(max(len(points), 1))
	return drawPoint{c.X / n, c.Y / n}
}

func drTurnPoints(points []drawPoint, a, b int, degrees float64, pivot drawPoint) {
	r := degrees * math.Pi / 180
	cos, sin := math.Cos(r), math.Sin(r)
	for i := a - 1; i < b; i++ {
		x, y := points[i].X-pivot.X, points[i].Y-pivot.Y
		points[i] = drawPoint{pivot.X + x*cos - y*sin, pivot.Y + x*sin + y*cos}
	}
}

func drMirrorPoints(points []drawPoint, a, b int, pivot drawPoint) {
	for i := a - 1; i < b; i++ {
		points[i].X = 2*pivot.X - points[i].X
	}
}

// ---- domains, helices and loops

// drDomainOf is the closing pair of the domain that holds base i: its own
// pair, else the innermost pair around it (false in the exterior loop).
func drDomainOf(d *drawing, i int) ([2]int, bool) {
	pt := drPairTable(len(d.Sequence), d.Pairs)
	if pt[i] != 0 {
		return [2]int{min(i, pt[i]), max(i, pt[i])}, true
	}
	for l := i - 1; l >= 1; l-- {
		if pt[l] > i {
			return [2]int{l, pt[l]}, true
		}
	}
	return [2]int{}, false
}

func drParentOf(d *drawing, a, b int) ([2]int, bool) {
	pt := drPairTable(len(d.Sequence), d.Pairs)
	for l := a - 1; l >= 1; l-- {
		if pt[l] > b {
			return [2]int{l, pt[l]}, true
		}
	}
	return [2]int{}, false
}

func drHelixOf(d *drawing, i int) ([]int, error) {
	pt := drPairTable(len(d.Sequence), d.Pairs)
	if pt[i] == 0 {
		return nil, fmt.Errorf("Base %d is unpaired; choose a paired base to anchor a helix annotation.", i)
	}
	l := min(i, pt[i])
	r := pt[l]
	for l > 1 && pt[l-1] == r+1 {
		l--
		r++
	}
	var bases []int
	for pt[l] == r && l < r {
		bases = append(bases, l, r)
		l++
		r--
	}
	return bases, nil
}

func drLoopOf(d *drawing, i int) ([]int, error) {
	pt := drPairTable(len(d.Sequence), d.Pairs)
	var l, r int
	if pt[i] != 0 {
		l = min(i, pt[i])
		r = pt[l]
	} else {
		c, ok := drDomainOf(d, i)
		if !ok {
			return nil, fmt.Errorf("Base %d lies in the exterior loop; choose a base inside a loop.", i)
		}
		l, r = c[0], c[1]
	}
	return drLoopVertices(pt, l, r), nil
}

func (d *drawing) anchor(a drawAnnotation) drawPoint {
	if a.Free != nil && *a.Free && a.X != nil && a.Y != nil {
		return drawPoint{*a.X, *a.Y}
	}
	bases := a.Bases
	if len(bases) == 0 {
		bases = []int{a.Base}
	}
	var p drawPoint
	for _, i := range bases {
		p.X += d.Points[i-1].X / float64(len(bases))
		p.Y += d.Points[i-1].Y / float64(len(bases))
	}
	return p
}

func drAutoAnnotations(d *drawing, kind string) ([]drawAnnotation, error) {
	note := func(base int, text string, bases []int) drawAnnotation {
		return drawAnnotation{Kind: "text", Base: base, Text: text, Bases: bases, DX: 25, DY: -35, Color: "#3C5488", Auto: kind}
	}
	if kind == "ends" {
		n, c := len(d.Sequence), d.Cutpoint
		// A two-strand drawing labels both ends of each strand.
		if c > 0 {
			return []drawAnnotation{note(1, "A 5′", []int{1}), note(c, "A 3′", []int{c}), note(c+1, "B 5′", []int{c + 1}), note(n, "B 3′", []int{n})}, nil
		}
		return []drawAnnotation{note(1, "5′", []int{1}), note(n, "3′", []int{n})}, nil
	}
	pairs := drSortedPairs(d.Pairs)
	keys := map[[2]int]bool{}
	for _, p := range pairs {
		keys[[2]int{p.Left, p.Right}] = true
	}
	out := []drawAnnotation{}
	if kind == "helices" {
		rank := 0
		for _, p := range pairs {
			if keys[[2]int{p.Left - 1, p.Right + 1}] {
				continue
			}
			var bases []int
			for l, r := p.Left, p.Right; keys[[2]int{l, r}]; l, r = l+1, r-1 {
				bases = append(bases, l, r)
			}
			rank++
			out = append(out, note(p.Left, fmt.Sprintf("H%d", rank), bases))
		}
		return out, nil
	}
	// Hairpins require a nested structure. Do not mislabel pseudoknot loops.
	for i, p := range pairs {
		for _, q := range pairs[i+1:] {
			if p.Left < q.Left && q.Left < p.Right && p.Right < q.Right {
				return nil, errors.New("Automatic hairpin labels require non-crossing pairs.")
			}
		}
	}
	paired := drawMates(pairs)
	rank := 0
	for _, p := range pairs {
		if p.Right <= p.Left+1 {
			continue
		}
		inner := false
		var bases []int
		for i := p.Left + 1; i < p.Right; i++ {
			if _, ok := paired[i]; ok {
				inner = true
				break
			}
			bases = append(bases, i)
		}
		if inner {
			continue
		}
		rank++
		out = append(out, note(p.Left+1, fmt.Sprintf("Hairpin %d", rank), bases))
	}
	return out, nil
}

// ---- the first drawing of a prediction

// drawingSource is the prediction a drawing starts from.
type drawingSource struct {
	Sequence, DBN string
	Pairs         []drawPair // left < right, sorted
	Cutpoint      int
	Label         string
	// Support is COMRADES support per base (nil: no RNAcofold evidence);
	// EvidencePairs how many RNAcofold evidence pairs it was scored from.
	Support       []float64
	EvidencePairs int
}

func initialDrawing(src *drawingSource) *drawing {
	n := len(src.Sequence)
	d := &drawing{Schema: drawingSchema, Sequence: src.Sequence, DotBracket: src.DBN,
		Points: drawLayoutPoints(n, src.Pairs, "radiate", 1, true), Layout: &drawLayout{Kind: "radiate", Spacing: 1, Flat: true},
		Styles: map[int]baseStyle{}, Annotations: []drawAnnotation{}, Background: "#ffffff", Backbone: "#8491B4",
		NumberInterval: 10, Title: nameOr(src.Label, "RNA structure")}
	d.Pairs = make([]drawPair, len(src.Pairs))
	for i, p := range src.Pairs {
		d.Pairs[i] = drawPair{Left: p.Left, Right: p.Right, Color: "#3C5488", Width: 2}
	}
	// CPLcofold: strand A is 1..cutpoint, strand B the rest; no backbone
	// joins them.
	if src.Cutpoint > 0 && src.Cutpoint < n {
		d.Cutpoint = src.Cutpoint
	}
	if e := drEvidenceFor(src.Support); e != nil {
		_ = drApplyEvidence(d, e, "lau")
	}
	return d
}

// ---- validation (of every edit and of a loaded drawing)

func validateDrawing(v *drawing, sequence, dbn string) error {
	n := len(sequence)
	pos := func(i int) bool { return i >= 1 && i <= n }
	fin := func(x float64) bool { return drFinite(x) && math.Abs(x) < 1e7 }
	within := func(x *float64, lo, hi float64) bool { return x == nil || (drFinite(*x) && *x >= lo && *x <= hi) }
	if v == nil || v.Schema != drawingSchema || v.Sequence != sequence || v.DotBracket != dbn {
		return errors.New("This drawing belongs to a different prediction.")
	}
	if len(v.Points) != n {
		return errors.New("Invalid nucleotide coordinates.")
	}
	for _, p := range v.Points {
		if !fin(p.X) || !fin(p.Y) {
			return errors.New("Invalid nucleotide coordinates.")
		}
	}
	if !drIsColor(v.Background) || !drIsColor(v.Backbone) || v.NumberInterval < 1 || v.NumberInterval > 1000 {
		return errors.New("Invalid drawing style.")
	}
	inList := func(s string, list []string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	_, fontOK := drawFonts[v.Font]
	if (v.PairStyle != "" && !inList(v.PairStyle, drawPairStyles)) || (v.PairCurve != "" && v.PairCurve != "line" && v.PairCurve != "arc") ||
		(v.Font != "" && !fontOK) || !within(v.BaseFontSize, 6, 30) || !within(v.TitleSize, 8, 48) || !within(v.BackboneWidth, 0, 10) ||
		(v.NumberColor != "" && !drIsColor(v.NumberColor)) || (v.TitleColor != "" && !drIsColor(v.TitleColor)) {
		return errors.New("Invalid drawing style.")
	}
	if v.Cutpoint != 0 && (v.Cutpoint < 1 || v.Cutpoint >= n) {
		return errors.New("Invalid strand break.")
	}
	if l := v.Layout; l != nil && (!inList(l.Kind, drawLayouts) || !drFinite(l.Spacing) || l.Spacing < .5 || l.Spacing > 3) {
		return errors.New("Invalid layout settings.")
	}
	if drJsLen(v.Title) > 500 {
		return errors.New("Invalid title.")
	}
	if float64(len(v.Pairs)) > float64(n)/2 {
		return errors.New("Invalid pair list.")
	}
	used := map[int]bool{}
	for _, p := range v.Pairs {
		if !pos(p.Left) || !pos(p.Right) || p.Left >= p.Right || used[p.Left] || used[p.Right] || !drIsColor(p.Color) || !drFinite(p.Width) || p.Width < .5 || p.Width > 10 {
			return errors.New("Invalid or overlapping base pairs.")
		}
		used[p.Left], used[p.Right] = true, true
	}
	if v.Styles == nil {
		return errors.New("Invalid nucleotide styles.")
	}
	for k, s := range v.Styles {
		if !pos(k) || !drIsColor(s.Fill) || !drIsColor(s.Text) || !drFinite(s.Radius) || s.Radius < 3 || s.Radius > 24 || (s.Outline != "" && !drIsColor(s.Outline)) {
			return errors.New("Invalid nucleotide style.")
		}
	}
	if v.Annotations == nil || len(v.Annotations) > 1000 {
		return errors.New("Invalid annotation.")
	}
	for _, a := range v.Annotations {
		if !pos(a.Base) || !fin(a.DX) || !fin(a.DY) || drJsLen(a.Text) > 500 || !drIsColor(a.Color) {
			return errors.New("Invalid annotation.")
		}
	}
	for _, a := range v.Annotations {
		if a.Kind != "" && !inList(a.Kind, []string{"text", "region", "probe"}) {
			return errors.New("Unknown annotation type.")
		}
		if a.Bases != nil {
			bad := len(a.Bases) == 0 || len(a.Bases) > n
			for _, i := range a.Bases {
				bad = bad || !pos(i)
			}
			if bad {
				return errors.New("Invalid annotation anchors.")
			}
		}
		if a.Kind == "region" && (!pos(a.End) || a.End < a.Base) {
			return errors.New("Invalid highlight interval.")
		}
		if a.Kind == "probe" && !inList(a.Marker, []string{"arrow", "dot", "triangle"}) {
			return errors.New("Invalid chemical probe marker.")
		}
		if a.Free != nil && *a.Free && (a.X == nil || a.Y == nil || !fin(*a.X) || !fin(*a.Y)) {
			return errors.New("Invalid free annotation position.")
		}
		if !within(a.Size, 6, 48) || !within(a.Opacity, .05, 1) || !within(a.Width, 2, 80) || !within(a.Intensity, .25, 4) ||
			(a.Direction != "" && a.Direction != "in" && a.Direction != "out") {
			return errors.New("Invalid annotation style.")
		}
	}
	if e := v.Evidence; e != nil {
		_, paletteOK := drawPalettes[e.Palette]
		bad := len(e.Values) != n || !drFinite(e.Maximum) || !paletteOK || drJsLen(e.Kind) > 100
		if !bad && e.Minimum != nil {
			bad = !drFinite(*e.Minimum) || !(e.Maximum > *e.Minimum)
			for _, x := range e.Values {
				bad = bad || (x != nil && !drFinite(*x))
			}
		} else if !bad {
			bad = e.Maximum <= 0
			for _, x := range e.Values {
				bad = bad || x == nil || !drFinite(*x) || *x < 0 || *x > e.Maximum
			}
		}
		if bad {
			return errors.New("Invalid evidence colours.")
		}
	}
	return nil
}

// parseDrawingJSON reads a saved drawing for this prediction.
func parseDrawingJSON(data []byte, sequence, dbn string) (*drawing, error) {
	if len(data) > 5_000_000 {
		return nil, errors.New("Drawing JSON exceeds 5 MB.")
	}
	var head struct {
		Schema     any `json:"schema"`
		Sequence   any `json:"sequence"`
		DotBracket any `json:"dotBracket"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("This file is not JSON: %v", err)
	}
	if head.Schema != drawingSchema || head.Sequence != sequence || head.DotBracket != dbn {
		return nil, errors.New("This drawing belongs to a different prediction.")
	}
	var d drawing
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, errors.New("Invalid drawing JSON.")
	}
	if d.Pairs == nil {
		return nil, errors.New("Invalid pair list.")
	}
	if err := validateDrawing(&d, sequence, dbn); err != nil {
		return nil, err
	}
	return d.clone(), nil
}

// ---- the edited structure as CT, BPSEQ and DBN

func drawingCT(d *drawing) string {
	mates := drawMates(d.Pairs)
	n, c := len(d.Sequence), d.Cutpoint
	var b strings.Builder
	fmt.Fprintf(&b, "%d HYB2 manually edited drawing; energy not recalculated\n", n)
	// The chain break: the last base of strand A has no next, the first of
	// B no previous.
	for i := 0; i < n; i++ {
		prev, next := 0, 0
		if i != 0 && i != c {
			prev = i
		}
		if i+1 < n && i+1 != c {
			next = i + 2
		}
		fmt.Fprintf(&b, "%d\t%c\t%d\t%d\t%d\t%d", i+1, d.Sequence[i], prev, next, mates[i+1], i+1)
		if i+1 < n {
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	return b.String()
}

func drawingBPSEQ(d *drawing) string {
	mates := drawMates(d.Pairs)
	var b strings.Builder
	b.WriteString("# HYB2 manually edited drawing; energy not recalculated\n")
	for i := 0; i < len(d.Sequence); i++ {
		fmt.Fprintf(&b, "%d %c %d\n", i+1, d.Sequence[i], mates[i+1])
	}
	return b.String()
}

var drSpaces = regexp.MustCompile(`\s+`)

func drawingDBN(d *drawing) (string, error) {
	dbn, err := drawDotBracket(len(d.Sequence), d.Pairs)
	if err != nil {
		return "", err
	}
	cut := func(s string) string {
		if d.Cutpoint > 0 {
			return s[:d.Cutpoint] + "&" + s[d.Cutpoint:]
		}
		return s
	}
	return ">" + drSpaces.ReplaceAllString(d.Title, " ") + " (edited drawing; energy not recalculated)\n" + cut(d.Sequence) + "\n" + cut(dbn) + "\n", nil
}

// drawingJSON is the drawing as the web writes it (two-space indent).
func drawingJSON(d *drawing) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// ---- the scene: one list of shapes feeds the screen, SVG and EPS

type sceneItem struct {
	t                      string // rect, text, line, quad, path, circle, g
	x, y, w, h             float64
	x1, y1, x2, y2, cx, cy float64
	r                      float64
	fill, stroke           string
	width, strokeWidth     float64
	opacity, fillOpacity   *float64
	size                   float64
	anchor, text           string
	points                 []drawPoint
	closed, round          bool
	attrs                  [][2]string
	items                  []sceneItem
	// base and annotation are the nucleotide (1-based) and annotation
	// (index+1) a group draws, for hit tests.
	base, annotation int
}

// drawBounds is the canvas: the points, annotation text, numbering and
// linear-layout arcs.
func drawBounds(d *drawing) [4]float64 {
	x, y, right, bottom := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	addPoint := func(p drawPoint) {
		x, y = math.Min(x, p.X), math.Min(y, p.Y)
		right, bottom = math.Max(right, p.X), math.Max(bottom, p.Y)
	}
	for _, p := range d.Points {
		addPoint(p)
	}
	textPad := 0.0
	for _, a := range d.Annotations {
		p := d.anchor(a)
		addPoint(drawPoint{p.X + a.DX, p.Y + a.DY})
		size := 14.0
		if a.Size != nil && *a.Size != 0 {
			size = *a.Size
		}
		textPad = math.Max(textPad, float64(drJsLen(a.Text))*size/2)
	}
	pad := 65 + textPad
	arcs := 0.0
	if d.PairCurve == "arc" {
		for _, p := range d.Pairs {
			a, b := d.Points[p.Left-1], d.Points[p.Right-1]
			arcs = math.Max(arcs, math.Hypot(b.X-a.X, b.Y-a.Y)*.45)
		}
	}
	minW := 200.0
	if d.Evidence != nil {
		minW = 500
	}
	return [4]float64{x - pad, y - 90 - arcs, math.Max(minW, right-x+pad*2), math.Max(160, bottom-y+180+arcs)}
}

// numberDirection points a number label away from the partner (paired) or
// the loop centre (unpaired).
func numberDirection(d *drawing, i int, mates map[int]int) drawPoint {
	p := d.Points
	a, b := p[max(0, i-1)], p[min(len(p)-1, i+1)]
	l := math.Hypot(b.X-a.X, b.Y-a.Y)
	if l == 0 {
		l = 1
	}
	n := drawPoint{(b.Y - a.Y) / l, -(b.X - a.X) / l}
	if d.PairCurve == "arc" {
		return drawPoint{-n.X, -n.Y}
	}
	toward := drawPoint{(a.X+b.X)/2 - p[i].X, (a.Y+b.Y)/2 - p[i].Y}
	if mate, ok := mates[i+1]; ok {
		toward = drawPoint{p[mate-1].X - p[i].X, p[mate-1].Y - p[i].Y}
	}
	if n.X*toward.X+n.Y*toward.Y > 0 {
		n = drawPoint{-n.X, -n.Y}
	}
	return n
}

func (d *drawing) style(i int) baseStyle {
	s, ok := d.Styles[i]
	if !ok {
		return drawDefault
	}
	if s.Fill == "" {
		s.Fill = drawDefault.Fill
	}
	if s.Text == "" {
		s.Text = drawDefault.Text
	}
	if s.Radius == 0 {
		s.Radius = drawDefault.Radius
	}
	return s
}

func drOrF(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

// drOrNZ is JavaScript's p || def for an optional number.
func drOrNZ(p *float64, def float64) float64 {
	if p == nil || *p == 0 {
		return def
	}
	return *p
}

func drOrS(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func drawScene(d *drawing, selection *[2]int, b [4]float64) []sceneItem {
	items := []sceneItem{
		{t: "rect", x: b[0], y: b[1], w: b[2], h: b[3], fill: d.Background, attrs: [][2]string{{"data-editor-background", "true"}}},
		{t: "text", x: b[0] + 20, y: b[1] + 28, size: drOrNZ(d.TitleSize, 18), fill: drOrS(d.TitleColor, "#222222"), text: d.Title},
	}
	line := func(p, q drawPoint, stroke string, width float64, opacity *float64) sceneItem {
		return sceneItem{t: "line", x1: p.X, y1: p.Y, x2: q.X, y2: q.Y, stroke: stroke, width: width, opacity: opacity}
	}
	for _, a := range d.Annotations {
		if a.Kind != "region" {
			continue
		}
		points := d.Points[a.Base-1 : a.End]
		opacity, width := drOrF(a.Opacity, .25), drOrNZ(a.Width, 32)
		items = append(items, sceneItem{t: "path", points: points, stroke: a.Color, opacity: drFptr(opacity), width: width, round: true})
		if len(points) == 1 {
			items = append(items, sceneItem{t: "circle", cx: points[0].X, cy: points[0].Y, r: width / 2, fill: a.Color, fillOpacity: drFptr(opacity)})
		}
	}
	if e := d.Evidence; e != nil {
		lo := drOrF(e.Minimum, 0)
		caption := e.Kind + " · " + e.Source + " · " + drNum(lo) + "–" + drNum(e.Maximum)
		if e.Overrides {
			caption += " · manual colours override"
		}
		items = append(items, sceneItem{t: "text", x: b[0] + 20, y: b[1] + 48, size: 10, fill: "#333333", text: caption})
		for i := 0; i < 40; i++ {
			items = append(items, sceneItem{t: "rect", x: b[0] + 20 + float64(i)*3, y: b[1] + 55, w: 3, h: 5, fill: drMapColor(e, lo+(e.Maximum-lo)*float64(i)/39)})
		}
	}
	if backbone := drOrF(d.BackboneWidth, 1.5); backbone > 0 {
		for i := 1; i < len(d.Points); i++ {
			if i != d.Cutpoint {
				items = append(items, line(d.Points[i-1], d.Points[i], d.Backbone, backbone, nil))
			}
		}
	}
	if style := drOrS(d.PairStyle, "simple"); style != "none" {
		for _, p := range d.Pairs {
			a, c := d.Points[p.Left-1], d.Points[p.Right-1]
			mid := drawPoint{(a.X + c.X) / 2, (a.Y + c.Y) / 2}
			bases := strings.ReplaceAll(strings.ToUpper(d.Sequence[p.Left-1:p.Left]+d.Sequence[p.Right-1:p.Right]), "T", "U")
			switch {
			case d.PairCurve == "arc":
				dx, dy := c.X-a.X, c.Y-a.Y
				items = append(items, sceneItem{t: "quad", x1: a.X, y1: a.Y, cx: mid.X + dy*.9, cy: mid.Y - dx*.9, x2: c.X, y2: c.Y, stroke: p.Color, width: p.Width})
			case style == "rnaviz":
				items = append(items, line(drawPoint{a.X + (c.X-a.X)/3, a.Y + (c.Y-a.Y)/3}, drawPoint{c.X - (c.X-a.X)/3, c.Y - (c.Y-a.Y)/3}, p.Color, p.Width, nil))
			default:
				items = append(items, line(a, c, p.Color, p.Width, nil))
			}
			// Leontis–Westhof glyphs for cis Watson–Crick pairs: canonical
			// pairs are a line, G·U wobbles add a filled dot and other
			// pairs an open circle.
			if style == "lw" && !drawCanonical[bases] {
				fill := d.Background
				if drawWobble[bases] {
					fill = p.Color
				}
				items = append(items, sceneItem{t: "circle", cx: mid.X, cy: mid.Y, r: 4, fill: fill, stroke: p.Color, strokeWidth: 1.5})
			}
		}
	}
	mates := drawMates(d.Pairs)
	letter := drOrNZ(d.BaseFontSize, 12)
	showNumbers := d.Numbering == nil || *d.Numbering
	for i, p := range d.Points {
		s := d.style(i + 1)
		selected := selection != nil && i+1 >= selection[0] && i+1 <= selection[1]
		stroke, sw := drOrS(s.Outline, "#3C5488"), 1.0
		if selected {
			stroke, sw = "#E64B35", 3
		}
		items = append(items, sceneItem{t: "g", base: i + 1, attrs: [][2]string{{"data-editor-base", strconv.Itoa(i + 1)}, {"tabindex", "0"}, {"role", "button"},
			{"aria-label", fmt.Sprintf("Move nucleotide %d %c", i+1, d.Sequence[i])}}, items: []sceneItem{
			{t: "circle", cx: p.X, cy: p.Y, r: s.Radius, fill: s.Fill, stroke: stroke, strokeWidth: sw},
			{t: "text", x: p.X, y: p.Y + letter/3, size: letter, anchor: "middle", fill: s.Text, text: d.Sequence[i : i+1]}}})
		if showNumbers && (i == 0 || i == len(d.Points)-1 || (i+1)%d.NumberInterval == 0) {
			n := numberDirection(d, i, mates)
			color := drOrS(d.NumberColor, "#444444")
			items = append(items, line(drawPoint{p.X + n.X*(s.Radius+2), p.Y + n.Y*(s.Radius+2)}, drawPoint{p.X + n.X*(s.Radius+8), p.Y + n.Y*(s.Radius+8)}, color, .8, nil),
				sceneItem{t: "text", x: p.X + n.X*(s.Radius+16), y: p.Y + n.Y*(s.Radius+16) + 3.5, size: 10, anchor: "middle", fill: color, text: strconv.Itoa(i + 1)})
		}
	}
	for i, a := range d.Annotations {
		p := d.anchor(a)
		q := drawPoint{p.X + a.DX, p.Y + a.DY}
		k := drOrNZ(a.Intensity, 1)
		free := a.Free != nil && *a.Free
		var group []sceneItem
		if !free {
			group = append(group, line(p, q, a.Color, 1, nil))
		}
		if a.Kind == "probe" {
			switch a.Marker {
			case "dot":
				group = append(group, sceneItem{t: "circle", cx: q.X, cy: q.Y, r: 5 * k, fill: a.Color})
			case "triangle":
				group = append(group, sceneItem{t: "path", points: []drawPoint{{q.X, q.Y - 6*k}, {q.X - 6*k, q.Y + 6*k}, {q.X + 6*k, q.Y + 6*k}}, closed: true, fill: a.Color})
			default:
				tip, from := p, q
				if a.Direction == "out" {
					tip, from = q, p
				}
				dx, dy := tip.X-from.X, tip.Y-from.Y
				l := math.Hypot(dx, dy)
				if l == 0 {
					l = 1
				}
				ux, uy := dx/l, dy/l
				group = append(group, sceneItem{t: "path", points: []drawPoint{tip, {tip.X - ux*10*k - uy*4*k, tip.Y - uy*10*k + ux*4*k}, {tip.X - ux*10*k + uy*4*k, tip.Y - uy*10*k - ux*4*k}}, closed: true, fill: a.Color})
			}
		}
		ty := q.Y - 8
		if free {
			ty = q.Y
		}
		group = append(group, sceneItem{t: "text", x: q.X, y: ty, size: drOrNZ(a.Size, 14), fill: a.Color, text: a.Text})
		items = append(items, sceneItem{t: "g", annotation: i + 1, attrs: [][2]string{{"data-editor-annotation", strconv.Itoa(i)}, {"role", "button"}, {"tabindex", "0"},
			{"aria-label", fmt.Sprintf("Annotation %d %s", i+1, a.Text)}}, items: group})
	}
	return items
}

func drSvgItem(b *strings.Builder, it sceneItem) {
	attrs := func() string {
		var s strings.Builder
		for _, a := range it.attrs {
			s.WriteString(" " + a[0] + `="` + drXmlText(a[1]) + `"`)
		}
		return s.String()
	}
	opacity := func(name string, v *float64) string {
		if v == nil || *v == 1 {
			return ""
		}
		return " " + name + `="` + drJsNum(*v) + `"`
	}
	switch it.t {
	case "g":
		b.WriteString("<g" + attrs() + ">")
		for _, c := range it.items {
			drSvgItem(b, c)
		}
		b.WriteString("</g>")
	case "rect":
		fmt.Fprintf(b, `<rect%s x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, attrs(), drNum(it.x), drNum(it.y), drNum(it.w), drNum(it.h), it.fill)
	case "line":
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%s"%s/>`, drNum(it.x1), drNum(it.y1), drNum(it.x2), drNum(it.y2), it.stroke, drJsNum(it.width), opacity("stroke-opacity", it.opacity))
	case "quad":
		fmt.Fprintf(b, `<path d="M%s,%s Q%s,%s %s,%s" fill="none" stroke="%s" stroke-width="%s"/>`, drNum(it.x1), drNum(it.y1), drNum(it.cx), drNum(it.cy), drNum(it.x2), drNum(it.y2), it.stroke, drJsNum(it.width))
	case "path":
		parts := make([]string, len(it.points))
		for i, p := range it.points {
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}
			parts[i] = cmd + drNum(p.X) + "," + drNum(p.Y)
		}
		d := strings.Join(parts, " ")
		if it.closed {
			d += " Z"
		}
		stroke := ""
		if it.stroke != "" {
			stroke = ` stroke="` + it.stroke + `"` + opacity("stroke-opacity", it.opacity) + ` stroke-width="` + drJsNum(it.width) + `"`
		}
		round := ""
		if it.round {
			round = ` stroke-linecap="round" stroke-linejoin="round"`
		}
		fmt.Fprintf(b, `<path d="%s" fill="%s"%s%s/>`, d, drOrS(it.fill, "none"), stroke, round)
	case "circle":
		stroke := ""
		if it.stroke != "" {
			stroke = ` stroke="` + it.stroke + `" stroke-width="` + drJsNum(it.strokeWidth) + `"`
		}
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="%s"%s%s/>`, drNum(it.cx), drNum(it.cy), drNum(it.r), it.fill, opacity("fill-opacity", it.fillOpacity), stroke)
	case "text":
		anchor := ""
		if it.anchor != "" {
			anchor = ` text-anchor="` + it.anchor + `"`
		}
		fmt.Fprintf(b, `<text x="%s" y="%s" font-size="%s"%s fill="%s">%s</text>`, drNum(it.x), drNum(it.y), drJsNum(it.size), anchor, it.fill, drXmlText(it.text))
	}
}

// drawingSVG is the web's SVG of the drawing (the editor's "SVG image").
func drawingSVG(d *drawing, selection *[2]int, box *[4]float64) string {
	b := drawBounds(d)
	if box != nil {
		b = *box
	}
	var s strings.Builder
	view := make([]string, 4)
	for i, v := range b {
		view[i] = drNum(v)
	}
	s.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="` + strings.Join(view, " ") + `" width="1000" height="700" role="img" aria-label="Editable RNA drawing"><g font-family="` + drOrS(d.Font, "sans-serif") + `">`)
	for _, it := range drawScene(d, selection, b) {
		drSvgItem(&s, it)
	}
	s.WriteString("</g></svg>")
	return s.String()
}

var drEpsNonASCII = regexp.MustCompile(`[^\x20-\x7e]`)

// drawingEPS is Encapsulated PostScript for journals and vector editors;
// opacity is flattened onto the background colour.
func drawingEPS(d *drawing) string {
	b := drawBounds(d)
	k := math.Min(1, 800/math.Max(b[2], b[3]))
	W, H := int(math.Ceil(b[2]*k)), int(math.Ceil(b[3]*k))
	bg := drHexRGB(d.Background)
	f2 := func(v float64) string { return drToFixed(v, 2) }
	X := func(x float64) string { return f2((x - b[0]) * k) }
	Y := func(y float64) string { return f2((b[1] + b[3] - y) * k) }
	rgb := func(c string, o *float64) string {
		a := drOrF(o, 1)
		v := drHexRGB(c)
		parts := make([]string, 3)
		for i := range parts {
			parts[i] = drToFixed((float64(v[i])*a+float64(bg[i])*(1-a))/255, 3)
		}
		return strings.Join(parts, " ") + " setrgbcolor"
	}
	str := func(t string) string {
		t = strings.NewReplacer("′", "'", "’", "'", "–", "-", "—", "-", "·", "|").Replace(t)
		t = drEpsNonASCII.ReplaceAllString(t, "?")
		t = strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(t)
		return "(" + t + ")"
	}
	font := drawFonts[d.Font]
	if font == "" {
		font = "Helvetica"
	}
	out := []string{"%!PS-Adobe-3.0 EPSF-3.0", fmt.Sprintf("%%%%BoundingBox: 0 0 %d %d", W, H), "%%Title: " + drEpsNonASCII.ReplaceAllString(d.Title, "?"),
		"%%Creator: HYB2 Web Lite drawing editor", "%%EndComments", "1 setlinecap 1 setlinejoin"}
	var emit func(it sceneItem)
	emit = func(it sceneItem) {
		switch it.t {
		case "g":
			for _, c := range it.items {
				emit(c)
			}
		case "rect":
			out = append(out, rgb(it.fill, nil)+" newpath "+X(it.x)+" "+Y(it.y+it.h)+" "+f2(it.w*k)+" "+f2(it.h*k)+" rectfill")
		case "line":
			out = append(out, rgb(it.stroke, it.opacity)+" "+f2(it.width*k)+" setlinewidth newpath "+X(it.x1)+" "+Y(it.y1)+" moveto "+X(it.x2)+" "+Y(it.y2)+" lineto stroke")
		case "quad":
			c1 := drawPoint{it.x1 + 2.0/3*(it.cx-it.x1), it.y1 + 2.0/3*(it.cy-it.y1)}
			c2 := drawPoint{it.x2 + 2.0/3*(it.cx-it.x2), it.y2 + 2.0/3*(it.cy-it.y2)}
			out = append(out, rgb(it.stroke, nil)+" "+f2(it.width*k)+" setlinewidth newpath "+X(it.x1)+" "+Y(it.y1)+" moveto "+X(c1.X)+" "+Y(c1.Y)+" "+X(c2.X)+" "+Y(c2.Y)+" "+X(it.x2)+" "+Y(it.y2)+" curveto stroke")
		case "path":
			parts := make([]string, len(it.points))
			for i, p := range it.points {
				op := " lineto"
				if i == 0 {
					op = " moveto"
				}
				parts[i] = X(p.X) + " " + Y(p.Y) + op
			}
			path := "newpath " + strings.Join(parts, " ")
			if it.closed {
				path += " closepath"
			}
			if it.fill != "" {
				out = append(out, rgb(it.fill, nil)+" "+path+" fill")
			}
			if it.stroke != "" {
				out = append(out, rgb(it.stroke, it.opacity)+" "+f2(it.width*k)+" setlinewidth "+path+" stroke")
			}
		case "circle":
			s := "newpath " + X(it.cx) + " " + Y(it.cy) + " " + f2(it.r*k) + " 0 360 arc closepath gsave " + rgb(it.fill, it.fillOpacity) + " fill grestore"
			if it.stroke != "" {
				s += " " + rgb(it.stroke, nil) + " " + f2(it.strokeWidth*k) + " setlinewidth stroke"
			} else {
				s += " newpath"
			}
			out = append(out, s)
		case "text":
			s := rgb(it.fill, nil) + " /" + font + " " + f2(it.size*k) + " selectfont " + X(it.x) + " " + Y(it.y) + " moveto " + str(it.text)
			if it.anchor == "middle" {
				s += " dup stringwidth pop 2 div neg 0 rmoveto"
			}
			out = append(out, s+" show")
		}
	}
	for _, it := range drawScene(d, nil, b) {
		emit(it)
	}
	out = append(out, "showpage", "%%EOF")
	return strings.Join(out, "\n") + "\n"
}

func drRanges(list []int) string {
	sorted := append([]int{}, list...)
	sort.Ints(sorted)
	var out [][2]int
	for _, i := range sorted {
		if len(out) > 0 && i == out[len(out)-1][1]+1 {
			out[len(out)-1][1] = i
		} else {
			out = append(out, [2]int{i, i})
		}
	}
	parts := make([]string, len(out))
	for i, r := range out {
		if r[0] == r[1] {
			parts[i] = strconv.Itoa(r[0])
		} else {
			parts[i] = fmt.Sprintf("%d-%d", r[0], r[1])
		}
	}
	return strings.Join(parts, ",")
}

// drawingVARNA is a VARNAcmd invocation that redraws this structure in the
// Java VARNA application with the same colours, highlights and
// annotations. VARNA computes its own layout, so dragged positions are not
// carried over.
func drawingVARNA(d *drawing) (string, error) {
	q := func(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }
	clean := func(t string) string { return strings.NewReplacer(":", " ", ";", " ", ",", " ").Replace(t) }
	dbn, err := drawDotBracket(len(d.Sequence), d.Pairs)
	if err != nil {
		return "", err
	}
	kind, flat, spacing := "simple", true, 1.0
	if d.Layout != nil {
		kind, flat = drOrS(d.Layout.Kind, "simple"), d.Layout.Flat
		if d.Layout.Spacing != 0 {
			spacing = d.Layout.Spacing
		}
	}
	bp := "#3C5488"
	if len(d.Pairs) > 0 {
		bp = d.Pairs[0].Color
	}
	period := d.NumberInterval
	if d.Numbering != nil && !*d.Numbering {
		period = 0
	}
	args := [][2]string{{"-sequenceDBN", q(d.Sequence)}, {"-structureDBN", q(dbn)},
		{"-algorithm", map[string]string{"radiate": "radiate", "simple": "radiate", "circular": "circular", "linear": "line"}[kind]},
		{"-flat", strconv.FormatBool(flat)}, {"-spaceBetweenBases", drNum(spacing)}, {"-title", q(d.Title)},
		{"-titleColor", drOrS(d.TitleColor, "#222222")}, {"-titleSize", drJsNum(drOrNZ(d.TitleSize, 18))}, {"-bpStyle", drOrS(d.PairStyle, "simple")},
		{"-background", d.Background}, {"-backbone", d.Backbone}, {"-bp", bp}, {"-periodNum", strconv.Itoa(period)},
		{"-baseNum", drOrS(d.NumberColor, "#444444")}, {"-resolution", "4.0"}, {"-o", "hyb2-drawing.svg"}}
	e := d.Evidence
	mapped := e != nil && !e.Overrides
	if mapped {
		lo := drOrF(e.Minimum, 0)
		values := make([]string, len(e.Values))
		for i, v := range e.Values {
			values[i] = drNum(drOrF(v, lo))
		}
		stops := drPaletteOr(e.Palette)
		style := make([]string, len(stops))
		for i, s := range stops {
			style[i] = drNum(lo+s.at*(e.Maximum-lo)) + ":" + s.color
		}
		args = append(args, [2]string{"-colorMap", q(strings.Join(values, ";"))}, [2]string{"-colorMapMin", drNum(lo)}, [2]string{"-colorMapMax", drNum(e.Maximum)},
			[2]string{"-colorMapStyle", q(strings.Join(style, ","))}, [2]string{"-colorMapCaption", q(clean(e.Kind))})
	}
	keys := make([]int, 0, len(d.Styles))
	for k := range d.Styles {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var order []string
	groups := map[string][]int{}
	for _, i := range keys {
		s := d.Styles[i]
		if mapped && i-1 < len(e.Values) && e.Values[i-1] != nil && s.Outline == "" {
			continue
		}
		key := "fill=" + s.Fill + ",outline=" + drOrS(s.Outline, "#3C5488") + ",label=" + s.Text + ",number=" + drOrS(d.NumberColor, "#444444")
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	for k, key := range order {
		args = append(args, [2]string{fmt.Sprintf("-basesStyle%d", k+1), q(key)}, [2]string{fmt.Sprintf("-applyBasesStyle%don", k+1), q(drRanges(groups[key]))})
	}
	var regions, probes, texts []drawAnnotation
	for _, a := range d.Annotations {
		switch {
		case a.Kind == "region":
			regions = append(regions, a)
		case a.Kind == "probe":
			probes = append(probes, a)
		case a.Text != "":
			texts = append(texts, a)
		}
	}
	if len(regions) > 0 {
		parts := make([]string, len(regions))
		for i, a := range regions {
			parts[i] = fmt.Sprintf("%d-%d:radius=%s,fill=%s,outline=%s", a.Base, a.End, drNum(drOrNZ(a.Width, 32)/2), a.Color, a.Color)
		}
		args = append(args, [2]string{"-highlightRegion", q(strings.Join(parts, ";"))})
	}
	if len(texts) > 0 {
		parts := make([]string, len(texts))
		for i, a := range texts {
			var where string
			switch {
			case a.Free != nil && *a.Free:
				where = "type=P,x=" + drNum(drOrF(a.X, 0)+a.DX) + ",y=" + drNum(drOrF(a.Y, 0)+a.DY)
			case a.AnchorType == "helix":
				where = "type=H,anchor=" + strconv.Itoa(a.Base)
			case a.AnchorType == "loop":
				where = "type=L,anchor=" + strconv.Itoa(a.Base)
			default:
				where = "type=B,anchor=" + strconv.Itoa(a.Base)
			}
			parts[i] = clean(a.Text) + ":" + where + ",color=" + a.Color + ",size=" + drJsNum(drOrNZ(a.Size, 14))
		}
		args = append(args, [2]string{"-annotations", q(strings.Join(parts, ";"))})
	}
	if len(probes) > 0 {
		parts := make([]string, len(probes))
		for i, a := range probes {
			at := min(a.Base, len(d.Sequence)-1)
			glyph := "arrow"
			if a.Marker == "triangle" || a.Marker == "dot" {
				glyph = a.Marker
			}
			parts[i] = fmt.Sprintf("%d-%d:glyph=%s,dir=%s,intensity=%s,color=%s", at, at+1, glyph, drOrS(a.Direction, "in"), drNum(drOrNZ(a.Intensity, 1)), a.Color)
		}
		args = append(args, [2]string{"-chemProb", q(strings.Join(parts, ";"))})
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# VARNA command generated by HYB2 Web Lite. Requires VARNA (GPL, varna.lri.fr) and Java.\n")
	b.WriteString("# VARNA computes its own layout: dragged positions and the ViennaRNA polygon layout are not carried over.\n")
	b.WriteString("# Colour-map stops and option spellings follow VARNA 3.9; check them against your VARNA version.\n")
	if d.Cutpoint > 0 {
		fmt.Fprintf(&b, "# Two strands: A is 1-%d, B %d-%d; VARNA draws them as one chain.\n", d.Cutpoint, d.Cutpoint+1, len(d.Sequence))
	}
	b.WriteString("java -cp VARNA.jar fr.orsay.lri.varna.applications.VARNAcmd \\\n")
	lines := make([]string, len(args))
	for i, a := range args {
		lines[i] = "  " + a[0] + " " + a[1]
	}
	b.WriteString(strings.Join(lines, " \\\n") + "\n")
	return b.String(), nil
}
