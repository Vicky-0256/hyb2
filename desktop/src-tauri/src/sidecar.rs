//! Persistent Python sidecar speaking the newline JSON protocol (contract C1):
//! stdin  `{"id":N,"request":{...}}\n`, stdout `{"id":N,"response":{...}}\n`.
//! Each sidecar serializes requests with a Mutex, restarts after crash/EOF and
//! is killed and restarted when a response exceeds its timeout.
use serde_json::{json, Value};
use std::collections::VecDeque;
use std::ffi::OsString;
use std::io::{BufRead, BufReader, Read, Write};
use std::path::PathBuf;
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::mpsc::{channel, Receiver, RecvTimeoutError};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

const STDERR_TAIL: usize = 4000;

#[derive(Clone, Debug)]
pub struct SidecarSpec {
    pub name: String,
    pub program: PathBuf,
    pub args: Vec<OsString>,
    pub cwd: PathBuf,
    pub envs: Vec<(String, OsString)>,
    pub timeout: Duration,
}

struct Proc {
    child: Child,
    stdin: ChildStdin,
    lines: Receiver<String>,
    stderr: Arc<Mutex<VecDeque<u8>>>,
}

impl Proc {
    fn stderr_tail(&self) -> String {
        let tail = self.stderr.lock().unwrap_or_else(|e| e.into_inner());
        String::from_utf8_lossy(&tail.iter().copied().collect::<Vec<u8>>())
            .trim()
            .to_string()
    }
    fn kill(mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
    fn shutdown(self) {
        let Proc {
            mut child, stdin, ..
        } = self;
        drop(stdin); // EOF asks the sidecar to exit cleanly.
        let deadline = Instant::now() + Duration::from_millis(500);
        while Instant::now() < deadline {
            if let Ok(Some(_)) = child.try_wait() {
                return;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        let _ = child.kill();
        let _ = child.wait();
    }
}

struct Inner {
    proc: Option<Proc>,
    next_id: u64,
    closed: bool,
}

pub struct Sidecar {
    spec: SidecarSpec,
    inner: Mutex<Inner>,
}

impl Sidecar {
    pub fn new(spec: SidecarSpec) -> Self {
        Sidecar {
            spec,
            inner: Mutex::new(Inner {
                proc: None,
                next_id: 1,
                closed: false,
            }),
        }
    }

    fn spawn(&self) -> Result<Proc, String> {
        let spec = &self.spec;
        let mut command = Command::new(&spec.program);
        command
            .args(&spec.args)
            .current_dir(&spec.cwd)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        for (key, value) in &spec.envs {
            command.env(key, value);
        }
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x08000000); // CREATE_NO_WINDOW
        }
        let mut child = command.spawn().map_err(|error| {
            format!(
                "could not start desktop Python {} sidecar ({}): {error}",
                spec.name,
                spec.program.display()
            )
        })?;
        let stdin = child.stdin.take().ok_or("sidecar stdin unavailable")?;
        let stdout = child.stdout.take().ok_or("sidecar stdout unavailable")?;
        let mut stderr_pipe = child.stderr.take().ok_or("sidecar stderr unavailable")?;
        let (tx, lines) = channel();
        std::thread::spawn(move || {
            for line in BufReader::new(stdout).lines() {
                match line {
                    Ok(line) => {
                        if tx.send(line).is_err() {
                            break;
                        }
                    }
                    Err(_) => break,
                }
            }
        });
        let stderr = Arc::new(Mutex::new(VecDeque::new()));
        let sink = stderr.clone();
        std::thread::spawn(move || {
            let mut buffer = [0u8; 4096];
            while let Ok(n) = stderr_pipe.read(&mut buffer) {
                if n == 0 {
                    break;
                }
                let mut tail = sink.lock().unwrap_or_else(|e| e.into_inner());
                tail.extend(&buffer[..n]);
                while tail.len() > STDERR_TAIL {
                    tail.pop_front();
                }
            }
        });
        Ok(Proc {
            child,
            stdin,
            lines,
            stderr,
        })
    }

    fn send(proc: &mut Proc, line: &[u8]) -> std::io::Result<()> {
        proc.stdin.write_all(line)?;
        proc.stdin.flush()
    }

    /// Sends one request and returns the raw `response` object.
    pub fn request(&self, request: &Value) -> Result<Value, String> {
        let mut inner = self.inner.lock().unwrap_or_else(|e| e.into_inner());
        if inner.closed {
            return Err("desktop runtime is shutting down".into());
        }
        if let Some(proc) = inner.proc.as_mut() {
            if !matches!(proc.child.try_wait(), Ok(None)) {
                if let Some(proc) = inner.proc.take() {
                    proc.kill()
                }
            }
        }
        let id = inner.next_id;
        inner.next_id += 1;
        let mut line = serde_json::to_vec(&json!({"id": id, "request": request}))
            .map_err(|error| format!("could not encode bridge request: {error}"))?;
        line.push(b'\n');
        // A write failure means the request never reached Python, so one
        // restart-and-retry is safe even for non-idempotent operations.
        let mut attempts = 0;
        loop {
            if inner.proc.is_none() {
                inner.proc = Some(self.spawn()?);
            }
            let proc = inner.proc.as_mut().expect("sidecar spawned");
            match Self::send(proc, &line) {
                Ok(()) => break,
                Err(error) => {
                    if let Some(proc) = inner.proc.take() {
                        proc.kill()
                    }
                    attempts += 1;
                    if attempts > 1 {
                        return Err(format!(
                            "could not send request to {} sidecar: {error}",
                            self.spec.name
                        ));
                    }
                }
            }
        }
        let deadline = Instant::now() + self.spec.timeout;
        loop {
            let proc = inner.proc.as_mut().expect("sidecar running");
            let remaining = deadline.saturating_duration_since(Instant::now());
            match proc.lines.recv_timeout(remaining) {
                Ok(text) => {
                    let Ok(message) = serde_json::from_str::<Value>(&text) else {
                        continue; // Non-protocol output; Python should use stderr.
                    };
                    if message.get("id").and_then(Value::as_u64) != Some(id) {
                        continue; // Stale response from an earlier request.
                    }
                    return message
                        .get("response")
                        .cloned()
                        .ok_or_else(|| "bridge response is missing `response`".to_string());
                }
                Err(RecvTimeoutError::Timeout) => {
                    if let Some(proc) = inner.proc.take() {
                        proc.kill()
                    }
                    return Err(format!(
                        "{} sidecar timed out after {} s and was restarted",
                        self.spec.name,
                        self.spec.timeout.as_secs()
                    ));
                }
                Err(RecvTimeoutError::Disconnected) => {
                    let proc = inner.proc.take().expect("sidecar running");
                    // Give the stderr reader a moment to collect the traceback.
                    std::thread::sleep(Duration::from_millis(50));
                    let detail = proc.stderr_tail();
                    proc.kill();
                    return Err(format!(
                        "{} sidecar exited unexpectedly: {}",
                        self.spec.name,
                        if detail.is_empty() {
                            "no diagnostic output"
                        } else {
                            &detail
                        }
                    ));
                }
            }
        }
    }

    /// Stops the process and refuses later requests (application exit).
    pub fn shutdown(&self) {
        let mut inner = self.inner.lock().unwrap_or_else(|e| e.into_inner());
        inner.closed = true;
        if let Some(proc) = inner.proc.take() {
            proc.shutdown()
        }
    }

    #[cfg(test)]
    fn pid(&self) -> Option<u32> {
        let inner = self.inner.lock().unwrap();
        inner.proc.as_ref().map(|p| p.child.id())
    }
}

impl Drop for Sidecar {
    fn drop(&mut self) {
        self.shutdown();
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    const ECHO: &str = r#"
import json, sys, time
for line in sys.stdin:
    msg = json.loads(line)
    req = msg["request"]
    if req.get("op") == "sleep":
        time.sleep(req["seconds"])
    if req.get("op") == "die":
        sys.stderr.write("boom\n"); sys.stderr.flush(); sys.exit(3)
    print("noise that is not json", flush=True)
    print(json.dumps({"id": msg["id"], "response": {"ok": True, "echo": req}}), flush=True)
"#;

    fn echo(timeout: Duration) -> Sidecar {
        Sidecar::new(SidecarSpec {
            name: "test".into(),
            program: "python3".into(),
            args: vec!["-c".into(), ECHO.into()],
            cwd: std::env::temp_dir(),
            envs: vec![],
            timeout,
        })
    }

    #[test]
    fn roundtrip_reuses_process() {
        let sidecar = echo(Duration::from_secs(20));
        let first = sidecar.request(&json!({"op": "a", "n": 1})).unwrap();
        assert_eq!(first["echo"]["n"], 1);
        let pid = sidecar.pid();
        let second = sidecar.request(&json!({"op": "b"})).unwrap();
        assert_eq!(second["echo"]["op"], "b");
        assert_eq!(sidecar.pid(), pid);
    }

    #[test]
    fn timeout_kills_and_restarts() {
        let sidecar = echo(Duration::from_millis(1500));
        sidecar.request(&json!({"op": "warm"})).unwrap();
        let pid = sidecar.pid();
        let error = sidecar
            .request(&json!({"op": "sleep", "seconds": 30}))
            .unwrap_err();
        assert!(error.contains("timed out"), "{error}");
        assert!(sidecar.pid().is_none());
        let again = sidecar.request(&json!({"op": "after"})).unwrap();
        assert_eq!(again["echo"]["op"], "after");
        assert_ne!(sidecar.pid(), pid);
    }

    #[test]
    fn crash_reports_stderr_and_restarts() {
        let sidecar = echo(Duration::from_secs(20));
        let error = sidecar.request(&json!({"op": "die"})).unwrap_err();
        assert!(error.contains("exited unexpectedly"), "{error}");
        assert!(error.contains("boom"), "{error}");
        assert_eq!(sidecar.request(&json!({"op": "x"})).unwrap()["ok"], true);
        sidecar.shutdown();
        assert!(sidecar.request(&json!({"op": "x"})).is_err());
    }
}

/// End-to-end check against the real `bridge.py --serve` when the repository
/// layout is available (skipped in packaged-source builds).
#[cfg(all(test, unix))]
mod bridge_tests {
    use super::*;
    #[test]
    fn real_bridge_serve_roundtrip() {
        let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..");
        if !root.join("desktop/runtime/bridge.py").is_file() {
            return;
        }
        let sidecar = Sidecar::new(SidecarSpec {
            name: "control".into(),
            program: "python3".into(),
            args: vec![
                "-m".into(),
                "desktop.runtime.bridge".into(),
                "--repo-root".into(),
                root.clone().into_os_string(),
                "--serve".into(),
            ],
            cwd: root,
            envs: vec![("HYB2_APP_VERSION".into(), env!("CARGO_PKG_VERSION").into())],
            timeout: Duration::from_secs(60),
        });
        let bad = sidecar.request(&json!({"operation": "nope"})).unwrap();
        assert_eq!(bad["ok"], false);
        assert!(bad["error"].as_str().unwrap().contains("unsupported"));
        let detect = sidecar.request(&json!({"operation": "detect"})).unwrap();
        assert_eq!(detect["ok"], true);
    }
}
