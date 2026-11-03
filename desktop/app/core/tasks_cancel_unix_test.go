//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCancelOutcomeNeverChanges: Cancel answers "cancelling" (active, not
// finished) while the child may still finish; the first finished snapshot
// is the final one, whether cancelled or succeeded.
func TestCancelOutcomeNeverChanges(t *testing.T) {
	s := testScheduler(t, 0, 0)
	project := tempProject(t)
	for i := range 100 {
		id := NewTaskID()
		root, runDir, err := s.prepareRunDir(project, id)
		if err != nil {
			t.Fatal(err)
		}
		cmd := shell(runDir, `printf '{"ok":true}' > result.json`)
		if _, err := startManaged(s, cmd, root, id, "validate_project", runDir, nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(i%10) * time.Millisecond)
		snap, err := s.Cancel(id, "")
		if err != nil {
			t.Fatal(err)
		}
		if snap["status"] == "cancelled" {
			t.Fatalf("run %d: cancelled before the child was reaped: %v", i, snap)
		}
		if snap["status"] == "cancelling" && finished(snap) {
			t.Fatalf("run %d: cancelling task has finished_at: %v", i, snap)
		}
		first := waitFor(t, s, id, project, finished)
		time.Sleep(5 * time.Millisecond)
		again, _ := s.Status(id, project)
		if first["status"] != again["status"] {
			t.Fatalf("run %d: finished %v, then %v", i, first["status"], again["status"])
		}
		if first["status"] != "cancelled" && first["status"] != "succeeded" {
			t.Fatalf("run %d: %v", i, first["status"])
		}
	}
}

// TestShutdownKeepsUserCancel: a task being cancelled when the app exits
// ends cancelled, not interrupted.
func TestShutdownKeepsUserCancel(t *testing.T) {
	s := testScheduler(t, 0, 0)
	project := tempProject(t)
	id, _ := submitSleeper(t, s, project, Budget{1, 100}, nil)
	message := "cancelled by user"
	s.mu.Lock()
	// As Cancel leaves it until the monitor reaps the child.
	s.tasks[id].status, s.tasks[id].err = "cancelling", &message
	s.mu.Unlock()
	s.ShutdownAll()
	snap, _ := s.Status(id, project)
	if snap["status"] != "cancelled" || !finished(snap) {
		t.Fatalf("%v", snap)
	}
	if readJSON(t, filepath.Join(project, "runs", id, "status.json"))["status"] != "cancelled" {
		t.Fatal("cancelled status not persisted")
	}
}

// TestShutdownWaitsOutsideTheLock: children that are slow to be reaped are
// waited for together, and status reads answer meanwhile.
func TestShutdownWaitsOutsideTheLock(t *testing.T) {
	s := testScheduler(t, 4, 1000)
	project := tempProject(t)
	var ids []string
	for range 2 {
		id, _ := submitSleeper(t, s, project, Budget{1, 100}, nil)
		ids = append(ids, id)
	}
	s.mu.Lock()
	for _, id := range ids {
		s.tasks[id].exited = make(chan struct{}) // never reaped
	}
	s.mu.Unlock()
	done := make(chan struct{})
	start := time.Now()
	go func() { s.ShutdownAll(); close(done) }()
	time.Sleep(200 * time.Millisecond)
	answered := make(chan struct{})
	go func() { _, _ = s.Status(ids[0], project); close(answered) }()
	select {
	case <-answered:
	case <-time.After(time.Second):
		t.Fatal("status blocked while shutdown waits for children")
	}
	<-done
	if took := time.Since(start); took > 8*time.Second {
		t.Fatalf("shutdown took %v (waits per task)", took)
	}
	for _, id := range ids {
		if snap, _ := s.Status(id, project); snap["status"] != "interrupted" {
			t.Fatalf("%s %v", id, snap["status"])
		}
	}
}

// TestFailedSubmitRemovesRunDir: a task that cannot be registered leaves
// no runs/<id> behind.
func TestFailedSubmitRemovesRunDir(t *testing.T) {
	s := testScheduler(t, 0, 0)
	project := tempProject(t)
	id := NewTaskID()
	root, runDir, err := s.prepareRunDir(project, id)
	if err != nil {
		t.Fatal(err)
	}
	// The owner lock cannot be taken on a directory.
	if err := os.Mkdir(filepath.Join(runDir, ownerLockFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := startManaged(s, shell(runDir, "true"), root, id, "validate_project", runDir, nil); err == nil {
		t.Fatal("submit succeeded")
	}
	if _, err := os.Lstat(runDir); !os.IsNotExist(err) {
		t.Fatalf("run directory left behind: %v", err)
	}
}

// TestDeadCancellingRecordIsCancelled: a record left "cancelling" by an
// exited owner reads as cancelled.
func TestDeadCancellingRecordIsCancelled(t *testing.T) {
	project := tempProject(t)
	runDir := filepath.Join(project, "runs", "task-x")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeStatus(runDir, Obj{"status": "cancelling", "error": "cancelled by user"}); err != nil {
		t.Fatal(err)
	}
	snap, err := readDisk("task-x", runDir)
	if err != nil || snap["status"] != "cancelled" {
		t.Fatalf("%v %v", snap, err)
	}
}
