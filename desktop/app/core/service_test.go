package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectOpAllowlist(t *testing.T) {
	if len(ProjectOperations) != 27 {
		t.Fatalf("%d project operations", len(ProjectOperations))
	}
	for _, operation := range ProjectOperations {
		request := map[string]any{"operation": operation, "path": "/p"}
		if got, err := projectOpRequest(request); err != nil || got["operation"] != operation {
			t.Fatalf("%s: %v", operation, err)
		}
	}
	for _, bad := range []any{
		map[string]any{"operation": "create_project", "path": "/p"},
		map[string]any{"operation": "run_task", "path": "/p"},
		map[string]any{"operation": "list_samples"},
		map[string]any{"operation": "list_samples", "path": " "},
		[]any{"list_samples"},
	} {
		if _, err := projectOpRequest(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	// The Backend method refuses payloads that try to override the fixed keys.
	s := NewService(nil, ServiceOptions{})
	for _, payload := range []Obj{{"operation": "x"}, {"path": "/q"}} {
		if _, err := s.ProjectOp("/p", "list_samples", payload); err == nil || !strings.Contains(err.Error(), "must not contain") {
			t.Fatalf("payload %v: %v", payload, err)
		}
	}
	if _, err := s.ProjectOp("/p", "create_project", nil); err == nil || !strings.Contains(err.Error(), "unsupported project operation") {
		t.Fatal(err)
	}
	if _, err := s.ProjectOp("/p", "list_samples", nil); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("no runtime: %v", err)
	}
}

// pipeline_energy_check goes through the project allowlist, and an answer
// that leaves a sample out (or an older runtime without the operation)
// is an error, never a pass.
func TestPipelineEnergyCheck(t *testing.T) {
	s := NewService(nil, ServiceOptions{})
	if _, err := s.PipelineEnergyCheck("/p", []string{"a"}, false); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("no runtime: %v", err)
	}
	if _, err := s.PipelineEnergyCheck(" ", []string{"a"}, false); err == nil {
		t.Fatal("no project path accepted")
	}
	response := Obj{"ok": true, "samples": map[string]any{
		"a": map[string]any{"needs_engine": true, "engine": "rnaduplex", "problem": nil, "note": nil},
		"b": map[string]any{"needs_engine": true, "engine": "rnaduplex", "problem": "not found", "note": nil},
	}}
	got, err := decodeEnergyCheck(response, []string{"a", "b"})
	if err != nil || !got["a"].NeedsEngine || got["a"].Engine != "rnaduplex" || got["a"].Problem != "" || got["b"].Problem != "not found" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := decodeEnergyCheck(response, []string{"a", "c"}); err == nil || !strings.Contains(err.Error(), "sample c") {
		t.Fatalf("missing sample: %v", err)
	}
	if _, err := decodeEnergyCheck(Obj{"ok": true}, []string{"a"}); err == nil {
		t.Fatal("no samples accepted")
	}
}

func TestAnalysisQueriesAreAllowlisted(t *testing.T) {
	for _, operation := range AnalysisOperations {
		request := map[string]any{"project_path": "/p", "operation": operation, "sample_ids": []any{"a"}, "bin_size": 10}
		got, err := analysisRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		assertJSON(t, got, `{"project_path":"/p","operation":"`+operation+`","sample_ids":["a"],"bin_size":10}`)
	}
	for _, bad := range []any{
		map[string]any{"project_path": "/p", "operation": "import_input"},
		map[string]any{"project_path": "/p", "operation": "sample_pipeline_run"},
		map[string]any{"project_path": "/p"},
		map[string]any{"operation": "analysis_summary"},
		map[string]any{"project_path": " ", "operation": "analysis_summary"},
		[]any{"analysis_summary"},
	} {
		if _, err := analysisRequest(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	s := NewService(nil, ServiceOptions{})
	if _, err := s.Query("/p", "analysis_summary", Obj{"project_path": "/q"}); err == nil {
		t.Fatal("payload overrode project_path")
	}
	if _, err := s.Query("/p", "import_input", nil); err == nil || !strings.Contains(err.Error(), "unsupported analysis operation") {
		t.Fatal(err)
	}
}

func TestReleaseIgnoresEnvironmentOverrides(t *testing.T) {
	t.Setenv("HYB2_REPOSITORY_ROOT", "/attacker/repo")
	t.Setenv("HYB2_PYTHON", "/attacker/python")
	if b, err := ResolveBridge(t.TempDir(), false, ""); err == nil {
		t.Fatalf("release build honored the environment: %v", b)
	}
	b, err := ResolveBridge(t.TempDir(), true, "")
	if err != nil || *b != (Bridge{Root: "/attacker/repo", Python: "/attacker/python"}) {
		t.Fatalf("dev: %v %v", b, err)
	}
	t.Setenv("HYB2_PYTHON", "")
	if b, _ := ResolveBridge(t.TempDir(), true, ""); b.Python != "python3" {
		t.Fatalf("default python %q", b.Python)
	}
	// HYB2_PYTHON alone is not an override.
	t.Setenv("HYB2_REPOSITORY_ROOT", "")
	t.Setenv("HYB2_PYTHON", "p")
	if _, err := ResolveBridge(t.TempDir(), true, ""); err == nil {
		t.Fatal("HYB2_PYTHON alone selected a runtime")
	}
}

func TestBridgeResultKeepsUnavailablePassthrough(t *testing.T) {
	if _, err := bridgeResult(json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := bridgeResult(json.RawMessage(`{"ok":false,"status":"unavailable"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := bridgeResult(json.RawMessage(`{"ok":false,"error":"bad"}`)); err == nil || err.Error() != "bad" {
		t.Fatal(err)
	}
	if _, err := bridgeResult(json.RawMessage(`{"ok":false}`)); err == nil || err.Error() != "desktop runtime operation failed" {
		t.Fatal(err)
	}
}

func TestVersionsAreInSync(t *testing.T) {
	config := readJSON(t, filepath.Join("..", "mygo.json"))
	if config["version"] != AppVersion {
		t.Fatalf("mygo.json %v, AppVersion %s", config["version"], AppVersion)
	}
	if pkg := filepath.Join(repoRoot(t), "desktop", "package.json"); isFile(pkg) {
		if version := readJSON(t, pkg)["version"]; version != AppVersion {
			t.Fatalf("package.json %v, AppVersion %s", version, AppVersion)
		}
	}
}

// newTestService runs the repository bridge with the staged (or PATH) Python.
func newTestService(t *testing.T) *Service {
	t.Helper()
	root := repoRoot(t)
	if !isFile(filepath.Join(root, "desktop", "runtime", "bridge.py")) {
		t.Skip("repository layout not available")
	}
	rt, err := NewRuntime(Bridge{Root: root, Python: testPython(t)}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(rt, ServiceOptions{PreferencesPath: filepath.Join(t.TempDir(), "config", PrefsFileName)})
	t.Cleanup(s.Shutdown)
	return s
}

// createProject makes a fresh project through the bridge and returns its
// canonical path.
func createProject(t *testing.T, s *Service) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "e2e.hyb2proj")
	if _, err := s.CreateProject(path, "Go e2e", "end-to-end test"); err != nil {
		t.Fatal(err)
	}
	opened, err := s.OpenProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opened["manifest"].(map[string]any); !ok {
		t.Fatalf("open_project returned no manifest: %v", opened)
	}
	return opened["path"].(string)
}

func waitTask(t *testing.T, s *Service, project, id string, timeout time.Duration) Obj {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		snap, err := s.TaskStatus(project, id)
		if err != nil {
			t.Fatal(err)
		}
		if finished(snap) || time.Now().After(deadline) {
			return snap
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitRun polls project.json until runs[] holds id with the given status.
func waitRun(t *testing.T, project, id, status string) map[string]any {
	t.Helper()
	var runs []any
	for range 200 {
		runs, _ = readJSON(t, filepath.Join(project, "project.json"))["runs"].([]any)
		for _, run := range runs {
			if entry, _ := run.(map[string]any); entry["id"] == id && entry["status"] == status {
				return entry
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("project.json runs[] has no %s run %s: %v", status, id, runs)
	return nil
}

// TestServiceValidateTaskEndToEnd: Service -> open_project -> task child
// (real bridge) -> status.json -> register_task_run in project.json.
func TestServiceValidateTaskEndToEnd(t *testing.T) {
	s := newTestService(t)
	detect, err := s.Detect()
	if err != nil || detect["ok"] != true {
		t.Fatalf("detect: %v %v", detect, err)
	}
	project := createProject(t, s)
	if validated, err := s.ValidateProject(project, true); err != nil || validated["ok"] != true {
		t.Fatalf("validate: %v %v", validated, err)
	}
	if _, err := s.ProjectOp(project, "list_samples", nil); err != nil {
		t.Fatalf("list_samples: %v", err)
	}
	if _, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "sh"}); err == nil {
		t.Fatal("unsupported kind started")
	}
	snap, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "validate_project"})
	if err != nil {
		t.Fatal(err)
	}
	id := snap["task_id"].(string)
	assertJSON(t, snap["claim"], `{"cpu":1,"mem_mb":256}`)
	snap = waitTask(t, s, project, id, 60*time.Second)
	if snap["status"] != "succeeded" || snap["exit_code"] != float64(0) {
		stderr, _ := os.ReadFile(filepath.Join(project, "runs", id, "stderr.log"))
		t.Fatalf("%v\n%s", snap, stderr)
	}
	run := waitRun(t, project, id, "succeeded")
	if run["kind"] != "validate_project" {
		t.Fatalf("registered run %v", run)
	}
	tasks, err := s.ListTasks(project)
	if err != nil || len(tasks) != 1 || tasks[0]["task_id"] != id {
		t.Fatalf("list: %v %v", tasks, err)
	}
	if _, err := s.ResumeTask(project, id); err == nil || !strings.Contains(err.Error(), "can be resumed") {
		t.Fatalf("resumed a succeeded task: %v", err)
	}
	// Preferences: a budget change applies to the scheduler at once.
	prefs, err := s.SetPreferences(Obj{"budget": Obj{"cpu": 1, "mem_mb": 512}, "theme": "dark"})
	if err != nil || prefs["custom_budget"] != true || prefs["theme"] != "dark" {
		t.Fatalf("prefs: %v %v", prefs, err)
	}
	if s.Scheduler().Budget() != (Budget{1, 512}) {
		t.Fatal("budget not applied")
	}
	if got, _ := s.Preferences(); got["custom_budget"] != true {
		t.Fatalf("preferences %v", got)
	}
	s.Shutdown()
	if _, err := s.Detect(); err == nil {
		t.Fatal("call after shutdown succeeded")
	}
	if _, err := s.StartTask(TaskStartRequest{ProjectPath: project, Kind: "validate_project"}); err == nil {
		t.Fatal("task started after shutdown")
	}
}

// pipeline_energy_check against the real bridge: the Go request and
// decoding match Python's operation (desktop/runtime/sample_run.py).
func TestPipelineEnergyCheckBridge(t *testing.T) {
	s := newTestService(t)
	project := createProject(t, s)
	dir := t.TempDir()
	reads := filepath.Join(dir, "reads.fastq")
	ref := filepath.Join(dir, "ref.fasta")
	for path, text := range map[string]string{reads: "@r1\nACGTACGTAC\n+\nIIIIIIIIII\n", ref: ">g\nACGTACGTACGTACGT\n"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	add := func(name string, settings Obj) string {
		t.Helper()
		added, err := s.ProjectOp(project, "add_sample", Obj{"source": reads, "name": name, "reference": ref, "settings": settings})
		if err != nil {
			t.Fatal(err)
		}
		sample, _ := added["sample"].(map[string]any)
		id, _ := sample["id"].(string)
		if id == "" {
			t.Fatalf("add_sample: %v", added)
		}
		return id
	}
	plain := add("plain", nil)
	off := add("off", Obj{"energy": false})
	checks, err := s.PipelineEnergyCheck(project, []string{plain, off, "nope"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// the bundled runtime has ViennaRNA: RNAduplex, nothing missing
	if c := checks[plain]; !c.NeedsEngine || c.Engine != "rnaduplex" || c.Problem != "" {
		t.Fatalf("plain: %+v", c)
	}
	if c := checks[off]; c.NeedsEngine || c.Problem != "" {
		t.Fatalf("energy false: %+v", c)
	}
	if c := checks["nope"]; c.Problem != "unknown sample" {
		t.Fatalf("unknown sample: %+v", c)
	}
	if _, err := s.PipelineEnergyCheck(project, []string{plain}, true); err != nil {
		t.Fatalf("resuming: %v", err)
	}
	if env := s.taskEnv([]string{"A=1"}); len(env) != 1 {
		t.Fatalf("task env %v", env)
	}
}

// The preference of earlier versions for the dG program is gone: a patch
// naming it is refused, and a stored one is kept in the file but not shown.
func TestRemovedDGProgramPreference(t *testing.T) {
	const old = "hybrid_min" // legacy-name: preference of versions before RNAduplex
	path := filepath.Join(t.TempDir(), "config", PrefsFileName)
	if _, err := ApplyPreferences(path, Obj{old: "/opt/x/bin/prog"}); err == nil {
		t.Fatal("the removed preference was accepted")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"`+old+`": "/opt/x/bin/prog", "theme": "dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, present := DescribePreferences(path)[old]; present {
		t.Fatal("the removed preference is shown")
	}
	if _, err := ApplyPreferences(path, Obj{"theme": "light"}); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, path); got["theme"] != "light" || got[old] != "/opt/x/bin/prog" {
		t.Fatalf("file %v", got)
	}
}

// The mfold preference (COMRADES(mfold)): stored as a path, kept
// apart from it, and given to task processes as HYB2_MFOLD.
func TestMfoldPreference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", PrefsFileName)
	if LoadMfold(path) != "" || DescribePreferences(path)["mfold"] != "" {
		t.Fatal("default is not the search")
	}
	if _, err := ApplyPreferences(path, Obj{"mfold": " /opt/m/bin/mfold "}); err != nil {
		t.Fatal(err)
	}
	if LoadMfold(path) != filepath.Clean("/opt/m/bin/mfold") {
		t.Fatalf("stored %q", LoadMfold(path))
	}
	for _, bad := range []any{"bin/mfold", 3} {
		if _, err := ApplyPreferences(path, Obj{"mfold": bad}); err == nil || !strings.Contains(err.Error(), "mfold") {
			t.Fatalf("accepted %v (%v)", bad, err)
		}
	}
	s := &Service{prefsPath: path}
	env := strings.Join(s.taskEnv([]string{"A=1"}), "\n")
	if !strings.Contains(env, MfoldEnv+"="+filepath.Clean("/opt/m/bin/mfold")) {
		t.Fatalf("task env %q", env)
	}
	if _, err := ApplyPreferences(path, Obj{"mfold": nil}); err != nil || LoadMfold(path) != "" {
		t.Fatalf("clear: %v %q", err, LoadMfold(path))
	}
	if strings.Contains(strings.Join(s.taskEnv(nil), "\n"), MfoldEnv) {
		t.Fatal("cleared preference still in the task env")
	}
}

// main.go resolves the runtime on a goroutine (first launch: unpacking it)
// and attaches it with SetRuntime; the window may close first. A runtime
// that arrives after Shutdown started is stopped, not attached, so no
// sidecar starts for it.
func TestSetRuntimeAfterShutdownDoesNotAttachOrStartSidecars(t *testing.T) {
	root := t.TempDir()
	bridgeDir := filepath.Join(root, "desktop", "runtime")
	if err := os.MkdirAll(bridgeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.py"), []byte("# stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(Bridge{Root: root, Python: "python3"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	s := NewService(nil, ServiceOptions{})
	s.SetPending(true)
	s.Shutdown()
	s.SetPending(true)
	if s.SetRuntime(rt) {
		t.Fatal("SetRuntime attached runtime after Shutdown")
	}
	if s.rt != nil || s.rtPending {
		t.Fatalf("service retained runtime or pending state: rt=%v pending=%v", s.rt, s.rtPending)
	}
	if !rt.control.closed.Load() || !rt.compute.closed.Load() {
		t.Fatal("incoming runtime sidecars were not shut down")
	}
	if _, err := rt.Call(Control, Obj{"operation": "detect"}); err == nil {
		t.Fatal("closed incoming runtime accepted a call")
	}
	if _, err := s.Detect(); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("Detect after Shutdown: %v", err)
	}
	s.WarmUp()
	if s.SetRuntime(nil) {
		t.Fatal("SetRuntime(nil) returned true after Shutdown")
	}

	// Before Shutdown, SetRuntime attaches the runtime and Shutdown closes it.
	rt2, err := NewRuntime(Bridge{Root: root, Python: "python3"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s2 := NewService(nil, ServiceOptions{})
	if !s2.SetRuntime(rt2) {
		t.Fatal("SetRuntime before Shutdown returned false")
	}
	s2.Shutdown()
	if !rt2.control.closed.Load() || !rt2.compute.closed.Load() {
		t.Fatal("attached runtime sidecars were not shut down on Shutdown")
	}

	// While Shutdown still stops the evidence worker and the tasks (which
	// can take seconds), SetRuntime is refused too: main.go then neither
	// detects nor warms up a runtime that Shutdown is about to stop.
	rt3, err := NewRuntime(Bridge{Root: root, Python: "python3"}, "")
	if err != nil {
		t.Fatal(err)
	}
	s3 := NewService(nil, ServiceOptions{})
	s3.SetPending(true)
	s3.evidence.mu.Lock() // holds Shutdown in evidence.Shutdown
	stopped := make(chan struct{})
	go func() { s3.Shutdown(); close(stopped) }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		s3.rtMu.RLock()
		closed := s3.closed
		s3.rtMu.RUnlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			s3.evidence.mu.Unlock()
			t.Fatal("Shutdown reached the evidence worker without marking the service closed")
		}
	}
	attached := s3.SetRuntime(rt3)
	s3.evidence.mu.Unlock()
	<-stopped
	if attached || s3.rt != nil || !rt3.control.closed.Load() || !rt3.compute.closed.Load() {
		t.Fatalf("SetRuntime during Shutdown: attached %v, kept %v, sidecars closed %v %v",
			attached, s3.rt != nil, rt3.control.closed.Load(), rt3.compute.closed.Load())
	}
}
