#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use base64::Engine as _;
use serde::Deserialize;
use serde_json::{json, Value};
use std::ffi::OsString;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tauri::Manager;
use tauri_plugin_dialog::DialogExt;

mod prefs;
mod sidecar;
mod tasks;
use sidecar::{Sidecar, SidecarSpec};

const CONTROL_TIMEOUT: Duration = Duration::from_secs(120);
const COMPUTE_TIMEOUT: Duration = Duration::from_secs(900);
const MAX_SAVE_BYTES: usize = 200 * 1024 * 1024;

#[derive(Debug, Deserialize)]
struct CreateProjectRequest {
    path: String,
    name: String,
    description: Option<String>,
}
#[derive(Debug, Deserialize)]
struct ValidateProjectRequest {
    path: String,
    check_files: Option<bool>,
}
#[derive(Debug, Deserialize)]
struct ImportInputDialogRequest {
    path: String,
    role: Option<String>,
    label: Option<String>,
}
#[derive(Debug, Deserialize)]
struct ReadInputRequest {
    path: String,
    relative: String,
}
#[derive(Debug, Deserialize)]
struct SaveAnalysisRunRequest {
    path: String,
    result: Value,
}
#[derive(Debug, Deserialize)]
struct UpdateUiStateRequest {
    path: String,
    ui_state: Value,
}
#[derive(Debug, Deserialize)]
struct CplfoldPredictRequest {
    sequence: String,
    evidence_mode: Option<String>,
    evidence_arms: Option<Value>,
    parameters: Option<Value>,
}
#[derive(Debug, Deserialize)]
struct FileSaveRequest {
    suggested_name: Option<String>,
    data_base64: String,
}

#[derive(Debug, PartialEq)]
struct BridgeRuntime {
    root: PathBuf,
    python: PathBuf,
}

fn bundled_runtime(resource_dir: &Path) -> Result<Option<BridgeRuntime>, String> {
    // Resource maps preserve this layout on every target platform.
    let root = resource_dir.join("native");
    if !root.join("desktop/runtime/bridge.py").is_file() {
        return Ok(None);
    }
    let python = root.join(if cfg!(windows) {
        "python/python.exe"
    } else {
        "python/bin/python3"
    });
    if !python.is_file() {
        return Err(format!(
            "Bundled Python is missing at {}. Rebuild or reinstall the desktop runtime.",
            python.display()
        ));
    }
    Ok(Some(BridgeRuntime { root, python }))
}

/// Development override from HYB2_REPOSITORY_ROOT / HYB2_PYTHON. Release
/// builds (`debug == false`) never honor the environment.
fn env_override(
    debug: bool,
    root: Option<OsString>,
    python: Option<OsString>,
) -> Option<BridgeRuntime> {
    if !debug {
        return None;
    }
    Some(BridgeRuntime {
        root: PathBuf::from(root?),
        python: python
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from("python3")),
    })
}

fn resolve_runtime(app: &tauri::AppHandle) -> Result<BridgeRuntime, String> {
    let debug = cfg!(debug_assertions);
    if let Some(runtime) = env_override(
        debug,
        std::env::var_os("HYB2_REPOSITORY_ROOT"),
        std::env::var_os("HYB2_PYTHON"),
    ) {
        return Ok(runtime);
    }
    let resources = app
        .path()
        .resource_dir()
        .map_err(|error| format!("cannot resolve application resources: {error}"))?;
    if let Some(runtime) = bundled_runtime(&resources)? {
        return Ok(runtime);
    }
    if debug {
        let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..");
        if let Some(runtime) = bundled_runtime(&root.join("desktop/resources"))? {
            return Ok(runtime);
        }
        let python = std::env::var_os("HYB2_PYTHON")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from("python3"));
        return Ok(BridgeRuntime { root, python });
    }
    Err("Native runtime is not bundled. Rebuild or reinstall HYB2 Desktop with the runtime resources.".into())
}

/// Resolved runtime plus its two persistent sidecars (contract C1).
struct Runtime {
    root: PathBuf,
    python: PathBuf,
    /// Environment for compute work (sidecar and task children).
    compute_env: Vec<(String, OsString)>,
    control: Sidecar,
    compute: Sidecar,
}

#[derive(Clone, Copy)]
enum Lane {
    Control,
    Compute,
}

impl Runtime {
    fn new(app: &tauri::AppHandle, bridge: BridgeRuntime) -> Result<Self, String> {
        if !bridge.root.join("desktop/runtime/bridge.py").is_file() {
            return Err(format!(
                "Desktop runtime bridge is missing under {}",
                bridge.root.display()
            ));
        }
        let base: Vec<(String, OsString)> = vec![
            ("HYB2_APP_VERSION".into(), env!("CARGO_PKG_VERSION").into()),
            ("PYTHONUNBUFFERED".into(), "1".into()),
            ("PYTHONIOENCODING".into(), "utf-8".into()),
            // Never write .pyc into the (signed, possibly read-only) app bundle.
            ("PYTHONDONTWRITEBYTECODE".into(), "1".into()),
        ];
        let mut compute_env = base.clone();
        if let Ok(cache) = app.path().app_cache_dir() {
            let numba = cache.join("numba");
            if fs::create_dir_all(&numba).is_ok() {
                compute_env.push(("NUMBA_CACHE_DIR".into(), numba.into_os_string()));
            }
        }
        let spec = |name: &str, envs: &Vec<(String, OsString)>, timeout| SidecarSpec {
            name: name.into(),
            program: bridge.python.clone(),
            args: vec![
                "-m".into(),
                "desktop.runtime.bridge".into(),
                "--repo-root".into(),
                bridge.root.clone().into_os_string(),
                "--serve".into(),
            ],
            cwd: bridge.root.clone(),
            envs: envs.clone(),
            timeout,
        };
        Ok(Runtime {
            control: Sidecar::new(spec("control", &base, CONTROL_TIMEOUT)),
            compute: Sidecar::new(spec("compute", &compute_env, COMPUTE_TIMEOUT)),
            compute_env,
            root: bridge.root,
            python: bridge.python,
        })
    }
    fn call(&self, lane: Lane, request: &Value) -> Result<Value, String> {
        let sidecar = match lane {
            Lane::Control => &self.control,
            Lane::Compute => &self.compute,
        };
        bridge_result(sidecar.request(request)?)
    }
}

/// Keeps the old single-shot semantics: failures become errors, except
/// `status: "unavailable"` responses, which the UI renders itself.
fn bridge_result(parsed: Value) -> Result<Value, String> {
    if !parsed.get("ok").and_then(Value::as_bool).unwrap_or(false)
        && parsed.get("status").and_then(Value::as_str) != Some("unavailable")
    {
        return Err(parsed
            .get("error")
            .and_then(Value::as_str)
            .unwrap_or("desktop runtime operation failed")
            .to_string());
    }
    Ok(parsed)
}

#[derive(Default)]
struct Desktop {
    runtime: Mutex<Option<Arc<Runtime>>>,
}

fn runtime(app: &tauri::AppHandle) -> Result<Arc<Runtime>, String> {
    let state = app.state::<Desktop>();
    let mut slot = state.runtime.lock().unwrap_or_else(|e| e.into_inner());
    if let Some(runtime) = slot.as_ref() {
        return Ok(runtime.clone());
    }
    let runtime = Arc::new(Runtime::new(app, resolve_runtime(app)?)?);
    *slot = Some(runtime.clone());
    Ok(runtime)
}

fn shutdown(app: &tauri::AppHandle) {
    tasks::shutdown_all();
    if let Some(state) = app.try_state::<Desktop>() {
        let taken = state
            .runtime
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .take();
        if let Some(runtime) = taken {
            runtime.control.shutdown();
            runtime.compute.shutdown();
        }
    }
}

async fn blocking<T: Send + 'static>(
    work: impl FnOnce() -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(work)
        .await
        .map_err(|error| format!("desktop worker failed: {error}"))?
}

async fn dispatch(app: tauri::AppHandle, lane: Lane, request: Value) -> Result<Value, String> {
    blocking(move || runtime(&app)?.call(lane, &request)).await
}

#[tauri::command]
async fn runtime_detect(app: tauri::AppHandle) -> Result<Value, String> {
    dispatch(app, Lane::Control, json!({"operation": "detect"})).await
}

#[tauri::command]
async fn project_create(
    app: tauri::AppHandle,
    request: CreateProjectRequest,
) -> Result<Value, String> {
    dispatch(app, Lane::Control, json!({"operation": "create_project", "path": request.path, "name": request.name, "description": request.description.unwrap_or_default()})).await
}

#[tauri::command]
async fn project_validate(
    app: tauri::AppHandle,
    request: ValidateProjectRequest,
) -> Result<Value, String> {
    dispatch(app, Lane::Control, json!({"operation": "validate_project", "path": request.path, "check_files": request.check_files.unwrap_or(false)})).await
}

/// Lets the webview read `<project>/inputs` and `<project>/runs` through the
/// asset protocol (contract C4). The static scope in tauri.conf.json is empty.
fn allow_project_assets(app: &tauri::AppHandle, project: &Path) -> Result<(), String> {
    let scope = app.asset_protocol_scope();
    for sub in ["inputs", "runs"] {
        scope
            .allow_directory(project.join(sub), true)
            .map_err(|error| format!("cannot grant asset access: {error}"))?;
    }
    Ok(())
}

#[tauri::command]
async fn project_open(
    app: tauri::AppHandle,
    request: ValidateProjectRequest,
) -> Result<Value, String> {
    let opened = dispatch(app.clone(), Lane::Control, json!({"operation": "open_project", "path": request.path, "check_files": request.check_files.unwrap_or(false)})).await?;
    if let Some(path) = opened.get("path").and_then(Value::as_str) {
        allow_project_assets(&app, Path::new(path))?;
    }
    Ok(opened)
}

#[tauri::command]
async fn project_read_input(
    app: tauri::AppHandle,
    request: ReadInputRequest,
) -> Result<Value, String> {
    dispatch(
        app,
        Lane::Control,
        json!({"operation":"read_input", "path":request.path, "relative":request.relative}),
    )
    .await
}

#[tauri::command]
async fn project_save_analysis_run(
    app: tauri::AppHandle,
    request: SaveAnalysisRunRequest,
) -> Result<Value, String> {
    dispatch(
        app,
        Lane::Control,
        json!({"operation":"save_analysis_run", "path":request.path, "result":request.result}),
    )
    .await
}

#[tauri::command]
async fn project_update_ui_state(
    app: tauri::AppHandle,
    request: UpdateUiStateRequest,
) -> Result<Value, String> {
    dispatch(
        app,
        Lane::Control,
        json!({"operation":"update_ui_state", "path":request.path, "ui_state":request.ui_state}),
    )
    .await
}

#[tauri::command]
async fn cplfold_predict(
    app: tauri::AppHandle,
    request: CplfoldPredictRequest,
) -> Result<Value, String> {
    dispatch(
        app,
        Lane::Compute,
        json!({
            "operation":"cplfold_predict",
            "sequence":request.sequence,
            "evidenceMode":request.evidence_mode.unwrap_or_else(|| "none".into()),
            "evidenceArms":request.evidence_arms.unwrap_or_else(|| json!([])),
            "parameters":request.parameters.unwrap_or_else(|| json!({}))
        }),
    )
    .await
}

// ---- Dialogs and files (contract C4). The frontend never passes a source
// path for reading: Rust obtains it from a native picker.

fn dialog_path(picked: Option<tauri_plugin_dialog::FilePath>) -> Result<Option<PathBuf>, String> {
    picked
        .map(|path| {
            path.into_path()
                .map_err(|error| format!("unsupported dialog path: {error}"))
        })
        .transpose()
}

fn file_dialog(
    app: &tauri::AppHandle,
    title: &str,
) -> tauri_plugin_dialog::FileDialogBuilder<tauri::Wry> {
    let mut builder = app.dialog().file().set_title(title);
    if let Some(window) = app.get_webview_window("main") {
        builder = builder.set_parent(&window);
    }
    builder
}

#[tauri::command]
async fn dialog_pick_project_folder(app: tauri::AppHandle) -> Result<Value, String> {
    blocking(move || {
        let path = dialog_path(file_dialog(&app, "Open HYB2 project").blocking_pick_folder())?;
        Ok(json!({"path": path}))
    })
    .await
}

#[tauri::command]
async fn dialog_pick_new_project_folder(app: tauri::AppHandle) -> Result<Value, String> {
    blocking(move || {
        let picked = file_dialog(&app, "Choose or create an empty folder for the new project")
            .set_can_create_directories(true)
            .blocking_pick_folder();
        Ok(json!({"path": dialog_path(picked)?}))
    })
    .await
}

#[tauri::command]
async fn project_import_input_dialog(
    app: tauri::AppHandle,
    request: ImportInputDialogRequest,
) -> Result<Value, String> {
    blocking(move || {
        let picked = file_dialog(&app, "Import input file").blocking_pick_file();
        let Some(source) = dialog_path(picked)? else {
            return Ok(json!({"ok": true, "cancelled": true}));
        };
        runtime(&app)?.call(Lane::Control, &json!({"operation":"import_input", "path":request.path, "source":source, "role":request.role.unwrap_or_else(|| "other".into()), "label":request.label.unwrap_or_default()}))
    })
    .await
}

/// Only the final path component of a suggested name is used.
fn safe_file_name(suggested: Option<&str>) -> String {
    suggested
        .map(|name| name.rsplit(['/', '\\']).next().unwrap_or("").trim())
        .filter(|name| !name.is_empty() && *name != "." && *name != "..")
        .unwrap_or("hyb2-export")
        .to_string()
}

fn decode_payload(data: &str) -> Result<Vec<u8>, String> {
    if data.len() > MAX_SAVE_BYTES / 3 * 4 + 4 {
        return Err(format!(
            "file is too large to save (limit {} MB)",
            MAX_SAVE_BYTES / 1024 / 1024
        ));
    }
    base64::engine::general_purpose::STANDARD
        .decode(data.trim())
        .map_err(|error| format!("invalid base64 data: {error}"))
}

#[tauri::command]
async fn file_save_dialog(
    app: tauri::AppHandle,
    request: FileSaveRequest,
) -> Result<Value, String> {
    blocking(move || {
        let bytes = decode_payload(&request.data_base64)?;
        let picked = file_dialog(&app, "Save file")
            .set_file_name(safe_file_name(request.suggested_name.as_deref()))
            .blocking_save_file();
        let Some(path) = dialog_path(picked)? else {
            return Ok(json!({"saved": false}));
        };
        fs::write(&path, bytes)
            .map_err(|error| format!("could not save {}: {error}", path.display()))?;
        Ok(json!({"saved": true, "path": path}))
    })
    .await
}

// ---- Unified tasks (contract C3).

/// Opens the project through the control lane and returns its canonical path.
fn open_project(rt: &Runtime, path: &str) -> Result<PathBuf, String> {
    let opened = rt.call(
        Lane::Control,
        &json!({"operation":"open_project","path":path}),
    )?;
    Ok(PathBuf::from(
        opened["path"].as_str().ok_or("project path missing")?,
    ))
}

/// Validates, stores and submits a task; it runs now or waits in the queue.
fn start_task(
    rt: &Arc<Runtime>,
    request: &tasks::TaskStartRequest,
    resumed_from: Option<&str>,
) -> Result<Value, String> {
    if !tasks::KINDS.contains(&request.kind.as_str()) {
        return Err("engine unavailable or not enabled: unsupported task kind".into());
    }
    let project = open_project(rt, &request.project_path)?;
    let prepared = tasks::prepare(request, &project, resumed_from)?;
    let command =
        tasks::bridge_command(&rt.python, &rt.root, &prepared.request_file, &rt.compute_env);
    let register = rt.clone();
    let on_finish: tasks::OnFinish = Arc::new(move |project: &Path, id: &str| {
        let request = json!({"operation":"register_task_run","path":project,"task_id":id});
        if let Err(error) = register.call(Lane::Control, &request) {
            eprintln!("HYB2: register_task_run failed for {id}: {error}");
        }
    });
    tasks::submit(command, prepared, on_finish)
}

#[tauri::command]
async fn task_start(
    app: tauri::AppHandle,
    request: tasks::TaskStartRequest,
) -> Result<Value, String> {
    blocking(move || {
        if !tasks::KINDS.contains(&request.kind.as_str()) {
            return Err("engine unavailable or not enabled: unsupported task kind".into());
        }
        start_task(&runtime(&app)?, &request, None)
    })
    .await
}

/// Starts a new task (new id) from the stored request of an interrupted,
/// failed or cancelled task. Stage checkpoints are Python's responsibility.
#[tauri::command]
async fn task_resume(app: tauri::AppHandle, request: tasks::TaskRequest) -> Result<Value, String> {
    blocking(move || {
        tasks::validate_id(&request.task_id)?;
        let path = request
            .project_path
            .as_deref()
            .filter(|p| !p.trim().is_empty())
            .ok_or("project_path is required to resume a task")?;
        let rt = runtime(&app)?;
        let project = open_project(&rt, path)?;
        let stored = tasks::resume_request(&request.task_id, &project)?;
        start_task(&rt, &stored, Some(&request.task_id))
    })
    .await
}

#[tauri::command]
async fn task_status(request: tasks::TaskRequest) -> Result<Value, String> {
    blocking(move || {
        tasks::status(
            &request.task_id,
            request.project_path.as_deref().map(Path::new),
        )
    })
    .await
}

#[tauri::command]
async fn task_cancel(request: tasks::TaskRequest) -> Result<Value, String> {
    blocking(move || {
        tasks::cancel(
            &request.task_id,
            request.project_path.as_deref().map(Path::new),
        )
    })
    .await
}

#[tauri::command]
async fn task_list(request: tasks::TaskListRequest) -> Result<Value, String> {
    blocking(move || tasks::list(Path::new(&request.project_path))).await
}

// ---- Analysis queries (contract R3/R6).

const ANALYSIS_OPERATIONS: [&str; 5] = [
    "analysis_summary",
    "analysis_contact_bins",
    "analysis_hybrids",
    "analysis_top",
    "compare_bins",
];

/// Accepts `{project_path, operation, ...}` for one of the read-only
/// analysis operations; everything else is forwarded unchanged for Python
/// to validate.
fn analysis_request(request: Value) -> Result<Value, String> {
    let Value::Object(object) = request else {
        return Err("analysis query must be a JSON object".into());
    };
    let operation = object.get("operation").and_then(Value::as_str).unwrap_or("");
    if !ANALYSIS_OPERATIONS.contains(&operation) {
        return Err(format!(
            "unsupported analysis operation; expected one of {}",
            ANALYSIS_OPERATIONS.join(", ")
        ));
    }
    if !object
        .get("project_path")
        .and_then(Value::as_str)
        .is_some_and(|p| !p.trim().is_empty())
    {
        return Err("analysis query needs a project_path".into());
    }
    Ok(Value::Object(object))
}

#[tauri::command]
async fn analysis_query(app: tauri::AppHandle, request: Value) -> Result<Value, String> {
    let request = analysis_request(request)?;
    dispatch(app, Lane::Compute, request).await
}

// ---- Project data operations (samples, references, structures).

/// Short control-lane operations on project.json; Python validates the rest.
const PROJECT_OPERATIONS: [&str; 13] = [
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
];

/// Accepts `{path, operation, ...}` for one of `PROJECT_OPERATIONS`.
fn project_op_request(request: Value) -> Result<Value, String> {
    let Value::Object(object) = request else {
        return Err("project operation must be a JSON object".into());
    };
    let operation = object.get("operation").and_then(Value::as_str).unwrap_or("");
    if !PROJECT_OPERATIONS.contains(&operation) {
        return Err(format!(
            "unsupported project operation; expected one of {}",
            PROJECT_OPERATIONS.join(", ")
        ));
    }
    if !object
        .get("path")
        .and_then(Value::as_str)
        .is_some_and(|p| !p.trim().is_empty())
    {
        return Err("project operation needs a project path".into());
    }
    Ok(Value::Object(object))
}

#[tauri::command]
async fn project_op(app: tauri::AppHandle, request: Value) -> Result<Value, String> {
    let request = project_op_request(request)?;
    dispatch(app, Lane::Control, request).await
}

#[derive(Debug, Deserialize)]
struct PickFilesRequest {
    title: Option<String>,
    multiple: Option<bool>,
}

/// Native file picker returning absolute paths (`{paths: []}` when cancelled).
/// Raw read files are linked in place, so only the path crosses into the UI.
#[tauri::command]
async fn dialog_pick_files(
    app: tauri::AppHandle,
    request: PickFilesRequest,
) -> Result<Value, String> {
    blocking(move || {
        let title = request.title.as_deref().unwrap_or("Choose files");
        let dialog = file_dialog(&app, title);
        let picked = if request.multiple.unwrap_or(true) {
            dialog.blocking_pick_files().unwrap_or_default()
        } else {
            dialog.blocking_pick_file().into_iter().collect()
        };
        let paths = picked
            .into_iter()
            .map(|path| dialog_path(Some(path)).map(|p| p.unwrap_or_default()))
            .collect::<Result<Vec<_>, _>>()?;
        Ok(json!({"paths": paths}))
    })
    .await
}

// ---- Preferences (resource budget, contract R6).

fn preferences_path(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    app.path()
        .app_config_dir()
        .map(|dir| dir.join(prefs::FILE_NAME))
        .map_err(|error| format!("cannot resolve the application config directory: {error}"))
}

#[tauri::command]
async fn preferences_get(app: tauri::AppHandle) -> Result<Value, String> {
    blocking(move || Ok(prefs::describe(&preferences_path(&app)?))).await
}

/// `{budget: {cpu?, mem_mb?} | null}`; the new budget applies at once and
/// starts queued tasks that now fit.
#[tauri::command]
async fn preferences_set(
    app: tauri::AppHandle,
    request: prefs::PreferencesSetRequest,
) -> Result<Value, String> {
    blocking(move || {
        let path = preferences_path(&app)?;
        tasks::set_budget(prefs::apply(&path, &request)?);
        Ok(prefs::describe(&path))
    })
    .await
}

// tauri.conf.json keeps `withGlobalTauri: true` on purpose: the frontend has
// no bundler and calls `window.__TAURI__.core.invoke` / `convertFileSrc`.
// Webview access is bounded by capabilities/main.json and the CSP.
fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .manage(Desktop::default())
        .setup(|app| {
            // A missing or invalid preferences file means the default budget.
            if let Ok(path) = preferences_path(app.handle()) {
                tasks::set_budget(prefs::load_budget(&path));
            }
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            runtime_detect,
            project_create,
            project_validate,
            project_open,
            project_import_input_dialog,
            project_read_input,
            project_save_analysis_run,
            project_update_ui_state,
            cplfold_predict,
            dialog_pick_project_folder,
            dialog_pick_new_project_folder,
            file_save_dialog,
            task_start,
            task_status,
            task_cancel,
            task_list,
            task_resume,
            analysis_query,
            project_op,
            dialog_pick_files,
            preferences_get,
            preferences_set
        ])
        .build(tauri::generate_context!())
        .expect("error while building HYB2 Desktop");
    app.run(|handle, event| match event {
        tauri::RunEvent::Ready => warm_up(handle.clone()),
        tauri::RunEvent::Exit => shutdown(handle),
        _ => {}
    });
}

/// Compiles the CPLfold Numba kernels in the compute sidecar right after
/// launch, so the first prediction does not pay the one-off JIT cost (about
/// 50 s on a fresh install; later launches load the on-disk cache).
fn warm_up(app: tauri::AppHandle) {
    std::thread::spawn(move || {
        if let Ok(rt) = runtime(&app) {
            let _ = rt.call(Lane::Compute, &json!({"operation": "warmup"}));
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn project_op_allowlist() {
        for operation in PROJECT_OPERATIONS {
            let request = json!({"operation": operation, "path": "/p"});
            assert_eq!(project_op_request(request.clone()).unwrap(), request);
        }
        for bad in [
            json!({"operation": "create_project", "path": "/p"}),
            json!({"operation": "run_task", "path": "/p"}),
            json!({"operation": "list_samples"}),
            json!({"operation": "list_samples", "path": " "}),
            json!(["list_samples"]),
        ] {
            assert!(project_op_request(bad).is_err());
        }
    }

    #[test]
    fn release_ignores_environment_overrides() {
        let root = Some(OsString::from("/attacker/repo"));
        let python = Some(OsString::from("/attacker/python"));
        assert_eq!(env_override(false, root.clone(), python.clone()), None);
        assert_eq!(
            env_override(true, root.clone(), python),
            Some(BridgeRuntime {
                root: "/attacker/repo".into(),
                python: "/attacker/python".into()
            })
        );
        assert_eq!(
            env_override(true, root, None).unwrap().python,
            PathBuf::from("python3")
        );
        assert_eq!(env_override(true, None, Some("p".into())), None);
    }

    #[test]
    fn bridge_result_keeps_unavailable_passthrough() {
        assert!(bridge_result(json!({"ok": true})).is_ok());
        assert!(bridge_result(json!({"ok": false, "status": "unavailable"})).is_ok());
        assert_eq!(
            bridge_result(json!({"ok": false, "error": "bad"})).unwrap_err(),
            "bad"
        );
    }

    #[test]
    fn analysis_queries_are_allowlisted() {
        for operation in ANALYSIS_OPERATIONS {
            let request = json!({"project_path": "/p", "operation": operation, "sample_ids": ["a"], "bin_size": 10});
            assert_eq!(analysis_request(request.clone()).unwrap(), request);
        }
        for bad in [
            json!({"project_path": "/p", "operation": "import_input"}),
            json!({"project_path": "/p", "operation": "sample_pipeline_run"}),
            json!({"project_path": "/p"}),
            json!({"operation": "analysis_summary"}),
            json!({"project_path": " ", "operation": "analysis_summary"}),
            json!(["analysis_summary"]),
        ] {
            assert!(analysis_request(bad).is_err());
        }
    }

    #[test]
    fn save_helpers() {
        assert_eq!(safe_file_name(Some("../../etc/passwd")), "passwd");
        assert_eq!(safe_file_name(Some("a\\b.csv")), "b.csv");
        assert_eq!(safe_file_name(Some("..")), "hyb2-export");
        assert_eq!(safe_file_name(None), "hyb2-export");
        assert_eq!(decode_payload("aGk=").unwrap(), b"hi");
        assert!(decode_payload("!!").is_err());
        assert!(decode_payload(&"A".repeat(MAX_SAVE_BYTES / 3 * 4 + 8)).is_err());
    }

    #[test]
    fn versions_are_in_sync() {
        let package: Value = serde_json::from_str(include_str!("../../package.json")).unwrap();
        assert_eq!(package["version"], env!("CARGO_PKG_VERSION"));
        let config: Value = serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        assert_eq!(config["version"], "../package.json");
    }

    #[test]
    fn config_has_strict_csp_and_asset_scope() {
        let config: Value = serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        let csp = config["app"]["security"]["csp"].as_str().unwrap();
        assert!(csp.contains("default-src 'self'"));
        let script = csp
            .split(';')
            .map(str::trim)
            .find(|d| d.starts_with("script-src"))
            .unwrap();
        assert!(!script.contains("'unsafe-inline'"));
        assert!(csp.contains("ipc: http://ipc.localhost"));
        assert_eq!(config["app"]["security"]["assetProtocol"]["enable"], true);
        assert_eq!(
            config["app"]["security"]["assetProtocol"]["scope"],
            json!([])
        );
    }
}
