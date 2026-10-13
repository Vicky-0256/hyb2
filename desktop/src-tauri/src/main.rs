#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use serde::Deserialize;
use serde_json::{json, Value};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use tauri::Manager;

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
struct ImportInputRequest {
    path: String,
    source: String,
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
struct CplfoldRequest {
    project_path: String,
    sequence: String,
    bonus_matrix: Option<String>,
    parameters: Option<Value>,
    task_id: Option<String>,
}

#[derive(Debug, Deserialize)]
struct CplfoldPredictRequest {
    sequence: String,
    evidence_mode: Option<String>,
    evidence_arms: Option<Value>,
    parameters: Option<Value>,
}

mod tasks;
use tasks::{task_cancel, task_start, task_status};

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

fn resolve_runtime(app: &tauri::AppHandle) -> Result<BridgeRuntime, String> {
    if let Ok(value) = std::env::var("HYB2_REPOSITORY_ROOT") {
        let root = PathBuf::from(value);
        let python = std::env::var_os("HYB2_PYTHON")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from("python3"));
        return Ok(BridgeRuntime { root, python });
    }
    let resources = app
        .path()
        .resource_dir()
        .map_err(|error| format!("cannot resolve application resources: {error}"))?;
    if let Some(runtime) = bundled_runtime(&resources)? {
        return Ok(runtime);
    }
    if cfg!(debug_assertions) {
        let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..");
        if let Some(runtime) = bundled_runtime(&root.join("desktop/resources"))? {
            return Ok(runtime);
        }
        let python = std::env::var_os("HYB2_PYTHON")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from("python3"));
        return Ok(BridgeRuntime { root, python });
    }
    Err("Native runtime is not bundled. Rebuild with the runtime resources, or explicitly configure HYB2_REPOSITORY_ROOT and HYB2_PYTHON for development.".into())
}

fn bridge(runtime: BridgeRuntime, request: Value) -> Result<Value, String> {
    if !runtime.root.join("desktop/runtime/bridge.py").is_file() {
        return Err(format!(
            "Desktop runtime bridge is missing under {}",
            runtime.root.display()
        ));
    }
    let mut child = Command::new(&runtime.python)
        .arg("-m")
        .arg("desktop.runtime.bridge")
        .arg("--repo-root")
        .arg(&runtime.root)
        .current_dir(&runtime.root)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|error| {
            format!(
                "could not start desktop Python ({}): {error}",
                runtime.python.display()
            )
        })?;
    let encoded = serde_json::to_vec(&request)
        .map_err(|error| format!("could not encode bridge request: {error}"))?;
    let write_result = child
        .stdin
        .take()
        .ok_or_else(|| "bridge stdin unavailable".to_string())
        .and_then(|mut stdin| {
            stdin
                .write_all(&encoded)
                .and_then(|_| stdin.write_all(b"\n"))
                .map_err(|error| format!("could not send bridge request: {error}"))
        });
    if let Err(error) = write_result {
        let _ = child.kill();
        let _ = child.wait();
        return Err(error);
    }
    let output = child
        .wait_with_output()
        .map_err(|error| format!("bridge process failed: {error}"))?;
    let parsed: Value = serde_json::from_slice(&output.stdout).map_err(|error| {
        let detail: String = String::from_utf8_lossy(&output.stderr)
            .chars()
            .take(2000)
            .collect();
        format!(
            "invalid bridge response ({}, {error}): {detail}",
            output.status
        )
    })?;
    if !parsed.get("ok").and_then(Value::as_bool).unwrap_or(false)
        && parsed.get("status").and_then(Value::as_str) != Some("unavailable")
    {
        return Err(parsed
            .get("error")
            .and_then(Value::as_str)
            .unwrap_or("desktop runtime operation failed")
            .to_string());
    }
    if !output.status.success() {
        return Err(format!("bridge exited unsuccessfully: {}", output.status));
    }
    Ok(parsed)
}

async fn dispatch(app: tauri::AppHandle, request: Value) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || bridge(resolve_runtime(&app)?, request))
        .await
        .map_err(|error| format!("desktop worker failed: {error}"))?
}

#[tauri::command]
async fn runtime_detect(app: tauri::AppHandle) -> Result<Value, String> {
    dispatch(app, json!({"operation": "detect"})).await
}

#[tauri::command]
async fn project_create(
    app: tauri::AppHandle,
    request: CreateProjectRequest,
) -> Result<Value, String> {
    dispatch(app, json!({"operation": "create_project", "path": request.path, "name": request.name, "description": request.description.unwrap_or_default()})).await
}

#[tauri::command]
async fn project_validate(
    app: tauri::AppHandle,
    request: ValidateProjectRequest,
) -> Result<Value, String> {
    dispatch(app, json!({"operation": "validate_project", "path": request.path, "check_files": request.check_files.unwrap_or(false)})).await
}

#[tauri::command]
async fn project_open(
    app: tauri::AppHandle,
    request: ValidateProjectRequest,
) -> Result<Value, String> {
    dispatch(app, json!({"operation": "open_project", "path": request.path, "check_files": request.check_files.unwrap_or(false)})).await
}

#[tauri::command]
async fn project_import_input(
    app: tauri::AppHandle,
    request: ImportInputRequest,
) -> Result<Value, String> {
    dispatch(app, json!({"operation":"import_input", "path":request.path, "source":request.source, "role":request.role.unwrap_or_else(|| "other".into()), "label":request.label.unwrap_or_default()})).await
}

#[tauri::command]
async fn project_read_input(
    app: tauri::AppHandle,
    request: ReadInputRequest,
) -> Result<Value, String> {
    dispatch(app, json!({"operation":"read_input", "path":request.path, "relative":request.relative})).await
}

#[tauri::command]
async fn project_save_analysis_run(
    app: tauri::AppHandle,
    request: SaveAnalysisRunRequest,
) -> Result<Value, String> {
    dispatch(app, json!({"operation":"save_analysis_run", "path":request.path, "result":request.result})).await
}

#[tauri::command]
async fn project_update_ui_state(
    app: tauri::AppHandle,
    request: UpdateUiStateRequest,
) -> Result<Value, String> {
    dispatch(
        app,
        json!({"operation":"update_ui_state", "path":request.path, "ui_state":request.ui_state}),
    )
    .await
}

#[derive(Debug, Deserialize)]
struct RunTaskRequest {
    path: String,
    kind: Option<String>,
}

#[tauri::command]
async fn task_run(app: tauri::AppHandle, request: RunTaskRequest) -> Result<Value, String> {
    dispatch(app, json!({"operation": "run_task", "path": request.path, "kind": request.kind.unwrap_or_else(|| "validate_project".into())})).await
}

#[tauri::command]
async fn cplfold_run(app: tauri::AppHandle, request: CplfoldRequest) -> Result<Value, String> {
    let params = request.parameters.unwrap_or_else(|| json!({}));
    dispatch(app, json!({"operation":"cplfold_run", "project_path":request.project_path, "sequence":request.sequence, "bonus_matrix":request.bonus_matrix, "parameters":params, "task_id":request.task_id})).await
}

#[tauri::command]
async fn cplfold_predict(
    app: tauri::AppHandle,
    request: CplfoldPredictRequest,
) -> Result<Value, String> {
    dispatch(
        app,
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

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            runtime_detect,
            project_create,
            project_validate,
            project_open,
            project_import_input,
            project_read_input,
            project_save_analysis_run,
            project_update_ui_state,
            task_run,
            cplfold_run,
            cplfold_predict,
            task_start,
            task_status,
            task_cancel
        ])
        .run(tauri::generate_context!())
        .expect("error while running HYB2 Desktop");
}
