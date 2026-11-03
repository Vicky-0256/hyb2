package views

import (
	"fmt"
	"strings"

	"hyb2desktop/model"
)

// The fold's evidence: the samples whose chimeras guide it. As the web's
// RNA Structure, which "takes its evidence as set by HYB evidence from"
// rather than the header's merge, the top bar's selection is followed only
// as far as it fits the RNA to fold: of "All datasets merged" (or a
// followed selection that mixes references) only the samples mapped to
// that RNA's reference are used. A prefill's "sampleIds" are used exactly.

// geneReference is the reference holding the RNA to fold, nil if none
// does.
func (p *structurePage) geneReference() *model.Reference {
	a := p.a
	if a.Project == nil || p.gene == "" {
		return nil
	}
	var want *model.Reference
	for i, r := range a.Project.References {
		for _, s := range r.Sequences {
			if s.Name == p.gene {
				want = &a.Project.References[i]
			}
		}
	}
	return want
}

func stReferenceName(r *model.Reference) string {
	if r.Label != "" {
		return r.Label
	}
	return r.ID
}

// stFitsReference: sample id is mapped to want (or its reference is not
// known).
func (p *structurePage) stFitsReference(id string, want *model.Reference) bool {
	s := p.a.Project.Sample(id)
	return want == nil || s == nil || s.ReferenceID == "" || s.ReferenceID == want.ID
}

// evidenceFilter: the evidence selection is the top bar's (followed) or
// all datasets, so its samples on other references are left out rather
// than blocking the fold (a reference region only).
func (p *structurePage) evidenceFilter() bool {
	return p.pinned == nil && p.src.kind == stSourceReference && (p.evidence == AllSelection || p.evidence == p.topKey)
}

// evidenceIDs are the samples whose chimeras guide the fold: the pinned
// samples, else the evidence selection's, of a followed selection only
// those mapped to the RNA's reference when some are (evidenceMismatch
// says why none is).
func (p *structurePage) evidenceIDs() []string {
	if p.pinned != nil {
		return p.pinned
	}
	ids := p.a.SelectionIDs(p.evidence)
	if !p.evidenceFilter() {
		return ids
	}
	want := p.geneReference()
	var fit []string
	for _, id := range ids {
		if p.stFitsReference(id, want) {
			fit = append(fit, id)
		}
	}
	if len(fit) == 0 {
		return ids
	}
	return fit
}

// evidenceLabel names the evidence: the selection's label, the pinned
// samples' names, or how many of the selection's samples are used.
func (p *structurePage) evidenceLabel() string {
	a := p.a
	if p.pinned != nil {
		return p.sampleNames(p.pinned)
	}
	label := a.SelectionLabel(p.evidence)
	if ids := p.evidenceIDs(); len(ids) < len(a.SelectionIDs(p.evidence)) {
		if want := p.geneReference(); want != nil {
			return fmt.Sprintf("%s, the %d mapped to %s", label, len(ids), stReferenceName(want))
		}
	}
	return label
}

// evidenceMismatch says why the evidence cannot be used for the chosen RNA:
// its samples are mapped to another reference (their chimeras name other
// sequences). "" when they match or the RNA's reference is unknown.
func (p *structurePage) evidenceMismatch() string {
	a := p.a
	want := p.geneReference()
	if want == nil {
		return ""
	}
	for _, id := range p.evidenceIDs() {
		if p.stFitsReference(id, want) {
			continue
		}
		other := a.Project.Sample(id).ReferenceID
		if r := a.Project.Reference(other); r != nil {
			other = stReferenceName(r)
		}
		return fmt.Sprintf("%s is mapped to %s, not to %s (the reference of this RNA). Choose evidence mapped to %s.",
			p.evidenceLabel(), other, stReferenceName(want), stReferenceName(want))
	}
	return ""
}

// pinSamples makes a prefill's "sampleIds" the evidence: its analysed
// samples, in its order; none (or no list) leaves the selection's.
func (p *structurePage) pinSamples(ids []string) {
	p.pinned = nil
	if p.a.Project == nil {
		return
	}
	var ready []string
	for _, id := range ids {
		if s := p.a.Project.Sample(strings.TrimSpace(id)); s != nil && s.IsReady() {
			ready = append(ready, s.ID)
		}
	}
	if len(ready) > 0 {
		p.pinned = ready
	}
}
