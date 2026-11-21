package views

import (
	"errors"
	"sync/atomic"
	"testing"

	"hyb2desktop/core"
)

// slowPrefsFake holds each preference save until gate is closed, and can
// make it fail, to check what the Preferences dialog does meanwhile.
type slowPrefsFake struct {
	*researchFake
	gate chan struct{}
	fail bool
	// readFail makes Preferences fail too (the stored values are unknown).
	readFail atomic.Bool
}

func (f *slowPrefsFake) Preferences() (core.Obj, error) {
	if f.readFail.Load() {
		return nil, errors.New("cannot read preferences")
	}
	return f.researchFake.Preferences()
}

func (f *slowPrefsFake) SetPreferences(patch core.Obj) (core.Obj, error) {
	<-f.gate
	if f.fail {
		return nil, errors.New("disk full")
	}
	return f.researchFake.SetPreferences(patch)
}

func openLookupNotice(t *testing.T, fail bool) (*App, *slowPrefsFake, func()) {
	t.Helper()
	a, rf, tt, _ := openResearch(t, false)
	f := &slowPrefsFake{researchFake: rf, gate: make(chan struct{}), fail: fail}
	a.B = f
	mustClick(t, tt, "Turn on in Preferences…")
	rsSettle(a, tt)
	mustClick(t, tt, "Allow external public-data lookups (RNAcentral)")
	rsSettle(a, tt)
	mustClick(t, tt, "Turn on")
	tt.Frame()
	tt.Frame()
	// The save is still held: Test connection reads the stored preference,
	// so it must not run yet.
	if a.prefs.saving != 1 {
		t.Fatalf("saving = %d while the save is held", a.prefs.saving)
	}
	_ = tt.Click("Test connection")
	tt.Frame()
	if a.prefs.testing || a.prefs.testMsg != "" {
		t.Fatalf("Test connection ran before the preference was stored: testing %v, %q", a.prefs.testing, a.prefs.testMsg)
	}
	return a, f, func() {
		close(f.gate)
		rsSettle(a, tt)
		if a.prefs.saving != 0 {
			t.Fatalf("saving = %d after the save returned", a.prefs.saving)
		}
		if fail {
			return
		}
		mustClick(t, tt, "Test connection")
		rsSettle(a, tt)
		mustHave(t, tt, "This does not contact RNAcentral; the first lookup does.")
	}
}

// Test connection waits for a lookup preference that is still being saved.
func TestLookupTestWaitsForTheSave(t *testing.T) {
	a, f, release := openLookupNotice(t, false)
	release()
	if !f.lookup || !a.prefs.lookup || a.prefs.testErr {
		t.Fatalf("lookups not on after the save: fake %v prefs %+v", f.lookup, a.prefs)
	}
}

// A lookup switch that could not be stored goes back to off.
func TestLookupSwitchFollowsAFailedSave(t *testing.T) {
	a, f, release := openLookupNotice(t, true)
	release()
	if f.lookup || a.prefs.lookup {
		t.Fatalf("switch shows on although nothing was stored: fake %v prefs.lookup %v", f.lookup, a.prefs.lookup)
	}
}

// Two quick lookup changes are stored in the order they were made, and the
// switch shows the last one.
func TestQuickLookupChangesKeepTheirOrder(t *testing.T) {
	for i := 0; i < 20; i++ {
		a, f, _ := openLookupNotice(t, false)
		a.savePrefs(core.Obj{"external_lookup": false})
		a.savePrefs(core.Obj{"external_lookup": true})
		a.savePrefs(core.Obj{"external_lookup": false})
		close(f.gate)
		for n := 0; a.prefs.saving > 0 && n < 50; n++ {
			waitFlush(a)
		}
		if f.lookup || a.prefs.lookup {
			t.Fatalf("run %d: last change was off, stored %v, switch shows %v", i, f.lookup, a.prefs.lookup)
		}
	}
}

// After failed saves the switch shows what is stored, not the opposite of
// the last change: on (fails) then off (fails) leaves it off.
func TestFailedSavesShowTheStoredLookup(t *testing.T) {
	a, f, _ := openLookupNotice(t, true)
	a.savePrefs(core.Obj{"external_lookup": false})
	close(f.gate)
	for n := 0; a.prefs.saving > 0 && n < 50; n++ {
		waitFlush(a)
	}
	if f.lookup || a.prefs.lookup {
		t.Fatalf("nothing was stored, yet the switch shows %v (stored %v)", a.prefs.lookup, f.lookup)
	}
}

// Closing and reopening Preferences while a save is held keeps the save
// chain: the counters end at zero and the dialog shows the stored value.
func TestReopenedPreferencesKeepThePendingSave(t *testing.T) {
	a, f, _ := openLookupNotice(t, false)
	a.prefsOpen = false
	a.openPrefs()
	if a.prefs.saving != 1 || a.prefs.lastSave == nil {
		t.Fatalf("reopening dropped the pending save: %+v", a.prefs)
	}
	a.savePrefs(core.Obj{"external_lookup": false})
	close(f.gate)
	for n := 0; (a.prefs.saving > 0 || a.prefs.loading) && n < 50; n++ {
		waitFlush(a)
	}
	if a.prefs.saving != 0 || f.lookup || a.prefs.lookup {
		t.Fatalf("saving %d, stored %v, switch %v", a.prefs.saving, f.lookup, a.prefs.lookup)
	}
}

// A failed save whose stored values cannot be read either puts every
// patched key back to the last stored preferences: the budget too, not
// only the lookup switch.
func TestFailedSaveUnreadableRestoresEveryPatchedKey(t *testing.T) {
	a, f, _ := openLookupNotice(t, true)
	f.readFail.Store(true)
	a.prefs.cpu, a.prefs.memMB = 3, 2048 // edited, then Save
	a.savePrefs(core.Obj{"budget": core.Obj{"cpu": 3, "mem_mb": 2048}, "external_lookup": true})
	close(f.gate)
	for n := 0; a.prefs.saving > 0 && n < 50; n++ {
		waitFlush(a)
	}
	if a.prefs.cpu != 10 || a.prefs.memMB != 24576 || a.prefs.custom || a.prefs.lookup || a.lookup.on {
		t.Fatalf("dialog keeps unsaved values: cpu %v mem %v custom %v lookup %v/%v",
			a.prefs.cpu, a.prefs.memMB, a.prefs.custom, a.prefs.lookup, a.lookup.on)
	}
}
