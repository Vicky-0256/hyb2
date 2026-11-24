//go:build !windows

package core

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real bridge registration failure must not lose a successful child's
// outputs. Only the computation is synthetic; chmod makes the actual
// project.json transaction fail on the host filesystem.
func TestServiceRecoversRegistrationAfterPermissionFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	s := newTestService(t)
	project := createProject(t, s)
	t.Cleanup(func() { _ = os.Chmod(project, 0o755) })
	python := testPython(t)
	s.taskCommand = func(_ *Runtime, task *Prepared) *exec.Cmd {
		return exec.Command(python, "-c", `
import json, os, pathlib, sys
project, run, task_id = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), sys.argv[3]
out = project / "structures" / task_id
out.mkdir(parents=True)
(out / "structure.dbn").write_text(">synthetic\nACGUACGU\n((....))\n")
with (project / "child-invocations").open("a") as handle:
    handle.write(task_id + "\n")
result = {"ok": True, "structure": {"sample_ids": ["sampleA"], "gene_id": "synthetic",
          "mode": "genome", "paths": {"dbn": "structures/" + task_id + "/structure.dbn"}}}
(run / "result.json").write_text(json.dumps(result))
os.chmod(project, 0o555)
`, project, task.RunDir, task.ID)
	}
	snap, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "structure",
		SampleIDs: []string{"sampleA"}, GeneID: ptr("synthetic"), Mode: ptr("genome")})
	if err != nil {
		t.Fatal(err)
	}
	id := snap["task_id"].(string)
	if snap = waitTask(t, s, project, id, 10*time.Second); snap["status"] != "succeeded" {
		t.Fatalf("synthetic child: %v", snap)
	}
	waitRegistration(t, s, project, id, "failed")
	s.Shutdown()
	snap, err = s.TaskStatus(project, id)
	if err != nil {
		t.Fatal(err)
	}
	manifest := readJSON(t, filepath.Join(project, "project.json"))
	t.Logf("before reopen: task=%s registration=%v registration_error=%v runs=%d structures=%d",
		id, snap["registration_status"], snap["registration_error"], len(manifest["runs"].([]any)), len(manifest["structures"].([]any)))
	if len(manifest["structures"].([]any)) != 0 {
		t.Fatal("permission fault did not prevent registration")
	}
	if snap["registration_status"] != "failed" || snap["registration_error"] == nil {
		t.Errorf("registration failure is not durable and visible: %v", snap)
	}
	resultFile := filepath.Join(project, "runs", id, "result.json")
	before, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(project, 0o755); err != nil {
		t.Fatal(err)
	}
	reopened := newTestService(t)
	reopened.taskCommand = func(_ *Runtime, _ *Prepared) *exec.Cmd {
		t.Error("registration recovery attempted to recompute the task")
		return exec.Command("/usr/bin/false")
	}
	for attempt := range 2 {
		opened, err := reopened.OpenProject(project)
		if err != nil {
			t.Fatal(err)
		}
		manifest := opened["manifest"].(map[string]any)
		tasks, err := reopened.ListTasks(project)
		if err != nil || len(tasks) != 1 {
			t.Fatalf("tasks: %v, %v", tasks, err)
		}
		t.Logf("reopen %d: status=%v registration=%v runs=%d structures=%d", attempt,
			tasks[0]["status"], tasks[0]["registration_status"], len(manifest["runs"].([]any)), len(manifest["structures"].([]any)))
		if len(manifest["runs"].([]any)) != 1 || len(manifest["structures"].([]any)) != 1 {
			t.Errorf("reopening did not idempotently recover the result: %v", manifest)
			// On the unfixed implementation, prove the output is recoverable
			// by registration alone, not by rerunning the synthetic child.
			if _, err := reopened.call(Control, Obj{"operation": "register_task_run", "path": project, "task_id": id}); err != nil {
				t.Fatal(err)
			}
			t.Logf("manual registration-only control: structures=%d", len(readJSON(t, filepath.Join(project, "project.json"))["structures"].([]any)))
		}
		if tasks[0]["status"] != "succeeded" || tasks[0]["registration_status"] != "registered" || tasks[0]["registration_error"] != nil {
			t.Errorf("recovered snapshot: %v", tasks[0])
		}
	}
	after, err := os.ReadFile(resultFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("recovery changed the result: %v", err)
	}
	invocations, err := os.ReadFile(filepath.Join(project, "child-invocations"))
	if err != nil || string(invocations) != id+"\n" {
		t.Fatalf("child invocation count changed: %q %v", invocations, err)
	}
}

func TestServiceRegistrationTracksTerminalPaths(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "launch-failure", "running-cancel", "queued-cancel", "running-shutdown", "queued-shutdown"} {
		t.Run(outcome, func(t *testing.T) {
			s := newTestService(t)
			project := createProject(t, s)
			want := "succeeded"
			if strings.HasPrefix(outcome, "queued-") {
				s.tasks.SetBudget(Budget{})
			}
			if outcome != "success" {
				s.taskCommand = func(_ *Runtime, task *Prepared) *exec.Cmd {
					if outcome == "launch-failure" {
						return exec.Command(filepath.Join(task.RunDir, "missing-engine"))
					}
					if outcome == "failure" {
						return shell(task.RunDir, "exit 2")
					}
					return shell(task.RunDir, "exec sleep 60")
				}
			}
			snap, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "validate_project"})
			if err != nil {
				t.Fatal(err)
			}
			id := snap["task_id"].(string)
			switch {
			case outcome == "failure" || outcome == "launch-failure":
				want = "failed"
			case strings.HasSuffix(outcome, "-cancel"):
				want = "cancelled"
				if _, err := s.CancelTask(project, id); err != nil {
					t.Fatal(err)
				}
			case strings.HasSuffix(outcome, "-shutdown"):
				want = "interrupted"
				s.Shutdown()
			}
			if snap := waitTask(t, s, project, id, 10*time.Second); snap["status"] != want {
				t.Fatalf("task outcome: %v, want %s", snap, want)
			}
			if strings.HasSuffix(outcome, "-shutdown") {
				// Shutdown leaves the receipt pending; the next open registers it.
				if snap, err := s.TaskStatus(project, id); err != nil || snap["registration_status"] != "pending" {
					t.Fatalf("registration at shutdown: %v, %v", snap, err)
				}
				if _, err := newTestService(t).OpenProject(project); err != nil {
					t.Fatal(err)
				}
			} else {
				waitRegistration(t, s, project, id, "registered")
				s.Shutdown()
			}
			snap, err = s.TaskStatus(project, id)
			if err != nil || snap["registration_status"] != "registered" || snap["registration_error"] != nil {
				t.Fatalf("registration: %v, %v", snap, err)
			}
			waitRun(t, project, id, want)
			if receipt := readJSON(t, filepath.Join(project, "runs", id, registrationFile)); receipt["status"] != "registered" || receipt["error"] != nil {
				t.Fatalf("registration receipt: %v", receipt)
			}
		})
	}
}

func TestServiceSucceededRegistrationStaysPendingUntilCommit(t *testing.T) {
	s := newTestService(t)
	project := createProject(t, s)
	lock, err := tryFileLock(filepath.Join(project, "project.json.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	snap, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "validate_project"})
	if err != nil {
		t.Fatal(err)
	}
	id := snap["task_id"].(string)
	if snap = waitTask(t, s, project, id, 10*time.Second); snap["status"] != "succeeded" || snap["registration_status"] != "pending" {
		t.Fatalf("execution and registration were not separated: %v", snap)
	}
	if receipt := readJSON(t, filepath.Join(project, "runs", id, registrationFile)); receipt["status"] != "pending" {
		t.Fatalf("pending intent was not persisted: %v", receipt)
	}
	if runs := readJSON(t, filepath.Join(project, "project.json"))["runs"].([]any); len(runs) != 0 {
		t.Fatalf("fault did not block manifest commit: %v", runs)
	}
	waitRegistering(t, filepath.Join(project, "runs", id))
	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown did not wait for the registration under way")
	case <-time.After(200 * time.Millisecond):
	}
	lock.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish after manifest unlock")
	}
	if snap, err := s.TaskStatus(project, id); err != nil || snap["registration_status"] != "registered" {
		t.Fatalf("committed registration: %v, %v", snap, err)
	}
	waitRun(t, project, id, "succeeded")
}

// Missing receipts recover old versions' lost registrations; pending
// receipts also cover a crash after manifest commit but before its ack.
func TestServiceRegistrationRecoveryIsIdempotentAcrossInstances(t *testing.T) {
	for _, receipt := range []string{"missing", "pending", "failed"} {
		t.Run(receipt, func(t *testing.T) {
			s := newTestService(t)
			project := createProject(t, s)
			snap, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "validate_project"})
			if err != nil {
				t.Fatal(err)
			}
			id := snap["task_id"].(string)
			if snap = waitTask(t, s, project, id, 10*time.Second); snap["status"] != "succeeded" {
				t.Fatal(snap)
			}
			s.Shutdown()
			runDir := filepath.Join(project, "runs", id)
			if receipt == "missing" {
				if err := os.Remove(filepath.Join(runDir, registrationFile)); err != nil {
					t.Fatal(err)
				}
				manifest := readJSON(t, filepath.Join(project, "project.json"))
				manifest["runs"] = []any{}
				if err := writeAtomic(project, "project.json", manifest); err != nil {
					t.Fatal(err)
				}
			} else if err := writeAtomic(runDir, registrationFile, taskRegistration{Status: receipt}); err != nil {
				t.Fatal(err)
			}
			first, second := newTestService(t), newTestService(t)
			barrier, done := make(chan struct{}), make(chan error, 2)
			for _, service := range []*Service{first, second} {
				go func() { <-barrier; _, err := service.OpenProject(project); done <- err }()
			}
			close(barrier)
			for range 2 {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			runs := readJSON(t, filepath.Join(project, "project.json"))["runs"].([]any)
			if len(runs) != 1 || runs[0].(map[string]any)["id"] != id {
				t.Fatalf("concurrent recovery duplicated/lost the run: %v", runs)
			}
			// Even the original Service must see another instance's repair,
			// not a stale copy in its in-memory task table.
			for _, service := range []*Service{s, first, second} {
				if snap, err := service.TaskStatus(project, id); err != nil || snap["registration_status"] != "registered" {
					t.Fatalf("recovered receipt: %v, %v", snap, err)
				}
			}
		})
	}
}

// waitRegistration polls until the task's registration_status is want.
func waitRegistration(t *testing.T, s *Service, project, id, want string) {
	t.Helper()
	var snap Obj
	for range 200 {
		var err error
		if snap, err = s.TaskStatus(project, id); err != nil {
			t.Fatal(err)
		}
		if snap["registration_status"] == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("registration of %s: %v, want %s", id, snap["registration_status"], want)
}

// waitRegistering polls until a registerTask call holds runDir's
// registration lock (its register_task_run is under way).
func waitRegistering(t *testing.T, runDir string) {
	t.Helper()
	for range 200 {
		lock, err := tryFileLock(filepath.Join(runDir, "registration.lock"))
		if errors.Is(err, errLockBusy) {
			return
		}
		if err == nil {
			lock.Close()
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("registration did not start")
}

// Quitting does not wait for register_task_run (a slow control lane would
// freeze the UI): interrupted tasks keep their pending receipt, and the next
// open registers them, so nothing is lost.
func TestShutdownLeavesRegistrationToTheNextOpen(t *testing.T) {
	s := newTestService(t)
	project := createProject(t, s)
	s.tasks.SetBudget(Budget{1, 1000})
	s.taskCommand = func(_ *Runtime, task *Prepared) *exec.Cmd {
		return shell(task.RunDir, "exec sleep 60")
	}
	var ids []string
	for range 2 { // one running, one queued
		snap, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "validate_project", CPU: ptr(uint32(1))})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, snap["task_id"].(string))
	}
	// A busy manifest: register_task_run would block until it is free.
	lock, err := tryFileLock(filepath.Join(project, "project.json.lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	done := make(chan struct{})
	start := time.Now()
	go func() { s.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		lock.Close()
		<-done
		t.Fatalf("shutdown waited %v for register_task_run", time.Since(start).Round(time.Millisecond))
	}
	t.Logf("shutdown took %v", time.Since(start).Round(time.Millisecond))
	lock.Close()
	for _, id := range ids {
		if status := readJSON(t, filepath.Join(project, "runs", id, "status.json"))["status"]; status != "interrupted" {
			t.Fatalf("%s status %v", id, status)
		}
		if receipt := readJSON(t, filepath.Join(project, "runs", id, registrationFile)); receipt["status"] != "pending" {
			t.Fatalf("%s receipt %v", id, receipt)
		}
	}
	reopened := newTestService(t)
	if _, err := reopened.OpenProject(project); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		waitRun(t, project, id, "interrupted")
		if snap, err := reopened.TaskStatus(project, id); err != nil || snap["registration_status"] != "registered" {
			t.Fatalf("recovered %s: %v, %v", id, snap, err)
		}
	}
}
