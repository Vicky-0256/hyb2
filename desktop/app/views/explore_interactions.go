package views

import (
	"fmt"
	"math"
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
	// scopeLabels are the web's interaction scopes; the homodimer proxy is
	// both arms on one RNA with an overlap score of 5 or more.
	scopeLabels = []string{"All interactions", "Intramolecular", "Intermolecular", "Homodimer proxy", "Not homodimer proxy"}
)

// ixFilters are the filters applied (the inputs hold drafts until Apply).
type ixFilters struct {
	gene1, gene2   string // "" = any
	typ            string
	scope          string // one of scopeLabels; "" = all
	search         string
	minReads       int
	minSupport     int
	minDG, maxDG   *float64
	x0, x1, y0, y1 int // 0 = unset
}

type interactionsState struct {
	applied ixFilters
	// inputs
	gene1, gene2, typ, order, scope string
	search, minReads, minSupport     string
	minDG, maxDG, x0, x1, y0, y1     string
	inputErr                         string
	// moreOpen shows the ΔG and arm position filters (open while one is set).
	moreOpen bool

	load, more loader
	key        string // the applied key the rows belong to
	rows       []hybridRow
	total      int
	list       ui.ListState
	chosen     int
}

type hybridRow struct {
	Index  int      `json:"index"`
	ID     int64    `json:"id"`
	Gene1  string   `json:"gene1"`
	Start1 int      `json:"start1"`
	End1   int      `json:"end1"`
	Gene2  string   `json:"gene2"`
	Start2 int      `json:"start2"`
	End2   int      `json:"end2"`
	Reads   int      `json:"reads"`
	Support int      `json:"support"`
	DG      *float64 `json:"dg"`
	Overlap *float64 `json:"overlap"`
	Type    string   `json:"type"`
	// Name and Sequence are columns 1 and 2 of the .hyb line ("" when the
	// file has changed since the index was built).
	Name     string `json:"name"`
	Sequence string `json:"sequence"`
}

func (s *interactionsState) init() {
	s.gene1, s.gene2, s.typ, s.order, s.scope = anyGene, anyGene, allTypes, orderLabels[0], scopeLabels[0]
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
	s.minDG, s.maxDG = dgText(f.minDG), dgText(f.maxDG)
	s.x0, s.x1, s.y0, s.y1 = coordText(f.x0), coordText(f.x1), coordText(f.y0), coordText(f.y1)
	s.inputErr = ""
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
	f := ixFilters{typ: s.typ, search: strings.TrimSpace(s.search)}
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
		return f, fmt.Errorf("min reads must be a whole number")
	}
	f.minReads = max(reads, 1)
	support, ok := parseCoord(s.minSupport)
	if !ok {
		return f, fmt.Errorf("min support must be a whole number")
	}
	f.minSupport = max(support, 1)
	if f.minDG, ok = parseDG(s.minDG); !ok {
		return f, fmt.Errorf("ΔG from must be a number")
	}
	if f.maxDG, ok = parseDG(s.maxDG); !ok {
		return f, fmt.Errorf("ΔG to must be a number")
	}
	if f.minDG != nil && f.maxDG != nil && *f.minDG > *f.maxDG {
		return f, fmt.Errorf("the ΔG range's lower bound must not exceed its upper bound")
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

// payload is the analysis_hybrids filters object.
func (f ixFilters) payload() core.Obj {
	out := core.Obj{}
	if f.gene1 != "" {
		out["gene1"] = f.gene1
	}
	if f.gene2 != "" {
		out["gene2"] = f.gene2
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
	return fmt.Sprintf("%s\x00%+v\x00%s\x00%s\x00%s", p.sel, f, dgText(f.minDG), dgText(f.maxDG), s.orderValue())
}

func (p *explorePage) fetchHybrids(path string, ids []string, f ixFilters, order string, offset int) ([]hybridRow, int, error) {
	got, err := p.query(path, "analysis_hybrids", ids, core.Obj{
		"filters": f.payload(), "limit": interactionsPage, "offset": offset, "order": order, "with_sequence": true,
	})
	if err != nil {
		return nil, 0, err
	}
	var rows []hybridRow
	if err := model.Decode(got["rows"], &rows); err != nil {
		return nil, 0, err
	}
	return rows, int(num(got["total"])), nil
}

func (p *explorePage) loadInteractions() {
	s := &p.ix
	key := p.interactionsKey()
	path, ids, f, order := p.a.Project.Path, p.ids, s.applied, s.orderValue()
	p.ensure(&s.load, key, func() (func(), error) {
		rows, total, err := p.fetchHybrids(path, ids, f, order, 0)
		if err != nil {
			return nil, err
		}
		return func() {
			s.key, s.rows, s.total, s.chosen = key, rows, total, -1
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
		rows, total, err := p.fetchHybrids(path, ids, f, order, offset)
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
			picker(c, "ix-gene1", "Gene 1", &s.gene1, genes, 150).OnChange(submit)
			picker(c, "ix-gene2", "Gene 2", &s.gene2, genes, 150).OnChange(submit)
			picker(c, "ix-scope", "Scope", &s.scope, scopeLabels, 150).OnChange(submit)
			picker(c, "ix-type", "Chimera type", &s.typ, withAll(allTypes, p.summary.types), 130).OnChange(submit)
			picker(c, "ix-order", "Sort by", &s.order, orderLabels, 150)
			Field(c, "Search", func() {
				ui.TextInput(c.Key("ix-search"), &s.search).Label("Search").Placeholder("ID, sequence, RNA, type").Width(170).OnSubmit(submit)
			})
			Field(c, "Min reads", func() {
				ui.TextInput(c.Key("ix-min"), &s.minReads).Label("Min reads").Width(70).OnSubmit(submit)
			})
			Field(c, "Min support", func() {
				ui.TextInput(c.Key("ix-min-support"), &s.minSupport).Label("Min support").Width(70).OnSubmit(submit)
			})
			if ui.PrimaryButton(c, "Apply").Clicked() {
				submit()
			}
			if ui.Button(c, "Clear").Clicked() {
				s.apply(ixFilters{minReads: 1, minSupport: 1, typ: allTypes})
			}
		})
		set := 0
		for _, v := range []string{s.minDG, s.maxDG, s.x0, s.x1, s.y0, s.y1} {
			if strings.TrimSpace(v) != "" {
				set++
			}
		}
		if set > 0 {
			s.moreOpen = true
		}
		label := "ΔG and arm positions"
		if set > 0 {
			label += fmt.Sprintf(" (%d set)", set)
		}
		ui.Collapsible(c.Key("ix-arms"), label, &s.moreOpen, func() {
			ui.Row(c).Gap(12).Wrap().AlignItems(ui.End).Padding(6, 0, 0, 0).Children(func() {
				Field(c, "ΔG from", func() {
					ui.TextInput(c.Key("ix-dg0"), &s.minDG).Label("ΔG from").Placeholder("kcal/mol").Width(90).OnSubmit(submit)
				})
				Field(c, "ΔG to", func() {
					ui.TextInput(c.Key("ix-dg1"), &s.maxDG).Label("ΔG to").Placeholder("kcal/mol").Width(90).OnSubmit(submit)
				})
				coordInput(c, "ix-x0", "Arm 1 from", &s.x0, "start").OnSubmit(submit)
				coordInput(c, "ix-x1", "Arm 1 to", &s.x1, "end").OnSubmit(submit)
				coordInput(c, "ix-y0", "Arm 2 from", &s.y0, "start").OnSubmit(submit)
				coordInput(c, "ix-y1", "Arm 2 to", &s.y1, "end").OnSubmit(submit)
				ui.Text(c, "A ΔG bound leaves out hybrids without ΔG. Positions are 1-based; Enter or Apply filters.").FontSize(12).TextColor(t.TextMuted)
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
			ui.Text(c, summary).TextColor(t.TextMuted).Grow(1).SingleLine()
			loadingLine(c, &s.load, "hybrids")
			loadingLine(c, &s.more, "more hybrids")
			if s.key != "" && len(s.rows) < s.total && !s.load.busy() {
				if ui.Button(c, "Load more").Disabled(s.more.busy()).Clicked() {
					p.loadMoreInteractions()
				}
			}
			if ui.Button(c, "Export TSV…").Tooltip("Save the rows loaded as a tab-separated file").Disabled(len(s.rows) == 0).Clicked() && len(s.rows) > 0 {
				p.exportInteractions()
			}
		})
		p.hybridTable(c)
		p.hybridDetail(c)
	})
}

func describeFilters(f ixFilters) string {
	var parts []string
	if f.gene1 != "" {
		parts = append(parts, "gene 1 "+shortGene(f.gene1))
	}
	if f.gene2 != "" {
		parts = append(parts, "gene 2 "+shortGene(f.gene2))
	}
	if f.scope != "" {
		parts = append(parts, strings.ToLower(f.scope))
	}
	if f.typ != "" && f.typ != allTypes {
		parts = append(parts, f.typ)
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
	if f.x0 > 0 || f.x1 > 0 {
		parts = append(parts, "arm 1 in "+rangeText(f.x0, f.x1))
	}
	if f.y0 > 0 || f.y1 > 0 {
		parts = append(parts, "arm 2 in "+rangeText(f.y0, f.y1))
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
		{Title: "#", Width: 64, Align: ui.End},
		{Title: "Gene 1", MinWidth: 80},
		{Title: "Arm 1 (nt)", Width: 140, Align: ui.End},
		{Title: "Gene 2", MinWidth: 80},
		{Title: "Arm 2 (nt)", Width: 140, Align: ui.End},
		{Title: "Reads", Width: 64, Align: ui.End},
		{Title: "Type", Width: 84},
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
	s.list.Selected = &s.chosen
	s.list.Key = func(i int) any { return [2]int64{int64(rows[i].Index), rows[i].ID} }
	ui.Table(c.Key("ix-table"), &s.list, cols, len(rows), func(row, col int) {
		r := rows[row]
		col = index[col]
		var text string
		muted := false
		switch col {
		case 0:
			text, muted = FormatNumber(r.ID), true
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
			text = r.Type
		case 7:
			text = "—"
			if r.DG != nil {
				text = strconv.FormatFloat(*r.DG, 'f', 1, 64)
			}
		case 8:
			if r.Index < len(ids) {
				text = ids[r.Index]
			}
		case 9:
			text = FormatNumber(int64(r.Support))
		}
		el := ui.Text(c, text).SingleLine().Padding(3, 6)
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
// .hyb name and sequence.
func (p *explorePage) hybridDetail(c *ui.Context) {
	s := &p.ix
	t := c.Theme()
	if s.chosen < 0 || s.chosen >= len(s.rows) {
		return
	}
	r := s.rows[s.chosen]
	Card(c, "", func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			title := "Hybrid " + FormatNumber(r.ID)
			if r.Name != "" {
				title += " · " + r.Name
			}
			ui.Text(c, title).FontSize(16).Bold().Grow(1).Shrink(1).MinWidth(0).SingleLine()
			if r.Sequence != "" && ui.Button(c, "Copy sequence").Clicked() {
				c.WriteClipboard(r.Sequence)
				p.a.Toast("Sequence copied")
			}
			if ui.Button(c, "Close").Clicked() {
				s.chosen = -1
			}
		})
		ui.Row(c).Gap(24).Wrap().Children(func() {
			stat(c, "Arm 1", shortGene(r.Gene1)+":"+span(r.Start1, r.End1))
			stat(c, "Arm 2", shortGene(r.Gene2)+":"+span(r.Start2, r.End2))
			stat(c, "Type", r.Type)
			stat(c, "Reads", FormatNumber(int64(r.Reads)))
			stat(c, "Cluster support", FormatNumber(int64(r.Support)))
			dg, overlap := "—", "—"
			if r.DG != nil {
				dg = strconv.FormatFloat(*r.DG, 'f', -1, 64) + " kcal/mol"
			}
			if r.Overlap != nil {
				overlap = strconv.FormatFloat(*r.Overlap, 'f', -1, 64)
			}
			stat(c, "ΔG", dg)
			stat(c, "Overlap score", overlap)
			if len(p.ids) > 1 && r.Index < len(p.ids) {
				stat(c, "Sample", p.ids[r.Index])
			}
		})
		if r.Sequence == "" {
			ui.Text(c, "The sequence is unavailable: the sample's .hyb file has changed since its index was built.").FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Text(c, r.Sequence).FontSize(12).Selectable().TextColor(t.TextMuted)
	})
}
