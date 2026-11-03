package views

// The palette's "On this page" entries: each page's main actions, as the
// web's palette lists the page's Export, Download, Explore, Copy, Run, Fold
// and Try example buttons (interaction.js ACTION_PATTERN). An entry runs
// the method the page's button runs, and is listed only when the button is
// shown and enabled.

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"hyb2desktop/model"
)

func (a *App) pageCommands() []command {
	var out []command
	add := func(title string, run func()) {
		out = append(out, command{group: "On this page", title: title, run: run})
	}
	// exports lists an Export menu's enabled items as "Export <item>"
	exports := func(items ...exportItem) {
		for _, it := range items {
			if !it.disabled {
				add("Export "+lowerFirst(it.label), it.run)
			}
		}
	}
	switch p := a.pages[a.Page].(type) {
	case *dataPage:
		// the samples Run all pending runs (dataPage.samplesCard)
		var pending []model.Sample
		for _, s := range a.Project.Samples {
			if _, active := p.activeTask(s); !active && !dataSampleDone(s) {
				pending = append(pending, s)
			}
		}
		if len(pending) > 0 {
			add(fmt.Sprintf("Run all pending (%d)", len(pending)), func() { p.runSamples(pending) })
		}
		add("Import data folder…", p.chooseFolder)
		add("Import sample sheet…", p.importSheet)
		add("Import raw files…", p.importRaw)
		add("Add reference…", p.addReference)
		// shown while the project has no samples and no reference
		if len(a.Project.Samples) == 0 && len(a.Project.References) == 0 && !p.exampleBusy {
			add("Try example", p.addExample)
		}
	case *qcPage:
		if o := &p.ov; o.key == p.overviewKey() && o.data != nil {
			add("Export Summary JSON…", p.exportOverviewJSON)
			add("Export RNA counts CSV…", func() { p.exportOverviewCSV(false) })
			add("Export RNA pairs CSV…", func() { p.exportOverviewCSV(true) })
		}
		if len(p.stats) > 0 {
			// the Per-dataset summary card's Export TSV…
			add("Export per-dataset summary TSV…", func() { p.exportTable(qcReadyIDs(a)) })
		}
	case *explorePage:
		switch p.tab {
		case tabInteractions:
			s := &p.ix
			none := s.key == "" || s.total == 0
			exports(exportItem{"Filtered records · CSV…", none, p.exportFilteredCSV},
				exportItem{"Filtered records · HYB…", none, p.exportFilteredHYB},
				exportItem{"Loaded rows · TSV…", len(s.rows) == 0, p.exportInteractions},
				exportItem{"Parameters · JSON…", false, p.exportInteractionsParams})
		case tabContact:
			// contactExportMenu's items
			none := p.cm.data == nil || len(p.cm.data.count) == 0
			exports(exportItem{"Map · SVG (vector)…", none, p.exportContactSVG},
				exportItem{"Map · PDF…", none, p.exportContactPDF},
				exportItem{"Map · PNG…", none, func() { p.exportContactPNG(false) }},
				exportItem{"Map · PNG, print resolution…", none, func() { p.exportContactPNG(true) }},
				exportItem{"Contact cells · TSV…", none, p.exportContactTSV},
				exportItem{"Matrix · CSV (bins × bins)…", none, p.exportContactMatrix},
				exportItem{"Parameters · JSON…", p.cm.data == nil, p.exportContactParams})
		case tabViewpoint:
			if p.vp.data != nil {
				exports(exportItem{"Plot · SVG…", false, p.exportViewpointSVG},
					exportItem{"Plot · PNG…", false, p.exportViewpointPNG},
					exportItem{"Coverage bins · TSV…", false, p.exportViewpointTSV},
					exportItem{"Parameters · JSON…", false, p.exportViewpointParams})
			}
		case tabRegion:
			s := &p.rg
			// the form's Explore (regionView's submit)
			add("Explore", func() {
				f, err := s.fromInputs()
				if err != nil {
					s.inputErr = err.Error()
					return
				}
				s.inputErr, s.applied = "", f
			})
			if d := s.data; d != nil {
				add("Fold this region", func() { p.foldRegion(d) })
				exports(exportItem{"Records · CSV…", d.records == 0, func() { p.exportRegionRecords(d, false) }},
					exportItem{"Records · HYB…", d.records == 0, func() { p.exportRegionRecords(d, true) }},
					exportItem{"Profile · SVG…", false, func() { p.exportRegionProfileSVG(d) }},
					exportItem{"Profile · TSV…", false, func() { p.exportRegionProfileTSV(d) }},
					exportItem{"Strongest interactions · TSV…", len(d.rows) == 0, func() { p.exportRegionInteractionsTSV(d) }},
					exportItem{"Sequence · FASTA…", stReferenceOf(a.Project, d.gene) == "", func() { p.exportRegionFASTA(d) }},
					exportItem{"Parameters · JSON…", false, func() { p.exportRegionParams(d) }})
				// the reference panel's buttons (regionReference)
				if seq := s.seq; seq != "" {
					add("Copy sequence", func() {
						a.copyText(seq)
						a.Toast("Sequence copied")
					})
					add("Download FASTA", func() { p.exportRegionFASTA(d) })
				}
			}
		}
	case *comparePage:
		if len(a.GroupOptions()) >= 2 {
			add("Run differential test", p.runTest)
			if !p.quick.busy {
				add("Quick view", p.runQuick)
			}
		}
		// the exports of what is shown: the selected test, or the quick view
		if p.mode == cmpModeQuick {
			if q := &p.quick; !q.busy && q.err == "" && q.data != nil && len(p.quickRows()) > 0 {
				add("Export quick view TSV…", p.exportQuickTSV)
			}
		} else if _, _, _, ok := p.exportTarget(); ok {
			add("Download map SVG…", p.exportMapSVG)
			add("Download map PNG…", p.exportMapPNG)
			add("Export interactions TSV…", p.exportInteractionsTSV)
			add("Export significant cells TSV…", p.exportSignificantTSV)
			add("Export parameters JSON…", p.exportParamsJSON)
		}
	case *structurePage:
		if len(p.plan().problems) == 0 && !p.running() {
			label := "Run fold"
			if p.engine == engineIRIS {
				label = "Predict IRIS ensemble"
			}
			add(label, p.runFold)
		}
		// the viewer's buttons for the structure shown
		if r := p.cache[p.viewA]; p.viewA != "" && r != nil {
			add("Save .dbn", func() { p.saveDBN(r) })
			add("Export SVG…", func() { p.exportStructureView(r, false) })
			add("Export PNG…", func() { p.exportStructureView(r, true) })
			add("Save .ct", func() { p.exportCT(r) })
			add("Save .bpseq", func() { p.exportBPSEQ(r) })
			add("Export pairs TSV…", func() { p.exportPairsTSV(r) })
			add("Export report JSON…", func() { p.exportReport(r) })
		}
	case *reportPage:
		if p.data != nil {
			add("Export report…", p.export)
			add("Save PDF…", p.exportPDF)
			add("Export for AI (ZIP)…", func() { p.exportPackage("ai") })
			add("Export for analysis (ZIP)…", func() { p.exportPackage("analysis") })
		}
	}
	return out
}

// exportStructureView is the viewer's Export SVG… and Export PNG…: the
// view shown, or its arcs.
func (p *structurePage) exportStructureView(r *structureResult, asPNG bool) {
	var b *structureResult
	if p.viewB != "" {
		b = p.cache[p.viewB]
	}
	_, other := stViewModes[p.view]
	switch {
	case other && b == nil:
		p.exportViewSVG(r, asPNG)
	case asPNG:
		p.exportArcsPNG(r)
	default:
		p.exportArcsSVG(r)
	}
}

// lowerFirst lower-cases a label's first letter ("Map · SVG…" → "map · SVG…").
func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || strings.ToUpper(s[:n]) != s[:n] {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}

// copyText puts text on the clipboard in the next frame (a palette entry
// has no context to write it with).
func (a *App) copyText(text string) {
	a.clipboard = &text
}
