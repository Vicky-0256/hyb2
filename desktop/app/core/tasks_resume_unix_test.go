//go:build !windows

package core

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func failedResumeSource(t *testing.T, project string) *Prepared {
	t.Helper()
	s := testScheduler(t, 1, 1000)
	p, err := s.Prepare(TaskStartRequest{ProjectPath: project, TaskID: ptr("original"), Kind: "sample_pipeline", SampleID: ptr("sampleA")}, project, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(shell(p.RunDir, "exit 2"), p, nil); err != nil {
		t.Fatal(err)
	}
	if snap := waitFor(t, s, p.ID, project, finished); snap["status"] != "failed" {
		t.Fatal(snap)
	}
	return p
}

func prepareResume(t *testing.T, s *Scheduler, project, from string) *Prepared {
	t.Helper()
	request, err := s.ResumeRequest(from, project)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Prepare(request, project, &from)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Both callers pass Prepare before the submission barrier. A scan during
// Prepare cannot prevent these two independently owned schedulers racing.
func TestResumeAdmissionIsAtomicAcrossInstances(t *testing.T) {
	project := tempProject(t)
	from := failedResumeSource(t, project).ID
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	first, second := testScheduler(t, 1, 1000), testScheduler(t, 1, 1000)
	schedulers := []*Scheduler{first, second}
	prepared := []*Prepared{prepareResume(t, first, project, from), prepareResume(t, second, alias, from)}
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for i, s := range schedulers {
		p := prepared[i]
		go func() {
			<-barrier
			_, err := s.Submit(shell(p.RunDir, "exec sleep 60"), p, nil)
			results <- err
		}()
	}
	close(barrier)
	accepted := 0
	for range schedulers {
		if err := <-results; err == nil {
			accepted++
		} else if !strings.Contains(err.Error(), "already") {
			t.Errorf("unexpected rejection: %v", err)
		}
	}
	t.Logf("simultaneous independent-instance resumes admitted=%d, want 1", accepted)
	if accepted != 1 {
		t.Fatalf("duplicate work admitted for one original task: %d", accepted)
	}
	entries, err := os.ReadDir(filepath.Join(project, "runs"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("losing admission left an orphan run: %v, %v", entries, err)
	}
}

func waitLifecycleFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if isRegularFile(path) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// Each helper is a genuinely independent process with its own Service and
// Scheduler. Files are barriers only; admission is the production Submit.
func TestResumeProcessHelper(t *testing.T) {
	control := os.Getenv("HYB2_TEST_RESUME_CONTROL")
	if control == "" {
		return
	}
	project, role := os.Getenv("HYB2_TEST_RESUME_PROJECT"), os.Getenv("HYB2_TEST_RESUME_ROLE")
	s := NewService(nil, ServiceOptions{})
	defer s.Shutdown()
	s.tasks.SetBudget(Budget{1, 1000})
	if os.Getenv("HYB2_TEST_RESUME_QUEUED") == "1" {
		s.tasks.SetBudget(Budget{}) // no CPU available: no child can start
	}
	p := prepareResume(t, s.tasks, project, "original")
	if err := os.WriteFile(filepath.Join(control, "ready-"+role), []byte(p.ID), 0o644); err != nil {
		t.Fatal(err)
	}
	waitLifecycleFile(t, filepath.Join(control, "submit"))
	cmd := shell(p.RunDir, `echo $$ > child-pid; exec sleep 60`)
	snap, err := s.tasks.Submit(cmd, p, nil)
	answer := Obj{"accepted": err == nil, "pid": os.Getpid(), "id": p.ID, "snapshot": snap}
	if err != nil {
		answer["error"] = err.Error()
	}
	if err := writeAtomic(control, "submitted-"+role, answer); err != nil {
		t.Fatal(err)
	}
	waitLifecycleFile(t, filepath.Join(control, "stop"))
}

type resumeProcess struct {
	cmd    *exec.Cmd
	done   chan error
	output bytes.Buffer
}

func startResumeProcess(t *testing.T, project, control, role string, queued bool) *resumeProcess {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &resumeProcess{cmd: exec.Command(binary, "-test.run=^TestResumeProcessHelper$", "-test.v"), done: make(chan error, 1)}
	p.cmd.Env = append(os.Environ(), "HYB2_TEST_RESUME_PROJECT="+project, "HYB2_TEST_RESUME_CONTROL="+control,
		"HYB2_TEST_RESUME_ROLE="+role, "HYB2_TEST_RESUME_QUEUED="+strconv.Itoa(map[bool]int{false: 0, true: 1}[queued]))
	p.cmd.Stdout, p.cmd.Stderr = &p.output, &p.output
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(control, "stop"), nil, 0o644)
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
			t.Error("resume helper did not stop")
		}
		t.Logf("resume helper %s: %s", role, p.output.String())
	})
	return p
}

func TestResumeAdmissionIsAtomicAcrossProcesses(t *testing.T) {
	project := tempProject(t)
	failedResumeSource(t, project)
	control := t.TempDir()
	first := startResumeProcess(t, project, control, "one", false)
	second := startResumeProcess(t, project, control, "two", false)
	for _, role := range []string{"one", "two"} {
		waitLifecycleFile(t, filepath.Join(control, "ready-"+role))
	}
	if err := os.WriteFile(filepath.Join(control, "submit"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, role := range []string{"one", "two"} {
		path := filepath.Join(control, "submitted-"+role)
		waitLifecycleFile(t, path)
		answer := readJSON(t, path)
		t.Logf("independent process %s: %v", role, answer)
		if answer["accepted"] == true {
			accepted++
			waitLifecycleFile(t, filepath.Join(project, "runs", answer["id"].(string), "child-pid"))
		} else if message, _ := answer["error"].(string); !strings.Contains(message, "already") {
			t.Errorf("unexpected rejection: %v", answer)
		}
	}
	if first.cmd.Process.Pid == second.cmd.Process.Pid || accepted != 1 {
		t.Fatalf("two processes admitted %d resumes, want exactly 1", accepted)
	}
}

func TestResumeClaimReleasedOnTerminalPaths(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "launch-failure", "owner-lock-failure", "status-write-failure",
		"queued-cancel", "running-cancel", "queued-shutdown", "running-shutdown"} {
		t.Run(outcome, func(t *testing.T) {
			project := tempProject(t)
			source := failedResumeSource(t, project)
			s := testScheduler(t, 1, 1000)
			p := prepareResume(t, s, project, source.ID)
			cmd := shell(p.RunDir, "exec sleep 60")
			switch outcome {
			case "success":
				cmd = shell(p.RunDir, "exit 0")
			case "failure":
				cmd = shell(p.RunDir, "exit 2")
			case "launch-failure":
				cmd = exec.Command(filepath.Join(p.RunDir, "missing-engine"))
			case "owner-lock-failure", "status-write-failure":
				name := ownerLockFile
				if outcome == "status-write-failure" {
					name = "status.json"
				}
				if err := os.Mkdir(filepath.Join(p.RunDir, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(outcome, "queued-") {
				s.SetBudget(Budget{})
			}
			snap, err := s.Submit(cmd, p, nil)
			admissionFailed := outcome == "owner-lock-failure" || outcome == "status-write-failure"
			if (err != nil) != admissionFailed {
				t.Fatalf("submission: %v, %v", snap, err)
			}
			if admissionFailed {
				if _, err := os.Stat(p.RunDir); !os.IsNotExist(err) {
					t.Fatalf("rejected run was not removed: %v", err)
				}
			} else {
				if strings.HasSuffix(outcome, "-cancel") || strings.HasSuffix(outcome, "-shutdown") {
					lock, err := tryFileLock(filepath.Join(source.RunDir, "resume.lock"))
					if lock != nil {
						lock.Close()
					}
					if !errors.Is(err, errLockBusy) {
						t.Fatalf("live resume has no exclusive claim: %v", err)
					}
					if strings.HasSuffix(outcome, "-cancel") {
						if _, err := s.Cancel(p.ID, project); err != nil {
							t.Fatal(err)
						}
					} else {
						s.ShutdownAll()
					}
				}
				if snap := waitFor(t, s, p.ID, project, finished); !finished(snap) {
					t.Fatalf("task did not finish: %v", snap)
				}
			}
			lock, err := tryFileLock(filepath.Join(source.RunDir, "resume.lock"))
			if err != nil {
				t.Fatalf("terminal path leaked the resume lock: %v", err)
			}
			lock.Close()
			retry := testScheduler(t, 1, 1000)
			again := prepareResume(t, retry, project, source.ID)
			if _, err := retry.Submit(shell(again.RunDir, "exit 0"), again, nil); err != nil {
				t.Fatalf("legitimate later retry refused: %v", err)
			}
			if snap := waitFor(t, retry, again.ID, project, finished); snap["status"] != "succeeded" {
				t.Fatal(snap)
			}
		})
	}
}

func TestResumeClaimHeldUntilCancelledChildIsReaped(t *testing.T) {
	project := tempProject(t)
	source := failedResumeSource(t, project)
	first, second := testScheduler(t, 1, 1000), testScheduler(t, 1, 1000)
	p := prepareResume(t, first, project, source.ID)
	contender := prepareResume(t, second, project, source.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, testPython(t), "-c", "import time; time.sleep(60)")
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	cmd.Cancel = func() error { close(entered); <-release; return nil }
	if _, err := first.Submit(cmd, p, nil); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait barrier was not entered")
	}
	if snap, err := first.Cancel(p.ID, project); err != nil || snap["status"] != "cancelling" {
		t.Fatalf("cancel: %v, %v", snap, err)
	}
	if _, err := second.Submit(shell(contender.RunDir, "exit 0"), contender, nil); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("resume admitted before Wait returned: %v", err)
	}
	unblock()
	if snap := waitFor(t, first, p.ID, project, finished); snap["status"] != "cancelled" {
		t.Fatal(snap)
	}
	again := prepareResume(t, second, project, source.ID)
	if _, err := second.Submit(shell(again.RunDir, "exit 0"), again, nil); err != nil {
		t.Fatal(err)
	}
	if snap := waitFor(t, second, again.ID, project, finished); snap["status"] != "succeeded" {
		t.Fatal(snap)
	}
}

func TestResumeClaimReleasedWhenOwningProcessDies(t *testing.T) {
	project := tempProject(t)
	source := failedResumeSource(t, project)
	control := t.TempDir()
	owner := startResumeProcess(t, project, control, "owner", true)
	waitLifecycleFile(t, filepath.Join(control, "ready-owner"))
	if err := os.WriteFile(filepath.Join(control, "submit"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitLifecycleFile(t, filepath.Join(control, "submitted-owner"))
	answer := readJSON(t, filepath.Join(control, "submitted-owner"))
	if answer["accepted"] != true || answer["snapshot"].(map[string]any)["status"] != "queued" {
		t.Fatalf("owner was not queued: %v", answer)
	}
	lock, err := tryFileLock(filepath.Join(source.RunDir, "resume.lock"))
	if lock != nil {
		lock.Close()
	}
	if !errors.Is(err, errLockBusy) {
		t.Fatalf("owner has no exclusive resume claim: %v", err)
	}
	if err := owner.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-owner.done; err == nil {
		t.Fatal("owner did not die by signal")
	}
	retry := testScheduler(t, 1, 1000)
	if snap, err := retry.Status(answer["id"].(string), project); err != nil || snap["status"] != "interrupted" {
		t.Fatalf("dead owner's task: %v, %v", snap, err)
	}
	p := prepareResume(t, retry, project, source.ID)
	if _, err := retry.Submit(shell(p.RunDir, "exit 0"), p, nil); err != nil {
		t.Fatal(err)
	}
	if snap := waitFor(t, retry, p.ID, project, finished); snap["status"] != "succeeded" {
		t.Fatal(snap)
	}
	t.Logf("owner pid=%d killed; later retry=%s succeeded", owner.cmd.Process.Pid, p.ID)
}

func TestResumeClaimsAreProjectScoped(t *testing.T) {
	firstProject, secondProject := tempProject(t), tempProject(t)
	firstSource, secondSource := failedResumeSource(t, firstProject), failedResumeSource(t, secondProject)
	s := testScheduler(t, 2, 4000)
	for _, source := range []*Prepared{firstSource, secondSource} {
		p := prepareResume(t, s, source.Project, source.ID)
		if _, err := s.Submit(shell(p.RunDir, "exec sleep 60"), p, nil); err != nil {
			t.Fatalf("resume in a different project was blocked: %v", err)
		}
	}
	// No sample-wide or global task lock is added: an ordinary start remains
	// admissible even while a resume for the same sample is live.
	other := testScheduler(t, 1, 1000)
	p, err := other.Prepare(TaskStartRequest{Kind: "sample_pipeline", SampleID: ptr("sampleA")}, firstProject, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Submit(shell(p.RunDir, "exit 0"), p, nil); err != nil {
		t.Fatalf("ordinary submission was locked out: %v", err)
	}
	if snap := waitFor(t, other, p.ID, firstProject, finished); snap["status"] != "succeeded" {
		t.Fatal(snap)
	}
}

func TestResumeClaimRefusesUnsafeLockFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			project := tempProject(t)
			source := failedResumeSource(t, project)
			path := filepath.Join(source.RunDir, "resume.lock")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(filepath.Join(project, "project.json"), path)
			case "fifo":
				err = syscall.Mkfifo(path, 0o600)
			case "directory":
				err = os.Mkdir(path, 0o755)
			}
			if err != nil {
				t.Fatal(err)
			}
			s := testScheduler(t, 1, 1000)
			p := prepareResume(t, s, project, source.ID)
			if _, err := s.Submit(shell(p.RunDir, "exit 0"), p, nil); err == nil {
				t.Fatal("unsafe lock file was accepted")
			}
			if _, err := os.Stat(p.RunDir); !os.IsNotExist(err) {
				t.Fatalf("failed admission left its run: %v", err)
			}
		})
	}
}
