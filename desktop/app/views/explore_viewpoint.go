package views

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
	"hyb2desktop/widgets"
)

// Viewpoint: interaction coverage along the selected gene and its partners
// (analysis_viewpoint, computed by the backend over all hybrids).

// resolutions are the web's Resolution options; each starts with its bin
// size in nt.
var resolutions = []string{"1 nt (legacy viewpoint)", "5 nt mean", "10 nt mean", "25 nt mean", "50 nt mean", "100 nt mean"}

// rangeModes are the web's Coordinate range options; the full mapped
// reference needs the gene's reference length.
var rangeModes = []string{"Selected coordinates", "Full mapped reference"}

// viewpointTableRows is the web data table's length: the strongest bins.
const viewpointTableRows = 150

type partnerTotal struct {
	Name    string `json:"name"`
	Records int64  `json:"records"`
	Support int64  `json:"support"`
	Reads   int64  `json:"reads"`
}

type viewpointState struct {
	partner, typ, measure, resolution string
	homodimer                         string // one of homodimerOptions
	rangeMode                         string // one of rangeModes
	start, end                        string // inputs
	from, to                          int    // applied (0 = default)
	inputErr                          string
	showTable                         bool

	load    loader
	data    *viewpointData
	profile *widgets.Profile
	hover   int
}

type viewpointData struct {
	gene           string
	start, end     int
	bin, requested int
	adjusted       bool
	measure        string
	records, reads int64
	arms           int64
	max            float64
	partners       []partnerTotal
}

func (s *viewpointState) init() {
	// 1 nt is the web's default ("legacy" per-nucleotide viewpoint)
	s.partner, s.typ, s.measure, s.resolution, s.homodimer = allPartners, allTypes, cmRecords, resolutions[0], homodimerOptions[0]
	s.rangeMode = rangeModes[0]
	s.profile = widgets.NewProfile()
	s.hover = -1
}

// selectionChanged drops the picked bin; a new gene starts again at its
// observed range with all partners, as the web's.
func (s *viewpointState) selectionChanged(geneChanged bool) {
	s.profile.Picked = -1
	if geneChanged {
		s.start, s.end, s.from, s.to, s.inputErr = "", "", 0, 0, ""
		s.partner, s.rangeMode = allPartners, rangeModes[0]
	}
}

// fullReference: the whole reference is plotted (Start and End ignored).
func (s *viewpointState) fullReference() bool { return s.rangeMode == rangeModes[1] }

func (p *explorePage) viewpointKey() string {
	s := &p.vp
	return strings.Join([]string{p.sel, s.partner, s.typ, s.homodimer, s.measure, s.resolution, s.rangeMode, fmt.Sprint(s.from, "-", s.to)}, "\x00")
}

func (p *explorePage) loadViewpoint() {
	s := &p.vp
	if s.fullReference() && p.geneLength(p.gene) <= 0 {
		s.rangeMode = rangeModes[0] // no reference length: the web offers only selected coordinates
	}
	path, ids, gene := p.a.Project.Path, p.ids, p.gene
	partner, typ, measure, resolution, from, to, full := s.partner, s.typ, s.measure, s.resolution, s.from, s.to, s.fullReference()
	homodimer := homodimerPayload(s.homodimer)
	p.ensure(&s.load, p.viewpointKey(), func() (func(), error) {
		var bin int
		fmt.Sscanf(resolution, "%d", &bin)
		payload := core.Obj{"gene": gene, "bin_size": max(bin, 1), "measure": measurePayload(measure)}
		if partner != allPartners {
			payload["partner"] = partner
		}
		if types := typesPayload(typ); types != nil {
			payload["types"] = types
		}
		if homodimer != "" {
			payload["homodimer"] = homodimer
		}
		if full {
			payload["full_reference"] = true
		} else {
			if from > 0 {
				payload["start"] = from
			}
			if to > 0 {
				payload["end"] = to
			}
		}
		got, err := p.query(path, "analysis_viewpoint", ids, payload)
		if err != nil {
			return nil, err
		}
		d := &viewpointData{gene: gene, start: int(num(got["start"])), end: int(num(got["end"])),
			bin: int(num(got["bin_size"])), requested: int(num(got["requested_bin_size"])), adjusted: got["adjusted"] == true,
			measure: measure, records: int64(num(got["records"])), reads: int64(num(got["reads"])), arms: int64(num(got["arms"])),
			max: num(got["max"])}
		_ = model.Decode(got["partners"], &d.partners)
		bins, _ := got["bins"].(map[string]any)
		starts, ends, values := ints(bins["start"]), ints(bins["end"]), floats(bins["value"])
		return func() {
			s.data = d
			s.profile.Set(starts, ends, values)
		}, nil
	})
}

// resolutionOptions are the Resolution labels, the chosen one saying the
// bin used when the backend widened it (the web's "10 nt mean · using 20 nt").
func resolutionOptions(chosen string, d *viewpointData) []string {
	out := slices.Clone(resolutions)
	if d == nil || !d.adjusted {
		return out
	}
	for i, label := range out {
		if label == chosen {
			out[i] = label + " · using " + FormatNumber(int64(d.bin)) + " nt"
		}
	}
	return out
}

func (p *explorePage) viewpointView(c *ui.Context) {
	s := &p.vp
	t := c.Theme()
	p.loadViewpoint()
	submit := func() {
		from, ok1 := parseCoord(s.start)
		to, ok2 := parseCoord(s.end)
		if !ok1 || !ok2 || (to > 0 && from > to) {
			s.inputErr = "Start and end must be positions, start not after end."
			return
		}
		s.inputErr, s.from, s.to = "", from, to
	}
	d := s.data
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(14).Children(func() {
			ui.Row(c).Gap(12).Wrap().AlignItems(ui.End).Children(func() {
				picker(c, "vp-partner", "Partner", &s.partner, withAll(allPartners, p.genes()), 190)
				modes := rangeModes[:1]
				if p.geneLength(p.gene) > 0 {
					modes = rangeModes
				}
				picker(c, "vp-range", "Coordinate range", &s.rangeMode, modes, 190)
				coordInput(c, "vp-start", "Start", &s.start, "Observed start").OnSubmit(submit)
				coordInput(c, "vp-end", "End", &s.end, "Observed end").OnSubmit(submit)
				options := resolutionOptions(s.resolution, d)
				shown := options[max(0, slices.Index(resolutions, s.resolution))]
				picker(c, "vp-resolution", "Resolution", &shown, options, 230).OnChange(func() {
					if i := slices.Index(options, shown); i >= 0 {
						s.resolution = resolutions[i]
					}
				})
				picker(c, "vp-measure", "Measure", &s.measure, cmMeasures, 150)
				p.patternPicker(c, "vp-type", &s.typ, 230, nil)
				picker(c, "vp-homodimer", "Homodimer subset", &s.homodimer, homodimerOptions, 200)
				if ui.PrimaryButton(c, "Apply").Clicked() {
					submit()
				}
				if ui.Button(c, "Reset selection").Tooltip("Clear the chosen interval").Clicked() {
					s.profile.Picked = -1
				}
			})
			hint := "At 1 nt resolution, each plotted value is the number of overlapping interaction arms, matching the legacy viewpoint interpretation."
			if s.fullReference() {
				hint = "The full mapped reference uses coordinates 1 through its FASTA length; Start and End are ignored."
			}
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
			if s.inputErr != "" {
				errorLine(c, s.inputErr)
			}
			loadingLine(c, &s.load, "the viewpoint")
			if d == nil {
				return
			}
			if d.adjusted {
				notice(c, fmt.Sprintf("Requested %s nt resolution; the plot uses %s nt to keep this plot responsive.",
					FormatNumber(int64(d.requested)), FormatNumber(int64(d.bin))))
			}
			Card(c, "", func() {
				ui.Row(c).Gap(24).Wrap().Children(func() {
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Text(c, d.gene+" interaction coverage").FontSize(16).Bold()
						ui.Text(c, fmt.Sprintf("%s matching records · %s mapped arms · %s nt", FormatNumber(d.records), FormatNumber(d.arms), span(d.start, d.end))).
							FontSize(12).TextColor(t.TextMuted)
					})
					ui.Text(c, measureKicker(d.measure)).FontSize(12).TextColor(t.TextMuted)
					exportMenu(c,
						exportItem{"Plot · SVG…", false, p.exportViewpointSVG},
						exportItem{"Plot · PNG…", false, p.exportViewpointPNG},
						exportItem{"Coverage bins · TSV…", false, p.exportViewpointTSV},
						exportItem{"Parameters · JSON…", false, p.exportViewpointParams})
				})
				_, height := p.a.ContentSize()
				ev := s.profile.Show(c, clampF(height*0.42, 180, 520), "Viewpoint coverage of "+d.gene, profileColor(t))
				s.hover = ev.Hover
				readout := "Hover the plot for a bin's coverage; click to choose it."
				if i := s.hover; i >= 0 && i < s.profile.Len() {
					start, end, v := s.profile.Bin(i)
					readout = fmt.Sprintf("%s · mean %s", span(start, end), webFormatValue(v))
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, readout).FontSize(12).TextColor(t.TextMuted).Grow(1)
					label := "Open data table"
					if s.showTable {
						label = "Hide data table"
					}
					if ui.Button(c, label).Clicked() {
						s.showTable = !s.showTable
					}
				})
				if s.showTable {
					viewpointTable(c, s.profile)
				}
			})
			ui.Row(c).Gap(16).AlignItems(ui.Start).Wrap().Children(func() {
				Card(c, "Selected interval", func() {
					i := s.profile.Picked
					if i < 0 || i >= s.profile.Len() {
						ui.Text(c, "Select a viewpoint bar").Bold()
						ui.Text(c, "Click a plotted interval to send its coordinates to Region Explorer.").FontSize(12).TextColor(t.TextMuted)
						ui.Row(c).Gap(24).Children(func() {
							stat(c, "Maximum coverage", webFormatValue(d.max))
							stat(c, "Resolution", FormatNumber(int64(d.bin))+" nt")
						})
						return
					}
					start, end, v := s.profile.Bin(i)
					ui.Text(c, shortGene(d.gene)+":"+span(start, end)).Bold()
					ui.Row(c).Gap(24).Children(func() {
						stat(c, "Mean coverage", webFormatValue(v))
						stat(c, "Mapped arms", FormatNumber(d.arms))
					})
					if ui.PrimaryButton(c, "Explore region").Clicked() {
						p.exploreRegion(start, end)
					}
				}).Width(280)
				partnerCard(c, "Partners in range", d.partners, d.measure, 8).Grow(1).Basis(320)
			})
			methodNote(c, "Each matching arm contributes across all of its inclusive RNA coordinates. One-nucleotide bins reproduce the original HYB2 viewpoint count; larger bins display mean coverage per nucleotide. A mapped FASTA can extend the graph across the complete reference, including zero-coverage positions.")
		})
	})
}

// measureKicker names a measure as the web's card kicker.
func measureKicker(measure string) string {
	switch measurePayload(measure) {
	case "support":
		return "Cluster support"
	case "reads":
		return "Reads"
	}
	return "Records"
}

// viewpointTable is the web's data table: the strongest bins, most first.
func viewpointTable(c *ui.Context, pr *widgets.Profile) {
	t := c.Theme()
	order := make([]int, pr.Len())
	for i := range order {
		order[i] = i
	}
	value := func(i int) float64 { _, _, v := pr.Bin(i); return v }
	sort.SliceStable(order, func(i, j int) bool { return value(order[i]) > value(order[j]) })
	if len(order) > viewpointTableRows {
		order = order[:viewpointTableRows]
	}
	columns := []string{"Start (nt)", "End (nt)", "Mean interaction coverage"}
	widths := []float32{120, 120, 200}
	ui.Column(c).Gap(0).Children(func() {
		ui.Row(c).Gap(0).Padding(6, 8).Children(func() {
			for i, name := range columns {
				ui.Text(c, name).FontSize(12).Bold().TextColor(t.TextMuted).Width(widths[i]).TextAlign(ui.End)
			}
		})
		for _, i := range order {
			start, end, v := pr.Bin(i)
			row := []string{FormatNumber(int64(start)), FormatNumber(int64(end)), webFormatValue(v)}
			ui.Row(c.Key(fmt.Sprint(i))).Gap(0).Padding(4, 8).Border(0, t.Border).Children(func() {
				for k, v := range row {
					ui.Text(c, v).FontSize(12).Width(widths[k]).TextAlign(ui.End)
				}
			})
		}
	})
}

// methodNote is the web's Method note under a view.
func methodNote(c *ui.Context, text string) {
	t := c.Theme()
	ui.Row(c).Gap(8).Padding(10, 12).Radius(8).Background(t.Surface).Children(func() {
		ui.Text(c, "Method").Bold().FontSize(12)
		ui.Text(c, text).FontSize(12).TextColor(t.TextMuted).Grow(1)
	})
}

// exploreRegion opens Region on the selected bin, gene:[start, end], with
// the viewpoint's partner and no partner range (the web's Explore region).
func (p *explorePage) exploreRegion(start, end int) {
	partner := p.vp.partner
	if partner == allPartners {
		partner = ""
	}
	p.rg.applyRange(start, end)
	p.rg.setPartner(partner, 0, 0)
	p.tab = tabRegion
}

// partnerCard draws the top partners with bars by measure, each labelled
// with its measured value as the web's bar lists (the rows come sorted by
// cluster support, as the web Region Explorer's).
func partnerCard(c *ui.Context, title string, partners []partnerTotal, measure string, top int) ui.Element {
	t := c.Theme()
	return Card(c, title, func() {
		ui.Text(c, measureKicker(measure)).FontSize(12).TextColor(t.TextMuted)
		if len(partners) == 0 {
			ui.Text(c, "No matching interactions.").TextColor(t.TextMuted)
			return
		}
		value := func(pt partnerTotal) int64 {
			switch measurePayload(measure) {
			case "reads":
				return pt.Reads
			case "support":
				return pt.Support
			}
			return pt.Records
		}
		most := value(partners[0])
		for _, pt := range partners {
			most = max(most, value(pt))
		}
		for i, pt := range partners {
			if i == top {
				ui.Text(c, fmt.Sprintf("and %d more", len(partners)-top)).FontSize(12).TextColor(t.TextMuted)
				break
			}
			ui.Row(c.Key(pt.Name)).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, shortGene(pt.Name)).Width(170).SingleLine().Tooltip(pt.Name)
				fraction := float32(1)
				if most > 0 {
					fraction = max(float32(value(pt))/float32(most), 0.03)
				}
				ui.Box(c).Grow(1).Height(8).Radius(4).Background(t.Border).Children(func() {
					ui.Box(c).WidthPercent(fraction * 100).FillHeight().Radius(4).Background(t.Accent)
				})
				ui.Text(c, FormatNumber(value(pt))).FontSize(12).TextColor(t.TextMuted).Width(90).TextAlign(ui.End)
			})
		}
	})
}

func trimFloat(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}
