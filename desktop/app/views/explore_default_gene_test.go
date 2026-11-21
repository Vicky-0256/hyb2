package views

import (
	"testing"

	"hyb2desktop/model"
)

// The top bar's default RNA is the reference's first sequence; when that is
// one without hybrids (a decoy), Explore shows the RNA with the most
// hybrids instead. An RNA the user picked stays, even with no hybrid.
func TestExploreDefaultGeneHasHybrids(t *testing.T) {
	a, _, p, tt := openExplore(t)
	const decoy = "AAA_decoy_RNA"
	ref := &a.Project.References[0]
	ref.Sequences = append([]model.Sequence{{Name: decoy, Length: 500}}, ref.Sequences...)
	a.Selected.Gene = ""
	a.normalizeSelection()
	if a.Selected.Gene != decoy || a.autoGene != decoy {
		t.Fatalf("default %q auto %q", a.Selected.Gene, a.autoGene)
	}
	p.track()
	settle(a, tt)
	if a.Selected.Gene != zikvGene || a.autoGene != zikvGene {
		t.Fatalf("gene %q, want %q", a.Selected.Gene, zikvGene)
	}

	// picked by the user: kept
	if !p.selectGene(decoy) {
		t.Fatal("decoy not selectable")
	}
	settle(a, tt)
	if a.Selected.Gene != decoy {
		t.Fatalf("user's RNA replaced by %q", a.Selected.Gene)
	}
}
