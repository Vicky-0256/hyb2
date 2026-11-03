package views

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// Interactions: analysis_hybrids with backend filters, order and paging.

const interactionsPage = 200

var (
	orderLabels = []string{"File order", "Reads (most first)", "Support (most first)", "Arm 1 position", "dG (lowest first)"}
	orderValues = []string{"file", "reads", "support", "position", "dg"}
	// scopeLabels are the web's interaction types; the homodimer proxy is
	// both arms on one RNA with an overlap score of 5 or more. The last, as
	// the contact map's homodimer subset, lists a cell's hybrids when the
	// map excludes the proxy.
	scopeLabels = []string{"All", "Intra-RNA", "Inter-RNA", "Homodimer proxy (overlap ≥ 5)", "Exclude homodimer proxy"}
	// countModes rank the partner panel, as the web's count mode.
	countModes = []string{"Cluster support", "Records"}
)

// ixFilters are the filters applied (the inputs hold drafts until Apply).
type ixFilters struct {
	// gene1, gene2 are the web's RNA and partner, either arm order, also
	// with arm positions (which apply to the record's arm 1 and arm 2 on
	// their own, as the web's); in a contact map cell's drill-down
	// (exactArms) they are arm 1 and arm 2. "" = any.
	gene1, gene2 string
	typ          string
	scope        string // one of scopeLabels; "" = all
	search       string
	minReads     int
	minSupport   int
	minDG, maxDG *float64
	// maxEvalue is the web's maximum e-value: no arm e-value above it.
	maxEvalue      *float64
	x0, x1, y0, y1 int // 0 = unset
	// exactArms (a contact map cell's Show hybrids): with arm positions,
	// gene1 is arm 1's RNA and gene2 arm 2's, so the list is the cell's.
	exactArms bool
	// bothOrders: with exactArms, also the reverse arm order (gene2 as
	// arm 1 in y0..y1, gene1 as arm 2 in x0..x1), as a contact map of
	// normalised pairs counts an inter-RNA cell.
	bothOrders bool
	// anchor is a region's own set of hybrids (Region's Show hybrids).
	anchor ixAnchor
}

// ixAnchor is the analysis_hybrids anchor filter: an arm on gene in
// start..end (either arm order; inside it when contained), the other on
// partner (in pStart..pEnd when set). gene "" = none.
type ixAnchor struct {
	gene, partner string
	start, end    int
	pStart, pEnd  int
	contained     bool
}

func (x ixAnchor) payload() core.Obj {
	out := core.Obj{"gene": x.gene, "start": x.start, "end": x.end, "overlap": "any"}
	if x.contained {
		out["overlap"] = "contained"
	}
	if x.partner != "" {
		out["partner"] = x.partner
	}
	if x.pStart > 0 {
		out["partner_start"], out["partner_end"] = x.pStart, x.pEnd
	}
	return out
}

func (x ixAnchor) describe() string {
	text := "region " + shortGene(x.gene) + ":" + span(x.start, x.end) + " (either arm"
	if x.contained {
		text += ", fully contained"
	}
	if x.partner != "" {
		text += ", partner " + shortGene(x.partner)
		if x.pStart > 0 {
			text += ":" + span(x.pStart, x.pEnd)
		}
	}
	return text + ")"
}

type interactionsState struct {
	applied ixFilters
	// inputs
	gene1, gene2, typ, order, scope string
	search, minReads, minSupport    string
	minDG, maxDG, x0, x1, y0, y1    string
	maxEvalue                       string
	inputErr                        string
	// moreOpen shows the ΔG, e-value and arm position filters (open while
	// one is set).
	moreOpen bool
	// countMode ranks the partners (one of countModes); it filters nothing.
	countMode string

	load, more loader
	key        string // the applied key the rows belong to
	rows       []hybridRow
	total      int
	// partners are the RNA's partners under the filters (the web's partner
	// panel), when an RNA is chosen and the filters are not oriented.
	partners []partnerTotal
	list     ui.ListState
	chosen   int
	// lineLoad reads the chosen record's line number (lines, by lineKey).
	lineLoad loader
	lines    map[string]*int
}

type hybridRow struct {
	Index   int      `json:"index"`
	ID      int64    `json:"id"`
	Gene1   string   `json:"gene1"`
	Start1  int      `json:"start1"`
	End1    int      `json:"end1"`
	Gene2   string   `json:"gene2"`
	Start2  int      `json:"start2"`
	End2    int      `json:"end2"`
	Reads   int      `json:"reads"`
	Support int      `json:"support"`
	DG      *float64 `json:"dg"`
	Overlap *float64 `json:"overlap"`
	Type    string   `json:"type"`
	// Name and Sequence are columns 1 and 2 of the .hyb line, EValue1 and
	// EValue2 columns 9 and 15, Raw the whole line ("" or nil when the file
	// has changed since the index was built).
	Name     string   `json:"name"`
	Sequence string   `json:"sequence"`
	EValue1  *float64 `json:"evalue1"`
	EValue2  *float64 `json:"evalue2"`
	Raw      string   `json:"raw"`
	// ReadStart1..ReadEnd2 are the arms' read coordinates (columns 5, 6,
	// 11 and 12); TypeSource is "column_17" or "inferred", as the web's.
	ReadStart1 *float64 `json:"read_start1"`
	ReadEnd1   *float64 `json:"read_end1"`
	ReadStart2 *float64 `json:"read_start2"`
	ReadEnd2   *float64 `json:"read_end2"`
	TypeSource string   `json:"type_source"`
}

func (s *interactionsState) init() {
	s.gene1, s.gene2, s.typ, s.order, s.scope = anyGene, anyGene, allTypes, orderLabels[0], scopeLabels[0]
	s.countMode = countModes[0]
	s.applied.minReads, s.applied.minSupport = 1, 1
	s.minReads, s.minSupport = "1", "1"
	s.chosen = -1
}

func (s *interactionsState) selectionChanged() {
	s.chosen = -1
}

// apply sets the filters (and the inputs showing them).
func (s *interactionsState) apply(f ixFilters) {
	s.applied = f
	s.gene1, s.gene2, s.typ = orAny(f.gene1), orAny(f.gene2), f.typ
	if s.typ == "" {
		s.typ = allTypes
	}
	s.scope = f.scope
	if s.scope == "" {
		s.scope = scopeLabels[0]
	}
	s.search = f.search
	s.minReads, s.minSupport = strconv.Itoa(max(f.minReads, 1)), strconv.Itoa(max(f.minSupport, 1))
	s.minDG, s.maxDG, s.maxEvalue = dgText(f.minDG), dgText(f.maxDG), dgText(f.maxEvalue)
	s.x0, s.x1, s.y0, s.y1 = coordText(f.x0), coordText(f.x1), coordText(f.y0), coordText(f.y1)
	s.inputErr = ""
}

// applyPickers applies the pickers at once, with the text drafts when they
// are valid; an invalid draft would otherwise block the change, so then
// only the pickers change.
func (s *interactionsState) applyPickers() {
	if f, err := s.fromInputs(); err == nil {
		s.applied, s.inputErr = f, ""
		return
	}
	f := s.applied
	f.gene1, f.gene2, f.typ, f.scope = "", "", s.typ, ""
	if s.gene1 != anyGene {
		f.gene1 = s.gene1
	}
	if s.gene2 != anyGene {
		f.gene2 = s.gene2
	}
	if s.scope != scopeLabels[0] {
		f.scope = s.scope
	}
	s.applied = f
}

func orAny(gene string) string {
	if gene == "" {
		return anyGene
	}
	return gene
}

func dgText(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// parseDG reads an optional ΔG bound ("" = none).
func parseDG(text string) (*float64, bool) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "−", "-"))
	if text == "" {
		return nil, true
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, false
	}
	return &v, true
}

// fromInputs reads the inputs into filters.
func (s *interactionsState) fromInputs() (ixFilters, error) {
	// the region set and the arm orders have no inputs: they stay until Clear
	f := ixFilters{typ: s.typ, search: strings.TrimSpace(s.search), anchor: s.applied.anchor,
		exactArms: s.applied.exactArms, bothOrders: s.applied.bothOrders}
	if s.scope != scopeLabels[0] {
		f.scope = s.scope
	}
	if s.gene1 != anyGene {
		f.gene1 = s.gene1
	}
	if s.gene2 != anyGene {
		f.gene2 = s.gene2
	}
	reads, ok := parseCoord(s.minReads)
	if !ok {
		return f, fmt.Errorf("minimum reads must be a whole number")
	}
	f.minReads = max(reads, 1)
	support, ok := parseCoord(s.minSupport)
	if !ok {
		return f, fmt.Errorf("minimum support must be a whole number")
	}
	f.minSupport = max(support, 1)
	if f.minDG, ok = parseDG(s.minDG); !ok {
		return f, fmt.Errorf("minimum dG must be a number")
	}
	if f.maxDG, ok = parseDG(s.maxDG); !ok {
		return f, fmt.Errorf("maximum dG must be a number")
	}
	if f.minDG != nil && f.maxDG != nil && *f.minDG > *f.maxDG {
		return f, fmt.Errorf("minimum dG must not exceed maximum dG")
	}
	if f.maxEvalue, ok = parseDG(s.maxEvalue); !ok || f.maxEvalue != nil && *f.maxEvalue < 0 {
		return f, fmt.Errorf("maximum e-value must be a number, 0 or more")
	}
	fields := []struct {
		text string
		into *int
		name string
	}{{s.x0, &f.x0, "arm 1 start"}, {s.x1, &f.x1, "arm 1 end"}, {s.y0, &f.y0, "arm 2 start"}, {s.y1, &f.y1, "arm 2 end"}}
	for _, field := range fields {
		n, ok := parseCoord(field.text)
		if !ok {
			return f, fmt.Errorf("%s must be a position", field.name)
		}
		*field.into = n
	}
	if f.x1 > 0 && f.x0 > f.x1 || f.y1 > 0 && f.y0 > f.y1 {
		return f, fmt.Errorf("a range's start must not exceed its end")
	}
	return f, nil
}

func (s *interactionsState) orderValue() string {
	for i, label := range orderLabels {
		if label == s.order {
			return orderValues[i]
		}
	}
	return "file"
}

// positioned: an arm position is set.
func (f ixFilters) positioned() bool { return f.x0 != 0 || f.x1 != 0 || f.y0 != 0 || f.y1 != 0 }

// oriented: a contact map cell's arm positions are set, so gene1 and gene2
// are the arms. Otherwise, as the web's, the RNA and partner match either
// arm whatever the positions.
func (f ixFilters) oriented() bool { return f.exactArms && f.positioned() }

// payload is the analysis_hybrids filters object.
func (f ixFilters) payload() core.Obj {
	out := core.Obj{}
	one, two := "gene", "partner"
	if f.oriented() {
		one, two = "gene1", "gene2"
	}
	if f.gene1 != "" {
		out[one] = f.gene1
	}
	if f.gene2 != "" {
		out[two] = f.gene2
	}
	if f.anchor.gene != "" {
		out["anchor"] = f.anchor.payload()
	}
	if types := typesPayload(f.typ); types != nil {
		out["types"] = types
	}
	if f.minReads > 1 {
		out["min_reads"] = f.minReads
	}
	if f.minSupport > 1 {
		out["min_support"] = f.minSupport
	}
	switch f.scope {
	case scopeLabels[1]:
		out["intra"] = true
	case scopeLabels[2]:
		out["intra"] = false
	case scopeLabels[3]:
		out["homodimer"] = "only"
	case scopeLabels[4]:
		out["homodimer"] = "exclude"
	}
	if f.search != "" {
		out["search"] = f.search
	}
	if f.minDG != nil {
		out["min_dg"] = *f.minDG
	}
	if f.maxDG != nil {
		out["max_dg"] = *f.maxDG
	}
	if f.maxEvalue != nil {
		out["max_evalue"] = *f.maxEvalue
	}
	region := core.Obj{}
	if f.x0 > 0 || f.x1 > 0 {
		region["x"] = []int{f.x0, orMax(f.x1)}
	}
	if f.y0 > 0 || f.y1 > 0 {
		region["y"] = []int{f.y0, orMax(f.y1)}
	}
	if len(region) > 0 {
		out["region"] = region
	}
	if f.bothOrders && f.oriented() {
		// this order or the reverse one: the arm genes and ranges swap
		this, reverse := core.Obj{}, core.Obj{}
		for _, key := range []string{"gene1", "gene2", "region"} {
			if v, ok := out[key]; ok {
				this[key] = v
				delete(out, key)
			}
		}
		if g, ok := this["gene1"]; ok {
			reverse["gene2"] = g
		}
		if g, ok := this["gene2"]; ok {
			reverse["gene1"] = g
		}
		if len(region) > 0 {
			swapped := core.Obj{}
			if x, ok := region["x"]; ok {
				swapped["y"] = x
			}
			if y, ok := region["y"]; ok {
				swapped["x"] = y
			}
			reverse["region"] = swapped
		}
		out["either"] = []core.Obj{this, reverse}
	}
	return out
}

func orMax(n int) int {
	if n <= 0 {
		return 1_000_000_000_000
	}
	return n
}

func (p *explorePage) interactionsKey() string {
	s := &p.ix
	f := s.applied
	f.minDG, f.maxDG, f.maxEvalue = nil, nil, nil // by value below, not by pointer
	return fmt.Sprintf("%s\x00%+v\x00%s\x00%s\x00%s\x00%s", p.sel, f, dgText(s.applied.minDG), dgText(s.applied.maxDG),
		dgText(s.applied.maxEvalue), s.orderValue())
}

// withPartners: the filters name an RNA, not arms, so the table has a
// partner panel (also with arm positions, as the web's).
func (f ixFilters) withPartners() bool { return f.gene1 != "" && !f.oriented() && f.anchor.gene == "" }

func (p *explorePage) fetchHybrids(path string, ids []string, f ixFilters, order string, offset int) ([]hybridRow, int, []partnerTotal, error) {
	payload := core.Obj{
		"filters": f.payload(), "limit": interactionsPage, "offset": offset, "order": order, "with_sequence": true, "with_raw": true,
	}
	if offset == 0 && f.withPartners() {
		payload["partners"] = true
	}
	got, err := p.query(path, "analysis_hybrids", ids, payload)
	if err != nil {
		return nil, 0, nil, err
	}
	var rows []hybridRow
	if err := model.Decode(got["rows"], &rows); err != nil {
		return nil, 0, nil, err
	}
	var partners []partnerTotal
	_ = model.Decode(got["partners"], &partners)
	return rows, int(num(got["total"])), partners, nil
}

func (p *explorePage) loadInteractions() {
	s := &p.ix
	key := p.interactionsKey()
	path, ids, f, order := p.a.Project.Path, p.ids, s.applied, s.orderValue()
	p.ensure(&s.load, key, func() (func(), error) {
		rows, total, partners, err := p.fetchHybrids(path, ids, f, order, 0)
		if err != nil {
			return nil, err
		}
		return func() {
			s.key, s.rows, s.total, s.partners, s.chosen = key, rows, total, partners, -1
			s.more.reset()
			s.list.ScrollTo(0, ui.Start)
		}, nil
	})
}

func (p *explorePage) loadMoreInteractions() {
	s := &p.ix
	key, offset := s.key, len(s.rows)
	path, ids, f, order := p.a.Project.Path, p.ids, s.applied, s.orderValue()
	p.ensure(&s.more, fmt.Sprintf("%s\x00%d", key, offset), func() (func(), error) {
		rows, total, _, err := p.fetchHybrids(path, ids, f, order, offset)
		if err != nil {
			return nil, err
		}
		return func() {
			if s.key != key || len(s.rows) != offset {
				return
			}
			s.rows, s.total = append(s.rows, rows...), total
		}, nil
	})
}

func (p *explorePage) interactionsView(c *ui.Context) {
	s := &p.ix
	t := c.Theme()
	p.loadInteractions()
	genes := withAll(anyGene, p.genes())
	submit := func() {
		f, err := s.fromInputs()
		if err != nil {
			s.inputErr = err.Error()
			return
		}
		s.inputErr = ""
		s.applied = f
	}
	ui.Column(c).Grow(1).Gap(12).Children(func() {
		ui.Row(c).Gap(12).Wrap().AlignItems(ui.End).Children(func() {
			one, two := "RNA", "Partner"
			if s.applied.oriented() {
				one, two = "Arm 1 RNA", "Arm 2 RNA"
			}
			picker(c, "ix-gene1", one, &s.gene1, genes, 150).OnChange(s.applyPickers)
			picker(c, "ix-gene2", two, &s.gene2, genes, 150).OnChange(s.applyPickers)
			picker(c, "ix-scope", "Interaction type", &s.scope, scopeLabels, 220).OnChange(s.applyPickers)
			p.patternPicker(c, "ix-type", &s.typ, 230, s.applyPickers)
			picker(c, "ix-order", "Sort by", &s.order, orderLabels, 150)
			picker(c, "ix-count-mode", "Count mode", &s.countMode, countModes, 140)
			Field(c, "Search", func() {
				ui.TextInput(c.Key("ix-search"), &s.search).Label("Search").Placeholder("Search ID or RNA").Width(170).
					Tooltip("Matches an RNA name, sequence ID or hybrid sequence").OnSubmit(submit)
			})
			Field(c, "Minimum reads", func() {
				ui.TextInput(c.Key("ix-min"), &s.minReads).Label("Minimum reads").Placeholder("1").Width(70).OnSubmit(submit)
			})
			Field(c, "Minimum support", func() {
				ui.TextInput(c.Key("ix-min-support"), &s.minSupport).Label("Minimum support").Placeholder("1").Width(70).OnSubmit(submit)
			})
			if ui.PrimaryButton(c, "Apply").Clicked() {
				submit()
			}
			if ui.Button(c, "Clear").Clicked() {
				s.apply(ixFilters{minReads: 1, minSupport: 1, typ: allTypes})
			}
		})
		set := 0
		for _, v := range []string{s.minDG, s.maxDG, s.maxEvalue, s.x0, s.x1, s.y0, s.y1} {
			if strings.TrimSpace(v) != "" {
				set++
			}
		}
		if set > 0 {
			s.moreOpen = true
		}
		label := "ΔG, e-value and arm positions"
		if set > 0 {
			label += fmt.Sprintf(" (%d set)", set)
		}
		ui.Collapsible(c.Key("ix-arms"), label, &s.moreOpen, func() {
			ui.Row(c).Gap(12).Wrap().AlignItems(ui.End).Padding(6, 0, 0, 0).Children(func() {
				coordInput(c, "ix-x0", "Arm 1 start", &s.x0, "Any").OnSubmit(submit)
				coordInput(c, "ix-x1", "Arm 1 end", &s.x1, "Any").OnSubmit(submit)
				coordInput(c, "ix-y0", "Arm 2 start", &s.y0, "Any").OnSubmit(submit)
				coordInput(c, "ix-y1", "Arm 2 end", &s.y1, "Any").OnSubmit(submit)
				Field(c, "Maximum e-value", func() {
					ui.TextInput(c.Key("ix-evalue"), &s.maxEvalue).Label("Maximum e-value").Placeholder("Any").Width(110).OnSubmit(submit)
				})
				Field(c, "Minimum dG", func() {
					ui.TextInput(c.Key("ix-dg0"), &s.minDG).Label("Minimum dG").Placeholder("Any").Width(100).OnSubmit(submit)
				})
				Field(c, "Maximum dG", func() {
					ui.TextInput(c.Key("ix-dg1"), &s.maxDG).Label("Maximum dG").Placeholder("Any").Width(100).OnSubmit(submit)
				})
				ui.Text(c, "A ΔG bound leaves out hybrids without ΔG; a missing e-value passes. Positions are 1-based; Enter or Apply filters.").
					FontSize(12).TextColor(t.TextMuted)
			})
		})
		if s.inputErr != "" {
			errorLine(c, s.inputErr)
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			summary := "No hybrids loaded yet."
			if s.key != "" {
				summary = fmt.Sprintf("Showing %s of %s hybrids", FormatNumber(int64(len(s.rows))), FormatNumber(int64(s.total)))
				if d := describeFilters(s.applied); d != "" {
					summary += " · " + d
				}
			}
			ui.Text(c, summary).TextColor(t.TextMuted).Grow(1).Shrink(1).MinWidth(0).SingleLine().Tooltip(summary)
			if s.applied.anchor.gene != "" && ui.Button(c, "Remove region").Tooltip("List every record again, not only the region's").Clicked() {
				f := s.applied
				f.anchor = ixAnchor{}
				s.applied = f
			}
			loadingLine(c, &s.load, "hybrids")
			loadingLine(c, &s.more, "more hybrids")
			if s.key != "" && len(s.rows) < s.total && !s.load.busy() {
				if ui.Button(c, "Load more").Disabled(s.more.busy()).Clicked() {
					p.loadMoreInteractions()
				}
			}
			density := nameOr(p.a.Density, densityLabels[0])
			ui.Select(c.Key("ix-density"), &density, densityLabels).Label("Table density").Width(170).OnChange(func() { p.a.setDensity(density) })
			none := s.key == "" || s.total == 0
			exportMenu(c,
				exportItem{"Filtered records · CSV…", none, p.exportFilteredCSV},
				exportItem{"Filtered records · HYB…", none, p.exportFilteredHYB},
				exportItem{"Loaded rows · TSV…", len(s.rows) == 0, p.exportInteractions},
				exportItem{"Parameters · JSON…", false, p.exportInteractionsParams})
		})
		if s.applied.withPartners() && s.key != "" {
			p.ixPartners(c)
		} else if s.applied.gene1 == "" {
			ui.Text(c, "Choose an RNA in the filter bar to see its interaction partners.").FontSize(12).TextColor(t.TextMuted)
		}
		p.hybridTable(c)
		p.hybridDetail(c)
	})
}

func describeFilters(f ixFilters) string {
	var parts []string
	one, two := "RNA ", "partner "
	if f.oriented() {
		one, two = "arm 1 ", "arm 2 "
	}
	if f.gene1 != "" {
		parts = append(parts, one+shortGene(f.gene1))
	}
	if f.gene2 != "" {
		parts = append(parts, two+shortGene(f.gene2))
	}
	if f.anchor.gene != "" {
		parts = append(parts, f.anchor.describe())
	}
	if f.scope != "" {
		parts = append(parts, strings.ToLower(f.scope))
	}
	if f.typ != "" && f.typ != allTypes && f.typ != allPattern {
		parts = append(parts, patternName(f.typ))
	}
	if f.search != "" {
		parts = append(parts, fmt.Sprintf("matching %q", f.search))
	}
	if f.minReads > 1 {
		parts = append(parts, fmt.Sprintf("≥ %d reads", f.minReads))
	}
	if f.minSupport > 1 {
		parts = append(parts, fmt.Sprintf("support ≥ %d", f.minSupport))
	}
	switch {
	case f.minDG != nil && f.maxDG != nil:
		parts = append(parts, fmt.Sprintf("ΔG %s to %s", dgText(f.minDG), dgText(f.maxDG)))
	case f.minDG != nil:
		parts = append(parts, "ΔG ≥ "+dgText(f.minDG))
	case f.maxDG != nil:
		parts = append(parts, "ΔG ≤ "+dgText(f.maxDG))
	}
	if f.maxEvalue != nil {
		parts = append(parts, "e-value ≤ "+dgText(f.maxEvalue))
	}
	if f.x0 > 0 || f.x1 > 0 {
		parts = append(parts, "arm 1 in "+rangeText(f.x0, f.x1))
	}
	if f.y0 > 0 || f.y1 > 0 {
		parts = append(parts, "arm 2 in "+rangeText(f.y0, f.y1))
	}
	if f.bothOrders && f.oriented() {
		parts = append(parts, "either arm order")
	}
	return strings.Join(parts, ", ")
}

func rangeText(start, end int) string {
	if end <= 0 {
		return FormatNumber(int64(start)) + "–end"
	}
	return span(max(start, 1), end)
}

func (p *explorePage) hybridTable(c *ui.Context) {
	s := &p.ix
	t := c.Theme()
	multi := len(p.ids) > 1
	rows := s.rows
	// One RNA (both arms on it in every row): say so once instead of
	// repeating its long name in two columns.
	oneGene := len(rows) > 0
	for _, r := range rows {
		if r.Gene1 != rows[0].Gene1 || r.Gene2 != rows[0].Gene1 {
			oneGene = false
			break
		}
	}
	all := []ui.TableColumn{
		{Title: "Sequence ID", Width: 190},
		{Title: "RNA 1", MinWidth: 80},
		{Title: "Region 1 (nt)", Width: 140, Align: ui.End},
		{Title: "RNA 2", MinWidth: 80},
		{Title: "Region 2 (nt)", Width: 140, Align: ui.End},
		{Title: "Reads", Width: 64, Align: ui.End},
		{Title: "Chimera type", Width: 200},
		{Title: "ΔG (kcal/mol)", Width: 122, Align: ui.End},
		{Title: "Sample", Width: 110},
		{Title: "Support", Width: 72, Align: ui.End},
	}
	var cols []ui.TableColumn
	var index []int // table column -> column of all
	// Support shows beside Reads (it was added after the other columns)
	for _, i := range []int{0, 1, 2, 3, 4, 5, 9, 6, 7, 8} {
		col := all[i]
		if (oneGene && (i == 1 || i == 3)) || (i == 8 && !multi) {
			continue
		}
		if oneGene && (i == 2 || i == 4) {
			col.Width, col.MinWidth = 0, 120
		}
		cols, index = append(cols, col), append(index, i)
	}
	if oneGene {
		ui.Text(c, "Both arms of every hybrid shown are on "+rows[0].Gene1+".").FontSize(12).TextColor(t.TextMuted)
	}
	ids := p.ids
	compact := p.a.compact()
	s.list.Selected = &s.chosen
	s.list.Key = func(i int) any { return [2]int64{int64(rows[i].Index), rows[i].ID} }
	ui.Table(c.Key("ix-table"), &s.list, cols, len(rows), func(row, col int) {
		r := rows[row]
		col = index[col]
		var text, tip string
		muted := false
		switch col {
		case 0:
			// the .hyb name, as the web's; the row id when the file has changed
			text, tip = r.Name, r.Name
			if text == "" {
				text, tip, muted = "#"+FormatNumber(r.ID), "The sample's .hyb file has changed since its index was built", true
			}
		case 1:
			text = r.Gene1
		case 2:
			text = span(r.Start1, r.End1)
		case 3:
			text = r.Gene2
		case 4:
			text = span(r.Start2, r.End2)
		case 5:
			text = FormatNumber(int64(r.Reads))
		case 6:
			// the web's name; the code shows on hover
			text, tip = patternName(r.Type), patternCodeLabel(r.Type)
		case 7:
			text = webFormat(r.DG)
		case 8:
			// the dataset's label, as the web's row dataset
			if r.Index < len(ids) {
				text, tip = ids[r.Index], ids[r.Index]
				if smp := p.a.Project.Sample(ids[r.Index]); smp != nil {
					text = smp.Label()
				}
			}
		case 9:
			text = FormatNumber(int64(r.Support))
		}
		el := ui.Text(c, text).SingleLine().Padding(3, 6)
		if tip != "" {
			el.Tooltip(tip)
		}
		if compact {
			el.Padding(0, 6).FontSize(12)
		}
		if muted {
			el.TextColor(t.TextMuted)
		}
	}).Grow(1).MinHeight(240).Label("Hybrids").Children(func() {
		if len(rows) == 0 && s.key != "" && !s.load.busy() {
			ui.Text(c, "No hybrids match these filters.").TextColor(t.TextMuted).Padding(12)
		}
	})
}

// hybridDetail is the web's record drawer: the chosen row in full, with its
// .hyb name, sequence, read coordinates and source line.
func (p *explorePage) hybridDetail(c *ui.Context) {
	s := &p.ix
	t := c.Theme()
	if s.chosen < 0 || s.chosen >= len(s.rows) {
		return
	}
	r := s.rows[s.chosen]
	p.loadRecordLine(r)
	Card(c, "", func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				ui.Text(c, "Interaction record").FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, recordID(r)).FontSize(16).Bold().SingleLine()
			})
			if r.Sequence != "" && ui.Button(c, "Copy sequence").Clicked() {
				c.WriteClipboard(r.Sequence)
				p.a.Toast("Sequence copied")
			}
			if ui.Button(c, "Close").Clicked() {
				s.chosen = -1
			}
		})
		ui.Text(c, "Sequence").FontSize(11).TextColor(t.TextMuted)
		if r.Sequence == "" {
			ui.Text(c, "The sequence is unavailable: the sample's .hyb file has changed since its index was built.").FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Text(c, truncate(r.Sequence, 180)).FontSize(12).Selectable().TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(24).Wrap().AlignItems(ui.Start).Children(func() {
			armDetails(c, "Arm 1", r.Gene1, r.Start1, r.End1, r.ReadStart1, r.ReadEnd1, r.EValue1)
			armDetails(c, "Arm 2", r.Gene2, r.Start2, r.End2, r.ReadStart2, r.ReadEnd2, r.EValue2)
		})
		ui.Row(c).Gap(24).Wrap().Children(func() {
			stat(c, "ΔG (kcal/mol)", webFormat(r.DG))
			stat(c, "Cluster support", FormatNumber(int64(r.Support)))
			stat(c, "Source reads", sourceReads(r))
			stat(c, "Overlap score", webFormat(r.Overlap))
			homodimer := "No"
			if isHomodimerProxy(r) {
				homodimer = "Yes (overlap ≥ 5)"
			}
			stat(c, "Homodimer proxy", homodimer)
			stat(c, "Chimera pattern", patternName(r.Type))
			stat(c, "Type source", typeSourceLabel(r))
			if dataset := p.recordDataset(r); dataset != "" {
				stat(c, "Dataset", dataset)
			}
			line := "—"
			if n := s.lines[p.lineKey(r)]; n != nil {
				line = FormatNumber(int64(*n))
			} else if s.lineLoad.busy() {
				line = "…"
			}
			stat(c, "Source line", line)
		})
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.Button(c, "Open in contact map").Tooltip("The map of arm 1's RNA against arm 2's, at this hybrid's cell").Clicked() {
				p.recordContact(r)
			}
			if ui.Button(c, "Explore RNA 1 region").Tooltip("Region around arm 1, with arm 2's RNA as the partner").Clicked() {
				p.recordRegion(r, true)
			}
			if ui.Button(c, "Explore RNA 2 region").Tooltip("Region around arm 2, with arm 1's RNA as the partner").Clicked() {
				p.recordRegion(r, false)
			}
			if ui.Button(c, "Fold sequence").Tooltip("Open Structure with this hybrid's arms").Clicked() {
				p.recordFold(r)
			}
			if ui.Button(c, "Copy record").Tooltip("Copy the hybrid's .hyb line").Disabled(r.Raw == "").Clicked() && r.Raw != "" {
				c.WriteClipboard(r.Raw)
				p.a.Toast("Record copied")
			}
		})
	})
}

// armDetails is one arm's section of the record drawer.
func armDetails(c *ui.Context, label, rna string, start, end int, readStart, readEnd, evalue *float64) {
	t := c.Theme()
	ui.Column(c).Gap(4).Basis(260).Grow(1).Children(func() {
		ui.Text(c, label).Bold()
		rows := [][2]string{{"RNA", rna}, {"RNA coordinates", span(start, end)},
			{"Read coordinates", nullableSpan(readStart, readEnd)}, {"E-value", webFormat(evalue)}}
		for _, row := range rows {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, row[0]).FontSize(12).TextColor(t.TextMuted).Width(120)
				ui.Text(c, row[1]).FontSize(12).Grow(1).Shrink(1).MinWidth(0).SingleLine().Tooltip(row[1])
			})
		}
	})
}

// recordID is the web's record id: the .hyb name, else its row.
func recordID(r hybridRow) string {
	if r.Name != "" {
		return r.Name
	}
	return "Hybrid " + FormatNumber(r.ID)
}

// nullableSpan is the web's nullableRange: a range of optional numbers.
func nullableSpan(start, end *float64) string {
	if start == nil || end == nil {
		return "—"
	}
	return webFormat(start) + "–" + webFormat(end)
}

// sourceReads is the web's rawReadCount: the read count in the .hyb name
// (<id>_<count>), "—" without one.
func sourceReads(r hybridRow) string {
	if r.Name != "" {
		fields := strings.Split(r.Name, "_")
		if len(fields) < 2 {
			return "—"
		}
		n, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || n < 1 || n > 1<<53-1 || strings.TrimLeft(fields[1], "0123456789") != "" {
			return "—"
		}
		return FormatNumber(n)
	}
	if r.Reads > 0 {
		return FormatNumber(int64(r.Reads))
	}
	return "—"
}

// isHomodimerProxy: both arms on one RNA with an overlap score of 5 or more.
func isHomodimerProxy(r hybridRow) bool {
	return r.Gene1 == r.Gene2 && r.Overlap != nil && *r.Overlap >= 5
}

func typeSourceLabel(r hybridRow) string {
	switch r.TypeSource {
	case "column_17":
		return "Column 17"
	case "inferred":
		return "Inferred from arms"
	}
	return "—"
}

// recordDataset is the web's Dataset: the row's sample and its .hyb file.
func (p *explorePage) recordDataset(r hybridRow) string {
	if r.Index >= len(p.ids) {
		return ""
	}
	id := p.ids[r.Index]
	s := p.a.Project.Sample(id)
	if s == nil {
		return id
	}
	name := s.Label()
	if s.Outputs.Hyb != "" {
		name += " · " + filepath.Base(s.Outputs.Hyb)
	}
	return name
}

func (p *explorePage) lineKey(r hybridRow) string {
	return fmt.Sprintf("%s\x00%d\x00%d", p.sel, r.Index, r.ID)
}

// loadRecordLine reads the chosen record's line number in its .hyb (a scan
// of the file up to it, so only for the record shown).
func (p *explorePage) loadRecordLine(r hybridRow) {
	s := &p.ix
	key := p.lineKey(r)
	if _, done := s.lines[key]; done || r.Raw == "" || r.Index >= len(p.ids) {
		return
	}
	path, id := p.a.Project.Path, p.ids[r.Index]
	p.ensure(&s.lineLoad, key, func() (func(), error) {
		got, err := p.query(path, "analysis_hybrids", []string{id}, core.Obj{
			"filters": core.Obj{"id": r.ID}, "limit": 1, "with_sequence": false, "with_line": true})
		var rows []struct {
			Line *int `json:"line"`
		}
		if err == nil {
			_ = model.Decode(got["rows"], &rows)
		}
		return func() {
			if s.lines == nil {
				s.lines = map[string]*int{}
			}
			s.lines[key] = nil
			if len(rows) == 1 {
				s.lines[key] = rows[0].Line
			}
		}, nil
	})
}

// ixPartners is the web's partner panel: the chosen RNA's partners under
// the filters, the top ten by the count mode; a click makes one the
// partner filter.
func (p *explorePage) ixPartners(c *ui.Context) {
	s := &p.ix
	t := c.Theme()
	records := s.countMode == countModes[1]
	partners := append([]partnerTotal(nil), s.partners...)
	value := func(pt partnerTotal) int64 {
		if records {
			return pt.Records
		}
		return pt.Support
	}
	sort.SliceStable(partners, func(i, j int) bool {
		if value(partners[i]) != value(partners[j]) {
			return value(partners[i]) > value(partners[j])
		}
		return partners[i].Name < partners[j].Name
	})
	Card(c, shortGene(s.applied.gene1)+" interaction partners", func() {
		ui.Text(c, s.countMode+" · click a partner to refine the table.").FontSize(12).TextColor(t.TextMuted)
		if len(partners) == 0 {
			ui.Text(c, "No records match the current filters.").TextColor(t.TextMuted)
			return
		}
		top := max(value(partners[0]), 1)
		for i, pt := range partners {
			if i == 10 {
				ui.Text(c, fmt.Sprintf("and %d more", len(partners)-10)).FontSize(12).TextColor(t.TextMuted)
				break
			}
			row := ui.ButtonBase(c.Key("ix-partner-"+pt.Name)).Label("Partner "+pt.Name).Gap(8).AlignItems(ui.Center).Padding(2, 4).Radius(4).
				Children(func() {
					ui.Text(c, shortGene(pt.Name)).Width(190).SingleLine().Tooltip(pt.Name)
					fraction := max(float32(value(pt))/float32(top), 0.03)
					ui.Box(c).Grow(1).Height(8).Radius(4).Background(t.Border).Children(func() {
						ui.Box(c).WidthPercent(fraction * 100).FillHeight().Radius(4).Background(t.Accent)
					})
					ui.Text(c, FormatNumber(value(pt))).FontSize(12).TextColor(t.TextMuted).Width(90).TextAlign(ui.End)
				})
			if row.Clicked() {
				s.gene2 = pt.Name
				s.applyPickers()
			}
		}
	})
}

// selectGene makes gene the top bar's RNA (and its reference the one
// selected); false, with a toast, when no reference of the project holds it.
func (p *explorePage) selectGene(gene string) bool {
	a := p.a
	if gene != a.Selected.Gene {
		reference, previous := a.Selected.ReferenceID, a.Selected.Gene
		if ref := stReferenceOf(a.Project, gene); ref != "" {
			a.Selected.ReferenceID = ref
		}
		a.Selected.Gene = gene
		a.normalizeSelection()
		if a.Selected.Gene != gene {
			a.Selected.ReferenceID, a.Selected.Gene = reference, previous
			a.normalizeSelection()
			a.Toast("%s is not in the project's references", gene)
			return false
		}
		a.saveUIState()
	}
	p.track()
	return true
}

// recordContact opens the contact map of arm 1's RNA against arm 2's,
// with every chimera type and hybrid, at the hybrid's cell (the web's
// "Open in Contact Map").
func (p *explorePage) recordContact(r hybridRow) {
	if !p.selectGene(r.Gene1) {
		return
	}
	s := &p.cm
	s.gene2, s.typ, s.homodimer = r.Gene2, allPattern, cmHomodimerOptions[0]
	// the record's cell, selected on the full extent (the web's)
	s.focus, s.focusZoom = &[4]int{r.Start1, r.Start2, r.End1, r.End2}, false
	s.load.reset()
	p.tab = tabContact
}

// recordRegion opens Region on one arm, with the other arm's RNA as the
// partner (the web's "Explore RNA 1 / RNA 2 region").
func (p *explorePage) recordRegion(r hybridRow, armOne bool) {
	gene, start, end, partner := r.Gene1, r.Start1, r.End1, r.Gene2
	if !armOne {
		gene, start, end, partner = r.Gene2, r.Start2, r.End2, r.Gene1
	}
	if !p.selectGene(gene) {
		return
	}
	s := &p.rg
	s.applyRange(start, end)
	s.setPartner(partner, 0, 0)
	p.tab = tabRegion
}

// recordFold opens ⑤ Structure on the record's own sequence (the "HYB
// record sequence" source), as the web's "Fold sequence" does.
func (p *explorePage) recordFold(r hybridRow) {
	p.toStructure(map[string]any{"record": r, "source": "an interaction record"})
}
