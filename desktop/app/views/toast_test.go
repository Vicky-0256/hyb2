package views

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Toasts show in the bottom right corner, clear of the pinned Run fold.
func TestToastsClearOfRunFold(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1400, 900)
	tt.SetFocused(true)
	a.Navigate("structure", nil)
	csSettle(a, tt)
	a.Toast("Saved /private/tmp/claude-502/very/long/path/ENSG_ENST_ZIKV-PE243-2015_virusRNA_structure-task-1-0_report.json")
	a.Toast("Prepared RNA sequence copied to the clipboard.")
	csSettle(a, tt)
	r, ok := tt.Find("Prepared RNA sequence copied to the clipboard.")
	b, ok2 := tt.Find("Run fold")
	if !ok || !ok2 {
		t.Fatalf("toast %v, Run fold %v: %q", ok, ok2, tt.Texts())
	}
	if r.X < b.X+b.W && r.Y < b.Y+b.H && r.Y+r.H > b.Y {
		t.Errorf("toast overlaps Run fold")
	}
	snapshot(t, tt, "toasts")
}
