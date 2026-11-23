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

// selectedReferenceID is the common reference of the unfiltered evidence.
// Do not use evidenceIDs here: filtering already depends on the chosen RNA.
func (p *structurePage) selectedReferenceID() string {
	ids := p.pinned
	if ids == nil {
		ids = p.a.SelectionIDs(p.evidence)
	}
	ref := ""
	for _, id := range ids {
		s := p.a.Project.Sample(id)
		if s == nil || s.ReferenceID == "" || (ref != "" && ref != s.ReferenceID) {
			return ""
		}
		ref = s.ReferenceID
	}
	return ref
}

// rnaReference resolves a form arm without discarding its reference. An
// explicit picker choice is authoritative. Otherwise the selected samples
// disambiguate the name, or a globally unique RNA can supply the reference.
// An implicit arm B follows arm A's reference when that RNA is present.
func (p *structurePage) rnaReference(second bool) (*model.Reference, int) {
	pr := p.a.Project
	if pr == nil {
		return nil, 0
	}
	gene, ref := p.gene, p.referenceID
	if second && p.gene2 != "" {
		gene, ref = p.gene2, p.referenceID2
		if ref == "" {
			if first, _ := p.rnaReference(false); first != nil {
				if r, n := pr.GeneReference(gene, first.ID); r != nil {
					return r, n
				}
			}
		}
	}
	if ref != "" {
		return pr.GeneReference(gene, ref)
	}
	if ref = p.selectedReferenceID(); ref != "" {
		if r, n := pr.GeneReference(gene, ref); r != nil {
			return r, n
		}
	}
	// The public bare-name lookup keeps first-match behavior for older
	// readers. Only this form's fallback requires uniqueness across references.
	var found *model.Reference
	length := 0
	for i := range pr.References {
		r := &pr.References[i]
		for _, s := range r.RNAs() {
			if s.Name == gene {
				if found != nil {
					return nil, 0
				}
				found, length = r, s.Length
				break
			}
		}
	}
	return found, length
}

// geneReference is the resolved reference of arm A, nil if missing or
// ambiguous. In particular it never uses project reference order.
func (p *structurePage) geneReference() *model.Reference {
	ref, _ := p.rnaReference(false)
	return ref
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
		s := a.Project.Sample(id)
		if s == nil || s.ReferenceID == "" {
			return "The selected samples need a mapped reference before folding a reference region."
		}
		if p.stFitsReference(id, want) {
			continue
		}
		other := s.ReferenceID
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
