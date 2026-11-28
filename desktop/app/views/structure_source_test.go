package views

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

const (
	stAllowKnots = "Allow pseudoknots: run Phase 2 to search for crossing base pairs"
	stNoKnots    = "Secondary structure only: Phase 1 candidates, no pseudoknot search"
)

func TestStructureSequenceSourceMinimumForm(t *testing.T) {
	for _, injected := range []bool{false, true} {
		pastedMin, sequenceMin := 5, 1
		if injected {
			pastedMin, sequenceMin = 8, 3
		}
		// pasted and record sequences are folded from the sequence alone
		for _, tc := range []struct{ name, source, input, normalized string }{
			{"seven", stSourcePaste, "\ufeff>seven & title\r\n1 a cgT a cg\n", "ACGUACG"},
			{"four", stSourcePaste, ">long header does not count\n1 a c g t\n", "ACGU"},
			{"boundary", stSourcePaste, ">boundary\n1 " + strings.Repeat("a ", pastedMin), strings.Repeat("A", pastedMin)},
			{"empty", stSourceRecord, "", ""},
			{"formatting-only", stSourceRecord, " \r\n\t ", ""},
			{"singleton", stSourceRecord, " \r\n t ", "U"},
			{"two", stSourceRecord, "a c", "AC"},
			{"boundary", stSourceRecord, strings.Repeat(" a ", sequenceMin), strings.Repeat("A", sequenceMin)},
		} {
			t.Run(fmt.Sprintf("injected=%t/%s/%s", injected, tc.source, tc.name), func(t *testing.T) {
				if injected {
					stInjectFoldTextMinimums(t, 12, pastedMin, sequenceMin)
				}
				a, f, tt, p := stSetupPage(t, 2200)
				stPick(t, tt, engineVienna, false)
				minimum, label := pastedMin, "The pasted sequence"
				mustClick(t, tt, "Reference region")
				if tc.source == stSourceRecord {
					minimum, label = sequenceMin, "The record's sequence"
					r := hybridRow{Name: "short-record", Sequence: tc.input, Reads: 1, Gene1: africaGene, Start1: 1, End1: 4,
						Gene2: africaGene, Start2: 8, End2: 11}
					f.queries["analysis_hybrids"] = core.Obj{"rows": []hybridRow{r}}
					mustClick(t, tt, "HYB record sequence")
					csSettle(a, tt)
					mustClick(t, tt, "Choose a record")
					mustClick(t, tt, stHybridLabel(r))
				} else {
					mustClick(t, tt, "Paste sequence")
					mustClick(t, tt, "Paste RNA sequence")
					tt.Type(tc.input)
				}
				tt.Frame()
				problem := ""
				if tc.source == stSourceRecord && tc.input == "" {
					problem = "This record's sequence cannot be read"
				} else if len(tc.normalized) < minimum {
					problem = fmt.Sprintf("%s: at least %d nt.", label, minimum)
				}
				if problem != "" && !tt.HasText(problem) {
					t.Errorf("missing precheck %q; got %v", problem, p.plan().problems)
				}
				mustClick(t, tt, "Run fold")
				csSettle(a, tt)
				started := f.startedKinds()
				t.Logf("source=%s input=%q normalized=%q minimum=%d; StartTask calls=%d; problems=%q",
					tc.source, tc.input, tc.normalized, minimum, len(started), p.plan().problems)
				if problem != "" {
					if len(started) != 0 {
						t.Fatal("short normalized sequence submitted a task")
					}
					return
				}
				if len(started) != 1 || started[0].Source == nil || *started[0].Source != tc.source ||
					started[0].Sequence == nil || *started[0].Sequence != tc.normalized || started[0].SequenceTwo != nil ||
					started[0].Mode == nil || *started[0].Mode != "genome" {
					t.Fatalf("valid normalized sequence was not submitted unchanged: %+v", started)
				}
				t.Logf("submitted sequence=%q, source=%s, mode=%s", *started[0].Sequence, *started[0].Source, *started[0].Mode)
			})
		}
	}
}

func TestStructurePastedStrandMinimumForm(t *testing.T) {
	for _, minimum := range []int{1, 3} {
		for _, tc := range []struct{ name, first, second, one, two string }{
			{"four-plus-four", ">RNA A\n1 a c g t", ">RNA B\r\n1 g c a t", "ACGU", "GCAU"},
			{"joined-four-plus-four", ">title & name\r1 a c g t & g c a t", "", "ACGU", "GCAU"},
			{"singletons", "a", "t", "A", "U"},
			{"short-A", "a c", "g c a t", "AC", "GCAU"},
			{"short-B", "a c g t", "g c", "ACGU", "GC"},
			{"boundary", strings.Repeat("a ", minimum), strings.Repeat("t ", minimum), strings.Repeat("A", minimum), strings.Repeat("U", minimum)},
			{"empty-A", ">empty\n1 ", "a c g t", "", "ACGU"},
			{"empty-B", "a c g t", ">empty\n1 ", "ACGU", ""},
			{"empty-joined-B", "a c g t &", "", "ACGU", ""},
		} {
			t.Run(fmt.Sprintf("min%d/%s", minimum, tc.name), func(t *testing.T) {
				if minimum != 1 {
					stInjectFoldTextMinimums(t, 12, 8, minimum)
				}
				a, f, tt, p := stSetupPage(t, 2200)
				stPick(t, tt, engineRNAcofold, true)
				mustClick(t, tt, "Reference region")
				mustClick(t, tt, "Paste sequence")
				mustClick(t, tt, "RNA A sequence (strand A)")
				tt.Type(tc.first)
				mustClick(t, tt, "RNA B sequence (strand B)")
				tt.Type(tc.second)
				tt.Frame()
				problem := ""
				switch {
				case tc.one == "" || tc.two == "":
					problem = "Paste RNA A and RNA B, or A&B in the RNA A box."
				case len(tc.one) < minimum:
					problem = fmt.Sprintf("RNA A: at least %d nt.", minimum)
				case len(tc.two) < minimum:
					problem = fmt.Sprintf("RNA B: at least %d nt.", minimum)
				}
				if problem != "" && !tt.HasText(problem) {
					t.Errorf("missing precheck %q; got %v", problem, p.plan().problems)
				}
				mustClick(t, tt, "Run fold")
				csSettle(a, tt)
				started := f.startedKinds()
				t.Logf("input=%q/%q normalized=%q/%q minimum=%d; StartTask calls=%d; problems=%q",
					tc.first, tc.second, tc.one, tc.two, minimum, len(started), p.plan().problems)
				if problem != "" {
					if len(started) != 0 {
						t.Fatal("short or empty strand submitted a task")
					}
					return
				}
				if len(started) != 1 || started[0].Sequence == nil || *started[0].Sequence != tc.one ||
					started[0].SequenceTwo == nil || *started[0].SequenceTwo != tc.two ||
					started[0].Source == nil || *started[0].Source != stSourcePaste || started[0].Mode == nil || *started[0].Mode != "cofold" {
					t.Fatalf("valid strands were not submitted unchanged: %+v", started)
				}
				t.Logf("submitted sequence=%q, sequence_two=%q, mode=%s", *started[0].Sequence, *started[0].SequenceTwo, *started[0].Mode)
			})
		}
	}
}

// Pasted inputs keep every selected sample, but the runtime still needs one
// shared reference. Both submission routes must reject an invalid selection.
func TestStructureSequenceReferenceAdmission(t *testing.T) {
	const diagnosis = "The selected samples must share one reference. Choose samples mapped to the same reference."
	all := []string{"AFR_REP1", "AFR_REP2", "ZBRA_REP1", "ZBRA_REP2"}
	afr := []string{"AFR_REP1", "AFR_REP2"}
	for _, tc := range []struct {
		name     string
		shortcut bool
	}{
		{"mixed/button", false},
		{"mixed/shortcut", true},
		{"pinned-mixed", false},
		{"missing-reference", false},
		{"unknown-reference", true},
		{"pinned-same-reference", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, f := openCS(t)
			// Keep the four indexed HYB examples, without the extra FASTQ subset.
			for _, id := range []string{"SUB_REP1", "SUB_REP2"} {
				s := a.Project.Sample(id)
				s.Ready, s.Outputs.Index = false, ""
			}
			a.Selected.Key = AllSelection
			tt := ui.NewTester(a.View, 1100, 2200)
			a.Navigate("structure", nil)
			csSettle(a, tt)
			p := a.PageFor("structure").(*structurePage)
			if pl := p.plan(); len(pl.problems) != 0 || !slices.Equal(pl.req.SampleIDs, afr) {
				t.Fatalf("reference-region filtering changed: ids=%v problems=%q", pl.req.SampleIDs, pl.problems)
			}
			stPick(t, tt, engineVienna, false)
			mustClick(t, tt, "Reference region")
			mustClick(t, tt, "Paste sequence")
			mustClick(t, tt, "Paste RNA sequence")
			sequence := "GGGAAAACCC"
			tt.Type(sequence)
			ids := all
			switch tc.name {
			case "pinned-mixed":
				p.evidence = "c:AFR"
				ids = []string{"ZBRA_REP2", "AFR_REP1"}
				p.pinSamples(ids)
			case "missing-reference":
				p.evidence, ids = "c:AFR", afr
				a.Project.Sample("AFR_REP2").ReferenceID = ""
			case "unknown-reference":
				p.evidence, ids = "c:AFR", afr
				for _, id := range ids {
					a.Project.Sample(id).ReferenceID = "UNKNOWN"
				}
			case "pinned-same-reference":
				ids = []string{"AFR_REP2", "AFR_REP1"}
				p.pinSamples(ids)
			}
			tt.Frame()
			if pl := p.plan(); !slices.Equal(pl.ids, ids) || !slices.Equal(pl.req.SampleIDs, ids) {
				t.Fatalf("pasted input silently changed its samples: ids=%v request=%v want=%v", pl.ids, pl.req.SampleIDs, ids)
			}
			submit := func() {
				t.Helper()
				if tc.shortcut {
					mustClick(t, tt, "Paste RNA sequence")
					tt.Key(ui.Cmd, ui.KeyEnter)
				} else {
					mustClick(t, tt, "Run fold")
				}
				csSettle(a, tt)
			}
			if tc.name != "pinned-same-reference" {
				pl := p.plan()
				if !slices.Equal(pl.problems, []string{diagnosis}) || !slices.Contains(tt.Texts(), "• "+diagnosis) {
					t.Errorf("expected the exact reference diagnosis in the form; problems=%q", pl.problems)
				}
				submit()
				started := f.startedKinds()
				t.Logf("rejected ids=%v shortcut=%t problems=%q StartTask calls=%d", ids, tc.shortcut, pl.problems, len(started))
				if len(started) != 0 {
					t.Fatalf("invalid sample references submitted %d task(s)", len(started))
				}
				if !strings.HasPrefix(tc.name, "mixed/") {
					return
				}
				// Changing only evidence recovers; no sequence edit or sample filtering.
				mustClick(t, tt, "Samples for this fold")
				mustClick(t, tt, "AFR · 2 datasets merged")
				csSettle(a, tt)
				ids = afr
			}
			if pl := p.plan(); len(pl.problems) != 0 || tt.HasText(diagnosis) {
				t.Fatalf("same-reference selection did not recover: %q", pl.problems)
			}
			submit()
			mode, source, gene, engine := "genome", stSourcePaste, "pasted_sequence", engineVienna
			workers, mem := uint32(1), uint64(60)
			want := core.TaskStartRequest{ProjectPath: a.Project.Path, Kind: "structure", SampleIDs: ids,
				GeneID: &gene, Mode: &mode, Source: &source, Engine: &engine, Sequence: &sequence, Workers: &workers, MemMB: &mem,
				Parameters: map[string]any{"temperature": float64(37), "min_loop": 3, "hard_pairs": [][2]int{}}}
			started := f.startedKinds()
			if len(started) != 1 || !reflect.DeepEqual(started[0], want) {
				t.Fatalf("same-reference request changed: got %+v, want %+v", started, want)
			}
			t.Logf("admitted exact request: sample_ids=%v sequence=%q engine=%s source=%s mode=%s parameters=%v workers=%d mem_mb=%d",
				started[0].SampleIDs, *started[0].Sequence, *started[0].Engine, *started[0].Source, *started[0].Mode,
				started[0].Parameters, *started[0].Workers, *started[0].MemMB)
		})
	}
}

// The web's "Paste sequence" source of a sequence-only fold: one strand
// for ViennaRNA MFE, read as the web reads it, checked, and named in the
// task. A guided fold reads a reference region only.
func TestStructurePastedSequence(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1300)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if pl := p.plan(); pl.source != "" || pl.req.Source != nil || tt.HasText("Sequence source") {
		t.Fatalf("a guided reference region by default: %q %v", pl.source, pl.req.Source)
	}
	mustClick(t, tt, "Sequence only")
	tt.Frame()
	if p.engine != engineVienna || !tt.HasText("Sequence source") {
		t.Fatalf("sequence only: %q %q", p.engine, tt.Texts())
	}
	p.mode, p.windows = "windows", "1-400"
	p.setSource(stSourcePaste)
	if p.mode != "genome" {
		t.Fatalf("a pasted sequence is folded whole, mode %q", p.mode)
	}
	tt.Frame()
	mustHave(t, tt, "• Paste the RNA sequence to fold (A, C, G, U; T is read as U).")
	if tt.HasText("RNA to fold") || tt.HasText("What to fold") {
		t.Fatal("reference inputs shown for a pasted sequence")
	}
	for text, want := range map[string]string{
		">first\nACGUACGUAC\n>second\nACGUACGUAC": "Paste only one FASTA record per sequence box.",
		"ACGUNNXACGU":     "The pasted sequence: only A, C, G and U are accepted (T is read as U); remove N, X.",
		"acgu":            "The pasted sequence: at least 5 nt.",
		"ACGU&ACGUACGUAC": "The pasted sequence: only A, C, G and U are accepted (T is read as U); remove &.",
	} {
		p.src.pasted = text
		if pl := p.plan(); !slices.Contains(pl.problems, want) {
			t.Fatalf("%q: %q", text, pl.problems)
		}
	}
	// a FASTA header, line breaks, lower case and T, as the web's normaliseFoldSequence
	p.src.pasted = "\ufeff>my RNA\r1 ggg aaa\r\n7 cccTTTgggaaaccc\n"
	pl := p.plan()
	if len(pl.problems) != 0 || *pl.req.Sequence != "GGGAAACCCUUUGGGAAACCC" || *pl.req.Source != "paste" ||
		*pl.req.Mode != "genome" || *pl.req.GeneID != "pasted_sequence" || pl.req.SequenceTwo != nil || pl.memMB == 0 ||
		len(pl.req.Windows) != 0 || pl.source != "pasted sequence, 21 nt" || *pl.req.Engine != "viennarna" || pl.pk {
		t.Fatalf("plan %+v %q %q", pl.req, pl.problems, pl.source)
	}
	tt.Frame()
	mustHave(t, tt, "the pasted sequence, 21 nt", "• ViennaRNA MFE folds the sequence alone; hard pairs are positions on it.")
	if tt.HasText("Structure topology") || tt.HasText("Allow pseudoknots") {
		t.Fatal("CPLfold topology shown for ViennaRNA MFE")
	}
	snapshot(t, tt, "structure-paste")
	a.theme = "system" // the default light preference overrides the tester's appearance
	tt.SetDark(true)
	snapshot(t, tt, "structure-paste-dark")
	tt.SetDark(false)
	mustClick(t, tt, "Run fold")
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || *started[0].Sequence != "GGGAAACCCUUUGGGAAACCC" || *started[0].Engine != "viennarna" {
		t.Fatalf("started %+v", started)
	}
	if got := stSourceTitle(engineVienna, pl.source); got != "Structure (ViennaRNA) · pasted sequence, 21 nt" {
		t.Fatalf("title %q", got)
	}
	// hard pairs are positions on the sequence; its length limit
	p.hardPairs = "1-21"
	if pl := p.plan(); len(pl.problems) != 0 || len(pl.req.Parameters.(map[string]any)["hard_pairs"].([][2]int)) != 1 {
		t.Fatalf("hard pairs %+v %q", pl.req, pl.problems)
	}
	p.hardPairs = "1-40"
	if pl := p.plan(); !slices.Contains(pl.problems, "Hard pair 1-40 is not inside a folded window.") {
		t.Fatalf("hard pair outside %q", pl.problems)
	}
	p.hardPairs, p.src.pasted = "", strings.Repeat("ACGU", stViennaMaxNT/4+1)
	if pl := p.plan(); !slices.Contains(pl.problems, "ViennaRNA MFE folds at most 30,000 nt; paste a shorter sequence.") {
		t.Fatalf("vienna limit %q", pl.problems)
	}
	// a prefill names reference regions again
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"gene": africaGene,
		"windows": []map[string]any{{"start": 3000, "end": 3400}}}})
	csSettle(a, tt)
	if p.src.kind != stSourceReference || p.mode != "windows" {
		t.Fatalf("prefill source %q mode %q", p.src.kind, p.mode)
	}
	// a guided algorithm reads a reference region only
	p.setSource(stSourcePaste)
	tt.Frame()
	mustClick(t, tt, "Guided by HYB chimeras")
	tt.Frame()
	if p.engine != engineCPLfold || p.src.kind != stSourceReference || tt.HasText("Sequence source") {
		t.Fatalf("guided: engine %q source %q", p.engine, p.src.kind)
	}
}

// Two pasted strands of a sequence-only fold for RNAcofold, as the web's
// RNA A and RNA B boxes.
func TestStructurePastedStrands(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1300)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	mustClick(t, tt, "Sequence only")
	tt.Frame()
	mustClick(t, tt, "Two regions")
	tt.Frame()
	if p.engine != engineRNAcofold {
		t.Fatalf("sequence only, two regions: %q", p.engine)
	}
	p.setSource(stSourcePaste)
	tt.Frame()
	if tt.HasText("Allow pseudoknots") || tt.HasText("Structure topology") {
		t.Fatal("RNAcofold folds without pseudoknots")
	}
	if !tt.HasText("• Paste RNA A and RNA B, or A&B in the RNA A box.") || tt.HasText("Region 2 (strand B)") {
		t.Fatalf("texts %q", tt.Texts())
	}
	p.src.pasted, p.src.pastedTwo = "GGGGAAAACCCC", "ggttttcccc"
	pl := p.plan()
	if len(pl.problems) != 0 || *pl.req.Mode != "cofold" || *pl.req.Engine != "viennarna" || *pl.req.Sequence != "GGGGAAAACCCC" ||
		*pl.req.SequenceTwo != "GGUUUUCCCC" || len(pl.req.Regions) != 0 || pl.span() != "12 + 10 nt" || pl.memMB == 0 ||
		pl.source != "pasted RNA A + RNA B, 12 + 10 nt" || *pl.req.GeneID != "pasted" || pl.pk {
		t.Fatalf("plan %+v %q %q", pl.req, pl.problems, pl.source)
	}
	if params := pl.req.Parameters.(map[string]any); len(params) != 2 || params["temperature"] != float64(37) || params["min_loop"] != 3 {
		t.Fatalf("parameters %v", params)
	}
	tt.Frame()
	mustHave(t, tt, "2 pasted strands, 12 + 10 nt", "RNAcofold, 37 °C, minimum loop 3",
		"• RNAcofold folds the two strands from their sequences alone: the chimeric reads are not used.")
	snapshot(t, tt, "structure-paste-rnacofold")
	mustClick(t, tt, "Run fold")
	csSettle(a, tt)
	if started := f.startedKinds(); len(started) != 1 || *started[0].Mode != "cofold" || *started[0].SequenceTwo != "GGUUUUCCCC" {
		t.Fatalf("started %+v", started)
	}
	if got := stSourceTitle(engineRNAcofold, pl.source); got != "Structure (RNAcofold) · pasted RNA A + RNA B, 12 + 10 nt" {
		t.Fatalf("title %q", got)
	}
	// A&B in the first box
	p.src.pasted, p.src.pastedTwo = ">label & 1\r1 GGGGAAAACCCC&ggttttcccc", ""
	if pl := p.plan(); len(pl.problems) != 0 || *pl.req.SequenceTwo != "GGUUUUCCCC" {
		t.Fatalf("A&B %+v %q", pl.req, pl.problems)
	}
	t.Run("narrowed catalog rejects normalized T", func(t *testing.T) {
		original := stKernels.FoldText.Alphabet
		t.Cleanup(func() { stKernels.FoldText.Alphabet = original })
		stKernels.FoldText.Alphabet = "ACG"
		if pl := p.plan(); !slices.ContainsFunc(pl.problems, func(s string) bool { return strings.Contains(s, "remove U") }) {
			t.Fatalf("expected invalid U in RNA B: %q", pl.problems)
		}
	})
	p.src.pastedTwo = "ACGU"
	if pl := p.plan(); !slices.Contains(pl.problems, "RNA A: only A, C, G and U are accepted (T is read as U); remove &.") ||
		len(pl.problems) != 1 { // four bases are valid for the independent RNA B strand
		t.Fatalf("problems %q", pl.problems)
	}
	// both strands count towards ViennaRNA's longest sequence
	p.src.pasted, p.src.pastedTwo = strings.Repeat("ACGU", stViennaMaxNT/4), "GGUUUUCCCC"
	if pl := p.plan(); !slices.Contains(pl.problems, "Both strands are 30,010 nt; RNAcofold folds at most 30,000 nt in all.") {
		t.Fatalf("length %q", pl.problems)
	}
	// a HYB record is not two strands: RNAcofold switches to pasting, as the web
	p.setSource(stSourceRecord)
	p.engineChanged()
	if p.src.kind != stSourcePaste {
		t.Fatalf("source %q", p.src.kind)
	}
}

// The web's "HYB record sequence" source: the samples' records, most
// supported first, with their sequences.
func TestStructureRecordSequence(t *testing.T) {
	a, f := openCS(t)
	f.queries["analysis_hybrids"] = fixture(t, "explore_hybrids.json")
	tt := ui.NewTester(a.View, 1100, 1300)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	stPick(t, tt, engineVienna, false)
	p.setSource(stSourceRecord)
	csSettle(a, tt)
	if len(p.src.records) == 0 || p.src.recordsErr != "" {
		t.Fatalf("records %d %q", len(p.src.records), p.src.recordsErr)
	}
	// read once for these samples
	tt.Frame()
	if n := csCountCalls(f, "query:analysis_hybrids"); n != 1 {
		t.Fatalf("records read %d times", n)
	}
	if pl := p.plan(); !slices.Contains(pl.problems, "Choose the HYB record whose sequence to fold.") {
		t.Fatalf("problems %q", pl.problems)
	}
	r := p.src.records[0]
	p.src.record = &r
	tt.Frame()
	for _, text := range []string{"• A HYB record's sequence is one chimeric read, both arms joined: not necessarily a whole RNA or a region of it.",
		"the record's sequence, 40 nt"} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "structure-record")
	pl := p.plan()
	if len(pl.problems) != 0 || *pl.req.Source != "record" || *pl.req.GeneID != "record_100001_1" ||
		*pl.req.Sequence != "CAGAUUUUCAUAUUAUGCAGAAAAUCUACUUCGCCUGAUA" || pl.source != "HYB record 100001_1, 40 nt" {
		t.Fatalf("plan %+v %q %q", pl.req, pl.problems, pl.source)
	}
	// a record whose .hyb line cannot be read any more
	r.Sequence = ""
	if pl := p.plan(); len(pl.problems) != 1 || !strings.HasPrefix(pl.problems[0], "This record's sequence cannot be read") {
		t.Fatalf("problems %q", pl.problems)
	}
}

// "Fold sequence" in Explore sends a record: Structure switches to the
// record source and chooses it, offering it first when it is not among the
// most supported records.
func TestStructureRecordPrefill(t *testing.T) {
	a, f := openCS(t)
	f.queries["analysis_hybrids"] = fixture(t, "explore_hybrids.json")
	tt := ui.NewTester(a.View, 1100, 1300)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	stPick(t, tt, engineVienna, false)
	p.setSource(stSourceRecord)
	csSettle(a, tt)
	listed := p.src.records[1]
	// from a guided two-region form: the record is one strand, sequence only
	stPick(t, tt, engineCPLcofold, true)
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"record": listed, "source": "an interaction record"}})
	csSettle(a, tt)
	if p.src.kind != stSourceRecord || p.engine != engineVienna || p.src.record == nil || p.src.record.Name != listed.Name {
		t.Fatalf("source %q engine %q record %+v", p.src.kind, p.engine, p.src.record)
	}
	mustHave(t, tt, "Prefilled from an interaction record with the record's own sequence, folded with ViennaRNA MFE from the sequence alone.")
	other := listed
	other.ID, other.Name, other.Sequence = 99999, "not_listed", "GGGGAAACCCCAAAGGGGAAACCCC"
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"record": other}})
	csSettle(a, tt)
	if p.src.record == nil || p.src.record.Name != "not_listed" || p.src.records[0].Name != "not_listed" {
		t.Fatalf("record %+v first %q", p.src.record, p.src.records[0].Name)
	}
	if pl := p.plan(); len(pl.problems) != 0 || *pl.req.Sequence != other.Sequence {
		t.Fatalf("plan %q", pl.problems)
	}
}

// A result of a pasted or record sequence is listed by its source and
// exports under its label.
func TestStructurePastedResult(t *testing.T) {
	e := &structureEntry{GeneID: "pasted_sequence", Mode: "genome",
		Parameters: map[string]any{"engine": "CPLfold", "evidence": "none", "sequence_source": "paste", "sequence_label": "pasted_sequence"},
		Summary:    map[string]any{"length": 21.0, "pairs": 6.0}}
	if got := stWhat(e); got != "pasted sequence · 21 nt" {
		t.Fatalf("what %q", got)
	}
	e.Mode, e.Windows = "windows", nil
	e.Parameters = map[string]any{"sequence_source": "record", "sequence_label": "record_100001_1",
		"sequence_mapped_to": []any{africaGene + ":40-60"}}
	if got := stWhat(e); got != "HYB record 100001_1, found at "+africaGene+":40-60 · 21 nt" {
		t.Fatalf("mapped %q", got)
	}
	e.Mode, e.Parameters = "cofold", map[string]any{"sequence_source": "paste"}
	if got := stWhat(e); got != "pasted RNA A + RNA B · 21 nt" {
		t.Fatalf("strands %q", got)
	}
	records := []structureRecord{{WindowStart: 1, WindowEnd: 12, Sequence: "GGGAAAUCCCAA", DBN: "(((....)))..", Energy: nil}}
	if bpseq := stBPSEQText("pasted_sequence", records); !strings.HasPrefix(bpseq, "# pasted_sequence:1-12; energy NA\n1 G 10\n") {
		t.Fatalf("bpseq %q", bpseq)
	}
	if ct := stCTText("pasted_sequence", records); !strings.Contains(ct, "pasted_sequence") || strings.Count(ct, "\n") != 13 {
		t.Fatalf("ct %q", ct)
	}
}
