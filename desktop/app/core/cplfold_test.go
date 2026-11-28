package core

import "testing"

// Values from detector.cplfold_memory_estimate (bytes / 2^20).
func TestCPLfoldMemoryMatchesDetector(t *testing.T) {
	for _, tc := range []struct {
		n    int
		pk   bool
		want float64
	}{
		{3000, false, 1496.62646484375}, {5331, false, 4311.65682220459},
		{10807, false, 17121.883811950684}, {10807, true, 22913.686168670654},
	} {
		got := CPLfoldMemoryMB(tc.n, tc.pk)
		if float64(got) < tc.want || float64(got)-tc.want >= 1 {
			t.Fatalf("%d nt pk=%v: %d MiB, detector %.2f", tc.n, tc.pk, got, tc.want)
		}
	}
	if StructureClaimMB(300, true, 4) != 4096 {
		t.Fatal("small windows keep the default claim")
	}
	if got := StructureClaimMB(10807, false, 1); got != 17122 {
		t.Fatalf("genome claim %d", got)
	}
	// The longest fold a budget holds: detector._max_length_for(budget /
	// parallel), and nothing below the default claim.
	for _, tc := range []struct {
		budgetMB       uint64
		pk             bool
		parallel, want int
	}{
		{24576, false, 1, 12969}, {24576, true, 1, 11195}, {24576, true, 2, 7885},
		{8192, false, 1, 7428}, {4096, false, 1, 5189}, {4095, false, 1, 0},
	} {
		got := StructureMaxNT(tc.budgetMB, tc.pk, tc.parallel)
		if got != tc.want {
			t.Fatalf("%d MiB pk=%v x%d: %d nt, detector %d", tc.budgetMB, tc.pk, tc.parallel, got, tc.want)
		}
		if got > 0 && (StructureClaimMB(got, tc.pk, tc.parallel) > tc.budgetMB || StructureClaimMB(got+1, tc.pk, tc.parallel) <= tc.budgetMB) {
			t.Fatalf("%d nt is not the longest fold within %d MiB", got, tc.budgetMB)
		}
	}
}
