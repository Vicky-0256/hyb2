package views

// ⑤ Structure: evidence-guided CPLfold runs (task kind "structure"): the
// whole RNA (pseudoknot-free) or windows (pseudoknots allowed), with every
// chimera of the chosen selection as evidence. Lists saved results
// (list_structures), shows them as arc diagrams (read_structure) and
// compares two structures (A above, B below).
//
// Navigate options consumed by Show:
//
//	{"structureId": "<id>"}                 show a result as A (tasks drawer)
//	{"prefill": {"gene": "<gene>", "windows": [{"start": s, "end": e}],
//	             "selectionKey": "c:AFR", "source": "the contact map"}}
//	                                        fill the form in windows mode with
//	                                        pseudoknots on (Explore's "Fold
//	                                        this region")
//	{"pair": [taskIdA, taskIdB], "pairLabels": [labelA, labelB]}
//	                                        Compare's "Fold A & B": show A, then
//	                                        A and B, as the two tasks finish

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
	"hyb2desktop/widgets"
)

func init() {
	RegisterPageFactory("structure", func(a *App) Page { return newStructurePage(a) })
	OnTaskFinished(func(a *App, t model.Task) {
		if t.Kind != "structure" {
			return
		}
		if p, ok := a.pages["structure"].(*structurePage); ok && a.Project != nil {
			p.loadList()
		}
	})
	OnTaskFinished(func(a *App, t model.Task) {
		if t.Kind != "compare" {
			return
		}
		if p, ok := a.pages["structure"].(*structurePage); ok {
			p.layers.cmpStale = true // reloaded when the page shows
		}
	})
}

// ---- data

type structureEntry struct {
	ID        string        `json:"id"`
	TaskID    string        `json:"task_id"`
	SampleIDs []string      `json:"sample_ids"`
	Samples   []string      `json:"samples"`
	GeneID    string        `json:"gene_id"`
	Mode      string        `json:"mode"`
	Windows   []core.Window `json:"windows"`
	// Regions are a CPLcofold fold's two strands (mode "cofold").
	Regions    []core.Region  `json:"regions"`
	Parameters map[string]any `json:"parameters"`
	CreatedAt  string         `json:"created_at"`
	Available  *bool          `json:"available"`
	// Summary is the task's {pairs, pseudoknot_pairs, energy, length, ...}.
	Summary map[string]any `json:"summary"`
}

type structureRecord struct {
	WindowStart int      `json:"window_start"`
	WindowEnd   int      `json:"window_end"`
	Sequence    string   `json:"sequence"`
	DBN         string   `json:"dbn"`
	Pairs       [][2]int `json:"pairs"`
	// Bonus is each pair's chimera evidence (HYB bonus), aligned with Pairs.
	Bonus  []*float64 `json:"bonus"`
	Energy *float64   `json:"energy"`
	// A CPLcofold record (two strands): Positions is where each nucleotide
	// is drawn (its RNA position, or its A+B index for two RNAs), Cutpoint
	// the length of strand A and Segments the two regions.
	Positions []int         `json:"positions"`
	Cutpoint  int           `json:"cutpoint"`
	Segments  []core.Region `json:"segments"`
	// A CPLfold record: its candidates (best first; structure_details.go),
	// the one shown (selectCandidate swaps DBN, Pairs, Bonus and Energy),
	// each nucleotide's strongest HYB bonus and the window's bonus matrix
	// TSV (empty when it was not written).
	Candidates        []stCandidate `json:"candidates"`
	SelectedCandidate int           `json:"selected_candidate"`
	NucleotideSupport []float64     `json:"nucleotide_support"`
	BonusMatrixPath   string        `json:"bonus_matrix_path"`
	// Arms are the window's HYB evidence drawn under the arcs (arms.json):
	// its rows, unique arm blocks and the most frequent blocks as
	// [a0, a1, b0, b1, rows] in window coordinates.
	Arms *stArms `json:"arms"`
	// mapped, when set, are the pairs moved onto another reference
	// (stMapRecords); they replace DBN and Pairs.
	mapped []stPair
}

// stWeights maps the records' pairs to their chimera bonus.
// Keys are stPairKey's (lower position first).
func stWeights(records []structureRecord) map[[2]int]float64 {
	out := map[[2]int]float64{}
	for _, r := range records {
		for i, pr := range r.Pairs {
			if i < len(r.Bonus) && r.Bonus[i] != nil {
				out[stPairKey(pr[0], pr[1])] = *r.Bonus[i]
			}
		}
	}
	return out
}

// stPairKey is a pair's key, lower position first: a CPLcofold record whose
// region 2 lies before region 1 on the RNA (the runtime keeps the regions'
// order) draws strand A after strand B, so its pairs come I > J.
func stPairKey(i, j int) [2]int {
	if i > j {
		i, j = j, i
	}
	return [2]int{i, j}
}

// stNormalise orders each record's pairs lower position first (stPairKey),
// as the arcs, the statistics and the exports expect.
func stNormalise(result *structureResult) {
	for k := range result.Records {
		for i, pr := range result.Records[k].Pairs {
			result.Records[k].Pairs[i] = stPairKey(pr[0], pr[1])
		}
	}
}

type structureResult struct {
	Structure structureEntry `json:"structure"`
	Meta      struct {
		Engine     string         `json:"engine"`
		GeneID     string         `json:"gene_id"`
		Mode       string         `json:"mode"`
		Parameters map[string]any `json:"parameters"`
		Timings    struct {
			TotalS float64 `json:"total_s"`
		} `json:"timings"`
		EvidenceInputs struct {
			Rows string `json:"rows"`
			// Hyb are the .hyb files the fold read (the report's inputs).
			Hyb []struct {
				Path      string `json:"path"`
				SizeBytes int64  `json:"size_bytes"`
			} `json:"hyb"`
		} `json:"evidence_inputs"`
		// Reference is the FASTA the sequence came from.
		Reference struct {
			Path string `json:"path"`
		} `json:"reference"`
		// EnergyMetadata is a CPLcofold fold's energy model and kind (the
		// web's energyMetadata; energy_kind "heuristic" is an experimental
		// pseudoknot energy).
		EnergyMetadata map[string]any `json:"energy_metadata"`
		// Records are the windows' summaries; evidence.source is
		// "hyb-block-intervals" when CPLfold folded with HYB evidence;
		// comrades is a COMRADES(ViennaRNA) window's evidence and ensemble.
		Records []struct {
			Evidence struct {
				Source            string `json:"source"`
				InputRecords      int    `json:"inputRecords"`
				UniqueBlocks      int    `json:"uniqueBlocks"`
				NonzeroBonusCells int    `json:"nonzeroBonusCells"`
				// NonzeroUpperTriangleCells is the web's count (i < j);
				// nil in results folded before it was kept.
				NonzeroUpperTriangleCells *int    `json:"nonzeroUpperTriangleCells"`
				MaximumBonus              float64 `json:"maximumBonus"`
			} `json:"evidence"`
			// Candidates is how many CPLfold candidates the window kept.
			Candidates int `json:"candidates"`
			Seconds    struct {
				Bonus float64 `json:"bonus"`
				Fold  float64 `json:"fold"`
			} `json:"seconds"`
			Comrades *stComrades `json:"comrades"`
			// Iris is an IRIS window's weighted ensemble (structure_iris.go).
			Iris *stIris `json:"iris"`
		} `json:"records"`
	} `json:"meta"`
	Records []structureRecord `json:"records"`
}

// positionOf is where the record's k-th nucleotide (1-based) is drawn.
func (r structureRecord) positionOf(k int) int {
	if len(r.Positions) > 0 {
		if k >= 1 && k <= len(r.Positions) {
			return r.Positions[k-1]
		}
		return 0
	}
	start, _ := r.span()
	return start + k - 1
}

// rnaPosition is the position of the record's k-th nucleotide on its own
// RNA: for two strands, from the regions (a two-RNA or overlapping result
// is drawn on A+B indices instead), else where it is drawn.
func (r structureRecord) rnaPosition(k int) int {
	if r.Cutpoint > 0 && len(r.Segments) == 2 {
		if k <= r.Cutpoint {
			return int(r.Segments[0].Start) + k - 1
		}
		return int(r.Segments[1].Start) + k - r.Cutpoint - 1
	}
	return r.positionOf(k)
}

// localOf is the 1-based index of the nucleotide drawn at pos, 0 if none.
func (r structureRecord) localOf(pos int) int {
	if len(r.Positions) > 0 {
		for k, p := range r.Positions {
			if p == pos {
				return k + 1
			}
		}
		return 0
	}
	start, _ := r.span()
	if k := pos - start + 1; k >= 1 && k <= len(r.Sequence) {
		return k
	}
	return 0
}

// ranges are the stretches of positions the record covers: its window, or
// a CPLcofold record's two regions on one RNA.
func (r structureRecord) ranges() [][2]int {
	if len(r.Positions) > 0 && r.Cutpoint > 0 && r.Cutpoint < len(r.Positions) {
		return [][2]int{{r.Positions[0], r.Positions[r.Cutpoint-1]}, {r.Positions[r.Cutpoint], r.Positions[len(r.Positions)-1]}}
	}
	start, end := r.span()
	return [][2]int{{start, end}}
}

// span is the first and last position a record covers.
func (r structureRecord) span() (int, int) {
	if len(r.Positions) > 0 {
		return slices.Min(r.Positions), slices.Max(r.Positions)
	}
	start := r.WindowStart
	if start < 1 {
		start = 1
	}
	end := r.WindowEnd
	if end < start {
		end = start + len(r.Sequence) - 1
	}
	return start, end
}

// stPair is a base pair in gene coordinates with its bracket layer (0 for
// "()", higher for pseudoknot brackets).
type stPair struct{ I, J, Layer int }

const (
	dbnOpen  = "([{<ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	dbnClose = ")]}>abcdefghijklmnopqrstuvwxyz"
)

// stParseDBN reads dot-bracket (with pseudoknot brackets) into pairs at gene
// positions starting at offset.
func stParseDBN(dbn string, offset int) []stPair {
	if offset < 1 {
		offset = 1
	}
	stacks := map[int][]int{}
	var pairs []stPair
	for i, ch := range []byte(dbn) {
		if open := strings.IndexByte(dbnOpen, ch); open >= 0 {
			stacks[open] = append(stacks[open], i+1)
			continue
		}
		if close := strings.IndexByte(dbnClose, ch); close >= 0 && len(stacks[close]) > 0 {
			stack := stacks[close]
			i0 := stack[len(stack)-1]
			stacks[close] = stack[:len(stack)-1]
			pairs = append(pairs, stPair{I: i0 + offset - 1, J: i + 1 + offset - 1, Layer: close})
		}
	}
	return pairs
}

func stRecordPairs(r structureRecord) []stPair {
	if r.mapped != nil {
		return r.mapped
	}
	if r.DBN != "" && len(r.Positions) == len(r.DBN) {
		pairs := stParseDBN(r.DBN, 1)
		for i := range pairs {
			// lower position first: strand B may be drawn before strand A
			key := stPairKey(r.Positions[pairs[i].I-1], r.Positions[pairs[i].J-1])
			pairs[i].I, pairs[i].J = key[0], key[1]
		}
		return pairs
	}
	if r.DBN != "" {
		return stParseDBN(r.DBN, r.WindowStart)
	}
	out := make([]stPair, len(r.Pairs))
	for i, pr := range r.Pairs {
		key := stPairKey(pr[0], pr[1])
		out[i] = stPair{I: key[0], J: key[1]}
	}
	return out
}

// localPair is pair pr's 1-based indices in the record (A+B for two
// strands), lower first, and whether that swapped its ends (strand B drawn
// before strand A); 0 for an end the record does not hold.
func (r structureRecord) localPair(pr stPair) (i, j int, swapped bool) {
	i, j = r.localOf(pr.I), r.localOf(pr.J)
	if i > j && j >= 1 {
		return j, i, true
	}
	return i, j, false
}

// stLocalAxis: a two-strand record drawn on its A+B indices rather than RNA
// positions (two RNAs, or overlapping regions of one; cofold_positions), so
// gene coordinates (the annotation, differential arcs, another result's
// positions) do not apply to it.
func stLocalAxis(r structureRecord) bool {
	return r.Cutpoint > 0 && r.mapped == nil && stRegionsLocal(r.Segments)
}

// stRegionsLocal: two regions a two-strand fold draws on A+B indices.
func stRegionsLocal(regions []core.Region) bool {
	if len(regions) != 2 {
		return false
	}
	one, two := regions[0], regions[1]
	return one.GeneID != two.GeneID || (one.Start <= two.End && two.Start <= one.End)
}

// stChainGap is the arcs' chain break of a two-strand record on RNA
// positions: From is strand A's position next to the break and To strand
// B's (From > To when region 2 lies before region 1; widgets.Gap).
func stChainGap(r structureRecord) widgets.Gap {
	c, n := r.Cutpoint, len(r.Positions)
	if c <= 0 || c >= n || r.mapped != nil {
		return widgets.Gap{}
	}
	if aStart, bEnd := r.Positions[0], r.Positions[n-1]; bEnd < aStart {
		return widgets.Gap{From: aStart, To: bEnd} // strand B first on the axis
	}
	return widgets.Gap{From: r.Positions[c-1], To: r.Positions[c]}
}

// stPickRecords: all records, or the one window "start-end".
func stPickRecords(records []structureRecord, choice string) []structureRecord {
	if choice == "" || choice == "all" || len(records) <= 1 {
		return records
	}
	var out []structureRecord
	for _, r := range records {
		if fmt.Sprintf("%d-%d", r.WindowStart, r.WindowEnd) == choice {
			out = append(out, r)
		}
	}
	return out
}

// stStats compares the structures shown: A alone, or A and B with
// pairs counted only inside the regions both cover (a window fold against a
// whole-RNA fold).
type stStats struct {
	LengthA, LengthB     int
	PairsA, PairsB       int
	KnottedA, KnottedB   int
	EnergyA, EnergyB     string
	Shared, OnlyA, OnlyB int
	Jaccard              string
	DifferentCover       bool
}

func stCompare(recordsA, recordsB []structureRecord, compare bool) ([]stPair, []stPair, stStats) {
	var pairsA, pairsB []stPair
	for _, r := range recordsA {
		pairsA = append(pairsA, stRecordPairs(r)...)
	}
	for _, r := range recordsB {
		pairsB = append(pairsB, stRecordPairs(r)...)
	}
	statsA, statsB := pairsA, pairsB
	var s stStats
	if compare {
		// each record's ranges: a window, or a CPLcofold record's two regions,
		// whose pairs may join them
		spans := func(records []structureRecord) [][][2]int {
			out := make([][][2]int, len(records))
			for i, r := range records {
				out[i] = r.ranges()
			}
			return out
		}
		in := func(pos int, ranges [][2]int) bool {
			for _, sp := range ranges {
				if pos >= sp[0] && pos <= sp[1] {
					return true
				}
			}
			return false
		}
		inside := func(pairs []stPair, spans [][][2]int) []stPair {
			var out []stPair
			for _, pr := range pairs {
				for _, ranges := range spans {
					if in(pr.I, ranges) && in(pr.J, ranges) {
						out = append(out, pr)
						break
					}
				}
			}
			return out
		}
		spansA, spansB := spans(recordsA), spans(recordsB)
		statsA, statsB = inside(pairsA, spansB), inside(pairsB, spansA)
		cover := func(spans [][][2]int) int {
			n := 0
			for _, ranges := range spans {
				for _, sp := range ranges {
					n += sp[1] - sp[0] + 1
				}
			}
			return n
		}
		s.DifferentCover = cover(spansA) != cover(spansB)
	}
	key := func(p stPair) [2]int { return [2]int{p.I, p.J} }
	setA, setB := map[[2]int]bool{}, map[[2]int]bool{}
	for _, p := range statsA {
		setA[key(p)] = true
	}
	for _, p := range statsB {
		setB[key(p)] = true
	}
	for k := range setA {
		if setB[k] {
			s.Shared++
		}
	}
	length := func(records []structureRecord) int {
		n := 0
		for _, r := range records {
			n += len(r.Sequence)
		}
		return n
	}
	knotted := func(pairs []stPair) int {
		n := 0
		for _, p := range pairs {
			if p.Layer > 0 {
				n++
			}
		}
		return n
	}
	energy := func(records []structureRecord) string {
		sum, n := 0.0, 0
		for _, r := range records {
			if r.Energy != nil {
				sum += *r.Energy
				n++
			}
		}
		if n == 0 {
			return "—"
		}
		return fmt.Sprintf("%.1f kcal/mol", sum)
	}
	s.LengthA, s.LengthB = length(recordsA), length(recordsB)
	s.PairsA, s.PairsB = len(statsA), len(statsB)
	s.KnottedA, s.KnottedB = knotted(statsA), knotted(statsB)
	s.EnergyA, s.EnergyB = energy(recordsA), energy(recordsB)
	s.OnlyA, s.OnlyB = len(setA)-s.Shared, len(setB)-s.Shared
	s.Jaccard = "—"
	if union := len(setA) + len(setB) - s.Shared; union > 0 {
		s.Jaccard = fmt.Sprintf("%.3f", float64(s.Shared)/float64(union))
	}
	return pairsA, pairsB, s
}

var stWindowLine = regexp.MustCompile(`^(\d+)\s*[-–:.\s]+\s*(\d+)$`)

// stParseWindows reads "start-end" per line (or separated by , ;).
func stParseWindows(text string, length int) ([]core.Window, []string) {
	var windows []core.Window
	var errs []string
	for _, line := range regexp.MustCompile(`[\n,;]+`).Split(text, -1) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := stWindowLine.FindStringSubmatch(line)
		if m == nil {
			errs = append(errs, "Not a window: "+line)
			continue
		}
		start, _ := strconv.Atoi(m[1])
		end, _ := strconv.Atoi(m[2])
		if start > end {
			start, end = end, start
		}
		if start < 1 || (length > 0 && end > length) {
			limit := "?"
			if length > 0 {
				limit = strconv.Itoa(length)
			}
			errs = append(errs, line+" is outside 1-"+limit)
			continue
		}
		if end-start+1 < 20 {
			errs = append(errs, line+" is shorter than 20 nt")
			continue
		}
		windows = append(windows, core.Window{Start: uint64(start), End: uint64(end)})
	}
	return windows, errs
}

// ---- page

type structurePage struct {
	// shade colors arcs by chimera evidence (HYB bonus).
	shade bool
	// the result details' collapsible parts (structure_result.go) and the
	// bonus heatmaps read so far and being read, by matrix file and length
	// (loadHeat)
	pairListOpen, computedOpen bool
	heatCache                  map[string]*stHeat
	heatReading                map[string]bool
	a                          *App
	project                    string

	// form
	gene, evidence string
	// pinned are the samples a prefill named ("sampleIds", Explore after
	// Compare): the evidence instead of evidence's, until the evidence or
	// the top bar's Samples are chosen again (structure_evidence.go).
	pinned []string
	// topGene and topKey are the top bar's RNA and Samples last followed
	// (followTopBar).
	topGene, topKey string
	mode            string // "genome" | "windows"
	windows         string
	pseudoknot      bool
	// topology is the "Structure topology" radio's value and lastPK the
	// pseudoknot setting it last showed (structure_fold.go whatToFold)
	topology string
	lastPK   bool
	// seqOnly folds with CPLfold or CPLcofold from the sequence alone (the
	// web's "sequence only" evidence mode).
	seqOnly bool
	// src is the sequence source: a reference region, a HYB record or
	// pasted sequences (structure_fold.go).
	src stSource
	// CPLcofold's two regions: strand A is region1 on gene, strand B region2
	// on gene2 (the same RNA or another), each "start-end".
	gene2, region1, region2 string
	alpha, beam             float64
	// the other CPLfold settings, as on the web RNA Structure page
	phase1, delta, beta float64
	model               string
	// engine is engineCPLfold or engineVienna; the ViennaRNA settings
	// (structure_vienna.go).
	engine               string
	temperature, minLoop float64
	hardPairs            string
	stems, randomFolds   float64
	seed                 string
	homodimerOnly        bool
	// allowLargeEnsemble confirms more than 100 randomised folds, as the web.
	allowLargeEnsemble bool
	workers            float64
	// estKey is the form state est was computed for (time and memory of
	// the fold the form describes).
	estKey      string
	est         *core.FoldEstimate
	prefillNote string
	prefsAsked  bool
	// budgetMB is the memory tasks may use (Preferences), 0 until read.
	budgetMB     uint64
	advancedOpen bool
	errorOpen    bool
	// the fold started here: starting, why it did not start, its task.
	starting bool
	startErr string
	lastTask string

	// list
	structures []structureEntry
	listErr    string
	loading    bool
	again      bool // a task finished while the list was loading

	// viewer
	viewA, viewB string
	record       string
	// view is one of stViewLabels (structure_views.go); layouts are the
	// folded view's, by nested dot-bracket.
	view        string
	layouts     map[string]stLayout
	sv          stViewState
	cache       map[string]*structureResult
	reading     map[string]bool
	readErr     map[string]string
	pendingPair []string
	// pendingShow is a fold started here, shown once it is listed.
	pendingShow string
	pairLabels  []string
	arcs        widgets.Arcs
	arcsKey     string
	stats       stStats
	dbnText     string
	// shownB is the B whose pairs the arcs hold ("" for none: B unread, still aligning or not comparable)
	shownB string
	// research is the Related research panel below the viewer and
	// target the result it binds saved cards to (set by syncArcs).
	research *researchPanel
	target   *researchTarget
	// layers: B on A's coordinates, differential arcs, gene track
	layers        stLayers
	overlayLabels [2]string
	// result details (structure_details.go): the CPLfold candidate table,
	// the COMRADES tables' open state, and the records the arcs show (for
	// the nucleotide inspector).
	candTable               ui.ListState
	candRow                 int
	stemsOpen, evidenceOpen bool
	shownA                  []structureRecord

	// iris: the IRIS settings and region check (structure_iris.go)
	iris stIrisState

	// setup: the folding workflow and the sequence preview (structure_setup.go)
	setup stSetup

	// editing shows the drawing editor (drawing_page.go) on A's window;
	// drawing is the session shown (drawingKey names it) and drawings every
	// session opened, by stDrawingKey, so switching result, window or
	// candidate and back restores its edits.
	editing    bool
	drawing    *drawingEditor
	drawingKey string
	drawings   map[string]*drawingEditor
}

func newStructurePage(a *App) *structurePage {
	p := &structurePage{a: a, research: newResearchPanel(a)}
	p.reset()
	return p
}

func (p *structurePage) reset() {
	p.gene, p.evidence, p.mode, p.windows = "", "", "genome", ""
	p.topGene, p.topKey, p.pinned = "", "", nil
	p.pseudoknot, p.seqOnly = false, false
	p.src = stSource{}
	p.gene2, p.region1, p.region2 = "", "", ""
	p.resetFoldSettings()
	p.prefillNote, p.startErr, p.lastTask, p.starting = "", "", "", false
	p.structures, p.listErr, p.loading = nil, "", false
	p.viewA, p.viewB, p.record = "", "", "all"
	p.view, p.layouts, p.sv = stViewLabels[0], map[string]stLayout{}, stViewState{}
	p.editing, p.drawing, p.drawingKey, p.drawings = false, nil, "", nil
	p.cache = map[string]*structureResult{}
	p.reading = map[string]bool{}
	p.readErr = map[string]string{}
	p.pendingPair, p.pairLabels, p.pendingShow = nil, nil, ""
	p.arcsKey, p.dbnText, p.target, p.shownA, p.shownB = "", "", nil, nil, ""
	p.heatCache, p.heatReading = nil, nil
	p.layers = newSTLayers()
	p.setup = stSetup{}
}

func (p *structurePage) Show() {
	a := p.a
	if a.Project == nil {
		return
	}
	if a.Project.Path != p.project {
		p.project = a.Project.Path
		p.reset()
	}
	p.followTopBar(true)
	if opts := a.NavOptions; opts != nil {
		p.applyOptions(opts)
		a.NavOptions = nil
	}
	p.askWorkers()
	p.loadList()
	if p.layers.comparisons == nil || p.layers.cmpStale {
		p.layers.cmpStale = false
		p.loadComparisons()
	}
	p.research.Show()
}

// followTopBar takes the top bar's RNA and Samples when they change, as
// Explore follows them; a choice made on the form afterwards (or a
// prefill, applied after this) stays until the top bar changes again. An
// empty or unknown form value takes the top bar's too when the page is
// shown (fill).
func (p *structurePage) followTopBar(fill bool) {
	a := p.a
	if a.Project == nil {
		return
	}
	if gene := a.Selected.Gene; gene != p.topGene || (fill && (p.gene == "" || a.Project.GeneLength(p.gene) == 0)) {
		p.topGene, p.gene = gene, gene
	}
	if key := a.Selected.Key; key != p.topKey || (fill && len(p.evidenceIDs()) == 0) {
		p.topKey, p.evidence, p.pinned = key, key, nil
	}
}

func (p *structurePage) applyOptions(opts map[string]any) {
	if raw, ok := opts["prefill"]; ok {
		var prefill struct {
			Gene         string `json:"gene"`
			SelectionKey string `json:"selectionKey"`
			Source       string `json:"source"`
			Windows      []struct {
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"windows"`
			// Regions: fold these two regions as two strands with CPLcofold.
			Regions []core.Region `json:"regions"`
			// Record: fold this HYB record's own sequence.
			Record *hybridRow `json:"record"`
			// SampleIDs: exactly these samples are the evidence (Explore
			// with the samples Compare pinned).
			SampleIDs []string `json:"sampleIds"`
		}
		err := model.Decode(raw, &prefill)
		if err == nil {
			p.src.kind = stSourceReference        // a prefill names reference regions
			defer p.pinSamples(prefill.SampleIDs) // after the selection key
		}
		switch {
		case err != nil:
		case prefill.Record != nil:
			if p.engine == engineCPLcofold {
				p.engine = engineCPLfold // a record is one strand
			}
			if prefill.SelectionKey != "" {
				p.evidence = prefill.SelectionKey
			}
			p.setSource(stSourceRecord)
			p.src.recordsKey, p.src.record, p.src.pending = "", nil, prefill.Record // read the records again, then choose it
			p.prefillNote = "Prefilled from " + nameOr(prefill.Source, "a fold request") +
				" with the record's own sequence. Check the engine, then press Run fold below."
		case len(prefill.Regions) == 2:
			r1, r2 := prefill.Regions[0], prefill.Regions[1]
			p.engine, p.gene, p.gene2 = engineCPLcofold, r1.GeneID, r2.GeneID
			p.region1, p.region2 = fmt.Sprintf("%d-%d", r1.Start, r1.End), fmt.Sprintf("%d-%d", r2.Start, r2.End)
			if p.model != "DP03" && p.model != "DP09" {
				p.model = stDefaultModel
			}
			if prefill.SelectionKey != "" {
				p.evidence = prefill.SelectionKey
			}
			p.prefillNote = "Prefilled from " + nameOr(prefill.Source, "a fold request") +
				" with both arms as two strands. Check the regions, then press Run fold below."
		default:
			if p.engine == engineCPLcofold {
				p.engine = engineCPLfold // a window is one strand
			}
			if prefill.Gene != "" {
				p.gene = prefill.Gene
			}
			p.mode = "windows"
			lines := make([]string, len(prefill.Windows))
			for i, w := range prefill.Windows {
				lines[i] = fmt.Sprintf("%d-%d", int(w.Start), int(w.End))
			}
			p.windows = strings.Join(lines, "\n")
			if prefill.SelectionKey != "" {
				p.evidence = prefill.SelectionKey
			}
			source := prefill.Source
			if source == "" {
				source = "a fold request"
			}
			p.prefillNote = "Prefilled from " + source + ". Check the window, then press Run fold below."
		}
	}
	if id, _ := opts["structureId"].(string); id != "" {
		p.show(id)
	}
	if raw, ok := opts["pair"]; ok {
		var pair, labels []string
		if model.Decode(raw, &pair) == nil && len(pair) == 2 {
			p.pendingPair = pair
			_ = model.Decode(opts["pairLabels"], &labels)
			p.pairLabels = labels
			p.viewA, p.viewB, p.record = "", "", "all"
		}
	}
}

// askWorkers defaults "Parallel windows" to the CPU budget.
func (p *structurePage) askWorkers() {
	if p.prefsAsked || p.workers > 0 {
		return
	}
	p.prefsAsked = true
	a := p.a
	a.Go(func() func() {
		prefs, err := a.B.Preferences()
		cpu, mem := 4.0, 0.0
		if err == nil {
			if budget, _ := prefs["budget"].(map[string]any); budget != nil {
				if v, ok := budget["cpu"].(float64); ok && v >= 1 {
					cpu = v
				}
				mem, _ = budget["mem_mb"].(float64)
			}
		}
		return func() {
			if p.workers <= 0 {
				p.workers = cpu
			}
			p.budgetMB = uint64(mem)
		}
	})
}

func (p *structurePage) loadList() {
	a := p.a
	if a.Project == nil {
		return
	}
	if p.loading {
		p.again = true
		return
	}
	path := a.Project.Path
	p.loading = true
	a.Go(func() func() {
		listed, err := a.B.ProjectOp(path, "list_structures", nil)
		var entries []structureEntry
		if err == nil {
			err = model.Decode(listed["structures"], &entries)
		}
		return func() {
			if a.Project == nil || a.Project.Path != path {
				return
			}
			p.loading = false
			if p.again {
				p.again = false
				defer p.loadList()
			}
			if err != nil {
				p.listErr = err.Error()
				return
			}
			p.listErr = ""
			p.structures = entries
			p.resolvePair()
		}
	})
}

// resolvePair shows the A fold of a pending pair once it is listed, and B
// once both are.
func (p *structurePage) resolvePair() {
	if p.pendingShow != "" && p.entry(p.pendingShow) != nil {
		p.show(p.pendingShow)
		p.pendingShow = ""
	}
	if len(p.pendingPair) != 2 {
		return
	}
	hasA, hasB := p.entry(p.pendingPair[0]) != nil, p.entry(p.pendingPair[1]) != nil
	if hasA && p.viewA != p.pendingPair[0] {
		p.show(p.pendingPair[0])
	}
	if hasA && hasB {
		p.viewB = p.pendingPair[1]
		p.ensureRead(p.viewB)
		p.pendingPair = nil
	}
}

func (p *structurePage) entry(id string) *structureEntry {
	for i := range p.structures {
		if p.structures[i].ID == id {
			return &p.structures[i]
		}
	}
	return nil
}

func (p *structurePage) show(id string) {
	p.viewA = id
	if p.viewB == id {
		p.viewB = ""
	}
	p.record = "all"
	p.ensureRead(id)
}

func (p *structurePage) ensureRead(id string) {
	a := p.a
	if id == "" || p.cache[id] != nil || p.reading[id] || a.Project == nil {
		return
	}
	path := a.Project.Path
	p.reading[id] = true
	delete(p.readErr, id)
	a.Go(func() func() {
		raw, err := a.B.ProjectOp(path, "read_structure", core.Obj{"structure_id": id})
		var result structureResult
		if err == nil {
			err = model.Decode(raw, &result)
		}
		stNormalise(&result)
		return func() {
			if a.Project == nil || a.Project.Path != path {
				return
			}
			delete(p.reading, id)
			if err != nil {
				p.readErr[id] = err.Error()
				return
			}
			p.cache[id] = &result
		}
	})
}

func (p *structurePage) evidenceNames(e *structureEntry) string {
	ids := e.SampleIDs
	if len(ids) == 0 {
		ids = e.Samples
	}
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = id
		if s := p.a.Project.Sample(id); s != nil {
			names[i] = s.Label()
		}
	}
	return strings.Join(names, " + ")
}

// ---- view

func (p *structurePage) View(c *ui.Context) {
	if p.a.Project != nil && p.a.Project.Path == p.project {
		p.followTopBar(false) // the top bar changed while this page is shown
	}
	ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
		PageHeader(c, "Structure", "Predict RNA secondary structure with CPLfold, CPLcofold or ViennaRNA, using your samples' chimeric reads as evidence, and compare structures.")
		// The form takes about a third of a wide window. A narrow window
		// puts it above the viewer in one scroll; a short one scrolls the
		// check and Run fold with the form, which pinned below it would
		// leave the form a few lines.
		w, h := p.a.ContentSize()
		side := clampF(w*0.3, 354, 460)
		pinned := h >= 700
		if w < 860 {
			ui.Scroll(c.Key("st-stack")).Grow(1).Gap(16).Children(func() {
				p.form(c)
				p.list(c)
				p.runPanel(c)
				p.viewer(c)
				p.research.View(c, p.researchTarget())
			})
			return
		}
		ui.Row(c).Grow(1).Gap(16).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(side).Gap(12).Children(func() {
				ui.Scroll(c.Key("st-side")).Grow(1).Padding(0, 14, 0, 0).Gap(16).Children(func() {
					p.form(c)
					p.list(c)
					if !pinned {
						p.runPanel(c)
					}
				})
				if pinned {
					p.runPanel(c)
				}
			})
			ui.Scroll(c.Key("st-main")).Grow(1).Gap(16).Children(func() {
				p.viewer(c)
				p.research.View(c, p.researchTarget())
			})
		})
	})
}

func (p *structurePage) viewer(c *ui.Context) {
	t := c.Theme()
	Card(c, "", func() {
		var a, b *structureResult
		if p.viewA != "" {
			a = p.cache[p.viewA]
		}
		if p.viewB != "" {
			b = p.cache[p.viewB]
		}
		var windowChoices []string
		if a != nil && len(a.Records) > 1 {
			windowChoices = append(windowChoices, "All windows")
			for _, r := range a.Records {
				windowChoices = append(windowChoices, fmt.Sprintf("Window %d-%d", r.WindowStart, r.WindowEnd))
			}
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, "Viewer").FontSize(16).Bold().Grow(1)
			if len(windowChoices) > 0 {
				current := "All windows"
				if p.record != "all" {
					current = "Window " + p.record
				}
				ui.Select(c.Key("st-record"), &current, windowChoices).Width(200).Label("Window").OnChange(func() {
					if current == "All windows" {
						p.record = "all"
					} else {
						p.record = strings.TrimPrefix(current, "Window ")
					}
				})
			}
			// A alone is drawn in the web's pair styles, whose opacity already
			// follows the HYB bonus (the web has no such switch): the switch
			// is for A and B, or A in one colour, in the arc view.
			_, otherView := stViewModes[p.view]
			if a != nil && (b != nil || (p.layers.mono && !otherView)) &&
				ui.Checkbox(c.Key("st-shade"), &p.shade, "Shade by chimera support").
					Tooltip("Arc opacity follows the pair's HYB bonus: faint arcs have little chimera evidence").Changed() {
				p.arcs.Shade = p.shade
			}
			if p.viewB != "" && ui.Button(c, "Clear B").Clicked() {
				p.viewB = ""
			}
			if a != nil {
				if ui.Button(c, "Save .dbn").Clicked() {
					p.saveDBN(a)
				}
				// while B is read or aligned the arcs hold A alone: wait for it
				wait := p.exportWait(a, b)
				for _, asPNG := range []bool{false, true} {
					label := map[bool]string{false: "Export SVG…", true: "Export PNG…"}[asPNG]
					tip := "Save the view as shown, in light colours"
					if wait != "" {
						tip = wait + "; the view saves once B is shown"
					}
					if ui.Button(c.Key(label), label).Disabled(wait != "").Tooltip(tip).Clicked() && wait == "" {
						_, other := stViewModes[p.view]
						switch {
						case other && b == nil:
							p.exportViewSVG(a, asPNG)
						case asPNG:
							p.exportArcsPNG(a)
						default:
							p.exportArcsSVG(a)
						}
					}
				}
				if ui.Button(c, "Save .ct").Tooltip("Connectivity table, one block per window shown").Clicked() {
					p.exportCT(a)
				}
				if ui.Button(c, "Save .bpseq").Tooltip("BPSEQ: position, base and partner, one block per window shown").Clicked() {
					p.exportBPSEQ(a)
				}
				if ui.Button(c, "VARNA command…").Tooltip("A shell script that draws each window shown with VARNA").Clicked() {
					p.exportVARNA(a)
				}
				if ui.Button(c, "Pairs TSV…").Tooltip("Every base pair shown, with its type, layer and chimera evidence").Clicked() {
					p.exportPairsTSV(a)
				}
				if ui.Button(c, "Report JSON…").Tooltip("What was folded, with which parameters, and each window's structure").Clicked() {
					p.exportReport(a)
				}
			}
		})
		if len(p.pendingPair) == 2 {
			labels := "A and B"
			if len(p.pairLabels) == 2 {
				labels = p.pairLabels[0] + " (A) and " + p.pairLabels[1] + " (B)"
			}
			ui.Text(c, "Folding "+labels+"; they show here as the folds finish.").FontSize(12).TextColor(t.TextMuted)
		}
		if p.viewA == "" {
			msg := "Run a fold to see its arc diagram here."
			if len(p.structures) > 0 {
				msg = "Choose a result to display; add a second one with “Compare as B”."
			}
			ui.Text(c, msg).TextColor(t.TextMuted)
			return
		}
		for _, id := range []string{p.viewA, p.viewB} {
			if err := p.readErr[id]; id != "" && err != "" {
				ui.Text(c, "Could not read the structure: "+err).TextColor(t.Danger)
				return
			}
		}
		if a == nil || (p.viewB != "" && b == nil) {
			ui.Text(c, "Loading…").TextColor(t.TextMuted)
			return
		}
		if b == nil {
			p.defaultView(a)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "View").FontSize(12).Bold().TextColor(t.TextMuted)
				ui.Select(c.Key("st-view"), &p.view, stViewChoices(a)).Width(140).Label("View").OnChange(func() { p.editing = false })
				p.drawingButton(c, a)
			})
			if p.editing && !stTwoStrand(a) {
				p.drawingView(c, a)
				ui.Text(c, p.metaLine(a)).FontSize(12).TextColor(t.TextMuted)
				p.candidatesPanel(c, a)
				p.comradesPanel(c, a)
				return
			}
			if p.otherView(c, a) {
				ui.Text(c, p.metaLine(a)).FontSize(12).TextColor(t.TextMuted)
				p.evidenceProvenance(c, a)
				p.candidatesPanel(c, a)
				p.resultDetails(c, a)
				p.comradesPanel(c, a)
				return
			}
		}
		p.layerControls(c, stGene(a))
		p.syncArcs(a, b)
		p.arcs.Mono = p.monoColor(t)
		// A alone: the web's pair styles and the raw HYB arms under the arcs
		var rawLabel, rawNote string
		p.arcs.StylesA = nil
		if b == nil && p.arcs.Mono.A == 0 {
			p.arcs.StylesA = stArcStyles(a, p.shownA, stShownPairs(p.shownA, stHardPairs(a)), t)
			if len(p.overlayLabels) == 0 || p.overlayLabels[0] == "" {
				var raw []widgets.OverlayArc
				raw, rawLabel, rawNote = stRawArms(p.shownA, t)
				p.arcs.Overlay = raw
			}
		}
		metaA := p.entry(p.viewA)
		labelA := "A · " + p.viewA
		if metaA != nil {
			labelA = "A · " + p.evidenceNames(metaA)
		}
		labelB := ""
		if b != nil {
			labelB = "B · " + p.viewB
			if metaB := p.entry(p.viewB); metaB != nil {
				labelB = "B · " + p.evidenceNames(metaB)
			}
		}
		ui.Row(c).Gap(14).Wrap().Children(func() {
			if b == nil && p.arcs.Mono.A == 0 {
				ui.Text(c, "Predicted structure · above").FontSize(12).Bold().TextColor(t.TextMuted)
				stArcLegend(c, a)
				if len(p.arcs.Overlay) > 0 && rawLabel != "" {
					stLegend(c, t.Accent, rawLabel)
				} else if rawLabel != "" {
					ui.Text(c, rawLabel).FontSize(12).TextColor(t.TextMuted)
				}
				return
			}
			colorA, colorB := cmpRed, cmpBlue
			if p.layers.mono {
				colorA, colorB = t.Text, t.Text
			}
			if b != nil && p.layers.mono {
				labelA, labelB = labelA+" (above)", labelB+" (below)"
			}
			stLegend(c, colorA, labelA)
			if b != nil {
				stLegend(c, colorB, labelB)
				if !p.layers.mono {
					stLegend(c, t.TextMuted, "in both")
				}
			}
			if len(p.arcs.Overlay) > 0 {
				stLegend(c, cmpRed, p.overlayLabels[0])
				stLegend(c, cmpBlue, p.overlayLabels[1])
			}
		})
		if rawNote != "" {
			ui.Text(c, rawNote).FontSize(11).TextColor(t.TextMuted)
		}
		if b == nil {
			ui.Text(c, stViewCopy(a, "arc")).FontSize(12).TextColor(t.TextMuted)
		}
		height := float32(340)
		if b != nil || len(p.arcs.Overlay) > 0 {
			height = 440
		}
		if len(p.arcs.Features) > 0 {
			height += 20
		}
		ev := p.arcs.Show(c.Key("st-arcs"), height, cmpRed, cmpBlue, t.TextMuted, "Arc diagram of base pairs")
		info := fmt.Sprintf("%s–%s · wheel zooms, drag pans, double click resets, click inspects a nucleotide (← → step)",
			FormatNumber(int64(p.arcs.Start)), FormatNumber(int64(p.arcs.End)))
		if ev.Hover {
			hover := stNucleotideText(p.shownA, ev.Position, nameOr(a.Meta.Engine, "CPLfold"))
			info = nameOr(hover, "Position "+FormatNumber(int64(ev.Position))) + " · " + info
		}
		ui.Text(c, info).FontSize(11).TextColor(t.TextMuted)
		if sel := p.arcs.Selected; sel > 0 && b != nil {
			if text := stNucleotideText(p.shownA, sel, nameOr(a.Meta.Engine, "CPLfold")); text != "" {
				ui.Text(c, "A · "+text).FontSize(12).Bold()
			}
		}
		for _, note := range p.layers.notes {
			ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
		}
		if p.stats.DifferentCover {
			ui.Text(c, "Pair statistics count pairs inside the regions both structures cover.").FontSize(12).TextColor(t.TextMuted)
		}
		if b == nil {
			ui.Text(c, p.metaLine(a)).FontSize(12).TextColor(t.TextMuted)
			p.evidenceProvenance(c, a)
			p.candidatesPanel(c, a)
			p.resultDetails(c, a)
			p.comradesPanel(c, a)
			return
		}
		p.statsTable(c, b != nil)
		ui.Text(c, p.metaLine(a)).FontSize(12).TextColor(t.TextMuted)
		p.candidatesPanel(c, a)
		p.comradesPanel(c, a)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Dot-bracket (A)").Bold().Grow(1)
			if ui.Button(c, "Copy").Clicked() {
				c.WriteClipboard(p.dbnText)
				p.a.Toast("Dot-bracket copied")
			}
		})
		ui.TextArea(c.Key("st-dbn"), &p.dbnText).ReadOnly(true).Font("monospace").FontSize(11).Height(130).Label("Dot-bracket")
	})
}

func stLegend(c *ui.Context, color ui.Color, label string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(14, 3).Background(color)
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

// stGene is the gene a result was folded on.
func stGene(r *structureResult) string {
	if r == nil {
		return ""
	}
	return nameOr(r.Meta.GeneID, r.Structure.GeneID)
}

// syncArcs rebuilds pairs, statistics and text when what is shown changes.
func (p *structurePage) syncArcs(a, b *structureResult) {
	key := fmt.Sprintf("%s|%s|%s|%d", p.viewA, p.viewB, p.record, p.layers.version)
	if key == p.arcsKey {
		return
	}
	p.arcsKey = key
	p.layers.notes = nil
	geneA := stGene(a)
	recordsA := stPickRecords(a.Records, p.record)
	p.shownA = recordsA
	// a two-RNA or overlapping two-strand A is drawn on its A+B indices:
	// nothing in RNA positions lines up with it
	local := slices.ContainsFunc(recordsA, stLocalAxis)
	var recordsB []structureRecord
	if b != nil {
		recordsB = stPickRecords(b.Records, p.record)
		if p.record != "all" && len(recordsB) == 0 {
			recordsB = b.Records
		}
		if why := stCompareBlock(a, b); why != "" {
			p.layers.notes = append(p.layers.notes, "B is not shown: "+why)
			recordsB = nil
		} else if geneB := stGene(b); geneB != "" && geneA != "" && geneB != geneA {
			// B folded on another reference: move it onto A's coordinates.
			m, ok := p.geneMap(geneB, geneA)
			switch {
			case ok && m != nil:
				var dropped int
				recordsB, dropped = stMapRecords(recordsB, m)
				note := "B is shown on A's coordinates: " + stMapNote(geneB, geneA, m) + "."
				if dropped > 0 {
					note += fmt.Sprintf(" %s pairs of B with an unaligned end are left out.", FormatNumber(int64(dropped)))
				}
				p.layers.notes = append(p.layers.notes, note)
			case p.layers.mapErr[geneB+">"+geneA] != "":
				p.layers.notes = append(p.layers.notes, "B is not shown: could not align "+geneB+" to "+geneA+": "+p.layers.mapErr[geneB+">"+geneA])
				recordsB = nil
			default:
				p.layers.notes = append(p.layers.notes, "Aligning "+geneB+" to "+geneA+" to show B on A's coordinates…")
				recordsB = nil
			}
		}
	}
	pairsA, pairsB, stats := stCompare(recordsA, recordsB, b != nil)
	p.stats = stats
	start, end := 0, 0
	for _, r := range append(append([]structureRecord{}, recordsA...), recordsB...) {
		s, e := r.span()
		if start == 0 || s < start {
			start = s
		}
		if e > end {
			end = e
		}
	}
	if start == 0 {
		start, end = 1, 100
	}
	gene := a.Meta.GeneID
	if gene == "" {
		gene = a.Structure.GeneID
	}
	length := p.a.Project.GeneLength(gene)
	if length < end || local {
		length = end // A+B indices: the RNA's length does not apply
	}
	toArcs := func(pairs []stPair) []widgets.Pair {
		out := make([]widgets.Pair, len(pairs))
		for i, pr := range pairs {
			out[i] = widgets.Pair{I: pr.I, J: pr.J}
		}
		return out
	}
	p.arcs.Set(toArcs(pairsA), toArcs(pairsB), length)
	weights := func(pairs []stPair, records []structureRecord) []float64 {
		known := stWeights(records)
		out := make([]float64, len(pairs))
		for i, pr := range pairs {
			out[i] = known[[2]int{pr.I, pr.J}]
		}
		return out
	}
	p.arcs.WeightA, p.arcs.WeightB = weights(pairsA, recordsA), weights(pairsB, recordsB)
	p.arcs.Shade = p.shade
	p.arcs.Overlay, p.overlayLabels, p.arcs.Features = nil, [2]string{}, nil
	if local {
		p.layers.notes = append(p.layers.notes, stLocalAxisNote)
	} else {
		overlay, labels, note, _ := p.overlayArcs(geneA)
		p.arcs.Overlay, p.overlayLabels = overlay, labels
		if note != "" {
			p.layers.notes = append(p.layers.notes, note)
		}
	}
	if an := p.annotation(geneA); an != nil && !local {
		p.arcs.Features = an.features
		if an.err != "" {
			p.layers.notes = append(p.layers.notes, "Gene annotation: "+an.err)
		} else if an.note != "" {
			p.layers.notes = append(p.layers.notes, an.note)
		}
	}
	// CPLcofold, two regions of one RNA, A alone: the web's A+B arcs, the
	// positions between the regions taking no room
	// (strand B first when region 2 lies before region 1)
	p.arcs.Gap = widgets.Gap{}
	if b == nil && len(recordsA) == 1 {
		p.arcs.Gap = stChainGap(recordsA[0])
	}
	// the arcs (and so the exports) hold B only once it is drawn
	p.shownB = ""
	if b != nil && recordsB != nil {
		p.shownB = p.viewB
	}
	p.arcs.Start, p.arcs.End = float64(start), float64(end)
	if end-start < 20 {
		p.arcs.Start, p.arcs.End = 1, float64(length)
	}
	p.dbnText = stDBNText(gene, recordsA)
	p.target = &researchTarget{StructureID: p.viewA, Record: p.record, Digest: researchDigest(recordsA), Gene: gene}
}

// stLocalAxisNote says why a two-RNA or overlapping two-strand A has no
// gene track or differential arcs.
const stLocalAxisNote = "This two-strand result is drawn on its A+B indices (two RNAs, or overlapping regions of one), " +
	"so the gene annotation and differential arcs, which are in RNA positions, are not drawn."

// stCompareBlock is why B cannot be compared with A ("" when it can): a
// two-RNA or overlapping two-strand result is drawn on its A+B indices,
// which only line up with another such result of the same regions.
func stCompareBlock(a, b *structureResult) string {
	if a == nil || b == nil {
		return ""
	}
	return stAxisBlock(stResultRegions(a), stResultRegions(b))
}

// stResultRegions are a two-strand result's regions, nil for one strand.
func stResultRegions(r *structureResult) []core.Region {
	for _, rec := range r.Records {
		if rec.Cutpoint > 0 && len(rec.Segments) == 2 {
			return rec.Segments
		}
	}
	if len(r.Structure.Regions) == 2 {
		return r.Structure.Regions
	}
	return nil
}

// compareAsBBlock is why listed result e cannot be compared as B with the
// A shown ("" when it can).
func (p *structurePage) compareAsBBlock(e *structureEntry) string {
	var regionsA []core.Region
	if a := p.cache[p.viewA]; a != nil {
		regionsA = stResultRegions(a)
	} else if ea := p.entry(p.viewA); ea != nil && len(ea.Regions) == 2 {
		regionsA = ea.Regions
	}
	var regionsB []core.Region
	if len(e.Regions) == 2 {
		regionsB = e.Regions
	}
	return stAxisBlock(regionsA, regionsB)
}

// exportWait is why the arc exports wait ("" when they can run): B is
// still being read, or aligned onto A's reference, so the arcs hold A
// alone. B that cannot be shown (unreadable, unaligned, not comparable)
// does not hold them: they save A alone, named and labelled so (arcSpec).
func (p *structurePage) exportWait(a, b *structureResult) string {
	if p.viewB == "" || p.readErr[p.viewB] != "" || stCompareBlock(a, b) != "" {
		return ""
	}
	if b == nil {
		return "B is still loading"
	}
	if geneA, geneB := stGene(a), stGene(b); geneA != "" && geneB != "" && geneA != geneB {
		if _, ok := p.geneMap(geneB, geneA); !ok && p.layers.mapErr[geneB+">"+geneA] == "" {
			return "B is still being aligned to A's reference"
		}
	}
	return ""
}

// stAxisBlock compares the regions of A and B (nil for one strand).
func stAxisBlock(a, b []core.Region) string {
	localA, localB := stRegionsLocal(a), stRegionsLocal(b)
	if !localA && !localB {
		return ""
	}
	same := localA && localB
	for i := 0; same && i < 2; i++ {
		same = a[i].GeneID == b[i].GeneID && a[i].Start == b[i].Start && a[i].End == b[i].End
	}
	if same {
		return ""
	}
	return "a two-RNA or overlapping two-strand result is drawn on its A+B indices, not RNA positions, " +
		"so its pairs and pair statistics cannot be compared with another result's. Compare it with a fold of the same two regions."
}

// researchTarget is the result A shown, if its arcs are current.
func (p *structurePage) researchTarget() *researchTarget {
	if p.target == nil || p.viewA == "" || p.target.StructureID != p.viewA || p.target.Record != p.record || p.cache[p.viewA] == nil {
		return nil
	}
	return p.target
}

func stDBNText(gene string, records []structureRecord) string {
	if gene == "" {
		gene = "RNA"
	}
	var b strings.Builder
	for _, r := range records {
		// as the web's .dbn: the energy follows the structure, two decimals
		energy := ""
		if r.Energy != nil && !math.IsNaN(*r.Energy) && !math.IsInf(*r.Energy, 0) {
			energy = " (" + jsFixed(*r.Energy, 2) + ")"
		}
		if c := r.Cutpoint; c > 0 && c < len(r.Sequence) && len(r.DBN) == len(r.Sequence) {
			// CPLcofold: the two strands, '&' at the break, as the web's .dbn
			fmt.Fprintf(&b, ">%s\n%s&%s\n%s&%s%s\n", stRegionsLabel(r.Segments), r.Sequence[:c], r.Sequence[c:], r.DBN[:c], r.DBN[c:], energy)
			continue
		}
		fmt.Fprintf(&b, ">%s:%d-%d\n%s\n%s%s\n", gene, r.WindowStart, r.WindowEnd, r.Sequence, r.DBN, energy)
	}
	return b.String()
}

// stRegionsLabel: "ZIKV:1-45 + ZIKV:1086-1134".
func stRegionsLabel(regions []core.Region) string {
	parts := make([]string, len(regions))
	for i, r := range regions {
		parts[i] = fmt.Sprintf("%s:%d-%d", r.GeneID, r.Start, r.End)
	}
	return strings.Join(parts, " + ")
}

func (p *structurePage) statsTable(c *ui.Context, compare bool) {
	t := c.Theme()
	s := p.stats
	type row struct{ label, a, b string }
	rows := []row{
		{"Length (nt)", FormatNumber(int64(s.LengthA)), FormatNumber(int64(s.LengthB))},
		{"Base pairs", FormatNumber(int64(s.PairsA)), FormatNumber(int64(s.PairsB))},
		{"Pseudoknotted pairs", FormatNumber(int64(s.KnottedA)), FormatNumber(int64(s.KnottedB))},
		{"Energy", s.EnergyA, s.EnergyB},
	}
	if compare {
		rows = append(rows,
			row{"Shared pairs", FormatNumber(int64(s.Shared)), FormatNumber(int64(s.Shared))},
			row{"Only in this structure", FormatNumber(int64(s.OnlyA)), FormatNumber(int64(s.OnlyB))},
			row{"Pair agreement (Jaccard)", s.Jaccard, ""})
	}
	ui.Column(c).Gap(0).Radius(8).Border(1, t.Border).Children(func() {
		line := func(label, a, b string, header bool) {
			r := ui.Row(c).Padding(5, 10).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
			r.Children(func() {
				ui.Text(c, label).Grow(1)
				ta := ui.Text(c, a).Width(130).TextAlign(ui.End)
				if header {
					ta.Bold().TextColor(cmpRed)
				}
				if compare {
					tb := ui.Text(c, b).Width(130).TextAlign(ui.End)
					if header {
						tb.Bold().TextColor(cmpBlue)
					}
				}
			})
		}
		line("", "A", "B", true)
		for _, r := range rows {
			line(r.label, r.a, r.b, false)
		}
	})
}

func (p *structurePage) metaLine(a *structureResult) string {
	m := a.Meta
	parts := []string{"Engine " + nameOr(m.Engine, "CPLfold")}
	if m.Engine == "COMRADES(ViennaRNA)" && m.Parameters != nil {
		parts = append(parts, fmt.Sprintf("%v °C, minimum loop %v, %v randomised folds, seed %v, %d stem pairs fitted",
			m.Parameters["temperature"], m.Parameters["min_loop"], m.Parameters["random_folds"], m.Parameters["seed"], len(stHardPairs(a))))
	} else if m.Engine == "IRIS" && m.Parameters != nil {
		parts = append(parts, stIrisMetaLine(a))
	} else if m.Engine == "ViennaRNA" && m.Parameters != nil {
		text := fmt.Sprintf("MFE, %v °C, minimum loop %v", m.Parameters["temperature"], m.Parameters["min_loop"])
		if n := len(stHardPairs(a)); n > 0 {
			text += fmt.Sprintf(", %d hard pair%s", n, plural(n))
		}
		parts = append(parts, text)
	} else if m.Parameters != nil {
		pk := "pseudoknot-free"
		if on, _ := m.Parameters["allow_pseudoknot"].(bool); on {
			pk = "pseudoknots allowed"
		}
		if m.Parameters["evidence"] == "none" {
			parts = append(parts, pk+", sequence only")
		} else {
			parts = append(parts, fmt.Sprintf("%s, alpha %v", pk, m.Parameters["alpha"]))
		}
		if len(a.Records) == 1 && a.Records[0].Cutpoint > 0 {
			r := a.Records[0]
			between := 0
			for _, pr := range stParseDBN(r.DBN, 1) {
				if pr.I <= r.Cutpoint && pr.J > r.Cutpoint {
					between++
				}
			}
			parts = append(parts, fmt.Sprintf("two strands %s, %d pair%s between them", stRegionsLabel(r.Segments), between, plural(between)))
		}
	}
	if m.Timings.TotalS > 0 {
		parts = append(parts, fmt.Sprintf("took %.1f s", m.Timings.TotalS))
	}
	if m.EvidenceInputs.Rows != "" {
		parts = append(parts, "evidence: "+m.EvidenceInputs.Rows)
	}
	return strings.Join(parts, " · ")
}

func (p *structurePage) saveDBN(a *structureResult) {
	gene := nameOr(a.Meta.GeneID, a.Structure.GeneID)
	// the records shown (all windows, or the one chosen), as the other exports
	text := stDBNText(gene, stPickRecords(a.Records, p.record))
	name := stExportFiles["dbn"]
	app := p.a
	app.Go(func() func() {
		path := PickSave("Save the dot-bracket file", name)
		if path == "" {
			return nil
		}
		err := os.WriteFile(path, []byte(text), 0o644)
		return func() {
			if err != nil {
				app.Toast("Could not save: %v", err)
			} else {
				app.Toast("Saved %s", path)
			}
		}
	})
}

// StructureMemClaim is the memory a structure request claims from the task
// budget: the CPLfold estimate for its longest window (the whole RNA in
// genome mode) times the windows folded at once.
func StructureMemClaim(req core.TaskStartRequest, geneLength int, allowPseudoknot bool) uint64 {
	longest, parallel := geneLength, 1
	if len(req.Windows) > 0 {
		longest = 0
		for _, w := range req.Windows {
			longest = max(longest, int(w.End-w.Start+1))
		}
		parallel = len(req.Windows)
		if req.Workers != nil {
			parallel = min(parallel, int(*req.Workers))
		}
	}
	return core.StructureClaimMB(longest, allowPseudoknot, parallel)
}

// StructureStartError explains a fold the memory budget cannot hold.
func StructureStartError(err error, needMB uint64) string {
	if strings.Contains(err.Error(), "mem_mb") {
		return fmt.Sprintf("this fold needs about %.1f GB of memory, more than the task budget. "+
			"Raise the memory budget in Preferences, use fewer parallel windows, or fold shorter windows.", float64(needMB)/1024)
	}
	return err.Error()
}
