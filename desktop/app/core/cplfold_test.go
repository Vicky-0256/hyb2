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
}
