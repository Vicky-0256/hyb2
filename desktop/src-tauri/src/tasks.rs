//! Unified native task lifecycle (contracts C3 and R6). Commands from the UI
//! are never executed: the UI chooses an allowlisted `kind`, Rust builds the
//! command. Rust is the only writer of `runs/<id>/status.json`; the child
//! writes `result.json` (folded into the snapshot's `output` at completion)
//! and may write `progress.json` (passed through as `progress`).
//!
//! Scheduling (R6): every task claims `{cpu, mem_mb}` from one global budget.
//! A task whose claim does not fit what running tasks leave free is persisted
//! as `queued` and launched automatically, strictly FIFO: a waiting head
//! blocks later tasks, so a large task is never overtaken indefinitely.
use crate::prefs::{self, Budget};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::HashMap;
use std::ffi::OsString;
use std::fs::{self, File};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

pub const KINDS: [&str; 7] = [
    "validate_project",
    "cplfold",
    "pipeline",
    "sample_pipeline",
    "analysis_index",
    "structure",
    "compare",
];
const ACTIVE: [&str; 2] = ["queued", "running"];
/// Terminal states `task_resume` accepts.
const RESUMABLE: [&str; 3] = ["interrupted", "failed", "cancelled"];
/// The original `TaskStartRequest`, stored for `task_resume`.
pub const START_FILE: &str = "task-start.json";
const MAX_WINDOWS: usize = 4096;
const MAX_SAMPLES: usize = 256;

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
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
    /// sample_pipeline, analysis_index
    #[serde(default)]
    pub sample_id: Option<String>,
    /// structure (contract R5)
    #[serde(default)]
    pub sample_ids: Option<Vec<String>>,
    #[serde(default)]
    pub gene_id: Option<String>,
    #[serde(default)]
    pub mode: Option<String>,
    #[serde(default)]
    pub windows: Option<Vec<Window>>,
    #[serde(default)]
    pub workers: Option<u32>,
    /// compare: sample ids of condition one and condition two
    #[serde(default)]
    pub samples_one: Option<Vec<String>>,
    #[serde(default)]
    pub samples_two: Option<Vec<String>>,
    /// Resource claim overrides; must fit inside the budget.
    #[serde(default)]
    pub cpu: Option<u32>,
    #[serde(default)]
    pub mem_mb: Option<u64>,
}
#[derive(Clone, Copy, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Window {
    pub start: u64,
    pub end: u64,
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

/// A task waiting for budget: its command is spawned at launch time.
struct Pending {
    command: Command,
    on_finish: OnFinish,
}

struct TaskState {
    kind: String,
    status: String,
    exit_code: Option<i32>,
    error: Option<String>,
    child: Option<Child>,
    pending: Option<Pending>,
    project: PathBuf,
    run_dir: PathBuf,
    /// Submission time (`task_start`); list order.
    started_at: String,
    /// Process spawn time; null while queued.
    launched_at: Option<String>,
    finished_at: Option<String>,
    output: Value,
    finished: Option<SystemTime>,
    claim: Budget,
    seq: u64,
    resumed_from: Option<String>,
}
static TASKS: OnceLock<Mutex<HashMap<String, TaskState>>> = OnceLock::new();
static COUNTER: AtomicU64 = AtomicU64::new(0);
static SEQ: AtomicU64 = AtomicU64::new(0);
static BUDGET: Mutex<Option<Budget>> = Mutex::new(None);
static SHUTTING_DOWN: AtomicBool = AtomicBool::new(false);
const MAX_TASKS: usize = 256;
fn task_map() -> std::sync::MutexGuard<'static, HashMap<String, TaskState>> {
    TASKS
        .get_or_init(|| Mutex::new(HashMap::new()))
        .lock()
        .unwrap_or_else(|e| e.into_inner())
}
/// The scheduler budget (preferences, else the system default).
pub fn budget() -> Budget {
    let slot = BUDGET.lock().unwrap_or_else(|e| e.into_inner());
    slot.unwrap_or_else(prefs::default_budget)
}
/// Replaces the budget and starts queued tasks that now fit.
pub fn set_budget(budget: Budget) {
    *BUDGET.lock().unwrap_or_else(|e| e.into_inner()) = Some(budget);
    schedule();
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
/// Sample ids follow the project manifest's id rule (`project._ID`).
fn validate_sample_id(id: &str) -> Result<(), String> {
    validate_id(id).map_err(|_| {
        format!("invalid sample id {id:?}: it must start with a letter and contain at most 64 letters, digits, dots, underscores or hyphens")
    })
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
/// The persisted record. `progress` is not part of it: it is read from the
/// child's `progress.json` whenever a snapshot is returned.
fn snapshot(id: &str, entry: &TaskState) -> Value {
    json!({"ok":true,"task_id":id,"kind":entry.kind,"status":entry.status,"exit_code":entry.exit_code,"error":entry.error,"run_dir":entry.run_dir,"log_paths":log_paths(&entry.run_dir),"started_at":entry.started_at,"launched_at":entry.launched_at,"finished_at":entry.finished_at,"output":entry.output,"claim":entry.claim,"resumed_from":entry.resumed_from})
}
/// Adds `progress`: the parsed `run_dir/progress.json` object, else null.
/// Called without holding the task lock.
fn with_progress(mut value: Value) -> Value {
    let progress = value["run_dir"]
        .as_str()
        .and_then(|dir| fs::read(Path::new(dir).join("progress.json")).ok())
        .and_then(|bytes| serde_json::from_slice::<Value>(&bytes).ok())
        .filter(Value::is_object)
        .unwrap_or(Value::Null);
    if let Some(object) = value.as_object_mut() {
        object.insert("progress".into(), progress);
    }
    value
}
fn write_atomic(dir: &Path, name: &str, value: &Value) -> Result<(), String> {
    let pending = dir.join(format!("{name}.tmp"));
    fs::write(
        &pending,
        serde_json::to_vec_pretty(value).map_err(|e| e.to_string())?,
    )
    .map_err(|e| e.to_string())?;
    // On Windows rename cannot replace a destination. Rust is the only writer
    // of these small records, so remove-then-rename is safe here.
    #[cfg(windows)]
    if dir.join(name).exists() {
        fs::remove_file(dir.join(name)).map_err(|e| e.to_string())?;
    }
    fs::rename(pending, dir.join(name)).map_err(|e| e.to_string())
}
fn write_status(run_dir: &Path, value: &Value) -> Result<(), String> {
    write_atomic(run_dir, "status.json", value)
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

// ---- Resource claims.

/// Default claim per kind, clamped to the budget.
pub fn default_claim(kind: &str, budget: Budget) -> Budget {
    let (cpu, mem_mb) = match kind {
        "sample_pipeline" | "pipeline" | "structure" => (budget.cpu, 4096),
        "cplfold" => (1, 2048),
        "analysis_index" => (1, 1024),
        "compare" => (budget.cpu, 3072),
        _ => (1, 256),
    };
    clamp_claim(Budget { cpu, mem_mb }, budget)
}
fn clamp_claim(claim: Budget, budget: Budget) -> Budget {
    Budget {
        cpu: claim.cpu.clamp(1, budget.cpu.max(1)),
        mem_mb: claim.mem_mb.min(budget.mem_mb),
    }
}
/// The claim for a request: per-kind defaults (structure: `workers` cpus),
/// replaced by explicit `cpu` / `mem_mb`, which must fit inside the budget.
pub fn claim_for(request: &TaskStartRequest, budget: Budget) -> Result<Budget, String> {
    let mut claim = default_claim(&request.kind, budget);
    if request.kind == "structure" {
        if let Some(workers) = request.workers {
            claim.cpu = workers.clamp(1, budget.cpu.max(1));
        }
    }
    if let Some(cpu) = request.cpu {
        if cpu == 0 || cpu > budget.cpu {
            return Err(format!(
                "task cpu must be between 1 and the budget ({})",
                budget.cpu
            ));
        }
        claim.cpu = cpu;
    }
    if let Some(mem_mb) = request.mem_mb {
        if mem_mb == 0 || mem_mb > budget.mem_mb {
            return Err(format!(
                "task mem_mb must be between 1 and the budget ({})",
                budget.mem_mb
            ));
        }
        claim.mem_mb = mem_mb;
    }
    Ok(claim)
}

// ---- Submission and scheduling.

/// Everything Rust knows about a task before it is submitted.
pub struct Prepared {
    pub id: String,
    pub kind: String,
    pub project: PathBuf,
    pub run_dir: PathBuf,
    pub request_file: PathBuf,
    pub claim: Budget,
    pub resumed_from: Option<String>,
}

fn id_prefix(kind: &str) -> &'static str {
    match kind {
        "cplfold" => "cplfold-",
        "pipeline" => "pipeline-",
        "sample_pipeline" => "sample-",
        "analysis_index" => "index-",
        "structure" => "structure-",
        "compare" => "compare-",
        _ => "",
    }
}

/// Validates `request`, reserves `runs/<id>` in the (canonical, opened)
/// project and writes the child request plus the stored start request.
pub fn prepare(
    request: &TaskStartRequest,
    project: &Path,
    resumed_from: Option<&str>,
) -> Result<Prepared, String> {
    if !KINDS.contains(&request.kind.as_str()) {
        return Err("engine unavailable or not enabled: unsupported task kind".into());
    }
    let id = match request.task_id.clone() {
        Some(id) => id,
        None => format!("{}{}", id_prefix(&request.kind), task_id()),
    };
    validate_id(&id)?;
    let claim = claim_for(request, budget())?;
    // Validate the payload before reserving a run directory.
    task_payload(
        request,
        project,
        &id,
        &project.join("runs").join(&id),
        claim,
    )?;
    let (project, run_dir) = prepare_run_dir(project, &id)?;
    let payload = task_payload(request, &project, &id, &run_dir, claim)?;
    let request_file = run_dir.join("task-request.json");
    let mut stored = request.clone();
    stored.task_id = None;
    stored.project_path = project.display().to_string();
    let written = serde_json::to_vec(&payload)
        .map_err(|e| e.to_string())
        .and_then(|bytes| fs::write(&request_file, bytes).map_err(|e| e.to_string()))
        .and_then(|()| write_atomic(&run_dir, START_FILE, &json!(stored)));
    if let Err(error) = written {
        let _ = fs::remove_dir_all(&run_dir);
        return Err(format!("cannot save task request: {error}"));
    }
    Ok(Prepared {
        id,
        kind: request.kind.clone(),
        project,
        run_dir,
        request_file,
        claim,
        resumed_from: resumed_from.map(str::to_string),
    })
}

/// Test helper: starts a task with the default claim for `kind` (callers build
/// their own run directory).
#[cfg(test)]
pub fn start_managed(
    command: Command,
    project: &Path,
    id: String,
    kind: &str,
    run_dir: PathBuf,
    on_finish: OnFinish,
) -> Result<Value, String> {
    let prepared = Prepared {
        claim: default_claim(kind, budget()),
        kind: kind.into(),
        project: project.to_path_buf(),
        request_file: run_dir.join("task-request.json"),
        run_dir,
        id,
        resumed_from: None,
    };
    submit(command, prepared, on_finish)
}

/// Registers the task as `queued` and launches it at once when the budget
/// allows. The returned snapshot shows `running`, `queued` or `failed`.
pub fn submit(command: Command, task: Prepared, on_finish: OnFinish) -> Result<Value, String> {
    validate_id(&task.id)?;
    if task.run_dir.parent().and_then(Path::parent) != Some(task.project.as_path()) {
        return Err("task run directory escapes project".into());
    }
    if SHUTTING_DOWN.load(Ordering::SeqCst) {
        return Err("the application is shutting down".into());
    }
    let id = task.id;
    {
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
        let entry = TaskState {
            kind: task.kind,
            status: "queued".into(),
            exit_code: None,
            error: None,
            child: None,
            pending: Some(Pending { command, on_finish }),
            project: task.project,
            run_dir: task.run_dir,
            started_at: iso(SystemTime::now()),
            launched_at: None,
            finished_at: None,
            output: Value::Null,
            finished: None,
            claim: Budget {
                cpu: task.claim.cpu.max(1),
                mem_mb: task.claim.mem_mb,
            },
            seq: SEQ.fetch_add(1, Ordering::SeqCst),
            resumed_from: task.resumed_from,
        };
        persist(&id, &entry).map_err(|e| format!("cannot save task status: {e}"))?;
        map.insert(id.clone(), entry);
    }
    schedule();
    let snap = task_map()
        .get(&id)
        .map(|e| snapshot(&id, e))
        .ok_or("task disappeared")?;
    Ok(with_progress(snap))
}

/// Queued tasks to launch now, with their (clamped) claims: FIFO by `seq`,
/// stopping at the first claim that does not fit (head-of-line blocking).
/// Claims larger than the whole budget are clamped, so when nothing runs the
/// head always fits and the queue cannot deadlock.
fn admit(
    queued: &mut [(u64, String, Budget)],
    mut used: Budget,
    budget: Budget,
) -> Vec<(String, Budget)> {
    queued.sort_by_key(|(seq, _, _)| *seq);
    let mut launch = Vec::new();
    for (_, id, claim) in queued.iter() {
        let claim = clamp_claim(*claim, budget);
        if used.cpu + claim.cpu > budget.cpu || used.mem_mb + claim.mem_mb > budget.mem_mb {
            break;
        }
        used.cpu += claim.cpu;
        used.mem_mb += claim.mem_mb;
        launch.push((id.clone(), claim));
    }
    launch
}

/// Launches every queued task that fits the budget.
fn schedule() {
    if SHUTTING_DOWN.load(Ordering::SeqCst) {
        return;
    }
    let budget = budget();
    let mut monitors = Vec::new();
    let mut failed = Vec::new();
    {
        let mut map = task_map();
        let mut used = Budget { cpu: 0, mem_mb: 0 };
        let mut queued = Vec::new();
        for (id, entry) in map.iter() {
            if entry.status == "running" && entry.child.is_some() {
                used.cpu += entry.claim.cpu;
                used.mem_mb += entry.claim.mem_mb;
            } else if entry.status == "queued" && entry.pending.is_some() {
                queued.push((entry.seq, id.clone(), entry.claim));
            }
        }
        for (id, claim) in admit(&mut queued, used, budget) {
            let Some(entry) = map.get_mut(&id) else {
                continue;
            };
            let Some(pending) = entry.pending.take() else {
                continue;
            };
            entry.claim = claim;
            if launch(&id, entry, pending.command) {
                monitors.push((id, entry.project.clone(), pending.on_finish));
            } else {
                failed.push((entry.project.clone(), id, pending.on_finish));
            }
        }
    }
    for (id, project, on_finish) in monitors {
        std::thread::spawn(move || monitor(id, project, on_finish));
    }
    let retry = !failed.is_empty();
    for (project, id, on_finish) in failed {
        on_finish(&project, &id);
    }
    // A failed launch released its share; later tasks may fit now.
    if retry {
        schedule();
    }
}

fn spawn_child(command: &mut Command, run_dir: &Path, claim: Budget) -> Result<Child, String> {
    command
        .env("HYB2_TASK_CPUS", claim.cpu.to_string())
        .env("HYB2_TASK_MEM_MB", claim.mem_mb.to_string())
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
    command
        .spawn()
        .map_err(|error| format!("engine unavailable: {error}"))
}

/// Spawns the child of a queued task. Returns whether it is running.
fn launch(id: &str, entry: &mut TaskState, mut command: Command) -> bool {
    let now = SystemTime::now();
    entry.launched_at = Some(iso(now));
    match spawn_child(&mut command, &entry.run_dir, entry.claim) {
        Ok(child) => {
            entry.status = "running".into();
            entry.child = Some(child);
            if let Err(error) = persist(id, entry) {
                if let Some(mut child) = entry.child.take() {
                    let _ = kill_tree(&child);
                    let _ = child.wait();
                }
                fail(id, entry, format!("cannot save task status: {error}"), now);
                return false;
            }
            true
        }
        Err(error) => {
            fail(id, entry, error, now);
            false
        }
    }
}
fn fail(id: &str, entry: &mut TaskState, error: String, now: SystemTime) {
    entry.status = "failed".into();
    entry.error = Some(error);
    entry.finished = Some(now);
    entry.finished_at = Some(iso(now));
    let _ = persist(id, entry);
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
    schedule();
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
        "launched_at",
        "finished_at",
        "output",
        "claim",
        "resumed_from",
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
fn status_raw(id: &str, project: Option<&Path>) -> Result<Value, String> {
    validate_id(id)?;
    if let Some(entry) = task_map().get(id) {
        return Ok(snapshot(id, entry));
    }
    let project = project.ok_or("task not found; pass project_path to read a finished task")?;
    let (_, runs) = canonical_project(project)?;
    read_disk(id, &runs.join(id))
}
pub fn status(id: &str, project: Option<&Path>) -> Result<Value, String> {
    status_raw(id, project).map(with_progress)
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
            tasks.push(with_progress(live));
        } else if let Ok(value) = read_disk(&id, &run_dir) {
            tasks.push(with_progress(value));
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
    let mut cancelled_queued = None;
    if let Some(pending) = entry.pending.take() {
        // Queued: nothing to kill, and it never reaches a monitor thread.
        let now = SystemTime::now();
        entry.status = "cancelled".into();
        entry.error = Some("cancelled by user".into());
        entry.finished = Some(now);
        entry.finished_at = Some(iso(now));
        persist(id, entry)?;
        cancelled_queued = Some((entry.project.clone(), pending.on_finish));
    } else if let Some(child) = entry.child.as_mut() {
        if child.try_wait().map_err(|e| e.to_string())?.is_none() {
            kill_tree(child)?;
            entry.status = "cancelled".into();
            entry.error = Some("cancelled by user".into());
            persist(id, entry)?;
        }
        // An already-exited child is finalized by the monitor thread.
    }
    let snap = snapshot(id, entry);
    drop(map);
    if let Some((project, on_finish)) = cancelled_queued {
        on_finish(&project, id);
    }
    // A cancelled task no longer counts against the budget or blocks the queue.
    schedule();
    Ok(with_progress(snap))
}
/// Kills every running task (application exit) and records running and
/// queued tasks as interrupted, so they can be resumed next session.
pub fn shutdown_all() {
    SHUTTING_DOWN.store(true, Ordering::SeqCst);
    let mut map = task_map();
    for (id, entry) in map.iter_mut() {
        let message = if entry.pending.take().is_some() {
            "the application exited before this queued task started"
        } else if let Some(mut child) = entry.child.take() {
            let _ = kill_tree(&child);
            let _ = child.wait();
            "the application exited while this task was running"
        } else {
            continue;
        };
        entry.status = "interrupted".into();
        entry.error = Some(message.into());
        let now = SystemTime::now();
        entry.finished = Some(now);
        entry.finished_at = Some(iso(now));
        let _ = persist(id, entry);
    }
}

/// The stored start request of a task in `project` that may be resumed
/// (`interrupted`, `failed` or `cancelled`).
pub fn resume_request(id: &str, project: &Path) -> Result<TaskStartRequest, String> {
    let (project, runs) = canonical_project(project)?;
    let snap = status_raw(id, Some(&project))?;
    let run_dir = runs.join(id);
    if snap["run_dir"].as_str().map(Path::new) != Some(run_dir.as_path()) {
        return Err("task does not belong to this project".into());
    }
    let state = snap["status"].as_str().unwrap_or("");
    if !RESUMABLE.contains(&state) {
        return Err(format!(
            "only interrupted, failed or cancelled tasks can be resumed (this task is {state})"
        ));
    }
    let bytes = fs::read(run_dir.join(START_FILE)).map_err(|_| {
        "this task cannot be resumed: its original request was not stored".to_string()
    })?;
    let mut request: TaskStartRequest = serde_json::from_slice(&bytes)
        .map_err(|e| format!("stored task request is unreadable: {e}"))?;
    request.task_id = None;
    request.project_path = project.display().to_string();
    Ok(request)
}

// ---- Payloads.

fn required<'a>(value: Option<&'a str>, message: &str) -> Result<&'a str, String> {
    value
        .filter(|s| !s.trim().is_empty())
        .ok_or_else(|| message.to_string())
}
fn object_parameters(request: &TaskStartRequest) -> Result<Value, String> {
    match &request.parameters {
        None | Some(Value::Null) => Ok(json!({})),
        Some(value @ Value::Object(_)) => Ok(value.clone()),
        Some(_) => Err("task parameters must be an object".into()),
    }
}
fn validate_gene_id(gene: &str) -> Result<(), String> {
    if gene.trim().is_empty() || gene.len() > 256 || gene.chars().any(char::is_control) {
        return Err("structure gene_id must be a non-empty name of at most 256 characters".into());
    }
    Ok(())
}
fn structure_payload(
    request: &TaskStartRequest,
    project: &Path,
    id: &str,
    run_dir: &Path,
    claim: Budget,
) -> Result<Value, String> {
    let samples = request
        .sample_ids
        .as_deref()
        .filter(|s| !s.is_empty())
        .ok_or("structure sample_ids must list at least one sample")?;
    if samples.len() > MAX_SAMPLES {
        return Err(format!("structure accepts at most {MAX_SAMPLES} samples"));
    }
    for (index, sample) in samples.iter().enumerate() {
        validate_sample_id(sample)?;
        if samples[..index].contains(sample) {
            return Err(format!("structure sample id {sample:?} is duplicated"));
        }
    }
    let gene = required(request.gene_id.as_deref(), "structure gene_id is required")?;
    validate_gene_id(gene)?;
    let mode = request
        .mode
        .as_deref()
        .ok_or("structure mode is required (genome or windows)")?;
    let windows = request.windows.as_deref().unwrap_or(&[]);
    match mode {
        "genome" if !windows.is_empty() => {
            return Err("structure windows are only allowed in windows mode".into())
        }
        "windows" if windows.is_empty() => {
            return Err("structure windows mode needs at least one window".into())
        }
        "genome" | "windows" => {}
        _ => return Err("structure mode must be genome or windows".into()),
    }
    if windows.len() > MAX_WINDOWS {
        return Err(format!("structure accepts at most {MAX_WINDOWS} windows"));
    }
    if let Some(bad) = windows.iter().find(|w| w.start >= w.end) {
        return Err(format!(
            "structure window {}-{} must have start < end",
            bad.start, bad.end
        ));
    }
    if request.workers == Some(0) {
        return Err("structure workers must be at least 1".into());
    }
    // Never more workers than the cpus granted to the task.
    let workers = request.workers.unwrap_or(claim.cpu).min(claim.cpu).max(1);
    Ok(json!({
        "operation":"structure_run","project_path":project,"sample_ids":samples,
        "gene_id":gene,"mode":mode,"windows":windows,"parameters":object_parameters(request)?,
        "workers":workers,"task_id":id,"run_dir":run_dir
    }))
}

fn compare_payload(
    request: &TaskStartRequest,
    project: &Path,
    id: &str,
    run_dir: &Path,
) -> Result<Value, String> {
    let one = request
        .samples_one
        .as_deref()
        .filter(|s| !s.is_empty())
        .ok_or("compare samples_one must list at least one sample")?;
    let two = request
        .samples_two
        .as_deref()
        .filter(|s| !s.is_empty())
        .ok_or("compare samples_two must list at least one sample")?;
    if one.len() + two.len() > MAX_SAMPLES {
        return Err(format!("compare accepts at most {MAX_SAMPLES} samples"));
    }
    if one.len() + two.len() < 3 {
        return Err(
            "compare needs at least three samples (two replicates in one condition)".into(),
        );
    }
    let all: Vec<&String> = one.iter().chain(two.iter()).collect();
    for (index, sample) in all.iter().enumerate() {
        validate_sample_id(sample)?;
        if all[..index].contains(sample) {
            return Err(format!("compare sample id {sample:?} is listed twice"));
        }
    }
    Ok(json!({
        "operation":"compare_run","project_path":project,"samples_one":one,"samples_two":two,
        "parameters":object_parameters(request)?,"task_id":id,"run_dir":run_dir
    }))
}

/// Builds the allowlisted child request for `kind`.
pub fn task_payload(
    request: &TaskStartRequest,
    project: &Path,
    id: &str,
    run_dir: &Path,
    claim: Budget,
) -> Result<Value, String> {
    match request.kind.as_str() {
        "validate_project" => Ok(
            json!({"operation":"validate_project","path":project,"check_files":true,"task_id":id,"run_dir":run_dir}),
        ),
        "cplfold" => {
            let sequence = required(request.sequence.as_deref(), "cplfold sequence is required")?;
            Ok(
                json!({"operation":"cplfold_run","project_path":project,"sequence":sequence,"bonus_matrix":request.bonus_matrix,"parameters":request.parameters.clone().unwrap_or_else(|| json!({})),"task_id":id,"run_dir":run_dir}),
            )
        }
        "pipeline" => {
            // Paths stay project-relative; the bridge rejects anything outside the project.
            let input = required(request.input.as_deref(), "pipeline input is required")?;
            let reference = required(
                request.reference.as_deref(),
                "pipeline reference is required",
            )?;
            Ok(
                json!({"operation":"pipeline_run","project_path":project,"input":input,"reference":reference,"parameters":request.parameters.clone().unwrap_or_else(|| json!({})),"task_id":id,"run_dir":run_dir}),
            )
        }
        "sample_pipeline" | "analysis_index" => {
            let sample = required(
                request.sample_id.as_deref(),
                &format!("{} sample_id is required", request.kind),
            )?;
            validate_sample_id(sample)?;
            let operation = if request.kind == "sample_pipeline" {
                "sample_pipeline_run"
            } else {
                "analysis_index_build"
            };
            Ok(
                json!({"operation":operation,"project_path":project,"sample_id":sample,"task_id":id,"run_dir":run_dir}),
            )
        }
        "structure" => structure_payload(request, project, id, run_dir, claim),
        "compare" => compare_payload(request, project, id, run_dir),
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

    /// Tests that start processes share the global task table and budget.
    static SERIAL: Mutex<()> = Mutex::new(());
    fn serial() -> std::sync::MutexGuard<'static, ()> {
        SERIAL.lock().unwrap_or_else(|e| e.into_inner())
    }
    /// Restores the default budget when a test ends (even on panic).
    struct BudgetGuard;
    impl Drop for BudgetGuard {
        fn drop(&mut self) {
            *BUDGET.lock().unwrap_or_else(|e| e.into_inner()) = None;
        }
    }
    fn with_budget(cpu: u32, mem_mb: u64) -> BudgetGuard {
        set_budget(Budget { cpu, mem_mb });
        BudgetGuard
    }
    fn noop() -> OnFinish {
        Arc::new(|_: &Path, _: &str| {})
    }
    fn temp_project() -> PathBuf {
        let project = std::env::temp_dir().join(task_id());
        fs::create_dir(&project).unwrap();
        fs::write(project.join("project.json"), "{}").unwrap();
        project.canonicalize().unwrap()
    }
    fn wait_for(id: &str, project: &Path, done: impl Fn(&Value) -> bool) -> Value {
        let mut snap = Value::Null;
        for _ in 0..250 {
            snap = status(id, Some(project)).unwrap();
            if done(&snap) {
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        snap
    }
    fn big() -> Budget {
        Budget {
            cpu: 4,
            mem_mb: 8000,
        }
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
            ..Default::default()
        };
        let run = Path::new("/p/runs/a");
        assert!(task_payload(&request, Path::new("/p"), "a", run, big()).is_err());
        let pipeline = TaskStartRequest {
            kind: "pipeline".into(),
            input: Some("inputs/a.sam".into()),
            reference: Some("inputs/r.fasta".into()),
            ..request
        };
        let payload = task_payload(&pipeline, Path::new("/p"), "a", run, big()).unwrap();
        assert_eq!(payload["operation"], "pipeline_run");
        assert_eq!(payload["input"], "inputs/a.sam");
        let missing = TaskStartRequest {
            reference: None,
            ..pipeline
        };
        assert!(task_payload(&missing, Path::new("/p"), "a", run, big()).is_err());
    }
    #[test]
    fn sample_kinds_map_to_bridge_operations() {
        let run = Path::new("/p/runs/a");
        for (kind, operation) in [
            ("sample_pipeline", "sample_pipeline_run"),
            ("analysis_index", "analysis_index_build"),
        ] {
            let mut request = TaskStartRequest {
                project_path: "/p".into(),
                kind: kind.into(),
                ..Default::default()
            };
            assert!(task_payload(&request, Path::new("/p"), "a", run, big()).is_err());
            for bad in ["", "  ", "../x", "1abc", "a/b", &"a".repeat(65)] {
                request.sample_id = Some(bad.into());
                assert!(
                    task_payload(&request, Path::new("/p"), "a", run, big()).is_err(),
                    "{kind} accepted {bad:?}"
                );
            }
            request.sample_id = Some("ZAFR_REP1".into());
            let payload = task_payload(&request, Path::new("/p"), "a", run, big()).unwrap();
            assert_eq!(
                payload,
                json!({"operation":operation,"project_path":"/p","sample_id":"ZAFR_REP1","task_id":"a","run_dir":"/p/runs/a"})
            );
        }
    }
    #[test]
    fn compare_requests_are_validated() {
        let run = Path::new("/p/runs/a");
        let good = TaskStartRequest {
            project_path: "/p".into(),
            kind: "compare".into(),
            samples_one: Some(vec!["ZAFR_REP1".into(), "ZAFR_REP2".into()]),
            samples_two: Some(vec!["ZBRA_REP1".into(), "ZBRA_REP2".into()]),
            parameters: Some(json!({"min_count": 2})),
            ..Default::default()
        };
        assert_eq!(
            claim_for(&good, big()).unwrap(),
            Budget {
                cpu: big().cpu,
                mem_mb: 3072
            }
        );
        assert_eq!(id_prefix("compare"), "compare-");
        assert_eq!(
            task_payload(&good, Path::new("/p"), "a", run, big()).unwrap(),
            json!({"operation":"compare_run","project_path":"/p","samples_one":["ZAFR_REP1","ZAFR_REP2"],
                   "samples_two":["ZBRA_REP1","ZBRA_REP2"],"parameters":{"min_count":2},"task_id":"a","run_dir":"/p/runs/a"})
        );
        let bad = [
            TaskStartRequest {
                samples_one: None,
                ..good.clone()
            },
            TaskStartRequest {
                samples_two: Some(vec![]),
                ..good.clone()
            },
            TaskStartRequest {
                samples_two: Some(vec!["ZBRA_REP1".into()]),
                samples_one: Some(vec!["ZAFR_REP1".into()]),
                ..good.clone()
            },
            TaskStartRequest {
                samples_two: Some(vec!["ZAFR_REP1".into(), "ZBRA_REP1".into()]),
                ..good.clone()
            },
            TaskStartRequest {
                samples_one: Some(vec!["../x".into(), "ZAFR_REP2".into()]),
                ..good.clone()
            },
            TaskStartRequest {
                parameters: Some(json!([1])),
                ..good.clone()
            },
        ];
        for request in bad {
            assert!(
                task_payload(&request, Path::new("/p"), "a", run, big()).is_err(),
                "{request:?}"
            );
        }
    }
    #[test]
    fn structure_requests_are_validated() {
        let run = Path::new("/p/runs/a");
        let good = TaskStartRequest {
            project_path: "/p".into(),
            kind: "structure".into(),
            sample_ids: Some(vec!["ZAFR_REP1".into(), "ZAFR_REP2".into()]),
            gene_id: Some("ZIKV_African".into()),
            mode: Some("windows".into()),
            windows: Some(vec![Window {
                start: 100,
                end: 400,
            }]),
            parameters: Some(json!({"pseudoknots": true})),
            workers: Some(2),
            ..Default::default()
        };
        let claim = claim_for(&good, big()).unwrap();
        assert_eq!(
            claim,
            Budget {
                cpu: 2,
                mem_mb: 4096
            }
        );
        let payload = task_payload(&good, Path::new("/p"), "a", run, claim).unwrap();
        assert_eq!(
            payload,
            json!({"operation":"structure_run","project_path":"/p","sample_ids":["ZAFR_REP1","ZAFR_REP2"],
                   "gene_id":"ZIKV_African","mode":"windows","windows":[{"start":100,"end":400}],
                   "parameters":{"pseudoknots":true},"workers":2,"task_id":"a","run_dir":"/p/runs/a"})
        );
        let genome = TaskStartRequest {
            mode: Some("genome".into()),
            windows: None,
            workers: None,
            parameters: None,
            ..good.clone()
        };
        let claim = claim_for(&genome, big()).unwrap();
        assert_eq!(claim.cpu, 4);
        let payload = task_payload(&genome, Path::new("/p"), "a", run, claim).unwrap();
        assert_eq!(payload["windows"], json!([]));
        assert_eq!(payload["workers"], 4);
        assert_eq!(payload["parameters"], json!({}));
        let bad: Vec<TaskStartRequest> = vec![
            TaskStartRequest {
                sample_ids: None,
                ..good.clone()
            },
            TaskStartRequest {
                sample_ids: Some(vec![]),
                ..good.clone()
            },
            TaskStartRequest {
                sample_ids: Some(vec!["../x".into()]),
                ..good.clone()
            },
            TaskStartRequest {
                sample_ids: Some(vec!["a".into(), "a".into()]),
                ..good.clone()
            },
            TaskStartRequest {
                gene_id: None,
                ..good.clone()
            },
            TaskStartRequest {
                gene_id: Some("a\nb".into()),
                ..good.clone()
            },
            TaskStartRequest {
                mode: None,
                ..good.clone()
            },
            TaskStartRequest {
                mode: Some("both".into()),
                ..good.clone()
            },
            TaskStartRequest {
                windows: None,
                ..good.clone()
            },
            TaskStartRequest {
                windows: Some(vec![Window { start: 5, end: 5 }]),
                ..good.clone()
            },
            TaskStartRequest {
                mode: Some("genome".into()),
                ..good.clone()
            },
            TaskStartRequest {
                workers: Some(0),
                ..good.clone()
            },
            TaskStartRequest {
                parameters: Some(json!([1])),
                ..good.clone()
            },
        ];
        for request in bad {
            assert!(
                task_payload(&request, Path::new("/p"), "a", run, big()).is_err(),
                "accepted {request:?}"
            );
        }
        // Unknown window fields are rejected at deserialization.
        assert!(serde_json::from_value::<TaskStartRequest>(
            json!({"project_path":"/p","kind":"structure","windows":[{"start":1,"end":2,"x":3}]})
        )
        .is_err());
    }
    #[test]
    fn claims_have_kind_defaults_and_bounded_overrides() {
        let budget = big();
        let request = |kind: &str| TaskStartRequest {
            kind: kind.into(),
            ..Default::default()
        };
        let claim = |r: &TaskStartRequest| claim_for(r, budget).unwrap();
        assert_eq!(
            claim(&request("sample_pipeline")),
            Budget {
                cpu: 4,
                mem_mb: 4096
            }
        );
        assert_eq!(
            claim(&request("structure")),
            Budget {
                cpu: 4,
                mem_mb: 4096
            }
        );
        assert_eq!(
            claim(&request("cplfold")),
            Budget {
                cpu: 1,
                mem_mb: 2048
            }
        );
        assert_eq!(
            claim(&request("validate_project")),
            Budget {
                cpu: 1,
                mem_mb: 256
            }
        );
        assert_eq!(
            claim(&request("analysis_index")),
            Budget {
                cpu: 1,
                mem_mb: 1024
            }
        );
        let structure = TaskStartRequest {
            workers: Some(64),
            mem_mb: Some(6000),
            ..request("structure")
        };
        assert_eq!(
            claim(&structure),
            Budget {
                cpu: 4,
                mem_mb: 6000
            }
        );
        let overridden = TaskStartRequest {
            cpu: Some(2),
            mem_mb: Some(100),
            ..request("sample_pipeline")
        };
        assert_eq!(
            claim(&overridden),
            Budget {
                cpu: 2,
                mem_mb: 100
            }
        );
        for bad in [
            TaskStartRequest {
                cpu: Some(0),
                ..request("cplfold")
            },
            TaskStartRequest {
                cpu: Some(5),
                ..request("cplfold")
            },
            TaskStartRequest {
                mem_mb: Some(0),
                ..request("cplfold")
            },
            TaskStartRequest {
                mem_mb: Some(8001),
                ..request("cplfold")
            },
        ] {
            assert!(claim_for(&bad, budget).is_err());
        }
        // Defaults never exceed a small budget.
        let small = Budget {
            cpu: 1,
            mem_mb: 512,
        };
        assert_eq!(
            claim_for(&request("sample_pipeline"), small).unwrap(),
            Budget {
                cpu: 1,
                mem_mb: 512
            }
        );
    }
    #[test]
    fn admission_is_fifo_with_head_blocking() {
        let budget = big();
        let b = |cpu, mem_mb| Budget { cpu, mem_mb };
        let idle = b(0, 0);
        let ids = |v: Vec<(String, Budget)>| v.into_iter().map(|(id, _)| id).collect::<Vec<_>>();
        // Submission order is seq, not slice order.
        let mut queue = vec![
            (2, "c".to_string(), b(1, 100)),
            (0, "a".to_string(), b(3, 100)),
            (1, "b".to_string(), b(3, 100)),
        ];
        // `b` does not fit after `a`; the small `c` must not overtake it.
        assert_eq!(ids(admit(&mut queue, idle, budget)), ["a"]);
        assert!(admit(&mut queue, b(2, 0), budget).is_empty());
        assert_eq!(ids(admit(&mut queue, b(1, 0), budget)), ["a"]);
        // Memory blocks as well as cpu.
        let mut queue = vec![
            (0, "m".to_string(), b(1, 5000)),
            (1, "n".to_string(), b(1, 10)),
        ];
        assert!(admit(&mut queue, b(0, 4000), budget).is_empty());
        assert_eq!(ids(admit(&mut queue, idle, budget)), ["m", "n"]);
        // An oversized claim is clamped and runs alone instead of deadlocking.
        let mut queue = vec![(0, "huge".to_string(), b(64, 1 << 20))];
        assert_eq!(
            admit(&mut queue, idle, budget),
            vec![("huge".to_string(), budget)]
        );
        assert!(admit(&mut queue, b(1, 0), budget).is_empty());
    }
    #[cfg(unix)]
    fn sleeper(run_dir: &Path) -> Command {
        let mut command = Command::new("/bin/sh");
        command
            .arg("-c")
            .arg("printf '%s %s' \"$HYB2_TASK_CPUS\" \"$HYB2_TASK_MEM_MB\" > granted.txt; exec sleep 30")
            .current_dir(run_dir);
        command
    }
    #[cfg(unix)]
    fn submit_sleeper(project: &Path, claim: Budget, on_finish: OnFinish) -> (String, Value) {
        let id = task_id();
        let (project, run_dir) = prepare_run_dir(project, &id).unwrap();
        let task = Prepared {
            id: id.clone(),
            kind: "structure".into(),
            project,
            request_file: run_dir.join("task-request.json"),
            run_dir: run_dir.clone(),
            claim,
            resumed_from: None,
        };
        let snap = submit(sleeper(&run_dir), task, on_finish).unwrap();
        (id, snap)
    }
    #[cfg(unix)]
    fn granted(project: &Path, id: &str) -> String {
        let file = project.join("runs").join(id).join("granted.txt");
        for _ in 0..250 {
            if let Ok(text) = fs::read_to_string(&file) {
                if !text.is_empty() {
                    return text;
                }
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        String::new()
    }
    #[cfg(unix)]
    #[test]
    fn over_budget_tasks_queue_and_start_in_order() {
        let _serial = serial();
        let _budget = with_budget(2, 1000);
        let project = temp_project();
        let finished = Arc::new(Mutex::new(Vec::<String>::new()));
        let log = finished.clone();
        let on_finish: OnFinish = Arc::new(move |_: &Path, id: &str| {
            log.lock().unwrap().push(id.to_string());
        });
        let b = |cpu, mem_mb| Budget { cpu, mem_mb };
        let (first, snap) = submit_sleeper(&project, b(2, 100), on_finish.clone());
        assert_eq!(snap["status"], "running");
        assert!(snap["launched_at"].is_string());
        assert_eq!(granted(&project, &first), "2 100");
        let (second, snap) = submit_sleeper(&project, b(1, 100), on_finish.clone());
        assert_eq!(snap["status"], "queued");
        assert!(snap["launched_at"].is_null());
        assert_eq!(snap["claim"], json!({"cpu": 1, "mem_mb": 100}));
        let (third, _) = submit_sleeper(&project, b(1, 100), on_finish.clone());
        let disk: Value = serde_json::from_slice(
            &fs::read(project.join("runs").join(&third).join("status.json")).unwrap(),
        )
        .unwrap();
        assert_eq!(disk["status"], "queued");
        // Cancelling a queued task finishes it without a process.
        let snap = cancel(&third, None).unwrap();
        assert_eq!(snap["status"], "cancelled");
        assert!(snap["finished_at"].is_string());
        assert_eq!(*finished.lock().unwrap(), vec![third.clone()]);
        assert_eq!(status(&second, None).unwrap()["status"], "queued");
        // Freeing the budget starts the head of the queue with its grant.
        assert_eq!(cancel(&first, None).unwrap()["status"], "cancelled");
        let snap = wait_for(&second, &project, |s| s["status"] == "running");
        assert_eq!(snap["status"], "running");
        assert_eq!(granted(&project, &second), "1 100");
        assert!(!project
            .join("runs")
            .join(&third)
            .join("granted.txt")
            .exists());
        cancel(&second, None).unwrap();
        wait_for(&second, &project, |s| s["finished_at"].is_string());
        fs::remove_dir_all(project).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn raising_the_budget_starts_queued_tasks() {
        let _serial = serial();
        let _budget = with_budget(1, 1000);
        let project = temp_project();
        let b = |cpu, mem_mb| Budget { cpu, mem_mb };
        let (first, _) = submit_sleeper(&project, b(1, 600), noop());
        let (second, snap) = submit_sleeper(&project, b(1, 600), noop());
        assert_eq!(snap["status"], "queued");
        set_budget(b(2, 2000));
        assert_eq!(status(&second, None).unwrap()["status"], "running");
        for id in [&first, &second] {
            cancel(id, None).unwrap();
            wait_for(id, &project, |s| s["finished_at"].is_string());
        }
        fs::remove_dir_all(project).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn shutdown_marks_queued_and_running_interrupted() {
        // Exercises the same bookkeeping as shutdown_all without flipping the
        // process-wide shutdown flag (which would stop other tests).
        let _serial = serial();
        let _budget = with_budget(1, 1000);
        let project = temp_project();
        let b = |cpu, mem_mb| Budget { cpu, mem_mb };
        let (first, _) = submit_sleeper(&project, b(1, 100), noop());
        let (second, _) = submit_sleeper(&project, b(1, 100), noop());
        // Simulate a restart: the in-memory table is gone, disk says active.
        {
            let mut map = task_map();
            for id in [&first, &second] {
                if let Some(mut entry) = map.remove(id.as_str()) {
                    if let Some(mut child) = entry.child.take() {
                        let _ = kill_tree(&child);
                        let _ = child.wait();
                    }
                }
            }
        }
        for id in [&first, &second] {
            let snap = status(id, Some(&project)).unwrap();
            assert_eq!(snap["status"], "interrupted");
            assert!(resume_request(id, &project).is_err(), "no stored request");
        }
        fs::remove_dir_all(project).unwrap();
    }
    #[test]
    fn progress_is_passed_through() {
        let project = temp_project();
        let runs = project.join("runs");
        fs::create_dir_all(runs.join("with-progress")).unwrap();
        fs::write(
            runs.join("with-progress").join("status.json"),
            json!({"task_id":"with-progress","kind":"sample_pipeline","status":"succeeded","started_at":"2024-01-01T00:00:00.000Z"}).to_string(),
        )
        .unwrap();
        let progress = json!({"stage":"bowtie2","detail":"mapping","bytes_done":10,"bytes_total":100,"updated_at":"2024-01-01T00:00:01.000Z"});
        fs::write(
            runs.join("with-progress").join("progress.json"),
            progress.to_string(),
        )
        .unwrap();
        let snap = status("with-progress", Some(&project)).unwrap();
        assert_eq!(snap["progress"], progress);
        assert!(snap["claim"].is_null() && snap["resumed_from"].is_null());
        let listed = list(&project).unwrap();
        assert_eq!(listed["tasks"][0]["progress"], progress);
        // Partial writes or non-objects are reported as no progress.
        fs::write(
            runs.join("with-progress").join("progress.json"),
            "{\"stage\":",
        )
        .unwrap();
        assert!(status("with-progress", Some(&project)).unwrap()["progress"].is_null());
        fs::remove_file(runs.join("with-progress").join("progress.json")).unwrap();
        assert!(status("with-progress", Some(&project)).unwrap()["progress"].is_null());
        // The persisted record never contains progress.
        let disk: Value = serde_json::from_slice(
            &fs::read(runs.join("with-progress").join("status.json")).unwrap(),
        )
        .unwrap();
        assert!(disk.get("progress").is_none());
        fs::remove_dir_all(project).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn live_progress_is_reported() {
        let _serial = serial();
        let project = temp_project();
        let id = task_id();
        let (project, run_dir) = prepare_run_dir(&project, &id).unwrap();
        let mut command = Command::new("/bin/sh");
        command
            .arg("-c")
            .arg(r#"printf '{"stage":"collapse","detail":"","bytes_done":1,"bytes_total":2,"updated_at":"x"}' > progress.tmp && mv progress.tmp progress.json; exec sleep 30"#)
            .current_dir(&run_dir);
        start_managed(
            command,
            &project,
            id.clone(),
            "sample_pipeline",
            run_dir,
            noop(),
        )
        .unwrap();
        let snap = wait_for(&id, &project, |s| s["progress"].is_object());
        assert_eq!(snap["status"], "running");
        assert_eq!(snap["progress"]["stage"], "collapse");
        assert_eq!(snap["progress"]["bytes_total"], 2);
        cancel(&id, None).unwrap();
        wait_for(&id, &project, |s| s["finished_at"].is_string());
        fs::remove_dir_all(project).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn resume_reuses_the_stored_request() {
        let _serial = serial();
        let project = temp_project();
        let request = TaskStartRequest {
            project_path: project.display().to_string(),
            kind: "analysis_index".into(),
            sample_id: Some("ZSIN_REP3".into()),
            mem_mb: Some(128),
            ..Default::default()
        };
        let prepared = prepare(&request, &project, None).unwrap();
        assert!(prepared.id.starts_with("index-"));
        assert_eq!(
            prepared.claim,
            Budget {
                cpu: 1,
                mem_mb: 128
            }
        );
        let child: Value =
            serde_json::from_slice(&fs::read(&prepared.request_file).unwrap()).unwrap();
        assert_eq!(child["operation"], "analysis_index_build");
        assert_eq!(child["run_dir"], json!(prepared.run_dir));
        let id = prepared.id.clone();
        let mut command = Command::new("/bin/sh");
        command
            .arg("-c")
            .arg(r#"printf '{"ok":false,"error":"boom"}' > result.json; exit 2"#)
            .current_dir(&prepared.run_dir);
        submit(command, prepared, noop()).unwrap();
        let snap = wait_for(&id, &project, |s| s["finished_at"].is_string());
        assert_eq!(snap["status"], "failed");
        let again = resume_request(&id, &project).unwrap();
        assert_eq!(again.kind, "analysis_index");
        assert_eq!(again.sample_id.as_deref(), Some("ZSIN_REP3"));
        assert_eq!(again.mem_mb, Some(128));
        assert!(again.task_id.is_none());
        let resumed = prepare(&again, &project, Some(&id)).unwrap();
        assert_ne!(resumed.id, id);
        let new_id = resumed.id.clone();
        let mut command = Command::new("/bin/sh");
        command
            .arg("-c")
            .arg("exit 0")
            .current_dir(&resumed.run_dir);
        let snap = submit(command, resumed, noop()).unwrap();
        assert_eq!(snap["resumed_from"], json!(id));
        let snap = wait_for(&new_id, &project, |s| s["finished_at"].is_string());
        assert_eq!(snap["status"], "succeeded");
        assert_eq!(snap["resumed_from"], json!(id));
        // Succeeded tasks, unknown tasks and tasks of other projects are refused.
        assert!(resume_request(&new_id, &project)
            .unwrap_err()
            .contains("can be resumed"));
        assert!(resume_request("missing", &project).is_err());
        let other = temp_project();
        assert!(resume_request(&id, &other).is_err());
        // An explicit task id is not reused by the resumed copy.
        let named = TaskStartRequest {
            task_id: Some("named-task".into()),
            ..request
        };
        let prepared = prepare(&named, &project, None).unwrap();
        let stored: Value =
            serde_json::from_slice(&fs::read(prepared.run_dir.join(START_FILE)).unwrap()).unwrap();
        assert!(stored["task_id"].is_null());
        assert_eq!(stored["sample_id"], "ZSIN_REP3");
        assert!(prepare(&named, &project, None).is_err(), "id already used");
        fs::remove_dir_all(project).unwrap();
        fs::remove_dir_all(other).unwrap();
    }
    #[test]
    fn prepare_rejects_invalid_requests_without_a_run_dir() {
        let project = temp_project();
        for request in [
            TaskStartRequest {
                kind: "sh".into(),
                ..Default::default()
            },
            TaskStartRequest {
                kind: "sample_pipeline".into(),
                ..Default::default()
            },
            TaskStartRequest {
                kind: "cplfold".into(),
                sequence: Some("ACGU".into()),
                cpu: Some(u32::MAX),
                ..Default::default()
            },
        ] {
            assert!(prepare(&request, &project, None).is_err());
        }
        let runs = fs::read_dir(project.join("runs")).map(Iterator::count);
        assert!(
            runs.map_or(true, |n| n == 0),
            "a run directory was reserved"
        );
        fs::remove_dir_all(project).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn cancellation_reaps_child_and_keeps_cancelled_status() {
        let _serial = serial();
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
        let _serial = serial();
        let project = temp_project();
        let id = task_id();
        let (project, run_dir) = prepare_run_dir(&project, &id).unwrap();
        let mut command = Command::new("/bin/sh");
        command
            .arg("-c")
            .arg(r#"printf '{"ok":true,"output":"x.out"}' > result.json"#)
            .current_dir(&run_dir);
        start_managed(command, &project, id.clone(), "cplfold", run_dir, noop()).unwrap();
        let snap = wait_for(&id, &project, |s| s["finished_at"].is_string());
        assert_eq!(snap["status"], "succeeded");
        assert_eq!(snap["kind"], "cplfold");
        assert_eq!(snap["output"], "x.out");
        assert_eq!(snap["exit_code"], 0);
        fs::remove_dir_all(project).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn failure_copies_result_error_and_null_output() {
        let _serial = serial();
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
        let snap = wait_for(&id, &project, |s| s["finished_at"].is_string());
        assert_eq!(snap["status"], "failed");
        assert_eq!(snap["error"], "bad");
        assert_eq!(snap["exit_code"], 1);
        assert!(snap["output"].is_null());
        fs::remove_dir_all(project).unwrap();
    }
    /// Rust task model -> bridge child -> hyb2pipe, against the real repository.
    /// Uses `python3` from PATH and the repository sources (not the gitignored
    /// bundled runtime); skipped when the sources are absent.
    #[cfg(unix)]
    #[test]
    fn pipeline_task_runs_real_bridge_end_to_end() {
        let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../..")
            .canonicalize()
            .unwrap();
        if !root.join("pipeline/hyb2pipe/__init__.py").is_file()
            || !root.join("data/testData.sam").is_file()
        {
            eprintln!("skipping: repository pipeline sources are not available");
            return;
        }
        let _serial = serial();
        let project = std::env::temp_dir().join(task_id()).join("p.hyb2proj");
        let setup = Command::new("python3")
            .args(["-c", "import sys; from pathlib import Path; from desktop.runtime.project import new_project, import_input as i; p=Path(sys.argv[1]); new_project(p, 'Rust e2e'); i(p, Path('data/testData.sam'), role='sam'); i(p, Path('data/Zika_18S_formatted.fasta'), role='reference')"])
            .arg(&project)
            .current_dir(&root)
            .status()
            .unwrap();
        assert!(setup.success());
        let project = project.canonicalize().unwrap();
        let request = TaskStartRequest {
            project_path: project.display().to_string(),
            kind: "pipeline".into(),
            input: Some("inputs/testData.sam".into()),
            reference: Some("inputs/Zika_18S_formatted.fasta".into()),
            ..Default::default()
        };
        let prepared = prepare(&request, &project, None).unwrap();
        let id = prepared.id.clone();
        let command = bridge_command(Path::new("python3"), &root, &prepared.request_file, &[]);
        let finished = Arc::new(AtomicBool::new(false));
        let flag = finished.clone();
        let on_finish: OnFinish =
            Arc::new(move |_: &Path, _: &str| flag.store(true, Ordering::SeqCst));
        submit(command, prepared, on_finish).unwrap();
        let mut snap = Value::Null;
        for _ in 0..1200 {
            snap = status(&id, None).unwrap();
            if snap["finished_at"].is_string() {
                break;
            }
            std::thread::sleep(Duration::from_millis(100));
        }
        assert_eq!(snap["status"], "succeeded", "{snap}");
        let output = PathBuf::from(snap["output"].as_str().unwrap());
        assert!(output.starts_with(&project) && output.ends_with("testData.hyb"));
        assert_eq!(
            fs::read(&output).unwrap(),
            fs::read(root.join("pipeline/tests/golden/testData.hyb")).unwrap()
        );
        for _ in 0..50 {
            if finished.load(Ordering::SeqCst) {
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        assert!(
            finished.load(Ordering::SeqCst),
            "on_finish hook did not run"
        );
        fs::remove_dir_all(project.parent().unwrap()).unwrap();
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
            ("stale-queue", "queued", "2024-03-01T00:00:00.000Z"),
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
        assert_eq!(tasks.len(), 3);
        assert_eq!(tasks[0]["task_id"], "stale-run");
        assert_eq!(tasks[0]["status"], "interrupted");
        assert_eq!(tasks[1]["status"], "interrupted");
        assert_eq!(tasks[2]["status"], "succeeded");
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
