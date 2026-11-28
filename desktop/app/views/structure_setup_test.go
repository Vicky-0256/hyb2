package views

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"hyb2desktop/core"
	"hyb2desktop/model"
)

// stSetupPage opens the Structure page of the test project.
func stSetupPage(t *testing.T, height int) (*App, *csFake, *ui.Tester, *structurePage) {
	t.Helper()
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, height)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	return a, f, tt, a.PageFor("structure").(*structurePage)
}

// Unlike a gene-only fixture, this backend returns different bases for the
// same selectable HYB alias on different references. Physical FASTA names
// are deliberately absent: the runtime, not the form, maps those names.
type stIdentityFake struct {
	*csFake
	sequences map[string]map[string]string
	reads     []core.Obj
}

func (f *stIdentityFake) ProjectOp(path, operation string, payload core.Obj) (core.Obj, error) {
	if operation != "read_reference_sequence" {
		return f.csFake.ProjectOp(path, operation, payload)
	}
	ref, _ := payload["reference_id"].(string)
	name, _ := payload["name"].(string)
	f.mu.Lock()
	f.reads = append(f.reads, core.Obj{"reference_id": ref, "name": name})
	seq := f.sequences[ref][name]
	f.mu.Unlock()
	if seq == "" {
		return nil, fmt.Errorf("no mapped RNA %q on reference %q", name, ref)
	}
	return core.Obj{"name": name, "sequence_name": "physical-" + ref + "-" + name, "sequence": seq}, nil
}

func stIdentityPage(t *testing.T, firstNT int, reversed bool) (*App, *stIdentityFake, *ui.Tester, *structurePage) {
	t.Helper()
	a, base := openCS(t)
	a.Project.References = []model.Reference{
		{ID: "R1", Label: "R1", Path: "inputs/r1.fa", Sequences: []model.Sequence{{Name: "physical-R1-shared", Length: firstNT}},
			RNASequences: []model.Sequence{{Name: "shared", Length: firstNT}, {Name: "partner", Length: firstNT}}},
		{ID: "R2", Label: "R2", Path: "inputs/r2.fa", Sequences: []model.Sequence{{Name: "physical-R2-shared", Length: 40}},
			RNASequences: []model.Sequence{{Name: "shared", Length: 40}, {Name: "partner", Length: 60}}},
	}
	if reversed {
		slices.Reverse(a.Project.References)
	}
	a.Project.Samples = []model.Sample{
		{ID: "reads-R1", Name: "R1 reads", ReferenceID: "R1"},
		{ID: "reads-R2", Name: "R2 reads", ReferenceID: "R2", Ready: true},
	}
	a.Project.Conditions = nil
	a.Selected = Selection{ReferenceID: "R1", Gene: "shared", Key: "s:reads-R2"}
	f := &stIdentityFake{csFake: base, sequences: map[string]map[string]string{
		"R1": {"shared": strings.Repeat("A", firstNT), "partner": strings.Repeat("C", firstNT)},
		"R2": {"shared": strings.Repeat("G", 40), "partner": strings.Repeat("U", 60)},
	}}
	a.B = f
	f.ops["list_samples"] = core.Obj{"samples": a.Project.Samples, "references": a.Project.References}
	f.ops["list_structures"] = core.Obj{"structures": []any{}}
	tt := ui.NewTester(a.View, 1100, 1500)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	p.mode, p.windows, p.setup.previewOpen = "windows", "21-40", true
	csSettle(a, tt)
	return a, f, tt, p
}

// Structure-only disambiguation must not disable existing readers that
// still pass a bare RNA name through the shared public lookup.
func TestStructureReferenceLookupCompatibility(t *testing.T) {
	a, f, _, _ := stIdentityPage(t, 20, false)
	a.Project.Sample("reads-R1").Ready = true
	a.Selected = Selection{ReferenceID: "R1", Gene: "shared", Key: "s:reads-R1"}
	f.mu.Lock()
	f.reads = nil
	f.mu.Unlock()

	seq, err := fetchSequence(a.B, a.Project, a.Project.Path, "shared")
	want := strings.Repeat("A", 20)
	t.Logf("fetchSequence: selected reference=%s, sequence=%q, error=%v", a.Selected.ReferenceID, seq, err)
	if err != nil || seq != want {
		t.Errorf("legacy sequence reader lost the previously usable R1 sequence: %q, %v", seq, err)
	}

	// Exercise Explore's real asynchronous region reader, including its
	// GeneLength-based range clamp, without replacing either view helper.
	explore := newExplorePage(a)
	d := regionData{gene: "shared", f: regionFilters{start: 1, end: 40}}
	explore.loadRegionSequence(&d)
	waitFlush(a)
	start, end := explore.regionSequenceRange(&d)
	t.Logf("Explore region: range=%d-%d, sequence=%q, error=%q", start, end, explore.rg.seq, explore.rg.seqErr)
	if start != 1 || end != 20 || explore.rg.seq != want || explore.rg.seqErr != "" || explore.rg.seqLoad.busy() {
		t.Errorf("legacy Explore region reader lost R1: range=%d-%d, sequence=%q, error=%q", start, end, explore.rg.seq, explore.rg.seqErr)
	}

	f.mu.Lock()
	reads := append([]core.Obj(nil), f.reads...)
	f.mu.Unlock()
	t.Logf("sequence backend reads: %v", reads)
	if len(reads) != 2 {
		t.Fatalf("both existing readers must reach the sequence backend: %v", reads)
	}
	for _, read := range reads {
		if read["reference_id"] != "R1" || read["name"] != "shared" {
			t.Fatalf("legacy sequence read changed the first reference or mapped alias: %v", read)
		}
	}
}

func TestStructureMultiReferenceIdentity(t *testing.T) {
	for _, firstNT := range []int{20, 40} {
		for _, reversed := range []bool{false, true} {
			name := fmt.Sprintf("first-%d-reversed-%t", firstNT, reversed)
			t.Run(name, func(t *testing.T) {
				a, f, tt, p := stIdentityPage(t, firstNT, reversed)
				pl := p.plan()
				parts, _, loading, problem := p.prepared(pl, true)
				snapshot(t, tt, "structure-reference-"+name)
				ref := p.geneReference()
				t.Logf("selected sample=reads-R2; resolved reference=%v; length=%d; problems=%q; preview=%v; loading=%t; error=%q",
					ref, pl.length, pl.problems, parts, loading, problem)
				if ref == nil || ref.ID != "R2" || pl.length != 40 || len(pl.problems) != 0 || loading || problem != "" ||
					len(parts) != 1 || parts[0].seq != strings.Repeat("G", 20) {
					t.Fatal("the selected R2 sample must determine both the 21–40 window and its bases")
				}
				mustHave(t, tt, "shared · 40 nt · R2")
				mustClick(t, tt, "Run fold")
				csSettle(a, tt)
				started := f.startedKinds()
				if len(started) != 1 || !slices.Equal(started[0].SampleIDs, []string{"reads-R2"}) ||
					started[0].GeneID == nil || *started[0].GeneID != "shared" ||
					!slices.Equal(started[0].Windows, []core.Window{{Start: 21, End: 40}}) {
					t.Fatalf("request lost the reference-defining sample or HYB alias: %+v", started)
				}
				f.mu.Lock()
				reads := append([]core.Obj(nil), f.reads...)
				f.mu.Unlock()
				if len(reads) == 0 {
					t.Fatal("the preview did not read the reference")
				}
				for _, read := range reads {
					if read["reference_id"] != "R2" || read["name"] != "shared" {
						t.Fatalf("preview changed reference identity or remapped the HYB alias: %v", read)
					}
				}
				t.Logf("preview reads=%v; request sample_ids=%v, gene_id=%s, windows=%v", reads,
					started[0].SampleIDs, *started[0].GeneID, started[0].Windows)
			})
		}
	}
}

func TestStructureQualifiedReferenceSelection(t *testing.T) {
	a, f, tt, p := stIdentityPage(t, 40, false)
	a.Project.Sample("reads-R1").Ready = true
	p.evidence = AllSelection
	csSettle(a, tt)
	if pl := p.plan(); pl.referenceID != "" || !slices.Contains(pl.problems, "Choose the RNA and its reference to fold.") {
		t.Fatalf("mixed-reference aliases must require a qualified choice: %+v", pl)
	}
	mustClick(t, tt, "Run fold")
	if len(f.startedKinds()) != 0 {
		t.Fatal("ambiguous reference submitted a task")
	}
	snapshot(t, tt, "structure-reference-ambiguous")
	mustClick(t, tt, "RNA to fold")
	mustHave(t, tt, "shared · 40 nt · R1", "shared · 40 nt · R2")
	snapshot(t, tt, "structure-reference-picker-equal-length")
	mustClick(t, tt, "shared · 40 nt · R2")
	csSettle(a, tt)
	if pl := p.plan(); p.referenceID != "R2" || pl.referenceID != "R2" || len(pl.problems) != 0 ||
		!slices.Equal(pl.req.SampleIDs, []string{"reads-R2"}) {
		t.Fatalf("qualified R2 selection did not select its evidence: %+v", pl)
	}
	slices.Reverse(a.Project.References)
	tt.Frame()
	if pl := p.plan(); pl.referenceID != "R2" || len(pl.problems) != 0 {
		t.Fatalf("reference reordering changed the explicit choice: %+v", pl)
	}

	// Changing evidence must not silently reinterpret the explicit RNA choice.
	mustClick(t, tt, "Evidence")
	mustClick(t, tt, "R1 reads")
	csSettle(a, tt)
	pl := p.plan()
	if pl.referenceID != "R2" || !strings.Contains(strings.Join(pl.problems, " "), "not to R2") {
		t.Fatalf("incompatible evidence replaced the selected reference: %+v", pl)
	}
	mustClick(t, tt, "Run fold")
	if len(f.startedKinds()) != 0 {
		t.Fatal("mismatched explicit reference submitted a task")
	}
	snapshot(t, tt, "structure-reference-explicit-mismatch")

	mustClick(t, tt, "RNA to fold")
	mustClick(t, tt, "shared · 40 nt · R1")
	csSettle(a, tt)
	pl = p.plan()
	parts, _, loading, problem := p.prepared(pl, true)
	if p.referenceID != "R1" || pl.referenceID != "R1" || len(pl.problems) != 0 || loading || problem != "" ||
		len(parts) != 1 || parts[0].seq != strings.Repeat("A", 20) {
		t.Fatalf("explicit R1 preview reused R2's cache: %+v, %v, %t, %q", pl, parts, loading, problem)
	}
	mustClick(t, tt, "Run fold")
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || !slices.Equal(started[0].SampleIDs, []string{"reads-R1"}) || *started[0].GeneID != "shared" {
		t.Fatalf("explicit reference did not reach the task through its samples: %+v", started)
	}
	t.Logf("explicit R1 request: sample_ids=%v, gene_id=%s; preview=%s", started[0].SampleIDs, *started[0].GeneID, parts[0].seq)

	// The header's reference can change without changing the alias. A prefill
	// subsequently resolves its own pinned samples, not a stale picker choice.
	a.Selected.ReferenceID = "R2"
	tt.Frame()
	if p.referenceID != "R2" {
		t.Fatal("a same-name header reference change was ignored")
	}
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"gene": "shared", "sampleIds": []string{"reads-R2"},
		"windows": []core.Window{{Start: 21, End: 40}}}})
	csSettle(a, tt)
	if pl := p.plan(); pl.referenceID != "R2" || len(pl.problems) != 0 || !slices.Equal(pl.req.SampleIDs, []string{"reads-R2"}) {
		t.Fatalf("prefill did not resolve its pinned sample's reference: %+v", pl)
	}
	p.pinSamples([]string{"reads-R1", "reads-R2"})
	if pl := p.plan(); len(pl.problems) == 0 || len(pl.req.SampleIDs) != 2 {
		t.Fatalf("mixed pinned samples were silently filtered: %+v", pl)
	}
}

func TestStructureMultiReferenceLayouts(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		for _, engine := range []string{engineCPLcofold, engineComrades} {
			t.Run(fmt.Sprintf("%s/reversed-%t", engine, reversed), func(t *testing.T) {
				a, f, tt, p := stIdentityPage(t, 20, reversed)
				p.engine, p.mode, p.region1, p.region2 = engine, "paired", "21-30", "31-40"
				for _, second := range []string{"shared", "partner"} {
					if second == "partner" {
						mustClick(t, tt, "Region 2 RNA")
						mustClick(t, tt, "partner · 60 nt · R2")
						p.region2 = "41-50"
					}
					csSettle(a, tt)
					pl := p.plan()
					parts, _, loading, problem := p.prepared(pl, true)
					want := strings.Repeat("G", 10)
					if engine == engineComrades {
						want += stSpacer
					}
					want += strings.Repeat(map[string]string{"shared": "G", "partner": "U"}[second], 10)
					if pl.referenceID != "R2" || pl.referenceID2 != "R2" || len(pl.problems) != 0 || loading || problem != "" ||
						len(parts) != 1 || parts[0].seq != want || !slices.Equal(pl.lengths, []int{10, 10}) || pl.req.Regions[1].GeneID != second {
						t.Fatalf("two-region identity: %+v, parts=%v, loading=%t, error=%q", pl, parts, loading, problem)
					}
					mustClick(t, tt, "Run fold")
					csSettle(a, tt)
					started := f.startedKinds()
					if len(started) == 0 {
						t.Fatal("Run fold did not submit the valid two-region plan")
					}
					req := started[len(started)-1]
					if !slices.Equal(req.SampleIDs, []string{"reads-R2"}) || !slices.Equal(req.Regions, pl.req.Regions) || *req.GeneID != "shared" {
						t.Fatalf("two-region request changed identity: %+v", req)
					}
					a.storeTask(model.Task{TaskID: p.lastTask, Kind: "structure", Status: "succeeded"})
					csSettle(a, tt)
					t.Logf("engine=%s, region refs=%s/%s, sample_ids=%v, regions=%v, prepared=%s", engine,
						pl.referenceID, pl.referenceID2, req.SampleIDs, req.Regions, parts[0].seq)
				}
				if len(f.startedKinds()) != 2 {
					t.Fatal("both layouts must submit exactly once")
				}
				snapshot(t, tt, fmt.Sprintf("structure-reference-%s-two-rnas-reversed-%t", engine, reversed))
				mustClick(t, tt, "Region 2 RNA")
				mustClick(t, tt, "shared · 20 nt · R1")
				p.region2 = "1-10"
				tt.Frame()
				pl := p.plan()
				if pl.referenceID2 != "R1" || !slices.Contains(pl.problems, "Both regions must be on RNAs of one reference, the reference of the samples.") {
					t.Fatalf("explicit arm B reference was ignored: %+v", pl)
				}
				mustClick(t, tt, "Run fold")
				if len(f.startedKinds()) != 2 {
					t.Fatal("different-reference arms submitted a task")
				}
			})
		}
	}
}

// The web's "Folding workflow" presets on the desktop's engines.
func TestStructureWorkflowPresets(t *testing.T) {
	_, _, tt, p := stSetupPage(t, 1400)
	if p.workflow() != stWorkflowCustom || !tt.HasText("Folding workflow") || !tt.HasText("Custom engine and reference layout") {
		t.Fatalf("workflow %q: %q", p.workflow(), tt.Texts())
	}
	p.gene = africaGene
	// two regions of the same RNA: CPLcofold, with a model it supports
	p.region1, p.model = "1-45", "CC06"
	p.applyWorkflow(stWorkflowLong)
	tt.Frame()
	if p.engine != engineCPLcofold || p.gene2 != africaGene || p.model != "DP09" || p.workflow() != stWorkflowLong {
		t.Fatalf("long: %q %q %q %q", p.engine, p.gene2, p.model, p.workflow())
	}
	for _, text := range []string{"Two local regions of the same RNA · CPLcofold",
		"With CPLfold selected, one-region presets run CPLfold; two-region presets run CPLcofold and preserve the selected reads, alpha, beta and pseudoknot settings. Same-RNA regions are folded as two excised fragments, with a strand break and no intervening reference sequence or artificial spacer."} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	// editing the regions keeps the preset
	p.region2 = "1086-1134"
	tt.Frame()
	if p.workflow() != stWorkflowLong || len(p.plan().problems) != 0 {
		t.Fatalf("regions edited: %q %q", p.workflow(), p.plan().problems)
	}
	// homodimer: region 2 copies region 1 and follows it
	p.applyWorkflow(stWorkflowHomodimer)
	tt.Frame()
	if p.region2 != "1-45" || !tt.HasText("Homodimer (overlap ≥ 5)") {
		t.Fatalf("homodimer region 2 %q", p.region2)
	}
	p.region1 = "10-60"
	tt.Frame()
	if p.region2 != "10-60" || p.workflow() != stWorkflowHomodimer {
		t.Fatalf("region 1 not mirrored: %q %q", p.region2, p.workflow())
	}
	// editing region 2 goes back to Custom
	p.region2 = "100-150"
	tt.Frame()
	if p.workflow() != stWorkflowCustom {
		t.Fatalf("region 2 edit kept %q", p.workflow())
	}
	// the web's checks of a preset's regions
	p.setup.workflow, p.region2 = stWorkflowHomodimer, "100-150"
	if pl := p.plan(); !slices.Contains(pl.problems, "The homodimer preset requires two copies of the same region; choose Custom for different coordinates.") {
		t.Fatalf("homodimer check %q", pl.problems)
	}
	p.setup.workflow = stWorkflowInter
	if pl := p.plan(); !slices.Contains(pl.problems, "Choose different RNAs for the intermolecular preset, or use the two-regions-of-the-same-RNA preset.") {
		t.Fatalf("intermolecular check %q", pl.problems)
	}
	p.setup.workflow = stWorkflowCustom
	tt.Frame()
	// intermolecular picks another RNA
	p.applyWorkflow(stWorkflowInter)
	if p.gene2 == "" || p.gene2 == p.gene || p.engine != engineCPLcofold {
		t.Fatalf("intermolecular region 2 RNA %q", p.gene2)
	}
	// one local region: CPLfold of region 1
	p.applyWorkflow(stWorkflowShort)
	tt.Frame()
	if p.engine != engineCPLfold || p.mode != "windows" || p.windows != "10-60" {
		t.Fatalf("short: %q %q %q", p.engine, p.mode, p.windows)
	}
	// changing the engine goes back to Custom, as the web
	p.engine = engineVienna
	p.engineChanged()
	tt.Frame()
	if p.workflow() != stWorkflowCustom || !tt.HasText("Long-ranged intramolecular") && !slices.Contains(stWorkflowLabels(false), "Long-ranged intramolecular") {
		t.Fatalf("engine change kept %q", p.workflow())
	}
	// ViennaRNA presets: COMRADES(ViennaRNA), two regions joined by the spacer
	p.applyWorkflow(stWorkflowHomodimer)
	tt.Frame()
	if p.engine != engineComrades || p.mode != "paired" || !p.homodimerOnly || p.gene2 != p.gene || p.region2 != p.region1 {
		t.Fatalf("ViennaRNA homodimer %q %q %v %q %q", p.engine, p.mode, p.homodimerOnly, p.region2, p.region1)
	}
	for _, text := range []string{"Homodimer (overlap ≥ 5)", "ViennaRNA presets use HYB-guided evidence. Short-range uses one region; long-range uses two regions of the same RNA. Intermolecular uses two different RNAs; homodimer uses two copies of one RNA and overlap score ≥ 5. Paired layouts include the original 100 nt spacer."} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "structure-workflow-vienna")
	p.applyWorkflow(stWorkflowLong)
	if p.homodimerOnly || p.mode != "paired" {
		t.Fatalf("ViennaRNA long %v %q", p.homodimerOnly, p.mode)
	}
	p.applyWorkflow(stWorkflowShort)
	tt.Frame()
	if p.mode != "windows" || p.engine != engineComrades {
		t.Fatalf("ViennaRNA short %q %q", p.mode, p.engine)
	}
	// unticking the homodimer filter is a change of layout too
	p.applyWorkflow(stWorkflowHomodimer)
	tt.Frame()
	p.homodimerOnly = false
	tt.Frame()
	if p.workflow() != stWorkflowCustom {
		t.Fatalf("filter change kept %q", p.workflow())
	}
}

// A mapped RNA keeps its HYB identity when its FASTA has a different name,
// length, and a non-ASCII base outside the selected two-strand windows.
func TestStructureMappedReferencePreview(t *testing.T) {
	a, f, tt, p := stSetupPage(t, 1600)
	r, _ := a.Project.GeneReference(africaGene)
	r.Sequences = []model.Sequence{{Name: "A", Length: 5}, {Name: "B", Length: 26}}
	r.RNASequences = []model.Sequence{{Name: "A", Length: 26}}
	p.engine, p.gene, p.gene2, p.region1, p.region2 = engineCPLcofold, "A", "A", "3-12", "17-26"
	p.setup.previewOpen = true
	f.ops["read_reference_sequence"] = map[string]any{"sequence": "🧬acgtcatgcaagNNNttggggaacc"}
	csSettle(a, tt)
	pl := p.plan()
	parts, _, loading, problem := p.prepared(pl, true)
	if len(pl.problems) != 0 || pl.length != 26 || pl.req.Regions[0].GeneID != "A" || pl.req.Regions[1].GeneID != "A" ||
		loading || problem != "" || len(parts) != 1 || parts[0].seq != "CGUCAUGCAAUUGGGGAACC" {
		t.Fatalf("mapped fold: %v, %v, %s, %v", pl, parts, problem, loading)
	}
	p.gene2 = "B" // explicitly unmatched, though the FASTA contains B
	if len(p.plan().problems) == 0 {
		t.Fatal("unmatched second RNA was accepted")
	}
	p.engine, p.mode, p.windows = engineVienna, "windows", "3-22"
	if _, _, _, problem := p.prepared(p.plan(), true); !strings.Contains(problem, "🧬") {
		t.Fatal("ordinary fold ignored the invalid flank:", problem)
	}
	qcFor(a).match.invalidate()
	f.ops["read_reference_sequence"] = map[string]any{"sequence": strings.Repeat("G", 26)}
	csSettle(a, tt)
	parts, _, _, problem = p.prepared(p.plan(), true)
	if len(parts) != 1 || parts[0].seq != strings.Repeat("G", 20) {
		t.Fatalf("old mapping still previewed: %v %s", parts, problem)
	}
}

// "Preview sequence": the prepared layout, the sequence, its FASTA and the
// local CPLfold inputs, as the web.
func TestStructurePreviewSequence(t *testing.T) {
	a, f, tt, p := stSetupPage(t, 1800)
	seq := strings.Repeat("ACGT", 10807/4) + "ACG"
	f.ops["read_reference_sequence"] = map[string]any{"ok": true, "sequence": strings.ToLower(seq)}
	rna := strings.ReplaceAll(seq, "T", "U")
	p.gene, p.mode, p.windows = africaGene, "windows", "1-60"
	csSettle(a, tt)
	if !tt.HasText("Preview sequence · " + africaGene + ":1–60") {
		t.Fatalf("no preview summary: %q", tt.Texts())
	}
	p.setup.previewOpen = true
	csSettle(a, tt)
	for _, text := range []string{"Prepared reference layout", africaGene + " 1–60 → prepared 1–60",
		" 1  ACGUACGUAC GUACGUACGU ACGUACGUAC", "31  GUACGUACGU ACGUACGUAC GUACGUACGU", "Copy sequence", "Download FASTA",
		"Download local inputs", "Local command",
		"bin/cplfold --sequence-file cplfold-input.fasta --bonus-matrix-file cplfold-bonus-matrix.tsv --beam 200 --delta 5 --max-phase1 1 --max-phase2 1 --model DP09 --beta 0 --alpha 0.5 --no-pseudoknot"} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "structure-preview")
	if err := tt.Click("Copy sequence"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != rna[:60] {
		t.Fatalf("clipboard %q", tt.Clipboard())
	}
	dir := t.TempDir()
	asked := pickSaveInto(t, dir)
	before := lastToast(a)
	if err := tt.Click("Download FASTA"); err != nil {
		t.Fatal(err)
	}
	waitToast(a, before)
	if got := readExport(t, asked); got != ">HYB2_Web_prepared_sequence\n"+rna[:60]+"\n" || filepath.Base((*asked)[0]) != "structure-input.fasta" {
		t.Fatalf("FASTA %q", got)
	}
	// local inputs before the fold: the FASTA, and the bonus matrix comes with the result
	before = lastToast(a)
	if err := tt.Click("Download local inputs"); err != nil {
		t.Fatal(err)
	}
	waitToast(a, before)
	if got := readExport(t, asked); filepath.Base((*asked)[1]) != "cplfold-input.fasta" || got != ">HYB2_Web_prepared_sequence\n"+rna[:60]+"\n" {
		t.Fatalf("local FASTA %q", got)
	}
	if toast := lastToast(a); !strings.Contains(toast, "The HYB bonus matrix is built when the fold runs") ||
		!strings.HasSuffix(toast, "bin/cplfold --sequence-file cplfold-input.fasta --beam 200 --delta 5 --max-phase1 1 --max-phase2 1 --model DP09 --beta 0 --no-pseudoknot") {
		t.Fatalf("toast %q", toast)
	}
	// two windows: one record each; bin/cplfold takes one
	p.windows = "1-60\n101-160"
	csSettle(a, tt)
	parts, _, _, _ := p.prepared(p.plan(), true)
	if len(parts) != 2 || !strings.HasPrefix(stFASTA(parts), ">HYB2_Web_prepared_sequence_1 "+africaGene+":1-60\n") || !tt.HasText(africaGene+":101–160") {
		t.Fatalf("windows %q", stFASTA(parts))
	}
	// CPLcofold: two strands, chain break after A
	p.engine, p.region1, p.region2, p.gene2 = engineCPLcofold, "1-45", "1086-1134", africaGene
	p.pseudoknot = true
	csSettle(a, tt)
	for _, text := range []string{"RNA A: 1–45 · RNA B: 46–94 · chain break after A (coordinates exclude &).",
		africaGene + " 1–45 → prepared 1–45", africaGene + " 1,086–1,134 → prepared 46–94", "31  GUACGUACGU ACGUA&CGUAC GUACGUACGU",
		"bin/cplcofold --sequence-file cplfold-input.fasta --mode pseudoknot --bonus-matrix-file cplfold-bonus-matrix.tsv --beam 200 --delta 5 --max-phase1 1 --max-phase2 1 --model DP09 --beta 0 --alpha 0.5"} {
		if !tt.HasText(text) {
			t.Fatalf("cofold lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "structure-preview-cofold")
	parts, _, _, _ = p.prepared(p.plan(), true)
	if want := ">HYB2_Web_RNA_A\n" + rna[:45] + "\n>HYB2_Web_RNA_B\n" + rna[1085:1134] + "\n"; stFASTA(parts) != want {
		t.Fatalf("cofold FASTA %q", stFASTA(parts))
	}
	// both strands, 600 + 401 nt, raise bin/cplfold's 1,000 nt guard
	p.region1, p.region2 = "1-600", "1001-1401"
	csSettle(a, tt)
	if !tt.HasText("HYB2_CPLFOLD_MAX_NT=1001 bin/cplcofold --sequence-file cplfold-input.fasta --mode pseudoknot ") {
		t.Fatalf("1001 nt cofold: %q", tt.Texts())
	}
	p.region1, p.region2 = "1-45", "1086-1134"
	// COMRADES of two regions: joined by the 100-nt A/U spacer
	p.engine, p.mode = engineComrades, "paired"
	csSettle(a, tt)
	if !tt.HasText("100 nt A/U spacer") || !tt.HasText(africaGene+" 1,086–1,134 → prepared 146–194") ||
		tt.HasText("Download local inputs") {
		t.Fatalf("paired: %q", tt.Texts())
	}
	parts, _, _, _ = p.prepared(p.plan(), true)
	if len(parts) != 1 || parts[0].seq != rna[:45]+strings.Repeat("A", 50)+strings.Repeat("U", 50)+rna[1085:1134] {
		t.Fatalf("paired sequence %+v", parts)
	}
	// a pasted sequence; nothing to fold yet waits
	p.engine, p.mode = engineCPLfold, "genome"
	p.setSource(stSourcePaste)
	csSettle(a, tt)
	if !tt.HasText("Waiting for a sequence") {
		t.Fatalf("no waiting state: %q", tt.Texts())
	}
	p.src.pasted = "acgtacgtacgtacgt"
	csSettle(a, tt)
	if !tt.HasText("Preview sequence · Pasted sequence") || !tt.HasText(" 1  ACGUACGUAC GUACGU") {
		t.Fatalf("pasted: %q", tt.Texts())
	}
}

// The web's local command line, for CPLfold and CPLcofold.
func TestStructureLocalCommand(t *testing.T) {
	run := stLocalRun{guided: true, beam: 50, phase1: 5, delta: 2.5, beta: 0.15, alpha: 0.25, model: "rE", pseudoknot: true}
	if got := stLocalCommand(run, "my input.fasta", stLocalMatrix); got != "bin/cplfold --sequence-file 'my input.fasta' --bonus-matrix-file cplfold-bonus-matrix.tsv --beam 50 --delta 2.5 --max-phase1 5 --max-phase2 1 --model RE --beta 0.15 --alpha 0.25" {
		t.Fatalf("guided %q", got)
	}
	run = stLocalRun{cofold: true, beam: 20, phase1: 3, delta: 5, model: "DP03"}
	if got := stLocalCommand(run, stLocalFASTA, stLocalMatrix); got != "bin/cplcofold --sequence-file cplfold-input.fasta --mode standard --beam 20 --delta 5 --max-phase1 3 --max-phase2 1 --model DP03 --beta 0 --no-pseudoknot" {
		t.Fatalf("cofold %q", got)
	}
	// bin/cplfold refuses more than 1,000 nt unless HYB2_CPLFOLD_MAX_NT raises its guard
	run.length = 1000
	if got := stLocalCommand(run, stLocalFASTA, stLocalMatrix); !strings.HasPrefix(got, "bin/cplcofold ") {
		t.Fatalf("1000 nt %q", got)
	}
	run.length = 1001
	if got := stLocalCommand(run, stLocalFASTA, stLocalMatrix); got != "HYB2_CPLFOLD_MAX_NT=1001 bin/cplcofold --sequence-file cplfold-input.fasta --mode standard --beam 20 --delta 5 --max-phase1 3 --max-phase2 1 --model DP03 --beta 0 --no-pseudoknot" {
		t.Fatalf("1001 nt %q", got)
	}
	if got := stSequenceLines("ACGUACGUACG", 4, 50); !slices.Equal(got, []string{" 1  ACGU&ACGUAC G"}) {
		t.Fatalf("lines %q", got)
	}
	// the web's 50 a line
	if got := stSequenceLines(strings.Repeat("A", 120), 0, 50); len(got) != 3 || got[1] != " 51  "+strings.TrimSpace(strings.Repeat("AAAAAAAAAA ", 5)) {
		t.Fatalf("50 a line %q", got)
	}
}

// A CPLfold result's "Local inputs…": the window's FASTA and bonus matrix,
// and the command from the result's own settings.
func TestStructureResultLocalInputs(t *testing.T) {
	a, f := openCS(t)
	read := fixture(t, "structure_read.json")
	record := read["records"].([]any)[0].(map[string]any)
	matrix := filepath.Join(t.TempDir(), "bonus_matrix.tsv")
	if err := os.WriteFile(matrix, []byte("# HYB2 CPLfold bonus matrix\n1\t20\t0.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record["candidates"] = []any{map[string]any{"rank": 0, "type": "phase1", "topology": "nested", "structure": record["dbn"], "pairs": 1055}}
	record["bonus_matrix_path"] = matrix
	f.structures[structA] = read
	tt := ui.NewTester(a.View, 1100, 1600)
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	// the result's samples, shown as the web's provenance
	if !tt.HasText("HYB evidence from AFR_REP1 + AFR_REP2.") && !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "HYB evidence from ") }) {
		t.Fatalf("no provenance: %q", tt.Texts())
	}
	dir := t.TempDir()
	asked := pickSaveInto(t, dir)
	before := lastToast(a)
	if err := tt.Click("Local inputs…"); err != nil {
		t.Fatal(err)
	}
	waitToast(a, before)
	seq := record["sequence"].(string)
	if got := readExport(t, asked); got != ">HYB2_Web_prepared_sequence\n"+seq+"\n" {
		t.Fatalf("FASTA %.80q", got)
	}
	if got, err := os.ReadFile(filepath.Join(dir, stLocalMatrix)); err != nil || string(got) != "# HYB2 CPLfold bonus matrix\n1\t20\t0.5\n" {
		t.Fatalf("matrix %q %v", got, err)
	}
	// the result's settings: beam 100, one phase-1 candidate, pseudoknots allowed;
	// the 3,060 nt window raises bin/cplfold's 1,000 nt guard
	want := "Run: HYB2_CPLFOLD_MAX_NT=3060 bin/cplfold --sequence-file cplfold-input.fasta --bonus-matrix-file cplfold-bonus-matrix.tsv --beam 100 --delta 5 --max-phase1 1 --max-phase2 1 --model DP09 --beta 0 --alpha 0.5"
	if toast := lastToast(a); !strings.Contains(toast, "CPLfold local inputs saved in "+dir) || !strings.HasSuffix(toast, want) {
		t.Fatalf("toast %q", toast)
	}

	// the evidence chosen now is not the result's: the web's warning
	var other string
	for _, o := range a.SelectionOptions() {
		if ids := a.SelectionIDs(o.Key); len(ids) > 0 && !slices.Contains(ids, "AFR_REP1") {
			other = o.Key
			break
		}
	}
	if other == "" {
		t.Fatal("no other evidence")
	}
	p.evidence = other
	tt.Frame()
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool {
		return strings.HasPrefix(s, "The HYB evidence has changed since this fold: it would now come from ") &&
			strings.HasSuffix(s, ". Run again to update the structure.")
	}) {
		t.Fatalf("no evidence warning: %q", tt.Texts())
	}
	snapshot(t, tt, "structure-result-provenance")
	// sequence only in the form: nothing to compare
	p.seqOnly = true
	tt.Frame()
	if slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "The HYB evidence has changed") }) {
		t.Fatal("warning for a sequence-only form")
	}
}

// The form follows the top bar's RNA and Samples when they change; a choice
// made on the form afterwards stays until the top bar changes again.
func TestStructureFollowsTopBar(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if p.gene != a.Selected.Gene || p.evidence != a.Selected.Key {
		t.Fatalf("form %q %q, top bar %q %q", p.gene, p.evidence, a.Selected.Gene, a.Selected.Key)
	}
	var keys []string
	for _, o := range a.SelectionOptions() {
		if o.Key != a.Selected.Key && len(a.SelectionIDs(o.Key)) > 0 {
			keys = append(keys, o.Key)
		}
	}
	if len(keys) < 2 {
		t.Fatalf("selections %q", keys)
	}
	// the top bar changes while the page is shown
	a.Selected.Key = keys[0]
	tt.Frame()
	if p.evidence != keys[0] {
		t.Fatalf("not followed: %q", p.evidence)
	}
	// the form's own choice stays, also after leaving the page
	p.evidence = keys[1]
	tt.Frame()
	a.Navigate("explore", nil)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	if p.evidence != keys[1] {
		t.Fatalf("form choice lost: %q", p.evidence)
	}
	// the RNA too (one RNA in this project: as if another was followed last)
	p.topGene, p.gene = "another RNA", ""
	tt.Frame()
	if p.gene != a.Selected.Gene || p.topGene != a.Selected.Gene {
		t.Fatalf("gene %q", p.gene)
	}
}
