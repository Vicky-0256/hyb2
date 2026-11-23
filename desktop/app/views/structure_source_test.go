package views

import (
	"fmt"
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
		for _, engine := range []string{engineCPLfold, engineVienna, engineComrades} {
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
				t.Run(fmt.Sprintf("injected=%t/%s/%s/%s", injected, engine, tc.source, tc.name), func(t *testing.T) {
					if injected {
						stInjectFoldTextMinimums(t, 12, pastedMin, sequenceMin)
					}
					a, f, tt, p := stSetupPage(t, 2200)
					if engine == engineVienna {
						mustClick(t, tt, "ViennaRNA MFE, the sequence alone (optional hard pairs)")
					} else if engine == engineComrades {
						mustClick(t, tt, "COMRADES(ViennaRNA or mfold), guided by RNAcofold of the chimeras")
					}
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
					t.Logf("engine=%s source=%s input=%q normalized=%q minimum=%d; StartTask calls=%d; problems=%q",
						engine, tc.source, tc.input, tc.normalized, minimum, len(started), p.plan().problems)
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
				mustClick(t, tt, "CPLcofold, two regions as two strands (one RNA or two RNAs)")
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

// A source change keeps the selected topology; changing it on the new source
// updates the check, local command, budget gate and the task Run fold submits.
func TestStructureSequenceSourceTopology(t *testing.T) {
	// Long enough that the two claims differ even after the 4 GiB task minimum.
	seq := strings.Repeat("ACGU", 1250)
	free := core.StructureClaimMB(len(seq), false, 1)
	knotted := core.StructureClaimMB(len(seq), true, 1)
	if free >= knotted {
		t.Fatalf("test needs different memory claims: %d >= %d", free, knotted)
	}
	for _, source := range []string{stSourcePaste, stSourceRecord} {
		for _, allow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pseudoknots=%t", source, allow), func(t *testing.T) {
				a, f, tt, p := stSetupPage(t, 2200)
				p.budgetMB = free
				if !allow {
					mustClick(t, tt, stAllowKnots)
				}
				mustClick(t, tt, "Reference region")
				if source == stSourcePaste {
					mustClick(t, tt, "Paste sequence")
					mustClick(t, tt, "Paste RNA sequence")
					tt.Type(seq)
					mustClick(t, tt, "Preview sequence · Pasted sequence")
				} else {
					r := hybridRow{Name: "test-read", Sequence: seq, Reads: 1, Gene1: "RNA A", Start1: 1, End1: 2500, Gene2: "RNA B", Start2: 1, End2: 2500}
					f.queries["analysis_hybrids"] = core.Obj{"rows": []hybridRow{r}}
					mustClick(t, tt, "HYB record sequence")
					csSettle(a, tt)
					mustClick(t, tt, "Choose a record")
					mustClick(t, tt, stHybridLabel(r))
					mustClick(t, tt, "Preview sequence · test-read · HYB record sequence")
				}
				check := func(pk bool) {
					t.Helper()
					tt.Frame()
					mb := core.StructureClaimMB(len(seq), pk, 1)
					for label, want := range map[string]bool{
						"bin/cplfold --sequence-file": true, "--no-pseudoknot": !pk,
						", pseudoknots allowed": pk, "Folded whole: no pseudoknots.": !pk,
						fmt.Sprintf("about %.1f GB of the %.1f GB tasks may use", float64(mb)/1024, float64(free)/1024): true,
						"This fold needs about": pk,
					} {
						if tt.HasText(label) != want {
							t.Errorf("pseudoknots=%t: expected %q visible=%t", pk, label, want)
						}
					}
				}
				check(!allow) // inherited from the reference
				if allow {
					mustClick(t, tt, stAllowKnots)
				} else {
					mustClick(t, tt, stNoKnots)
				}
				check(allow)
				if source == stSourceRecord {
					snapshot(t, tt, fmt.Sprintf("structure-record-budget-pseudoknots-%t", allow))
				}
				wantMB := free
				if allow {
					mustClick(t, tt, "Run fold")
					csSettle(a, tt)
					if len(f.startedKinds()) != 0 {
						t.Fatal("Run fold submitted an over-budget request")
					}
					p.budgetMB, wantMB = knotted, knotted
					tt.Frame()
				}
				mustClick(t, tt, "Run fold")
				csSettle(a, tt)
				started := f.startedKinds()
				if len(started) != 1 {
					t.Fatalf("Run fold submitted %d tasks", len(started))
				}
				req := started[0]
				if req.Source == nil || *req.Source != source || req.Sequence == nil || *req.Sequence != seq || req.MemMB == nil {
					t.Fatal("Run fold did not submit the selected source, its sequence and a memory claim")
				}
				pk := req.Parameters.(map[string]any)["allow_pseudoknot"]
				t.Logf("Run fold: source=%s, %d nt, allow_pseudoknot=%v, mem_mb=%d", *req.Source, len(*req.Sequence), pk, *req.MemMB)
				if pk != allow || *req.MemMB != wantMB {
					t.Fatalf("wanted allow_pseudoknot=%t, mem_mb=%d", allow, wantMB)
				}
			})
		}
	}
}

// The web's "Paste sequence" source: one strand for CPLfold, ViennaRNA and
// COMRADES, read as the web reads it, checked, and named in the task.
func TestStructurePastedSequence(t *testing.T) {
	a, f := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1300)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	if pl := p.plan(); pl.source != "" || pl.req.Source != nil {
		t.Fatalf("a reference region by default: %q %v", pl.source, pl.req.Source)
	}
	p.mode, p.windows = "windows", "1-400"
	p.setSource(stSourcePaste)
	if p.mode != "genome" {
		t.Fatalf("a pasted sequence is folded whole, mode %q", p.mode)
	}
	tt.Frame()
	for _, text := range []string{"1  Sequence source", "• Paste the RNA sequence to fold (A, C, G, U; T is read as U)."} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("1  RNA to fold") || tt.HasText("3  What to fold") {
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
		len(pl.req.Windows) != 0 || pl.source != "pasted sequence, 21 nt" {
		t.Fatalf("plan %+v %q %q", pl.req, pl.problems, pl.source)
	}
	tt.Frame()
	for _, text := range []string{"the pasted sequence, 21 nt",
		"• Chimeras guide the fold only if the sequence occurs exactly once in the samples' reference: it is then folded as that region. " +
			"Otherwise it is folded from the sequence alone."} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	snapshot(t, tt, "structure-paste")
	mustClick(t, tt, stAllowKnots)
	tt.Frame()
	mustHave(t, tt, "the pasted sequence, 21 nt, pseudoknots allowed")
	snapshot(t, tt, "structure-paste-pseudoknots")
	a.theme = "system" // the default light preference overrides the tester's appearance
	tt.SetDark(true)
	snapshot(t, tt, "structure-paste-pseudoknots-dark")
	tt.SetDark(false)
	if err := tt.Click("Run fold"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	started := f.startedKinds()
	if len(started) != 1 || *started[0].Sequence != "GGGAAACCCUUUGGGAAACCC" || started[0].Parameters.(map[string]any)["evidence"] != nil {
		t.Fatalf("started %+v", started)
	}
	if got := stSourceTitle(engineCPLfold, pl.source, true); got != "Structure · pasted sequence, 21 nt · sequence only" {
		t.Fatalf("title %q", got)
	}
	// ViennaRNA: the sequence alone, hard pairs on it, its length limit
	p.engine, p.hardPairs = engineVienna, "1-21"
	tt.Frame()
	if tt.HasText("Structure topology") || p.plan().pk {
		t.Fatal("CPLfold topology leaked into ViennaRNA")
	}
	if pl := p.plan(); len(pl.problems) != 0 || *pl.req.Engine != "viennarna" || pl.req.Parameters.(map[string]any)["hard_pairs"] == nil ||
		!slices.Contains(pl.limits, "ViennaRNA MFE folds the sequence alone; hard pairs are positions on it.") {
		t.Fatalf("vienna %+v %q %q", pl.req, pl.problems, pl.limits)
	}
	p.hardPairs = "1-40"
	if pl := p.plan(); !slices.Contains(pl.problems, "Hard pair 1-40 is not inside a folded window.") {
		t.Fatalf("hard pair outside %q", pl.problems)
	}
	p.hardPairs, p.src.pasted = "", strings.Repeat("ACGU", stViennaMaxNT/4+1)
	if pl := p.plan(); !slices.Contains(pl.problems, "ViennaRNA MFE folds at most 30,000 nt; paste a shorter sequence.") {
		t.Fatalf("vienna limit %q", pl.problems)
	}
	if got := stSourceTitle(engineVienna, "pasted sequence, 21 nt", true); got != "Structure (ViennaRNA) · pasted sequence, 21 nt" {
		t.Fatalf("vienna title %q", got)
	}
	// COMRADES needs chimeras: only a region of the reference can run
	p.engine = engineComrades
	if pl := p.plan(); !slices.Contains(pl.problems, "COMRADES(ViennaRNA) folds at most 3,000 nt at a time, as the web: fold windows of that size.") ||
		!strings.HasPrefix(pl.limits[0], "COMRADES(ViennaRNA) needs chimeras") {
		t.Fatalf("comrades %q %q", pl.problems, pl.limits)
	}
	// a prefill names reference regions again
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"gene": africaGene,
		"windows": []map[string]any{{"start": 3000, "end": 3400}}}})
	csSettle(a, tt)
	if p.src.kind != stSourceReference || p.mode != "windows" {
		t.Fatalf("prefill source %q mode %q", p.src.kind, p.mode)
	}
}

// Two pasted strands for CPLcofold, as the web's RNA A and RNA B boxes.
func TestStructurePastedStrands(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1300)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	p.engine = engineCPLcofold
	p.setSource(stSourcePaste)
	tt.Frame()
	if !tt.HasText("Allow pseudoknots") || tt.HasText("Structure topology") {
		t.Fatal("CPLcofold should keep its own pseudoknot checkbox")
	}
	if !tt.HasText("• Paste RNA A and RNA B, or A&B in the RNA A box.") || tt.HasText("3  Region 2 (strand B)") {
		t.Fatalf("texts %q", tt.Texts())
	}
	p.src.pasted, p.src.pastedTwo = "GGGGAAAACCCC", "ggttttcccc"
	pl := p.plan()
	if len(pl.problems) != 0 || *pl.req.Mode != "cofold" || *pl.req.Engine != "cplcofold" || *pl.req.Sequence != "GGGGAAAACCCC" ||
		*pl.req.SequenceTwo != "GGUUUUCCCC" || len(pl.req.Regions) != 0 || pl.span() != "12 + 10 nt" || pl.memMB == 0 ||
		pl.source != "pasted RNA A + RNA B, 12 + 10 nt" || *pl.req.GeneID != "pasted" {
		t.Fatalf("plan %+v %q %q", pl.req, pl.problems, pl.source)
	}
	tt.Frame()
	if !tt.HasText("2 pasted strands, 12 + 10 nt") {
		t.Fatalf("texts %q", tt.Texts())
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
	// the energy models and beam CPLcofold supports apply to pasted strands too
	p.src.pastedTwo, p.model = "GGUUUUCCCC", "CC06"
	if pl := p.plan(); !slices.Contains(pl.problems, "CPLcofold uses the DP03 or DP09 energy model: choose one under Advanced settings.") {
		t.Fatalf("model %q", pl.problems)
	}
	// a HYB record is not two strands: CPLcofold switches to pasting, as the web
	p.model = "DP09"
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
	p.engine = engineCPLcofold
	p.setSource(stSourceRecord)
	csSettle(a, tt)
	listed := p.src.records[1]
	a.Navigate("structure", map[string]any{"prefill": map[string]any{"record": listed, "source": "an interaction record"}})
	csSettle(a, tt)
	if p.src.kind != stSourceRecord || p.engine != engineCPLfold || p.src.record == nil || p.src.record.Name != listed.Name {
		t.Fatalf("source %q engine %q record %+v", p.src.kind, p.engine, p.src.record)
	}
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
