package views

// The structure report JSON's parts laid out as the web's structureReport
// (structure-ui.js): the input files with their SHA-256, the prediction
// shown (its pairs and selected candidate), the CPLfold candidates and the
// visualization settings. export_pages.go's exportReport adds them to the
// desktop's own keys.

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// stReportFile is the web's sourceHyb / sourceFasta entry: the file's name,
// size and SHA-256 (sha256Status "available", or "unavailable" when the
// file can no longer be read).
func stReportFile(path string) map[string]any {
	out := map[string]any{"fileName": filepath.Base(path), "fileSize": nil, "sha256": nil, "sha256Status": "unavailable"}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return out
	}
	out["fileSize"], out["sha256"], out["sha256Status"] = n, hex.EncodeToString(h.Sum(nil)), "available"
	return out
}

// stReportInputs are the web's sourceHyb (one entry per .hyb file read; the
// web names one) and sourceFasta, hashed when the report is saved.
func stReportInputs(a *structureResult) (hyb []map[string]any, fasta map[string]any) {
	hyb = []map[string]any{}
	for _, h := range a.Meta.EvidenceInputs.Hyb {
		if h.Path != "" {
			hyb = append(hyb, stReportFile(h.Path))
		}
	}
	if path := a.Meta.Reference.Path; path != "" {
		fasta = stReportFile(path)
	}
	return hyb, fasta
}

// stWebViewMode is the web's viewMode for a desktop view label.
func stWebViewMode(view string) string {
	return strings.ToLower(nameOr(view, "Arc"))
}

// stReportPairs are the records' pairs as the web's prediction.pairs:
// positions as the pairs TSV (A+B for two strands), bases, pair type,
// layer, evidence support and whether the pair was a hard constraint.
func stReportPairs(a *structureResult, records []structureRecord) []map[string]any {
	cpl := stIsCPL(a)
	out := []map[string]any{}
	for _, pr := range stShownPairs(records, stHardPairs(a)) {
		r := records[pr.record]
		i, j := pr.I, pr.J
		if r.Cutpoint > 0 {
			// A+B positions, lower first (region 2 may lie before region 1)
			var swapped bool
			if i, j, swapped = r.localPair(stPair{I: pr.I, J: pr.J}); swapped {
				pr.BaseI, pr.BaseJ = pr.BaseJ, pr.BaseI
			}
		}
		layer := "nested"
		if cpl {
			layer = stLayerName(pr.Layer)
		}
		pair := map[string]any{"left": i, "right": j, "leftBase": string(pr.BaseI), "rightBase": string(pr.BaseJ),
			"type": string(pr.BaseI) + "–" + string(pr.BaseJ), "layer": layer, "evidenceSupport": pr.Bonus, "hardConstraint": pr.Hard}
		if r.Cutpoint > 0 {
			strand := func(k int) (string, int) {
				if k <= r.Cutpoint {
					return "A", k
				}
				return "B", k - r.Cutpoint
			}
			pair["leftStrand"], pair["leftStrandPosition"] = strand(i)
			pair["rightStrand"], pair["rightStrandPosition"] = strand(j)
			pair["intermolecular"] = i <= r.Cutpoint && r.Cutpoint < j
			pair["leftRnaPosition"], pair["rightRnaPosition"] = r.rnaPosition(i), r.rnaPosition(j)
		}
		out = append(out, pair)
	}
	return out
}

// stReportWeb adds the web's sections to the report payload: prediction
// (the records shown: all windows, or the one chosen), the CPLfold
// candidates, and visualization. The input files are added when the report
// is saved (stReportInputs reads them).
func (p *structurePage) stReportWeb(a *structureResult, payload map[string]any) {
	records := stPickRecords(a.Records, p.record)
	cpl := stIsCPL(a)
	prediction := map[string]any{"engine": stEngine(a), "pairs": stReportPairs(a, records),
		"energyMetadata": nil, "selectedCandidateIndex": nil, "selectedCandidateRank": nil, "cutpoint": nil}
	if len(a.Meta.EnergyMetadata) > 0 {
		prediction["energyMetadata"] = a.Meta.EnergyMetadata
	}
	hard := stHardPairs(a)
	keys := slices.SortedFunc(maps.Keys(hard), func(x, y [2]int) int { return cmp.Or(x[0]-y[0], x[1]-y[1]) })
	constraints := []map[string]int{}
	for _, k := range keys {
		constraints = append(constraints, map[string]int{"left": k[0], "right": k[1]})
	}
	prediction["constraintCount"], prediction["constraints"] = len(constraints), constraints
	if len(records) == 1 {
		r := records[0]
		prediction["energy"], prediction["dotBracket"] = r.Energy, r.DBN
		if r.Cutpoint > 0 {
			prediction["cutpoint"], prediction["dotBracketWithBreak"] = r.Cutpoint, stWithBreak(r.DBN, r.Cutpoint)
			prediction["strandLengths"] = []int{r.Cutpoint, len(r.Sequence) - r.Cutpoint}
		}
		if cpl {
			prediction["selectedCandidateIndex"], prediction["selectedCandidateRank"] = r.SelectedCandidate, r.SelectedCandidate+1
			prediction["crossingPairs"] = stCrossingPairs(r.DBN)
		}
	}
	payload["prediction"] = prediction
	if cpl {
		var candidates []map[string]any
		for _, r := range records {
			for k, c := range r.Candidates {
				candidates = append(candidates, map[string]any{"window": stRecordLabel(stGene(a), r), "rank": k + 1,
					"selected": k == r.SelectedCandidate, "type": c.Type, "topology": c.Topology, "energy": c.Energy,
					"effectiveEnergy": c.EffectiveEnergy, "phase1Energy": c.Phase1Energy, "score": c.Score,
					"dotBracket": stWithBreak(c.Structure, r.Cutpoint), "basePairs": c.Pairs, "crossingPairs": stCrossingPairs(c.Structure)})
			}
		}
		payload["cplfoldCandidates"], payload["cplfoldParameters"] = candidates, a.Meta.Parameters
	}
	payload["visualization"] = map[string]any{"viewMode": stWebViewMode(p.view), "window": p.record,
		"shadeByChimeraSupport": p.shade, "selectedNucleotide": p.arcs.Selected}
}
