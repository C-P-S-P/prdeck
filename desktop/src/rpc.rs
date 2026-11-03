//! Talks to `prdeck serve`: one JSON request per line on its stdin, one response per
//! line on its stdout, matched by id. Calls return futures, so the UI never waits on
//! GitHub or git.

use std::{
    collections::HashMap,
    io::{BufRead, BufReader, Read, Write},
    path::PathBuf,
    process::{Child, ChildStdin, Command, Stdio},
    sync::{
        Arc, Mutex,
        atomic::{AtomicU64, Ordering},
    },
    thread,
};

use anyhow::{Context as _, Result, anyhow};
use futures::channel::oneshot;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};

#[derive(Clone, Debug)]
pub struct RpcError {
    /// no_clone, dirty, needs_terminal, bad_request, error, or gone (the server exited).
    pub code: String,
    pub message: String,
}

impl std::fmt::Display for RpcError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.message)
    }
}

type Reply = std::result::Result<Value, RpcError>;

#[derive(Clone)]
pub struct Rpc(Arc<Inner>);

struct Inner {
    stdin: Mutex<ChildStdin>,
    pending: Arc<Mutex<HashMap<u64, oneshot::Sender<Reply>>>>,
    gone: Arc<Mutex<Option<String>>>,
    next: AtomicU64,
    child: Mutex<Child>,
}

impl Drop for Inner {
    fn drop(&mut self) {
        if let Ok(mut c) = self.child.lock() {
            let _ = c.kill();
            let _ = c.wait();
        }
    }
}

impl Rpc {
    /// Starts `prdeck serve` (with sample data when `demo` is set).
    pub fn start(demo: bool) -> Result<Self> {
        let bin = sidecar();
        let mut cmd = Command::new(&bin);
        cmd.arg("serve");
        if let Some(path) = login_path() {
            cmd.env("PATH", path);
        }
        if demo {
            cmd.arg("--demo");
        }
        let mut child = cmd
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .with_context(|| format!("starting {}", bin.display()))?;
        let stdin = child.stdin.take().ok_or_else(|| anyhow!("no stdin"))?;
        let stdout = child.stdout.take().ok_or_else(|| anyhow!("no stdout"))?;
        let mut stderr = child.stderr.take().ok_or_else(|| anyhow!("no stderr"))?;

        let pending: Arc<Mutex<HashMap<u64, oneshot::Sender<Reply>>>> = Default::default();
        let gone: Arc<Mutex<Option<String>>> = Default::default();
        let errtext: Arc<Mutex<String>> = Default::default();

        {
            let errtext = errtext.clone();
            thread::spawn(move || {
                let mut s = String::new();
                let _ = stderr.read_to_string(&mut s);
                *errtext.lock().unwrap() = s;
            });
        }
        {
            let (pending, gone) = (pending.clone(), gone.clone());
            thread::spawn(move || {
                let debug = std::env::var_os("PRDECK_DEBUG").is_some();
                for line in BufReader::new(stdout).lines() {
                    let Ok(line) = line else { break };
                    if debug {
                        eprintln!("rpc ← {}", line.chars().take(200).collect::<String>());
                    }
                    let Ok(v) = serde_json::from_str::<Value>(&line) else { continue };
                    let id = v.get("id").and_then(Value::as_u64).unwrap_or(0);
                    let reply = match v.get("error") {
                        Some(e) if !e.is_null() => Err(RpcError {
                            code: e.get("code").and_then(Value::as_str).unwrap_or("error").into(),
                            message: e.get("message").and_then(Value::as_str).unwrap_or("").into(),
                        }),
                        _ => Ok(v.get("result").cloned().unwrap_or(Value::Null)),
                    };
                    if let Some(tx) = pending.lock().unwrap().remove(&id) {
                        let _ = tx.send(reply);
                    }
                }
                // The server exited: say why, and fail everything still waiting.
                thread::sleep(std::time::Duration::from_millis(100));
                let why = errtext.lock().unwrap().trim().to_string();
                let why = if why.is_empty() { "prdeck serve stopped".to_string() } else { why };
                *gone.lock().unwrap() = Some(why.clone());
                for (_, tx) in pending.lock().unwrap().drain() {
                    let _ = tx.send(Err(RpcError { code: "gone".into(), message: why.clone() }));
                }
            });
        }

        Ok(Rpc(Arc::new(Inner {
            stdin: Mutex::new(stdin),
            pending,
            gone,
            next: AtomicU64::new(1),
            child: Mutex::new(child),
        })))
    }

    /// Sends a request; the future resolves with the decoded result.
    pub fn call<T: DeserializeOwned + 'static>(
        &self,
        method: &str,
        params: Value,
    ) -> impl std::future::Future<Output = std::result::Result<T, RpcError>> + 'static {
        let inner = self.0.clone();
        let id = inner.next.fetch_add(1, Ordering::Relaxed);
        let (tx, rx) = oneshot::channel();
        let sent = (|| {
            if let Some(why) = inner.gone.lock().unwrap().clone() {
                return Err(RpcError { code: "gone".into(), message: why });
            }
            inner.pending.lock().unwrap().insert(id, tx);
            let line = json!({ "id": id, "method": method, "params": params }).to_string();
            if std::env::var_os("PRDECK_DEBUG").is_some() {
                eprintln!("rpc → {}", line.chars().take(200).collect::<String>());
            }
            let mut w = inner.stdin.lock().unwrap();
            w.write_all(line.as_bytes())
                .and_then(|_| w.write_all(b"\n"))
                .and_then(|_| w.flush())
                .map_err(|e| RpcError { code: "gone".into(), message: format!("prdeck serve: {e}") })
        })();
        async move {
            sent?;
            let v = rx
                .await
                .map_err(|_| RpcError { code: "gone".into(), message: "prdeck serve stopped".into() })??;
            serde_json::from_value(v).map_err(|e| RpcError { code: "decode".into(), message: e.to_string() })
        }
    }
}

/// PATH from the user's login shell. An app opened from Finder or the Dock gets a bare
/// PATH without Homebrew, so `gh` and `git` would not be found; Zed does the same.
fn login_path() -> Option<String> {
    let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".into());
    let mut child = Command::new(shell)
        .args(["-l", "-c", "printf '%s' \"$PATH\""])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .ok()?;
    let start = std::time::Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(_)) => break,
            Ok(None) if start.elapsed() < std::time::Duration::from_secs(3) => {
                thread::sleep(std::time::Duration::from_millis(20))
            }
            _ => {
                let _ = child.kill();
                return None;
            }
        }
    }
    let mut out = String::new();
    child.stdout.take()?.read_to_string(&mut out).ok()?;
    let out = out.trim();
    (!out.is_empty()).then(|| out.to_string())
}

/// The prdeck binary: $PRDECK_BIN, else one next to this app (inside the .app bundle),
/// else the one build.rs compiled, else `prdeck` on PATH.
fn sidecar() -> PathBuf {
    if let Some(p) = std::env::var_os("PRDECK_BIN") {
        return p.into();
    }
    if let Some(p) = std::env::current_exe().ok().and_then(|e| e.parent().map(|d| d.join("prdeck"))) {
        if p.is_file() {
            return p;
        }
    }
    let built = env!("PRDECK_SIDECAR");
    if !built.is_empty() && std::path::Path::new(built).is_file() {
        return built.into();
    }
    "prdeck".into()
}
