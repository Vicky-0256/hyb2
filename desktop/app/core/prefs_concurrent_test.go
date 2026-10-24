package core

import (
	"path/filepath"
	"sync"
	"testing"
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
