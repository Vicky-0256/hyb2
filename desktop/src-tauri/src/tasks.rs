//! Unified native task lifecycle (contract C3). Commands from the UI are never
//! executed: the UI chooses an allowlisted `kind`, Rust builds the command.
//! Rust is the only writer of `runs/<id>/status.json`; the child writes
//! `result.json`, which Rust folds into the snapshot's `output` at completion.
use serde::Deserialize;
use serde_json::{json, Value};
use std::collections::HashMap;
use std::ffi::OsString;
use std::fs::{self, File};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

pub const KINDS: [&str; 3] = ["validate_project", "cplfold", "pipeline"];
const ACTIVE: [&str; 2] = ["queued", "running"];

#[derive(Debug, Deserialize)]
pub struct TaskStartRequest {
    pub project_path: String,
    pub kind: String,
    pub task_id: Option<String>,
    pub sequence: Option<String>,
    pub bonus_matrix: Option<String>,
    pub parameters: Option<Value>,
    /// pipeline: project-relative reads (FASTQ/FASTA/SAM) and reference FASTA
    #[serde(default)]
    pub input: Option<String>,
    #[serde(default)]
    pub reference: Option<String>,
}
#[derive(Debug, Deserialize)]
pub struct TaskRequest {
    pub task_id: String,
    pub project_path: Option<String>,
}
#[derive(Debug, Deserialize)]
pub struct TaskListRequest {
    pub project_path: String,
}

/// Called with (project, task_id) after a task reaches a terminal state.
pub type OnFinish = Arc<dyn Fn(&Path, &str) + Send + Sync>;

struct TaskState {
    kind: String,
    status: String,
    exit_code: Option<i32>,
    error: Option<String>,
    child: Option<Child>,
    project: PathBuf,
    run_dir: PathBuf,
    started_at: String,
    finished_at: Option<String>,
    output: Value,
    finished: Option<SystemTime>,
}
static TASKS: OnceLock<Mutex<HashMap<String, TaskState>>> = OnceLock::new();
static COUNTER: AtomicU64 = AtomicU64::new(0);
const MAX_TASKS: usize = 256;
fn task_map() -> std::sync::MutexGuard<'static, HashMap<String, TaskState>> {
    TASKS
        .get_or_init(|| Mutex::new(HashMap::new()))
        .lock()
        .unwrap_or_else(|e| e.into_inner())
}
pub fn task_id() -> String {
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
pub fn validate_id(id: &str) -> Result<(), String> {
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
/// UTC timestamp `YYYY-MM-DDTHH:MM:SS.mmmZ` (lexicographically sortable).
fn iso(at: SystemTime) -> String {
    let d = at.duration_since(UNIX_EPOCH).unwrap_or_default();
    let (secs, ms) = (d.as_secs() as i64, d.subsec_millis());
    let (days, rem) = (secs.div_euclid(86_400), secs.rem_euclid(86_400));
    // Howard Hinnant's civil_from_days.
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}.{ms:03}Z",
        rem / 3600,
        rem / 60 % 60,
        rem % 60
    )
}
fn log_paths(run_dir: &Path) -> Value {
    json!([run_dir.join("stdout.log"), run_dir.join("stderr.log")])
}
fn snapshot(id: &str, entry: &TaskState) -> Value {
    json!({"ok":true,"task_id":id,"kind":entry.kind,"status":entry.status,"exit_code":entry.exit_code,"error":entry.error,"run_dir":entry.run_dir,"log_paths":log_paths(&entry.run_dir),"started_at":entry.started_at,"finished_at":entry.finished_at,"output":entry.output})
}
fn write_status(run_dir: &Path, value: &Value) -> Result<(), String> {
    let pending = run_dir.join("status.tmp");
    fs::write(
        &pending,
        serde_json::to_vec_pretty(value).map_err(|e| e.to_string())?,
    )
    .map_err(|e| e.to_string())?;
    // On Windows rename cannot replace a destination. Rust is the only writer
    // of this small record, so remove-then-rename is safe here.
    #[cfg(windows)]
    if run_dir.join("status.json").exists() {
        fs::remove_file(run_dir.join("status.json")).map_err(|e| e.to_string())?;
    }
    fs::rename(pending, run_dir.join("status.json")).map_err(|e| e.to_string())
}
fn persist(id: &str, entry: &TaskState) -> Result<(), String> {
    write_status(&entry.run_dir, &snapshot(id, entry))
}
fn canonical_project(project: &Path) -> Result<(PathBuf, PathBuf), String> {
    let project = project
        .canonicalize()
        .map_err(|e| format!("project path is invalid: {e}"))?;
    let runs = project.join("runs");
    fs::create_dir_all(&runs).map_err(|e| e.to_string())?;
    let runs = runs.canonicalize().map_err(|e| e.to_string())?;
    if !runs.starts_with(&project) || runs.parent() != Some(project.as_path()) {
        return Err("project runs directory escapes project".into());
    }
    Ok((project, runs))
}
/// Creates the unique `runs/<id>` directory for a new task.
pub fn prepare_run_dir(project: &Path, id: &str) -> Result<(PathBuf, PathBuf), String> {
    validate_id(id)?;
    let (project, runs) = canonical_project(project)?;
    if task_map().contains_key(id) {
        return Err("task id is already in use".into());
    }
    let run_dir = runs.join(id);
    fs::create_dir(&run_dir).map_err(|e| {
        if e.kind() == std::io::ErrorKind::AlreadyExists {
            "task id is already in use".to_string()
        } else {
            format!("cannot create unique task directory: {e}")
        }
    })?;
    Ok((project, run_dir))
}
/// Reads the child's `result.json` (written via `--result-file`), falling back
/// to the last JSON line the single-shot bridge printed to stdout.
fn read_result(run_dir: &Path) -> Value {
    if let Some(value) = fs::read(run_dir.join("result.json"))
        .ok()
        .and_then(|bytes| serde_json::from_slice::<Value>(&bytes).ok())
    {
        return value;
    }
    fs::read_to_string(run_dir.join("stdout.log"))
        .ok()
        .and_then(|text| {
            text.lines()
                .rev()
                .find(|l| !l.trim().is_empty())
                .and_then(|l| serde_json::from_str::<Value>(l).ok())
        })
        .filter(Value::is_object)
        .unwrap_or(Value::Null)
}
/// Snapshot `output`: the result's string `output` path (cplfold), else null.
fn output_field(result: &Value) -> Value {
    result
        .get("output")
        .filter(|v| v.is_string())
        .cloned()
        .unwrap_or(Value::Null)
}
fn kill_tree(child: &Child) -> Result<(), String> {
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
    Ok(())
}
pub fn start_managed(
    mut command: Command,
    project: &Path,
    id: String,
    kind: &str,
    run_dir: PathBuf,
    on_finish: OnFinish,
) -> Result<Value, String> {
    validate_id(&id)?;
    if run_dir.parent().and_then(Path::parent) != Some(project) {
        return Err("task run directory escapes project".into());
    }
    let mut map = task_map();
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
        // CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
        command.creation_flags(0x00000200 | 0x08000000);
    }
    let (child, error) = match command.spawn() {
        Ok(child) => (Some(child), None),
        Err(error) => (None, Some(format!("engine unavailable: {error}"))),
    };
    let now = SystemTime::now();
    let mut entry = TaskState {
        kind: kind.into(),
        status: if child.is_some() { "running" } else { "failed" }.into(),
        exit_code: None,
        error,
        child,
        project: project.to_path_buf(),
        run_dir,
        started_at: iso(now),
        finished_at: None,
        output: Value::Null,
        finished: None,
    };
    if entry.child.is_none() {
        entry.finished = Some(now);
        entry.finished_at = Some(iso(now));
    }
    if let Err(error) = persist(&id, &entry) {
        if let Some(child) = entry.child.as_mut() {
            let _ = kill_tree(child);
            let _ = child.wait();
        }
        return Err(format!("cannot save task status: {error}"));
    }
    let result = snapshot(&id, &entry);
    let active = entry.child.is_some();
    let project = entry.project.clone();
    map.insert(id.clone(), entry);
    drop(map);
    if active {
        std::thread::spawn(move || monitor(id, project, on_finish));
    } else {
        on_finish(&project, &id);
    }
    Ok(result)
}
fn monitor(id: String, project: PathBuf, on_finish: OnFinish) {
    let (run_dir, wait_error) = loop {
        std::thread::sleep(Duration::from_millis(50));
        let mut map = task_map();
        let Some(entry) = map.get_mut(&id) else {
            return;
        };
        let Some(child) = entry.child.as_mut() else {
            break (entry.run_dir.clone(), None);
        };
        match child.try_wait() {
            Ok(None) => continue,
            Ok(Some(exit)) => {
                entry.exit_code = exit.code();
                entry.child = None;
                break (entry.run_dir.clone(), None);
            }
            Err(error) => {
                if let Some(mut child) = entry.child.take() {
                    let _ = kill_tree(&child);
                    let _ = child.wait();
                }
                break (entry.run_dir.clone(), Some(error.to_string()));
            }
        }
    };
    // File reads happen without holding the task lock.
    let result = read_result(&run_dir);
    {
        let mut map = task_map();
        let Some(entry) = map.get_mut(&id) else {
            return;
        };
        if ACTIVE.contains(&entry.status.as_str()) {
            let ok = wait_error.is_none() && entry.exit_code == Some(0);
            entry.status = if ok { "succeeded" } else { "failed" }.into();
            if !ok && entry.error.is_none() {
                entry.error = wait_error.or_else(|| {
                    result
                        .get("error")
                        .and_then(Value::as_str)
                        .map(str::to_string)
                        .or_else(|| {
                            Some(match entry.exit_code {
                                Some(code) => format!("task exited with code {code}"),
                                None => "task was terminated by a signal".into(),
                            })
                        })
                });
            }
        }
        entry.output = output_field(&result);
        let now = SystemTime::now();
        entry.finished = Some(now);
        entry.finished_at = Some(iso(now));
        if let Err(error) = persist(&id, entry) {
            entry.error = Some(error);
        }
    }
    on_finish(&project, &id);
}
fn is_live(id: &str, run_dir: &Path) -> Option<Value> {
    let map = task_map();
    map.get(id)
        .filter(|e| e.run_dir == run_dir)
        .map(|e| snapshot(id, e))
}
/// Reads a task snapshot persisted by an earlier session. A `queued`/`running`
/// record without a live process in this session is marked `interrupted`.
fn read_disk(id: &str, run_dir: &Path) -> Result<Value, String> {
    let bytes = fs::read(run_dir.join("status.json")).map_err(|_| "task not found".to_string())?;
    let mut value: Value =
        serde_json::from_slice(&bytes).map_err(|e| format!("task status is unreadable: {e}"))?;
    let object = value
        .as_object_mut()
        .ok_or("task status is not a JSON object")?;
    object.insert("ok".into(), json!(true));
    object.insert("task_id".into(), json!(id));
    object.insert("run_dir".into(), json!(run_dir));
    object.insert("log_paths".into(), log_paths(run_dir));
    for key in [
        "kind",
        "exit_code",
        "error",
        "started_at",
        "finished_at",
        "output",
    ] {
        object.entry(key).or_insert(Value::Null);
    }
    let status = object
        .get("status")
        .and_then(Value::as_str)
        .unwrap_or("interrupted")
        .to_string();
    if ACTIVE.contains(&status.as_str()) || !object.contains_key("status") {
        object.insert("status".into(), json!("interrupted"));
        if object["error"].is_null() {
            object.insert(
                "error".into(),
                json!("the application stopped before this task finished"),
            );
        }
        if object["output"].is_null() {
            object.insert("output".into(), output_field(&read_result(run_dir)));
        }
        write_status(run_dir, &value)?;
    }
    Ok(value)
}
pub fn status(id: &str, project: Option<&Path>) -> Result<Value, String> {
    validate_id(id)?;
    if let Some(entry) = task_map().get(id) {
        return Ok(snapshot(id, entry));
    }
    let project = project.ok_or("task not found; pass project_path to read a finished task")?;
    let (_, runs) = canonical_project(project)?;
    read_disk(id, &runs.join(id))
}
pub fn list(project: &Path) -> Result<Value, String> {
    if !project.join("project.json").is_file() {
        return Err("project directory with project.json does not exist".into());
    }
    let (_, runs) = canonical_project(project)?;
    let mut tasks = Vec::new();
    for item in fs::read_dir(&runs).map_err(|e| e.to_string())? {
        let item = item.map_err(|e| e.to_string())?;
        let Some(id) = item.file_name().to_str().map(str::to_string) else {
            continue;
        };
        let run_dir = item.path();
        if validate_id(&id).is_err() || !run_dir.join("status.json").is_file() {
            continue;
        }
        if let Some(live) = is_live(&id, &run_dir) {
            tasks.push(live);
        } else if let Ok(value) = read_disk(&id, &run_dir) {
            tasks.push(value);
        }
    }
    let key = |v: &Value| v["started_at"].as_str().unwrap_or("").to_string();
    tasks.sort_by_key(|v| std::cmp::Reverse(key(v)));
    Ok(json!({"ok": true, "tasks": tasks}))
}
pub fn cancel(id: &str, project: Option<&Path>) -> Result<Value, String> {
    let mut map = task_map();
    let Some(entry) = map.get_mut(id) else {
        drop(map);
        return status(id, project);
    };
    if let Some(child) = entry.child.as_mut() {
        if child.try_wait().map_err(|e| e.to_string())?.is_none() {
            kill_tree(child)?;
            entry.status = "cancelled".into();
            entry.error = Some("cancelled by user".into());
            persist(id, entry)?;
        }
        // An already-exited child is finalized by the monitor thread.
    }
    Ok(snapshot(id, entry))
}
/// Kills every running task (application exit) and records it as interrupted.
pub fn shutdown_all() {
    let mut map = task_map();
    for (id, entry) in map.iter_mut() {
        if let Some(mut child) = entry.child.take() {
            let _ = kill_tree(&child);
            let _ = child.wait();
            entry.status = "interrupted".into();
            entry.error = Some("the application exited while this task was running".into());
            let now = SystemTime::now();
            entry.finished = Some(now);
            entry.finished_at = Some(iso(now));
            let _ = persist(id, entry);
        }
    }
}
/// Builds the allowlisted child request for `kind`.
pub fn task_payload(
    request: &TaskStartRequest,
    project: &Path,
    id: &str,
    run_dir: &Path,
) -> Result<Value, String> {
    match request.kind.as_str() {
        "validate_project" => Ok(
            json!({"operation":"validate_project","path":project,"check_files":true,"task_id":id,"run_dir":run_dir}),
        ),
        "cplfold" => {
            let sequence = request
                .sequence
                .as_deref()
                .filter(|s| !s.trim().is_empty())
                .ok_or("cplfold sequence is required")?;
            Ok(
                json!({"operation":"cplfold_run","project_path":project,"sequence":sequence,"bonus_matrix":request.bonus_matrix,"parameters":request.parameters.clone().unwrap_or_else(|| json!({})),"task_id":id,"run_dir":run_dir}),
            )
        }
        "pipeline" => {
            // Paths stay project-relative; the bridge rejects anything outside the project.
            let input = request.input.as_deref().filter(|s| !s.trim().is_empty()).ok_or("pipeline input is required")?;
            let reference = request.reference.as_deref().filter(|s| !s.trim().is_empty()).ok_or("pipeline reference is required")?;
            Ok(
                json!({"operation":"pipeline_run","project_path":project,"input":input,"reference":reference,"parameters":request.parameters.clone().unwrap_or_else(|| json!({})),"task_id":id,"run_dir":run_dir}),
            )
        }
        _ => Err("engine unavailable or not enabled: unsupported task kind".into()),
    }
}
/// Child command: single-shot bridge reading `task-request.json`.
pub fn bridge_command(
    python: &Path,
    root: &Path,
    request_file: &Path,
    envs: &[(String, OsString)],
) -> Command {
    let mut command = Command::new(python);
    command
        .arg("-m")
        .arg("desktop.runtime.bridge")
        .arg("--repo-root")
        .arg(root)
        .arg("--request")
        .arg(request_file)
        .arg("--result-file")
        .arg(request_file.with_file_name("result.json"))
        .current_dir(root);
    for (key, value) in envs {
        command.env(key, value);
    }
    command
}

#[cfg(test)]
mod tests {
    use super::*;
    fn noop() -> OnFinish {
        Arc::new(|_: &Path, _: &str| {})
    }
    fn temp_project() -> PathBuf {
        let project = std::env::temp_dir().join(task_id());
        fs::create_dir(&project).unwrap();
        fs::write(project.join("project.json"), "{}").unwrap();
        project.canonicalize().unwrap()
    }
    #[test]
    fn ids_are_unique_and_safe() {
        assert_ne!(task_id(), task_id());
        for id in ["../escape", "/tmp/a", "", "a/b"] {
            assert!(validate_id(id).is_err());
        }
    }
    #[test]
    fn iso_timestamps() {
        assert_eq!(iso(UNIX_EPOCH), "1970-01-01T00:00:00.000Z");
        let at = UNIX_EPOCH + Duration::from_millis(1_709_210_096_789);
        assert_eq!(iso(at), "2024-02-29T12:34:56.789Z");
    }
    #[test]
    fn unsupported_kind_is_rejected() {
        let request = TaskStartRequest {
            project_path: "/p".into(),
            kind: "sh".into(),
            task_id: None,
            sequence: None,
            bonus_matrix: None,
            parameters: None,
            input: None,
            reference: None,
        };
        assert!(task_payload(&request, Path::new("/p"), "a", Path::new("/p/runs/a")).is_err());
        let pipeline = TaskStartRequest { kind: "pipeline".into(), input: Some("inputs/a.sam".into()), reference: Some("inputs/r.fasta".into()), ..request };
        let payload = task_payload(&pipeline, Path::new("/p"), "a", Path::new("/p/runs/a")).unwrap();
        assert_eq!(payload["operation"], "pipeline_run");
        assert_eq!(payload["input"], "inputs/a.sam");
        let missing = TaskStartRequest { reference: None, ..pipeline };
        assert!(task_payload(&missing, Path::new("/p"), "a", Path::new("/p/runs/a")).is_err());
    }
    #[cfg(unix)]
    #[test]
    fn cancellation_reaps_child_and_keeps_cancelled_status() {
        let project = temp_project();
        let id = task_id();
        let (project, run_dir) = prepare_run_dir(&project, &id).unwrap();
        let mut command = Command::new("/bin/sleep");
        command.arg("30");
        let finished = Arc::new(AtomicU64::new(0));
        let flag = finished.clone();
        let on_finish: OnFinish = Arc::new(move |_: &Path, _: &str| {
            flag.fetch_add(1, Ordering::SeqCst);
        });
        start_managed(
            command,
            &project,
            id.clone(),
            "validate_project",
            run_dir,
            on_finish,
        )
        .unwrap();
        assert_eq!(cancel(&id, None).unwrap()["status"], "cancelled");
        for _ in 0..200 {
            if finished.load(Ordering::SeqCst) == 1 {
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        assert_eq!(finished.load(Ordering::SeqCst), 1);
        let snap = status(&id, None).unwrap();
        assert_eq!(snap["status"], "cancelled");
        assert!(snap["finished_at"].is_string());
        let disk: Value = serde_json::from_slice(
            &fs::read(project.join("runs").join(&id).join("status.json")).unwrap(),
        )
        .unwrap();
        assert_eq!(disk["status"], "cancelled");
        fs::remove_dir_all(project).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn completion_reads_result_json() {
        let project = temp_project();
        let id = task_id();
        let (project, run_dir) = prepare_run_dir(&project, &id).unwrap();
        let mut command = Command::new("/bin/sh");
        command
            .arg("-c")
            .arg(r#"printf '{"ok":true,"output":"x.out"}' > result.json"#)
            .current_dir(&run_dir);
        start_managed(command, &project, id.clone(), "cplfold", run_dir, noop()).unwrap();
        let mut snap = Value::Null;
        for _ in 0..200 {
            snap = status(&id, Some(&project)).unwrap();
            if snap["finished_at"].is_string() {
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        assert_eq!(snap["status"], "succeeded");
        assert_eq!(snap["kind"], "cplfold");
        assert_eq!(snap["output"], "x.out");
        assert_eq!(snap["exit_code"], 0);
        fs::remove_dir_all(project).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn failure_copies_result_error_and_null_output() {
        let project = temp_project();
        let id = task_id();
        let (project, run_dir) = prepare_run_dir(&project, &id).unwrap();
        let mut command = Command::new("/bin/sh");
        command
            .arg("-c")
            .arg(r#"printf '{"ok":false,"path":"p","errors":["bad"],"error":"bad"}' > result.json; exit 1"#)
            .current_dir(&run_dir);
        start_managed(
            command,
            &project,
            id.clone(),
            "validate_project",
            run_dir,
            noop(),
        )
        .unwrap();
        let mut snap = Value::Null;
        for _ in 0..200 {
            snap = status(&id, None).unwrap();
            if snap["finished_at"].is_string() {
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        assert_eq!(snap["status"], "failed");
        assert_eq!(snap["error"], "bad");
        assert_eq!(snap["exit_code"], 1);
        assert!(snap["output"].is_null());
        fs::remove_dir_all(project).unwrap();
    }
    #[test]
    fn child_command_passes_result_file() {
        let command = bridge_command(
            Path::new("py"),
            Path::new("/r"),
            Path::new("/p/runs/t/task-request.json"),
            &[],
        );
        let args: Vec<_> = command
            .get_args()
            .map(|a| a.to_string_lossy().into_owned())
            .collect();
        assert!(args
            .windows(2)
            .any(|w| w[0] == "--result-file" && w[1] == "/p/runs/t/result.json"));
    }
    #[test]
    fn list_marks_stale_running_as_interrupted() {
        let project = temp_project();
        let runs = project.join("runs");
        for (id, status, started) in [
            ("old-done", "succeeded", "2024-01-01T00:00:00.000Z"),
            ("stale-run", "running", "2024-06-01T00:00:00.000Z"),
        ] {
            fs::create_dir_all(runs.join(id)).unwrap();
            fs::write(
                runs.join(id).join("status.json"),
                json!({"task_id":id,"kind":"cplfold","status":status,"started_at":started})
                    .to_string(),
            )
            .unwrap();
        }
        fs::create_dir_all(runs.join("analysis-without-status")).unwrap();
        let listed = list(&project).unwrap();
        let tasks = listed["tasks"].as_array().unwrap();
        assert_eq!(tasks.len(), 2);
        assert_eq!(tasks[0]["task_id"], "stale-run");
        assert_eq!(tasks[0]["status"], "interrupted");
        assert_eq!(tasks[1]["status"], "succeeded");
        let disk: Value =
            serde_json::from_slice(&fs::read(runs.join("stale-run").join("status.json")).unwrap())
                .unwrap();
        assert_eq!(disk["status"], "interrupted");
        assert_eq!(
            status("stale-run", Some(&project)).unwrap()["status"],
            "interrupted"
        );
        assert!(status("missing", Some(&project)).is_err());
        fs::remove_dir_all(project).unwrap();
    }
}
