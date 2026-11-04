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
    /// Starts `prdeck serve` (with sample data when `demo` is set; `signed_out` makes
    /// the demo start signed out, to try the sign-in screen).
    pub fn start(demo: bool, signed_out: bool) -> Result<Self> {
        let bin = sidecar();
        let mut cmd = Command::new(&bin);
        cmd.arg("serve");
        for (name, value) in login_env() {
            cmd.env(name, value);
        }
        if demo {
            cmd.arg(if signed_out { "--demo-signed-out" } else { "--demo" });
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

/// Settings prdeck needs from the user's login shell: PATH (an app opened from Finder
/// or the Dock gets a bare one, without Homebrew's `gh` and `git`; Zed does the same)
/// and where the config lives, so the app and the terminal read the same file.
const SHELL_VARS: [&str; 4] = ["PATH", "XDG_CONFIG_HOME", "PRDECK_CONFIG", "GH_CONFIG_DIR"];

/// The SHELL_VARS the login shell sets differently from this process.
fn login_env() -> Vec<(&'static str, String)> {
    let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".into());
    let Some(env) = shell_values(&shell) else { return vec![] };
    SHELL_VARS
        .iter()
        .filter_map(|name| Some((*name, env.get(*name)?.clone())))
        .filter(|(name, value)| !value.is_empty() && std::env::var(name).ok().as_deref() != Some(value.as_str()))
        .collect()
}

/// The environment `shell` exports after a login. It is printed by `env -0`, not the
/// shell, so fish (whose "$PATH" is a list joined with spaces) gives a real PATH too.
/// Dotfiles can print as well (`eval "$(ssh-agent -s)"` prints "Agent pid 1234"), so
/// the output is read from between markers.
fn shell_values(shell: &str) -> Option<std::collections::HashMap<String, String>> {
    let out = login_shell(shell, "printf _PRDECK_ENV_; /usr/bin/env -0; printf _PRDECK_ENV_")?;
    let env = out.split("_PRDECK_ENV_").nth(1)?;
    Some(env.split('\0').filter_map(|kv| kv.split_once('=')).map(|(k, v)| (k.to_string(), v.to_string())).collect())
}

/// Runs a script in `shell` as a login shell (3 seconds at most) and returns its output.
fn login_shell(shell: &str, script: &str) -> Option<String> {
    let mut child = Command::new(shell)
        .args(["-l", "-c", script])
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
                let _ = child.wait();
                return None;
            }
        }
    }
    let mut out = String::new();
    child.stdout.take()?.read_to_string(&mut out).ok()?;
    Some(out)
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

#[cfg(test)]
mod tests {
    use super::shell_values;
    use std::os::unix::fs::PermissionsExt;

    #[test]
    fn login_path_ignores_what_dotfiles_print() {
        // A login shell whose dotfiles print before and after the command.
        let sh = std::env::temp_dir().join(format!("prdeck-test-shell-{}", std::process::id()));
        std::fs::write(&sh, "#!/bin/sh\necho Agent pid 1234\nPATH=/opt/homebrew/bin:/usr/bin\neval \"$3\"\necho bye\n").unwrap();
        std::fs::set_permissions(&sh, std::fs::Permissions::from_mode(0o755)).unwrap();
        // macOS checks a new executable the first time it runs, which can take longer
        // than login_shell waits; do that first.
        let _ = std::process::Command::new(&sh).output();
        let values = shell_values(sh.to_str().unwrap());
        let _ = std::fs::remove_file(&sh);
        assert_eq!(values.as_ref().and_then(|v| v.get("PATH")).map(String::as_str), Some("/opt/homebrew/bin:/usr/bin"));
    }

    #[test]
    fn path_comes_from_env_not_the_shells_own_syntax() {
        // A shell like fish, where "$PATH" would join a list with spaces: the value
        // must come from the exported environment instead.
        let sh = std::env::temp_dir().join(format!("prdeck-test-fish-{}", std::process::id()));
        std::fs::write(&sh, "#!/bin/sh\nexport PATH=/opt/homebrew/bin:/usr/bin\nPATH_LIST='/opt/homebrew/bin /usr/bin'\neval \"$3\"\n").unwrap();
        std::fs::set_permissions(&sh, std::fs::Permissions::from_mode(0o755)).unwrap();
        let _ = std::process::Command::new(&sh).output();
        let values = shell_values(sh.to_str().unwrap());
        let _ = std::fs::remove_file(&sh);
        let path = values.as_ref().and_then(|v| v.get("PATH")).cloned().unwrap_or_default();
        assert_eq!(path, "/opt/homebrew/bin:/usr/bin");
    }
}
