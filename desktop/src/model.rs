//! The JSON shapes `prdeck serve` sends and accepts. Field names follow the Go side.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Deserializer, Serialize};

/// Go writes an empty list as `null`; read it as an empty Vec.
fn null_vec<'de, D, T>(d: D) -> Result<Vec<T>, D::Error>
where
    D: Deserializer<'de>,
    T: Deserialize<'de>,
{
    Ok(Option::<Vec<T>>::deserialize(d)?.unwrap_or_default())
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Hello {
    pub version: String,
    pub demo: bool,
    pub config_path: String,
    pub viewer: String,
    #[serde(deserialize_with = "null_vec")]
    pub warnings: Vec<String>,
    pub refresh: f64,
    pub editor: String,
    #[serde(deserialize_with = "null_vec")]
    pub filters: Vec<Filter>,
    #[serde(deserialize_with = "null_vec")]
    pub commands: Vec<Cmd>,
}

#[derive(Clone, Debug, Default, Deserialize)]
pub struct Filter {
    pub name: String,
    pub query: String,
}

/// A command from the config: a git command, or a built-in action.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Cmd {
    pub name: String,
    pub key: String,
    pub run: String,
    pub action: String,
    pub bar: bool,
    pub confirm: bool,
    pub prompt: String,
    pub output: String,
    pub branch: bool,
    pub clean: bool,
    pub worktree: bool,
}

impl Cmd {
    /// Approve, Request changes and Comment: on the desktop the review box does these.
    pub fn reviews(&self) -> bool {
        matches!(self.action.as_str(), "approve" | "request_changes" | "comment")
    }
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Pr {
    pub number: u64,
    pub title: String,
    pub url: String,
    #[serde(rename = "isDraft")]
    pub is_draft: bool,
    pub state: String,
    #[serde(rename = "updatedAt")]
    pub updated_at: String,
    #[serde(rename = "createdAt")]
    pub created_at: String,
    #[serde(rename = "headRefName")]
    pub head: String,
    #[serde(rename = "baseRefName")]
    pub base: String,
    #[serde(rename = "isCrossRepository")]
    pub cross_repo: bool,
    pub mergeable: String,
    #[serde(rename = "reviewDecision")]
    pub review_decision: String,
    pub author: String,
    pub repo: String,
    pub comments: u64,
    #[serde(rename = "checkState")]
    pub check_state: String,
}

impl Pr {
    pub fn key(&self) -> String {
        format!("{}#{}", self.repo.to_lowercase(), self.number)
    }
    pub fn name(&self) -> &str {
        self.repo.split_once('/').map(|(_, n)| n).unwrap_or(&self.repo)
    }
    pub fn conflict(&self) -> bool {
        self.mergeable == "CONFLICTING"
    }
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Detail {
    #[serde(flatten)]
    pub pr: Pr,
    /// GitHub's ID for the PR, for comments and reviews.
    pub node_id: String,
    pub body: String,
    pub additions: u64,
    pub deletions: u64,
    pub changed_files: u64,
    pub commits: u64,
    #[serde(deserialize_with = "null_vec")]
    pub labels: Vec<String>,
    #[serde(deserialize_with = "null_vec")]
    pub reviewers: Vec<Reviewer>,
    #[serde(deserialize_with = "null_vec")]
    pub checks: Vec<Check>,
    #[serde(deserialize_with = "null_vec")]
    pub timeline: Vec<Event>,
    #[serde(deserialize_with = "null_vec")]
    pub commit_list: Vec<Commit>,
    #[serde(deserialize_with = "null_vec")]
    pub files: Vec<ChangedFile>,
    /// Set while auto-merge is on: GitHub merges the PR once checks and reviews pass.
    pub auto_merge: Option<AutoMerge>,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct AutoMerge {
    /// MERGE, SQUASH or REBASE.
    pub method: String,
    pub by: String,
    pub at: String,
}

impl AutoMerge {
    /// The method the way GitHub's merge button names it.
    pub fn word(&self) -> &'static str {
        match self.method.as_str() {
            "SQUASH" => "squash and merge",
            "REBASE" => "rebase and merge",
            _ => "merge",
        }
    }
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Commit {
    pub oid: String,
    pub short: String,
    pub headline: String,
    pub body: String,
    pub at: String,
    pub author: String,
    pub additions: u64,
    pub deletions: u64,
    pub check_state: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ChangedFile {
    pub path: String,
    pub additions: u64,
    pub deletions: u64,
    pub change_type: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct FetchedImage {
    pub data: String,
    #[serde(rename = "type")]
    pub kind: String,
}

impl Detail {
    /// Passed, failed and still-running checks.
    pub fn check_counts(&self) -> (usize, usize, usize) {
        let (mut pass, mut fail, mut pend) = (0, 0, 0);
        for c in &self.checks {
            match check_kind(&c.state) {
                CheckKind::Pass => pass += 1,
                CheckKind::Fail => fail += 1,
                CheckKind::Pending => pend += 1,
            }
        }
        (pass, fail, pend)
    }
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Reviewer {
    pub login: String,
    pub state: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Check {
    pub name: String,
    pub state: String,
    /// Its log or details page; empty when the CI service gave none.
    pub url: String,
    /// Its GitHub Actions run; 0 for other CI services and commit statuses.
    #[serde(rename = "runId")]
    pub run_id: i64,
}

impl Check {
    /// Its log link, when it is one to open in the browser (http or https only).
    pub fn log_url(&self) -> Option<&str> {
        (self.url.starts_with("https://") || self.url.starts_with("http://")).then_some(self.url.as_str())
    }
}

pub enum CheckKind {
    Pass,
    Fail,
    Pending,
}

pub fn check_kind(state: &str) -> CheckKind {
    match state {
        "SUCCESS" | "NEUTRAL" | "SKIPPED" => CheckKind::Pass,
        "FAILURE" | "ERROR" | "CANCELLED" | "TIMED_OUT" | "ACTION_REQUIRED" | "STARTUP_FAILURE" => CheckKind::Fail,
        _ => CheckKind::Pending,
    }
}

/// One entry in the conversation: a comment, a review, or a code thread.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Event {
    pub kind: String,
    pub author: String,
    pub state: String,
    pub body: String,
    pub at: String,
    pub path: String,
    pub line: u64,
    pub diff_hunk: String,
    /// Review threads: GitHub's ID, for replies.
    pub thread_id: String,
    /// Review threads: RIGHT (new and unchanged lines) or LEFT (removed lines).
    pub side: String,
    pub resolved: bool,
    pub outdated: bool,
    #[serde(deserialize_with = "null_vec")]
    pub replies: Vec<Reply>,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Reply {
    pub author: String,
    pub body: String,
    pub at: String,
}

/// Where a PR's commands run, and what state that checkout is in.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Folder {
    pub path: String,
    pub clone: String,
    pub remote: String,
    pub worktree: bool,
    pub exists: bool,
    pub branch: String,
    #[serde(deserialize_with = "null_vec")]
    pub changes: Vec<String>,
    pub rebasing: bool,
}

/// A command ready to show and run. It goes back to the server unchanged to run.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Plan {
    pub title: String,
    pub pre: String,
    pub script: String,
    pub action: String,
    /// The text a GitHub write sends (a comment, what should change).
    pub input: String,
    pub folder: Folder,
    pub confirm: bool,
    pub terminal: bool,
    pub silent: bool,
    #[serde(deserialize_with = "null_vec")]
    pub changes: Vec<String>,
    pub pr: Pr,
}

impl Plan {
    /// Every shell command the plan runs, in order.
    pub fn full_script(&self) -> String {
        match (self.pre.is_empty(), self.script.is_empty()) {
            (true, _) => self.script.clone(),
            (false, true) => self.pre.clone(),
            (false, false) => format!("{} && {}", self.pre, self.script),
        }
    }
    /// Where it runs, for display.
    pub fn dir(&self) -> String {
        if self.action == "browse" || self.action == "clone" {
            "~".into()
        } else {
            short_home(&self.folder.path)
        }
    }
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct ExecResult {
    pub ok: bool,
    pub title: String,
    pub output: String,
    pub error: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct SearchResult {
    pub found: bool,
    #[serde(deserialize_with = "null_vec")]
    pub prs: Vec<Pr>,
    pub viewer: String,
    pub at: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct CachedDetail {
    pub detail: Option<Detail>,
    pub fresh: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct SetPath {
    pub note: String,
    pub path: String,
}

/// "4m", "3h", "2d" since an RFC 3339 time.
pub fn ago(ts: &str) -> String {
    let Ok(t) = DateTime::parse_from_rfc3339(ts) else {
        return String::new();
    };
    ago_secs((Utc::now() - t.with_timezone(&Utc)).num_seconds())
}

pub fn ago_secs(s: i64) -> String {
    match s.max(0) {
        s if s < 60 => "now".into(),
        s if s < 3600 => format!("{}m", s / 60),
        s if s < 48 * 3600 => format!("{}h", s / 3600),
        s => format!("{}d", s / 86400),
    }
}

pub fn short_home(p: &str) -> String {
    match std::env::var("HOME") {
        Ok(h) if !h.is_empty() && p.starts_with(&h) => format!("~{}", &p[h.len()..]),
        _ => p.to_string(),
    }
}

/// What first-run setup needs to know.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct SetupStatus {
    pub gh_installed: bool,
    pub gh_version: String,
    pub brew_found: bool,
    pub signed_in: bool,
    pub login: String,
    pub config_exists: bool,
    pub config_path: String,
    #[serde(deserialize_with = "null_vec")]
    pub folders: Vec<FolderChoice>,
    #[serde(deserialize_with = "null_vec")]
    pub editors: Vec<EditorChoice>,
    pub editor: String,
    pub worktree: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct FolderChoice {
    pub path: String,
    pub clones: u64,
    pub saved: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct EditorChoice {
    pub name: String,
    pub label: String,
    pub installed: bool,
    pub run: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct DeviceCode {
    pub code: String,
    pub url: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct LoginDone {
    pub ok: bool,
    pub login: String,
}

/// One file's change from GitHub, with its unified-diff patch (empty for binary or
/// very large files).
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct FileDiff {
    pub path: String,
    pub previous_path: String,
    pub status: String,
    pub additions: u64,
    pub deletions: u64,
    pub patch: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct CommitDiff {
    pub oid: String,
    pub message: String,
    pub author: String,
    pub at: String,
    #[serde(deserialize_with = "null_vec")]
    pub files: Vec<FileDiff>,
}

/// A comment on one line of the diff, waiting to be sent with a review.
#[derive(Clone, Debug, Serialize)]
pub struct DraftThread {
    pub path: String,
    pub line: u32,
    /// RIGHT: added or unchanged lines (new numbers); LEFT: removed lines (old numbers).
    pub side: &'static str,
    pub body: String,
}

#[cfg(test)]
mod tests {
    use super::{Check, Cmd, Detail};

    #[test]
    fn checks_read_links_and_runs() {
        let d: Detail = serde_json::from_str(
            r#"{"checks": [
                {"name": "unit", "state": "FAILURE", "url": "https://github.com/a/b/actions/runs/5/job/9", "runId": 5},
                {"name": "codecov/patch", "state": "FAILURE", "url": "javascript:alert(1)"},
                {"name": "lint", "state": "SUCCESS"}
            ]}"#,
        )
        .unwrap();
        assert_eq!(d.checks[0].run_id, 5);
        assert_eq!(d.checks[0].log_url(), Some("https://github.com/a/b/actions/runs/5/job/9"));
        assert_eq!(d.checks[1].log_url(), None);
        // Saved before checks had links: still reads, nothing to open.
        assert_eq!(d.checks[2].log_url(), None);
        assert_eq!(Check::default().run_id, 0);
    }

    #[test]
    fn auto_merge_and_review_commands() {
        let d: Detail = serde_json::from_str(r#"{"autoMerge": {"method": "SQUASH", "by": "tnguyen", "at": "2026-11-04T10:00:00Z"}}"#).unwrap();
        assert_eq!(d.auto_merge.as_ref().map(|a| (a.word(), a.by.as_str())), Some(("squash and merge", "tnguyen")));
        // Off, or saved before PR Deck knew about auto-merge.
        assert!(serde_json::from_str::<Detail>("{}").unwrap().auto_merge.is_none());

        let cmds: Vec<Cmd> = serde_json::from_str(r#"[{"name": "Approve", "action": "approve"}, {"name": "Merge when ready", "action": "automerge"}, {"name": "Push", "run": "git push"}]"#).unwrap();
        let reviews: Vec<&str> = cmds.iter().filter(|c| c.reviews()).map(|c| c.name.as_str()).collect();
        assert_eq!(reviews, ["Approve"]);
    }
}
