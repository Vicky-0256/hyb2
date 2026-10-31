package views

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

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
		"ACGUNNXACGU":     "The pasted sequence: only A, C, G and U are accepted (T is read as U); remove N, X.",
		"acgu":            "The pasted sequence: at least 10 nt.",
		"ACGU&ACGUACGUAC": "The pasted sequence: only A, C, G and U are accepted (T is read as U); remove &.",
	} {
		p.src.pasted = text
		if pl := p.plan(); !slices.Contains(pl.problems, want) {
			t.Fatalf("%q: %q", text, pl.problems)
		}
	}
	// a FASTA header, line breaks, lower case and T, as the web's normaliseFoldSequence
	p.src.pasted = ">my RNA\nggg aaa\ncccTTTgggaaaccc\n"
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
	p.src.pasted, p.src.pastedTwo = "GGGGAAAACCCC&GGUUUUCCCC", ""
	if pl := p.plan(); len(pl.problems) != 0 || *pl.req.SequenceTwo != "GGUUUUCCCC" {
		t.Fatalf("A&B %+v %q", pl.req, pl.problems)
	}
	p.src.pastedTwo = "ACGU"
	if pl := p.plan(); !slices.Contains(pl.problems, "RNA A: only A, C, G and U are accepted (T is read as U); remove &.") ||
		!slices.Contains(pl.problems, "RNA B: at least 10 nt.") {
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
