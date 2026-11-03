package views

// ⑤ Structure, the setup side of the web RNA Structure page: the "Folding
// workflow" presets (web/analysis-ui.js applyLegacyWorkflow), "Preview
// sequence" with its copy and FASTA, "Download local inputs" for
// bin/cplfold and bin/cplcofold (web/structure-ui.js
// downloadLocalCplfoldInput), and the warning when the evidence chosen in
// the form is not the evidence of the result shown.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// stSetup is the state of the setup parts in this file.
type stSetup struct {
	// workflow is one of stWorkflows; snap is the form as the preset left
	// it (or as last seen), to tell when the user changes what the preset
	// set.
	workflow string
	snap     stWorkflowSnap
	// the "Preview sequence" fold and the reference sequences it read
	previewOpen bool
	seqs        map[string]string
	seqErr      map[string]string
	reading     map[string]bool
	// localFASTA is the name the form's local FASTA was last saved under,
	// for the local command shown ("" until saved).
	localFASTA string
}

// ---- Folding workflow

// The web's workflow presets.
const (
	stWorkflowCustom    = "custom"
	stWorkflowShort     = "short-intramolecular"
	stWorkflowLong      = "long-intramolecular"
	stWorkflowInter     = "intermolecular"
	stWorkflowHomodimer = "homodimer"
)

var stWorkflows = []string{stWorkflowCustom, stWorkflowShort, stWorkflowLong, stWorkflowInter, stWorkflowHomodimer}

// stWorkflowLabels are the web's labels: CPLfold's name the engine each
// preset runs, ViennaRNA's the interaction.
func stWorkflowLabels(cpl bool) []string {
	if cpl {
		return []string{"Custom engine and reference layout", "One local region · CPLfold",
			"Two local regions of the same RNA · CPLcofold", "Two local regions of different RNAs · CPLcofold", "Homodimer (overlap ≥ 5)"}
	}
	return []string{"Custom engine and reference layout", "Short-ranged intramolecular (<500 nt)",
		"Long-ranged intramolecular", "Intermolecular", "Homodimer (overlap ≥ 5)"}
}

// stWorkflowSnap is what a preset sets.
type stWorkflowSnap struct {
	engine, source, gene, gene2, region1, region2 string
	paired, homodimer                             bool
}

func (p *structurePage) workflowSnap() stWorkflowSnap {
	return stWorkflowSnap{engine: p.engine, source: p.src.kind, gene: p.gene, gene2: p.gene2, region1: p.region1,
		region2: p.region2, paired: p.mode == "paired", homodimer: p.homodimerOnly}
}

// stCPL: CPLfold or CPLcofold, the web's "CPLfold" engine.
func stCPL(engine string) bool { return engine == engineCPLfold || engine == engineCPLcofold }

func (p *structurePage) workflow() string { return nameOr(p.setup.workflow, stWorkflowCustom) }

// applyWorkflow sets the form as the web's preset does, on the desktop's
// engines: with CPLfold, one region runs CPLfold and two run CPLcofold
// (DP09 unless DP03 or DP09 is chosen); otherwise COMRADES(ViennaRNA)
// folds one region, or two joined by the 100-nt spacer, with the homodimer
// proxy filter for the homodimer preset. A region from a CPLcofold or
// windows form carries over as the one region or region 1.
func (p *structurePage) applyWorkflow(value string) {
	s := &p.setup
	s.workflow = value
	defer func() { s.snap = p.workflowSnap() }()
	if value == stWorkflowCustom {
		return
	}
	if strings.TrimSpace(p.region1) == "" && p.mode == "windows" {
		if lines := strings.Fields(p.windows); len(lines) > 0 {
			p.region1 = lines[0]
		}
	}
	region, _ := stParseRegion(p.region1, p.a.Project.GeneLength(p.gene), "Region 1")
	one := value == stWorkflowShort
	p.setSource(stSourceReference)
	if stCPL(p.engine) {
		if one {
			p.engine = engineCPLfold
		} else {
			p.engine = engineCPLcofold
			if p.model != "DP03" && p.model != "DP09" {
				p.model = stDefaultModel
			}
		}
	} else {
		p.engine = engineComrades
		p.homodimerOnly = value == stWorkflowHomodimer
		if !one {
			p.mode = "paired"
		}
	}
	if one && region.End > 0 {
		p.mode, p.windows = "windows", fmt.Sprintf("%d-%d", region.Start, region.End)
	} else if one && p.mode == "paired" {
		p.mode = "genome"
	}
	switch value {
	case stWorkflowLong, stWorkflowHomodimer:
		p.gene2 = p.gene
	case stWorkflowInter:
		if p.gene2 == p.gene || p.gene2 == "" {
			p.gene2 = p.otherRNA()
		}
	}
	if value == stWorkflowHomodimer {
		p.region2 = p.region1
	}
	p.engineChanged()
}

// otherRNA is the first RNA other than the one folded, of its reference
// when it has one (both regions must be on one reference), else of any.
func (p *structurePage) otherRNA() string {
	ref, other := stReferenceID(p.a.Project, p.gene), ""
	for _, r := range p.a.Project.References {
		for _, s := range r.Sequences {
			switch {
			case s.Name == p.gene:
			case r.ID == ref:
				return s.Name
			case other == "":
				other = s.Name
			}
		}
	}
	return other
}

// syncWorkflow keeps a preset in step with the form, as the web: changing
// the engine, the source, the layout, the homodimer filter or region 2's
// RNA goes back to Custom; changing the RNA of a same-RNA preset moves
// region 2 with it, and with CPLcofold the homodimer preset copies region
// 1 into region 2 (editing region 2 goes back to Custom).
func (p *structurePage) syncWorkflow() {
	s := &p.setup
	defer func() { s.snap = p.workflowSnap() }()
	wf := p.workflow()
	if wf == stWorkflowCustom {
		return
	}
	now, was := p.workflowSnap(), s.snap
	custom := func() { s.workflow = stWorkflowCustom }
	switch {
	case now.engine != was.engine || now.source != was.source || now.paired != was.paired || now.homodimer != was.homodimer:
		custom()
		return
	case now.gene != was.gene && (wf == stWorkflowLong || wf == stWorkflowHomodimer):
		p.gene2 = p.gene
	case now.gene2 != was.gene2:
		custom()
		return
	}
	if p.engine == engineCPLcofold && wf == stWorkflowHomodimer {
		if now.region2 != was.region2 {
			custom()
		} else if now.region1 != was.region1 {
			p.region2 = p.region1
		}
	}
}

// workflowProblems are the web's preset checks of the two regions.
func (p *structurePage) workflowProblems(pl *foldPlan) {
	wf := p.workflow()
	if wf == stWorkflowCustom || wf == stWorkflowShort || p.src.kind != stSourceReference || p.gene == "" {
		return
	}
	same := nameOr(p.gene2, p.gene) == p.gene
	if !stCPL(p.engine) {
		if wf == stWorkflowInter && same {
			pl.problems = append(pl.problems, "Intermolecular folding requires two different RNAs. Choose a different Region 2 RNA.")
		}
		return
	}
	switch {
	case wf == stWorkflowInter && same:
		pl.problems = append(pl.problems, "Choose different RNAs for the intermolecular preset, or use the two-regions-of-the-same-RNA preset.")
	case wf != stWorkflowInter && !same:
		pl.problems = append(pl.problems, "This preset requires two regions of the same RNA; choose Custom for different RNAs.")
	case wf == stWorkflowHomodimer && strings.Join(strings.Fields(p.region1), "") != strings.Join(strings.Fields(p.region2), ""):
		pl.problems = append(pl.problems, "The homodimer preset requires two copies of the same region; choose Custom for different coordinates.")
	}
}

// workflowField is the web's "Folding workflow" select, with its note.
func (p *structurePage) workflowField(c *ui.Context) {
	if p.engine == engineIRIS {
		return // the web hides the workflow for IRIS: one reference region
	}
	t := c.Theme()
	cpl := stCPL(p.engine)
	labels := stWorkflowLabels(cpl)
	wf := p.workflow()
	current := labels[max(slices.Index(stWorkflows, wf), 0)]
	Field(c, "Folding workflow", func() {
		if ui.Select(c.Key("st-workflow"), &current, labels).FillWidth().Label("Folding workflow").Tooltip(current).Changed() {
			if i := slices.Index(labels, current); i >= 0 {
				p.applyWorkflow(stWorkflows[i])
			}
		}
		if wf == stWorkflowCustom {
			return
		}
		note := "ViennaRNA presets use HYB-guided evidence. Short-range uses one region; long-range uses two regions of the same RNA. " +
			"Intermolecular uses two different RNAs; homodimer uses two copies of one RNA and overlap score ≥ 5. " +
			"Paired layouts include the original 100 nt spacer."
		if cpl {
			note = "With CPLfold selected, one-region presets run CPLfold; two-region presets run CPLcofold and preserve the selected reads, " +
				"alpha, beta and pseudoknot settings. Same-RNA regions are folded as two excised fragments, with a strand break " +
				"and no intervening reference sequence or artificial spacer."
		}
		ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
		if cpl && wf == stWorkflowHomodimer {
			// the web filters CPLcofold's rows to homodimers; the desktop
			// CPLcofold reads no overlap score
			ui.Text(c, "Desktop CPLcofold has no homodimer filter: every eligible HYB row contributes, not only those with overlap score ≥ 5.").
				FontSize(12).Padding(8).Radius(6).Background(t.Warning.Alpha(0.10))
		}
	})
}

// ---- Preview sequence

// stSegment is one region of a prepared sequence: RNA positions from-to
// placed at prepared positions prepFrom-prepTo.
type stSegment struct {
	rna                        string
	from, to, prepFrom, prepTo int
}

// stPrepared is a sequence as it will be folded: a window, the whole RNA,
// two strands (cutpoint after strand A) or two regions joined by the
// spacer (linker).
type stPrepared struct {
	title    string // the window, when there are several
	seq      string
	cutpoint int
	segments []stSegment
	linker   bool
}

// stSpacer is the web's spacer between two COMRADES regions (desktop/runtime/comrades.py LINKER).
var stSpacer = strings.Repeat("A", stSpacerNT/2) + strings.Repeat("U", stSpacerNT/2)

// stRange is "a–b" with thousands separators.
func stRange(from, to int) string {
	return FormatNumber(int64(from)) + "–" + FormatNumber(int64(to))
}

// prepared are the sequences the plan folds and their label; with read
// false only the label (no reference sequence is read). loading while a
// reference sequence is read; err when it cannot be.
func (p *structurePage) prepared(pl foldPlan, read bool) (parts []stPrepared, label string, loading bool, err string) {
	if len(pl.lengths) == 0 {
		return nil, "", false, ""
	}
	if p.src.kind != stSourceReference {
		label = "Pasted sequence"
		if r := p.src.record; p.src.kind == stSourceRecord && r != nil {
			label = stRecordName(*r) + " · HYB record sequence"
		}
		if pl.req.Sequence == nil {
			return nil, label, false, ""
		}
		part := stPrepared{seq: *pl.req.Sequence}
		if pl.req.SequenceTwo != nil {
			part.seq, part.cutpoint = part.seq+*pl.req.SequenceTwo, len(part.seq)
		}
		return []stPrepared{part}, label, false, ""
	}
	regions := pl.req.Regions
	two := len(regions) == 2 && len(pl.lengths) == 2
	switch {
	case two:
		label = fmt.Sprintf("%s:%s + %s:%s", regions[0].GeneID, stRange(int(regions[0].Start), int(regions[0].End)),
			regions[1].GeneID, stRange(int(regions[1].Start), int(regions[1].End)))
	case len(pl.windows) == 1:
		label = fmt.Sprintf("%s:%s", p.gene, stRange(int(pl.windows[0].Start), int(pl.windows[0].End)))
	case len(pl.windows) > 1:
		label = fmt.Sprintf("%s · %d windows", p.gene, len(pl.windows))
	default:
		label = fmt.Sprintf("%s:%s", p.gene, stRange(1, pl.length))
	}
	if !read {
		return nil, label, false, ""
	}
	cut := func(gene string, from, to int) (string, bool) {
		seq, ok, e := p.referenceSequence(gene)
		switch {
		case e != "":
			err = e
		case !ok:
			loading = true
		case from < 1 || to > len(seq) || from > to:
			err = fmt.Sprintf("%s:%d-%d is outside its %s nt.", gene, from, to, FormatNumber(int64(len(seq))))
		default:
			return seq[from-1 : to], true
		}
		return "", false
	}
	switch {
	case two:
		r1, r2 := regions[0], regions[1]
		s1, ok1 := cut(r1.GeneID, int(r1.Start), int(r1.End))
		s2, ok2 := cut(r2.GeneID, int(r2.Start), int(r2.End))
		if !ok1 || !ok2 {
			return nil, label, loading, err
		}
		gap := 0
		part := stPrepared{seq: s1 + s2, cutpoint: len(s1)}
		if p.engine != engineCPLcofold { // COMRADES: joined by the spacer
			gap = len(stSpacer)
			part = stPrepared{seq: s1 + stSpacer + s2, linker: true}
		}
		part.segments = []stSegment{{r1.GeneID, int(r1.Start), int(r1.End), 1, len(s1)},
			{r2.GeneID, int(r2.Start), int(r2.End), len(s1) + gap + 1, len(s1) + gap + len(s2)}}
		return []stPrepared{part}, label, false, ""
	case len(pl.windows) > 0:
		for _, w := range pl.windows {
			seq, ok := cut(p.gene, int(w.Start), int(w.End))
			if !ok {
				return nil, label, loading, err
			}
			parts = append(parts, stPrepared{title: fmt.Sprintf("%s:%s", p.gene, stRange(int(w.Start), int(w.End))), seq: seq,
				segments: []stSegment{{p.gene, int(w.Start), int(w.End), 1, len(seq)}}})
		}
		return parts, label, false, ""
	default:
		seq, ok := cut(p.gene, 1, pl.length)
		if !ok {
			return nil, label, loading, err
		}
		return []stPrepared{{seq: seq, segments: []stSegment{{p.gene, 1, pl.length, 1, pl.length}}}}, label, false, ""
	}
}

// referenceSequence is an RNA's reference sequence as folded (upper case,
// T read as U), read once off the UI thread.
func (p *structurePage) referenceSequence(gene string) (string, bool, string) {
	s := &p.setup
	if seq, ok := s.seqs[gene]; ok {
		return seq, true, ""
	}
	if e := s.seqErr[gene]; e != "" {
		return "", false, e
	}
	if s.reading == nil {
		s.seqs, s.seqErr, s.reading = map[string]string{}, map[string]string{}, map[string]bool{}
	}
	if !s.reading[gene] {
		s.reading[gene] = true
		a, pr := p.a, p.a.Project
		path := pr.Path
		a.Go(func() func() {
			seq, err := fetchSequence(a.B, pr, path, gene)
			seq = strings.ReplaceAll(strings.ToUpper(seq), "T", "U")
			return func() {
				if a.Project == nil || a.Project.Path != path || s.reading == nil {
					return
				}
				delete(s.reading, gene)
				if err != nil {
					s.seqErr[gene] = err.Error()
					return
				}
				s.seqs[gene] = seq
			}
		})
	}
	return "", false, ""
}

// stSequenceLines is the web's aligned sequence: perLine nt a line (the
// web's 50 where the form is wide enough) in groups of 10, each line led by
// its prepared position, "&" after strand A.
func stSequenceLines(seq string, cutpoint, perLine int) []string {
	width := len(strconv.Itoa(max(len(seq), 1)))
	var lines []string
	for at := 0; at < len(seq); at += perLine {
		var b strings.Builder
		fmt.Fprintf(&b, "%*d  ", width, at+1)
		for k := at; k < min(at+perLine, len(seq)); k++ {
			if k > at && (k-at)%10 == 0 {
				b.WriteByte(' ')
			}
			b.WriteByte(seq[k])
			if cutpoint > 0 && k+1 == cutpoint && cutpoint < len(seq) {
				b.WriteByte('&')
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}

// stFASTA is the web's prepared FASTA: one record, or RNA A and RNA B of
// two strands; several windows are one record each.
func stFASTA(parts []stPrepared) string {
	var b strings.Builder
	for i, part := range parts {
		switch {
		case part.cutpoint > 0:
			fmt.Fprintf(&b, ">HYB2_Web_RNA_A\n%s\n>HYB2_Web_RNA_B\n%s\n", part.seq[:part.cutpoint], part.seq[part.cutpoint:])
		case len(parts) > 1:
			fmt.Fprintf(&b, ">HYB2_Web_prepared_sequence_%d %s\n%s\n", i+1, strings.ReplaceAll(part.title, "–", "-"), part.seq)
		default:
			fmt.Fprintf(&b, ">HYB2_Web_prepared_sequence\n%s\n", part.seq)
		}
	}
	return b.String()
}

// stShort cuts s to n characters (not bytes), as the web's truncate.
func stShort(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// previewPanel is the web's "Preview sequence": the prepared layout, the
// sequence, Copy sequence, Download FASTA and (CPLfold) Download local
// inputs with the local command line.
func (p *structurePage) previewPanel(c *ui.Context, pl foldPlan) {
	t := c.Theme()
	s := &p.setup
	_, label, _, _ := p.prepared(pl, false)
	title := "Preview sequence"
	if label != "" {
		title += " · " + stShort(label, 48)
	}
	ui.Collapsible(c.Key("st-preview"), title, &s.previewOpen, func() {
		ui.Column(c).Gap(8).Padding(6, 0, 0, 0).Children(func() {
			if len(pl.lengths) == 0 {
				ui.Text(c, "Waiting for a sequence").FontSize(12).Bold()
				ui.Text(c, "Reference regions require a mapped FASTA. A HYB sequence can be selected from a record, or you can paste A, C, G and U directly.").
					FontSize(12).TextColor(t.TextMuted)
				return
			}
			parts, _, loading, err := p.prepared(pl, true)
			switch {
			case err != "":
				ui.Text(c, "Could not read the reference sequence: "+err).FontSize(12).TextColor(t.Danger)
				return
			case loading || len(parts) == 0:
				ui.Spinner(c).Label("Loading the reference sequence…")
				return
			}
			for i, part := range parts {
				p.previewPart(c.Key(fmt.Sprint("st-prep-", i)), part, len(parts) > 1)
			}
			ui.Row(c).Gap(8).Wrap().Children(func() {
				if ui.Button(c, "Copy sequence").Clicked() {
					texts := make([]string, len(parts))
					for i, part := range parts {
						texts[i] = stWithBreak(part.seq, part.cutpoint)
					}
					c.WriteClipboard(strings.Join(texts, "\n"))
					p.a.Toast("Prepared RNA sequence copied to the clipboard.")
				}
				if ui.Button(c, "Download FASTA").Clicked() {
					text := stFASTA(parts)
					saveExport(p.a, "Save the structure input FASTA", "structure-input.fasta", func() ([]byte, error) {
						return []byte(text), nil
					})
				}
				if stCPL(p.engine) {
					one := len(parts) == 1
					tip := "Download prepared FASTA and HYB bonus matrix for local bin/cplfold"
					if !one {
						tip = "bin/cplfold folds one sequence: fold one window (or the whole RNA) to download its local inputs"
					}
					if ui.Button(c, "Download local inputs").Disabled(!one).Tooltip(tip).Clicked() {
						p.saveLocalInputs(stFASTA(parts), "", p.localRun(pl), true)
					}
				}
			})
			if stCPL(p.engine) && len(parts) == 1 {
				run := p.localRun(pl)
				ui.Text(c, "Local command").FontSize(12).Bold().TextColor(t.TextMuted)
				ui.Text(c, stLocalCommand(run, nameOr(p.setup.localFASTA, stLocalFASTA), stLocalMatrix)).Font("monospace").FontSize(11).Selectable()
				if run.guided {
					ui.Text(c, "The bonus matrix is built when the fold runs: after Run fold, “Local inputs…” under CPLfold candidates saves it with the FASTA.").
						FontSize(12).TextColor(t.TextMuted)
				}
			}
		})
	})
}

// previewPerLine is how many nucleotides a line of the preview holds: the
// web's 50 when the form is wide enough (View sizes it), else 40 or 30, in
// whole groups of 10.
func (p *structurePage) previewPerLine(length int) int {
	w, _ := p.a.ContentSize()
	form := clampF(w*0.3, 354, 460)
	if w < 860 {
		form = w - 48
	}
	// the card's and the block's padding; 11 px monospace is ~6.7 px a character
	chars := int((form - 80) / 6.7)
	digits := len(strconv.Itoa(max(length, 1)))
	for _, n := range []int{50, 40, 30, 20} {
		if digits+2+n+n/10 <= chars {
			return n
		}
	}
	return 10
}

// previewPart is one prepared sequence: its layout and aligned sequence.
func (p *structurePage) previewPart(c *ui.Context, part stPrepared, titled bool) {
	t := c.Theme()
	ui.Column(c).Gap(4).Children(func() {
		if titled {
			ui.Text(c, part.title).FontSize(12).Bold()
		}
		if len(part.segments) > 0 {
			ui.Text(c, "Prepared reference layout").FontSize(11).Bold().TextColor(t.TextMuted)
			// one segment a line in the narrow form, the spacer between
			for i, sg := range part.segments {
				if i > 0 && part.linker {
					ui.Text(c, "100 nt A/U spacer").FontSize(12).TextColor(t.TextMuted)
				}
				ui.Text(c, sg.rna+" "+stRange(sg.from, sg.to)+" → prepared "+stRange(sg.prepFrom, sg.prepTo)).FontSize(12)
			}
		}
		if n := part.cutpoint; n > 0 {
			ui.Text(c, fmt.Sprintf("RNA A: 1–%s · RNA B: %s–%s · chain break after A (coordinates exclude &).",
				FormatNumber(int64(n)), FormatNumber(int64(n+1)), FormatNumber(int64(len(part.seq))))).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Column(c).Padding(8, 10).Radius(6).Background(t.Surface).Children(func() {
			ui.Scroll(c.Key("seq")).MaxHeight(220).Children(func() {
				for i, line := range stSequenceLines(part.seq, part.cutpoint, p.previewPerLine(len(part.seq))) {
					ui.Text(c.Key(fmt.Sprint(i)), line).Font("monospace").FontSize(11).SingleLine()
				}
			})
		})
	})
}

// ---- Download local inputs

// The web's local input files.
const (
	stLocalFASTA  = "cplfold-input.fasta"
	stLocalMatrix = "cplfold-bonus-matrix.tsv"
)

// stLocalRun is what the local command line needs.
type stLocalRun struct {
	cofold, guided, pseudoknot bool
	beam, phase1               int
	delta, beta, alpha         float64
	model                      string
}

// localRun is the form's fold.
func (p *structurePage) localRun(pl foldPlan) stLocalRun {
	return stLocalRun{cofold: p.engine == engineCPLcofold, guided: !p.seqOnly, pseudoknot: pl.pk, beam: int(p.beam),
		phase1: int(p.phase1), delta: p.delta, beta: p.beta, alpha: p.alpha, model: p.model}
}

// stResultRun is a CPLfold result's fold, from its parameters.
func stResultRun(a *structureResult) stLocalRun {
	num := func(key string, fallback float64) float64 {
		if v, ok := a.Meta.Parameters[key].(float64); ok {
			return v
		}
		return fallback
	}
	pk, _ := a.Meta.Parameters["allow_pseudoknot"].(bool)
	model, _ := a.Meta.Parameters["energy_model"].(string)
	return stLocalRun{cofold: stEngine(a) == "CPLcofold", guided: stGuided(a), pseudoknot: pk,
		beam: int(num("beam", stDefaultBeam)), phase1: int(num("max_phase1", stDefaultPhase1)), delta: num("energy_delta", stDefaultDelta),
		beta: num("beta", stDefaultBeta), alpha: num("alpha", stDefaultAlpha), model: nameOr(model, stDefaultModel)}
}

// stShellWord quotes a file name for the shell when it needs it.
func stShellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\!*?&;|<>()[]{}#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// stLocalCommand is the web's local command line (localCplfoldCommand),
// with the FASTA and bonus matrix file names given.
func stLocalCommand(r stLocalRun, fasta, matrix string) string {
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	cmd := []string{"bin/cplfold", "--sequence-file", stShellWord(fasta)}
	if r.cofold {
		mode := "pseudoknot"
		if !r.pseudoknot {
			mode = "standard"
		}
		cmd[0] = "bin/cplcofold"
		cmd = append(cmd, "--mode", mode)
	}
	if r.guided {
		cmd = append(cmd, "--bonus-matrix-file", stShellWord(nameOr(matrix, stLocalMatrix)))
	}
	cmd = append(cmd, "--beam", strconv.Itoa(r.beam), "--delta", num(r.delta), "--max-phase1", strconv.Itoa(r.phase1),
		"--max-phase2", "1", "--model", strings.ToUpper(nameOr(r.model, stDefaultModel)), "--beta", num(r.beta))
	if r.guided {
		cmd = append(cmd, "--alpha", num(r.alpha))
	}
	if !r.pseudoknot {
		cmd = append(cmd, "--no-pseudoknot")
	}
	return strings.Join(cmd, " ")
}

// saveLocalInputs asks where to save the FASTA and writes the bonus matrix
// (a file the runtime wrote, "" when there is none) beside it as
// cplfold-bonus-matrix.tsv, or, when that file is already there, under a
// name of the FASTA's (stMatrixNames) rather than over it; the toast gives
// the names and the command. setup: saved from the form, before the fold
// has built a bonus matrix.
func (p *structurePage) saveLocalInputs(fasta, matrix string, run stLocalRun, setup bool) {
	a := p.a
	a.Go(func() func() {
		path := PickSave("Save the CPLfold local inputs", stLocalFASTA)
		if path == "" {
			return nil
		}
		// the save dialog asked before replacing the FASTA
		err := stWriteAtomic(path, []byte(fasta))
		guided := run.guided
		withMatrix := err == nil && guided && matrix != ""
		matrixName := ""
		if withMatrix {
			matrixName, err = stCopyNew(matrix, filepath.Dir(path), stMatrixNames(filepath.Base(path)))
		}
		// without its bonus matrix, bin/cplfold folds the sequence alone
		run.guided = withMatrix
		cmd := stLocalCommand(run, filepath.Base(path), matrixName)
		return func() {
			if err == nil && setup {
				p.setup.localFASTA = filepath.Base(path)
			}
			switch {
			case err != nil:
				a.Toast("Could not export: %v", err)
			case withMatrix:
				a.Toast("CPLfold local inputs saved in %s: %s and %s. Run: %s", filepath.Dir(path), filepath.Base(path), matrixName, cmd)
			case guided && setup:
				a.Toast("CPLfold FASTA saved as %s. The HYB bonus matrix is built when the fold runs: save it with “Local inputs…” "+
					"under the result's CPLfold candidates. Sequence only: %s", path, cmd)
			case guided:
				a.Toast("CPLfold FASTA saved as %s. This result has no bonus matrix file (more than 2,000,000 cells). Sequence only: %s", path, cmd)
			default:
				a.Toast("CPLfold FASTA saved as %s. Run: %s", path, cmd)
			}
		}
	})
}

// stMatrixNames are the names the bonus matrix may take beside the FASTA
// fasta, first free one wins: the web's cplfold-bonus-matrix.tsv, then
// "<fasta stem>-bonus-matrix.tsv", then numbered.
func stMatrixNames(fasta string) []string {
	stem := strings.TrimSuffix(fasta, filepath.Ext(fasta))
	names := []string{stLocalMatrix, stem + "-bonus-matrix.tsv"}
	for k := 2; k <= 99; k++ {
		names = append(names, fmt.Sprintf("%s-bonus-matrix-%d.tsv", stem, k))
	}
	return names
}

// stWriteAtomic writes path through a temporary file beside it, renamed
// over it once complete, so a failed write leaves the old file.
func stWriteAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// stCopyNew copies from into dir under the first of names that does not
// exist yet, never over an existing file, and returns that name. The copy
// is written to a temporary file first and then linked in whole, so a
// failed copy leaves nothing half written.
func stCopyNew(from, dir string, names []string) (string, error) {
	in, err := os.Open(from)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".hyb2-bonus-matrix.*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	_, err = io.Copy(tmp, in)
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	for _, candidate := range names {
		target := filepath.Join(dir, candidate)
		linkErr := os.Link(name, target) // fails when target exists
		if linkErr == nil {
			return candidate, nil
		}
		if os.IsExist(linkErr) {
			continue
		}
		// a file system without hard links: rename when the name is free
		if _, statErr := os.Lstat(target); os.IsNotExist(statErr) {
			if err := os.Rename(name, target); err != nil {
				return "", err
			}
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s and the other bonus matrix names are taken in %s", stLocalMatrix, dir)
}

// localInputsButton is the web's "Download local inputs" of a CPLfold
// result: the shown window's FASTA, its bonus matrix and the command.
func (p *structurePage) localInputsButton(c *ui.Context, a *structureResult) {
	if !stIsCPL(a) {
		return
	}
	index := p.shownRecord(a)
	tip := "Download prepared FASTA and HYB bonus matrix for local bin/cplfold"
	if index < 0 {
		tip = "Choose a window: each window has its own local inputs"
	}
	if ui.Button(c, "Local inputs…").Disabled(index < 0).Tooltip(tip).Clicked() {
		r := a.Records[index]
		part := stPrepared{seq: r.Sequence, cutpoint: r.Cutpoint}
		p.saveLocalInputs(stFASTA([]stPrepared{part}), r.BonusMatrixPath, stResultRun(a), false)
	}
}

// ---- Evidence provenance

// evidenceProvenance is the web's "HYB evidence from A + B." under a
// guided result, and its warning when the evidence the form would use now
// is another.
func (p *structurePage) evidenceProvenance(c *ui.Context, a *structureResult) {
	t := c.Theme()
	e := p.entry(p.viewA)
	if e == nil || !stUsesHYB(a) {
		return
	}
	used := slices.Clone(e.SampleIDs)
	if len(used) == 0 {
		used = slices.Clone(e.Samples)
	}
	if len(used) == 0 {
		return
	}
	ui.Text(c, "HYB evidence from "+p.evidenceNames(e)+".").FontSize(12).TextColor(t.TextMuted)
	// as the web's, against the evidence the form would fold with now:
	// none for a form set to fold without chimeras
	current := p.evidenceIDs()
	if p.engine == engineVienna || (p.seqOnly && stCPL(p.engine)) {
		current = nil
	}
	now := slices.Clone(current)
	slices.Sort(used)
	slices.Sort(now)
	if len(now) == 0 || slices.Equal(used, now) {
		return
	}
	ui.Text(c, "The HYB evidence has changed since this fold: it would now come from "+p.sampleNames(current)+
		". Run again to update the structure.").FontSize(12).Padding(8).Radius(6).Background(t.Warning.Alpha(0.12))
}

// stUsesHYB: the result was folded with the samples' chimeras (CPLfold
// or CPLcofold with HYB evidence, COMRADES, IRIS), from its own engine and
// evidence rather than the form's.
func stUsesHYB(a *structureResult) bool {
	return stGuided(a) || stEngine(a) == "IRIS"
}
