package views

// ⑤ Structure, the New fold task: what to fold (input), how (advanced
// settings, folded away but always summarised), a check of the fold before
// it runs (what, evidence, time, memory and its limits, with every reason
// it cannot run yet), its status in place (queued, running, done, failed
// with an explanation and a next step, cancelled, interrupted), and the
// results with their numbers and units. The viewer is in structure.go.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// The web RNA Structure page's CPLfold defaults (web/analysis-data.js).
const (
	stDefaultAlpha  = 0.5
	stDefaultBeam   = 20
	stDefaultPhase1 = 3
	stDefaultDelta  = 5
	stDefaultBeta   = 0
	stDefaultModel  = "DP09"
)

// stModels are CPLfold's energy models, labelled as on the web page.
var (
	stModels      = []string{"DP09", "DP03", "CC06", "CC09", "RE"}
	stModelLabels = []string{"DP09", "DP03", "CC06", "CC09", "RE (Rivas–Eddy)"}
)

func (p *structurePage) resetFoldSettings() {
	p.alpha, p.beam, p.phase1, p.delta, p.beta, p.model =
		stDefaultAlpha, stDefaultBeam, stDefaultPhase1, stDefaultDelta, stDefaultBeta, stDefaultModel
	if p.engine == "" {
		p.engine, p.temperature, p.minLoop = engineCPLfold, stDefaultTemp, stDefaultLoop
		p.stems, p.randomFolds, p.seed = stDefaultStems, 0, stDefaultSeed
	}
}

func (p *structurePage) foldSettingsChanged() bool {
	return p.alpha != stDefaultAlpha || int(p.beam) != stDefaultBeam || int(p.phase1) != stDefaultPhase1 ||
		p.delta != stDefaultDelta || p.beta != stDefaultBeta || p.model != stDefaultModel
}

// foldPlan is the fold the form describes, checked.
type foldPlan struct {
	gene     string
	length   int
	evidence string // its label
	ids      []string
	windows  []core.Window
	lengths  []int
	pk       bool
	// problems are why it cannot run yet, each with what to do.
	problems []string
	// limits are what the result will and will not show.
	limits []string
	req    core.TaskStartRequest
	memMB  uint64
	what   string // "whole RNA" | "2 windows"
	// source names a pasted or HYB record sequence for the task title,
	// "pasted sequence, 90 nt"; "" for a reference region.
	source string
}

// plan checks the form. Memory is checked against the task budget once it
// is known (askWorkers reads it with the CPU budget).
func (p *structurePage) plan() foldPlan {
	a := p.a
	if p.src.kind != stSourceReference {
		return p.sequencePlan()
	}
	pl := foldPlan{gene: p.gene, length: a.Project.GeneLength(p.gene), ids: a.SelectionIDs(p.evidence),
		evidence: a.SelectionLabel(p.evidence), what: "whole RNA"}
	switch {
	case pl.gene == "" || pl.length == 0:
		pl.problems = append(pl.problems, "Choose the RNA to fold.")
	case len(a.SelectionOptions()) == 0:
		pl.problems = append(pl.problems, "No sample has been analysed yet: run the pipeline on the Data page first, then come back.")
	case len(pl.ids) == 0:
		pl.problems = append(pl.problems, "Choose the samples whose chimeric reads guide the fold.")
	default:
		if msg := p.evidenceMismatch(); msg != "" {
			pl.problems = append(pl.problems, msg)
		}
	}
	if p.engine == engineCPLcofold {
		p.cofoldPlan(&pl)
		p.checkBudget(&pl)
		return pl
	}
	params := map[string]any{"alpha": p.alpha, "beam": int(p.beam), "max_phase1": int(p.phase1),
		"energy_delta": p.delta, "beta": p.beta, "energy_model": p.model, "allow_pseudoknot": false}
	if p.seqOnly && p.engine == engineCPLfold {
		params["evidence"] = "none"
	}
	mode := p.mode
	pl.req = core.TaskStartRequest{Kind: "structure", SampleIDs: pl.ids, GeneID: &pl.gene, Mode: &mode, Parameters: params}
	workers := uint32(max(int(p.workers), 1))
	pl.req.Workers = &workers
	if p.mode == "windows" {
		windows, errs := stParseWindows(p.windows, pl.length)
		for _, e := range errs {
			pl.problems = append(pl.problems, e+" (windows are start-end, at least 20 nt).")
		}
		if len(errs) == 0 && len(windows) == 0 && pl.length > 0 {
			pl.problems = append(pl.problems, fmt.Sprintf("Add at least one window, one per line as start-end within 1-%s.", FormatNumber(int64(pl.length))))
		}
		pl.windows, pl.pk = windows, p.pseudoknot
		pl.req.Windows = windows
		params["allow_pseudoknot"] = p.pseudoknot
		for _, w := range windows {
			pl.lengths = append(pl.lengths, int(w.End-w.Start+1))
		}
		pl.what = fmt.Sprintf("%d window%s", len(windows), plural(len(windows)))
		pl.limits = append(pl.limits, "Each window is folded on its own: no pair joins two windows or leaves a window.")
		if !p.pseudoknot {
			pl.limits = append(pl.limits, "Pseudoknots are off: crossing pairs are not predicted.")
		}
	} else if pl.length > 0 {
		pl.lengths = []int{pl.length}
		pl.limits = append(pl.limits, "Whole-RNA folds cannot contain pseudoknots; fold windows to allow them.")
	}
	if p.seqOnly && p.engine == engineCPLfold {
		pl.limits = append(pl.limits, "Sequence only: the chimeric reads are not used; the samples only give the reference.")
	} else {
		pl.limits = append(pl.limits, "Only chimeras with both arms inside the folded region are used as evidence.")
	}
	if p.engine != engineCPLfold {
		p.viennaPlan(&pl)
	} else if len(pl.lengths) > 0 {
		pl.memMB = StructureMemClaim(pl.req, pl.length, pl.pk)
		pl.req.MemMB = &pl.memMB
	}
	p.checkBudget(&pl)
	return pl
}

// checkBudget: the fold must fit the memory tasks may use.
func (p *structurePage) checkBudget(pl *foldPlan) {
	if len(pl.lengths) > 0 && p.budgetMB > 0 && pl.memMB > p.budgetMB {
		pl.problems = append(pl.problems, fmt.Sprintf("This fold needs about %.1f GB of memory but tasks may use %.1f GB. "+
			"Fold shorter windows, fewer windows at once (Advanced), or raise Memory in Preferences.",
			float64(pl.memMB)/1024, float64(p.budgetMB)/1024))
	}
}

// stCofoldMinNT is the shortest CPLcofold region (desktop/runtime/structure.py MIN_WINDOW_NT).
const stCofoldMinNT = 10

// stCofoldMaxBeam is CPLcofold's largest beam (the shared module's bound).
const stCofoldMaxBeam = 200

// stParseRegion reads one "start-end" region of an RNA of length nt.
func stParseRegion(text string, length int, label string) (core.Region, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return core.Region{}, label + ": enter it as start-end, for example 1-45."
	}
	m := stWindowLine.FindStringSubmatch(text)
	if m == nil {
		return core.Region{}, label + ": " + text + " is not start-end, for example 1-45."
	}
	start, _ := strconv.Atoi(m[1])
	end, _ := strconv.Atoi(m[2])
	if start > end {
		start, end = end, start
	}
	switch {
	case start < 1 || (length > 0 && end > length):
		return core.Region{}, fmt.Sprintf("%s: %d-%d is outside 1-%s.", label, start, end, FormatNumber(int64(length)))
	case end-start+1 < stCofoldMinNT:
		return core.Region{}, fmt.Sprintf("%s: at least %d nt.", label, stCofoldMinNT)
	}
	return core.Region{Start: uint64(start), End: uint64(end)}, ""
}

// stReferenceID is the project reference holding gene, "" if unknown.
func stReferenceID(pr *model.Project, gene string) string {
	for _, r := range pr.References {
		for _, s := range r.Sequences {
			if s.Name == gene {
				return r.ID
			}
		}
	}
	return ""
}

// twoRegions reads the form's two regions (region 2 on gene2, else on the
// RNA of region 1); ok when both are valid. Their problems go to the plan.
func (p *structurePage) twoRegions(pl *foldPlan) (r1, r2 core.Region, ok bool) {
	a := p.a
	gene2 := nameOr(p.gene2, p.gene)
	r1, err1 := stParseRegion(p.region1, pl.length, "Region 1")
	r2, err2 := stParseRegion(p.region2, a.Project.GeneLength(gene2), "Region 2")
	for _, e := range []string{err1, err2} {
		if e != "" && pl.length > 0 {
			pl.problems = append(pl.problems, e)
		}
	}
	if id1, id2 := stReferenceID(a.Project, p.gene), stReferenceID(a.Project, gene2); id1 != "" && id2 != "" && id1 != id2 {
		pl.problems = append(pl.problems, "Both regions must be on RNAs of one reference, the reference of the samples.")
	}
	r1.GeneID, r2.GeneID = p.gene, gene2
	return r1, r2, err1 == "" && err2 == ""
}

// cofoldPlan: two regions folded as two strands with CPLcofold, the web's
// two-region CPLfold layout (one RNA or two RNAs of one reference).
func (p *structurePage) cofoldPlan(pl *foldPlan) {
	r1, r2, ok := p.twoRegions(pl)
	params := p.cofoldParameters(pl)
	mode, engine, one := "cofold", engineCPLcofold, uint32(1)
	pl.req = core.TaskStartRequest{Kind: "structure", SampleIDs: pl.ids, GeneID: &pl.gene, Mode: &mode, Engine: &engine,
		Regions: []core.Region{r1, r2}, Parameters: params, Workers: &one}
	pl.what, pl.pk, pl.windows, pl.lengths = "2 regions as two strands", p.pseudoknot, nil, nil
	if ok {
		pl.lengths = []int{int(r1.End - r1.Start + 1), int(r2.End - r2.Start + 1)}
		pl.memMB = core.StructureClaimMB(pl.lengths[0]+pl.lengths[1], p.pseudoknot, 1)
		pl.req.MemMB = &pl.memMB
	}
	pl.limits = []string{"Region 1 is strand A and region 2 strand B, each 5′→3′; the sequence between them is not folded."}
	if p.seqOnly {
		pl.limits = append(pl.limits, "Sequence only: the chimeric reads are not used; the samples only give the reference.")
	} else {
		pl.limits = append(pl.limits, "Chimeras with one arm in each region join the strands; chimeras with both arms in one region are evidence within it.")
	}
	if !p.pseudoknot {
		pl.limits = append(pl.limits, "Pseudoknots are off: crossing pairs are not predicted.")
	}
}

// cofoldParameters are CPLcofold's settings, checked against the energy
// models and beam it supports.
func (p *structurePage) cofoldParameters(pl *foldPlan) map[string]any {
	if p.model != "DP03" && p.model != "DP09" {
		pl.problems = append(pl.problems, "CPLcofold uses the DP03 or DP09 energy model: choose one under Advanced settings.")
	}
	if int(p.beam) > stCofoldMaxBeam {
		pl.problems = append(pl.problems, fmt.Sprintf("CPLcofold's beam size is at most %d: lower it under Advanced settings.", stCofoldMaxBeam))
	}
	params := map[string]any{"alpha": p.alpha, "beam": int(p.beam), "max_phase1": int(p.phase1),
		"energy_delta": p.delta, "beta": p.beta, "energy_model": p.model, "allow_pseudoknot": p.pseudoknot}
	if p.seqOnly {
		params["evidence"] = "none"
	}
	return params
}

// ---- the sequence source

// Sequence sources, as the web's RNA Structure page.
const (
	stSourceReference = "" // a region of a reference RNA (the default)
	stSourceRecord    = "record"
	stSourcePaste     = "paste"
	// stRecordChoices is how many HYB records the source offers, as the web.
	stRecordChoices = 500
	// stViennaMaxNT is ViennaRNA MFE's longest sequence (desktop/runtime/vienna.py MAX_VIENNA_NT).
	stViennaMaxNT = 30_000
)

var (
	stSourceLabels = []string{"Reference region", "HYB record sequence", "Paste sequence"}
	stSourceKinds  = []string{stSourceReference, stSourceRecord, stSourcePaste}
)

// stSource is what a fold reads its sequence from: a region of a reference
// RNA (the form's RNA, windows or regions), the sequence of one HYB record
// of the samples, or pasted sequences (RNA A and RNA B for CPLcofold).
type stSource struct {
	kind              string // one of stSourceKinds
	pasted, pastedTwo string
	// record is the chosen HYB record; records are the ones offered, read
	// for recordsKey (the samples).
	record  *hybridRow
	records []hybridRow
	// pending is a record sent by Explore: chosen once the records are read,
	// and offered first when it is not among them.
	pending    *hybridRow
	recordsKey string
	recordsErr string
	loading    bool
}

// stCleanSequence reads a pasted sequence as the web does
// (normaliseFoldSequence): FASTA header lines and whitespace dropped, upper
// case, T read as U. bad lists any other characters, once each.
func stCleanSequence(text string) (seq string, bad []string) {
	var b strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			continue
		}
		for _, r := range line {
			if unicode.IsSpace(r) {
				continue
			}
			if r = unicode.ToUpper(r); r == 'T' {
				r = 'U'
			}
			b.WriteRune(r)
			if !strings.ContainsRune("ACGU", r) && !slices.Contains(bad, string(r)) {
				bad = append(bad, string(r))
			}
		}
	}
	return b.String(), bad
}

// stStrand is one pasted strand, checked; "" with its problem in pl when
// it cannot be folded. name says which strand ("RNA A").
func stStrand(text, name string, pl *foldPlan) string {
	seq, bad := stCleanSequence(text)
	switch {
	case len(bad) > 0:
		pl.problems = append(pl.problems, fmt.Sprintf("%s: only A, C, G and U are accepted (T is read as U); remove %s.",
			name, strings.Join(bad, ", ")))
	case len(seq) < stCofoldMinNT:
		pl.problems = append(pl.problems, fmt.Sprintf("%s: at least %d nt.", name, stCofoldMinNT))
	default:
		return seq
	}
	return ""
}

// stRecordName is how a HYB record is named: its read name (column 1 of
// the .hyb line), else its row.
func stRecordName(r hybridRow) string {
	if name := strings.Join(strings.Fields(r.Name), "_"); name != "" {
		return name
	}
	return fmt.Sprintf("#%d", r.ID)
}

// stHybridLabel is a record in the choice list, as the web's
// "id · RNA one–RNA two", with its arms and reads.
func stHybridLabel(r hybridRow) string {
	return fmt.Sprintf("%s · %s %d–%d / %s %d–%d · %s read%s", stRecordName(r), r.Gene1, r.Start1, r.End1,
		r.Gene2, r.Start2, r.End2, FormatNumber(int64(r.Reads)), plural(r.Reads))
}

// setSource switches the sequence source. A pasted or record sequence is
// folded whole, so "What to fold" goes back to the whole RNA.
func (p *structurePage) setSource(kind string) {
	p.src.kind = kind
	if kind != stSourceReference {
		p.mode = "genome"
	}
}

// loadRecords reads the HYB records offered for the samples (most
// supported first), with their sequences, from the analysis index.
func (p *structurePage) loadRecords() {
	a := p.a
	ids := a.SelectionIDs(p.evidence)
	key := a.Project.Path + "\x00" + strings.Join(ids, "\x00")
	if len(ids) == 0 || key == p.src.recordsKey {
		return
	}
	// other samples, other records: the chosen one is chosen again
	p.src.recordsKey, p.src.records, p.src.recordsErr, p.src.loading, p.src.record = key, nil, "", true, nil
	path := a.Project.Path
	a.Go(func() func() {
		raw, err := a.B.Query(path, "analysis_hybrids", core.Obj{"sample_ids": ids, "filters": core.Obj{},
			"limit": stRecordChoices, "offset": 0, "order": "support", "with_sequence": true})
		var rows []hybridRow
		if err == nil {
			got, _ := raw.(map[string]any)
			err = model.Decode(got["rows"], &rows)
		}
		return func() {
			if p.src.recordsKey != key {
				return
			}
			p.src.loading = false
			if err != nil {
				p.src.recordsErr = err.Error()
				return
			}
			p.src.records = rows
			if r := p.src.pending; r != nil {
				p.src.pending = nil
				found := false
				for i := range rows {
					if rows[i].ID == r.ID && rows[i].Name == r.Name {
						p.src.record, found = &rows[i], true
						break
					}
				}
				if !found {
					p.src.records = append([]hybridRow{*r}, rows...)
					p.src.record = &p.src.records[0]
				}
			}
		}
	})
}

// sequencePlan: a pasted or HYB record sequence folded whole, or two pasted
// strands with CPLcofold. As the web, chimeras guide only the fold of a
// reference region: the runtime folds a sequence found exactly once in the
// samples' reference as that region, and any other from the sequence alone.
func (p *structurePage) sequencePlan() foldPlan {
	a := p.a
	pl := foldPlan{ids: a.SelectionIDs(p.evidence), evidence: a.SelectionLabel(p.evidence)}
	switch {
	case len(a.SelectionOptions()) == 0:
		pl.problems = append(pl.problems, "No sample has been analysed yet: run the pipeline on the Data page first, then come back.")
	case len(pl.ids) == 0:
		pl.problems = append(pl.problems, "Choose the samples: their reference is searched for the sequence.")
	}
	cofold, record := p.engine == engineCPLcofold, p.src.kind == stSourceRecord
	var strands []string
	switch {
	case record && cofold:
		pl.problems = append(pl.problems, "A HYB record is one chimeric read, not two strands: paste RNA A and RNA B for CPLcofold, or choose another engine.")
	case record:
		r := p.src.record
		switch {
		case r == nil:
			pl.problems = append(pl.problems, "Choose the HYB record whose sequence to fold.")
		case r.Sequence == "":
			pl.problems = append(pl.problems, "This record's sequence cannot be read: its .hyb file changed after the index was built. "+
				"Rebuild the index on the Data page, or paste the sequence.")
		default:
			if s := stStrand(r.Sequence, "The record's sequence", &pl); s != "" {
				strands = []string{s}
			}
			pl.gene, pl.source = "record_"+stRecordName(*r), "HYB record "+stRecordName(*r)
		}
	case cofold:
		one, two := p.src.pasted, p.src.pastedTwo
		if strings.Contains(one, "&") && strings.TrimSpace(two) == "" {
			one, two, _ = strings.Cut(one, "&") // A&B in the RNA A box
		}
		if strings.TrimSpace(one) == "" || strings.TrimSpace(two) == "" {
			pl.problems = append(pl.problems, "Paste RNA A and RNA B, or A&B in the RNA A box.")
		} else if s1, s2 := stStrand(one, "RNA A", &pl), stStrand(two, "RNA B", &pl); s1 != "" && s2 != "" {
			strands = []string{s1, s2}
		}
		pl.gene, pl.source = "pasted", "pasted RNA A + RNA B"
	default:
		if strings.TrimSpace(p.src.pasted) == "" {
			pl.problems = append(pl.problems, "Paste the RNA sequence to fold (A, C, G, U; T is read as U).")
		} else if s := stStrand(p.src.pasted, "The pasted sequence", &pl); s != "" {
			strands = []string{s}
		}
		pl.gene, pl.source = "pasted_sequence", "pasted sequence"
	}
	for _, s := range strands {
		pl.lengths = append(pl.lengths, len(s))
		pl.length += len(s)
	}
	if pl.length > 0 {
		pl.source += ", " + pl.span()
	}
	source, mode, one := nameOr(p.src.kind, stSourcePaste), "genome", uint32(1)
	pl.req = core.TaskStartRequest{Kind: "structure", SampleIDs: pl.ids, GeneID: &pl.gene, Mode: &mode, Source: &source, Workers: &one}
	if len(strands) > 0 {
		pl.req.Sequence = &strands[0]
	}
	if len(strands) == 2 {
		pl.req.SequenceTwo = &strands[1]
	}
	pl.what = "the pasted sequence"
	if record {
		pl.what = "the record's sequence"
	}
	found := "Chimeras guide the fold only if the sequence occurs exactly once in the samples' reference: it is then folded as that region. " +
		"Otherwise it is folded from the sequence alone."
	switch p.engine {
	case engineCPLcofold:
		mode = "cofold"
		engine := engineCPLcofold
		pl.req.Engine, pl.req.Parameters = &engine, p.cofoldParameters(&pl)
		pl.what, pl.pk = "2 pasted strands", p.pseudoknot
		if len(strands) == 2 {
			pl.memMB = core.StructureClaimMB(pl.length, p.pseudoknot, 1)
			pl.req.MemMB = &pl.memMB
		}
		pl.limits = []string{"RNA A is strand A and RNA B strand B, each 5′→3′."}
		if p.seqOnly {
			pl.limits = append(pl.limits, "Sequence only: the chimeric reads are not used.")
		} else {
			pl.limits = append(pl.limits, "Chimeras guide the fold only if both strands occur exactly once in the samples' reference: "+
				"they are then folded as those regions. Otherwise the strands are folded from the sequence alone.")
		}
		if !p.pseudoknot {
			pl.limits = append(pl.limits, "Pseudoknots are off: crossing pairs are not predicted.")
		}
	case engineCPLfold:
		params := map[string]any{"alpha": p.alpha, "beam": int(p.beam), "max_phase1": int(p.phase1),
			"energy_delta": p.delta, "beta": p.beta, "energy_model": p.model, "allow_pseudoknot": false}
		if p.seqOnly {
			params["evidence"] = "none"
		}
		pl.req.Parameters = params
		if len(strands) > 0 {
			pl.memMB = StructureMemClaim(pl.req, pl.length, false)
			pl.req.MemMB = &pl.memMB
		}
		pl.limits = []string{"Folded whole: no pseudoknots."}
		if p.seqOnly {
			pl.limits = append(pl.limits, "Sequence only: the chimeric reads are not used.")
		} else {
			pl.limits = append(pl.limits, found)
		}
	default: // ViennaRNA MFE, COMRADES(ViennaRNA)
		p.viennaPlan(&pl)
		if p.engine == engineComrades {
			pl.limits = append([]string{"COMRADES(ViennaRNA) needs chimeras: the sequence must occur exactly once in the samples' " +
				"reference, and is then folded as that region; otherwise the fold stops and says so."}, pl.limits...)
		} else {
			pl.limits = []string{"ViennaRNA MFE folds the sequence alone; hard pairs are positions on it.",
				"No pseudoknots: ViennaRNA returns a nested structure."}
			if pl.length > stViennaMaxNT {
				pl.problems = append(pl.problems, fmt.Sprintf("ViennaRNA MFE folds at most %s nt; paste a shorter sequence.",
					FormatNumber(stViennaMaxNT)))
			}
		}
	}
	if record {
		pl.limits = append(pl.limits, "A HYB record's sequence is one chimeric read, both arms joined: not necessarily a whole RNA or a region of it.")
	}
	if pl.evidence != "" && p.engine != engineVienna && !(p.seqOnly && p.engine != engineComrades) {
		pl.evidence += ", used only if the sequence occurs in their reference"
	}
	p.checkBudget(&pl)
	return pl
}

// sourceField: where the sequence comes from, and the record or pasted
// sequences when it is not a reference region.
func (p *structurePage) sourceField(c *ui.Context, pl foldPlan) {
	t := c.Theme()
	cofold := p.engine == engineCPLcofold
	labels, kinds := stSourceLabels, stSourceKinds
	if cofold {
		// a record is one chimeric read, not two strands
		labels, kinds = []string{labels[0], labels[2]}, []string{kinds[0], kinds[2]}
	}
	current := labels[0]
	for i, k := range kinds {
		if k == p.src.kind {
			current = labels[i]
		}
	}
	label := "Sequence source"
	if p.src.kind != stSourceReference {
		label = "1  Sequence source"
	}
	Field(c, label, func() {
		if ui.Select(c.Key("st-source"), &current, labels).FillWidth().Label("Sequence source").Changed() {
			for i, l := range labels {
				if l == current {
					p.setSource(kinds[i])
				}
			}
		}
		switch p.src.kind {
		case stSourceRecord:
			p.recordChoice(c)
		case stSourcePaste:
			first := "Paste RNA sequence"
			if cofold {
				first = "RNA A sequence (strand A)"
			}
			ui.TextArea(c.Key("st-paste"), &p.src.pasted).Lines(3, 8).Font("monospace").Placeholder("ACGU…").Label(first)
			if cofold {
				ui.TextArea(c.Key("st-paste2"), &p.src.pastedTwo).Lines(2, 6).Font("monospace").Placeholder("ACGU…").
					Label("RNA B sequence (strand B)")
				ui.Text(c, "The first box is RNA A. Alternatively paste A&B there and leave RNA B empty.").
					FontSize(12).TextColor(t.TextMuted)
				ui.Checkbox(c.Key("st-pk"), &p.pseudoknot, "Allow pseudoknots")
			} else {
				ui.Text(c, "A, C, G and U; T is read as U, spaces and a FASTA header line are ignored.").
					FontSize(12).TextColor(t.TextMuted)
			}
		}
		if p.src.kind != stSourceReference && len(pl.lengths) > 0 {
			ui.Text(c, pl.span()).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// recordChoice: the HYB records of the samples, most supported first.
func (p *structurePage) recordChoice(c *ui.Context) {
	t := c.Theme()
	p.loadRecords()
	switch {
	case p.src.loading:
		ui.Spinner(c).Label("Loading the HYB records…")
		return
	case p.src.recordsErr != "":
		ui.Text(c, "Could not read the HYB records: "+p.src.recordsErr+". Check that the samples' index is built (Data page).").
			FontSize(12).TextColor(t.Danger)
		return
	case len(p.src.records) == 0:
		ui.Text(c, "No HYB records for these samples. Choose other samples, or paste the sequence.").
			FontSize(12).TextColor(t.TextMuted)
		return
	}
	labels := []string{"Choose a record"}
	rows := map[string]int{}
	current := labels[0]
	for i, r := range p.src.records {
		label := stHybridLabel(r)
		if _, seen := rows[label]; seen {
			label += fmt.Sprintf(" (row %d)", r.Index+1)
		}
		labels = append(labels, label)
		rows[label] = i
		if p.src.record != nil && p.src.record.Index == r.Index {
			current = label
		}
	}
	if ui.Select(c.Key("st-record"), &current, labels).FillWidth().Label("HYB record").Tooltip(current).Changed() {
		p.src.record = nil
		if i, ok := rows[current]; ok {
			r := p.src.records[i]
			p.src.record = &r
		}
	}
	ui.Text(c, "Hybrid-read sequence: the chimeric sequence stored in the record, not necessarily a complete reference RNA or region.").
		FontSize(12).Padding(8).Radius(6).Background(t.Warning.Alpha(0.10))
}

// span of the folded region: "10,807 nt" or "3,000 + 400 nt".
func (pl foldPlan) span() string {
	parts := make([]string, len(pl.lengths))
	total := 0
	for i, n := range pl.lengths {
		parts[i] = FormatNumber(int64(n))
		total += n
	}
	if len(parts) > 4 {
		return FormatNumber(int64(total)) + " nt in all"
	}
	return strings.Join(parts, " + ") + " nt"
}

// ---- the form

func (p *structurePage) form(c *ui.Context) {
	t := c.Theme()
	pl := p.plan()
	card := Card(c, "New fold", func() {
		intro := "Predict the secondary structure of one RNA, guided by the chimeric reads of your samples."
		if p.engine == engineCPLcofold || (p.engine == engineComrades && p.mode == "paired") {
			intro = "Fold two regions, of one RNA or of two RNAs, guided by the chimeric reads of your samples."
		}
		ui.Text(c, intro).
			FontSize(12).TextColor(t.TextMuted)
		if p.prefillNote != "" {
			ui.Text(c, p.prefillNote).FontSize(12).Padding(8).Radius(6).Background(t.Accent.Alpha(0.10))
		}
		p.inputs(c, pl)
		if p.engine != engineCPLfold && p.engine != engineCPLcofold {
			p.viennaAdvanced(c)
		} else {
			p.advanced(c)
		}
	})
	// Cmd+Enter (Ctrl+Enter) runs the fold from anywhere in the form.
	card.OnShortcut(ui.Cmd, ui.KeyEnter, func() {
		if len(p.plan().problems) == 0 {
			p.runFold()
		}
	})
}

// runPanel: the check before running, Run fold and the status of the fold
// started here, pinned below the scrolling form so they stay in view.
func (p *structurePage) runPanel(c *ui.Context) {
	t := c.Theme()
	pl := p.plan()
	// At most about half the page, so the form and results keep room; a
	// longer check (a failure explained) scrolls inside.
	_, height := p.a.ContentSize()
	ui.Scroll(c.Key("st-run")).MaxHeight(clampF(height*0.55, 260, 560)).Margin(0, 14, 0, 0).Radius(10).
		Background(t.Background).Border(1, t.Border).Children(func() {
		ui.Column(c).Gap(8).Padding(12).Children(func() {
			p.check(c, pl)
			p.status(c)
		})
	})
}

// inputs: the RNA, the evidence and the mode.
func (p *structurePage) inputs(c *ui.Context, pl foldPlan) {
	a := p.a
	t := c.Theme()
	var geneLabels []string
	names := map[string]string{}
	current := ""
	for _, r := range a.Project.References {
		for _, s := range r.Sequences {
			label := s.Name + " · " + FormatNumber(int64(s.Length)) + " nt"
			if _, seen := names[label]; seen {
				continue
			}
			geneLabels = append(geneLabels, label)
			names[label] = s.Name
			if s.Name == p.gene {
				current = label
			}
		}
	}
	cofold := p.engine == engineCPLcofold
	twoRegions := cofold || (p.engine == engineComrades && p.mode == "paired")
	first := "1  RNA to fold"
	if twoRegions {
		first = "1  Region 1 (strand A)"
	}
	p.sourceField(c, pl)
	reference := p.src.kind == stSourceReference
	if reference {
		Field(c, first, func() {
			ui.Select(c.Key("st-gene"), &current, geneLabels).FillWidth().Label("RNA to fold").Tooltip(current).
				OnChange(func() { p.gene = names[current] })
			if pl.length > 0 {
				// The full name, which the select may cut.
				ui.Text(c, p.gene+" · "+FormatNumber(int64(pl.length))+" nt").FontSize(12).TextColor(t.TextMuted)
			}
			if twoRegions {
				ui.TextInput(c.Key("st-region1"), &p.region1).Placeholder("start-end, e.g. 1-45").Label("Region 1")
			}
		})
	}
	options := a.SelectionOptions()
	var evLabels []string
	keys := map[string]string{}
	currentEv := ""
	for _, o := range options {
		evLabels = append(evLabels, o.Label)
		keys[o.Label] = o.Key
		if o.Key == p.evidence {
			currentEv = o.Label
		}
	}
	evidenceLabel := "2  Evidence: chimeric reads of"
	switch {
	case !reference && (p.engine == engineVienna || (p.seqOnly && (p.engine == engineCPLfold || cofold))):
		evidenceLabel = "2  Samples (their chimeric reads are not used)"
	case !reference:
		evidenceLabel = "2  Evidence: chimeric reads of (used if the sequence is in their reference)"
	case p.engine == engineVienna || (p.seqOnly && (p.engine == engineCPLfold || cofold)):
		evidenceLabel = "2  Samples (their reference is folded)"
	}
	Field(c, evidenceLabel, func() {
		if len(evLabels) == 0 {
			ui.Text(c, "No analysed samples yet. Run the pipeline on the Data page; analysed samples and conditions appear here.").
				FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Select(c.Key("st-evidence"), &currentEv, evLabels).FillWidth().Label("Evidence").
			OnChange(func() { p.evidence = keys[currentEv] })
		if len(pl.ids) > 0 {
			ui.Text(c, "Samples: "+p.sampleNames(pl.ids)).FontSize(12).TextColor(t.TextMuted)
		}
		if p.engine == engineCPLfold || cofold {
			ui.Checkbox(c.Key("st-seqonly"), &p.seqOnly, "Sequence only: fold without the chimeric reads").
				Tooltip("As the web's sequence-only CPLfold; the samples then only give the reference")
		}
	})
	if !reference {
		// a pasted or record sequence is folded whole
		p.engineField(c)
		return
	}
	region2 := func(label string) {
		second := ""
		for l, name := range names {
			if name == nameOr(p.gene2, p.gene) {
				second = l
			}
		}
		Field(c, label, func() {
			ui.Select(c.Key("st-gene2"), &second, geneLabels).FillWidth().Label("Region 2 RNA").Tooltip(second).
				OnChange(func() { p.gene2 = names[second] })
			ui.TextInput(c.Key("st-region2"), &p.region2).Placeholder("start-end, e.g. 1086-1134").Label("Region 2")
			note := "The same RNA as region 1: a long-range interaction. Another RNA: an intermolecular one."
			if !cofold {
				note += " The same region twice, with homodimer proxies only (ViennaRNA settings): a homodimer."
			}
			if len(pl.lengths) == 2 {
				note = pl.span() + " · " + note
			}
			ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
			if cofold {
				ui.Checkbox(c.Key("st-pk"), &p.pseudoknot, "Allow pseudoknots")
			}
		})
	}
	if cofold {
		region2("3  Region 2 (strand B)")
	} else {
		p.whatToFold(c, pl)
		if twoRegions {
			region2("Region 2 (strand B)")
		}
	}
	p.engineField(c)
}

// whatToFold: the whole RNA or windows (CPLfold, ViennaRNA, COMRADES), or
// two regions joined by the web's spacer (COMRADES).
func (p *structurePage) whatToFold(c *ui.Context, pl foldPlan) {
	t := c.Theme()
	Field(c, "3  What to fold", func() {
		ui.RadioGroup(c.Key("st-mode"), func() {
			ui.Radio(c, &p.mode, "genome", "The whole RNA (no pseudoknots)")
			windows := "Regions (windows), pseudoknots possible"
			if p.engine != engineCPLfold {
				windows = "Regions (windows)"
			}
			ui.Radio(c, &p.mode, "windows", windows)
			if p.engine == engineComrades {
				ui.Radio(c, &p.mode, "paired", "Two regions joined by a 100-nt spacer (long-range, intermolecular or homodimer)")
			}
		}).Label("What to fold")
	})
	if p.mode == "windows" {
		Field(c, "Windows: start-end, one per line (1-based, inclusive)", func() {
			ui.TextArea(c.Key("st-windows"), &p.windows).Lines(3, 8).Font("monospace").
				Placeholder("e.g.\n1-400\n2300-2700").Label("Windows")
			if len(pl.lengths) > 0 {
				ui.Text(c, pl.what+": "+pl.span()).FontSize(12).TextColor(t.TextMuted)
			}
		})
		if p.engine == engineCPLfold {
			ui.Checkbox(c.Key("st-pk"), &p.pseudoknot, "Allow pseudoknots in the windows")
		}
	}
}

// engineField: the engines of the web RNA Structure page.
func (p *structurePage) engineField(c *ui.Context) {
	Field(c, "4  Engine", func() {
		ui.RadioGroup(c.Key("st-engine"), func() {
			ui.Radio(c, &p.engine, engineCPLfold, "CPLfold, guided by the chimeric reads").OnChange(p.engineChanged)
			ui.Radio(c, &p.engine, engineCPLcofold, "CPLcofold, two regions as two strands (one RNA or two RNAs)").OnChange(p.engineChanged)
			ui.Radio(c, &p.engine, engineVienna, "ViennaRNA MFE, the sequence alone (optional hard pairs)").OnChange(p.engineChanged)
			ui.Radio(c, &p.engine, engineComrades, "COMRADES(ViennaRNA), guided by RNAcofold of the chimeras").OnChange(p.engineChanged)
		}).Label("Engine")
	})
}

// engineChanged keeps "What to fold" valid for the new engine: two regions
// joined by the spacer are a COMRADES layout only. As the web, CPLcofold
// takes pasted strands instead of a HYB record.
func (p *structurePage) engineChanged() {
	if p.mode == "paired" && p.engine != engineComrades {
		p.mode = "genome"
	}
	if p.engine == engineCPLcofold && p.src.kind == stSourceRecord {
		p.setSource(stSourcePaste)
	}
}

// advanced: the CPLfold settings and parallel windows, folded away. Their
// values stay visible in the summary line, flagged when changed.
func (p *structurePage) advanced(c *ui.Context) {
	t := c.Theme()
	changed := p.foldSettingsChanged()
	summary := fmt.Sprintf("Evidence weight %.2f · beam %d · phase 1 %d · energy window %g · %s · beta %.2f",
		p.alpha, int(p.beam), int(p.phase1), p.delta, p.model, p.beta)
	if p.mode == "windows" {
		summary += fmt.Sprintf(" · %d window%s at once", int(p.workers), plural(int(p.workers)))
	}
	if changed {
		summary += " · changed from the defaults"
	} else {
		summary += " · defaults"
	}
	ui.Collapsible(c.Key("st-advanced"), "Advanced settings", &p.advancedOpen, func() {
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
			row("st-alpha", "Evidence weight (alpha)", &p.alpha, 0, 1, 0.05,
				"How strongly chimeric reads pull bases together, 0 to 1. 0 ignores them (energy only). Default 0.5.")
			row("st-beam", "Beam size", &p.beam, 1, 1000, 10,
				"How many partial structures CPLfold keeps per position. Larger is slower. Default 20, as on the web.")
			row("st-phase1", "Phase-1 candidates", &p.phase1, 1, 20, 1,
				"Candidate structures carried from the first phase into the second. Default 3.")
			row("st-delta", "Energy window", &p.delta, 0, 50, 1,
				"kcal/mol above the best structure within which candidates are kept. Default 5.")
			row("st-beta", "Pseudoknot beta", &p.beta, 0, 1, 0.05,
				"Weight of the pseudoknot energy term, 0 to 1. Default 0.")
			ui.Column(c).Gap(2).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Energy model").FontSize(12).Bold().Width(150)
					models, labels := stModels, stModelLabels
					if p.engine == engineCPLcofold {
						models, labels = stModels[:2], stModelLabels[:2] // DP09, DP03
					}
					i := 0
					for k, m := range models {
						if m == p.model {
							i = k
						}
					}
					label := labels[i]
					if ui.Select(c.Key("st-model"), &label, labels).Grow(1).Label("Energy model").Changed() {
						for k, l := range labels {
							if l == label {
								p.model = models[k]
							}
						}
					}
				})
				ui.Text(c, "Energy model that ranks candidates when pseudoknots are on. Default DP09. Pseudoknot-free folds rank with Turner 2004.").FontSize(12).TextColor(t.TextMuted)
			})
			if p.mode == "windows" {
				row("st-workers", "Windows at once", &p.workers, 1, 256, 1,
					"Windows folded in parallel; each needs its own memory. Default: your CPU budget.")
			}
			if ui.Button(c, "Reset to defaults").Disabled(!changed).Clicked() {
				p.resetFoldSettings()
			}
		})
	})
	// The values in use, also while the settings are folded away.
	color := t.TextMuted
	if changed {
		color = t.Warning
	}
	ui.Text(c, summary).FontSize(12).TextColor(color)
}

// check: the fold to run, its cost and limits, and Run fold — disabled,
// with the reasons, until it can run.
func (p *structurePage) check(c *ui.Context, pl foldPlan) {
	t := c.Theme()
	p.estimate(pl)
	ui.Column(c.Key("st-check")).Gap(6).Children(func() {
		ui.Text(c, "Check before running").Bold()
		line := func(label, value string) {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).Width(70)
				ui.Text(c, value).FontSize(12).Grow(1).Shrink(1).MinWidth(0)
			})
		}
		if pl.length > 0 && len(pl.lengths) > 0 {
			what := pl.what + ", " + pl.span()
			if pl.what == "whole RNA" {
				what = "the whole RNA, " + pl.span() + ", without pseudoknots"
			} else if pl.pk {
				what += ", pseudoknots allowed"
			}
			line("Fold", what)
		}
		if len(pl.ids) > 0 && p.engine != engineVienna {
			if p.seqOnly && (p.engine == engineCPLfold || p.engine == engineCPLcofold) {
				line("Evidence", "none: sequence only")
			} else {
				line("Evidence", pl.evidence)
			}
		}
		if p.engine == engineVienna && len(pl.lengths) > 0 {
			line("Engine", fmt.Sprintf("ViennaRNA MFE, %g °C, minimum loop %d", p.temperature, int(p.minLoop)))
			line("Time", "about "+FormatDuration(viennaSeconds(pl.lengths))+" (rough: MFE time grows with about the 2.5th power of the length)")
		}
		if p.engine == engineComrades && len(pl.lengths) > 0 {
			lengths := pl.lengths
			if p.mode == "paired" && len(lengths) == 2 {
				lengths = []int{lengths[0] + stSpacerNT + lengths[1]} // folded joined
			}
			line("Engine", fmt.Sprintf("COMRADES(ViennaRNA), %g °C, minimum loop %d", p.temperature, int(p.minLoop)))
			line("Time", "up to about "+FormatDuration(p.comradesSeconds(lengths))+" (one ViennaRNA fold per stem tried, per order)")
		}
		if p.est != nil && (p.engine == engineCPLfold || p.engine == engineCPLcofold) {
			time := "about " + FormatDuration(p.est.Seconds) + " (measured on this computer)"
			if !p.est.Measured {
				time = "about " + FormatDuration(p.est.Seconds) + " (rough; the first fold after installing also " +
					"prepares the folding engine, about 1 min more)"
			}
			line("Time", time)
		}
		if pl.memMB > 0 {
			mem := fmt.Sprintf("about %.1f GB", float64(pl.memMB)/1024)
			if p.budgetMB > 0 {
				mem += fmt.Sprintf(" of the %.1f GB tasks may use", float64(p.budgetMB)/1024)
			}
			line("Memory", mem)
		}
		for _, l := range pl.limits {
			ui.Text(c, "• "+l).FontSize(12).TextColor(t.TextMuted)
		}
		if len(pl.problems) > 0 {
			ui.Text(c, "Cannot run yet:").FontSize(12).Bold().TextColor(t.Danger)
			for _, msg := range pl.problems {
				ui.Text(c, "• "+msg).FontSize(12).TextColor(t.Danger)
			}
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Shortcut: "+cmdName()+"+Enter").FontSize(12).TextColor(t.TextMuted).Grow(1)
			busy := p.running()
			label := "Run fold"
			if busy {
				label = "Fold running…"
			}
			if ui.PrimaryButton(c, label).Disabled(len(pl.problems) > 0 || busy).Clicked() {
				p.runFold()
			}
		})
	})
}

func cmdName() string {
	if ui.Cmd == ui.Super {
		return "⌘"
	}
	return "Ctrl"
}

// estimate keeps p.est for the plan's lengths (computed off the UI thread).
func (p *structurePage) estimate(pl foldPlan) {
	if len(pl.lengths) == 0 {
		p.estKey, p.est = "", nil
		return
	}
	a := p.a
	workers := max(int(p.workers), 1)
	if p.mode != "windows" {
		workers = 1
	}
	lengths := pl.lengths
	if p.engine == engineCPLcofold {
		// two strands fold as one A+B sequence
		lengths, workers = []int{pl.lengths[0] + pl.lengths[1]}, 1
	}
	key := fmt.Sprint(lengths, int(p.beam), workers, pl.pk)
	if key == p.estKey {
		return
	}
	p.estKey, p.est = key, nil
	beam := int(p.beam)
	a.Go(func() func() {
		e := a.B.EstimateFold(lengths, beam, workers)
		return func() {
			if p.estKey == key {
				p.est = &e
			}
		}
	})
}

func (p *structurePage) runFold() {
	a := p.a
	pl := p.plan()
	if len(pl.problems) > 0 || p.running() {
		return
	}
	p.starting, p.startErr, p.lastTask = true, "", ""
	mem := pl.memMB
	title := "Structure · " + pl.evidence + " · " + pl.what
	switch p.engine {
	case engineVienna:
		title = "Structure (ViennaRNA) · " + pl.what
	case engineComrades:
		title = "Structure (COMRADES) · " + pl.evidence + " · " + pl.what
	case engineCPLcofold:
		title = "Structure (CPLcofold) · " + pl.evidence + " · " + stRegionsLabel(pl.req.Regions)
	}
	if p.seqOnly && (p.engine == engineCPLfold || p.engine == engineCPLcofold) {
		title = strings.Replace(title, pl.evidence, "sequence only", 1)
	}
	if pl.source != "" {
		title = stSourceTitle(p.engine, pl.source, p.seqOnly)
	}
	a.StartTask(pl.req, title, func(t model.Task, err error) {
		p.starting = false
		if err != nil {
			p.startErr = StructureStartError(err, mem)
			return
		}
		p.prefillNote = ""
		p.lastTask = t.TaskID
		p.pendingShow = t.TaskID
	})
}

// stSourceTitle is the task title of a pasted or HYB record sequence:
// "Structure (ViennaRNA) · pasted sequence, 90 nt".
func stSourceTitle(engine, source string, seqOnly bool) string {
	title := map[string]string{engineVienna: "Structure (ViennaRNA)", engineComrades: "Structure (COMRADES)",
		engineCPLcofold: "Structure (CPLcofold)"}[engine]
	title = nameOr(title, "Structure") + " · " + source
	if seqOnly && (engine == engineCPLfold || engine == engineCPLcofold) {
		title += " · sequence only"
	}
	return title
}

// running: this page's fold is starting, queued or running.
func (p *structurePage) running() bool {
	if p.starting {
		return true
	}
	t, ok := p.a.Tasks[p.lastTask]
	return ok && t.Active()
}

// ---- status of the fold started here

func (p *structurePage) status(c *ui.Context) {
	a := p.a
	t := c.Theme()
	box := func(color ui.Color, body func()) {
		ui.Column(c.Key("st-status")).Gap(6).Padding(10).Radius(8).Border(1, color).Children(body)
	}
	switch {
	case p.starting:
		box(t.Border, func() { ui.Spinner(c).Label("Starting the fold…") })
		return
	case p.startErr != "":
		box(t.Danger, func() {
			ui.Text(c, "The fold did not start").Bold().TextColor(t.Danger)
			ui.Text(c, p.startErr).FontSize(12)
		})
		return
	case p.lastTask == "":
		return
	}
	task, ok := a.Tasks[p.lastTask]
	if !ok {
		return
	}
	switch task.Status {
	case "queued":
		box(t.Accent, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Waiting to start").Bold().Grow(1)
				if ui.Button(c, "Cancel").Clicked() {
					a.CancelTask(task.TaskID)
				}
			})
			ui.Text(c, "Other tasks are using the CPU or memory tasks may use (Preferences); this fold starts when they finish.").
				FontSize(12).TextColor(t.TextMuted)
		})
	case "running":
		box(t.Accent, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, stStageText(task)).Bold().Grow(1).Shrink(1).MinWidth(0)
				if ui.Button(c, "Cancel").Tooltip("Stop the fold; nothing is saved").Clicked() {
					a.CancelTask(task.TaskID)
				}
			})
			ui.Progress(c, task.Progress.Fraction()).Label("Fold progress")
			ui.Text(c, stTimeLine(task)).FontSize(12).TextColor(t.TextMuted)
		})
	case "succeeded":
		box(t.Success, func() {
			ui.Text(c, "Fold finished").Bold().TextColor(t.Success)
			done := "It is shown in the viewer and listed under Results."
			if e := p.entryForTask(task.TaskID); e != nil {
				if s := stSummaryText(e); s != "" {
					done = s + ". " + done
				}
			}
			ui.Text(c, done).FontSize(12)
		})
	case "failed":
		what, next := stExplain(task.ErrorText())
		box(t.Danger, func() {
			ui.Text(c, "The fold failed").Bold().TextColor(t.Danger)
			ui.Text(c, what).FontSize(12)
			ui.Text(c, "Next: "+next).FontSize(12)
			ui.Collapsible(c.Key("st-error"), "Technical details", &p.errorOpen, func() {
				ui.Text(c, truncate(task.ErrorText(), 600)+" (log: runs/"+task.TaskID+"/stderr.log)").FontSize(12).
					TextColor(t.TextMuted).Selectable()
			})
		})
	case "cancelled":
		box(t.Border, func() {
			ui.Text(c, "Fold cancelled").Bold()
			ui.Text(c, "Nothing was saved, including windows that had already been folded. Change the form if needed, then run it again.").
				FontSize(12).TextColor(t.TextMuted)
		})
	case "interrupted":
		box(t.Warning, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Fold stopped").Bold().Grow(1)
				if ui.Button(c, "Run it again").Clicked() {
					a.ResumeTaskThen(task.TaskID, a.TaskLabel(task), func(nt model.Task, err error) {
						if err == nil {
							p.lastTask, p.pendingShow = nt.TaskID, nt.TaskID
						}
					})
				}
			})
			ui.Text(c, "HYB2 Desktop closed while it ran. Nothing was saved; it starts from the beginning.").
				FontSize(12).TextColor(t.TextMuted)
		})
	}
}

// stStageText says what a running fold does.
func stStageText(task model.Task) string {
	pr := task.Progress
	if pr == nil {
		return "Folding…"
	}
	switch pr.Stage {
	case "evidence":
		return "Reading the chimeric reads…"
	case "fold":
		if pr.BytesTotal != nil && *pr.BytesTotal > 1 && pr.BytesDone != nil {
			return fmt.Sprintf("Folding: %.0f of %.0f windows done", *pr.BytesDone, *pr.BytesTotal)
		}
		return "Folding…"
	case "write":
		return "Saving the results…"
	}
	return "Folding…"
}

// stTimeLine: elapsed and, when known, the time left.
func stTimeLine(task model.Task) string {
	parts := []string{}
	if at, err := time.Parse(time.RFC3339, task.LaunchedAt); err == nil {
		parts = append(parts, "running for "+FormatDuration(time.Since(at).Seconds()))
	}
	if task.SecondsLeft != nil {
		parts = append(parts, "about "+FormatDuration(*task.SecondsLeft)+" left")
	} else {
		parts = append(parts, "time left unknown")
	}
	return strings.Join(parts, " · ") + ". You can keep working; the fold continues in the background."
}

// stExplain turns a fold's error into what happened and what to do.
func stExplain(err string) (what, next string) {
	low := strings.ToLower(err)
	switch {
	case strings.Contains(low, "exceeds the cplfold limit"):
		return "The region is longer than CPLfold can fold with the memory tasks may use.",
			"fold shorter windows, or raise Memory in Preferences."
	case strings.Contains(low, "no intramolecular") || strings.Contains(low, "chimeras fall inside") ||
		strings.Contains(low, "chimeras have both arms inside"):
		return "No chimeric read of these samples has both arms inside the folded region, so there is no evidence to guide the fold.",
			"choose a wider window or other samples, or check the region on Explore › Contact map."
	case strings.Contains(low, "memory") || strings.Contains(low, "killed") || strings.Contains(low, "signal"):
		return "The fold ran out of memory or was stopped by the system.",
			"fold shorter windows or fewer at once (Advanced), or close other programs and run it again."
	case strings.Contains(low, "not bundled") || strings.Contains(low, "unavailable") || strings.Contains(low, "no module"):
		return "The folding engine is missing from this installation.",
			"reinstall HYB2 Desktop."
	case strings.Contains(low, "missing") || strings.Contains(low, "no such file") || strings.Contains(low, "not found"):
		return "A file the fold reads (a sample's .hyb or the reference) is missing or moved.",
			"use Check inputs on the Data page and relink the file, then run the fold again."
	}
	return "CPLfold stopped with an error.", "run it again; if it fails again, send the technical details below to the HYB2 team."
}

// ---- results

// stSummary is a listed structure's summary (from its task).
type stSummary struct {
	Pairs           int      `json:"pairs"`
	PseudoknotPairs int      `json:"pseudoknot_pairs"`
	Energy          *float64 `json:"energy"`
	Length          int      `json:"length"`
	Records         int      `json:"records"`
}

func (p *structurePage) entryForTask(id string) *structureEntry {
	for i := range p.structures {
		if p.structures[i].TaskID == id || p.structures[i].ID == id {
			return &p.structures[i]
		}
	}
	return nil
}

// stSummaryText: "1,055 base pairs (0 pseudoknotted) · −452.0 kcal/mol".
func stSummaryText(e *structureEntry) string {
	var s stSummary
	if e.Summary == nil || model.Decode(e.Summary, &s) != nil || s.Pairs == 0 && s.Length == 0 {
		return ""
	}
	text := FormatNumber(int64(s.Pairs)) + " base pairs"
	if s.PseudoknotPairs > 0 {
		text += fmt.Sprintf(" (%s pseudoknotted)", FormatNumber(int64(s.PseudoknotPairs)))
	}
	if s.Energy != nil {
		text += fmt.Sprintf(" · %.1f kcal/mol", *s.Energy)
	}
	return text
}

// stWhat: "whole RNA · 10,807 nt" or "2 windows · 3,060 nt".
func stWhat(e *structureEntry) string {
	if source := stEntrySource(e); source != "" {
		return source
	}
	switch e.Mode {
	case "cofold":
		return "2 regions as two strands · " + stRegionsLabel(e.Regions)
	case "paired":
		return "2 regions joined by the spacer · " + stRegionsLabel(e.Regions)
	}
	if e.Mode == "genome" {
		var s stSummary
		if e.Summary != nil && model.Decode(e.Summary, &s) == nil && s.Length > 0 {
			return "whole RNA · " + FormatNumber(int64(s.Length)) + " nt"
		}
		return "whole RNA"
	}
	total := 0
	spans := make([]string, 0, len(e.Windows))
	for _, w := range e.Windows {
		total += int(w.End - w.Start + 1)
		spans = append(spans, FormatNumber(int64(w.Start))+"–"+FormatNumber(int64(w.End)))
	}
	text := fmt.Sprintf("%d window%s · %s nt", len(e.Windows), plural(len(e.Windows)), FormatNumber(int64(total)))
	if len(spans) <= 2 {
		text += " (" + strings.Join(spans, ", ") + ")"
	}
	return text
}

// stEntrySource names a fold of a pasted or HYB record sequence, "pasted
// sequence · 90 nt" or "pasted sequence, found at RNA:40-200 · 161 nt";
// "" for a reference region.
func stEntrySource(e *structureEntry) string {
	kind, _ := e.Parameters["sequence_source"].(string)
	if kind == "" {
		return ""
	}
	what := "pasted sequence"
	switch {
	case e.Mode == "cofold":
		what = "pasted RNA A + RNA B"
	case kind == "record":
		what = "HYB record sequence"
		if label, _ := e.Parameters["sequence_label"].(string); label != "" {
			what = "HYB record " + strings.TrimPrefix(label, "record_")
		}
	}
	var mapped []string
	if model.Decode(e.Parameters["sequence_mapped_to"], &mapped) == nil && len(mapped) > 0 {
		what += ", found at " + strings.Join(mapped, " + ")
	}
	var s stSummary
	if e.Summary != nil && model.Decode(e.Summary, &s) == nil && s.Length > 0 {
		what += " · " + FormatNumber(int64(s.Length)) + " nt"
	}
	return what
}

func (p *structurePage) list(c *ui.Context) {
	t := c.Theme()
	Card(c, "", func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			title := "Results"
			if n := len(p.structures); n > 0 {
				title = fmt.Sprintf("Results (%d)", n)
			}
			ui.Text(c, title).FontSize(16).Bold().Grow(1)
			if ui.Button(c, "Refresh").Tooltip("Read the project's structures again").Clicked() {
				p.loadList()
			}
		})
		ui.Text(c, "Newest first. Show puts a result in the viewer as A; Compare as B overlays a second one.").
			FontSize(12).TextColor(t.TextMuted)
		others := p.otherTasks(c)
		if p.listErr != "" {
			ui.Text(c, "Could not read the project's structures: "+p.listErr+". Press Refresh to try again.").
				FontSize(12).TextColor(t.Danger)
		}
		if len(p.structures) == 0 {
			switch {
			case p.loading:
				ui.Spinner(c).Label("Loading results…")
			case others == 0 && p.listErr == "":
				ui.Text(c, "No structures yet. Choose an RNA and its evidence above and press Run fold; "+
					"results appear here and in the Report.").FontSize(12).TextColor(t.TextMuted)
			}
			return
		}
		for i := range p.structures {
			e := &p.structures[i]
			isA, isB := p.viewA == e.ID, p.viewB == e.ID
			item := ui.Column(c.Key("st-"+e.ID)).Gap(3).Padding(8, 10).Radius(8).Border(1, t.Border)
			if isA || isB {
				item.Background(t.Accent.Alpha(0.10)).BorderColor(t.Accent)
			}
			item.Children(func() {
				// The whole RNA name, on up to two lines.
				ui.Text(c, e.GeneID).Bold().MaxLines(2).Tooltip(e.GeneID)
				ui.Text(c, stWhat(e)).FontSize(12)
				if s := stSummaryText(e); s != "" {
					ui.Text(c, s).FontSize(12)
				}
				meta := "Evidence: " + nameOr(p.evidenceNames(e), "—") + " · " + FormatTime(e.CreatedAt)
				switch {
				case stEntrySource(e) != "" && (e.Parameters["engine"] == "ViennaRNA" || e.Parameters["evidence"] == "none"):
					// a pasted or record sequence folded alone: the samples gave nothing
					engine, _ := e.Parameters["engine"].(string)
					meta = nameOr(engine, "CPLfold") + " · sequence only · " + FormatTime(e.CreatedAt)
				case e.Parameters["engine"] == "ViennaRNA":
					meta = "ViennaRNA MFE · reference of " + nameOr(p.evidenceNames(e), "—") + " · " + FormatTime(e.CreatedAt)
				case e.Parameters["evidence"] == "none":
					engine, _ := e.Parameters["engine"].(string)
					meta = nameOr(engine, "CPLfold") + " · sequence only · reference of " +
						nameOr(p.evidenceNames(e), "—") + " · " + FormatTime(e.CreatedAt)
				case e.Parameters["engine"] == "CPLcofold":
					meta = "CPLcofold · evidence: " + nameOr(p.evidenceNames(e), "—") + " · " + FormatTime(e.CreatedAt)
				}
				ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted)
				if e.Available != nil && !*e.Available {
					ui.Text(c, "Its files are missing from the project folder; it cannot be shown. Run the fold again to recreate it.").
						FontSize(12).TextColor(t.Danger)
					return
				}
				ui.Row(c).Gap(6).Children(func() {
					if isA {
						ui.Badge(c, "A · shown").Background(cmpRed.Alpha(0.15)).TextColor(cmpRed)
					} else if ui.Button(c, "Show").Clicked() {
						p.show(e.ID)
					}
					if isB {
						ui.Badge(c, "B · compared").Background(cmpBlue.Alpha(0.15)).TextColor(cmpBlue)
					} else if p.viewA != "" && !isA {
						if ui.Button(c, "Compare as B").Clicked() {
							p.viewB = e.ID
							p.ensureRead(e.ID)
						}
					}
				})
			})
		}
	})
}

// otherTasks lists structure folds started elsewhere (Explore, Compare) or
// earlier: running ones with Cancel, recent failures explained. Returns how
// many it showed.
func (p *structurePage) otherTasks(c *ui.Context) int {
	a := p.a
	t := c.Theme()
	shown, failed := 0, 0
	for _, task := range a.TaskList() {
		if task.Kind != "structure" || task.TaskID == p.lastTask {
			continue
		}
		switch {
		case task.Active():
			shown++
			ui.Column(c.Key("task-"+task.TaskID)).Gap(4).Padding(8, 10).Radius(8).Border(1, t.Accent.Alpha(0.5)).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.TaskLabel(task)).Bold().Grow(1).Shrink(1).MinWidth(0)
					if ui.Button(c, "Cancel").Clicked() {
						a.CancelTask(task.TaskID)
					}
				})
				if task.Status == "queued" {
					ui.Text(c, "Waiting to start").FontSize(12).TextColor(t.TextMuted)
				} else {
					ui.Text(c, stStageText(task)).FontSize(12)
					ui.Progress(c, task.Progress.Fraction()).Label("Fold progress")
				}
			})
		case failed < 3 && (task.Status == "interrupted" || (task.Status == "failed" && task.FinishedAt >= a.SessionStart)):
			failed++
			shown++
			ui.Column(c.Key("task-"+task.TaskID)).Gap(4).Padding(8, 10).Radius(8).Border(1, t.Border).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.TaskLabel(task)).Bold().Grow(1).Shrink(1).MinWidth(0)
					StatusChip(c, task.Status)
					if ui.Button(c, "Run it again").Clicked() {
						a.ResumeTask(task.TaskID)
					}
				})
				if task.Status == "interrupted" {
					ui.Text(c, "Stopped when HYB2 Desktop closed; nothing was saved.").FontSize(12).TextColor(t.TextMuted)
				} else {
					what, next := stExplain(task.ErrorText())
					ui.Text(c, what+" Next: "+next).FontSize(12).TextColor(t.Danger)
				}
			})
		}
	}
	return shown
}

// sampleNames: "SUB_REP1 + SUB_REP2".
func (p *structurePage) sampleNames(ids []string) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = id
		if s := p.a.Project.Sample(id); s != nil {
			names[i] = s.Label()
		}
	}
	return strings.Join(names, " + ")
}
