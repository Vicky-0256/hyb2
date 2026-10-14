package core

// Service implements Backend: the request building, validation and lane of
// every Tauri command of desktop/src-tauri/src/main.rs.
//
// Intentionally dropped with the webview: the asset-protocol scope that
// project_open granted (views read project files directly) and
// file_save_dialog's base64 payload (views write files themselves after
// mygo.Dialog.Save). Native dialogs are the views' business; project_
// import_input_dialog becomes ImportInput with the picked source path.

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"slices"
	"strings"
)

// AnalysisOperations are the read-only analysis queries (contract R3/R6),
// served on the compute lane.
var AnalysisOperations = []string{
	"analysis_summary",
	"analysis_contact_bins",
	"analysis_hybrids",
	"analysis_top",
	"compare_bins",
}

// ProjectOperations are the short control-lane operations on project.json;
// Python validates the rest.
var ProjectOperations = []string{
	"add_reference",
	"update_reference",
	"remove_reference",
	"add_sample",
	"update_sample",
	"remove_sample",
	"import_sample_sheet",
	"check_inputs",
	"disk_estimate",
	"list_samples",
	"list_structures",
	"read_structure",
	"read_reference_sequence",
	"list_comparisons",
	"read_comparison",
}

// ServiceOptions configures a Service.
type ServiceOptions struct {
	// PreferencesPath is preferences.json in the application config
	// directory. Empty means default preferences that cannot be saved.
	PreferencesPath string
	// Logf receives errors nobody waits for (register_task_run, warm-up);
	// nil uses log.Printf.
	Logf func(format string, args ...any)
}

// Service is the Backend over a Runtime and a task Scheduler.
type Service struct {
	rt        *Runtime
	tasks     *Scheduler
	prefsPath string
	logf      func(format string, args ...any)
	// taskCommand builds a task's child command; tests replace it.
	taskCommand func(rt *Runtime, task *Prepared) *exec.Cmd
}

var _ Backend = (*Service)(nil)

// NewService wires the scheduler to the stored budget. rt may be nil when
// the runtime could not be resolved; bridge calls then fail.
func NewService(rt *Runtime, opts ServiceOptions) *Service {
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	tasks := NewScheduler()
	// A missing or invalid preferences file means the default budget.
	tasks.SetBudget(LoadBudget(opts.PreferencesPath))
	return &Service{
		rt: rt, tasks: tasks, prefsPath: opts.PreferencesPath, logf: logf,
		taskCommand: func(rt *Runtime, task *Prepared) *exec.Cmd {
			return BridgeCommand(rt.Bridge.Python, rt.Bridge.Root, task.RequestFile, rt.ComputeEnv)
		},
	}
}

// Scheduler is the task scheduler (for views that need the budget).
func (s *Service) Scheduler() *Scheduler { return s.tasks }

func (s *Service) runtime() (*Runtime, error) {
	if s.rt == nil {
		return nil, errors.New("Native runtime is not available. Rebuild or reinstall HYB2 Desktop with the runtime resources.")
	}
	return s.rt, nil
}

func (s *Service) call(lane Lane, request Obj) (Obj, error) {
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	return rt.Call(lane, request)
}

// Detect is runtime_detect.
func (s *Service) Detect() (Obj, error) {
	return s.call(Control, Obj{"operation": "detect"})
}

// CreateProject is project_create.
func (s *Service) CreateProject(path, name, description string) (Obj, error) {
	return s.call(Control, Obj{"operation": "create_project", "path": path, "name": name, "description": description})
}

// ValidateProject is project_validate.
func (s *Service) ValidateProject(path string, checkFiles bool) (Obj, error) {
	return s.call(Control, Obj{"operation": "validate_project", "path": path, "check_files": checkFiles})
}

// OpenProject is project_open (without the asset scope, see above).
func (s *Service) OpenProject(path string) (Obj, error) {
	return s.call(Control, Obj{"operation": "open_project", "path": path, "check_files": false})
}

// ImportInput is project_import_input_dialog after the dialog: it links or
// copies source into the project's inputs. role defaults to "other".
func (s *Service) ImportInput(path, source, role, label string) (Obj, error) {
	if role == "" {
		role = "other"
	}
	return s.call(Control, Obj{"operation": "import_input", "path": path, "source": source, "role": role, "label": label})
}

// ReadInput is project_read_input.
func (s *Service) ReadInput(path, relative string) (Obj, error) {
	return s.call(Control, Obj{"operation": "read_input", "path": path, "relative": relative})
}

// SaveAnalysisRun is project_save_analysis_run.
func (s *Service) SaveAnalysisRun(path string, result Obj) (Obj, error) {
	return s.call(Control, Obj{"operation": "save_analysis_run", "path": path, "result": result})
}

// UpdateUIState is project_update_ui_state.
func (s *Service) UpdateUIState(path string, uiState Obj) (Obj, error) {
	return s.call(Control, Obj{"operation": "update_ui_state", "path": path, "ui_state": uiState})
}

// CplfoldPredict is cplfold_predict on the compute lane.
func (s *Service) CplfoldPredict(sequence, evidenceMode string, evidenceArms any, parameters Obj) (Obj, error) {
	if evidenceMode == "" {
		evidenceMode = "none"
	}
	if evidenceArms == nil {
		evidenceArms = []any{}
	}
	if parameters == nil {
		parameters = Obj{}
	}
	return s.call(Compute, Obj{"operation": "cplfold_predict", "sequence": sequence,
		"evidenceMode": evidenceMode, "evidenceArms": evidenceArms, "parameters": parameters})
}

// mergeRequest copies payload and adds the fixed keys, which payload must
// not contain.
func mergeRequest(payload Obj, fixed Obj) (Obj, error) {
	request := Obj{}
	for key, value := range payload {
		if _, clash := fixed[key]; clash {
			return nil, fmt.Errorf("payload must not contain %q", key)
		}
		request[key] = value
	}
	for key, value := range fixed {
		request[key] = value
	}
	return request, nil
}

// projectOpRequest accepts {path, operation, ...} for one of ProjectOperations.
func projectOpRequest(request any) (Obj, error) {
	object, ok := request.(map[string]any)
	if !ok {
		return nil, errors.New("project operation must be a JSON object")
	}
	operation, _ := object["operation"].(string)
	if !slices.Contains(ProjectOperations, operation) {
		return nil, fmt.Errorf("unsupported project operation; expected one of %s", strings.Join(ProjectOperations, ", "))
	}
	if path, _ := object["path"].(string); strings.TrimSpace(path) == "" {
		return nil, errors.New("project operation needs a project path")
	}
	return object, nil
}

// ProjectOp is project_op.
func (s *Service) ProjectOp(path, operation string, payload Obj) (Obj, error) {
	request, err := mergeRequest(payload, Obj{"operation": operation, "path": path})
	if err != nil {
		return nil, err
	}
	if request, err = projectOpRequest(request); err != nil {
		return nil, err
	}
	return s.call(Control, request)
}

// analysisRequest accepts {project_path, operation, ...} for one of the
// read-only AnalysisOperations; everything else is forwarded unchanged for
// Python to validate.
func analysisRequest(request any) (Obj, error) {
	object, ok := request.(map[string]any)
	if !ok {
		return nil, errors.New("analysis query must be a JSON object")
	}
	operation, _ := object["operation"].(string)
	if !slices.Contains(AnalysisOperations, operation) {
		return nil, fmt.Errorf("unsupported analysis operation; expected one of %s", strings.Join(AnalysisOperations, ", "))
	}
	if path, _ := object["project_path"].(string); strings.TrimSpace(path) == "" {
		return nil, errors.New("analysis query needs a project_path")
	}
	return object, nil
}

// Query is analysis_query; it returns the response's `result` field.
func (s *Service) Query(projectPath, operation string, payload Obj) (any, error) {
	request, err := mergeRequest(payload, Obj{"operation": operation, "project_path": projectPath})
	if err != nil {
		return nil, err
	}
	if request, err = analysisRequest(request); err != nil {
		return nil, err
	}
	response, err := s.call(Compute, request)
	if err != nil {
		return nil, err
	}
	return response["result"], nil
}

// openProject opens the project through the control lane and returns its
// canonical path.
func (s *Service) openProject(path string) (string, error) {
	opened, err := s.call(Control, Obj{"operation": "open_project", "path": path})
	if err != nil {
		return "", err
	}
	project, ok := opened["path"].(string)
	if !ok {
		return "", errors.New("project path missing")
	}
	return project, nil
}

// startTask validates, stores and submits a task; it runs now or waits in
// the queue. When it ends, register_task_run records it in project.json.
func (s *Service) startTask(request TaskStartRequest, resumedFrom *string) (Obj, error) {
	if !slices.Contains(TaskKinds, request.Kind) {
		return nil, errors.New(unsupportedKind)
	}
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	project, err := s.openProject(request.ProjectPath)
	if err != nil {
		return nil, err
	}
	prepared, err := s.tasks.Prepare(request, project, resumedFrom)
	if err != nil {
		return nil, err
	}
	onFinish := func(project, id string) {
		register := Obj{"operation": "register_task_run", "path": project, "task_id": id}
		if _, err := rt.Call(Control, register); err != nil {
			s.logf("HYB2: register_task_run failed for %s: %v", id, err)
		}
	}
	return s.tasks.Submit(s.taskCommand(rt, prepared), prepared, onFinish)
}

// StartTask is task_start.
func (s *Service) StartTask(request TaskStartRequest) (Obj, error) {
	return s.startTask(request, nil)
}

// ResumeTask is task_resume: a new task (new id) from the stored request of
// an interrupted, failed or cancelled task. Stage checkpoints are Python's
// responsibility.
func (s *Service) ResumeTask(projectPath, taskID string) (Obj, error) {
	if err := ValidateID(taskID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectPath) == "" {
		return nil, errors.New("project_path is required to resume a task")
	}
	project, err := s.openProject(projectPath)
	if err != nil {
		return nil, err
	}
	stored, err := s.tasks.ResumeRequest(taskID, project)
	if err != nil {
		return nil, err
	}
	return s.startTask(stored, &taskID)
}

// TaskStatus is task_status; projectPath may be empty for tasks of this
// session.
func (s *Service) TaskStatus(projectPath, taskID string) (Obj, error) {
	return s.tasks.Status(taskID, projectPath)
}

// CancelTask is task_cancel.
func (s *Service) CancelTask(projectPath, taskID string) (Obj, error) {
	return s.tasks.Cancel(taskID, projectPath)
}

// ListTasks is task_list (the `tasks` array), newest first.
func (s *Service) ListTasks(projectPath string) ([]Obj, error) {
	return s.tasks.List(projectPath)
}

// Preferences is preferences_get.
func (s *Service) Preferences() (Obj, error) {
	if s.prefsPath == "" {
		return nil, errors.New("cannot resolve the application config directory")
	}
	return DescribePreferences(s.prefsPath), nil
}

// SetPreferences is preferences_set: `{budget?: {cpu?, mem_mb?} | null,
// theme?: ... | null}`. A new budget applies at once and starts queued
// tasks that now fit.
func (s *Service) SetPreferences(patch Obj) (Obj, error) {
	budget, err := ApplyPreferences(s.prefsPath, patch)
	if err != nil {
		return nil, err
	}
	s.tasks.SetBudget(budget)
	return DescribePreferences(s.prefsPath), nil
}

// WarmUp compiles the CPLfold Numba kernels in the compute sidecar in the
// background right after launch, so the first prediction does not pay the
// one-off JIT cost (about 50 s on a fresh install; later launches load the
// on-disk cache). It returns at once.
func (s *Service) WarmUp() {
	rt, err := s.runtime()
	if err != nil {
		return
	}
	go func() {
		if _, err := rt.Call(Compute, Obj{"operation": "warmup"}); err != nil {
			s.logf("HYB2: compute warm-up failed: %v", err)
		}
	}()
}

// Shutdown marks running and queued tasks interrupted (killing their
// process trees), then stops the sidecars; later calls fail.
func (s *Service) Shutdown() {
	s.tasks.ShutdownAll()
	if s.rt != nil {
		s.rt.Shutdown()
	}
}
