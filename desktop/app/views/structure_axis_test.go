package views

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/widgets"
)

// stReversedCofold is a CPLcofold record whose region 1 (strand A) is
// 500-600 and region 2 (strand B) 100-200 of one RNA: the runtime keeps
// the regions' order, so its positions run 500..600 then 100..200 and its
// pairs between the strands come I > J, as read_structure gives them.
func stReversedCofold() structureResult {
	const gene = "ZIKV_African_KX377335-1_virusRNA"
	seq := []byte(strings.Repeat("GCAU", 51)[:202])
	dbn := []byte(strings.Repeat(".", 202))
	// local pairs: intra-A (1,20), between the strands (30,150) and
	// (31,149), intra-B (110,130), and a pseudoknot (25,140)
	for _, pr := range [][3]int{{1, 20, 0}, {30, 150, 0}, {31, 149, 0}, {110, 130, 0}, {25, 140, 1}} {
		dbn[pr[0]-1], dbn[pr[1]-1] = dbnOpen[pr[2]], dbnClose[pr[2]]
	}
	var positions []int
	for p := 500; p <= 600; p++ {
		positions = append(positions, p)
	}
	for p := 100; p <= 200; p++ {
		positions = append(positions, p)
	}
	r := structureRecord{WindowStart: 100, WindowEnd: 600, Sequence: string(seq), DBN: string(dbn), Positions: positions, Cutpoint: 101,
		Segments: []core.Region{{GeneID: gene, Start: 500, End: 600}, {GeneID: gene, Start: 100, End: 200}}}
	for k, pr := range stParseDBN(r.DBN, 1) {
		r.Pairs = append(r.Pairs, [2]int{positions[pr.I-1], positions[pr.J-1]}) // as the runtime: I > J between the strands
		r.Bonus = append(r.Bonus, stFloat(float64(k+1)))
	}
	res := structureResult{Records: []structureRecord{r}}
	res.Meta.Engine, res.Meta.GeneID, res.Structure.GeneID = "CPLcofold", gene, gene
	res.Structure.Regions = []core.Region{{GeneID: gene, Start: 500, End: 600}, {GeneID: gene, Start: 100, End: 200}}
	return res
}

// Regions 500-600 then 100-200: pairs lower position first everywhere, the
// chain break collapses 201-499 with strand B first ("B | A"), and the
// statistics, inspector and exports agree with the A+B structure.
func TestStructureCofoldReversedRegions(t *testing.T) {
	res := stReversedCofold()
	if !slices.Contains(res.Records[0].Pairs, [2]int{529, 148}) {
		t.Fatalf("fixture pairs %v", res.Records[0].Pairs)
	}
	stNormalise(&res)
	r := res.Records[0]
	pairs := stRecordPairs(r)
	if len(pairs) != 5 || !slices.Contains(pairs, stPair{I: 148, J: 529}) || !slices.Contains(pairs, stPair{I: 138, J: 524, Layer: 1}) {
		t.Fatalf("pairs %v", pairs)
	}
	for _, pr := range pairs {
		if pr.I >= pr.J {
			t.Fatalf("pair %v not I < J", pr)
		}
	}
	for _, pr := range r.Pairs {
		if pr[0] >= pr[1] {
			t.Fatalf("record pair %v not I < J", pr)
		}
	}
	// a candidate shown again keeps the order
	r.Candidates = []stCandidate{{Structure: r.DBN}}
	if !r.selectCandidate(0) || !slices.Contains(r.Pairs, [2]int{148, 529}) || slices.ContainsFunc(r.Pairs, func(pr [2]int) bool { return pr[0] >= pr[1] }) {
		t.Fatalf("candidate pairs %v", r.Pairs)
	}
	r = res.Records[0] // as folded, with its bonus
	// the regions, the span and the nucleotides drawn
	if rg := r.ranges(); !slices.Equal(rg, [][2]int{{500, 600}, {100, 200}}) {
		t.Fatalf("ranges %v", rg)
	}
	if s, e := r.span(); s != 100 || e != 600 {
		t.Fatalf("span %d-%d", s, e)
	}
	if r.localOf(148) != 150 || r.localOf(529) != 30 || r.localOf(300) != 0 || r.rnaPosition(150) != 148 {
		t.Fatalf("local %d %d %d", r.localOf(148), r.localOf(529), r.localOf(300))
	}
	// statistics: a result against itself shares every pair
	_, _, s := stCompare([]structureRecord{r}, []structureRecord{r}, true)
	if s.Shared != 5 || s.OnlyA != 0 || s.OnlyB != 0 || s.Jaccard != "1.000" || s.KnottedA != 1 {
		t.Fatalf("stats %+v", s)
	}
	// crossing pairs as on the A+B strand
	window := structureRecord{WindowStart: 1, WindowEnd: 202, Sequence: r.Sequence, DBN: r.DBN}
	if got, want := stShownCrossingPairs([]structureRecord{r}), stShownCrossingPairs([]structureRecord{window}); got != want || want == 0 {
		t.Fatalf("crossing %d, A+B %d", got, want)
	}
	// each pair keeps its bonus
	shown := stShownPairs([]structureRecord{r}, nil)
	if i := slices.IndexFunc(shown, func(p stPairInfo) bool { return p.I == 148 && p.J == 529 }); i < 0 || shown[i].Bonus != 5 {
		t.Fatalf("shown %+v", shown)
	}
	if text := stNucleotideText([]structureRecord{r}, 148, "CPLcofold"); !strings.Contains(text, "paired with 529") || !strings.Contains(text, "chimera support 5") {
		t.Fatalf("inspector %q", text)
	}
	// the chain break: strand B (100-200) first, 201-499 take no room
	g := stChainGap(r)
	if g != (widgets.Gap{From: 500, To: 200}) || g.Label() != "B | A" || g.Axis(500) != 201 || g.Position(201) != 500 || g.Axis(150) != 150 {
		t.Fatalf("gap %+v axis %v", g, g.Axis(500))
	}
	for _, tick := range g.Ticks(100, 600, 8) {
		if tick > 200 && tick < 500 {
			t.Fatalf("tick %v inside the gap", tick)
		}
	}
	// pairs TSV: A+B positions lower first, bases and RNA positions with them
	rows := stPairsRows([]structureRecord{r}, "CPLcofold", nil, true)
	var between []string
	for _, row := range rows {
		i, j := row[0], row[2]
		if i == "30" {
			between = row
		}
		if x, _ := strconv.Atoi(i); x >= func() int { y, _ := strconv.Atoi(j); return y }() {
			t.Fatalf("row %v not left < right", row)
		}
	}
	want := []string{"30", string(r.Sequence[29]), "150", string(r.Sequence[149])}
	if between == nil || !slices.Equal(between[:4], want) ||
		!slices.Equal(between[13:], []string{"101", "A", "30", "B", "49", res.Meta.GeneID, "529", res.Meta.GeneID, "148"}) {
		t.Fatalf("between %v", between)
	}
	// CT: each base's partner on the A+B strand
	ct := stCTText("x", []structureRecord{r})
	if !strings.Contains(ct, "\n30\t"+string(r.Sequence[29])+"\t29\t31\t150\t30\n") || !strings.Contains(ct, "\n150\t"+string(r.Sequence[149])+"\t149\t151\t30\t150\n") {
		t.Fatalf("ct %.300q", ct)
	}
	// the report's pairs: A+B positions lower first, strands and RNA positions
	report := stReportPairs(&res, []structureRecord{r})
	if i := slices.IndexFunc(report, func(m map[string]any) bool { return m["left"] == 30 }); i < 0 ||
		report[i]["right"] != 150 || report[i]["leftRnaPosition"] != 529 || report[i]["rightStrand"] != "B" || report[i]["intermolecular"] != true ||
		report[i]["leftBase"] != string(r.Sequence[29]) {
		t.Fatalf("report %v", report)
	}
}

// syncArcs on the reversed regions: arcs lower position first, the chain
// break with strand B first, the view over both regions.
func TestStructureCofoldReversedArcs(t *testing.T) {
	a, _ := openCS(t)
	p := a.PageFor("structure").(*structurePage)
	res := stReversedCofold()
	stNormalise(&res)
	p.viewA, p.record, p.cache["rev"] = "rev", "all", &res
	p.syncArcs(&res, nil)
	for _, pr := range p.arcs.A {
		if pr.I >= pr.J {
			t.Fatalf("arc %v", pr)
		}
	}
	if p.arcs.Gap != (widgets.Gap{From: 500, To: 200}) || p.arcs.Start != 100 || p.arcs.End != 600 {
		t.Fatalf("gap %+v range %v-%v", p.arcs.Gap, p.arcs.Start, p.arcs.End)
	}
	spec, _ := p.arcSpec(&res)
	if svg, _ := spec.SVG(); !strings.Contains(svg, `data-chain-break="200">B | A<`) {
		t.Fatal("svg lacks the B | A chain break")
	}
}

// stGeneX is a project RNA (its length is the reference's, far more than
// an A+B strand).
const stGeneX = "ZIKV_African_KX377335-1_virusRNA"

// stTwoRNACofold is a CPLcofold result of two RNAs, drawn on A+B indices.
func stTwoRNACofold() structureResult {
	res := structureResult{}
	seq := strings.Repeat("GCAU", 25)
	r := structureRecord{WindowStart: 1, WindowEnd: 100, Sequence: seq, DBN: "((" + strings.Repeat(".", 96) + "))", Cutpoint: 40,
		Segments: []core.Region{{GeneID: stGeneX, Start: 1, End: 40}, {GeneID: "geneY", Start: 11, End: 70}}}
	for k := 1; k <= 100; k++ {
		r.Positions = append(r.Positions, k)
	}
	r.Pairs = [][2]int{{1, 100}, {2, 99}}
	res.Records = []structureRecord{r}
	res.Meta.Engine, res.Meta.GeneID, res.Structure.GeneID = "CPLcofold", stGeneX, stGeneX
	res.Structure.Regions = r.Segments
	return res
}

// A two-RNA (or overlapping) CPLcofold A is on A+B indices: no gene track
// or differential arcs (a note says why), and B only when it is a fold of
// the same two regions.
func TestStructureCofoldLocalAxis(t *testing.T) {
	a, _ := openCS(t)
	p := a.PageFor("structure").(*structurePage)
	res := stTwoRNACofold()
	if !stLocalAxis(res.Records[0]) || !stRegionsLocal([]core.Region{{GeneID: "g", Start: 1, End: 50}, {GeneID: "g", Start: 40, End: 90}}) ||
		stRegionsLocal([]core.Region{{GeneID: "g", Start: 1, End: 50}, {GeneID: "g", Start: 60, End: 90}}) {
		t.Fatal("local axis")
	}
	p.layers.annot[stGeneX] = &stAnnotation{features: []widgets.Feature{{Start: 1, End: 30, Name: "C"}}}
	p.viewA, p.record, p.cache["two"] = "two", "all", &res
	p.syncArcs(&res, nil)
	if len(p.arcs.Features) != 0 || len(p.arcs.Overlay) != 0 || !slices.Contains(p.layers.notes, stLocalAxisNote) || p.arcs.Length != 100 {
		t.Fatalf("features %v notes %q length %d", p.arcs.Features, p.layers.notes, p.arcs.Length)
	}
	// a one-strand B is not compared with it
	one := structureResult{Records: []structureRecord{{WindowStart: 1, WindowEnd: 12, Sequence: "GGGAAACCCUUU", DBN: "(((...)))...", Pairs: [][2]int{{1, 9}, {2, 8}, {3, 7}}}}}
	one.Meta.GeneID = stGeneX
	if stCompareBlock(&res, &one) == "" || stCompareBlock(&one, &res) == "" {
		t.Fatal("one strand compared with A+B indices")
	}
	p.viewB, p.cache["one"] = "one", &one
	p.syncArcs(&res, &one)
	if len(p.arcs.B) != 0 || p.shownB != "" || !slices.ContainsFunc(p.layers.notes, func(n string) bool { return strings.HasPrefix(n, "B is not shown: a two-RNA") }) {
		t.Fatalf("B drawn %v notes %q", p.arcs.B, p.layers.notes)
	}
	if _, name := p.arcSpec(&res); strings.Contains(name, "vs") {
		t.Fatalf("A alone named %q", name)
	}
	// a fold of the same two regions is
	same := stTwoRNACofold()
	if stCompareBlock(&res, &same) != "" {
		t.Fatal("same regions blocked")
	}
	p.viewB, p.cache["same"] = "same", &same
	p.syncArcs(&res, &same)
	if len(p.arcs.B) != 2 || p.shownB != "same" || p.stats.Shared != 2 {
		t.Fatalf("B %v stats %+v", p.arcs.B, p.stats)
	}
	// the list's "Compare as B" follows the entries' regions
	p.cache = map[string]*structureResult{}
	p.structures = []structureEntry{{ID: "two", GeneID: stGeneX, Regions: res.Structure.Regions}, {ID: "one", GeneID: stGeneX, Mode: "genome"},
		{ID: "same", GeneID: stGeneX, Regions: res.Structure.Regions}}
	if p.compareAsBBlock(&p.structures[1]) == "" || p.compareAsBBlock(&p.structures[2]) != "" {
		t.Fatal("Compare as B")
	}
	p.viewA = "one"
	if p.compareAsBBlock(&p.structures[0]) == "" {
		t.Fatal("A+B result compared as B with one strand")
	}
}

// The arc exports wait while B is read or aligned (the arcs hold A alone),
// and name and label B only once the arcs hold it.
func TestStructureExportWaitsForB(t *testing.T) {
	a, _ := openCS(t)
	p := a.PageFor("structure").(*structurePage)
	resA := structureResult{Records: []structureRecord{{WindowStart: 1, WindowEnd: 12, Sequence: "GGGAAACCCUUU", DBN: "(((...)))...", Pairs: [][2]int{{1, 9}, {2, 8}, {3, 7}}}}}
	resA.Meta.GeneID = stGeneX
	resB := resA
	p.viewA, p.record, p.cache["a"] = "a", "all", &resA
	p.syncArcs(&resA, nil) // A drawn, then B chosen: the arcs still hold A alone
	p.viewB = "b"
	if got := p.exportWait(&resA, nil); got != "B is still loading" {
		t.Fatalf("loading %q", got)
	}
	if spec, name := p.arcSpec(&resA); spec.LabelB != "" || strings.Contains(name, "vs") {
		t.Fatalf("A alone exported as %q %q", name, spec.LabelB)
	}
	p.readErr["b"] = "unreadable"
	if p.exportWait(&resA, nil) != "" {
		t.Fatal("an unreadable B blocks the exports")
	}
	delete(p.readErr, "b")
	// B on another reference: wait while it is aligned
	other := resB
	other.Meta.GeneID = "geneY"
	p.layers.mapPending["geneY>"+stGeneX] = true
	if got := p.exportWait(&resA, &other); got != "B is still being aligned to A's reference" {
		t.Fatalf("aligning %q", got)
	}
	p.layers.mapErr["geneY>"+stGeneX] = "no reference"
	if p.exportWait(&resA, &other) != "" {
		t.Fatal("an unaligned B blocks the exports")
	}
	// B read, on A's reference: A vs B
	p.cache["b"] = &resB
	if p.exportWait(&resA, &resB) != "" {
		t.Fatal("B ready but blocked")
	}
	p.syncArcs(&resA, &resB)
	if spec, name := p.arcSpec(&resA); !strings.HasPrefix(spec.LabelB, "B · ") || !strings.Contains(name, "vs") {
		t.Fatalf("A vs B exported as %q %q", name, spec.LabelB)
	}
}

// The drawing editor keeps a session per result, window and candidate:
// switching away and back restores the edits.
func TestStructureDrawingSessionsKept(t *testing.T) {
	a, _ := openCS(t)
	p := a.PageFor("structure").(*structurePage)
	res := structureResult{Records: []structureRecord{
		{WindowStart: 1, WindowEnd: 12, Sequence: "GGGAAACCCUUU", DBN: "(((...)))..."},
		{WindowStart: 21, WindowEnd: 32, Sequence: "GGGGAAACCCCU", DBN: "((((...))))."},
	}}
	res.Meta.GeneID = stGeneX
	p.viewA, p.record = "r", "1-12"
	tt := ui.NewTester(func(c *ui.Context) { p.drawingView(c, &res) }, 1000, 900)
	tt.Frame()
	first := p.drawing
	if first == nil || first.data.Sequence != "GGGAAACCCUUU" {
		t.Fatal("no session")
	}
	first.data.Title = "edited"
	p.record = "21-32"
	tt.Frame()
	if p.drawing == first || p.drawing.data.Sequence != "GGGGAAACCCCU" {
		t.Fatal("same session for another window")
	}
	p.record = "1-12"
	tt.Frame()
	if p.drawing != first || p.drawing.data.Title != "edited" {
		t.Fatal("edits lost switching windows")
	}
	// another result with the same window gets its own session
	p.viewA = "s"
	tt.Frame()
	if p.drawing == first {
		t.Fatal("session shared across results")
	}
	p.viewA = "r"
	tt.Frame()
	if p.drawing != first || len(p.drawings) != 3 {
		t.Fatalf("sessions %d", len(p.drawings))
	}
}

// "Download local inputs" never writes over a bonus matrix already beside
// the FASTA: it takes the FASTA's name and the toast and command say so.
func TestStructureLocalInputsNoClobber(t *testing.T) {
	dir := t.TempDir()
	matrix := filepath.Join(t.TempDir(), "bonus_matrix.tsv")
	if err := os.WriteFile(matrix, []byte("1\t20\t0.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, stLocalMatrix)
	if err := os.WriteFile(existing, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if names := stMatrixNames("my run.fasta"); names[0] != stLocalMatrix || names[1] != "my run-bonus-matrix.tsv" || names[2] != "my run-bonus-matrix-2.tsv" {
		t.Fatalf("names %q", names[:3])
	}
	name, err := stCopyNew(matrix, dir, stMatrixNames("my.fasta"))
	if err != nil || name != "my-bonus-matrix.tsv" {
		t.Fatalf("copy %q %v", name, err)
	}
	if got, _ := os.ReadFile(existing); string(got) != "mine\n" {
		t.Fatalf("overwrote %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != "1\t20\t0.5\n" {
		t.Fatalf("copied %q", got)
	}
	if name, _ = stCopyNew(matrix, dir, stMatrixNames("my.fasta")); name != "my-bonus-matrix-2.tsv" {
		t.Fatalf("second copy %q", name)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Fatalf("left behind %v", entries)
	}
	// through the page: the toast and the command name the files written
	a, _ := openCS(t)
	p := a.PageFor("structure").(*structurePage)
	asked := pickSaveInto(t, dir)
	before := lastToast(a)
	run := stLocalRun{guided: true, beam: 100, phase1: 1, delta: 5, alpha: 0.5, model: "DP09", pseudoknot: true}
	p.saveLocalInputs(">x\nGGGAAACCC\n", matrix, run, false)
	waitToast(a, before)
	if got := readExport(t, asked); got != ">x\nGGGAAACCC\n" {
		t.Fatalf("FASTA %q", got)
	}
	toast := lastToast(a)
	if !strings.Contains(toast, "saved in "+dir+": cplfold-input.fasta and cplfold-input-bonus-matrix.tsv. Run: ") ||
		!strings.Contains(toast, "--bonus-matrix-file cplfold-input-bonus-matrix.tsv ") {
		t.Fatalf("toast %q", toast)
	}
	if got, _ := os.ReadFile(existing); string(got) != "mine\n" {
		t.Fatalf("overwrote %q", got)
	}
}

// The bonus heatmap is read off the UI thread, once.
func TestStructureHeatLoadsAsync(t *testing.T) {
	a, _ := openCS(t)
	p := a.PageFor("structure").(*structurePage)
	path := filepath.Join(t.TempDir(), "bonus.tsv")
	if err := os.WriteFile(path, []byte("# m\n1\t20\t0.5\n3\t9\t1.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p.loadHeat(path, 20) != nil {
		t.Fatal("read on the UI thread")
	}
	waitFlush(a)
	heat := p.loadHeat(path, 20)
	if heat == nil || heat.err != "" || heat.max != 1.5 {
		t.Fatalf("heat %+v", heat)
	}
	_ = os.Remove(path)
	if p.loadHeat(path, 20) != heat {
		t.Fatal("not cached")
	}
}
