//go:build unix

package core

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func plantRun(t *testing.T, status string) (project, runDir string) {
	project = tempProject(t)
	runDir = filepath.Join(project, "runs", "x1")
	os.MkdirAll(runDir, 0o755)
	os.WriteFile(filepath.Join(runDir, "status.json"), []byte(`{"status":"`+status+`","kind":"cplfold"}`), 0o644)
	return
}

func within(t *testing.T, d time.Duration, what string, f func()) {
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s hung for %s", what, d)
	}
}

// A copied project with a "running" task and stdout.log as a FIFO: List
// marks it interrupted and reads stdout.log with plain os.ReadFile.
func TestListDoesNotHangOnFifoStdoutLog(t *testing.T) {
	project, runDir := plantRun(t, "running")
	if err := syscall.Mkfifo(filepath.Join(runDir, "stdout.log"), 0o644); err != nil {
		t.Skip(err)
	}
	s := testScheduler(t, 0, 0)
	within(t, 3*time.Second, "Scheduler.List", func() { _, _ = s.List(project) })
}

func TestResumeDoesNotHangOnFifoStartFile(t *testing.T) {
	project, runDir := plantRun(t, "failed")
	if err := syscall.Mkfifo(filepath.Join(runDir, StartFile), 0o644); err != nil {
		t.Skip(err)
	}
	s := testScheduler(t, 0, 0)
	within(t, 3*time.Second, "Scheduler.ResumeRequest", func() { _, _ = s.ResumeRequest("x1", project) })
}
