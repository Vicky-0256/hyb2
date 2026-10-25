package core

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Two concurrent patches touching different keys: both should survive.
func TestPreferencesConcurrentSavesKeepBoth(t *testing.T) {
	lost, failed := 0, 0
	for i := 0; i < 200; i++ {
		path := filepath.Join(t.TempDir(), "prefs.json")
		var wg sync.WaitGroup
		var e1, e2 error
		wg.Add(2)
		go func() { defer wg.Done(); _, e1 = ApplyPreferences(path, Obj{"theme": "dark"}) }()
		go func() { defer wg.Done(); _, e2 = ApplyPreferences(path, Obj{"external_lookup": true}) }()
		wg.Wait()
		if e1 != nil || e2 != nil {
			failed++
			if i == 0 {
				t.Log(e1, e2)
			}
			continue
		}
		stored := readPrefs(path)
		if stored["theme"] != "dark" || stored["external_lookup"] != true {
			lost++
		}
	}
	if lost+failed > 0 {
		t.Fatalf("of 200 concurrent patch pairs: %d lost an update, %d returned an error", lost, failed)
	}
}

// A budget change is in force before the next one is saved, so two quick
// changes cannot reach the scheduler in the opposite order.
func TestBudgetChangeAppliesBeforeTheNextIsSaved(t *testing.T) {
	s := newTestService(t)
	set := func(cpu int, done chan<- struct{}) {
		if _, err := s.SetPreferences(Obj{"budget": Obj{"cpu": cpu, "mem_mb": 512}}); err != nil {
			t.Error(err)
		}
		close(done)
	}
	// The scheduler is busy: the first change is saved but cannot apply yet.
	s.tasks.mu.Lock()
	first, second := make(chan struct{}), make(chan struct{})
	go set(1, first)
	for LoadBudget(s.prefsPath).CPU != 1 {
		time.Sleep(time.Millisecond)
	}
	go set(2, second)
	time.Sleep(100 * time.Millisecond)
	saved := LoadBudget(s.prefsPath)
	s.tasks.mu.Unlock()
	<-first
	<-second
	if saved.CPU != 1 {
		t.Fatalf("second change saved before the first applied: %v", saved)
	}
	if got := s.Scheduler().Budget(); got != (Budget{2, 512}) || got != LoadBudget(s.prefsPath) {
		t.Fatalf("scheduler %v, saved %v", got, LoadBudget(s.prefsPath))
	}
}
