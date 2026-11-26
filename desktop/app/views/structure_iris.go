package views

// ⑤ Structure, the IRIS engine of the web RNA Structure page ("IRIS ·
// evidence-guided ensemble", web/iris-core.js; desktop/runtime/iris.py):
// its settings, the check of the region before a run (the web's recovery
// block, from the analysis_iris_region query), the weighted ensemble table
// (a member shows in every view and the result details), its download and
// its part of the report JSON.

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

const (
	engineIRIS = "iris"
	// The desktop limits (desktop/runtime/iris.py). The web's 300 nt and
	// 2,000 rows are browser limits; the cost here is the ViennaRNA folds,
	// at most stIrisMaxFoldsFor of them.
	stIrisMaxNT     = 1000
	stIrisMaxRows   = 50_000
	stIrisMaxFolds  = 1000 // the web's bound, the least the desktop allows
	stIrisFoldsCap  = 6000
	stIrisBudgetS   = 900.0
	stIrisMaxSets   = 20_000
	stIrisSourceURL = "https://github.com/JY-Zhou/IRIS"
	// the licence of the IRIS snapshot the adaptation follows (third_party/iris)
	stIrisLicenceURL = "https://github.com/JY-Zhou/IRIS/blob/4057c5ac0e038144e414c27f01cfd7b5a5be926f/LICENSE"
)

// stIrisState is the form's IRIS settings and the check of its region.
type stIrisState struct {
	init      bool
	k, c      float64
	fraction  float64
	stems     string
	diagKey   string
	diag      *stIrisDiag
	diagErr   string
	loading   bool
	stepsOpen bool
}

func (s *stIrisState) defaults() {
	d := engineSettings(engineIRIS).Defaults
	s.init, s.k, s.c, s.fraction, s.stems = true, d.K, d.C, d.Fraction, d.KRange
}

func (s *stIrisState) stemLabel() string {
	return nameOr(s.stems, engineSettings(engineIRIS).Defaults.KRange)
}

func (s *stIrisState) changed() bool {
	d := engineSettings(engineIRIS).Defaults
	stems, msg := stIrisParseStems(s.stems)
	defaultStems, _ := stIrisParseStems(d.KRange)
	return s.k != d.K || s.c != d.C || s.fraction != d.Fraction || msg != "" || !slices.Equal(stems, defaultStems)
}

// stIrisDiag is the runtime's diagnosis of a region (iris.diagnose): the web's
// diagnoseIrisRegion counts, a nearby supported window and the message.
type stIrisDiag struct {
	Counts struct {
		Total     int `json:"total"`
		SameRNA   int `json:"sameRna"`
		Forward   int `json:"forward"`
		Contained int `json:"contained"`
		TooWide   int `json:"tooWide"`
	} `json:"counts"`
	Suggestion *struct {
		RNA   string `json:"rna"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	} `json:"suggestion"`
	Message string `json:"message"`
}

// stIris is an IRIS window's ensemble (meta.json records[].iris).
type stIris struct {
	EngineVersion string         `json:"engineVersion"`
	Model         string         `json:"model"`
	Parameters    map[string]any `json:"parameters"`
	Ensemble      struct {
		MeanEnergy      float64 `json:"meanEnergy"`
		MeanSupport     float64 `json:"meanSupport"`
		Lambda          float64 `json:"lambda"`
		TestedEnsembles int     `json:"testedEnsembles"`
		CandidateCount  int     `json:"candidateCount"`
		GeneratedCount  int     `json:"generatedCount"`
	} `json:"ensemble"`
	Evidence struct {
		InputRecords int `json:"inputRecords"`
		StemCount    int `json:"stemCount"`
	} `json:"evidence"`
	Warnings []string       `json:"warnings"`
	Members  []stIrisMember `json:"members"`
}

// stIrisMember is one member of the ensemble, best weight first; its
// constraints are 1-based positions in the region.
type stIrisMember struct {
	Member      int      `json:"member"`
	Weight      float64  `json:"weight"`
	Coefficient float64  `json:"coefficient"`
	Energy      float64  `json:"energy"`
	DotBracket  string   `json:"dotBracket"`
	Constraints [][2]int `json:"constraints"`
}

// stIrisOf is the IRIS ensemble of a result's record i, nil for other engines.
func stIrisOf(a *structureResult, i int) *stIris {
	if a == nil || a.Meta.Engine != "IRIS" || i < 0 || i >= len(a.Meta.Records) {
		return nil
	}
	return a.Meta.Records[i].Iris
}

// stIrisHardPairs are the constraint pairs of the members shown (RNA
// positions); nil when the result is not IRIS.
func stIrisHardPairs(a *structureResult) map[[2]int]bool {
	if a == nil || a.Meta.Engine != "IRIS" {
		return nil
	}
	out := map[[2]int]bool{}
	for i, r := range a.Records {
		ir := stIrisOf(a, i)
		if ir == nil || r.SelectedCandidate < 0 || r.SelectedCandidate >= len(ir.Members) {
			continue
		}
		for _, pr := range ir.Members[r.SelectedCandidate].Constraints {
			out[stPairKey(r.positionOf(pr[0]), r.positionOf(pr[1]))] = true
		}
	}
	return out
}

// stIrisParseStems reads space- or comma-separated stem lengths, using the catalog default for an empty field.
func stIrisParseStems(text string) ([]int, string) {
	settings := engineSettings(engineIRIS)
	text = nameOr(text, settings.Defaults.KRange)
	r := settings.Ranges["stemLength"]
	invalid := fmt.Sprintf("IRIS stem length must be an integer from %s.", r)
	if !settings.KRangeGrammar.List.MatchString(text) {
		return nil, invalid
	}
	var out []int
	for _, token := range settings.KRangeGrammar.Number.FindAllString(text, -1) {
		n, err := strconv.ParseFloat(token, 64)
		if err != nil || n != math.Trunc(n) || n < r[0] || n > r[1] {
			return nil, invalid
		}
		out = append(out, int(n))
	}
	slices.Sort(out)
	return slices.Compact(out), ""
}

// irisPlan: one reference region with its chimeras, the web's IRIS checks
// and limits (desktop limits), and the region's evidence check.
func (p *structurePage) irisPlan(pl *foldPlan) {
	s := &p.iris
	if !s.init {
		s.defaults()
	}
	k, c := int(s.k), int(s.c)
	ranges := engineSettings(engineIRIS).Ranges
	kr, cr, q := ranges["K"], ranges["C"], ranges["fraction"]
	cr[0] = max(cr[0], s.k)
	if s.k != math.Trunc(s.k) || s.k < kr[0] || s.k > kr[1] {
		pl.problems = append(pl.problems, fmt.Sprintf("IRIS ensemble size K must be an integer from %s.", kr))
	} else {
		cr.check(pl, "IRIS candidate count C", s.c, true)
	}
	if !(q[0] <= s.fraction && s.fraction < q[1]) {
		pl.problems = append(pl.problems, fmt.Sprintf("IRIS support quantile must be at least %g and below %g.", q[0], q[1]))
	}
	stems, msg := stIrisParseStems(s.stems)
	if msg != "" {
		pl.problems = append(pl.problems, msg)
	}
	pl.req.Parameters = map[string]any{"K": k, "C": c, "fraction": s.fraction, "kRange": stems}
	pl.limits = []string{
		fmt.Sprintf("IRIS desktop adaptation · up to %s nt and %s evidence rows · 37 °C · nested structures.",
			FormatNumber(stIrisMaxNT), FormatNumber(stIrisMaxRows)),
		"Only chimeras with both arms inside the region are evidence; each adds one Gaussian support block.",
		fmt.Sprintf("At most %s constrained folds and %s candidate ensembles per run.", FormatNumber(int64(stIrisMaxFoldsFor(pl.lengths))), FormatNumber(stIrisMaxSets)),
	}
	region := pl.windowsOrWhole()
	switch {
	case len(pl.windows) > 1:
		pl.problems = append(pl.problems, "IRIS uses one reference region: enter one window.")
		return
	case pl.length == 0 || len(pl.lengths) == 0:
		return
	case pl.lengths[0] > stIrisMaxNT:
		pl.problems = append(pl.problems, fmt.Sprintf("IRIS desktop limit is %s nt; select a smaller region.", FormatNumber(stIrisMaxNT)))
	}
	p.irisCheck(pl.gene, pl.ids, region[0][0], region[0][1])
	if d := s.diag; d != nil && s.diagKey == p.irisKey(pl.gene, pl.ids, region[0][0], region[0][1]) && d.Counts.Contained == 0 {
		pl.problems = append(pl.problems, "IRIS requires HYB evidence inside the selected reference region.")
	}
	viennaMemory(pl)
	// the support matrix and stem sums: a few n x n float64 arrays
	r := float64(pl.lengths[0]) / 1000
	pl.memMB += uint64(100 + 64*r*r)
}

func (p *structurePage) irisKey(gene string, ids []string, start, end int) string {
	return fmt.Sprint(p.a.Project.Path, "\x00", gene, "\x00", strings.Join(ids, "\x00"), "\x00", start, "-", end)
}

// irisCheck reads the region's evidence counts from the samples' indexes
// (analysis_iris_region) once per region and samples.
func (p *structurePage) irisCheck(gene string, ids []string, start, end int) {
	a := p.a
	s := &p.iris
	key := p.irisKey(gene, ids, start, end)
	if gene == "" || len(ids) == 0 || key == s.diagKey {
		return
	}
	s.diagKey, s.diag, s.diagErr, s.loading = key, nil, "", true
	path := a.Project.Path
	a.Go(func() func() {
		raw, err := a.B.Query(path, "analysis_iris_region", core.Obj{"sample_ids": ids, "gene": gene, "start": start, "end": end})
		var d stIrisDiag
		if err == nil {
			err = model.Decode(raw, &d)
		}
		return func() {
			if s.diagKey != key {
				return
			}
			s.loading = false
			if err != nil {
				s.diagErr = err.Error()
				return
			}
			s.diag = &d
		}
	})
}

// irisRecovery is the web's recovery block when IRIS cannot run on the
// region: the evidence counts and a nearby supported region to use.
func (p *structurePage) irisRecovery(c *ui.Context, pl foldPlan) {
	t := c.Theme()
	s := &p.iris
	if p.engine != engineIRIS || p.src.kind != stSourceReference {
		return
	}
	if s.loading {
		ui.Spinner(c).Label("Checking the chimeras in this region…")
		return
	}
	if s.diagErr != "" {
		ui.Text(c, "Could not check the chimeras in this region: "+s.diagErr+". The run reads them itself.").FontSize(FontSmall).TextColor(t.TextMuted)
		return
	}
	d := s.diag
	if d == nil || d.Counts.Contained > 0 {
		return
	}
	ui.Column(c.Key("st-iris-recovery")).Gap(Space2).Padding(Space2, Space3).Radius(RadiusSmall).Background(t.Warning.Alpha(0.10)).Children(func() {
		if d.Message != "" {
			ui.Text(c, d.Message).FontSize(FontSmall)
		}
		ui.Text(c, fmt.Sprintf("HYB evidence: %s loaded; %s with both arms on this RNA; %s with forward coordinates; %s fully inside this region.",
			FormatNumber(int64(d.Counts.Total)), FormatNumber(int64(d.Counts.SameRNA)), FormatNumber(int64(d.Counts.Forward)),
			FormatNumber(int64(d.Counts.Contained)))).FontSize(FontSmall).TextColor(t.TextMuted)
		if g := d.Suggestion; g != nil {
			label := fmt.Sprintf("Use nearby HYB-supported region %s:%d-%d", g.RNA, g.Start, g.End)
			if ui.Button(c, label).Tooltip(label).Disabled(p.running()).Clicked() {
				p.useIrisRegion()
			}
			ui.Text(c, "Changes the interval only. Review it, then run IRIS. Having HYB evidence does not guarantee enough "+
				"candidate structures for an ensemble.").FontSize(FontSmall).TextColor(t.TextMuted)
		} else {
			ui.Text(c, fmt.Sprintf("Choose another RNA or other samples with a complete arm pair within %s nt. "+
				"Two separated regions are not supported by IRIS.", FormatNumber(stIrisMaxNT))).FontSize(FontSmall).TextColor(t.TextMuted)
		}
	})
}

// useIrisRegion puts the suggested window in the form (the web's "Use
// nearby HYB-supported region").
func (p *structurePage) useIrisRegion() {
	d := p.iris.diag
	if d == nil || d.Suggestion == nil {
		p.a.Toast("No complete HYB arm pair fits the %s nt IRIS limit for this RNA.", FormatNumber(stIrisMaxNT))
		return
	}
	g := d.Suggestion
	p.gene, p.mode, p.windows = g.RNA, "windows", fmt.Sprintf("%d-%d", g.Start, g.End)
	p.a.Toast("Region updated. Review the interval, then run IRIS.")
}

// irisCheckLines are the check's Engine and Time lines.
func (p *structurePage) irisCheckLines(line func(label, value string), pl foldPlan) {
	s := &p.iris
	line("Engine", fmt.Sprintf("IRIS ensemble: K %d of up to C %d candidates, support quantile %g, stem lengths %s; ViennaRNA, 37 °C, minimum loop 3",
		int(s.k), int(s.c), s.fraction, s.stemLabel()))
	folds := stIrisMaxFoldsFor(pl.lengths)
	line("Time", "up to about "+FormatDuration(stIrisFoldSeconds(pl.lengths)*float64(folds))+
		" (one ViennaRNA fold per stem and stem pair, at most "+FormatNumber(int64(folds))+"; usually far fewer)")
}

// stIrisFoldSeconds is one constrained ViennaRNA fold of the region
// (iris.py fold_seconds: 0.04 s at 300 nt, 0.82 s at 1,000).
func stIrisFoldSeconds(lengths []int) float64 {
	n := 0
	for _, l := range lengths {
		n += l
	}
	return 0.82 * math.Pow(float64(max(n, 1))/1000, 2.5)
}

// stIrisMaxFoldsFor is iris.py max_folds: about stIrisBudgetS of folding,
// from the web's 1,000 up to stIrisFoldsCap.
func stIrisMaxFoldsFor(lengths []int) int {
	return int(min(stIrisFoldsCap, max(stIrisMaxFolds, math.Floor(stIrisBudgetS/stIrisFoldSeconds(lengths)))))
}

// irisAdvanced: the web's IRIS options, its note and the source and licence.
func (p *structurePage) irisAdvanced(c *ui.Context) {
	t := c.Theme()
	s := &p.iris
	if !s.init {
		s.defaults()
	}
	ui.Column(c).Gap(Space1).Padding(Space2, Space3).Radius(RadiusSmall).Background(t.Surface).Children(func() {
		ui.Text(c, "IRIS redistributes HYB evidence, generates constrained structures, then selects a weighted ensemble. "+
			"Uses one reference region; nothing leaves this computer.").FontSize(FontSmall)
		ui.Row(c).Gap(Space3).Children(func() {
			if ui.Link(c, "IRIS source", "").Tooltip(stIrisSourceURL).Clicked() {
				c.OpenURL(stIrisSourceURL)
			}
			if ui.Link(c, "Licence", "").Tooltip("GNU GPL version 3 · " + stIrisLicenceURL).Clicked() {
				c.OpenURL(stIrisLicenceURL)
			}
		})
	})
	changed := s.changed()
	ui.Collapsible(c.Key("st-iris"), "IRIS settings", &p.advancedOpen, func() {
		ui.Column(c).Gap(Space3).Padding(6, 0, 0, 0).Children(func() {
			settings := engineSettings(engineIRIS)
			r, d := settings.Ranges, settings.Defaults
			kr, cr, q := r["K"], r["C"], r["fraction"]
			foldNumberRow(c, "st-iris-k", "Ensemble size K", &s.k, kr, 1, fmt.Sprintf("Representative structures in the ensemble, %s. Default %g.", kr, d.K))
			foldNumberRow(c, "st-iris-c", "Candidate count C", &s.c, cr, 1, fmt.Sprintf("Clustered candidates the ensemble is chosen from, K to %g. Default %g.", cr[1], d.C))
			q[1] -= 0.01 // the control uses hundredths; the plan accepts any value below the exclusive maximum
			foldNumberRow(c, "st-iris-q", "Support quantile", &s.fraction, q, 0.05,
				fmt.Sprintf("A stem is kept when its mean support exceeds this quantile of the positive support. Default %g.", d.Fraction))
			Field(c, "Stem lengths", func() {
				ui.TextInput(c.Key("st-iris-stems"), &s.stems).Placeholder(d.KRange).Label("Stem lengths").Width(200)
				ui.Text(c, fmt.Sprintf("Stem lengths to enumerate, %s, separated by spaces or commas. Default %s.", r["stemLength"], d.KRange)).FontSize(FontSmall).TextColor(t.TextMuted)
			})
			ui.Text(c, fmt.Sprintf("K selects %g–%g representative structures. C retains up to %g candidates; higher values increase work. ", kr[0], kr[1], cr[1])+
				"All compatible single- and double-stem constraints are considered within the desktop limits.").FontSize(FontSmall).TextColor(t.TextMuted)
			if ui.Button(c, "Reset to defaults").Disabled(!changed).Clicked() {
				s.defaults()
			}
		})
	})
	summary := fmt.Sprintf("K %d · C %d · support quantile %g · stem lengths %s · 37 °C",
		int(s.k), int(s.c), s.fraction, s.stemLabel())
	color := t.TextMuted
	if changed {
		summary += " · changed from the defaults"
		color = t.Warning
	} else {
		summary += " · defaults"
	}
	ui.Text(c, summary).FontSize(FontSmall).TextColor(color)
}

// stIrisExplain explains a failed IRIS run: the runtime's message is the
// web's, so it says what happened; ok false for other errors.
func stIrisExplain(err string) (what, next string, ok bool) {
	low := strings.ToLower(err)
	if !strings.Contains(low, "iris") && !strings.HasPrefix(low, "no hyb") && !strings.HasPrefix(low, "all hyb rows") {
		return "", "", false
	}
	what = err
	switch {
	case strings.Contains(low, "complete hyb arm pair") || strings.Contains(low, "no hyb") || strings.Contains(low, "requires hyb evidence"):
		next = "use the nearby HYB-supported region the form suggests, or choose other samples or another RNA."
	case strings.Contains(low, "constrained folds") || strings.Contains(low, "no supported canonical stems"):
		next = "select a smaller region, or change the support quantile or stem lengths under IRIS settings."
	case strings.Contains(low, "only") && strings.Contains(low, "candidate"):
		next = "reduce the ensemble size K under IRIS settings."
	case strings.Contains(low, "ensembles"):
		next = "reduce C or K under IRIS settings."
	default:
		next = "change the region or the IRIS settings and run it again."
	}
	if i := strings.Index(what, "Traceback"); i > 0 {
		what = strings.TrimSpace(what[:i])
	}
	return what, next, true
}

// ---- the result

// stIrisRows are the web's iris-ensemble.tsv rows.
func stIrisRows(ir *stIris, selected int) [][]string {
	rows := [][]string{{"member", "selected", "weight", "coefficient", "energy_kcal_per_mol", "dot_bracket"}}
	for i, m := range ir.Members {
		yes := "no"
		if i == selected {
			yes = "yes"
		}
		rows = append(rows, []string{strconv.Itoa(i + 1), yes, jsNumber(m.Weight), jsNumber(m.Coefficient), jsNumber(m.Energy), m.DotBracket})
	}
	return rows
}

// irisPanel is the web's "IRIS weighted ensemble": a member shown in every
// view (diagram, statistics, details and exports), the ensemble's numbers,
// its download and how it was computed.
func (p *structurePage) irisPanel(c *ui.Context, a *structureResult) {
	t := c.Theme()
	index := p.shownRecord(a)
	ir := stIrisOf(a, index)
	if ir == nil || index < 0 || len(ir.Members) == 0 {
		return
	}
	r := &a.Records[index]
	ui.Column(c).Gap(Space2).Children(func() {
		ui.Row(c).Gap(Space2).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, "IRIS weighted ensemble").Bold().Grow(1)
			if ui.Button(c, "Weighted ensemble TSV…").Tooltip("Download weighted ensemble: each member's weight, coefficient, energy and dot-bracket").Clicked() {
				rows := stIrisRows(ir, r.SelectedCandidate)
				saveExport(p.a, "Save the IRIS weighted ensemble", stExportFiles["iris"],
					func() ([]byte, error) { return webTSV(rows), nil })
			}
		})
		ui.Text(c, "Select a member to update every structure view. Weights describe the fitted mixture, not confidence.").FontSize(FontSmall).TextColor(t.TextMuted)
		cols := []ui.TableColumn{{Title: "Member", Width: 84, Align: ui.End}, {Title: "Weight", Width: 80, Align: ui.End},
			{Title: "Energy (kcal/mol)", Width: 160, Align: ui.End}, {Title: "View"}}
		p.candRow = r.SelectedCandidate
		p.candTable.Selected = &p.candRow
		table := ui.Table(c.Key(fmt.Sprintf("st-iris-%s-%d", p.viewA, index)), &p.candTable, cols, len(ir.Members), func(i, col int) {
			m := ir.Members[i]
			switch col {
			case 0:
				ui.Text(c, strconv.Itoa(i+1))
			case 1:
				ui.Text(c, jsFixed(m.Weight*100, 1)+"%")
			case 2:
				ui.Text(c, jsFixed(m.Energy, 2))
			case 3:
				state := "View member " + strconv.Itoa(i+1)
				if i == r.SelectedCandidate {
					state = "Selected"
				}
				ui.Text(c, state).SingleLine()
			}
		}).Height(float32(len(ir.Members))*28 + 42).Label("IRIS weighted ensemble members")
		if table.Changed() && p.candRow != r.SelectedCandidate {
			if r.selectCandidate(p.candRow) {
				p.arcsKey = "" // redraw with the member
				p.arcs.Selected = 0
				p.a.Toast("IRIS ensemble member %d selected.", p.candRow+1)
			} else {
				p.a.Toast("That IRIS member is unavailable.")
			}
		}
		e := ir.Ensemble
		ui.Text(c, fmt.Sprintf("Ensemble mean energy: %s kcal/mol · %s candidates · %s tested", jsFixed(e.MeanEnergy, 2),
			FormatNumber(int64(e.CandidateCount)), stCountLabel(e.TestedEnsembles, "ensemble"))).FontSize(FontSmall).TextColor(t.TextMuted)
		ui.Collapsible(c.Key("st-iris-steps"), "How this was computed", &p.iris.stepsOpen, func() {
			var tiles [][3]string
			stage := func(title, value, note string) { tiles = append(tiles, [3]string{title, value, note}) }
			stage("1 · HYB support", FormatNumber(int64(ir.Evidence.InputRecords))+" rows", "Gaussian arm blocks, log1p support")
			stage("2 · Supported stems", FormatNumber(int64(ir.Evidence.StemCount))+" stems",
				fmt.Sprintf("lengths %v above the %v quantile", ir.Parameters["kRange"], ir.Parameters["fraction"]))
			stage("3 · Constrained folds", FormatNumber(int64(e.GeneratedCount))+" structures",
				"clustered to "+FormatNumber(int64(e.CandidateCount))+" candidates")
			stage("4 · Ensemble", stCountLabel(e.TestedEnsembles, "ensemble")+" tested",
				fmt.Sprintf("K %v · lambda %s", ir.Parameters["K"], jsFixed(e.Lambda, 3)))
			p.statTiles(c, tiles)
			ui.Text(c, nameOr(ir.Model, "IRIS")+". "+strings.Join(ir.Warnings, " ")).FontSize(FontSmall).TextColor(t.TextMuted)
		})
	})
}

// stIrisMetaLine is the viewer's line under the diagram for IRIS.
func stIrisMetaLine(a *structureResult) string {
	m := a.Meta.Parameters
	return fmt.Sprintf("weighted ensemble, K %v, C %v, support quantile %v, stem lengths %v, %v °C, minimum loop %v",
		m["K"], m["C"], m["fraction"], m["kRange"], m["temperature"], m["min_loop"])
}

// irisReport is the report JSON's "iris": parameters, ensemble, members,
// the member shown and the warnings, as the web's report; nil for other engines.
func (p *structurePage) irisReport(a *structureResult) any {
	index := p.shownRecord(a)
	if index < 0 && len(a.Records) > 0 {
		index = 0
	}
	ir := stIrisOf(a, index)
	if ir == nil {
		return nil
	}
	raw, err := json.Marshal(ir)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	out["selectedMember"] = a.Records[index].SelectedCandidate
	return out
}
