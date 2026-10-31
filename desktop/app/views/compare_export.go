package views

// ④ Compare: the web Compare page's exports for the selected test.
//
//   - map PNG and significant cells TSV (web deseq-card.js: deseq-download-png,
//     deseq-export-significant);
//   - the files for R or the HYB2 command-line tool (web analysis-ui.js:
//     comparison-export-deseq-counts / -hyb2-names / -deseq-metadata) and the
//     parameters JSON (comparison-export-params). The runtime writes the
//     first three next to the results, byte-identical to the web's for the
//     same counts (desktop/runtime/compare.py EXPORT_FILES);
//   - "Top 10 local maps per direction · ZIP" (web batch-tools.js runMaps):
//     the runtime keeps the windows' cells and the histogram of every
//     replicate's cell counts in top_maps.json, and the ZIP is written here
//     with the contrast quantile chosen on the page, by ports of the web's
//     histogramQuantile, mapSvg and mapFiles.
//
// A test run before these files existed says so; run it again.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/widgets"
)

// cmpExportFiles are the runtime's export files (compare.py EXPORT_FILES).
var cmpExportFiles = map[string]string{"counts": "counts.table.txt", "names": "names.table",
	"metadata": "sample_metadata.tsv", "top_maps": "top_maps.json"}

// cmpTopMaps is top_maps.json.
type cmpTopMaps struct {
	Labels  map[string]string `json:"labels"`
	Regions []cmpTopRegion    `json:"regions"`
	Samples []struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		FileName  string `json:"file_name"`
		Condition string `json:"condition"`
		RNAX      string `json:"rna_x"`
		RNAY      string `json:"rna_y"`
		Records   *int64 `json:"records"`
	} `json:"samples"`
	// Panels[region][sample] are the window's cells [x, y, count].
	Panels    [][][][3]int64 `json:"panels"`
	Histogram [][2]float64   `json:"histogram"`
}

type cmpTopRegion struct {
	Direction string   `json:"direction"`
	Rank      int      `json:"rank"`
	X         int64    `json:"x"`
	Y         int64    `json:"y"`
	Padj      float64  `json:"padj"`
	LFC       *float64 `json:"log2FoldChange"`
	X1        int64    `json:"x1"`
	X2        int64    `json:"x2"`
	Y1        int64    `json:"y1"`
	Y2        int64    `json:"y2"`
}

// cmpZipFile is one file of a ZIP, as the web's Hyb2Results.zip takes them.
type cmpZipFile struct {
	Path, Text string
}

// ---- file locations

// comparisonDir is the directory of a test's files.
func (p *comparePage) comparisonDir(e *comparisonEntry) string {
	rel := e.Paths["significant"]
	if rel == "" {
		rel = "comparisons/" + e.ID + "/significant.tsv"
	}
	return filepath.Join(p.a.Project.Path, filepath.FromSlash(path.Dir(rel)))
}

// comparisonFile is one of the test's files, or "" with a toast when it is
// missing (a test run before the file existed).
func (p *comparePage) comparisonFile(e *comparisonEntry, name string) string {
	file := filepath.Join(p.comparisonDir(e), name)
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		p.a.Toast("This test has no %s yet: it was run before the file existed. Run it again to get it.", name)
		return ""
	}
	return file
}

// ---- exports

// exportCopy saves a copy of one of the test's files.
func (p *comparePage) exportCopy(key, title, name string) {
	e, _, _, ok := p.exportTarget()
	if !ok {
		return
	}
	file := p.comparisonFile(e, cmpExportFiles[key])
	if file == "" {
		return
	}
	saveExport(p.a, title, name, func() ([]byte, error) { return os.ReadFile(file) })
}

// exportSignificantTSV writes the significant cells as the web's
// significantToTsv: x y log2FoldChange padj baseMean in padj order, numbers
// as JavaScript prints them (significant.tsv already holds them so).
func (p *comparePage) exportSignificantTSV() {
	e, _, ctx, ok := p.exportTarget()
	if !ok {
		return
	}
	file := p.comparisonFile(e, path.Base(nameOr(e.Paths["significant"], "significant.tsv")))
	if file == "" {
		return
	}
	saveExport(p.a, "Export significant cells", exportName(".tsv", ctx.LabelA, "vs", ctx.LabelB, "significant"), func() ([]byte, error) {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return cmpSignificantTSV(raw)
	})
}

// cmpSignificantTSV picks the web's columns out of significant.tsv.
func cmpSignificantTSV(raw []byte) ([]byte, error) {
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	header := strings.Split(lines[0], "\t")
	want := []string{"x", "y", "log2FoldChange", "padj", "baseMean"}
	index := make([]int, len(want))
	for i, name := range want {
		index[i] = slicesIndex(header, name)
		if index[i] < 0 {
			return nil, fmt.Errorf("significant.tsv has no %s column", name)
		}
	}
	var b strings.Builder
	b.WriteString(strings.Join(want, "\t") + "\n")
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		for i, k := range index {
			if i > 0 {
				b.WriteByte('\t')
			}
			if k < len(parts) {
				b.WriteString(parts[k])
			}
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func slicesIndex(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// exportMapPNG draws the significant map off screen in the light theme,
// as shown (zoom and direction), at twice its size as the web's PNG.
func (p *comparePage) exportMapPNG() {
	e, result, ctx, ok := p.exportTarget()
	if !ok {
		return
	}
	cells := append([]widgets.Cell(nil), p.heat.Cells()...)
	view := p.heat.View()
	length := float64(p.mapLength(e, result))
	directions := []string{"", " · higher in A only", " · higher in B only"}
	title := "Differential coverage map · " + ctx.LabelA + " vs " + ctx.LabelB + directions[min(max(p.direction, 0), 2)]
	axis := cmpAxisLabel(e)
	saveExport(p.a, "Export the significant map", exportName(".png", ctx.LabelA, "vs", ctx.LabelB, "map"), func() ([]byte, error) {
		return cmpMapPNG(cells, view, length, title, axis, ctx.LabelA, ctx.LabelB)
	})
}

// cmpMapPNG draws a significant map with its title, axes note and legend.
func cmpMapPNG(cells []widgets.Cell, view widgets.Window, length float64, title, axis, labelA, labelB string) ([]byte, error) {
	heat := widgets.NewHeatmap()
	heat.MinCell = 3
	heat.SetCells(cells, length, length)
	if view.X1 > view.X0 && view.Y1 > view.Y0 {
		heat.SetView(view)
	}
	return renderPNG(600, 680, func(c *ui.Context) {
		t := c.Theme()
		ui.Text(c, title).FontSize(13).Bold().TextColor(t.Text)
		ui.Text(c, axis).FontSize(11).TextColor(t.TextMuted)
		heat.Show(c.Key("map"), 560, title)
		cmpLegend(c, labelA, labelB)
	})
}

// cmpAxisLabel names the map's axes: "X" or "X × Y".
func cmpAxisLabel(e *comparisonEntry) string {
	genes := strings.Join(e.Genes, ", ")
	if e.Parameters.Gene2 != nil && *e.Parameters.Gene2 != "" && !(len(e.Genes) == 1 && e.Genes[0] == *e.Parameters.Gene2) {
		return "x: " + genes + " · y: " + *e.Parameters.Gene2
	}
	return "x, y: " + genes
}

// cmpParamsJSON is the parameters JSON, shaped as the web's
// comparisonParameters (keys in its order).
type cmpParamsJSON struct {
	Application      string             `json:"application"`
	Analysis         string             `json:"analysis"`
	GeneratedLocally bool               `json:"generatedLocally"`
	ComparisonID     string             `json:"comparisonId"`
	Datasets         []cmpParamsDataset `json:"datasets"`
	Parameters       struct {
		ConditionALabel   string  `json:"conditionALabel"`
		ConditionBLabel   string  `json:"conditionBLabel"`
		RNAX              string  `json:"rnaX"`
		RNAY              string  `json:"rnaY"`
		BinSize           int     `json:"binSize"`
		Measure           string  `json:"measure"`
		Normalisation     string  `json:"normalisation"`
		Pseudocount       float64 `json:"pseudocount"`
		SimilarityMinimum float64 `json:"similarityMinimum"`
		SimilarityPanel   string  `json:"similarityPanel"`
		SimilarityMethod  string  `json:"similarityMethod"`
	} `json:"parameters"`
	DifferentialTest struct {
		Method     string `json:"method"`
		Parameters struct {
			MinCount                  *float64 `json:"minCount"`
			MergeRange                *float64 `json:"mergeRange"`
			PadjCutoff                *float64 `json:"padjCutoff"`
			IndependentFilteringAlpha float64  `json:"independentFilteringAlpha"`
			BinSize                   int      `json:"binSize"`
			Measure                   string   `json:"measure"`
		} `json:"parameters"`
		Result struct {
			CompletedAt string             `json:"completedAt"`
			Datasets    []cmpParamsDataset `json:"datasets"`
			Summary     comparisonSummary  `json:"summary"`
		} `json:"result"`
	} `json:"differentialTest"`
}

type cmpParamsDataset struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Condition string `json:"condition"`
	RNAX      string `json:"rnaX"`
	RNAY      string `json:"rnaY"`
}

// exportParamsJSON writes the test's and the page's settings.
func (p *comparePage) exportParamsJSON() {
	e, result, ctx, ok := p.exportTarget()
	if !ok {
		return
	}
	var out cmpParamsJSON
	out.Application, out.Analysis, out.GeneratedLocally, out.ComparisonID = "HYB2 Desktop", "comparison", true, e.ID
	gene2 := ""
	if e.Parameters.Gene2 != nil {
		gene2 = *e.Parameters.Gene2
	}
	dataset := func(id, condition, gene string) cmpParamsDataset {
		return cmpParamsDataset{ID: id, Label: p.sampleName(id), Condition: condition, RNAX: gene, RNAY: nameOr(gene2, gene)}
	}
	for _, id := range e.SamplesOne {
		out.Datasets = append(out.Datasets, dataset(id, "A", ctx.GeneA))
	}
	for _, id := range e.SamplesTwo {
		out.Datasets = append(out.Datasets, dataset(id, "B", ctx.GeneB))
	}
	pr := &out.Parameters
	pr.ConditionALabel, pr.ConditionBLabel = ctx.LabelA, ctx.LabelB
	pr.RNAX, pr.RNAY = ctx.GeneA, nameOr(gene2, ctx.GeneA)
	pr.BinSize, _ = strconv.Atoi(strings.TrimSuffix(p.quick.bin, " nt"))
	pr.Measure = map[bool]string{false: "record-count", true: "cluster-support"}[p.measure == cmpMeasureSupport]
	pr.Normalisation, pr.Pseudocount = "per-million-library-size", 1
	if p.normalise == cmpNormaliseNone {
		pr.Normalisation, pr.Pseudocount = "none", 0.5
	}
	pr.SimilarityMinimum = 1
	if e.Parameters.SimMin != nil {
		pr.SimilarityMinimum = *e.Parameters.SimMin
	}
	pr.SimilarityPanel = "white (as similarity_heatmap.R)"
	if p.simPanel == cmpPanelGrey {
		pr.SimilarityPanel = "grey (#EBEBEB, display only)"
	}
	pr.SimilarityMethod = "HYB2 plot_similarity_map on one merged raw map per condition (differential.py): min(A, B) of bins present in both; similarityMinimum = awk $3 >= MIN on each merged map"
	dt := &out.DifferentialTest
	dt.Method = "DESeq2 1.46 Wald test, condition A (condition_one) vs B, HYB2 Desktop's numpy port (hyb2analysis.deseq); hyb2_compare interaction merge"
	dt.Parameters.MinCount, dt.Parameters.MergeRange, dt.Parameters.PadjCutoff = e.Parameters.MinCount, e.Parameters.Range, e.Parameters.PadjCutoff
	dt.Parameters.IndependentFilteringAlpha, dt.Parameters.BinSize, dt.Parameters.Measure = 0.01, 10, "record-count"
	dt.Result.CompletedAt, dt.Result.Datasets, dt.Result.Summary = e.CreatedAt, out.Datasets, result.Summary
	saveExport(p.a, "Export the parameters", exportName(".json", ctx.LabelA, "vs", ctx.LabelB, "parameters"), func() ([]byte, error) {
		return json.MarshalIndent(out, "", "  ")
	})
}

// exportTopMapsZIP writes the web's "Top 10 local maps per direction" ZIP.
func (p *comparePage) exportTopMapsZIP() {
	e, _, ctx, ok := p.exportTarget()
	if !ok {
		return
	}
	probability := p.topQuantile
	if math.IsNaN(probability) || probability < 0 || probability > 1 {
		p.a.Toast("Contrast quantile must be a number from 0 to 1 (Lau -q; default 0.95).")
		return
	}
	file := p.comparisonFile(e, cmpExportFiles["top_maps"])
	if file == "" {
		return
	}
	test := map[string]any{"minCount": e.Parameters.MinCount, "range": e.Parameters.Range, "alpha": 0.01,
		"padjCutoff": e.Parameters.PadjCutoff, "binSize": 10}
	saveExport(p.a, "Export the top 10 local maps", exportName(".zip", ctx.LabelA, "vs", ctx.LabelB, "top10-local-maps"), func() ([]byte, error) {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var maps cmpTopMaps
		if err := json.Unmarshal(raw, &maps); err != nil {
			return nil, err
		}
		if len(maps.Regions) == 0 {
			return nil, errors.New("no significant merged interactions to export")
		}
		files := cmpMapFiles(&maps, probability, test, e.CreatedAt)
		return cmpZip(files, "hyb2-top10-local-maps")
	})
}

// ---- ports of web/batch-tools.js

// cmpHistogramQuantile is histogramQuantile: the type-7 quantile of the
// values a histogram [[value, count], ...] lists.
func cmpHistogramQuantile(hist [][2]float64, probability float64) float64 {
	entries := append([][2]float64(nil), hist...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i][0] < entries[j][0] })
	n := 0.0
	for _, e := range entries {
		n += e[1]
	}
	if n == 0 {
		return 1
	}
	position := (n - 1) * probability
	at := func(index float64) float64 {
		count := 0.0
		for _, e := range entries {
			count += e[1]
			if index < count {
				return e[0]
			}
		}
		return entries[len(entries)-1][0]
	}
	lo, hi := at(math.Floor(position)), at(math.Ceil(position))
	return lo + (hi-lo)*(position-math.Floor(position))
}

// cmpEsc is batch-tools.js esc (html.EscapeString writes &#34; for ").
func cmpEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}

// cmpTSVCell is batch-tools.js tsv: each tab or line break becomes a space.
func cmpTSVCell(s string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)
}

// cmpGrey is mapSvg's grey of a count: white at 0, black at the cap
// (CIE lightness, then sRGB).
func cmpGrey(count, quantile, limit float64) int {
	lightness := 100 * (1 - math.Min(count, quantile)/limit)
	luminance := lightness / 903.3
	if lightness > 8 {
		luminance = math.Pow((lightness+16)/116, 3)
	}
	srgb := 12.92 * luminance
	if luminance > .0031308 {
		srgb = 1.055*math.Pow(luminance, 1/2.4) - .055
	}
	return int(math.Floor(255*srgb + 0.5)) // Math.round
}

// cmpMapSVG is mapSvg: one window, each replicate side by side.
func cmpMapSVG(maps *cmpTopMaps, k int, limit int, quantile float64) string {
	r := maps.Regions[k]
	samples := maps.Samples
	width, height := max(1, len(samples))*300, 390
	side := "B"
	if r.Direction == "up" {
		side = "A"
	}
	title := "Top " + strconv.Itoa(r.Rank) + " · Higher in " + maps.Labels[side]
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img"><title>%s</title><rect width="100%%" height="100%%" fill="white"/><g font-family="sans-serif" fill="#222"><text x="18" y="24" font-size="16">%s</text><text x="18" y="45" font-size="12">padj %s · 10 nt bins · raw records · common white–black cap %d</text>`,
		width, height, width, height, cmpEsc(title), cmpEsc(title), cmpEsc(jsPrecision(r.Padj, 4)), limit)
	dx, dy := float64(r.X2-r.X1), float64(r.Y2-r.Y1)
	for index, sample := range samples {
		left, top, size := float64(index*300+48), 104.0, 220.0
		sx := func(x float64) float64 { return left + (x-float64(r.X1))/dx*size }
		sy := func(y float64) float64 { return top + size - (y-float64(r.Y1))/dy*size }
		fmt.Fprintf(&b, `<text x="%d" y="70" font-size="12">%s</text><text x="%d" y="88" font-size="10">%s</text><defs><clipPath id="panel%d"><rect x="%s" y="%s" width="%s" height="%s"/></clipPath></defs><g clip-path="url(#panel%d)">`,
			index*300+12, cmpEsc(sample.Label), index*300+12, cmpEsc(sample.RNAX+" × "+sample.RNAY), index,
			jsNumber(left), jsNumber(top), jsNumber(size), jsNumber(size), index)
		var cells [][3]int64
		if k < len(maps.Panels) && index < len(maps.Panels[k]) {
			cells = maps.Panels[k][index]
		}
		for _, c := range cells {
			grey := cmpGrey(float64(c[2]), quantile, float64(limit))
			fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="rgb(%d,%d,%d)"><title>%d, %d: %d</title></rect>`,
				jsNumber(sx(float64(c[0]-5))), jsNumber(sy(float64(c[1]+5))), jsNumber(size*10/dx), jsNumber(size*10/dy),
				grey, grey, grey, c[0], c[1], c[2])
		}
		fmt.Fprintf(&b, `</g><rect x="%s" y="%s" width="%s" height="%s" fill="none" stroke="#222"/><g font-size="10"><text x="%s" y="345">%d</text><text x="%s" y="345" text-anchor="end">%d</text><text x="%s" y="%s" text-anchor="end">%d</text><text x="%s" y="%s" text-anchor="end">%d</text><text x="%s" y="365">%s</text></g>`,
			jsNumber(left), jsNumber(top), jsNumber(size), jsNumber(size), jsNumber(left), r.X1, jsNumber(left+size), r.X2,
			jsNumber(left-4), jsNumber(top+size), r.Y1, jsNumber(left-4), jsNumber(top+10), r.Y2, jsNumber(left),
			cmpEsc("RNA X (nt) · condition "+sample.Condition))
		if len(cells) == 0 {
			fmt.Fprintf(&b, `<text x="%s" y="215" text-anchor="middle" font-size="12">No contacts in this window</text>`, jsNumber(left+size/2))
		}
	}
	return b.String() + "</g></svg>"
}

// cmpMapFiles is mapFiles: one SVG and one raw-count TSV per window, the
// windows' table, a printable preview.html and parameters.json.
func cmpMapFiles(maps *cmpTopMaps, probability float64, test map[string]any, completedAt string) []cmpZipFile {
	quantile := cmpHistogramQuantile(maps.Histogram, probability)
	limit := int(math.Max(1, math.Ceil(quantile)))
	var files []cmpZipFile
	rows := []string{"direction\trank\tx1\tx2\ty1\ty2\tpadj\tlog2FoldChange\tfile"}
	var html strings.Builder
	for k, r := range maps.Regions {
		name := fmt.Sprintf("%s-%02d-%d-%d", r.Direction, r.Rank, r.X, r.Y)
		svg := cmpMapSVG(maps, k, limit, quantile)
		files = append(files, cmpZipFile{name + ".svg", svg})
		cells := []string{"dataset_id\tlabel\tcondition\trna_x\trna_y\tx\ty\trecords"}
		for j, s := range maps.Samples {
			if k >= len(maps.Panels) || j >= len(maps.Panels[k]) {
				continue
			}
			for _, c := range maps.Panels[k][j] {
				fields := []string{s.ID, s.Label, s.Condition, s.RNAX, s.RNAY, strconv.FormatInt(c[0], 10), strconv.FormatInt(c[1], 10), strconv.FormatInt(c[2], 10)}
				for i := range fields {
					fields[i] = cmpTSVCell(fields[i])
				}
				cells = append(cells, strings.Join(fields, "\t"))
			}
		}
		files = append(files, cmpZipFile{name + ".tsv", strings.Join(cells, "\n") + "\n"})
		lfc := "" // [].join prints null as ""
		if r.LFC != nil {
			lfc = jsNumber(*r.LFC)
		}
		rows = append(rows, strings.Join([]string{r.Direction, strconv.Itoa(r.Rank), strconv.FormatInt(r.X1, 10), strconv.FormatInt(r.X2, 10),
			strconv.FormatInt(r.Y1, 10), strconv.FormatInt(r.Y2, 10), jsNumber(r.Padj), lfc, name + ".svg"}, "\t"))
		html.WriteString("<section>" + svg + "</section>")
	}
	files = append(files, cmpZipFile{"interactions.tsv", strings.Join(rows, "\n") + "\n"})
	files = append(files, cmpZipFile{"preview.html", `<!doctype html><meta charset="utf-8"><title>Top 10 local contact maps</title><style>body{font:16px sans-serif;margin:24px}section{margin:24px 0;overflow:auto;break-after:page}svg{max-width:100%;height:auto}@media print{section{overflow:visible}body{margin:0}}</style><h1>Top 10 local contact maps per direction</h1><p>Ranked by adjusted p-value; ±150 nt windows (lower bound 1), each replicate separately, shared ` +
		jsNumber(100*probability) + `% count cap. Counts use each dataset’s own RNA coordinates; no sequence alignment. Print this page to save PDF.</p>` + html.String()})
	type dataset struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		FileName  string `json:"fileName"`
		Condition string `json:"condition"`
		RNAX      string `json:"rnaX"`
		RNAY      string `json:"rnaY"`
		Records   *int64 `json:"records"`
	}
	datasets := make([]dataset, len(maps.Samples))
	for i, s := range maps.Samples {
		datasets[i] = dataset{s.ID, s.Label, s.FileName, s.Condition, s.RNAX, s.RNAY, s.Records}
	}
	params := struct {
		Method           string            `json:"method"`
		TopPerDirection  int               `json:"topPerDirection"`
		Flank            int               `json:"flank"`
		BinSize          int               `json:"binSize"`
		Quantile         float64           `json:"quantile"`
		Cap              int               `json:"cap"`
		UncappedQuantile float64           `json:"uncappedQuantile"`
		Labels           map[string]string `json:"labels"`
		Datasets         []dataset         `json:"datasets"`
		Test             map[string]any    `json:"test"`
		CompletedAt      string            `json:"completedAt"`
	}{"Lau-style replicate contact maps", 10, 150, 10, probability, limit, quantile, maps.Labels, datasets, test, completedAt}
	text, _ := json.MarshalIndent(params, "", "  ")
	files = append(files, cmpZipFile{"parameters.json", string(text)})
	return files
}

// cmpZip packs files under folder/, as the web's Hyb2Results.zip.
func cmpZip(files []cmpZipFile, folder string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		out, err := w.Create(folder + "/" + f.Path)
		if err != nil {
			return nil, err
		}
		if _, err := out.Write([]byte(f.Text)); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jsPrecision is JavaScript's Number.prototype.toPrecision(digits): the
// exact value rounded half up to digits significant digits, in exponent
// form below 1e-6 or from 10^digits.
func jsPrecision(v float64, digits int) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	}
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	var keep string
	e := 0
	if v == 0 {
		keep = strings.Repeat("0", digits)
	} else {
		// the exact decimal digits of v (a float64 has at most 767 of them)
		mant, exp, _ := strings.Cut(strconv.FormatFloat(v, 'e', 800, 64), "e")
		e, _ = strconv.Atoi(exp)
		all := strings.Replace(mant, ".", "", 1)
		keep = all[:digits]
		if all[digits] >= '5' {
			b := []byte(keep)
			i := len(b) - 1
			for ; i >= 0 && b[i] == '9'; i-- {
				b[i] = '0'
			}
			if i < 0 {
				b = append([]byte{'1'}, b[:len(b)-1]...)
				e++
			} else {
				b[i]++
			}
			keep = string(b)
		}
	}
	if e < -6 || e >= digits {
		out := keep[:1]
		if digits > 1 {
			out += "." + keep[1:]
		}
		if e >= 0 {
			return sign + out + "e+" + strconv.Itoa(e)
		}
		return sign + out + "e" + strconv.Itoa(e)
	}
	if e >= 0 {
		if e+1 == digits {
			return sign + keep
		}
		return sign + keep[:e+1] + "." + keep[e+1:]
	}
	return sign + "0." + strings.Repeat("0", -e-1) + keep
}

// ---- view

// exportsCard: the files for R or HYB2, the parameters and the local maps.
func (p *comparePage) exportsCard(c *ui.Context, e *comparisonEntry) {
	t := c.Theme()
	Card(c, "", func() {
		ui.Text(c, "Exports").FontSize(16).Bold()
		ui.Text(c, "Files for R or the HYB2 command-line tool: the raw DESeq2 count table (every 10-nt cell, one column per sample), HYB2's headerless names.table and the extended sample metadata (header and file names, for audit or your own R workflow), as the web app writes them.").
			FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.Button(c, "Count table…").Tooltip("HYB2-compatible raw count table (feature_id x_y, one column per sample)").Clicked() {
				p.exportCopy("counts", "Export the count table", "hyb2-desktop.table.txt")
			}
			if ui.Button(c, "names.table…").Tooltip("HYB2's two-column names table: sample, condition_one or condition_two").Clicked() {
				p.exportCopy("names", "Export names.table", "hyb2-desktop_names.table")
			}
			if ui.Button(c, "Sample metadata…").Tooltip("sample_id, condition, condition_label, file_name").Clicked() {
				p.exportCopy("metadata", "Export the sample metadata", "deseq2-sample-metadata.tsv")
			}
			if ui.Button(c, "Parameters JSON…").Tooltip("The test's parameters and result, and this page's settings").Clicked() {
				p.exportParamsJSON()
			}
		})
		ui.Row(c).Gap(12).AlignItems(ui.End).Wrap().Children(func() {
			Field(c, "Local map contrast quantile (Lau -q)", func() {
				ui.NumberInput(c.Key("cmp-topq"), &p.topQuantile, 0, 1, 0.01).Width(110).Label("Local map contrast quantile")
			})
			if ui.Button(c, "Top 10 local maps per direction · ZIP…").Tooltip("SVG maps, raw-count TSVs and a printable preview.html").Clicked() {
				p.exportTopMapsZIP()
			}
		})
		ui.Text(c, "Each map shows every replicate separately, ±150 nt around one of the ten smallest-padj merged interactions of each direction, white to black up to one count cap shared by all replicates (the quantile of every replicate's cell counts; lower values increase contrast).").
			FontSize(12).TextColor(t.TextMuted)
	})
}
