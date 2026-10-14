package core

// Obj is a JSON object as the bridge sends and receives it.
type Obj = map[string]any

// Window is one gene-coordinate window of a structure task (1-based).
type Window struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
}

// TaskStartRequest mirrors tasks::TaskStartRequest of the Tauri shell; it is
// also what task-start.json stores for task_resume.
type TaskStartRequest struct {
	ProjectPath string   `json:"project_path"`
	Kind        string   `json:"kind"`
	TaskID      *string  `json:"task_id,omitempty"`
	Sequence    *string  `json:"sequence,omitempty"`
	BonusMatrix *string  `json:"bonus_matrix,omitempty"`
	Parameters  any      `json:"parameters,omitempty"`
	Input       *string  `json:"input,omitempty"`
	Reference   *string  `json:"reference,omitempty"`
	SampleID    *string  `json:"sample_id,omitempty"`
	SampleIDs   []string `json:"sample_ids,omitempty"`
	GeneID      *string  `json:"gene_id,omitempty"`
	Mode        *string  `json:"mode,omitempty"`
	Windows     []Window `json:"windows,omitempty"`
	Workers     *uint32  `json:"workers,omitempty"`
	SamplesOne  []string `json:"samples_one,omitempty"`
	SamplesTwo  []string `json:"samples_two,omitempty"`
	CPU         *uint32  `json:"cpu,omitempty"`
	MemMB       *uint64  `json:"mem_mb,omitempty"`
}

// Backend is everything the native UI asks of the shell: one method per
// Tauri command of desktop/src-tauri/src/main.rs (named in each comment),
// with the same validation, lanes and results. Views depend on this
// interface only, so tests can substitute a fake. Every method may block;
// call it from a goroutine and publish results with Window.Update.
//
// Native dialogs are not here: views call mygo.Dialog directly and pass the
// chosen paths in.
type Backend interface {
	// runtime_detect: the bridge's whole `detect` response; the detection
	// itself is under "result".
	Detect() (Obj, error)

	// project_create: creates the project directory and returns the bridge result.
	CreateProject(path, name, description string) (Obj, error)
	// project_validate.
	ValidateProject(path string, checkFiles bool) (Obj, error)
	// project_open: validates and opens a project; the result carries the
	// canonical "path" and the "manifest". (Tasks open their project
	// themselves, as task_start does.)
	OpenProject(path string) (Obj, error)
	// project_import_input_dialog after the dialog: imports source (a path
	// the view picked with mygo.Dialog) into the project's inputs with a
	// role ("" means "other") and label.
	ImportInput(path, source, role, label string) (Obj, error)
	// project_update_ui_state.
	UpdateUIState(path string, uiState Obj) (Obj, error)
	// project_save_analysis_run.
	SaveAnalysisRun(path string, result Obj) (Obj, error)
	// project_read_input.
	ReadInput(path, relative string) (Obj, error)

	// project_op: one of the PROJECT_OPERATIONS allowlist on the control
	// lane; payload must not contain "operation" or "path".
	ProjectOp(path, operation string, payload Obj) (Obj, error)
	// analysis_query: an allowlisted analysis operation on the compute lane;
	// returns the bridge's `result` field (as the Tauri command does).
	Query(projectPath, operation string, payload Obj) (any, error)
	// cplfold_predict (compute lane).
	CplfoldPredict(sequence string, evidenceMode string, evidenceArms any, parameters Obj) (Obj, error)

	// task_start / task_resume / task_status / task_cancel / task_list:
	// task snapshots as the Tauri scheduler returns them
	// ({task_id, kind, status, progress, claim, ...}).
	StartTask(request TaskStartRequest) (Obj, error)
	ResumeTask(projectPath, taskID string) (Obj, error)
	TaskStatus(projectPath, taskID string) (Obj, error)
	CancelTask(projectPath, taskID string) (Obj, error)
	ListTasks(projectPath string) ([]Obj, error)

	// preferences_get / preferences_set.
	Preferences() (Obj, error)
	SetPreferences(patch Obj) (Obj, error)

	// Shutdown marks running tasks interrupted, stops the sidecars and
	// refuses later calls (application exit).
	Shutdown()
}
