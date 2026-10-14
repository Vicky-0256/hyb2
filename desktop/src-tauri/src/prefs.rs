//! Resource-budget preferences (contract R6), stored in `preferences.json` in
//! the application config directory. Unknown keys are preserved so later
//! preferences can share the file.
//!
//! Physical memory is read without new dependencies and without `unsafe`
//! (the crate forbids it): `sysctl hw.memsize` on macOS, `/proc/meminfo` on
//! Linux and a CIM query through PowerShell on Windows.
use serde::{Deserialize, Serialize};
use serde_json::{json, Map, Value};
use std::fs;
use std::path::Path;
use std::sync::OnceLock;

pub const FILE_NAME: &str = "preferences.json";
/// Used when physical memory cannot be determined.
const FALLBACK_TOTAL_MB: u64 = 8192;
const MIN_MEM_MB: u64 = 256;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Budget {
    pub cpu: u32,
    pub mem_mb: u64,
}

pub fn system_cpus() -> u32 {
    std::thread::available_parallelism()
        .map(|n| u32::try_from(n.get()).unwrap_or(u32::MAX))
        .unwrap_or(1)
}

/// Total physical memory in MiB, if the platform reports it.
pub fn system_mem_mb() -> Option<u64> {
    static CACHE: OnceLock<Option<u64>> = OnceLock::new();
    *CACHE.get_or_init(|| physical_bytes().map(|b| b / (1024 * 1024)).filter(|&mb| mb > 0))
}

#[cfg(target_os = "macos")]
fn physical_bytes() -> Option<u64> {
    let out = std::process::Command::new("/usr/sbin/sysctl")
        .args(["-n", "hw.memsize"])
        .output()
        .ok()?;
    String::from_utf8_lossy(&out.stdout).trim().parse().ok()
}

#[cfg(target_os = "linux")]
fn physical_bytes() -> Option<u64> {
    parse_meminfo(&fs::read_to_string("/proc/meminfo").ok()?)
}

#[cfg(windows)]
fn physical_bytes() -> Option<u64> {
    use std::os::windows::process::CommandExt;
    let out = std::process::Command::new("powershell")
        .args([
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            "(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory",
        ])
        .creation_flags(0x08000000) // CREATE_NO_WINDOW
        .output()
        .ok()?;
    String::from_utf8_lossy(&out.stdout).trim().parse().ok()
}

#[cfg(not(any(target_os = "macos", target_os = "linux", windows)))]
fn physical_bytes() -> Option<u64> {
    None
}

#[cfg(any(target_os = "linux", test))]
fn parse_meminfo(text: &str) -> Option<u64> {
    let line = text.lines().find(|l| l.starts_with("MemTotal:"))?;
    let kb: u64 = line.split_whitespace().nth(1)?.parse().ok()?;
    Some(kb * 1024)
}

/// All cores and 75% of physical memory.
pub fn default_budget() -> Budget {
    let total = system_mem_mb().unwrap_or(FALLBACK_TOTAL_MB);
    Budget {
        cpu: system_cpus(),
        mem_mb: (total * 3 / 4).max(MIN_MEM_MB),
    }
}

fn max_mem_mb() -> u64 {
    system_mem_mb().unwrap_or(FALLBACK_TOTAL_MB).max(MIN_MEM_MB)
}

fn read_object(path: &Path) -> Map<String, Value> {
    fs::read(path)
        .ok()
        .and_then(|bytes| serde_json::from_slice::<Value>(&bytes).ok())
        .and_then(|value| match value {
            Value::Object(map) => Some(map),
            _ => None,
        })
        .unwrap_or_default()
}

fn validate(budget: Budget) -> Result<Budget, String> {
    let cpus = system_cpus();
    if budget.cpu == 0 || budget.cpu > cpus {
        return Err(format!("budget cpu must be between 1 and {cpus}"));
    }
    let max = max_mem_mb();
    if budget.mem_mb < MIN_MEM_MB || budget.mem_mb > max {
        return Err(format!(
            "budget mem_mb must be between {MIN_MEM_MB} and {max}"
        ));
    }
    Ok(budget)
}

/// The stored budget, or the default when absent or no longer valid (for
/// example after moving the config to a smaller machine).
pub fn load_budget(path: &Path) -> Budget {
    read_object(path)
        .get("budget")
        .and_then(|v| serde_json::from_value::<Budget>(v.clone()).ok())
        .and_then(|b| validate(b).ok())
        .unwrap_or_else(default_budget)
}

/// Response for `preferences_get` / `preferences_set`.
pub fn describe(path: &Path) -> Value {
    let stored = read_object(path);
    json!({
        "ok": true,
        "path": path,
        "budget": load_budget(path),
        "custom_budget": stored.get("budget").is_some_and(|v| !v.is_null()),
        "defaults": {"budget": default_budget()},
        "system": {"cpus": system_cpus(), "mem_mb": system_mem_mb()},
    })
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BudgetPatch {
    pub cpu: Option<u32>,
    pub mem_mb: Option<u64>,
}
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PreferencesSetRequest {
    /// `null` resets to the defaults; omitted fields keep their value.
    #[serde(default, deserialize_with = "nullable")]
    pub budget: Option<Option<BudgetPatch>>,
}
fn nullable<'de, D: serde::Deserializer<'de>>(d: D) -> Result<Option<Option<BudgetPatch>>, D::Error> {
    Option::<BudgetPatch>::deserialize(d).map(Some)
}

/// Applies the patch, writes the file atomically and returns the new budget.
pub fn apply(path: &Path, request: &PreferencesSetRequest) -> Result<Budget, String> {
    let mut stored = read_object(path);
    match &request.budget {
        None => {}
        Some(None) => {
            stored.remove("budget");
        }
        Some(Some(patch)) => {
            let current = load_budget(path);
            let next = validate(Budget {
                cpu: patch.cpu.unwrap_or(current.cpu),
                mem_mb: patch.mem_mb.unwrap_or(current.mem_mb),
            })?;
            stored.insert("budget".into(), json!(next));
        }
    }
    if let Some(dir) = path.parent() {
        fs::create_dir_all(dir).map_err(|e| format!("cannot create config directory: {e}"))?;
    }
    let pending = path.with_extension("json.tmp");
    let bytes = serde_json::to_vec_pretty(&Value::Object(stored)).map_err(|e| e.to_string())?;
    fs::write(&pending, bytes).map_err(|e| format!("cannot save preferences: {e}"))?;
    #[cfg(windows)]
    if path.exists() {
        fs::remove_file(path).map_err(|e| e.to_string())?;
    }
    fs::rename(&pending, path).map_err(|e| format!("cannot save preferences: {e}"))?;
    Ok(load_budget(path))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn temp_file() -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(crate::tasks::task_id());
        dir.join("config").join(FILE_NAME)
    }

    #[test]
    fn defaults_are_sane() {
        let budget = default_budget();
        assert!(budget.cpu >= 1 && budget.cpu == system_cpus());
        assert!(budget.mem_mb >= MIN_MEM_MB);
        if let Some(total) = system_mem_mb() {
            assert_eq!(budget.mem_mb, (total * 3 / 4).max(MIN_MEM_MB));
        }
        #[cfg(any(target_os = "macos", target_os = "linux"))]
        assert!(system_mem_mb().is_some());
        assert_eq!(
            parse_meminfo("MemTotal:       16384 kB\nMemFree: 1 kB\n"),
            Some(16384 * 1024)
        );
        assert_eq!(parse_meminfo("nothing"), None);
    }

    #[test]
    fn preferences_round_trip_and_reset() {
        let path = temp_file();
        assert_eq!(load_budget(&path), default_budget());
        assert_eq!(describe(&path)["custom_budget"], false);
        // Unknown keys written by other features survive updates.
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(&path, r#"{"theme":"dark"}"#).unwrap();
        let patch: PreferencesSetRequest =
            serde_json::from_value(json!({"budget": {"cpu": 1, "mem_mb": 512}})).unwrap();
        assert_eq!(apply(&path, &patch).unwrap(), Budget { cpu: 1, mem_mb: 512 });
        assert_eq!(load_budget(&path), Budget { cpu: 1, mem_mb: 512 });
        let described = describe(&path);
        assert_eq!(described["budget"], json!({"cpu": 1, "mem_mb": 512}));
        assert_eq!(described["custom_budget"], true);
        // Partial patch keeps the other field.
        let partial: PreferencesSetRequest =
            serde_json::from_value(json!({"budget": {"mem_mb": 1024}})).unwrap();
        assert_eq!(apply(&path, &partial).unwrap(), Budget { cpu: 1, mem_mb: 1024 });
        let stored: Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
        assert_eq!(stored["theme"], "dark");
        // Out-of-range values are rejected and leave the file untouched.
        for bad in [json!({"cpu": 0}), json!({"cpu": system_cpus() + 1}), json!({"mem_mb": 1})] {
            let patch: PreferencesSetRequest =
                serde_json::from_value(json!({ "budget": bad })).unwrap();
            assert!(apply(&path, &patch).is_err());
        }
        assert_eq!(load_budget(&path), Budget { cpu: 1, mem_mb: 1024 });
        assert!(serde_json::from_value::<PreferencesSetRequest>(json!({"budget": {"gpu": 1}})).is_err());
        // null resets to the defaults.
        let reset: PreferencesSetRequest = serde_json::from_value(json!({"budget": null})).unwrap();
        assert_eq!(apply(&path, &reset).unwrap(), default_budget());
        let stored: Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
        assert!(stored.get("budget").is_none());
        assert_eq!(stored["theme"], "dark");
        // A corrupt file falls back to the defaults.
        fs::write(&path, "not json").unwrap();
        assert_eq!(load_budget(&path), default_budget());
        fs::remove_dir_all(path.parent().unwrap().parent().unwrap()).unwrap();
    }
}
