package core

import "testing"

// Values from detector.cplfold_memory_estimate (bytes / 2^20).
func TestCPLfoldMemoryMatchesDetector(t *testing.T) {
	for _, tc := range []struct {
		n    int
		pk   bool
		want float64
	}{
		{3000, false, 1462.294189453125}, {5331, false, 4203.244800567627},
		{10807, false, 16676.360553741455}, {10807, true, 22468.162910461426},
	} {
		got := CPLfoldMemoryMB(tc.n, tc.pk)
		if float64(got) < tc.want || float64(got)-tc.want >= 1 {
			t.Fatalf("%d nt pk=%v: %d MiB, detector %.2f", tc.n, tc.pk, got, tc.want)
		}
	}
	if StructureClaimMB(300, true, 4) != 4096 {
		t.Fatal("small windows keep the default claim")
	}
	if got := StructureClaimMB(10807, false, 1); got != 16677 {
		t.Fatalf("genome claim %d", got)
	}
}
