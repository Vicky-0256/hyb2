package views

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

func stFloat(v float64) *float64 { return &v }

func TestStructureCandidateSelection(t *testing.T) {
	r := structureRecord{WindowStart: 101, WindowEnd: 112, Sequence: "GGGAAACCCUUU", DBN: "(((...)))...",
		Pairs: [][2]int{{101, 109}, {102, 108}, {103, 107}}, Bonus: []*float64{stFloat(1), stFloat(2), stFloat(3)}, Energy: stFloat(-3.1),
		Candidates: []stCandidate{
			{Rank: 0, Type: "phase1", Topology: "nested", Energy: stFloat(-3.1), EffectiveEnergy: stFloat(-3.1), Structure: "(((...)))...", Pairs: 3,
				Bonus: []*float64{stFloat(1), stFloat(2), stFloat(3)}},
			{Rank: 1, Type: "pseudoknot", Topology: "pseudoknotted", Energy: stFloat(-2.5), EffectiveEnergy: stFloat(-2.0), Phase1Energy: stFloat(-1.5),
				Score: stFloat(7), Structure: "((.[[.)).]].", Pairs: 4, PseudoknotPairs: 2, Bonus: []*float64{stFloat(4), stFloat(5), stFloat(6), stFloat(7)}},
		},
		NucleotideSupport: []float64{0.5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1.23456}}
	if r.selectCandidate(5) || r.selectCandidate(-1) {
		t.Fatal("selected a candidate that does not exist")
	}
	if !r.selectCandidate(1) {
		t.Fatal("candidate 1 not selected")
	}
	// pairs sorted by their left end, aligned with the candidate's bonus
	if !slices.Equal(r.Pairs, [][2]int{{101, 108}, {102, 107}, {104, 111}, {105, 110}}) || r.DBN != "((.[[.)).]]." ||
		*r.Energy != -2.5 || r.SelectedCandidate != 1 || *r.Bonus[2] != 6 {
		t.Fatalf("record %+v", r)
	}
	if got := stWeights([]structureRecord{r})[[2]int{104, 111}]; got != 6 {
		t.Fatalf("weight %v", got)
	}
	// the nucleotide inspector: base, partner, layer, support
	if got := stNucleotideText([]structureRecord{r}, 104, "CPLfold"); got != "Nucleotide 104 A · paired with 111 U (A–U, pseudoknot-1) · chimera support 6" {
		t.Fatalf("inspector %q", got)
	}
	if got := stNucleotideText([]structureRecord{r}, 112, "CPLfold"); got != "Nucleotide 112 U · unpaired · strongest chimera support 1.235" {
		t.Fatalf("inspector unpaired %q", got)
	}
	if got := stNucleotideText([]structureRecord{r}, 50, "CPLfold"); got != "" {
		t.Fatalf("outside %q", got)
	}
	// the web's candidates TSV
	rows := stCandidatesRows("G", []structureRecord{r})
	if !slices.Equal(rows[0][:4], []string{"rank", "selected", "type", "topology"}) || len(rows) != 3 {
		t.Fatalf("rows %q", rows)
	}
	if want := []string{"2", "yes", "pseudoknot", "pseudoknotted", "-2.5", "-2", "-1.5", "7", "4", "4", "((.[[.)).]].", "G:101-112"}; !slices.Equal(rows[2], want) {
		t.Fatalf("row %q", rows[2])
	}
	if rows[1][1] != "no" {
		t.Fatalf("best row %q", rows[1])
	}
	raw, err := stCandidatesJSON("s1", "CPLfold", "G", []structureRecord{r})
	var body struct {
		Windows []struct {
			Region            string        `json:"region"`
			SelectedCandidate int           `json:"selected_candidate"`
			Candidates        []stCandidate `json:"candidates"`
		} `json:"windows"`
	}
	if err != nil || json.Unmarshal(raw, &body) != nil || len(body.Windows) != 1 || body.Windows[0].SelectedCandidate != 1 ||
		len(body.Windows[0].Candidates[1].Bonus) != 4 || body.Windows[0].Region != "G:101-112" {
		t.Fatalf("json %s", raw)
	}
	// the result package's structure JSON lists them as the web's (rank from 1)
	files := rpStructureFiles("s1", reportStructure{ID: "s1", Gene: "G", Length: 12, Result: &structureResult{Records: []structureRecord{r}}}, "S05", true)
	var pkg struct {
		Selected   int              `json:"selectedCandidate"`
		Crossing   int              `json:"crossingPairs"`
		Candidates []map[string]any `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(files[1].Text), &pkg); err != nil || pkg.Selected != 2 || len(pkg.Candidates) != 2 ||
		pkg.Crossing != 4 || pkg.Candidates[0]["crossingPairs"] != 0.0 || pkg.Candidates[1]["rank"] != 2.0 ||
		pkg.Candidates[1]["dotBracket"] != "((.[[.)).]]." || pkg.Candidates[1]["crossingPairs"] != 4.0 {
		t.Fatalf("package %s", files[1].Text)
	}
	// back to the best
	if !r.selectCandidate(0) || r.DBN != "(((...)))..." || len(r.Pairs) != 3 {
		t.Fatalf("best %+v", r)
	}
	// a CPLcofold candidate keeps the break in the TSV and maps to the drawn positions
	co := structureRecord{WindowStart: 1, WindowEnd: 108, Sequence: "GGGAAACCCUUUCC", DBN: "(((....)))..()",
		Positions: []int{1, 2, 3, 4, 5, 6, 101, 102, 103, 104, 105, 106, 107, 108}, Cutpoint: 6,
		Segments:   []core.Region{{GeneID: "G", Start: 1, End: 6}, {GeneID: "G", Start: 101, End: 108}},
		Candidates: []stCandidate{{Structure: "(((....)))..()"}, {Structure: "((......))...."}}}
	if !co.selectCandidate(1) || !slices.Equal(co.Pairs, [][2]int{{1, 104}, {2, 103}}) || co.Bonus != nil {
		t.Fatalf("cofold %+v", co)
	}
	if rows := stCandidatesRows("G", []structureRecord{co}); !slices.Equal(rows[0][9:], []string{"crossing_pairs", "dot_bracket", "cutpoint", "region"}) ||
		rows[2][10] != "((....&..))...." || rows[2][11] != "6" || rows[2][12] != "G:1-6 + G:101-108" {
		t.Fatalf("cofold rows %q", rows)
	}
}

func TestStructureComradesRows(t *testing.T) {
	var cm stComrades
	raw := `{"evidence": {"inputRecords": 60, "uniqueBasePairs": 40, "selectedBasePairs": 2, "rankedStems": 5, "requestedStemConstraints": 3,
		"acceptedStemConstraints": [{"id": "F1", "rank": 2, "left": 10, "right": 50, "length": 4, "support": 9}],
		"rejectedStemConstraints": [{"id": "F2", "rank": 3, "reason": "overlap"}],
		"comradesScore": 18, "matchedEvidencePairs": 4,
		"selectedEvidencePairs": [{"one": 10, "two": 50, "count": 5, "recordIds": ["r1", "line_7"]}, {"one": 11, "two": 49, "count": 4}]},
		"randomisation": {"requestedFolds": 1, "seed": "s1", "selectedRun": 1,
		"structures": [{"run": 1, "randomised": true, "energy": -12.5, "comradesScore": 18, "matchedEvidencePairs": 4,
			"acceptedStemConstraints": ["F1", "F3"], "rejectedStemConstraints": 1, "dotBracket": "((..))"},
			{"run": 0, "randomised": false, "energy": -13, "comradesScore": 10, "matchedEvidencePairs": 2,
			"acceptedStemConstraints": [], "rejectedStemConstraints": 0, "dotBracket": "......"}]}}`
	if err := json.Unmarshal([]byte(raw), &cm); err != nil {
		t.Fatal(err)
	}
	if got := string(webTSV(stEvidenceRows(&cm))); got != "prepared_position_1\tprepared_position_2\thyb_row_observations\texample_record_ids\n10\t50\t5\tr1,line_7\n11\t49\t4\t\n" {
		t.Fatalf("evidence %q", got)
	}
	if got := stConstraintRows(&cm); !slices.Equal(got[1], []string{"F1", "2", "F 10 50 4", "10", "50", "4", "9", "yes"}) ||
		!slices.Equal(got[2], []string{"F2", "3", "", "", "", "", "", "no:overlap"}) {
		t.Fatalf("constraints %q", got)
	}
	if got := stEnsembleRows(&cm); !slices.Equal(got[1], []string{"1", "yes", "yes", "-12.5", "18", "4", "F1,F3", "1", "((..))"}) ||
		!slices.Equal(got[2], []string{"0", "no", "no", "-13", "10", "2", "", "0", "......"}) {
		t.Fatalf("ensemble %q", got)
	}
	r := structureRecord{WindowStart: 1, WindowEnd: 6, Sequence: "GGAACC", DBN: "((..))", Pairs: [][2]int{{1, 6}, {2, 5}},
		Bonus: []*float64{stFloat(5), stFloat(0)}}
	if got := stNucleotideText([]structureRecord{r}, 6, "COMRADES(ViennaRNA)"); got != "Nucleotide 6 C · paired with 1 G (G–C) · RNAcofold evidence 5" {
		t.Fatalf("inspector %q", got)
	}
	var a structureResult
	if err := json.Unmarshal([]byte(`{"meta":{"engine":"COMRADES(ViennaRNA)","records":[{"comrades":{"evidence":{"selectedBasePairs":1001,"scoredBasePairs":1037}}}]}}`), &a); err != nil {
		t.Fatal(err)
	}
	a.Records = []structureRecord{r}
	src, err := stDrawingSource(&a, r)
	if err != nil || src.EvidencePairs != 1037 || !slices.Equal(src.Support, []float64{5, 0, 0, 0, 0, 5}) {
		t.Fatalf("scoring pool or per-base counts: %+v, %v", src, err)
	}
	a.Meta.Records[0].Comrades.Evidence.ScoredBasePairs = nil
	if old, _ := stDrawingSource(&a, r); old.EvidencePairs != 1001 {
		t.Fatalf("historical scoring pool: %d", old.EvidencePairs)
	}
	// The score card uses the stored (capped) scalar, not twice the raw
	// observations that the same result displays at its paired bases.
	a.Meta.Records[0].Comrades.Evidence.Score = 2_000_000
	a.Meta.Records[0].Comrades.Evidence.MatchedPairs = 1
	a.Records[0].Bonus[0] = stFloat(1_100_000)
	tt := ui.NewTester(func(c *ui.Context) {
		(&structurePage{}).resultMetrics(c, &a, a.Records, stShownPairs(a.Records, nil), len(r.Sequence))
	}, 1000, 200)
	tt.Frame()
	if !tt.HasText("2000000") || tt.HasText("2200000") {
		t.Fatalf("standalone score card: %v", tt.Texts())
	}
}

// Export raw counts even above the cap; never send one window's pool to another.
func TestComradesScoringExports(t *testing.T) {
	const legacy = `{"comrades":{"evidence":{"selectedEvidencePairs":[{"one":6,"two":1,"count":3}],"scoredBasePairs":2}}}`
	const current = `{"comrades":{"evidence":{"scoringVersion":"all-pairs-capped-v1","comradesScore":2000006,
		"selectedEvidencePairs":[{"one":6,"two":1,"count":3}],"scoredEvidencePairs":[{"one":1,"two":6,"count":3},{"one":2,"two":5,"count":1100000}]},
		"randomisation":{"selectedRun":1,"structures":[{"run":1,"dotBracket":"((..))","comradesScore":2000006},{"run":0,"dotBracket":"(....)","comradesScore":6}]}}}`
	var a structureResult
	if err := json.Unmarshal([]byte(`{"meta":{"records":[`+current+`,`+legacy+`]}}`), &a); err != nil {
		t.Fatal(err)
	}
	cm := a.Meta.Records[0].Comrades
	if got := string(webTSV(stScoredEvidenceRows(cm))); got != "# scoring_version=all-pairs-capped-v1\nprepared_left\tprepared_right\traw_pair_count\n1\t6\t3\n2\t5\t1100000\n" {
		t.Fatalf("full score TSV: %q", got)
	}
	if got := stEvidenceRows(cm); len(got) != 2 || !slices.Equal(got[1], []string{"6", "1", "3", ""}) {
		t.Fatalf("shortlist changed: %q", got)
	}
	r := structureRecord{WindowStart: 101, WindowEnd: 106, Sequence: "GGAACC", DBN: "((..))"}
	for _, n := range []int{1, 2} {
		a.Records = append(a.Records, r)
		file := rpStructureFiles("results/structure_prediction", reportStructure{Result: &a}, "S12", true)[1]
		var row map[string]json.RawMessage
		if err := json.Unmarshal([]byte(file.Text), &row); err != nil {
			t.Fatal(err)
		}
		if n == 2 {
			var windows []map[string]json.RawMessage
			if err := json.Unmarshal(row["windows"], &windows); err != nil || len(windows) != 2 {
				t.Fatalf("windows: %v, %v", windows, err)
			}
			if windows[1]["scoringVersion"] != nil || windows[1]["scoredEvidencePairs"] != nil || row["scoredEvidencePairs"] != nil {
				t.Fatal("export inferred a legacy or cross-window evidence pool")
			}
			row = windows[0]
		}
		var pairs []stCount
		var rnd struct{ Structures []stEnsembleEntry }
		if string(row["scoringVersion"]) != `"all-pairs-capped-v1"` || json.Unmarshal(row["scoredEvidencePairs"], &pairs) != nil ||
			json.Unmarshal(row["randomisation"], &rnd) != nil || len(rnd.Structures) != 2 || len(pairs) != 2 || pairs[1].Count != 1_100_000 {
			t.Fatalf("missing or truncated package evidence: %s", file.Text)
		}
		for _, member := range rnd.Structures {
			score := 0
			for _, p := range stParseDBN(member.DotBracket, 1) {
				for _, e := range pairs {
					if e.One == p.I && e.Two == p.J {
						score += 2 * min(e.Count, 1_000_000)
					}
				}
			}
			if float64(score) != member.Score {
				t.Fatalf("run %d: recomputed %d, stored %v", member.Run, score, member.Score)
			}
		}
	}
}

// The viewer: the CPLfold candidate table switches the structure shown,
// the bonus matrix is offered, and a COMRADES result shows its details.
func TestStructureResultDetailsViewer(t *testing.T) {
	a, f := openCS(t)
	read := fixture(t, "structure_read.json")
	record := read["records"].([]any)[0].(map[string]any)
	dbn := record["dbn"].(string)
	second := []byte(strings.Repeat(".", len(dbn)))
	second[0], second[20] = '(', ')'
	matrix := filepath.Join(t.TempDir(), "bonus_matrix.tsv")
	if err := os.WriteFile(matrix, []byte("# HYB2 CPLfold bonus matrix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record["candidates"] = []any{
		map[string]any{"rank": 0, "type": "phase1", "topology": "nested", "energy": -452.02, "effective_energy": -452.02, "structure": dbn, "pairs": 1055},
		map[string]any{"rank": 1, "type": "phase1", "topology": "nested", "energy": -440.5, "effective_energy": -441.0, "structure": string(second), "pairs": 1},
	}
	record["selected_candidate"] = 0
	record["bonus_matrix_path"] = matrix
	f.structures[structA] = read
	tt := ui.NewTester(a.View, 1100, 1600)
	a.Navigate("structure", map[string]any{"structureId": structA})
	csSettle(a, tt)
	p := a.PageFor("structure").(*structurePage)
	for _, text := range []string{"CPLfold candidates", "Candidates TSV…", "Candidates JSON…", "Bonus matrix TSV…", "#2", "-441.00", "Shown"} {
		if !tt.HasText(text) {
			t.Fatalf("lacks %q: %q", text, tt.Texts())
		}
	}
	if len(p.arcs.A) != 1055 {
		t.Fatalf("arcs %d", len(p.arcs.A))
	}
	if err := tt.Click("#2"); err != nil {
		t.Fatal(err)
	}
	csSettle(a, tt)
	if got := p.cache[structA].Records[0]; got.SelectedCandidate != 1 || got.DBN != string(second) || len(p.arcs.A) != 1 || p.stats.PairsA != 1 {
		t.Fatalf("selected %d, %d arcs, stats %+v", got.SelectedCandidate, len(p.arcs.A), p.stats)
	}
	if !strings.Contains(p.dbnText, " (-440.50)\n") {
		t.Fatalf("dbn text %.120q", p.dbnText)
	}
	// clicking the diagram inspects a nucleotide; the arrow keys step it
	p.arcs.Selected = 3171
	tt.Frame()
	// the web's inspector card: the position, then what it pairs with
	if !tt.HasText("Nucleotide 3,171") || !slices.ContainsFunc(tt.Texts(), func(s string) bool {
		return strings.Contains(s, "unpaired in this CPLfold candidate")
	}) {
		t.Fatalf("no inspector: %q", tt.Texts())
	}
	p.arcs.Step(-1)
	tt.Frame()
	if !tt.HasText("Nucleotide 3,170") || !slices.ContainsFunc(tt.Texts(), func(s string) bool {
		return strings.Contains(s, "paired with 3,190")
	}) {
		t.Fatalf("no partner: %q", tt.Texts())
	}

	// a COMRADES(ViennaRNA) result: score, ensemble, downloads
	comrades := fixture(t, "structure_read.json")
	meta := comrades["meta"].(map[string]any)
	meta["engine"] = "COMRADES(ViennaRNA)"
	meta["records"] = []any{map[string]any{"comrades": map[string]any{
		"evidence": map[string]any{"inputRecords": 60, "uniqueBasePairs": 40, "selectedBasePairs": 1, "rankedStems": 5, "requestedStemConstraints": 3,
			"acceptedStemConstraints": []any{map[string]any{"id": "F1", "rank": 1, "left": 3170, "right": 3300, "length": 4, "support": 9}},
			"comradesScore":           18, "matchedEvidencePairs": 4, "selectedEvidencePairs": []any{map[string]any{"one": 1, "two": 40, "count": 5}}},
		"randomisation": map[string]any{"requestedFolds": 0, "seed": "HYB2-Web", "selectedRun": 0,
			"structures": []any{map[string]any{"run": 0, "randomised": false, "energy": -452.0, "comradesScore": 18, "matchedEvidencePairs": 4,
				"acceptedStemConstraints": []any{"F1"}, "rejectedStemConstraints": 0, "dotBracket": dbn}}},
	}}}
	f.structures[structB] = comrades
	p.cache = map[string]*structureResult{}
	a.Navigate("structure", map[string]any{"structureId": structB})
	csSettle(a, tt)
	for _, text := range []string{"COMRADES result", "COMRADES score 18 · 4 supported structure pairs · selected fold: ranked",
		"Shortlist TSV…", "RNAcofold constraint shortlist (1)", "Constraints TSV…", "Ensemble TSV…", "Accepted stem constraints (1)",
		"1 · RNAcofold evidence", "40 unique pairs", "60 eligible HYB rows; one observation per row", "2 · Ranked stems", "5 stems",
		"Top 3 sent to compatibility fitting", "3 · Accepted constraints", "1 stem", "4 enforced base pairs", "4 · Structure ensemble",
		"1 structure", "ranked fold + 0 seeded random orders", "#1 · ranked",
		// the web's two metric cards and its scope sentence
		"Constraint mode", "HYB-guided", "COMRADES score", "How this was computed"} {
		if !tt.HasText(text) {
			t.Fatalf("COMRADES lacks %q: %q", text, tt.Texts())
		}
	}
	if got := stComputedScope(p.cache[structB]); !strings.HasPrefix(got, "This reproduces the ViennaRNA path after a HYB file: RNAcofold evidence") {
		t.Fatalf("scope %q", got)
	}
	if got := stComputedScope(&structureResult{}); got != "" {
		t.Fatalf("CPLfold scope %q", got)
	}
	vienna := &structureResult{}
	vienna.Meta.Engine = "ViennaRNA"
	if got := stComputedScope(vienna); got != "ViennaRNA minimum-free-energy fold on this computer, without base-pair constraints." {
		t.Fatalf("ViennaRNA scope %q", got)
	}
	if tt.HasText("CPLfold candidates") {
		t.Fatal("candidates shown for COMRADES")
	}
	if tt.HasText("Scored evidence TSV…") || !tt.HasText("The complete scored evidence set was not recorded in this result.") {
		t.Fatal("legacy COMRADES result claims to have a complete scoring pool")
	}
}
