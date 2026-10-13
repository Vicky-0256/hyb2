//! Allowlisted native task lifecycle. Commands from the UI are never executed.
use serde::Deserialize;
use serde_json::{json, Value};
use std::collections::HashMap;
use std::fs::{self, File};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

#[derive(Debug, Deserialize)]
pub struct TaskStartRequest {
    project_path: String,
    task_id: Option<String>,
    command: Vec<String>,
    cwd: Option<String>,
    sequence: Option<String>,
    bonus_matrix: Option<String>,
    parameters: Option<Value>,
}
#[derive(Debug, Deserialize)]
pub struct TaskRequest {
    task_id: String,
}
struct TaskState {
    status: String,
    exit_code: Option<i32>,
    error: Option<String>,
    child: Option<Child>,
    run_dir: PathBuf,
    finished: Option<SystemTime>,
}
static TASKS: OnceLock<Mutex<HashMap<String, TaskState>>> = OnceLock::new();
static COUNTER: AtomicU64 = AtomicU64::new(0);
const MAX_TASKS: usize = 256;
fn task_map() -> &'static Mutex<HashMap<String, TaskState>> {
    TASKS.get_or_init(|| Mutex::new(HashMap::new()))
}
fn task_id() -> String {
    format!(
        "task-{}-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos(),
        COUNTER.fetch_add(1, Ordering::Relaxed)
    )
}
fn validate_id(id: &str) -> Result<(), String> {
    if id.is_empty()
        || id.len() > 64
        || !id.as_bytes()[0].is_ascii_alphabetic()
        || !id
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || b"._-".contains(&c))
    {
        return Err("task id must start with a letter and contain at most 64 letters, digits, dots, underscores or hyphens".into());
    }
    Ok(())
}
fn snapshot(id: &str, entry: &TaskState) -> Value {
    json!({"ok":true,"task_id":id,"status":entry.status,"exit_code":entry.exit_code,"error":entry.error,"run_dir":entry.run_dir,"log_paths":[entry.run_dir.join("stdout.log"),entry.run_dir.join("stderr.log")]})
}
fn persist(id: &str, entry: &TaskState) -> Result<(), String> {
    let pending = entry.run_dir.join("status.tmp");
    fs::write(
        &pending,
        serde_json::to_vec_pretty(&snapshot(id, entry)).map_err(|e| e.to_string())?,
    )
    .map_err(|e| e.to_string())?;
    // On Windows rename cannot replace a destination. The UI reads the in-memory
    // status, so replacing the small on-disk record is safe for this local writer.
    #[cfg(windows)]
    if entry.run_dir.join("status.json").exists() {
        fs::remove_file(entry.run_dir.join("status.json")).map_err(|e| e.to_string())?;
    }
    fs::rename(pending, entry.run_dir.join("status.json")).map_err(|e| e.to_string())
}
fn start_managed(
    mut command: Command,
    project: &Path,
    id: String,
    reserved_run_dir: Option<PathBuf>,
) -> Result<Value, String> {
    validate_id(&id)?;
    let project = project
        .canonicalize()
        .map_err(|e| format!("project path is invalid: {e}"))?;
    let runs = project.join("runs");
    fs::create_dir_all(&runs).map_err(|e| e.to_string())?;
    let runs = runs.canonicalize().map_err(|e| e.to_string())?;
    if !runs.starts_with(&project) {
        return Err("project runs directory escapes project".into());
    }
    let mut map = task_map().lock().map_err(|_| "task state lock poisoned")?;
    if map.contains_key(&id) {
        return Err("task id is already in use".into());
    }
    if map.len() >= MAX_TASKS {
        let oldest = map
            .iter()
            .filter_map(|(id, e)| e.finished.map(|at| (id.clone(), at)))
            .min_by_key(|(_, at)| *at)
            .map(|(id, _)| id);
        if let Some(oldest) = oldest {
            map.remove(&oldest);
        } else {
            return Err("task limit reached; wait for an active task to finish".into());
        }
    }
    let run_dir = reserved_run_dir.clone().unwrap_or_else(|| runs.join(&id));
    if run_dir.parent() != Some(runs.as_path()) {
        return Err("task run directory escapes project".into());
    }
    if reserved_run_dir.is_none() {
        fs::create_dir(&run_dir)
            .map_err(|e| format!("cannot create unique task directory: {e}"))?;
    } else if !run_dir.is_dir() {
        return Err("reserved task run directory is missing".into());
    }
    command
        .stdin(Stdio::null())
        .stdout(Stdio::from(
            File::create(run_dir.join("stdout.log")).map_err(|e| e.to_string())?,
        ))
        .stderr(Stdio::from(
            File::create(run_dir.join("stderr.log")).map_err(|e| e.to_string())?,
        ));
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        command.process_group(0);
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x00000200);
    }
    let child = command.spawn();
    let (child, error) = match child {
        Ok(child) => (Some(child), None),
        Err(error) => (None, Some(format!("engine unavailable: {error}"))),
    };
    let mut entry = TaskState {
        status: if child.is_some() { "running" } else { "failed" }.into(),
        exit_code: None,
        error,
        child,
        run_dir,
        finished: None,
    };
    if entry.child.is_none() {
        entry.finished = Some(SystemTime::now());
    }
    if let Err(error) = persist(&id, &entry) {
        if let Some(child) = entry.child.as_mut() {
            let _ = child.kill();
            let _ = child.wait();
        }
        return Err(format!("cannot save task status: {error}"));
    }
    let result = snapshot(&id, &entry);
    let active = entry.child.is_some();
    map.insert(id.clone(), entry);
    drop(map);
    if active {
        std::thread::spawn(move || loop {
            let done = if let Ok(mut map) = task_map().lock() {
                if let Some(entry) = map.get_mut(&id) {
                    let outcome = entry.child.as_mut().map(Child::try_wait);
                    match outcome {
                        Some(Ok(None)) => false,
                        Some(Ok(Some(exit))) => {
                            entry.exit_code = exit.code();
                            if entry.status != "cancelled" {
                                entry.status = if exit.success() {
                                    "succeeded"
                                } else {
                                    "failed"
                                }
                                .into();
                            }
                            entry.child = None;
                            entry.finished = Some(SystemTime::now());
                            if let Err(error) = persist(&id, entry) {
                                entry.error = Some(error);
                            }
                            true
                        }
                        Some(Err(error)) => {
                            entry.error = Some(error.to_string());
                            entry.status = "failed".into();
                            if let Some(mut child) = entry.child.take() {
                                let _ = child.kill();
                                let _ = child.wait();
                            }
                            entry.finished = Some(SystemTime::now());
                            let _ = persist(&id, entry);
                            true
                        }
                        None => true,
                    }
                } else {
                    true
                }
            } else {
                true
            };
            if done {
                break;
            }
            std::thread::sleep(Duration::from_millis(50));
        });
    }
    Ok(result)
}

#[tauri::command]
pub async fn task_start(app: tauri::AppHandle, request: TaskStartRequest) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move||{
        if request.command != ["validate_project"] && request.command != ["cplfold"] {return Err("engine unavailable or not enabled: unsupported task kind".into());}
        let runtime=super::resolve_runtime(&app)?;
        let opened=super::bridge(super::resolve_runtime(&app)?,json!({"operation":"open_project","path":request.project_path}))?;
        let project=PathBuf::from(opened["path"].as_str().ok_or("project path missing")?);
        let cwd=request.cwd.map(PathBuf::from).unwrap_or_else(||project.clone()).canonicalize().map_err(|e|format!("invalid working directory: {e}"))?;
        if !cwd.is_dir() || !cwd.starts_with(&project) {return Err("task working directory must be inside the project".into());}
        let id=request.task_id.unwrap_or_else(|| if request.command == ["cplfold"] { format!("cplfold-{}", task_id()) } else { task_id() });
        let mut command=Command::new(&runtime.python);
        let reserved = if request.command == ["cplfold"] {
            let runs=project.join("runs"); fs::create_dir_all(&runs).map_err(|e|e.to_string())?; let runs=runs.canonicalize().map_err(|e|e.to_string())?;
            validate_id(&id)?; let dir=runs.join(&id); if dir.exists() { return Err("task id is already in use".into()); } fs::create_dir(&dir).map_err(|e|e.to_string())?;
            let seq=request.sequence.ok_or("cplfold sequence is required")?;
            let payload=json!({"operation":"cplfold_run","project_path":project,"sequence":seq,"bonus_matrix":request.bonus_matrix,"parameters":request.parameters.unwrap_or_else(||json!({})),"task_id":id,"run_dir":dir});
            fs::write(dir.join("task-request.json"), serde_json::to_vec(&payload).map_err(|e|e.to_string())?).map_err(|e|e.to_string())?;
            command.arg("-m").arg("desktop.runtime.bridge").arg("--repo-root").arg(&runtime.root).arg("--request").arg(dir.join("task-request.json")).current_dir(&runtime.root);
            Some(dir)
        } else {
            command.arg("-m").arg("desktop.runtime.bridge").arg("--repo-root").arg(&runtime.root).arg("--validate-project").arg(&project).current_dir(&runtime.root); None
        };
        start_managed(command,&project,id,reserved)
    }).await.map_err(|e|format!("task worker failed: {e}"))?
}
#[tauri::command]
pub async fn task_status(request: TaskRequest) -> Result<Value, String> {
    let map = task_map().lock().map_err(|_| "task state lock poisoned")?;
    Ok(snapshot(
        &request.task_id,
        map.get(&request.task_id)
            .ok_or("task not found (completed tasks are retained on disk)")?,
    ))
}
fn cancel(id: &str) -> Result<Value, String> {
    let mut map = task_map().lock().map_err(|_| "task state lock poisoned")?;
    let entry = map.get_mut(id).ok_or("task not found")?;
    if let Some(child) = entry.child.as_mut() {
        if let Some(exit) = child.try_wait().map_err(|e| e.to_string())? {
            entry.exit_code = exit.code();
            entry.status = if exit.success() {
                "succeeded"
            } else {
                "failed"
            }
            .into();
            entry.child = None;
            entry.finished = Some(SystemTime::now());
        } else {
            #[cfg(unix)]
            let result = Command::new("/bin/kill")
                .args(["-KILL", "--", &format!("-{}", child.id())])
                .stdout(Stdio::null())
                .stderr(Stdio::piped())
                .output();
            #[cfg(windows)]
            let result = Command::new("taskkill")
                .args(["/PID", &child.id().to_string(), "/T", "/F"])
                .stdout(Stdio::null())
                .stderr(Stdio::piped())
                .output();
            let result = result.map_err(|e| format!("could not cancel process tree: {e}"))?;
            if !result.status.success() {
                return Err(format!(
                    "could not cancel process tree: {}",
                    String::from_utf8_lossy(&result.stderr)
                ));
            }
            entry.status = "cancelled".into();
        }
        persist(id, entry)?;
    }
    Ok(snapshot(id, entry))
}
#[tauri::command]
pub async fn task_cancel(request: TaskRequest) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || cancel(&request.task_id))
        .await
        .map_err(|e| e.to_string())?
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn ids_are_unique_and_safe() {
        assert_ne!(task_id(), task_id());
        for id in ["../escape", "/tmp/a", "", "a/b"] {
            assert!(validate_id(id).is_err());
        }
    }
    #[cfg(unix)]
    #[test]
    fn cancellation_reaps_child_and_keeps_cancelled_status() {
        let project = std::env::temp_dir().join(task_id());
        fs::create_dir(&project).unwrap();
        let mut command = Command::new("/bin/sleep");
        command.arg("30");
        let id = task_id();
        start_managed(command, &project, id.clone(), None).unwrap();
        assert_eq!(cancel(&id).unwrap()["status"], "cancelled");
        for _ in 0..100 {
            if task_map().lock().unwrap()[&id].child.is_none() {
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        let map = task_map().lock().unwrap();
        let entry = &map[&id];
        assert!(entry.child.is_none());
        assert_eq!(entry.status, "cancelled");
        assert!(entry.run_dir.join("status.json").is_file());
        drop(map);
        fs::remove_dir_all(project).unwrap();
    }
}
