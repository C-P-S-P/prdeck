//! Pane sizes, remembered between launches in
//! ~/Library/Application Support/prdeck/desktop.json.

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
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join("Library/Application Support/prdeck/desktop.json"))
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
