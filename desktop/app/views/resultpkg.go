package views

// ⑥ Report, the result packages: the web app's hyb2-result/1.0 record
// (web/results.js) built from the project, written as the web's two ZIPs —
// "ai" (README, template report.md, summary, data dictionary, provenance,
// evidence, bounded tables) and "analysis" (full tables, structures,
// figures) — each with a manifest of sizes and SHA-256. Step and result ids
// are the web's (resultschema/catalog.json, exported from results.js), so
// "R26" means the same in both apps; steps that are interactive views on
// the desktop (Explore, the quick view) are not_run here.

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"hyb2desktop/widgets"
)

//go:embed resultschema/*.json
var resultSchemaFS embed.FS

// rpCatalog is resultschema/catalog.json: the web's ids, labels and units.
type rpCatalog struct {
	Schema  string            `json:"schema"`
	Status  map[string]string `json:"status"`
	Steps   [][3]string       `json:"steps"`
	Results [][6]string       `json:"results"`
	Nature  map[string]string `json:"nature"`
}

var (
	rpStatusOrder = []string{"ok", "no_result", "not_run", "running", "failed", "stale", "pending"}
	rpNatureOrder = []string{"count", "setting", "descriptive", "statistical_test", "prediction", "derived_from_prediction", "external_source"}
	// web/results.js RESULT_TYPES and NATURE (defaults integer and count)
	rpTypes  = map[string]string{"R05": "number", "R14": "number", "R16": "number", "R31": "string", "R32": "string", "R34": "number", "R36": "number", "R38": "number"}
	rpNature = map[string]string{"R12": "setting", "R20": "descriptive", "R21": "descriptive",
		"R22": "statistical_test", "R23": "statistical_test", "R24": "statistical_test", "R25": "statistical_test", "R26": "statistical_test", "R27": "statistical_test",
		"R28": "prediction", "R29": "prediction", "R30": "statistical_test", "R31": "derived_from_prediction", "R32": "derived_from_prediction",
		"R33": "derived_from_prediction", "R34": "statistical_test", "R35": "derived_from_prediction", "R36": "statistical_test",
		"R37": "setting", "R38": "prediction", "R39": "prediction", "R40": "prediction", "R41": "external_source"}
	// what a project's numbers are, where the web's say "the primary HYB file"
	rpDefinitions = map[string]string{
		"R01": "Valid hybrid records of the analysed samples (their analysis indexes).",
		"R02": "Rows the analysis indexes skipped as invalid (blank and comment lines not included).",
		"R03": "Distinct RNA names across both arms of the analysed samples' records.",
		"R05": "Sum of the cluster support of the analysed samples' records (1 per record when a file has none).",
		"R06": "Records whose chimera type was inferred; the desktop's indexes do not record it.",
		"R07": "Sequences in the project's reference FASTA files.",
		"R08": "RNA names of the analysed records found in a project reference.",
		"R18": "Samples in condition A of the S08 test.",
		"R19": "Samples in condition B of the S08 test.",
		"R21": "Bins of the S08 test's similarity heatmap (smaller summed count of A and B).",
		"R37": "Length folded by the S12 structure (all its windows).",
		"R38": "Free energy of the S12 structure (one window or the whole RNA; null for several windows).",
		"R41": "Saved external sources the researcher included in the report.",
	}
	rpTitles = map[string]string{
		"S01": "Analysed samples (HYB records in the analysis indexes)",
		"S02": "Reference FASTA files",
		"S08": "Compare: differential test (DESeq2 1.46 Wald test, numpy port)",
	}
	rpViews = "An interactive view on the desktop (Explore, or Compare's quick view); the project report covers saved results."
)

func rpLoadCatalog() rpCatalog {
	raw, _ := resultSchemaFS.ReadFile("resultschema/catalog.json")
	var c rpCatalog
	_ = json.Unmarshal(raw, &c)
	return c
}

// ---- the record

type rpStep struct {
	ID, Key, Title, Status, StatusText string
	Detail, Scope, CompletedAt         *string
	Parameters                         map[string]any
	Inputs, Results, Tables, Figures   []string
}

type rpResult struct {
	ID         string  `json:"id"`
	Key        string  `json:"key"`
	Step       string  `json:"step"`
	Label      string  `json:"label"`
	Type       string  `json:"type"`
	Nature     string  `json:"nature"`
	Value      any     `json:"value"`
	Unit       string  `json:"unit"`
	Scope      *string `json:"scope"`
	Definition string  `json:"definition"`
	Status     string  `json:"status"`
}

type rpTable struct {
	ID, File, Step, Title, Scope string
	Audience                     []string
	Header                       []string
	Rows                         [][]string
}

type rpFigure struct {
	ID, File, Step, Title string
	SVG                   func() string
}

type rpFile struct {
	Path, MediaType, Text, Description string
}

type rpRecord struct {
	Schema      string
	Application map[string]any
	GeneratedAt string
	Scope       map[string]any
	Run         map[string]string
	Inputs      []map[string]any
	Steps       []*rpStep
	Results     []rpResult
	Tables      []*rpTable
	Figures     []*rpFigure
	Structures  []rpFile
	Evidence    []map[string]any
	Warnings    []map[string]string
	Limitations []string
	Literature  map[string]any
	catalog     rpCatalog
	// exclude are the paths the researcher leaves out of a package;
	// omitted those files() left out (resultpkg_select.go).
	exclude map[string]bool
	omitted []string
}

func strPtr(s string) *string { return &s }

// buildResultRecord describes the project in the web's result format.
func buildResultRecord(d *reportData, now time.Time) *rpRecord {
	cat := rpLoadCatalog()
	r := &rpRecord{Schema: cat.Schema, catalog: cat, GeneratedAt: now.UTC().Format(time.RFC3339),
		Application: map[string]any{"name": "HYB2 Desktop", "version": nameOr(d.AppVersion, "development build"), "buildCommit": nil}}
	r.Scope = map[string]any{"kind": "session_snapshot", "label": "Project report",
		"description": "Every saved result of the HYB2 Desktop project " + d.Name + ": its analysed samples, differential tests and structures. " +
			"Steps that are interactive views on the desktop, or were not run, are listed as not_run.",
		"primaryDataset": nil, "purpose": nil}
	r.Limitations = []string{
		"Results describe the project's samples only. Counts are not normalised against other experiments.",
		"Only S08 (and the paired t-tests of S11) are statistical tests; counts and maps are descriptive.",
		"S08 is HYB2 Desktop's numpy port of DESeq2 1.46, not R itself; check key results against R before publication.",
		"Structures (S12) are computational predictions, not experimental structures.",
		"External sources (S13) are background material; citations are not verified and a mention is not evidence for a structure.",
	}
	steps := map[string]*rpStep{}
	for _, spec := range cat.Steps {
		title := spec[2]
		if t, ok := rpTitles[spec[0]]; ok {
			title = t
		}
		st := &rpStep{ID: spec[0], Key: spec[1], Title: title, Status: "not_run", StatusText: cat.Status["not_run"], Parameters: map[string]any{}}
		steps[spec[1]] = st
		r.Steps = append(r.Steps, st)
	}
	values := map[string]any{}
	status := func(key, s, detail string) {
		steps[key].Status, steps[key].StatusText = s, cat.Status[s]
		if detail != "" {
			steps[key].Detail = &detail
		}
	}
	warn := func(key, text string) {
		r.Warnings = append(r.Warnings, map[string]string{"id": fmt.Sprintf("W%02d", len(r.Warnings)+1), "step": steps[key].ID, "text": text})
	}
	input := func(key string, in map[string]any) {
		in["id"] = fmt.Sprintf("I%02d", len(r.Inputs)+1)
		r.Inputs = append(r.Inputs, in)
		steps[key].Inputs = append(steps[key].Inputs, in["id"].(string))
	}
	table := func(key string, t *rpTable) {
		t.ID, t.Step = fmt.Sprintf("T%02d", len(r.Tables)+1), steps[key].ID
		r.Tables = append(r.Tables, t)
		steps[key].Tables = append(steps[key].Tables, t.ID)
	}
	figure := func(key string, f *rpFigure) {
		f.ID, f.Step = fmt.Sprintf("F%02d", len(r.Figures)+1), steps[key].ID
		r.Figures = append(r.Figures, f)
		steps[key].Figures = append(steps[key].Figures, f.ID)
	}
	labels := map[string]string{}
	for _, s := range d.Samples {
		labels[s.ID] = s.Label
	}

	// S01 the analysed samples
	for _, s := range d.Samples {
		in := map[string]any{"role": "sample_reads", "fileName": nil, "label": s.Label, "condition": nameOr(s.Condition, ""), "sha256Status": "not_computed"}
		if s.Format == "hyb" {
			in["role"] = "sample_hyb"
		}
		if s.Reads != "" {
			in["fileName"] = filepath.Base(s.Reads)
		}
		if s.Counts != nil {
			in["records"] = s.Counts.Records
		}
		input("parse", in)
	}
	if len(d.Ready) > 0 {
		var parts []qcStats
		var rows [][]string
		for _, s := range d.Samples {
			if s.Counts == nil {
				continue
			}
			parts = append(parts, *s.Counts)
			c := s.Counts
			rows = append(rows, []string{s.ID, s.Label, s.Condition, s.Replicate, itoa64(c.Records), itoa64(c.Reads), itoa64(c.WithDG), itoa64(c.Intra), itoa64(c.Inter)})
		}
		merged := qcMerge(parts)
		values["valid_records"] = merged.Records
		status("parse", "ok", "")
		if merged.Records == 0 {
			status("parse", "no_result", "The analysed samples have no records.")
		}
		table("parse", &rpTable{File: "tables/samples.tsv", Title: "Analysed samples", Audience: []string{"ai", "analysis"},
			Scope: fmt.Sprintf("all %d analysed samples", len(rows)), Header: []string{"sample", "label", "condition", "replicate", "valid_records", "reads", "with_dg", "intra", "inter"}, Rows: rows})
		if ov := d.Overview; ov != nil {
			values["invalid_records"], values["unique_rnas"], values["rna_pairs"], values["cluster_support"] = ov.Invalid, ov.UniqueRNAs, ov.UniquePairs, ov.Support
			if ov.Invalid > 0 {
				warn("parse", fmt.Sprintf("%d invalid rows were skipped.", ov.Invalid))
			}
			rna := func(rows []ovCount) [][]string {
				out := make([][]string, len(rows))
				for i, c := range rows {
					out[i] = []string{c.Name, itoa64(c.Records), itoa64(c.Support)}
				}
				return out
			}
			pair := func(rows []ovCount) [][]string {
				out := make([][]string, len(rows))
				for i, c := range rows {
					out[i] = []string{c.Gene1, c.Gene2, itoa64(c.Records), itoa64(c.Support)}
				}
				return out
			}
			top := min(len(ov.RNAs), 10)
			table("parse", &rpTable{File: "tables/top_rnas.tsv", Title: "Top RNAs by cluster support", Audience: []string{"ai", "analysis"},
				Scope: fmt.Sprintf("top %d of %d RNAs by support (each record counts once per arm)", top, ov.UniqueRNAs), Header: []string{"name", "records", "support"}, Rows: rna(ov.RNAs[:top])})
			topPairs := min(len(ov.Pairs), 10)
			table("parse", &rpTable{File: "tables/top_rna_pairs.tsv", Title: "Top RNA pairs by cluster support", Audience: []string{"ai", "analysis"},
				Scope: fmt.Sprintf("top %d of %d pairs by support", topPairs, ov.UniquePairs), Header: []string{"rna_1", "rna_2", "records", "support"}, Rows: pair(ov.Pairs[:topPairs])})
			table("parse", &rpTable{File: "tables/rna_pairs.tsv", Title: "Every RNA pair", Audience: []string{"analysis"},
				Scope: fmt.Sprintf("all %d pairs", ov.UniquePairs), Header: []string{"rna_1", "rna_2", "records", "support"}, Rows: pair(ov.Pairs)})
		}
	} else {
		steps["parse"].Detail = strPtr("No sample has been analysed yet.")
	}

	// S02 references
	sequences, names := 0, map[string]bool{}
	for _, ref := range d.References {
		in := map[string]any{"role": "reference_fasta", "fileName": nil, "bytes": ref.SizeBytes, "sha256": nil, "sha256Status": "not_computed"}
		if ref.Path != "" {
			in["fileName"] = filepath.Base(ref.Path)
		}
		if ref.SHA256 != "" {
			in["sha256"], in["sha256Status"] = ref.SHA256, "computed"
		}
		input("reference", in)
		for _, s := range ref.Sequences {
			sequences++
			names[s.Name] = true
		}
	}
	if len(d.References) > 0 {
		values["fasta_sequences"] = int64(sequences)
		if ov := d.Overview; ov != nil {
			mapped := int64(0)
			for _, c := range ov.RNAs {
				if names[c.Name] {
					mapped++
				}
			}
			values["mapped_rna_names"] = mapped
			if mapped < ov.UniqueRNAs && len(ov.RNAs) == int(ov.UniqueRNAs) {
				warn("reference", fmt.Sprintf("Only %d of %d RNA names of the records are in a project reference.", mapped, ov.UniqueRNAs))
			}
		}
		status("reference", "ok", "")
	}

	// S03-S07: interactive views on the desktop
	for _, key := range []string{"interactions", "contact_map", "viewpoint", "region"} {
		steps[key].Detail = strPtr(rpViews)
	}
	for _, key := range []string{"arc_map", "region_structures"} {
		steps[key].Detail = strPtr("Drawn on demand on the Compare and Structure pages; not saved with the project.")
	}

	// S07 / S08 the newest differential test; every test in the tables
	if len(d.Tests) > 0 {
		var rows, all [][]string
		for _, x := range d.Tests {
			s := x.Summary
			rows = append(rows, []string{x.ID, x.LabelA, x.LabelB, strings.Join(x.Genes, ","), x.Created, itoa64(s.CellsTested), itoa64(s.Significant),
				itoa64(s.InteractionsUp), itoa64(s.InteractionsDown), nameOr(x.Err, "")})
			for _, it := range x.All {
				all = append(all, append([]string{x.ID}, rpInteractionRow(it)...))
			}
		}
		table("differential_test", &rpTable{File: "tables/differential_tests.tsv", Title: "Differential tests of the project", Audience: []string{"ai", "analysis"},
			Scope: fmt.Sprintf("all %d tests", len(d.Tests)), Header: []string{"test", "label_a", "label_b", "genes", "created", "cells_tested", "significant", "interactions_a", "interactions_b", "error"}, Rows: rows})
		x := rpNewestTest(d.Tests)
		e := x.Entry
		steps["comparison"].Parameters = map[string]any{"conditionA": x.LabelA, "conditionB": x.LabelB, "test": x.ID}
		values["compare_datasets_a"], values["compare_datasets_b"] = int64(len(e.SamplesOne)), int64(len(e.SamplesTwo))
		if x.Summary.SimilarityCells > 0 {
			values["similarity_bins"] = x.Summary.SimilarityCells
		}
		status("comparison", "ok", "Desktop tests are saved with their similarity heatmap; the effect map (quick view) is not saved.")
		names := func(ids []string) string {
			out := make([]string, len(ids))
			for i, id := range ids {
				out[i] = nameOr(labels[id], id)
			}
			return strings.Join(out, ", ")
		}
		scope := fmt.Sprintf("Test %s: %s (A: %s) vs %s (B: %s); %s; 10-nt cells", x.ID, x.LabelA, names(e.SamplesOne), x.LabelB, names(e.SamplesTwo), strings.Join(x.Genes, ", "))
		steps["comparison"].Scope, steps["differential_test"].Scope = strPtr(scope), strPtr(scope)
		p := e.Parameters
		steps["differential_test"].Parameters = map[string]any{"minCount": p.MinCount, "mergeRange": p.Range, "padjCutoff": p.PadjCutoff,
			"binSize": 10, "method": "DESeq2 1.46 Wald test (HYB2 Desktop numpy port); hyb2_compare interaction merge"}
		if x.Created != "" {
			steps["differential_test"].CompletedAt = strPtr(x.Created)
		}
		if x.Err != "" {
			status("differential_test", "failed", "The test's files could not be read: "+x.Err)
		} else {
			s := x.Summary
			values["cells_tested"], values["significant_cells"], values["significant_cells_a"], values["significant_cells_b"] =
				s.CellsTested, s.Significant, s.SignificantUp, s.SignificantDown
			values["interactions_a"], values["interactions_b"] = s.InteractionsUp, s.InteractionsDown
			st := "ok"
			if s.Significant == 0 {
				st = "no_result"
			}
			status("differential_test", st, "")
			var newest [][]string
			for _, it := range x.All {
				newest = append(newest, rpInteractionRow(it))
			}
			table("differential_test", &rpTable{File: "tables/differential_interactions.tsv", Title: "Merged differential interactions of the S08 test",
				Audience: []string{"ai", "analysis"}, Scope: fmt.Sprintf("all %d merged interactions of test %s", len(newest), x.ID),
				Header: []string{"direction", "x", "y", "log2FoldChange", "padj", "baseMean"}, Rows: newest})
			if len(d.Tests) > 1 {
				table("differential_test", &rpTable{File: "tables/differential_interactions_all_tests.tsv", Title: "Merged differential interactions of every test",
					Audience: []string{"analysis"}, Scope: fmt.Sprintf("all merged interactions of the %d tests", len(d.Tests)),
					Header: []string{"test", "direction", "x", "y", "log2FoldChange", "padj", "baseMean"}, Rows: all})
			}
			if len(x.Cells) > 0 {
				cells, length, title := x.Cells, x.Length, "A · Differential coverage map · "+x.LabelA+" vs "+x.LabelB
				figure("differential_test", &rpFigure{File: "figures/A_differential_map.svg", Title: "A · Differential coverage map", SVG: func() string {
					svg, _, _ := heatSVG{Cells: cells, View: widgets.Window{X0: 0, X1: float64(length), Y0: 0, Y1: float64(length)}, Size: 520,
						Title: title, MaxCells: svgMaxCells, XLabel: "Arm 1", YLabel: "Arm 2",
						Note: "Red: higher in A; blue: higher in B; stronger colour, smaller padj."}.SVG()
					return string(svgFile(svg))
				}})
			}
		}
		// S11 the test's folding energy
		if en := x.Energy; en != nil && en.Summary != nil {
			sum := en.Summary
			steps["folding_energy"].Parameters = map[string]any{"padj": sum.Parameters.Padj, "fragmentLength": sum.Parameters.Length, "temperature": 37,
				"engine": "ViennaRNA RNAcofold " + nameOr(sum.ViennaVersion, "")}
			steps["folding_energy"].Scope = strPtr(fmt.Sprintf("Interactions of test %s with padj below %g, each folded with both lineages' sequences", x.ID, sum.Parameters.Padj))
			one, two := sum.Sides["enriched_in_one"], sum.Sides["enriched_in_two"]
			values["energy_n_one"], values["energy_n_two"] = int64(one.N), int64(two.N)
			if one.P != nil {
				values["energy_p_one"] = *one.P
			}
			if two.P != nil {
				values["energy_p_two"] = *two.P
			}
			st := "ok"
			if sum.Folded == 0 {
				st = "no_result"
			}
			status("folding_energy", st, "")
			var rowsE [][]string
			if rr := en.Rows; rr != nil {
				for k := range rr.Side {
					rowsE = append(rowsE, []string{rr.Side[k], rpInt(rr.X, k), rpInt(rr.Y, k), rpFloat(rr.LFC, k), rpFloat(rr.Padj, k), rpFloat(rr.DGOne, k), rpFloat(rr.DGTwo, k)})
				}
			}
			table("folding_energy", &rpTable{File: "tables/folding_energy.tsv", Title: "RNAcofold energies per interaction", Audience: []string{"ai", "analysis"},
				Scope: fmt.Sprintf("all %d folded interactions", len(rowsE)), Header: []string{"side", "x", "y", "log2FoldChange", "padj", "dG_one", "dG_two"}, Rows: rowsE})
			var tests [][]string
			for _, side := range energySides {
				s := sum.Sides[side]
				tests = append(tests, []string{side, strconv.Itoa(s.N), rpPtr(s.MeanOne), rpPtr(s.MeanTwo), rpPtr(s.MeanDiff), rpPtr(s.T), rpPtr(s.P)})
			}
			table("folding_energy", &rpTable{File: "tables/folding_energy_tests.tsv", Title: "Paired t-tests", Audience: []string{"ai", "analysis"},
				Scope: "both sides", Header: []string{"side", "n", "mean_dG_one", "mean_dG_two", "mean_diff_one_minus_two", "t", "p"}, Rows: tests})
		} else {
			steps["folding_energy"].Detail = strPtr("Not computed for the S08 test (Compare › Folding energy).")
		}
	}

	// S12 the newest structure; every structure listed
	if len(d.Structures) > 0 {
		var rows [][]string
		for _, s := range d.Structures {
			energy := "NA"
			if s.Energy != nil {
				energy = strconv.FormatFloat(*s.Energy, 'f', -1, 64)
			}
			engine := "CPLfold"
			if s.Result != nil {
				engine = nameOr(s.Result.Meta.Engine, engine)
			}
			rows = append(rows, []string{s.ID, s.Gene, s.Mode, s.Windows, engine, s.Evidence, strconv.Itoa(s.Length), strconv.Itoa(s.Pairs), strconv.Itoa(s.Pseudoknots), energy, s.Created, nameOr(s.Err, "")})
		}
		table("structure", &rpTable{File: "tables/structures.tsv", Title: "Structures of the project", Audience: []string{"ai", "analysis"},
			Scope: fmt.Sprintf("all %d structures", len(d.Structures)), Header: []string{"structure", "rna", "mode", "windows", "engine", "evidence", "length", "pairs", "crossing_pairs", "energy", "created", "error"}, Rows: rows})
		s := rpNewestStructure(d.Structures)
		steps["structure"].CompletedAt = strPtr(s.Created)
		if s.Result == nil {
			status("structure", "failed", "The structure could not be read: "+s.Err)
		} else {
			res := s.Result
			steps["structure"].Parameters = map[string]any{"engine": nameOr(res.Meta.Engine, "CPLfold"), "rna": s.Gene, "mode": nameOr(res.Meta.Mode, res.Structure.Mode),
				"structure": s.ID, "settings": res.Meta.Parameters}
			steps["structure"].Scope = strPtr(fmt.Sprintf("%s, %s, %s nt (structure %s)", s.Gene, nameOr(s.Windows, s.Mode), FormatNumber(int64(s.Length)), s.ID))
			values["structure_length"], values["structure_pairs"], values["structure_crossing_pairs"] = int64(s.Length), int64(s.Pairs), int64(s.Pseudoknots)
			if s.Energy != nil {
				values["structure_energy"] = *s.Energy
			}
			status("structure", "ok", "")
			for _, st := range d.Structures {
				if st.Result == nil {
					continue
				}
				base := "results/structure_prediction"
				if st.ID != s.ID {
					base = "results/structures/" + exportName("", st.ID)
				}
				r.Structures = append(r.Structures, rpStructureFiles(base, st, steps["structure"].ID, st.ID == s.ID)...)
			}
		}
	}

	// S13 external sources
	r.Literature = map[string]any{"status": "not_run", "lookups": d.Cards, "includedInReport": len(d.Evidence), "hit_count": nil}
	if d.Cards > 0 {
		r.Literature["status"] = "ok"
		values["evidence_cards"] = int64(len(d.Evidence))
		status("literature_search", "ok", "")
		steps["literature_search"].Scope = strPtr("Cards added to the report by the researcher")
	}
	for i, e := range d.Evidence {
		r.Evidence = append(r.Evidence, map[string]any{"id": fmt.Sprintf("E%02d", i+1), "source": e.Title, "contentKind": "database_metadata",
			"query": e.Query, "status": e.Status, "retrievedAt": e.Retrieved, "review": map[string]any{"state": rpReviewState(e.Review), "note": rpNull(e.Note)},
			"evidenceNature": "database annotation (background, not validation)", "citationsVerified": false, "sourceUrl": rpNull(e.SourceURL)})
	}

	// scopes of the counting steps
	if len(d.Ready) > 0 {
		steps["parse"].Scope = strPtr(fmt.Sprintf("The %d analysed samples of project %s", len(d.Ready), d.Name))
	}
	if len(d.References) > 0 {
		steps["reference"].Scope = strPtr("The project's references against the RNA names of the analysed records")
	}

	// results in catalog order, every one present
	for _, spec := range cat.Results {
		st := steps[spec[2]]
		value, known := values[spec[1]]
		def := spec[5]
		if over, ok := rpDefinitions[spec[0]]; ok {
			def = over
		}
		res := rpResult{ID: spec[0], Key: spec[1], Step: st.ID, Label: spec[3], Type: nameOr(rpTypes[spec[0]], "integer"),
			Nature: nameOr(rpNature[spec[0]], "count"), Value: value, Unit: spec[4], Scope: st.Scope, Definition: def}
		switch {
		case known && value != nil:
			res.Status = st.Status
		case st.Status == "ok" || st.Status == "no_result":
			res.Status = "not_available"
		default:
			res.Status = st.Status
		}
		r.Results = append(r.Results, res)
		st.Results = append(st.Results, spec[0])
	}

	// one id per state: the same inputs, settings, results and completion
	// times give the same id; any change gives a new one
	basis := struct {
		Inputs  []map[string]any
		Steps   [][4]any
		Results []any
	}{Inputs: r.Inputs}
	for _, st := range r.Steps {
		basis.Steps = append(basis.Steps, [4]any{st.Key, st.Status, st.Parameters, st.CompletedAt})
	}
	for _, res := range r.Results {
		basis.Results = append(basis.Results, res.Value)
	}
	raw, _ := json.Marshal(basis)
	r.Run = map[string]string{"id": "run-" + rpFNV(string(raw)),
		"basis": "inputs (names, sizes, digests), step settings and status, step completion times and result values"}
	return r
}

func rpNewestTest(tests []reportTest) reportTest {
	best := tests[0]
	for _, x := range tests[1:] {
		if x.Created > best.Created {
			best = x
		}
	}
	return best
}

func rpNewestStructure(list []reportStructure) reportStructure {
	best := list[0]
	for _, s := range list[1:] {
		if s.Created > best.Created {
			best = s
		}
	}
	return best
}

func rpInteractionRow(it cmpInteraction) []string {
	return []string{it.Direction, strconv.Itoa(it.X), strconv.Itoa(it.Y), rpPtr(it.LFC), rpPtr(it.Padj), rpPtr(it.BaseMean)}
}

func rpPtr(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return "NA"
	}
	return strconv.FormatFloat(*v, 'g', -1, 64)
}

func rpFloat(values []*float64, k int) string {
	if k >= len(values) {
		return "NA"
	}
	return rpPtr(values[k])
}

func rpInt(values []int, k int) string {
	if k >= len(values) {
		return "NA"
	}
	return strconv.Itoa(values[k])
}

func rpNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func rpReviewState(review string) string {
	switch strings.ToLower(review) {
	case "reviewed", "relevant":
		return "reviewed"
	case "rejected", "not relevant":
		return "rejected"
	}
	return "unreviewed"
}

// rpFNV is the web's fnv1a: two 32-bit FNV-style hashes over the UTF-16
// code units, as 16 hex digits.
func rpFNV(text string) string {
	h1, h2 := uint32(0x811c9dc5), uint32(0x01000193)
	for _, unit := range utf16Units(text) {
		h1 = (h1 ^ uint32(unit)) * 0x01000193
		h2 = (h2 ^ uint32(unit)) * 0x5bd1e995
	}
	return fmt.Sprintf("%08x%08x", h1, h2)
}

func utf16Units(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// rpStructureFiles: a structure's .dbn (every window) and its .json (the
// web's structure schema: pairs on the RNA, energy, windows).
func rpStructureFiles(base string, s reportStructure, step string, selected bool) []rpFile {
	res := s.Result
	title := "Structure " + s.ID + " (" + nameOr(res.Meta.Engine, "CPLfold") + ")"
	if selected {
		title = "RNA structure prediction (" + nameOr(res.Meta.Engine, "CPLfold") + ", structure " + s.ID + ")"
	}
	dbn := stDBNText(s.Gene, res.Records)
	var pairs [][2]int
	crossing := 0
	var sequence, dot strings.Builder
	for _, rec := range res.Records {
		for _, p := range stRecordPairs(rec) {
			pairs = append(pairs, [2]int{p.I, p.J})
			if p.Layer > 0 {
				crossing++
			}
		}
		if len(res.Records) == 1 {
			sequence.WriteString(rec.Sequence)
			dot.WriteString(rec.DBN)
		}
	}
	offset := 0
	if len(res.Records) > 0 {
		offset, _ = res.Records[0].span()
		offset--
	}
	body := map[string]any{"$schema": "schema/structure.schema.json", "schema": "hyb2-result/1.0", "step": step, "source": s.Gene,
		"condition": nil, "method": nameOr(res.Meta.Engine, "CPLfold"), "length": s.Length, "energy": s.Energy, "energyUnit": "kcal/mol",
		"topology": nil, "crossingPairs": crossing, "pairs": pairs,
		"coordinates":    "1-based positions on " + s.Gene + " (pairs); sequence and dotBracket start at positionOffset + 1",
		"positionOffset": offset, "selectedCandidate": nil, "candidates": []any{},
		"prediction": "computational prediction; not an experimental structure"}
	if len(res.Records) == 1 && len(res.Records[0].Candidates) > 0 {
		// the web's candidates: rank and selectedCandidate from 1
		r := res.Records[0]
		candidates := make([]map[string]any, len(r.Candidates))
		for k, c := range r.Candidates {
			candidates[k] = map[string]any{"rank": k + 1, "type": rpNull(c.Type), "topology": rpNull(c.Topology), "energy": c.Energy,
				"effectiveEnergy": c.EffectiveEnergy, "pairs": c.Pairs, "crossingPairs": c.PseudoknotPairs, "dotBracket": c.Structure}
		}
		body["selectedCandidate"], body["candidates"] = r.SelectedCandidate+1, candidates
	}
	if len(res.Records) == 1 {
		body["sequence"], body["dotBracket"] = sequence.String(), dot.String()
		if r := res.Records[0]; r.Cutpoint > 0 {
			// CPLcofold: two strands; pairs are on the RNA when both regions are on one
			body["cutpoint"], body["regions"], body["positionOffset"] = r.Cutpoint, r.Segments, nil
			body["coordinates"] = "sequence and dotBracket are strand A then strand B (cutpoint nucleotides of A); pairs as drawn: RNA positions for two regions of one RNA, A+B indices for two RNAs"
		}
	} else {
		body["sequence"], body["dotBracket"] = nil, nil
		windows := make([]map[string]any, len(res.Records))
		for i, rec := range res.Records {
			start, end := rec.span()
			windows[i] = map[string]any{"start": start, "end": end, "dotBracket": rec.DBN, "energy": rec.Energy}
		}
		body["windows"] = windows
	}
	raw, _ := json.MarshalIndent(body, "", "  ")
	return []rpFile{
		{Path: base + ".dbn", MediaType: "text/plain", Text: dbn, Description: title + ", " + strconv.Itoa(s.Length) + " nt (dot-bracket)."},
		{Path: base + ".json", MediaType: "application/json", Text: string(raw) + "\n", Description: title + " (base pairs, windows), " + strconv.Itoa(s.Length) + " nt."},
	}
}

// ---- files

var rpColumns = map[string][3]string{
	"sample": {"string", "", "Sample id in the project."}, "label": {"string", "", "Sample label."},
	"condition": {"string", "", "Condition of the sample."}, "replicate": {"string", "", "Replicate number."},
	"valid_records": {"integer", "records", "Valid records of the sample's analysis index."}, "reads": {"integer", "reads", "Collapsed source reads of the records."},
	"with_dg": {"integer", "records", "Records with a ΔG value."}, "intra": {"integer", "records", "Records with both arms on one RNA."},
	"inter": {"integer", "records", "Records with arms on two RNAs."}, "name": {"string", "", "RNA name."},
	"records": {"integer", "records", "Number of records (per arm for RNAs)."}, "support": {"number", "support units", "Sum of cluster support."},
	"rna": {"string", "", "RNA name."}, "rna_1": {"string", "", "RNA of arm 1 (name order)."}, "rna_2": {"string", "", "RNA of arm 2 (name order)."},
	"test": {"string", "", "Differential test id in the project."}, "label_a": {"string", "", "Condition A of the test."},
	"label_b": {"string", "", "Condition B of the test."}, "genes": {"string", "", "RNAs tested."}, "created": {"string", "", "When the result was made."},
	"cells_tested": {"integer", "cells", "Cells tested."}, "significant": {"integer", "cells", "Cells with padj below the cutoff."},
	"interactions_a": {"integer", "interactions", "Merged interactions higher in A."}, "interactions_b": {"integer", "interactions", "Merged interactions higher in B."},
	"error":     {"string", "", "Why the result could not be read; empty when it was."},
	"direction": {"string", "", "up = higher in A; down = higher in B."}, "x": {"integer", "nt", "Arm 1 cell start, 1-based."},
	"y": {"integer", "nt", "Arm 2 cell start, 1-based."}, "log2FoldChange": {"number", "log2", "DESeq2 log2 fold change A/B."},
	"padj": {"number", "", "Benjamini-Hochberg adjusted p-value; NA when not tested."}, "baseMean": {"number", "normalised count", "Mean normalised count over all samples."},
	"side": {"string", "", "enriched_in_one or enriched_in_two."}, "dG_one": {"number", "kcal/mol", "RNAcofold energy with condition one's sequence."},
	"dG_two": {"number", "kcal/mol", "RNAcofold energy with condition two's sequence."}, "n": {"integer", "interactions", "Paired energies."},
	"mean_dG_one": {"number", "kcal/mol", "Mean dG with condition one's sequence."}, "mean_dG_two": {"number", "kcal/mol", "Mean dG with condition two's sequence."},
	"mean_diff_one_minus_two": {"number", "kcal/mol", "Mean of dG(one) − dG(two)."}, "t": {"number", "", "Paired t statistic."},
	"p": {"number", "", "Two-sided p-value."}, "structure": {"string", "", "Structure id in the project."},
	"mode": {"string", "", "whole RNA or windows."}, "windows": {"string", "", "Folded windows, start–end."},
	"engine": {"string", "", "Folding engine."}, "evidence": {"string", "", "Samples whose chimeras guided the fold (or gave the reference)."},
	"length": {"integer", "nt", "Folded length."}, "pairs": {"integer", "pairs", "Base pairs."},
	"crossing_pairs": {"integer", "pairs", "Pseudoknotted base pairs."}, "energy": {"number", "kcal/mol", "Free energy; NA for several windows."},
}

func (t *rpTable) tsv() string {
	var b strings.Builder
	clean := func(s string) string {
		if s == "" {
			return "NA"
		}
		return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
	}
	b.WriteString(strings.Join(t.Header, "\t") + "\n")
	for _, row := range t.Rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = clean(c)
		}
		b.WriteString(strings.Join(cells, "\t") + "\n")
	}
	return b.String()
}

func (t *rpTable) columns() []map[string]any {
	out := make([]map[string]any, len(t.Header))
	for i, name := range t.Header {
		spec, ok := rpColumns[name]
		if !ok {
			spec = [3]string{"string", "", name}
		}
		var unit any
		if spec[1] != "" {
			unit = spec[1]
		}
		out[i] = map[string]any{"name": name, "type": spec[0], "unit": unit, "description": spec[2]}
	}
	return out
}

func rpJSON(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw) + "\n"
}

func (r *rpRecord) stepsJSON(full bool) []map[string]any {
	out := make([]map[string]any, len(r.Steps))
	for i, st := range r.Steps {
		m := map[string]any{"id": st.ID, "key": st.Key, "title": st.Title, "status": st.Status, "detail": st.Detail, "scope": st.Scope, "completedAt": st.CompletedAt}
		if full {
			m["parameters"], m["inputs"], m["results"], m["tables"], m["figures"] = st.Parameters, nonNil(st.Inputs), nonNil(st.Results), nonNil(st.Tables), nonNil(st.Figures)
		}
		out[i] = m
	}
	return out
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func (r *rpRecord) natureVocabulary() map[string]string { return r.catalog.Nature }

func (r *rpRecord) summaryJSON() map[string]any {
	warnings := r.Warnings
	if warnings == nil {
		warnings = []map[string]string{}
	}
	return map[string]any{"$schema": "schema/summary.schema.json", "schema": r.Schema, "generatedAt": r.GeneratedAt, "application": r.Application,
		"scope": r.Scope, "run": r.Run, "steps": r.stepsJSON(false), "results": r.Results, "natureVocabulary": r.natureVocabulary(),
		"warnings": warnings, "literature_search": r.Literature, "limitations": r.Limitations}
}

func (r *rpRecord) dataDictionary(tables []*rpTable) map[string]any {
	var list []map[string]any
	for _, t := range tables {
		list = append(list, map[string]any{"id": t.ID, "file": t.File, "step": t.Step, "title": t.Title, "scope": t.Scope, "rowCount": len(t.Rows), "columns": t.columns()})
	}
	if list == nil {
		list = []map[string]any{}
	}
	var results []map[string]any
	for _, res := range r.Results {
		results = append(results, map[string]any{"id": res.ID, "key": res.Key, "label": res.Label, "type": res.Type, "nature": res.Nature, "unit": res.Unit, "definition": res.Definition})
	}
	return map[string]any{"$schema": "schema/data_dictionary.schema.json", "schema": r.Schema, "run": r.Run,
		"conventions": map[string]any{"coordinates": "1-based, inclusive, on each RNA's own sequence; differential x/y are 10-nt cell starts",
			"missing": map[string]any{"tsv": "NA", "json": nil}, "notRun": "status not_run; values null, never 0",
			"statusVocabulary": r.catalog.Status, "natureVocabulary": r.catalog.Nature,
			"units": "given per column; counts are record counts unless the column says support units"},
		"tables": list, "results": results}
}

func (r *rpRecord) provenance() map[string]any {
	inputs := r.Inputs
	if inputs == nil {
		inputs = []map[string]any{}
	}
	return map[string]any{"$schema": "schema/provenance.schema.json", "schema": r.Schema, "run": r.Run, "application": r.Application,
		"generatedAt": r.GeneratedAt, "environment": "HYB2 Desktop on this computer; all computation local", "scope": r.Scope,
		"inputs": inputs, "steps": r.stepsJSON(true),
		"methods": map[string]any{"parsing": "HYB2 analysis index (hyb2analysis, the web parser's rules)",
			"differentialTest": "DESeq2 1.46 Wald test, numpy port; hyb2_compare interaction merge",
			"foldingEnergy":    "ViennaRNA RNAcofold, 37 °C, default parameters",
			"structure":        "CPLfold (evidence-guided), ViennaRNA MFE or COMRADES(ViennaRNA)"},
		"reproducibility": "Versions, parameters and input names are recorded so results can be traced. Re-running needs the same input files; the package does not contain them."}
}

func (r *rpRecord) evidenceJSON() map[string]any {
	sources := r.Evidence
	if sources == nil {
		sources = []map[string]any{}
	}
	return map[string]any{"$schema": "schema/evidence.schema.json", "schema": r.Schema, "run": r.Run, "literature_search": r.Literature, "sources": sources,
		"note": "External records are background material chosen by the researcher. Citations are not verified; a database entry or a literature mention does not validate a structure or an interaction."}
}

func rpFormat(res rpResult) string {
	switch v := res.Value.(type) {
	case nil:
		return "—"
	case int64:
		return FormatNumber(v)
	case int:
		return FormatNumber(int64(v))
	case float64:
		if res.Unit == "p-value" {
			return strconv.FormatFloat(v, 'e', 2, 64)
		}
		if v == math.Trunc(v) && math.Abs(v) < 1e15 {
			return FormatNumber(int64(v))
		}
		return strconv.FormatFloat(v, 'g', 6, 64)
	}
	return fmt.Sprint(res.Value)
}

func mdCell(s string) string {
	return strings.NewReplacer("|", "\\|", "\r\n", " ", "\n", " ").Replace(s)
}

func deref(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

// reportMarkdown is the web's template report.md for the record.
func (r *rpRecord) reportMarkdown() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# HYB2 analysis report")
	w("")
	w("- Scope: **%s**. %s", r.Scope["label"], r.Scope["description"])
	w("- Purpose: %s", r.purposeLine())
	w("- Generated: %s by %s %s, locally on this computer.", r.GeneratedAt, r.Application["name"], r.Application["version"])
	w("- Run: `%s` (changes when %s change).", r.Run["id"], r.Run["basis"])
	w("- Schema: `%s`. Ids: S = step, R = result, T = table, F = figure, E = external source, W = warning, I = input.", r.Schema)
	w("")
	w("This text was generated from a fixed template, not by a language model. Every number below is also in `summary.json` under the same id.")
	w("")
	w("## Steps")
	w("")
	w("| Id | Step | Status | Scope | Completed | Detail |")
	w("|---|---|---|---|---|---|")
	for _, st := range r.Steps {
		completed := "—"
		if st.CompletedAt != nil {
			completed = *st.CompletedAt
		} else if st.Status == "ok" || st.Status == "no_result" {
			completed = "computed from the current project"
		}
		w("| %s | %s | `%s` | %s | %s | %s |", st.ID, mdCell(st.Title), st.Status, mdCell(deref(st.Scope, "—")), mdCell(completed), mdCell(deref(st.Detail, st.StatusText)))
	}
	w("")
	w("## Results")
	w("")
	w("Results of steps that did not run are listed with no value (—), never as 0. Each result covers its step's scope above.")
	w("")
	w("| Id | Step | Result | Value | Unit | Status | Nature |")
	w("|---|---|---|---|---|---|---|")
	for _, res := range r.Results {
		w("| %s | %s | %s | %s | %s | `%s` | %s |", res.ID, res.Step, mdCell(res.Label), mdCell(rpFormat(res)), mdCell(res.Unit), res.Status, res.Nature)
	}
	w("")
	var natures []string
	for _, k := range rpNatureOrder {
		natures = append(natures, "`"+k+"` "+r.catalog.Nature[k])
	}
	w("Nature: %s.", strings.Join(natures, "; "))
	w("")
	w("## Warnings")
	w("")
	if len(r.Warnings) == 0 {
		w("- None.")
	}
	for _, x := range r.Warnings {
		w("- **%s** (%s): %s", x["id"], x["step"], x["text"])
	}
	w("")
	w("## Limitations")
	w("")
	for _, l := range r.Limitations {
		w("- %s", l)
	}
	w("")
	w("## Inputs")
	w("")
	for _, in := range r.Inputs {
		line := fmt.Sprintf("- **%s** %s: `%v`", in["id"], in["role"], nameOr(fmt.Sprint(in["fileName"]), "unnamed"))
		if n, ok := in["records"].(int64); ok {
			line += ", " + FormatNumber(n) + " valid records"
		}
		if sha, ok := in["sha256"].(string); ok && sha != "" {
			line += ", SHA-256 `" + sha + "`"
		} else {
			line += ", SHA-256 " + fmt.Sprint(in["sha256Status"])
		}
		if c, _ := in["condition"].(string); c != "" {
			line += ", condition " + c
		}
		if l, _ := in["label"].(string); l != "" {
			line += " (" + l + ")"
		}
		w("%s", line)
	}
	w("")
	w("## External sources")
	w("")
	w("Literature search status: `%s`.", r.Literature["status"])
	w("")
	for _, e := range r.Evidence {
		w("- **%s** %s, query `%s`: %s; review `%s`; citations not verified.", e["id"], e["source"], e["query"], e["evidenceNature"], e["review"].(map[string]any)["state"])
	}
	w("")
	w("## Tables and figures")
	w("")
	for _, t := range r.Tables {
		w("- **%s** (%s) `%s`: %s; %s.", t.ID, t.Step, t.File, t.Title, t.Scope)
	}
	for _, f := range r.Figures {
		w("- **%s** (%s) `%s`: %s (analysis package).", f.ID, f.Step, f.File, f.Title)
	}
	for _, s := range r.Structures {
		w("- `%s`: %s", s.Path, s.Description)
	}
	w("")
	w("## Parameters")
	w("")
	for _, st := range r.Steps {
		if st.Status == "not_run" || len(st.Parameters) == 0 {
			continue
		}
		keys := make([]string, 0, len(st.Parameters))
		for k := range st.Parameters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			v := st.Parameters[k]
			if v == nil {
				continue
			}
			text := fmt.Sprint(v)
			if _, isMap := v.(map[string]any); isMap {
				raw, _ := json.Marshal(v)
				text = string(raw)
			}
			parts = append(parts, k+" = "+text)
		}
		w("- **%s** %s", st.ID, strings.Join(parts, "; "))
	}
	w("")
	return b.String()
}

func (r *rpRecord) readme(audience string, files []rpFile) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	what := "Context package for an AI assistant"
	if audience == "analysis" {
		what = "Data package for further analysis"
	}
	w("# HYB2 result package")
	w("")
	w("%s from %s %s, generated %s.", what, r.Application["name"], r.Application["version"], r.GeneratedAt)
	w("")
	w("Scope: **%s**: %s", r.Scope["label"], r.Scope["description"])
	if r.selected() {
		w("")
		w("**This is a selected-results package, not the whole project.**")
	}
	w("")
	w("## Reading order")
	w("")
	if audience == "ai" {
		w("1. `report.md`: what was analysed, which steps ran and on what scope, the results with ids, warnings and limits.")
		w("2. `summary.json`: the same results as typed values with status and unit.")
		w("3. `data_dictionary.json` before reading a table; `provenance.json` for methods and parameters; `evidence.json` for external sources.")
		w("4. Tables under `tables/` only when a question needs rows; structures under `results/` (`.dbn`, and `.json` with base pairs and coordinates).")
	} else {
		w("1. `data_dictionary.json`: columns, units, coordinates and missing values.")
		w("2. `manifest.json`: every file with its size and SHA-256.")
		w("3. `data_dictionary.json` before reading a table; `provenance.json` for methods and parameters.")
		w("4. Tables under `tables/` only when a question needs rows; structures under `results/` (`.dbn`, and `.json` with base pairs and coordinates); figures under `figures/`.")
	}
	w("5. `schema/` has a JSON Schema for each HYB2 JSON file; each file names its own in `$schema` (structure and types only).")
	w("")
	w("Run `%s`: results from different runs have different ids; do not mix them.", r.Run["id"])
	w("")
	w("## Conventions")
	w("")
	w("- Coordinates are 1-based and inclusive, on each RNA's own sequence unless a column says otherwise.")
	w("- Missing values are `NA` in TSV and `null` in JSON. A step that did not run has status `not_run` and its results are `null`, never 0.")
	var vocab []string
	for _, k := range rpStatusOrder {
		vocab = append(vocab, "`"+k+"` "+strings.ToLower(r.catalog.Status[k]))
	}
	w("- Status vocabulary: %s.", strings.Join(vocab, "; "))
	w("- Ids are fixed and shared with HYB2 Web Lite: R26 always means \"%s\". Cite them (\"R26\", \"T05\", \"E01\") instead of \"the report\".", r.catalog.Results[25][3])
	w("")
	w("## Not included")
	w("")
	w("- The input reads, HYB, FASTA and project files themselves (only their names, and SHA-256 when the project recorded one).")
	w("- Local file paths, keys or account details.")
	if audience == "ai" {
		w("- Large tables (every RNA pair, every test's interactions) and figures: use the analysis package.")
	} else {
		w("- Interpretation text: this package has data and definitions only.")
	}
	w("")
	w("## Files")
	w("")
	for _, f := range files {
		w("- `%s`: %s", f.Path, f.Description)
	}
	w("- `manifest.json`: file list with sizes and SHA-256.")
	if len(r.omitted) > 0 {
		w("")
		w("## Left out by the researcher")
		w("")
		for _, path := range r.omitted {
			w("- `%s`", path)
		}
	}
	w("")
	return b.String()
}

// files are a package's files ("ai" or "analysis"), README first, without
// the manifest.
func (r *rpRecord) files(audience string) []rpFile {
	var out []rpFile
	var tables, all []*rpTable
	for _, t := range r.Tables {
		for _, a := range t.Audience {
			if a == audience {
				all = append(all, t)
				if !r.leftOut(t.File) {
					tables = append(tables, t)
				}
			}
		}
	}
	if audience == "ai" {
		out = append(out, rpFile{"report.md", "text/markdown", r.reportMarkdown(), "Template report: scope, steps, results with ids, warnings, limits, sources."})
	}
	out = append(out,
		rpFile{"summary.json", "application/json", rpJSON(r.summaryJSON()), "Run, steps and results as typed values with status, nature, unit and scope."},
		rpFile{"data_dictionary.json", "application/json", rpJSON(r.dataDictionary(tables)), "Conventions, status and nature vocabularies, every column of every table."},
		rpFile{"provenance.json", "application/json", rpJSON(r.provenance()), "Run id, inputs, methods, parameters and completion time per step."})
	if audience == "ai" {
		out = append(out, rpFile{"evidence.json", "application/json", rpJSON(r.evidenceJSON()), "External sources added to the report, with query, nature and review state."})
	}
	for _, name := range []string{"summary", "provenance", "data_dictionary", "evidence", "structure"} {
		if audience == "analysis" && name == "evidence" {
			continue
		}
		raw, _ := resultSchemaFS.ReadFile("resultschema/" + name + ".schema.json")
		out = append(out, rpFile{"schema/" + name + ".schema.json", "application/schema+json", string(raw),
			"JSON Schema (2020-12) of " + name + ": structure and types, not scientific correctness."})
	}
	for _, t := range tables {
		out = append(out, rpFile{t.File, "text/tab-separated-values", t.tsv(), t.ID + " " + t.Title + " (" + t.Scope + ")."})
	}
	for _, s := range r.Structures {
		if audience == "ai" && strings.HasPrefix(s.Path, "results/structures/") {
			continue // the analysis package has every structure
		}
		out = append(out, s)
	}
	if audience == "analysis" {
		for _, f := range r.Figures {
			if svg := f.SVG(); svg != "" {
				out = append(out, rpFile{f.File, "image/svg+xml", svg, f.ID + " " + f.Title + "."})
			}
		}
	}
	out = r.keepFiles(out, all)
	return append([]rpFile{{"README.md", "text/markdown", r.readme(audience, out), "What the package contains, reading order, conventions, what is excluded."}}, out...)
}

// zip is the package as a ZIP under hyb2-result/, with manifest.json.
func (r *rpRecord) zip(audience string, now time.Time) ([]byte, error) {
	files := r.files(audience)
	var entries []map[string]any
	for _, f := range files {
		sum := sha256.Sum256([]byte(f.Text))
		entries = append(entries, map[string]any{"path": f.Path, "mediaType": f.MediaType, "bytes": len(f.Text), "sha256": hex.EncodeToString(sum[:]), "description": f.Description})
	}
	manifest := map[string]any{"schema": r.Schema, "run": r.Run, "generatedAt": r.GeneratedAt, "application": r.Application, "scope": r.Scope,
		"files": entries, "leftOutByResearcher": nonNil(r.omitted), "note": "sha256 of each file's bytes."}
	files = append(files, rpFile{"manifest.json", "application/json", rpJSON(manifest), "File list with sizes and SHA-256."})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "hyb2-result/" + f.Path, Method: zip.Deflate, Modified: now})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.Text)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
