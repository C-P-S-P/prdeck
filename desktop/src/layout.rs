//! Pane sizes, remembered between launches in
//! ~/Library/Application Support/prdeck/desktop.json (%APPDATA%\prdeck on Windows,
//! ~/.config/prdeck on Linux).

use std::ffi::OsString;
use std::path::PathBuf;

use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, Serialize, Deserialize)]
#[serde(default)]
pub struct Layout {
    /// Width of the left column (repositories and pull requests).
    pub sidebar: f32,
    /// Height of the repositories list inside it.
    pub repos: f32,
    /// Width of the PR's side panel (branch, reviewers, checks).
    pub aside: f32,
    pub sidebar_hidden: bool,
    /// First-run setup was finished (or skipped).
    pub onboarded: bool,
}

impl Default for Layout {
    fn default() -> Self {
        Layout { sidebar: 340., repos: 230., aside: 260., sidebar_hidden: false, onboarded: false }
    }
}

fn path() -> Option<PathBuf> {
    path_for(std::env::consts::OS, |v| std::env::var_os(v))
}

/// Where the file lives on `os`: Application Support on the Mac, %APPDATA% on Windows,
/// $XDG_CONFIG_HOME or ~/.config elsewhere.
fn path_for(os: &str, var: impl Fn(&str) -> Option<OsString>) -> Option<PathBuf> {
    let dir = match os {
        "macos" => PathBuf::from(var("HOME")?).join("Library/Application Support"),
        "windows" => PathBuf::from(var("APPDATA")?),
        _ => match var("XDG_CONFIG_HOME").filter(|d| !d.is_empty()) {
            Some(d) => PathBuf::from(d),
            None => PathBuf::from(var("HOME")?).join(".config"),
        },
    };
    Some(dir.join("prdeck").join("desktop.json"))
}

impl Layout {
    pub fn load() -> Layout {
        path()
            .and_then(|p| std::fs::read(p).ok())
            .and_then(|b| serde_json::from_slice(&b).ok())
            .unwrap_or_default()
    }

    pub fn save(&self) {
        let Some(p) = path() else { return };
        if let Some(dir) = p.parent() {
            let _ = std::fs::create_dir_all(dir);
        }
        if let Ok(b) = serde_json::to_vec_pretty(self) {
            let _ = std::fs::write(p, b);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{OsString, PathBuf, path_for};

    fn env(pairs: &'static [(&'static str, &'static str)]) -> impl Fn(&str) -> Option<OsString> {
        move |v| pairs.iter().find(|(k, _)| *k == v).map(|(_, x)| x.into())
    }

    #[test]
    fn path_per_os() {
        let p = |s: &str| Some(PathBuf::from(s));
        let home = env(&[("HOME", "/h"), ("APPDATA", r"C:\Users\me\AppData\Roaming")]);
        assert_eq!(path_for("macos", &home), p("/h/Library/Application Support/prdeck/desktop.json"));
        assert_eq!(path_for("linux", &home), p("/h/.config/prdeck/desktop.json"));
        assert_eq!(path_for("linux", env(&[("HOME", "/h"), ("XDG_CONFIG_HOME", "/x")])), p("/x/prdeck/desktop.json"));
        assert_eq!(path_for("linux", env(&[("HOME", "/h"), ("XDG_CONFIG_HOME", "")])), p("/h/.config/prdeck/desktop.json"));
        assert_eq!(path_for("windows", &home), Some(PathBuf::from(r"C:\Users\me\AppData\Roaming").join("prdeck").join("desktop.json")));
        assert_eq!(path_for("windows", env(&[("HOME", "/h")])), None);
    }
}
