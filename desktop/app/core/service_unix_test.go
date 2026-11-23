//go:build !windows

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestServiceCancelKillsProcessTree cancels a running task whose child
// (python) started a grandchild (python), checks both are gone and the
// cancellation is registered, then resumes it as a real validate task.
func TestServiceCancelKillsProcessTree(t *testing.T) {
	s := newTestService(t)
	project := createProject(t, s)
	python := testPython(t)
	sleeper := `import os, subprocess, sys, time
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
open("pids.tmp", "w").write("%d %d" % (os.getpid(), child.pid))
os.replace("pids.tmp", "pids")
time.sleep(60)`
	s.taskCommand = func(rt *Runtime, task *Prepared) *exec.Cmd {
		cmd := exec.Command(python, "-c", sleeper)
		cmd.Dir = task.RunDir
		cmd.Env = append(os.Environ(), rt.ComputeEnv...)
		return cmd
	}
	snap, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "validate_project"})
	if err != nil || snap["status"] != "running" {
		t.Fatalf("%v %v", snap, err)
	}
	id := snap["task_id"].(string)
	pids := readPids(t, filepath.Join(project, "runs", id, "pids"), 2)
	if snap, err := s.CancelTask(project, id); err != nil || snap["status"] != "cancelling" {
		t.Fatalf("%v %v", snap, err)
	}
	assertGone(t, pids)
	if snap := waitTask(t, s, project, id, 10*time.Second); snap["status"] != "cancelled" {
		t.Fatalf("%v", snap)
	}
	waitRun(t, project, id, "cancelled")

	// Resume runs the stored request with the real bridge under a new id.
	s.taskCommand = NewService(nil, ServiceOptions{}).taskCommand
	resumed, err := s.ResumeTask(project, id)
	if err != nil {
		t.Fatal(err)
	}
	newID := resumed["task_id"].(string)
	if newID == id || resumed["resumed_from"] != id {
		t.Fatalf("%v", resumed)
	}
	if snap := waitTask(t, s, project, newID, 60*time.Second); snap["status"] != "succeeded" {
		t.Fatalf("%v", snap)
	}
	waitRun(t, project, newID, "succeeded")
}

func TestCancelThenShutdownRegistersRun(t *testing.T) {
	s := newTestService(t)
	project := createProject(t, s)
	s.taskCommand = func(_ *Runtime, _ *Prepared) *exec.Cmd {
		return exec.Command(testPython(t), "-c", "import time; time.sleep(60)")
	}
	snap, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "validate_project"})
	if err != nil {
		t.Fatal(err)
	}
	id := snap["task_id"].(string)
	// Delay the real bridge's register_task_run, not the Go process monitor.
	lock, err := tryFileLock(filepath.Join(project, "project.json.lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	if _, err := s.CancelTask(project, id); err != nil {
		t.Fatal(err)
	}
	if task := waitTask(t, s, project, id, 5*time.Second); task["status"] != "cancelled" {
		t.Fatalf("task: %v", task)
	}
	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown returned before project.json could be updated")
	case <-time.After(200 * time.Millisecond):
	}
	lock.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish after releasing the manifest")
	}
	runs := readJSON(t, filepath.Join(project, "project.json"))["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["id"] != id || runs[0].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("persisted runs at shutdown: %v", runs)
	}
}
