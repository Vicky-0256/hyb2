package views

import (
	"strings"
	"testing"
	"unicode/utf8"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// TestStoreTaskIgnoresOlderSnapshots: a poll reply read before a cancel
// (running) does not undo it, a finished task never returns to running,
// and the finish hooks run once.
func TestStoreTaskIgnoresOlderSnapshots(t *testing.T) {
	a, f := openFixture(t)
	saved := taskFinishedHooks
	t.Cleanup(func() { taskFinishedHooks = saved })
	finished := 0
	OnTaskFinished(func(*App, model.Task) { finished++ })
	listed := func() int { return strings.Count(strings.Join(f.calls, ","), "op:list_samples") }
	before := listed()

	id := "sample-1"
	a.storeTask(model.Task{TaskID: id, Kind: "sample_pipeline", Status: "running"})
	a.storeTask(model.Task{TaskID: id, Kind: "sample_pipeline", Status: "cancelling"})
	a.storeTask(model.Task{TaskID: id, Kind: "sample_pipeline", Status: "running"}) // stale poll reply
	if got := a.Tasks[id].Status; got != "cancelling" {
		t.Fatalf("status %q after a stale running snapshot", got)
	}
	if !a.Tasks[id].Active() {
		t.Fatal("a cancelling task must stay active (polled)")
	}
	a.storeTask(model.Task{TaskID: id, Kind: "sample_pipeline", Status: "cancelled", FinishedAt: "2026-10-14T10:00:00Z"})
	a.storeTask(model.Task{TaskID: id, Kind: "sample_pipeline", Status: "running"})
	a.storeTask(model.Task{TaskID: id, Kind: "sample_pipeline", Status: "cancelling"})
	if got := a.Tasks[id].Status; got != "cancelled" {
		t.Fatalf("status %q after stale snapshots of a finished task", got)
	}
	waitFlush(a)
	if finished != 1 {
		t.Fatalf("finish hooks ran %d times", finished)
	}
	if n := listed() - before; n != 1 {
		t.Fatalf("project refreshed %d times", n)
	}
}

// TestRefreshTasksRelistsStaleSamples: when the task list shows a task a
// sample stage still calls running has finished, the samples are read again.
func TestRefreshTasksRelistsStaleSamples(t *testing.T) {
	a, f := openFixture(t)
	a.Project.Samples[0].Stages = map[string]model.Stage{"map": {Status: "running", TaskID: "sample-old"}}
	f.mu.Lock()
	f.tasks = append(f.tasks, core.Obj{"task_id": "sample-old", "kind": "sample_pipeline", "status": "interrupted"})
	f.mu.Unlock()
	listed := func() int { return strings.Count(strings.Join(f.calls, ","), "op:list_samples") }
	before := listed()
	a.RefreshTasks()
	waitFlush(a)
	if listed() == before {
		t.Fatal("samples not listed again after their task was found interrupted")
	}
}

// TestPendingFolderBelongsToItsProject: a data folder chosen with a new
// project is dropped when that Open fails, is replaced, or the project
// closes; it is never scanned into another project.
func TestPendingFolderBelongsToItsProject(t *testing.T) {
	f := newFake(t)
	a := New(f, nil, "")
	a.pendingFolder = pendingFolder{"/missing", "/data/zika_files"}
	a.Open("/missing")
	waitFlush(a)
	if a.pendingFolder != (pendingFolder{}) {
		t.Fatalf("failed open kept %+v", a.pendingFolder)
	}
	a.pendingFolder = pendingFolder{"/projects/new.hyb2proj", "/data/zika_files"}
	a.Open("/projects/zikv.hyb2proj") // supersedes the new project's Open
	waitFlush(a)
	if a.pendingFolder != (pendingFolder{}) {
		t.Fatalf("another project's open kept %+v", a.pendingFolder)
	}
	a.pendingFolder = pendingFolder{a.Project.Path, "/data/zika_files"}
	a.Close()
	if a.pendingFolder != (pendingFolder{}) {
		t.Fatalf("close kept %+v", a.pendingFolder)
	}
	if strings.Contains(strings.Join(f.calls, ","), "op:scan_data_folder") {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestTruncateKeepsCharacters(t *testing.T) {
	s := strings.Repeat("é", 10) // 2 bytes each
	for n := 0; n < len(s); n++ {
		got := truncate(s, n)
		if !utf8.ValidString(got) || len(strings.TrimSuffix(got, "…")) > n {
			t.Fatalf("truncate(%d) = %q", n, got)
		}
	}
	if truncate("abc", 3) != "abc" || truncate("abcd", 3) != "abc…" {
		t.Fatal("ASCII truncation changed")
	}
}
