package views

// Answers to reads that a project switch, a Refresh or a finished task made
// stale are dropped (report.go, compare.go, compare_panels.go).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/model"
)

// waitPosted waits until n answers are posted (not yet applied).
func waitPosted(t *testing.T, a *App, n int) {
	t.Helper()
	for range 400 {
		a.mu.Lock()
		got := len(a.pending)
		a.mu.Unlock()
		if got >= n {
			return
		}
		sleepMS(5)
	}
	t.Fatalf("%d answers not posted", n)
}

// applyFirstPosted applies only the oldest posted answer.
func applyFirstPosted(a *App) {
	a.mu.Lock()
	fn := a.pending[0]
	a.pending = a.pending[1:]
	a.mu.Unlock()
	fn()
}

// A Show for another project while the report is read clears the page at
// once; the earlier answer is dropped and the new project is read.
func TestReportProjectSwitchWhileLoading(t *testing.T) {
	a, _ := openReport(t)
	tt := ui.NewTester(a.View, 1100, 3000)
	a.Navigate("report", nil)
	csSettle(a, tt)
	p := a.PageFor("report").(*reportPage)
	pathA := a.Project.Path
	if p.data == nil || p.data.Path != pathA || p.loading {
		t.Fatalf("report of A not shown: %v", p.data != nil)
	}
	p.data = nil
	p.load()
	waitPosted(t, a, 1)
	other := *a.Project
	other.Path, other.Name = "/projects/other.hyb2proj", "Other"
	a.Project = &other
	p.load()
	if p.data != nil || p.project != other.Path || !p.again {
		t.Fatalf("not cleared for B: data %v, project %q, again %v", p.data != nil, p.project, p.again)
	}
	applyFirstPosted(a) // A's report
	if p.data != nil {
		t.Fatalf("A's report shown for B: %q", p.data.Path)
	}
	if !p.loading {
		t.Fatal("B is not read")
	}
	waitFlush(a)
	if p.data == nil || p.data.Path != other.Path || p.data.Name != "Other" || p.loading {
		t.Fatalf("B's report: %+v", p.data)
	}
}

// Refresh forgets the read tests and the views drawn from them; a read in
// flight from before it answers into nothing.
func TestCompareRefreshDropsAReadInFlight(t *testing.T) {
	a, _ := openCS(t)
	tt := ui.NewTester(a.View, 1100, 1400)
	a.Navigate("compare", nil)
	csSettle(a, tt)
	p := a.PageFor("compare").(*comparePage)
	id := p.selectedID
	if p.cache[id] == nil || p.heatKey == "" || p.shownKey == "" {
		t.Fatalf("not read: %v %q %q", p.cache[id] != nil, p.heatKey, p.shownKey)
	}
	delete(p.cache, id)
	p.ensureResult()
	waitPosted(t, a, 1)
	if err := tt.Click("Refresh"); err != nil {
		t.Fatal(err)
	}
	if p.heatKey != "" || p.simKey != "" || p.shownKey != "" {
		t.Fatalf("derived keys kept: %q %q %q", p.heatKey, p.simKey, p.shownKey)
	}
	applyFirstPosted(a) // the read from before Refresh
	if p.cache[id] != nil {
		t.Fatal("a read from before Refresh landed in the new cache")
	}
	csSettle(a, tt)
	if p.cache[id] == nil || p.heatKey == "" || p.shownKey == "" {
		t.Fatalf("not read again: %v %q %q", p.cache[id] != nil, p.heatKey, p.shownKey)
	}
}

// A finished energy task clears the read energies and the reads in flight:
// an answer from before it is dropped and the test is read again.
func TestComparePanelsEnergyTaskDropsAReadInFlight(t *testing.T) {
	a, f := openPanels(t)
	tt := ui.NewTester(a.View, 1100, 3600)
	a.Navigate("compare", nil)
	csSettle(a, tt)
	p := a.PageFor("compare").(*comparePage)
	s := &p.panels
	id := p.selectedID
	if s.energy[id] == nil {
		t.Fatal("energy not read")
	}
	reads := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.payloads["read_energy"])
	}
	s.clearEnergyOf(id)
	p.loadEnergy(id)
	waitPosted(t, a, 1)
	before := reads()
	for _, hook := range taskFinishedHooks {
		hook(a, model.Task{Kind: "energy", Status: "succeeded"})
	}
	if !s.energyBusy[id] {
		t.Fatal("the test is not read again after the task")
	}
	applyFirstPosted(a) // the read from before the task finished
	if s.energy[id] != nil || !s.energyBusy[id] {
		t.Fatalf("stale answer applied: energy %v, busy %v", s.energy[id] != nil, s.energyBusy[id])
	}
	csSettle(a, tt)
	if s.energy[id] == nil || s.energyBusy[id] || reads() <= before {
		t.Fatalf("energy %v, busy %v, reads %d", s.energy[id] != nil, s.energyBusy[id], reads())
	}
}
