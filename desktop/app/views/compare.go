package views

// ④ Compare: condition (or sample) A vs B.
//
// The main path is the differential test: a `compare` task runs DESeq2 on
// the 10-nt contact maps of the replicates (desktop/runtime/compare.py), and
// its registered result (`list_comparisons` / `read_comparison`) shows as a
// padj-weighted map of the significant cells plus the merged interactions.
// An interaction can be located on Explore's contact map, opened in Region,
// or folded once with A's and once with B's evidence ("Fold A & B").
//
// Under a test, compare_panels.go adds its enriched regions' local
// structures (structure_regions) and the folding energy of its interactions
// (an `energy` task, read_energy).
//
// "Quick view" is the descriptive comparison: the `compare_bins` query
// (record counts or cluster support, per million or raw, no statistics).
//
// The settings follow the web Compare page: condition labels, RNA X and
// RNA Y (an inter-RNA test when they differ), the quick view's measure and
// normalisation and the similarity panel (white or grey). The exports
// (map PNG, significant cells, the files for R / HYB2, parameters and the
// top 10 local maps ZIP) are compare_export.go.
//
// Navigate options consumed by Show:
//
//	{"comparisonId": "<id>"}   select a result (the tasks drawer sends it)
//
// Navigate options produced:
//
//	explore:   {"view": "contact-map" | "region", "selectionKey": "c:AFR",
//	            "sampleIds": [...], "gene": "<gene>", "binSize": 10,
//	            "x": <arm 1 start>, "y": <arm 2 start>,
//	            "xEnd": x+binSize-1, "yEnd": y+binSize-1,
//	            "gene2": "<RNA Y>" (inter-RNA results only: y is on it)}
//	           The top bar's selection and gene are set to the same values
//	           first, so Explore shows the right data even if it ignores the
//	           focus fields. selectionKey is "c:<condition>" or "s:<sample>",
//	           never "all": the A / B pickers offer GroupOptions, which leave
//	           every-sample merge out.
//	structure: {"pair": [taskIdA, taskIdB], "pairLabels": [labelA, labelB]}
//	           (Fold A & B: the Structure page shows A above, B below once
//	           both finish).

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
	"hyb2desktop/widgets"
)

func init() {
	RegisterPageFactory("compare", func(a *App) Page { return newComparePage(a) })
	OnTaskFinished(func(a *App, t model.Task) {
		if t.Kind != "compare" {
			return
		}
		if p, ok := a.pages["compare"].(*comparePage); ok && a.Project != nil {
			p.loadList()
		}
	})
}

var (
	cmpRed  = ui.RGB(214, 69, 65)
	cmpBlue = ui.RGB(43, 108, 196)
)

// ---- data

type comparisonParams struct {
	MinCount   *float64 `json:"min_count"`
	Range      *float64 `json:"range"`
	PadjCutoff *float64 `json:"padj_cutoff"`
	SimMin     *float64 `json:"similarity_min"`
	GeneID     *string  `json:"gene_id"`
	// Gene2 is RNA Y of an inter-RNA test (x on GeneID or each sample's
	// gene, y on Gene2); nil or the same gene for a homodimer map.
	Gene2 *string `json:"gene2"`
}

type comparisonSummary struct {
	CellsTested      int64     `json:"cells_tested"`
	Significant      int64     `json:"significant"`
	SignificantUp    int64     `json:"significant_up"`
	SignificantDown  int64     `json:"significant_down"`
	Interactions     int64     `json:"interactions"`
	InteractionsUp   int64     `json:"interactions_up"`
	InteractionsDown int64     `json:"interactions_down"`
	MaxCoordinate    int64     `json:"max_coordinate"`
	SizeFactors      []float64 `json:"size_factors"`
	SimilarityCells  int64     `json:"similarity_cells"`
	SimilarityCap    float64   `json:"similarity_cap"`
	// CellsTotal (cells with a count in any sample) is in summary.json
	// only; Timings.TotalS is the run time.
	CellsTotal int64 `json:"cells_total"`
	Timings    struct {
		TotalS *float64 `json:"total_s"`
	} `json:"timings"`
}

type comparisonEntry struct {
	ID         string            `json:"id"`
	TaskID     string            `json:"task_id"`
	SamplesOne []string          `json:"samples_one"`
	SamplesTwo []string          `json:"samples_two"`
	LabelOne   string            `json:"label_one"`
	LabelTwo   string            `json:"label_two"`
	Genes      []string          `json:"genes"`
	Parameters comparisonParams  `json:"parameters"`
	CreatedAt  string            `json:"created_at"`
	Paths      map[string]string `json:"paths"`
	Summary    comparisonSummary `json:"summary"`
	Available  *bool             `json:"available"`
}

type cmpInteraction struct {
	Direction string   `json:"direction"`
	X         int      `json:"x"`
	Y         int      `json:"y"`
	LFC       *float64 `json:"log2FoldChange"`
	Padj      *float64 `json:"padj"`
	BaseMean  *float64 `json:"baseMean"`
}

type comparisonResult struct {
	Summary comparisonSummary `json:"summary"`
	Cells   struct {
		X        []int      `json:"x"`
		Y        []int      `json:"y"`
		LFC      []*float64 `json:"log2FoldChange"`
		Padj     []*float64 `json:"padj"`
		BaseMean []*float64 `json:"baseMean"`
	} `json:"cells"`
	CellsTotal   int64            `json:"cells_total"`
	Truncated    bool             `json:"truncated"`
	Interactions []cmpInteraction `json:"interactions"`
	// Similarity is HYB2's similarity heatmap (bin/plot_similarity_map on
	// the two conditions' merged maps), strongest cells first; nil for tests
	// made before it existed.
	Similarity *struct {
		X     []int `json:"x"`
		Y     []int `json:"y"`
		Count []int `json:"count"`
	} `json:"similarity"`
}

// cmpQuickResult is compare_bins' result.
type cmpQuickResult struct {
	Gene     string     `json:"gene"`
	BinSize  int        `json:"bin_size"`
	Length   int        `json:"length"`
	Length2  int        `json:"length2"`
	Cells    int64      `json:"cells"`
	LibraryA *float64   `json:"library_a"`
	LibraryB *float64   `json:"library_b"`
	X        []int      `json:"x"`
	Y        []int      `json:"y"`
	ACount   []float64  `json:"a_count"`
	BCount   []float64  `json:"b_count"`
	A        []float64  `json:"a"`
	B        []float64  `json:"b"`
	Log2     []*float64 `json:"log2ratio"`
	// Effect is the web Compare page's log2((A + p) / (B + p)) on the
	// condition means of per-million values (p = Pseudocount).
	Effect        []*float64 `json:"effect"`
	Pseudocount   float64    `json:"pseudocount"`
	Gene2         string     `json:"gene2"`
	Measure       string     `json:"measure"`
	Normalisation string     `json:"normalisation"`
}

type cmpQuickRow struct {
	X, Y       int
	A, B       float64  // condition values: mean (per million, or raw) of each group's datasets
	RawA, RawB float64  // summed raw counts
	Ratio      *float64 // log2 A/B (the web's effect)
}

// quickBins are the web Compare page's bin sizes; quickMaxCells its cell limit.
var quickBins = []string{"5 nt", "10 nt", "25 nt", "50 nt", "100 nt"}

const quickMaxCells = 1_500_000

// The web Compare page's map settings (web/analysis-pages.js): measure,
// normalisation and similarity panel; the RNA pickers' first entries.
const (
	cmpMeasureRecords   = "Record count"
	cmpMeasureSupport   = "Cluster support"
	cmpNormaliseLibrary = "Per million (library size)"
	cmpNormaliseNone    = "Raw mean count"
	cmpPanelWhite       = "White (as similarity_heatmap.R)"
	cmpPanelGrey        = "Light grey (shows bins at the minimum)"
	cmpEachGene         = "Each sample's gene"
	cmpSameAsX          = "Same as RNA X"
)

var (
	cmpMeasures       = []string{cmpMeasureRecords, cmpMeasureSupport}
	cmpNormalisations = []string{cmpNormaliseLibrary, cmpNormaliseNone}
	cmpSimPanels      = []string{cmpPanelWhite, cmpPanelGrey}
	// cmpGrey235 is the grey panel: ggplot theme_grey's #EBEBEB, as the web.
	cmpGrey235 = ui.RGB(235, 235, 235)
)

// cmpContext names the two groups of a result for Locate / Region / Fold.
type cmpContext struct {
	KeyA, KeyB     string
	IDsA, IDsB     []string
	GeneA, GeneB   string
	LabelA, LabelB string
	Bin, Pad       int
	// Partner is RNA Y of an inter-RNA result ("" for a homodimer map).
	Partner string
}

// cmpTarget is one row the actions act on.
type cmpTarget struct {
	X, Y      int
	HigherInB bool
}

type cmpFoldPlan struct {
	ctx        cmpContext
	start, end int
}

// ---- page

const (
	cmpModeTest = iota
	cmpModeQuick
)

type comparePage struct {
	a       *App
	project string

	keyA, keyB string
	minCount   float64
	mergeRange float64
	padj       float64
	simMin     float64
	settings   bool
	runMsg     string
	// labelA, labelB name the conditions ("" = the selection's name);
	// geneX, geneY are RNA X and RNA Y ("" = each sample's gene, RNA X).
	labelA, labelB string
	geneX, geneY   string
	measure        string  // quick view: cmpMeasures
	normalise      string  // quick view: cmpNormalisations
	simPanel       string  // cmpSimPanels
	topQuantile    float64 // contrast quantile of the top 10 local maps (Lau -q)

	comparisons []comparisonEntry
	listErr     string
	loading     bool
	reloadAgain bool // a task finished while the list was loading

	mode       int
	selectedID string
	cache      map[string]*comparisonResult
	reading    map[string]bool
	readErr    map[string]string
	readGen    int // bumped by clearResults; an older read's answer is dropped

	direction int // 0 all, 1 higher in A (up), 2 higher in B (down)
	table     ui.ListState
	row       int
	heat      *widgets.Heatmap
	heatKey   string
	simHeat   *widgets.Heatmap
	simKey    string
	shown     []cmpInteraction
	shownKey  string

	quick struct {
		busy    bool
		err     string
		data    *cmpQuickResult
		keyA    string
		keyB    string
		gene    string
		gene2   string
		labelA  string
		labelB  string
		idsA    []string
		idsB    []string
		bin     string
		rows    []cmpQuickRow // built with data, off the UI goroutine
		cap     float64       // quickEffectCap(rows)
		rowsKey string
		seq     int // of the request shown; a stale answer is dropped
		table   ui.ListState
		row     int
		heat    *widgets.Heatmap
		heatKey string
	}

	foldOpen bool
	fold     cmpFoldPlan
	// arcMapFed is the test "Use in arc map" last sent to the Structure page.
	arcMapFed string

	// panels of the selected test (compare_panels.go)
	panels cmpPanels
}

func newComparePage(a *App) *comparePage {
	p := &comparePage{a: a}
	p.reset()
	return p
}

func (p *comparePage) reset() {
	p.minCount, p.mergeRange, p.padj, p.simMin = 2, 100, 0.05, 1
	p.keyA, p.keyB = "", ""
	p.labelA, p.labelB, p.geneX, p.geneY = "", "", "", ""
	p.measure, p.normalise, p.simPanel, p.topQuantile = cmpMeasureRecords, cmpNormaliseLibrary, cmpPanelWhite, 0.95
	p.comparisons, p.listErr, p.loading = nil, "", false
	p.mode, p.selectedID = cmpModeTest, ""
	p.clearResults()
	p.direction, p.row = 0, -1
	p.table.Selected = &p.row
	p.heat = widgets.NewHeatmap()
	// 10-nt cells are a fraction of a pixel on a whole genome: draw each
	// significant one at least 3 DIPs wide so it can be seen.
	p.heat.MinCell = 3
	p.simHeat = widgets.NewHeatmap()
	p.simHeat.MinCell = 3
	p.heatKey, p.simKey, p.shownKey = "", "", ""
	p.quick.busy, p.quick.err, p.quick.data = false, "", nil
	p.quick.bin, p.quick.row = "10 nt", -1
	p.quick.table.Selected = &p.quick.row
	p.quick.heat = widgets.NewHeatmap()
	p.quick.heatKey, p.quick.rowsKey = "", ""
	p.quick.rows, p.quick.seq = nil, p.quick.seq+1
	p.foldOpen = false
	p.arcMapFed = ""
	p.runMsg = ""
	p.panels.reset()
}

// clearResults forgets the read tests and the views drawn from them; reads
// still in flight are dropped when they answer.
func (p *comparePage) clearResults() {
	p.cache = map[string]*comparisonResult{}
	p.reading = map[string]bool{}
	p.readErr = map[string]string{}
	p.readGen++
	p.heatKey, p.simKey, p.shownKey = "", "", ""
}

func (p *comparePage) Show() {
	a := p.a
	if a.Project == nil {
		return
	}
	if a.Project.Path != p.project {
		p.project = a.Project.Path
		p.reset()
	}
	p.defaultGroups()
	if id, _ := a.NavOptions["comparisonId"].(string); id != "" {
		p.selectedID = id
		p.mode = cmpModeTest
		p.row = -1
		a.NavOptions = nil
	}
	p.loadList()
}

// defaultGroups picks the first two conditions (or selections) for A and B.
func (p *comparePage) defaultGroups() {
	options := p.a.GroupOptions()
	var conditions []SelectionOption
	keys := map[string]bool{}
	for _, o := range options {
		keys[o.Key] = true
		if o.Group == "Conditions" {
			conditions = append(conditions, o)
		}
	}
	pool := options
	if len(conditions) >= 2 {
		pool = conditions
	}
	if !keys[p.keyA] {
		p.keyA = ""
		if len(pool) > 0 {
			p.keyA = pool[0].Key
		}
	}
	if !keys[p.keyB] || p.keyB == p.keyA {
		p.keyB = ""
		for _, o := range pool {
			if o.Key != p.keyA {
				p.keyB = o.Key
				break
			}
		}
	}
}

// ---- loading

func (p *comparePage) loadList() {
	a := p.a
	if a.Project == nil {
		return
	}
	if p.loading {
		p.reloadAgain = true
		return
	}
	path := a.Project.Path
	p.loading = true
	a.Go(func() func() {
		listed, err := a.B.ProjectOp(path, "list_comparisons", nil)
		var entries []comparisonEntry
		if err == nil {
			err = model.Decode(listed["comparisons"], &entries)
		}
		return func() {
			if a.Project == nil || a.Project.Path != path {
				return
			}
			p.loading = false
			if p.reloadAgain {
				p.reloadAgain = false
				defer p.loadList()
			}
			if err != nil {
				p.listErr = err.Error()
				return
			}
			p.listErr = ""
			p.comparisons = entries
			// A re-run may reuse nothing, but results can change on disk.
			for _, e := range entries {
				if p.readErr[e.ID] != "" {
					delete(p.readErr, e.ID)
				}
			}
			if p.selectedID == "" && len(entries) > 0 && p.mode == cmpModeTest {
				p.selectedID = entries[0].ID
			}
			p.ensureResult()
		}
	})
}

func (p *comparePage) entry(id string) *comparisonEntry {
	for i := range p.comparisons {
		if p.comparisons[i].ID == id {
			return &p.comparisons[i]
		}
	}
	return nil
}

// cmpReadLimit is how many significant cells (the most significant) the
// page reads, draws and exports as the map.
const cmpReadLimit = 250_000

// ensureResult reads the selected comparison once.
func (p *comparePage) ensureResult() {
	a := p.a
	id := p.selectedID
	if id == "" || p.entry(id) == nil || p.cache[id] != nil || p.reading[id] || p.readErr[id] != "" || a.Project == nil {
		return
	}
	path, gen := a.Project.Path, p.readGen
	p.reading[id] = true
	a.Go(func() func() {
		raw, err := a.B.ProjectOp(path, "read_comparison", core.Obj{"comparison_id": id, "limit": cmpReadLimit})
		var result comparisonResult
		if err == nil {
			err = model.Decode(raw, &result)
		}
		return func() {
			if a.Project == nil || a.Project.Path != path || p.readGen != gen {
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

func (p *comparePage) selectComparison(id string) {
	p.selectedID = id
	p.mode = cmpModeTest
	p.row = -1
	p.ensureResult()
}

// referenceNotice warns when A and B are mapped to different references:
// the test compares their positions 1:1, which is only meaningful when the
// references are colinear (e.g. two strains of one virus).
func (p *comparePage) referenceNotice() string {
	a := p.a
	refs := func(key string) []string {
		var out []string
		for _, id := range a.SelectionIDs(key) {
			if s := a.Project.Sample(id); s != nil && s.ReferenceID != "" && !slices.Contains(out, s.ReferenceID) {
				out = append(out, s.ReferenceID)
			}
		}
		return out
	}
	refA, refB := refs(p.keyA), refs(p.keyB)
	if len(refA) == 0 || len(refB) == 0 || slices.Equal(refA, refB) {
		return ""
	}
	name := func(ids []string) string {
		var out []string
		for _, id := range ids {
			if r := a.Project.Reference(id); r != nil {
				out = append(out, nameOr(r.Label, r.ID))
			} else {
				out = append(out, id)
			}
		}
		return strings.Join(out, ", ")
	}
	return fmt.Sprintf("A is mapped to %s and B to %s: positions are compared 1:1, which assumes the references are aligned position by position.",
		name(refA), name(refB))
}

// ---- actions

func (p *comparePage) runTest() {
	a := p.a
	p.runMsg = ""
	idsA, idsB := a.SelectionIDs(p.keyA), a.SelectionIDs(p.keyB)
	if len(idsA) == 0 || len(idsB) == 0 {
		p.runMsg = "Choose two analysed groups."
		return
	}
	for _, id := range idsA {
		for _, other := range idsB {
			if id == other {
				p.runMsg = "A and B share samples; choose two different groups."
				return
			}
		}
	}
	if len(idsA)+len(idsB) < 3 {
		p.runMsg = "The test needs at least three samples (two replicates in one condition)."
		return
	}
	var missing []string
	for _, id := range append(append([]string{}, idsA...), idsB...) {
		if s := a.Project.Sample(id); s == nil || !s.HasHyb() {
			missing = append(missing, p.sampleName(id))
		}
	}
	if len(missing) > 0 {
		p.runMsg = "Run these samples first: " + strings.Join(missing, ", ")
		return
	}
	labelA, labelB := p.conditionLabels()
	params := map[string]any{"min_count": int(p.minCount), "range": int(p.mergeRange), "padj_cutoff": p.padj,
		"similarity_min": int(math.Max(1, p.simMin)), "label_one": labelA, "label_two": labelB}
	if p.geneX != "" {
		params["gene_id"] = p.geneX
	}
	if p.geneY != "" {
		params["gene2"] = p.geneY
	}
	req := core.TaskStartRequest{Kind: "compare", SamplesOne: idsA, SamplesTwo: idsB, Parameters: params}
	a.StartTask(req, "Differential test · "+labelA+" vs "+labelB, func(t model.Task, err error) {
		if err != nil {
			p.runMsg = "Could not start the test: " + err.Error()
			return
		}
		p.selectedID = t.TaskID
		p.mode = cmpModeTest
		p.row = -1
	})
}

// conditionLabels are the condition labels of the settings, or the
// selections' names where they are empty.
func (p *comparePage) conditionLabels() (string, string) {
	return nameOr(strings.TrimSpace(p.labelA), p.a.SelectionLabel(p.keyA)), nameOr(strings.TrimSpace(p.labelB), p.a.SelectionLabel(p.keyB))
}

// geneOptions are the genes of every reference, once each, for RNA X and Y.
func (p *comparePage) geneOptions() []string {
	var out []string
	for _, r := range p.a.Project.References {
		for _, s := range r.Sequences {
			if !slices.Contains(out, s.Name) {
				out = append(out, s.Name)
			}
		}
	}
	return out
}

func (p *comparePage) sampleName(id string) string {
	if p.a.Project != nil {
		if s := p.a.Project.Sample(id); s != nil {
			return s.Label()
		}
	}
	return id
}

func (p *comparePage) sampleNames(ids []string) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = p.sampleName(id)
	}
	return strings.Join(names, ", ")
}

// context derives the groups of a result: a condition key when the samples
// are exactly one condition's analysed samples, else the first sample.
func (p *comparePage) context(e *comparisonEntry) cmpContext {
	a := p.a
	keyFor := func(ids []string) string {
		if len(ids) == 0 {
			return ""
		}
		conditions := map[string]bool{}
		for _, id := range ids {
			if s := a.Project.Sample(id); s != nil && s.Condition != "" {
				conditions[s.Condition] = true
			}
		}
		if len(conditions) == 1 {
			for name := range conditions {
				all := a.SelectionIDs("c:" + name)
				if len(all) == len(ids) && cmpSameSet(all, ids) {
					return "c:" + name
				}
			}
		}
		return "s:" + ids[0]
	}
	geneFor := func(ids []string) string {
		if e.Parameters.GeneID != nil && *e.Parameters.GeneID != "" {
			return *e.Parameters.GeneID
		}
		if len(ids) > 0 {
			if s := a.Project.Sample(ids[0]); s != nil && s.GeneID != "" {
				return s.GeneID
			}
		}
		if len(e.Genes) > 0 {
			return e.Genes[0]
		}
		return a.Selected.Gene
	}
	pad := 100
	if e.Parameters.Range != nil {
		pad = int(*e.Parameters.Range)
	}
	ctx := cmpContext{KeyA: keyFor(e.SamplesOne), KeyB: keyFor(e.SamplesTwo), IDsA: e.SamplesOne, IDsB: e.SamplesTwo,
		GeneA: geneFor(e.SamplesOne), GeneB: geneFor(e.SamplesTwo),
		LabelA: nameOr(e.LabelOne, p.sampleNames(e.SamplesOne)), LabelB: nameOr(e.LabelTwo, p.sampleNames(e.SamplesTwo)),
		Bin: 10, Pad: pad}
	if g := e.Parameters.Gene2; g != nil && *g != "" && (*g != ctx.GeneA || *g != ctx.GeneB) {
		ctx.Partner = *g
	}
	return ctx
}

func cmpSameSet(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if !set[x] {
			return false
		}
	}
	return true
}

// referenceOf returns the reference holding a gene.
func (p *comparePage) referenceOf(gene string) string {
	for _, r := range p.a.Project.References {
		for _, s := range r.Sequences {
			if s.Name == gene {
				return r.ID
			}
		}
	}
	return ""
}

// locate opens Explore on the group where the cell is stronger.
func (p *comparePage) locate(row cmpTarget, ctx cmpContext, view string) {
	a := p.a
	key, ids, gene := ctx.KeyA, ctx.IDsA, ctx.GeneA
	if row.HigherInB {
		key, ids, gene = ctx.KeyB, ctx.IDsB, ctx.GeneB
	}
	if ref := p.referenceOf(gene); ref != "" {
		a.Selected.ReferenceID = ref
	}
	a.Selected.Gene = gene
	if key != "" {
		a.Selected.Key = key
	}
	a.normalizeSelection()
	options := map[string]any{
		"view": view, "selectionKey": key, "sampleIds": append([]string(nil), ids...), "gene": gene,
		"binSize": ctx.Bin, "x": row.X, "y": row.Y, "xEnd": row.X + ctx.Bin - 1, "yEnd": row.Y + ctx.Bin - 1,
	}
	if ctx.Partner != "" {
		options["gene2"] = ctx.Partner // y is a position on RNA Y
	}
	a.Navigate("explore", options)
}

// planFold opens the confirmation for two window folds around a row.
func (p *comparePage) planFold(row cmpTarget, ctx cmpContext) {
	start := cmpMax(1, cmpMin(row.X, row.Y)-ctx.Pad)
	end := cmpMax(row.X, row.Y) + ctx.Bin - 1 + ctx.Pad
	if length := p.a.Project.GeneLength(ctx.GeneA); length > 0 && end > length {
		end = length
	}
	p.fold = cmpFoldPlan{ctx: ctx, start: start, end: end}
	p.foldOpen = true
}

// startFold starts the A fold, then the B fold, then shows Structure.
func (p *comparePage) startFold() {
	a := p.a
	plan := p.fold
	p.foldOpen = false
	type group struct {
		ids         []string
		gene, label string
	}
	groups := []group{{plan.ctx.IDsA, plan.ctx.GeneA, plan.ctx.LabelA}, {plan.ctx.IDsB, plan.ctx.GeneB, plan.ctx.LabelB}}
	var started []string
	var next func(i int)
	next = func(i int) {
		if i >= len(groups) {
			if len(started) == 0 {
				return
			}
			options := map[string]any{}
			if len(started) == 2 {
				options["pair"] = started
				options["pairLabels"] = []string{plan.ctx.LabelA, plan.ctx.LabelB}
			} else {
				options["structureId"] = started[0]
			}
			a.Navigate("structure", options)
			return
		}
		g := groups[i]
		end := plan.end
		if length := a.Project.GeneLength(g.gene); length > 0 && end > length {
			end = length
		}
		gene, mode := g.gene, "windows"
		req := core.TaskStartRequest{Kind: "structure", SampleIDs: append([]string(nil), g.ids...), GeneID: &gene, Mode: &mode,
			Windows:    []core.Window{{Start: uint64(plan.start), End: uint64(end)}},
			Parameters: map[string]any{"allow_pseudoknot": false}}
		mem := StructureMemClaim(req, a.Project.GeneLength(g.gene), false)
		req.MemMB = &mem
		label := fmt.Sprintf("Structure · %s · %d-%d", g.label, plan.start, end)
		a.StartTask(req, label, func(t model.Task, err error) {
			if err != nil {
				a.Toast("Could not start the fold: %s", StructureStartError(err, mem))
			} else {
				started = append(started, t.TaskID)
			}
			next(i + 1)
		})
	}
	next(0)
}

func (p *comparePage) runQuick() {
	a := p.a
	p.runMsg = ""
	idsA, idsB := a.SelectionIDs(p.keyA), a.SelectionIDs(p.keyB)
	if len(idsA) == 0 || len(idsB) == 0 {
		p.runMsg = "Choose two analysed groups."
		return
	}
	if p.keyA == p.keyB {
		p.runMsg = "A and B are the same selection."
		return
	}
	gene := nameOr(p.geneX, a.Selected.Gene)
	if gene == "" {
		p.runMsg = "Choose a gene in the top bar or RNA X in the settings."
		return
	}
	gene2 := nameOr(p.geneY, gene)
	labelA, labelB := p.conditionLabels()
	measure, normalisation := "records", "library"
	if p.measure == cmpMeasureSupport {
		measure = "support"
	}
	if p.normalise == cmpNormaliseNone {
		normalisation = "none"
	}
	bin, _ := strconv.Atoi(strings.TrimSuffix(p.quick.bin, " nt"))
	if bin <= 0 {
		bin = 10
	}
	q := &p.quick
	q.busy, q.err, q.data, q.rows, q.row = true, "", nil, nil, -1
	q.seq++
	seq := q.seq
	q.keyA, q.keyB, q.gene, q.gene2, q.idsA, q.idsB = p.keyA, p.keyB, gene, gene2, idsA, idsB
	q.labelA, q.labelB = labelA, labelB
	p.mode = cmpModeQuick
	path := a.Project.Path
	a.Go(func() func() {
		raw, err := a.B.Query(path, "compare_bins", core.Obj{"sample_ids_a": idsA, "sample_ids_b": idsB, "gene": gene,
			"gene2": gene2, "bin_size": bin, "max_cells": quickMaxCells, "orientation": "both",
			"measure": measure, "normalisation": normalisation})
		var result cmpQuickResult
		if err == nil {
			err = model.Decode(raw, &result)
		}
		// Sorting 1.5M rows takes seconds: not on the UI goroutine.
		var rows []cmpQuickRow
		if err == nil {
			rows = cmpQuickRows(&result)
		}
		limit := quickEffectCap(rows)
		return func() {
			if a.Project == nil || a.Project.Path != path || q.seq != seq {
				return
			}
			q.busy = false
			if err != nil {
				q.err = err.Error()
				return
			}
			q.data, q.rows, q.cap = &result, rows, limit
			q.rowsKey, q.heatKey = fmt.Sprintf("%p", &result), ""
			q.row = -1
			q.table = ui.ListState{}
		}
	})
}

// ---- view

func (p *comparePage) View(c *ui.Context) {
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).Padding(24).Gap(16).Children(func() {
			PageHeader(c, "Compare", "Differential contacts between two conditions: DESeq2 on the 10-nt contact maps of the replicates (as hyb2_compare), merged into interactions.")
			p.controls(c)
			p.list(c)
			if p.mode == cmpModeQuick {
				p.quickView(c)
			} else {
				p.results(c)
			}
		})
	})
	p.foldDialog(c)
}

func (p *comparePage) controls(c *ui.Context) {
	a := p.a
	t := c.Theme()
	Card(c, "", func() {
		options := a.GroupOptions()
		if len(options) < 2 {
			ui.Text(c, "Comparisons need two analysed conditions or samples. Set conditions on the Data page and run the samples.").TextColor(t.TextMuted)
			return
		}
		ui.Row(c).Gap(12).AlignItems(ui.End).Wrap().Children(func() {
			p.groupPicker(c.Key("cmp-a"), "A (condition one)", &p.keyA, cmpRed, options)
			ui.Text(c, "vs").TextColor(t.TextMuted).Margin(0, 0, 8, 0)
			p.groupPicker(c.Key("cmp-b"), "B (condition two)", &p.keyB, cmpBlue, options)
			ui.Spacer(c)
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, fmt.Sprintf("%d + %d replicates", len(a.SelectionIDs(p.keyA)), len(a.SelectionIDs(p.keyB)))).
					FontSize(12).Bold().TextColor(t.TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "Run differential test").Clicked() {
						p.runTest()
					}
					label := "Quick view"
					if p.quick.busy {
						label = "Binning…"
					}
					if ui.Button(c, label).Tooltip("Library-size normalised difference, no statistics").Disabled(p.quick.busy).Clicked() {
						p.runQuick()
					}
				})
			})
		})
		if p.runMsg != "" {
			ui.Text(c, p.runMsg).TextColor(t.Danger)
		}
		if notice := p.referenceNotice(); notice != "" {
			ui.Text(c, notice).FontSize(12).TextColor(t.Warning)
		}
		ui.Collapsible(c.Key("cmp-settings"), "Settings", &p.settings, func() {
			ui.Column(c).Gap(8).Padding(8, 0, 0, 0).Children(func() {
				ui.Row(c).Gap(16).Wrap().Children(func() {
					Field(c, "Keep cells with total count >", func() {
						ui.NumberInput(c.Key("cmp-min"), &p.minCount, 0, 1e6, 1).Width(110).Label("Minimum count")
					})
					Field(c, "Merge range (nt)", func() {
						ui.NumberInput(c.Key("cmp-range"), &p.mergeRange, 0, 100000, 10).Width(110).Label("Merge range")
					})
					Field(c, "padj cutoff", func() {
						ui.NumberInput(c.Key("cmp-padj"), &p.padj, 0.0001, 1, 0.01).Width(110).Label("padj cutoff")
					})
					Field(c, "Similarity: keep cells with count ≥", func() {
						ui.NumberInput(c.Key("cmp-simmin"), &p.simMin, 1, 1e6, 1).Width(110).Label("Similarity minimum count")
					})
					Field(c, "Quick view bin", func() {
						ui.Select(c.Key("cmp-bin"), &p.quick.bin, quickBins).Width(110).Label("Quick view bin")
					})
				})
				ui.Row(c).Gap(16).Wrap().Children(func() {
					Field(c, "Condition A label", func() {
						ui.TextInput(c.Key("cmp-label-a"), &p.labelA).Placeholder(a.SelectionLabel(p.keyA)).Width(200).Label("Condition A label")
					})
					Field(c, "Condition B label", func() {
						ui.TextInput(c.Key("cmp-label-b"), &p.labelB).Placeholder(a.SelectionLabel(p.keyB)).Width(200).Label("Condition B label")
					})
					genes := p.geneOptions()
					rnaX, rnaY := nameOr(p.geneX, cmpEachGene), nameOr(p.geneY, cmpSameAsX)
					Field(c, "RNA X", func() {
						ui.Select(c.Key("cmp-rna-x"), &rnaX, append([]string{cmpEachGene}, genes...)).Width(220).Label("RNA X").OnChange(func() {
							p.geneX = map[bool]string{true: "", false: rnaX}[rnaX == cmpEachGene]
						})
					})
					Field(c, "RNA Y", func() {
						ui.Select(c.Key("cmp-rna-y"), &rnaY, append([]string{cmpSameAsX}, genes...)).Width(220).Label("RNA Y").OnChange(func() {
							p.geneY = map[bool]string{true: "", false: rnaY}[rnaY == cmpSameAsX]
						})
					})
				})
				ui.Row(c).Gap(16).Wrap().Children(func() {
					Field(c, "Quick view measure", func() {
						ui.Select(c.Key("cmp-measure"), &p.measure, cmpMeasures).Width(160).Label("Quick view measure")
					})
					Field(c, "Quick view normalisation", func() {
						ui.Select(c.Key("cmp-normalise"), &p.normalise, cmpNormalisations).Width(210).Label("Quick view normalisation")
					})
					Field(c, "Similarity panel", func() {
						ui.Select(c.Key("cmp-sim-panel"), &p.simPanel, cmpSimPanels).Width(280).Label("Similarity panel")
					})
				})
				ui.Text(c, "Labels and RNAs apply to the next test and quick view. With RNA Y set to another RNA the test compares the inter-RNA map: x on RNA X, y on RNA Y, hybrids written Y × X counted with their arms swapped (as the web app). Measure and normalisation change the quick view only: the test always uses raw record counts. The similarity panel is display only: on white, as similarity_heatmap.R, cells at the smallest count barely show; the grey panel shows them.").
					FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, "Only the differential test says whether a contact differs: it uses the replicates as the noise estimate and gives a padj per 10-nt cell, so it needs at least two biological replicates per condition. The quick view and the similarity heatmap only describe counts. The test follows hyb2_compare: DESeq2 Wald test (condition one vs two, alpha 0.01, independent filtering); cells with padj below the cutoff are significant, and significant cells within the merge range of a stronger one are merged into it. The similarity heatmap follows plot_similarity_map: each condition's replicates are summed into one map (as if their .hyb files were concatenated), cells below the similarity minimum are dropped from each map, and a cell in both maps shows the smaller count (1 keeps every cell, as hyb2 does).").
					FontSize(12).TextColor(t.TextMuted)
			})
		})
	})
}

func (p *comparePage) groupPicker(c *ui.Context, label string, key *string, color ui.Color, options []SelectionOption) {
	var labels []string
	keys := map[string]string{}
	current := ""
	for _, o := range options {
		labels = append(labels, o.Label)
		keys[o.Label] = o.Key
		if o.Key == *key {
			current = o.Label
		}
	}
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(12).Bold().TextColor(color)
		ui.Select(c.Key("select"), &current, labels).Width(240).Label(label).OnChange(func() {
			*key = keys[current]
			p.runMsg = ""
		})
	})
}

// ---- tests list

func (p *comparePage) list(c *ui.Context) {
	a := p.a
	t := c.Theme()
	Card(c, "", func() {
		ui.Row(c).Children(func() {
			ui.Text(c, "Differential tests").FontSize(16).Bold().Grow(1)
			if ui.Button(c, "Refresh").Clicked() {
				p.clearResults()
				p.loadList()
			}
		})
		running := cmpTaskRows(c, a, "compare")
		if p.listErr != "" {
			// Not "no tests yet": the list is unknown.
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Could not list comparisons: "+ShortError(p.listErr)).TextColor(t.Danger).Grow(1).Shrink(1).MinWidth(0)
				if ui.Button(c, "Retry").Clicked() {
					p.loadList()
				}
			})
			if len(p.comparisons) == 0 && running == 0 {
				return
			}
		}
		if len(p.comparisons) == 0 && running == 0 {
			msg := "No differential tests yet. Pick A and B, then Run differential test."
			if p.loading {
				msg = "Loading…"
			}
			ui.Text(c, msg).TextColor(t.TextMuted)
			return
		}
		for i := range p.comparisons {
			e := &p.comparisons[i]
			selected := p.mode == cmpModeTest && p.selectedID == e.ID
			item := ui.Column(c.Key("cmp-"+e.ID)).Gap(2).Padding(8, 10).Radius(8).Border(1, t.Border)
			if selected {
				item.Background(t.Accent.Alpha(0.10)).BorderColor(t.Accent)
			}
			item.Children(func() {
				title := nameOr(e.LabelOne, p.sampleNames(e.SamplesOne)) + " vs " + nameOr(e.LabelTwo, p.sampleNames(e.SamplesTwo))
				if cmpTitleButton(c, title).Clicked() {
					p.selectComparison(e.ID)
				}
				s := e.Summary
				meta := fmt.Sprintf("%s significant cells (%s higher in A, %s in B) · %s interactions · %s",
					FormatNumber(s.Significant), FormatNumber(s.SignificantUp), FormatNumber(s.SignificantDown),
					FormatNumber(s.Interactions), FormatTime(e.CreatedAt))
				if e.Available != nil && !*e.Available {
					meta += " · files missing"
				}
				ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted)
			})
		}
	})
}

// cmpTaskRows lists running tasks of a kind (progress, Cancel) and its
// interrupted or failed ones of this session (Resume); it returns how many
// are running.
func cmpTaskRows(c *ui.Context, a *App, kind string) int {
	t := c.Theme()
	running := 0
	failed := 0
	for _, task := range a.TaskList() {
		switch {
		case task.Kind != kind:
		case task.Active():
			running++
			ui.Column(c.Key("task-"+task.TaskID)).Gap(4).Padding(8, 10).Radius(8).Border(1, t.Accent.Alpha(0.5)).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, a.TaskLabel(task)).Bold().Grow(1)
					StatusChip(c, task.Status)
					if ui.Button(c, "Cancel").Clicked() {
						a.CancelTask(task.TaskID)
					}
				})
				ProgressBar(c, task)
			})
		case failed < 3 && (task.Status == "interrupted" || (task.Status == "failed" && task.FinishedAt >= a.SessionStart)):
			failed++
			ui.Column(c.Key("task-"+task.TaskID)).Gap(4).Padding(8, 10).Radius(8).Border(1, t.Border).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, a.TaskLabel(task)).Bold().Grow(1)
					StatusChip(c, task.Status)
					if ui.Button(c, "Resume").Clicked() {
						a.ResumeTask(task.TaskID)
					}
				})
				if e := task.ErrorText(); e != "" {
					ui.Text(c, truncate(e, 300)).FontSize(12).TextColor(t.Danger)
				}
			})
		}
	}
	return running
}

// ---- test result

func (p *comparePage) results(c *ui.Context) {
	a := p.a
	t := c.Theme()
	id := p.selectedID
	if id == "" {
		return
	}
	e := p.entry(id)
	if e == nil {
		if task, ok := a.Tasks[id]; ok && task.Active() {
			Card(c, "", func() {
				ui.Text(c, "The test is running; results appear here when it finishes.").TextColor(t.TextMuted)
			})
		}
		return
	}
	result := p.cache[id]
	if result == nil {
		Card(c, "", func() {
			if err := p.readErr[id]; err != "" {
				ui.Text(c, "Could not read the comparison: "+err).TextColor(t.Danger)
			} else {
				ui.Text(c, "Loading results…").TextColor(t.TextMuted)
			}
		})
		return
	}
	ctx := p.context(e)
	shown := p.shownInteractions(result)
	p.syncHeatmap(e, result)
	if p.row >= len(shown) {
		p.row = -1
	}
	stale := !p.inputsCurrent(e)
	Card(c, "", func() {
		if stale {
			ui.Text(c, cmpStaleNotice).FontSize(12).TextColor(t.Warning)
		}
		s := result.Summary
		cutoff := 0.05
		if e.Parameters.PadjCutoff != nil {
			cutoff = *e.Parameters.PadjCutoff
		}
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, ctx.LabelA+" vs "+ctx.LabelB).FontSize(18).Bold()
			genes := strings.Join(e.Genes, ", ")
			if ctx.Partner != "" {
				genes += " × " + ctx.Partner
			}
			ui.Text(c, fmt.Sprintf("%s · DESeq2 on %s cells (10 nt) · padj < %g · log2FC = A / B",
				genes, FormatNumber(s.CellsTested), cutoff)).FontSize(12).TextColor(t.TextMuted)
		})
		// the web card's statistics (deseq-card.js renderResult)
		ui.Row(c).Gap(10).Wrap().Children(func() {
			cmpFact(c, "Cells tested", cmpCellsTested(s, e.Parameters.MinCount), t.Text)
			cmpFact(c, "Significant (padj < "+jsNumber(cutoff)+")", FormatNumber(s.Significant)+" cells", t.Text)
			cmpFact(c, "Higher in "+ctx.LabelA, FormatNumber(s.SignificantUp)+" cells · "+FormatNumber(s.InteractionsUp)+" interactions", cmpRed)
			cmpFact(c, "Higher in "+ctx.LabelB, FormatNumber(s.SignificantDown)+" cells · "+FormatNumber(s.InteractionsDown)+" interactions", cmpBlue)
			cmpFact(c, "Size factors", cmpSizeFactors(s.SizeFactors, " · "), t.Text).
				Tooltip("DESeq2 size factors, in sample order: " + p.sampleNames(append(append([]string{}, e.SamplesOne...), e.SamplesTwo...)))
			if s.Timings.TotalS != nil {
				cmpFact(c, "Run time", cmpSeconds(*s.Timings.TotalS), t.Text)
			}
		})
		ui.Text(c, "padj is corrected over every tested cell. Before reporting an interaction, check that each replicate supports it (open the region in Explore for each sample): a small padj with a low mean count can still come from one replicate.").
			FontSize(12).TextColor(t.TextMuted)
		if result.Truncated {
			ui.Text(c, fmt.Sprintf("The map shows the %s strongest of %s significant cells.",
				FormatNumber(int64(len(result.Cells.X))), FormatNumber(result.CellsTotal))).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				if row := p.pickedInteraction(shown); row != nil {
					p.heat.Highlight = widgets.Cell{X: float64(row.X - ctx.Pad), Y: float64(row.Y - ctx.Pad),
						W: float64(2*ctx.Pad + ctx.Bin), H: float64(2*ctx.Pad + ctx.Bin)}
				} else {
					p.heat.Highlight = widgets.Cell{}
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
					if ui.Button(c, "Zoom in").Clicked() {
						cmpZoom(p.heat, 0.5)
					}
					if ui.Button(c, "Zoom out").Clicked() {
						cmpZoom(p.heat, 2)
					}
					if ui.Button(c, "Reset zoom").Clicked() {
						p.heat.ResetZoom()
					}
					ui.Text(c, "Scroll to zoom · drag to pan · double-click to reset").FontSize(12).TextColor(t.TextMuted)
				})
				ev := p.heat.Show(c.Key("cmp-heat"), p.mapSide(), "Differential coverage map: red higher in A, blue higher in B; opacity by −log10 padj")
				if ev.Point {
					v := p.heat.View()
					p.pickNearest(shown, ev.PX, ev.PY, cmpPickReach(ctx.Pad, v.X1-v.X0, float64(p.mapSide())))
					p.heat.Picked = -1
				}
				cmpPadjLegend(c, ctx.LabelA, ctx.LabelB)
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "Use in arc map").Tooltip("Draw these merged interactions as the Structure page's differential arcs").Clicked() {
						p.useInArcMap(e)
					}
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "Download map SVG…").Tooltip("Save the differential coverage map as shown").Clicked() {
						p.exportMapSVG()
					}
					if ui.Button(c, "Download map PNG…").Tooltip("Save the differential coverage map as shown, as a PNG").Clicked() {
						p.exportMapPNG()
					}
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "Export interactions TSV…").Tooltip("Save all merged interactions as a tab-separated file").Clicked() {
						p.exportInteractionsTSV()
					}
					if ui.Button(c, "Export significant cells TSV…").Tooltip("Save every significant 10-nt cell (x, y, log2FoldChange, padj, baseMean)").Clicked() {
						p.exportSignificantTSV()
					}
				})
				if p.arcMapFed == e.ID && !stale {
					ui.Text(c, cmpArcMapNote).FontSize(12).TextColor(t.TextMuted).Width(p.mapSide() + 44)
				}
			})
			ui.Column(c).Gap(8).Grow(1).MinWidth(320).Children(func() {
				ui.Row(c).Gap(8).Wrap().Children(func() {
					ui.Text(c, "Merged interactions ("+FormatNumber(int64(len(shown)))+")").Bold().Grow(1)
					previous := p.direction
					ui.Segmented(c.Key("cmp-direction"), &p.direction, "All", "Higher in A", "Higher in B").Label("Direction")
					if p.direction != previous {
						p.row = -1
					}
				})
				p.interactionTable(c, shown, ctx)
				var target *cmpTarget
				if row := p.pickedInteraction(shown); row != nil {
					target = &cmpTarget{X: row.X, Y: row.Y, HigherInB: row.LFC != nil && *row.LFC < 0}
				}
				p.actionRow(c, target, ctx)
				var files []string
				for _, k := range sortedKeys(e.Paths) {
					if e.Paths[k] != "" {
						files = append(files, e.Paths[k])
					}
				}
				if len(files) > 0 {
					ui.Text(c, "Files: "+strings.Join(files, ", ")).FontSize(11).TextColor(t.TextMuted)
				}
			})
		})
	})
	p.exportsCard(c, e, stale)
	p.similarityCard(c, e, result, ctx)
	p.regionsCard(c.Key("cmp-regions"), e, ctx)
	p.energyCard(c.Key("cmp-energy"), e, ctx)
}

// similarityCard shows HYB2's similarity heatmap of the test's two groups.
func (p *comparePage) similarityCard(c *ui.Context, e *comparisonEntry, result *comparisonResult, ctx cmpContext) {
	t := c.Theme()
	Card(c, "", func() {
		ui.Text(c, "Similarity heatmap").FontSize(16).Bold()
		if result.Similarity == nil {
			ui.Text(c, "This test was run before the similarity heatmap existed; run it again to get one.").TextColor(t.TextMuted)
			return
		}
		key := e.ID
		if key != p.simKey {
			p.simKey = key
			length := float64(p.mapLength(e, result))
			p.simHeat.SetCells(cmpSimilarityCells(result), length, length)
		}
		minimum := 1.0
		if e.Parameters.SimMin != nil {
			minimum = *e.Parameters.SimMin
		}
		note := fmt.Sprintf("Cells in both %s and %s", ctx.LabelA, ctx.LabelB)
		if minimum > 1 {
			note += fmt.Sprintf(" (count ≥ %g in each)", minimum)
		}
		note += fmt.Sprintf("; green = the smaller of the two summed counts, capped at the 95%% quantile (%g). As hyb2's plot_similarity_map.", result.Summary.SimilarityCap)
		ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(10).Wrap().Children(func() {
			cmpStat(c, "Similar cells", FormatNumber(result.Summary.SimilarityCells), cmpGreen)
			cmpStat(c, "Colour cap", fmt.Sprintf("%g", result.Summary.SimilarityCap), t.Text)
		})
		if n := int64(len(result.Similarity.X)); n < result.Summary.SimilarityCells {
			ui.Text(c, fmt.Sprintf("The map shows the %s strongest of %s cells.", FormatNumber(n),
				FormatNumber(result.Summary.SimilarityCells))).FontSize(12).TextColor(t.TextMuted)
		}
		// similarity_heatmap.R draws on white; keep the theme surface in dark
		// mode. The grey panel (ggplot's theme_grey, as the web's) shows the
		// cells at the smallest count.
		switch {
		case p.simPanel == cmpPanelGrey:
			p.simHeat.Background = cmpGrey235
		case t.Background.R >= 128:
			p.simHeat.Background = ui.RGB(255, 255, 255)
		default:
			p.simHeat.Background = ui.Color{}
		}
		p.simHeat.Show(c.Key("cmp-sim-heat"), p.mapSide(), "Similarity heatmap: cells in both groups, green by the smaller count")
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Export SVG…").Tooltip("Save the similarity heatmap as shown").Clicked() {
				p.exportSimilaritySVG()
			}
		})
	})
}

// cmpGreen is similarity_heatmap.R's forestgreen.
var cmpGreen = ui.RGB(34, 139, 34)

// cmpSimilarityCells colours the similar cells forestgreen by count / cap
// (white at 0 on a light surface); weakest first so strong cells stay on top.
func cmpSimilarityCells(result *comparisonResult) []widgets.Cell {
	sim := result.Similarity
	if sim == nil {
		return nil
	}
	limit := math.Max(1, result.Summary.SimilarityCap)
	out := make([]widgets.Cell, 0, len(sim.X))
	for i := len(sim.X) - 1; i >= 0; i-- {
		if i >= len(sim.Y) || i >= len(sim.Count) {
			continue
		}
		ratio := math.Min(1, float64(sim.Count[i])/limit)
		out = append(out, widgets.Cell{X: float64(sim.X[i]), Y: float64(sim.Y[i]), W: 10, H: 10,
			Color: cmpGreen.Alpha(float32(0.06 + 0.94*ratio))})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Color.A < out[j].Color.A })
	return out
}

// cmpTitleButton is a list item's title that selects it.
func cmpTitleButton(c *ui.Context, title string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Cursor(ui.CursorPointer).Justify(ui.Start).Label(title)
	b.Children(func() { ui.Text(c, title).Bold().TextColor(t.Accent) })
	return b
}

func cmpStat(c *ui.Context, label, value string, color ui.Color) ui.Element {
	t := c.Theme()
	return ui.Column(c).Gap(2).Padding(8, 12).Radius(8).Background(t.Surface).MinWidth(120).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(18).Bold().TextColor(color)
	})
}

// cmpFact is one of the test's statistics: a label over its value.
func cmpFact(c *ui.Context, label, value string, color ui.Color) ui.Element {
	t := c.Theme()
	return ui.Column(c).Gap(2).Padding(8, 12).Radius(8).Background(t.Surface).MinWidth(120).Shrink(1).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(14).Bold().TextColor(color)
	})
}

// cmpZoom zooms a map around its centre (the web's Zoom in / Zoom out).
func cmpZoom(h *widgets.Heatmap, factor float64) {
	v := h.View()
	cx, cy := (v.X0+v.X1)/2, (v.Y0+v.Y1)/2
	hw, hh := (v.X1-v.X0)*factor/2, (v.Y1-v.Y0)*factor/2
	h.SetView(widgets.Window{X0: cx - hw, X1: cx + hw, Y0: cy - hh, Y1: cy + hh})
}

// useInArcMap is the web's "Use in arc map": the Structure page draws the
// test's merged interactions as its differential arcs (with its own padj
// cutoff).
func (p *comparePage) useInArcMap(e *comparisonEntry) {
	a := p.a
	st, ok := a.PageFor("structure").(*structurePage)
	if !ok {
		return
	}
	st.layers.cmpStale = true // the list may predate this test
	a.Navigate("structure", nil)
	st.setOverlay(e.ID)
	// as the web's (showStructures = false): the structures in one colour,
	// even when another test was drawn before
	st.layers.mono = true
	p.arcMapFed = e.ID
	a.Toast("The arc map now draws the %s merged interactions (its own padj filter, %g, applies).",
		FormatNumber(e.Summary.Interactions), st.layers.padj)
}

// shownInteractions: sorted by padj, filtered by direction (cached).
func (p *comparePage) shownInteractions(result *comparisonResult) []cmpInteraction {
	key := fmt.Sprintf("%s/%d", p.selectedID, p.direction)
	if key == p.shownKey {
		return p.shown
	}
	var out []cmpInteraction
	for _, row := range result.Interactions {
		if (p.direction == 1 && row.Direction != "up") || (p.direction == 2 && row.Direction != "down") {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return cmpPadj(out[i].Padj) < cmpPadj(out[j].Padj) })
	p.shown, p.shownKey = out, key
	// A new list starts at its top (a kept ListState keeps its place by key).
	p.table = ui.ListState{}
	return out
}

func cmpPadj(v *float64) float64 {
	if v == nil || math.IsNaN(*v) {
		return math.Inf(1)
	}
	return *v
}

// syncHeatmap rebuilds the map's cells when the result or direction changes.
func (p *comparePage) syncHeatmap(e *comparisonEntry, result *comparisonResult) {
	key := fmt.Sprintf("%s/%d", e.ID, p.direction)
	if key == p.heatKey {
		return
	}
	p.heatKey = key
	p.heat.SetCells(cmpSignificantCells(result, p.direction), float64(p.mapLength(e, result)), float64(p.mapLength(e, result)))
}

// mapLength is the map's extent, as the web's: the largest coordinate of
// the test plus one cell.
func (p *comparePage) mapLength(_ *comparisonEntry, result *comparisonResult) int {
	return cmpMapLength(result.Summary)
}

// cmpSignificantCells colours the significant 10-nt cells (cmpCells): red
// higher in A, blue higher in B, opacity by −log10 padj; weakest first so
// strong cells stay on top.
func cmpSignificantCells(result *comparisonResult, direction int) []widgets.Cell {
	return cmpWidgetCells(cmpCells(result, direction))
}

func (p *comparePage) pickedInteraction(shown []cmpInteraction) *cmpInteraction {
	if p.row >= 0 && p.row < len(shown) {
		return &shown[p.row]
	}
	return nil
}

// pickNearest chooses the cmpInteraction whose cell centre is nearest a
// clicked point (web deseq-card.js onpointerup), within reach.
func (p *comparePage) pickNearest(shown []cmpInteraction, x, y, reach float64) {
	if i := cmpNearest(shown, x, y, reach); i >= 0 {
		p.row = i
	}
}

// cmpNearest is the index of the row whose cell centre is nearest (x, y) by
// the larger axis distance, -1 when none is within reach.
func cmpNearest(shown []cmpInteraction, x, y, reach float64) int {
	best, bestDistance := -1, math.Inf(1)
	for i, row := range shown {
		d := math.Max(math.Abs(float64(row.X)+5-x), math.Abs(float64(row.Y)+5-y))
		if d < bestDistance {
			best, bestDistance = i, d
		}
	}
	if best >= 0 && bestDistance <= reach {
		return best
	}
	return -1
}

// cmpPickReach is the web's pick distance: the merge range, at least one
// cell, and at least 4 pixels of the map (span nt shown on side pixels).
func cmpPickReach(radius int, span, side float64) float64 {
	reach := math.Max(float64(radius), 10)
	if side > 0 {
		reach = math.Max(reach, 4*span/side)
	}
	return reach
}

func (p *comparePage) interactionTable(c *ui.Context, shown []cmpInteraction, ctx cmpContext) {
	t := c.Theme()
	if len(shown) == 0 {
		ui.Text(c, "No significant interaction in this direction.").TextColor(t.TextMuted)
		return
	}
	// the web card's columns (deseq-card.js renderResult)
	cols := []ui.TableColumn{{Title: "Higher in", Width: 110}, {Title: "Arm 1 (x, nt)", Width: 112}, {Title: "Arm 2 (y, nt)", Width: 112},
		{Title: "log2 " + ctx.LabelA + "/" + ctx.LabelB, Width: 110, Align: ui.End}, {Title: "padj", Width: 72, Align: ui.End},
		{Title: "Mean count (normalised)", Align: ui.End}}
	p.table.Selected = &p.row
	p.table.Key = func(i int) any { return fmt.Sprintf("%d_%d_%s", shown[i].X, shown[i].Y, shown[i].Direction) }
	ui.Table(c.Key("cmp-table-"+p.shownKey), &p.table, cols, len(shown), func(i, col int) {
		row := shown[i]
		up := row.Direction == "up"
		switch col {
		case 0:
			label, color := ctx.LabelB, cmpBlue
			if up {
				label, color = ctx.LabelA, cmpRed
			}
			text := ui.Text(c, label).SingleLine()
			if i != p.row { // the chosen row's text takes the accent's text color
				text.TextColor(color)
			}
		case 1:
			ui.Text(c, FormatNumber(int64(row.X))+"–"+FormatNumber(int64(row.X+9))).SingleLine()
		case 2:
			ui.Text(c, FormatNumber(int64(row.Y))+"–"+FormatNumber(int64(row.Y+9))).SingleLine()
		case 3:
			ui.Text(c, cmpFixed(row.LFC, 2))
		case 4:
			ui.Text(c, cmpPadjText(row.Padj))
		case 5:
			ui.Text(c, cmpFixed(row.BaseMean, 1))
		}
	}).Height(p.mapSide() - 40).Label("Merged interactions")
}

func cmpFloat(v *float64, format string) string {
	if v == nil || math.IsNaN(*v) {
		return "—"
	}
	return fmt.Sprintf(format, *v)
}

// actionRow: Locate, Region and Fold A & B for the chosen row.
func (p *comparePage) actionRow(c *ui.Context, target *cmpTarget, ctx cmpContext) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		none := target == nil
		if ui.Button(c, "Locate").Disabled(none).Tooltip("Show this cell on the contact map of the group where it is stronger").Clicked() && !none {
			p.locate(*target, ctx, "contact-map")
		}
		if ui.Button(c, "Region").Disabled(none).Tooltip("Open both arms in Region").Clicked() && !none {
			p.locate(*target, ctx, "region")
		}
		// a window fold is on one RNA: not for an inter-RNA cell
		pair := ctx.Partner != ""
		tip := "Fold the window spanning both arms with A's and with B's evidence"
		if pair {
			tip = "The arms are on two RNAs; a window fold needs both on one"
		}
		if ui.Button(c, "Fold A & B").Disabled(none || pair).Tooltip(tip).Clicked() && !none && !pair {
			p.planFold(*target, ctx)
		}
		if none {
			ui.Text(c, "Choose a row or a cell on the map.").FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Text(c, fmt.Sprintf("%d–%d × %d–%d", target.X, target.X+ctx.Bin-1, target.Y, target.Y+ctx.Bin-1)).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

func (p *comparePage) foldDialog(c *ui.Context) {
	if !p.foldOpen {
		return
	}
	t := c.Theme()
	plan := p.fold
	ui.Modal(c.Key("cmp-fold"), &p.foldOpen, func() {
		ui.Column(c).Gap(12).Width(420).Children(func() {
			ui.Text(c, fmt.Sprintf("Fold %s:%d–%d", plan.ctx.GeneA, plan.start, plan.end)).FontSize(16).Bold()
			ui.Text(c, fmt.Sprintf("Two window folds (pseudoknots allowed) of %d nt: one with %s evidence, one with %s evidence. They appear on the Structure page.",
				plan.end-plan.start+1, plan.ctx.LabelA, plan.ctx.LabelB)).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					p.foldOpen = false
				}
				if ui.PrimaryButton(c, "Start both").Clicked() {
					p.startFold()
				}
			})
		})
	})
}

// ---- quick view

// quickRows are the quick view's bins, strongest effect first.
func (p *comparePage) quickRows() []cmpQuickRow {
	if p.quick.data == nil {
		return nil
	}
	return p.quick.rows
}

// cmpQuickRows are d's bins, strongest effect first (then most hybrids).
func cmpQuickRows(d *cmpQuickResult) []cmpQuickRow {
	at := func(values []float64, i int) float64 {
		if i < len(values) {
			return values[i]
		}
		return 0
	}
	var rows []cmpQuickRow
	for i := range d.X {
		if i >= len(d.Y) {
			break
		}
		row := cmpQuickRow{X: d.X[i], Y: d.Y[i], A: at(d.A, i), B: at(d.B, i), RawA: at(d.ACount, i), RawB: at(d.BCount, i)}
		if i < len(d.Effect) && d.Effect[i] != nil {
			r := *d.Effect[i]
			row.Ratio = &r
		} else if i < len(d.Log2) && d.Log2[i] != nil { // a result from before the effect field
			r := -*d.Log2[i]
			row.Ratio = &r
		}
		rows = append(rows, row)
	}
	abs := func(r *float64) float64 {
		if r == nil {
			return 0
		}
		return math.Abs(*r)
	}
	slices.SortStableFunc(rows, func(a, b cmpQuickRow) int {
		if c := cmp.Compare(abs(b.Ratio), abs(a.Ratio)); c != 0 {
			return c
		}
		return cmp.Compare(b.RawA+b.RawB, a.RawA+a.RawB)
	})
	return rows
}

// quickEffectCap is the web's effect cap: the 95th percentile of |effect|,
// at least 0.25.
func quickEffectCap(rows []cmpQuickRow) float64 {
	values := make([]float64, 0, len(rows))
	for _, r := range rows {
		if r.Ratio != nil {
			values = append(values, math.Abs(*r.Ratio))
		}
	}
	sort.Float64s(values)
	return math.Max(0.25, quantile(values, 0.95))
}

func (p *comparePage) quickContext() cmpContext {
	q := &p.quick
	bin := 10
	if q.data != nil && q.data.BinSize > 0 {
		bin = q.data.BinSize
	}
	ctx := cmpContext{KeyA: q.keyA, KeyB: q.keyB, IDsA: q.idsA, IDsB: q.idsB, GeneA: q.gene, GeneB: q.gene,
		LabelA: nameOr(q.labelA, p.a.SelectionLabel(q.keyA)), LabelB: nameOr(q.labelB, p.a.SelectionLabel(q.keyB)), Bin: bin}
	if q.gene2 != "" && q.gene2 != q.gene {
		ctx.Partner = q.gene2
	}
	return ctx
}

func (p *comparePage) quickView(c *ui.Context) {
	t := c.Theme()
	q := &p.quick
	Card(c, "", func() {
		if q.busy {
			ui.Text(c, "Binning both groups…").TextColor(t.TextMuted)
			return
		}
		if q.err != "" {
			ui.Text(c, "The quick comparison is unavailable: "+q.err).TextColor(t.Danger)
			if ui.Button(c, "Back to tests").Clicked() {
				p.mode = cmpModeTest
			}
			return
		}
		if q.data == nil {
			return
		}
		d := q.data
		ctx := p.quickContext()
		rows := p.quickRows()
		ui.Row(c).Gap(8).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "Quick view · "+ctx.LabelA+" vs "+ctx.LabelB).FontSize(18).Bold()
				genes := q.gene
				if ctx.Partner != "" {
					genes += " × " + ctx.Partner
				}
				meta := fmt.Sprintf("%s · %d-nt bins · %s cells", genes, d.BinSize, FormatNumber(int64(len(d.X))))
				if d.LibraryA != nil && d.LibraryB != nil {
					meta += " · libraries " + FormatNumber(int64(*d.LibraryA)) + " / " + FormatNumber(int64(*d.LibraryB))
				}
				ui.Text(c, meta+" · "+quickValueNote(d)+" · no statistics; use the differential test for padj").
					FontSize(12).TextColor(t.TextMuted)
			})
			if ui.Button(c, "Export TSV…").Tooltip("Save every bin as a tab-separated file").Disabled(len(rows) == 0).Clicked() && len(rows) > 0 {
				p.exportQuickTSV()
			}
			if ui.Button(c, "Back to tests").Clicked() {
				p.mode = cmpModeTest
			}
		})
		if heatKey := fmt.Sprint(q.rowsKey, t.Dark); q.heatKey != heatKey {
			q.heatKey = heatKey
			length := d.Length
			if length <= 0 {
				for i := range d.X {
					length = cmpMax(length, cmpMax(d.X[i], d.Y[i]))
				}
				length += d.BinSize
			}
			length2 := length // y is on RNA Y for an inter-RNA map
			if ctx.Partner != "" && d.Length2 > 0 {
				length2 = d.Length2
			}
			// The cells come in the background; until then the map is empty.
			q.heat.SetCells(nil, float64(length), float64(length2))
			bin, dark, limit := d.BinSize, t.Dark, q.cap
			p.a.Go(func() func() {
				cells := cmpQuickCells(rows, bin, dark, limit)
				return func() {
					if q.heatKey == heatKey {
						q.heat.SetCells(cells, float64(length), float64(length2))
					}
				}
			})
		}
		if q.row >= 0 && q.row < len(rows) {
			r := rows[q.row]
			q.heat.Highlight = widgets.Cell{X: float64(r.X), Y: float64(r.Y), W: float64(d.BinSize), H: float64(d.BinSize)}
		} else {
			q.heat.Highlight = widgets.Cell{}
		}
		ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				ev := q.heat.Show(c.Key("quick-heat"), p.mapSide(), "Normalised contact difference, violet: higher in A, teal: higher in B")
				if ev.Clicked >= 0 {
					cell := q.heat.Cells()[ev.Clicked]
					for i, r := range rows {
						if float64(r.X) == cell.X && float64(r.Y) == cell.Y {
							q.row = i
						}
					}
					q.heat.Picked = -1
				}
				effectLegend(c, ctx.LabelA, ctx.LabelB, q.cap)
			})
			ui.Column(c).Gap(8).Grow(1).MinWidth(320).Children(func() {
				ui.Text(c, "Bins ("+FormatNumber(int64(len(rows)))+")").Bold()
				if len(rows) == 0 {
					ui.Text(c, "No bin has hybrids in either group.").TextColor(t.TextMuted)
				} else {
					unit := "per M"
					if d.Normalisation == "none" {
						unit = "mean"
					}
					cols := []ui.TableColumn{{Title: "Arm 1 (nt)", Width: 112}, {Title: "Arm 2 (nt)", Width: 112},
						{Title: "A " + unit, Width: 76, Align: ui.End}, {Title: "B " + unit, Width: 76, Align: ui.End}, {Title: "log2 A/B", Align: ui.End}}
					q.table.Selected = &q.row
					q.table.Key = func(i int) any { return [2]int{rows[i].X, rows[i].Y} }
					ui.Table(c.Key("quick-table-"+q.rowsKey), &q.table, cols, len(rows), func(i, col int) {
						r := rows[i]
						switch col {
						case 0:
							ui.Text(c, fmt.Sprintf("%d–%d", r.X, r.X+d.BinSize-1)).SingleLine()
						case 1:
							ui.Text(c, fmt.Sprintf("%d–%d", r.Y, r.Y+d.BinSize-1)).SingleLine()
						case 2:
							ui.Text(c, trimFloat(r.A)).Tooltip(FormatNumber(int64(r.RawA)) + " raw")
						case 3:
							ui.Text(c, trimFloat(r.B)).Tooltip(FormatNumber(int64(r.RawB)) + " raw")
						case 4:
							color := effectColor(-1, t.Dark)
							if r.Ratio != nil && *r.Ratio > 0 {
								color = effectColor(1, t.Dark)
							}
							text := ui.Text(c, cmpFloat(r.Ratio, "%.2f"))
							if i != q.row {
								text.TextColor(color)
							}
						}
					}).Height(p.mapSide() - 40).Label("Quick view bins")
				}
				var target *cmpTarget
				if q.row >= 0 && q.row < len(rows) {
					target = &cmpTarget{X: rows[q.row].X, Y: rows[q.row].Y, HigherInB: rows[q.row].Ratio != nil && *rows[q.row].Ratio < 0}
				}
				p.actionRow(c, target, ctx)
			})
		})
	})
}

// quickValueNote says what the quick view's values are, as the web's
// comparisonValueDescription and its effect's pseudocount.
func quickValueNote(d *cmpQuickResult) string {
	unit := "HYB records"
	if d.Measure == "support" {
		unit = "cluster-support units"
	}
	pseudo := trimFloat(d.Pseudocount)
	if d.Normalisation == "none" {
		return fmt.Sprintf("raw mean %s of each group's datasets, log2((A + %s) / (B + %s))", unit, pseudo, pseudo)
	}
	return fmt.Sprintf("mean per million %s of each group's datasets, log2((A + %s) / (B + %s))", unit, pseudo, pseudo)
}

// cmpQuickCells colours bins by the effect over its cap, as the web's
// effect map: violet higher in A, teal higher in B.
func cmpQuickCells(rows []cmpQuickRow, bin int, dark bool, limit float64) []widgets.Cell {
	out := make([]widgets.Cell, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if r.Ratio == nil {
			continue
		}
		out = append(out, widgets.Cell{X: float64(r.X), Y: float64(r.Y), W: float64(bin), H: float64(bin),
			Color: effectColor(*r.Ratio/limit, dark)})
	}
	return out
}

// effectLegend is the quick view's scale: teal (B) through the surface to violet (A).
func effectLegend(c *ui.Context, labelA, labelB string, limit float64) {
	t := c.Theme()
	ui.Column(c).Gap(4).Width(260).Children(func() {
		ui.Box(c).FillWidth().Height(10).Radius(3).Draw(func(pt *ui.Painter, r ui.Rect) {
			steps := 48
			for i := range steps {
				w := r.W / float32(steps)
				v := -1 + 2*float64(i)/float64(steps-1)
				pt.Fill(ui.Rect{X: r.X + float32(i)*w, Y: r.Y, W: w + 0.5, H: r.H}, effectColor(v, t.Dark), 0)
			}
		})
		ui.Row(c).Children(func() {
			ui.Text(c, fmt.Sprintf("≤ −%s · higher in %s", trimFloat(limit), labelB)).FontSize(11).TextColor(t.TextMuted).Grow(1)
			ui.Text(c, fmt.Sprintf("higher in %s · ≥ %s", labelA, trimFloat(limit))).FontSize(11).TextColor(t.TextMuted)
		})
	})
}

func cmpMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func cmpMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// mapSide is the side of the Compare maps: about half the page width and
// two thirds of its height.
func (p *comparePage) mapSide() float32 {
	w, h := p.a.ContentSize()
	return clampF(min(w*0.44, h*0.62), 340, 820)
}
