//! The replies `prdeck serve` sends, read into the app's own types. The Go test
//! TestContract (internal/server) saves them from the demo data in tests/fixtures, so
//! a field renamed or retyped on either side fails a test. After changing a reply on
//! purpose: `go test ./internal/server -run TestContract -update`, then `cargo test`.

use std::{fs, path::PathBuf};

use base64::Engine as _;
use serde::de::{self, DeserializeOwned};
use serde_json::Value;

use crate::model::{
    CachedDetail, ChangedFile, Check, Cmd, Commit, CommitDiff, Detail, DeviceCode, EditorChoice, Event, ExecResult,
    FetchedImage, FileDiff, Filter, Folder, FolderChoice, Hello, LoginDone, Plan, Pr, Reply, Reviewer, SearchResult,
    SetPath, SetupStatus,
};

/// Every reply read below. A fixture not listed here fails `every_fixture_is_read`.
const READ: &[&str] = &[
    "hello", "setupStatus", "cachedSearch", "search", "cachedDetail", "detail", "folder", "prepare", "execute",
    "clonePlan", "findClones", "setClonePath", "image", "prDiff", "commitDiff", "comment", "review", "replyThread",
    "loginStart", "loginWait", "loginCancel", "installGh", "saveSetup",
];

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures")
}

/// tests/fixtures/<method>.json, read as T.
fn read<T: DeserializeOwned>(method: &str) -> T {
    let path = dir().join(format!("{method}.json"));
    let text = fs::read_to_string(&path).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    serde_json::from_str(&text).unwrap_or_else(|e| panic!("{method}.json does not fit the app's type: {e}"))
}

/// A value serde did not make up. serde fills a missing field with its default, so a
/// field renamed on one side arrives empty rather than failing.
trait Filled {
    fn filled(&self) -> bool;
}
impl Filled for String {
    fn filled(&self) -> bool {
        !self.is_empty()
    }
}
impl Filled for u64 {
    fn filled(&self) -> bool {
        *self > 0
    }
}
impl Filled for f64 {
    fn filled(&self) -> bool {
        *self > 0.
    }
}
impl Filled for bool {
    fn filled(&self) -> bool {
        *self
    }
}
impl<T> Filled for Vec<T> {
    fn filled(&self) -> bool {
        !self.is_empty()
    }
}

/// Asserts that each field has a value, naming the one that does not.
macro_rules! filled {
    ($($e:expr),+ $(,)?) => {
        $(assert!(Filled::filled(&$e), "{} is empty: renamed or missing in the reply?", stringify!($e));)+
    };
}

/// Fails when a field the server sent is lost by reading it into T and writing it
/// back. Plans and PRs go back to the server, so they must keep every field.
fn round_trips<T: DeserializeOwned + serde::Serialize>(sent: &Value, what: &str) {
    let back = serde_json::to_value(serde_json::from_value::<T>(sent.clone()).unwrap()).unwrap();
    fn keys(sent: &Value, back: &Value, at: &str) {
        if let (Value::Object(s), Value::Object(b)) = (sent, back) {
            for (k, v) in s {
                let at = format!("{at}.{k}");
                let Some(bv) = b.get(k) else { panic!("{at} is lost on the way back to the server") };
                keys(v, bv, &at);
            }
        }
    }
    keys(sent, &back, what);
}

/// The names T reads from a reply, as serde's derive lists them: Grab stops at the
/// struct before reading anything. A type with a flattened field lists none.
fn fields<T: DeserializeOwned>() -> &'static [&'static str] {
    struct Grab<'a>(&'a mut &'static [&'static str]);
    impl<'de> de::Deserializer<'de> for Grab<'_> {
        type Error = de::value::Error;
        fn deserialize_any<V: de::Visitor<'de>>(self, _: V) -> Result<V::Value, Self::Error> {
            Err(de::Error::custom("not a struct"))
        }
        fn deserialize_struct<V: de::Visitor<'de>>(
            self,
            _: &'static str,
            fields: &'static [&'static str],
            _: V,
        ) -> Result<V::Value, Self::Error> {
            *self.0 = fields;
            Err(de::Error::custom("got the names"))
        }
        serde::forward_to_deserialize_any! {
            bool i8 i16 i32 i64 i128 u8 u16 u32 u64 u128 f32 f64 char str string bytes byte_buf option unit
            unit_struct newtype_struct seq tuple tuple_struct map enum identifier ignored_any
        }
    }
    let mut names: &'static [&'static str] = &[];
    let _ = T::deserialize(Grab(&mut names));
    names
}

/// Fails when a field T reads is not in the reply at all. `filled!` only catches a
/// rename where the demo has a value; this catches it where the value is empty too.
fn sent<T: DeserializeOwned>(reply: &Value, what: &str) {
    let names = fields::<T>();
    assert!(!names.is_empty(), "{what}: no field names to check");
    for n in names {
        assert!(reply.get(n).is_some(), "{what}.{n} is not in the reply: renamed on one side?");
    }
}

/// `sent` for each item of a list in the reply.
fn each_sent<T: DeserializeOwned>(list: &Value, what: &str) {
    let items = list.as_array().unwrap_or_else(|| panic!("{what} is not a list"));
    assert!(!items.is_empty(), "{what} is empty");
    for v in items {
        sent::<T>(v, what);
    }
}

fn check_pr(p: &Pr) {
    filled!(p.number, p.title, p.url, p.repo, p.author, p.head, p.base, p.state, p.mergeable);
    filled!(p.updated_at, p.created_at, p.check_state);
}

#[test]
fn every_fixture_is_read() {
    for entry in fs::read_dir(dir()).unwrap() {
        let name = entry.unwrap().file_name().into_string().unwrap();
        let method = name.strip_suffix(".json").unwrap_or(&name);
        assert!(READ.contains(&method), "{name} is not read by these tests; add a check for it");
    }
}

#[test]
fn every_field_is_sent() {
    let h: Value = read("hello");
    sent::<Hello>(&h, "hello");
    each_sent::<Filter>(&h["filters"], "hello.filters");
    each_sent::<Cmd>(&h["commands"], "hello.commands");
    let s: Value = read("setupStatus");
    sent::<SetupStatus>(&s, "setupStatus");
    each_sent::<FolderChoice>(&s["folders"], "setupStatus.folders");
    each_sent::<EditorChoice>(&s["editors"], "setupStatus.editors");

    // search sends no "found", which only the cached reply has.
    let cached: Value = read("cachedSearch");
    sent::<SearchResult>(&cached, "cachedSearch");
    each_sent::<Pr>(&cached["prs"], "cachedSearch.prs");
    each_sent::<Pr>(&read::<Value>("search")["prs"], "search.prs");

    // Detail flattens Pr into itself, so serde lists no names for it; pr_detail checks
    // its own fields have values.
    let cd: Value = read("cachedDetail");
    sent::<CachedDetail>(&cd, "cachedDetail");
    let d: Value = read("detail");
    for (v, what) in [(&cd["detail"], "cachedDetail.detail"), (&d, "detail")] {
        sent::<Pr>(v, what);
        each_sent::<Reviewer>(&v["reviewers"], &format!("{what}.reviewers"));
        each_sent::<Check>(&v["checks"], &format!("{what}.checks"));
        each_sent::<Event>(&v["timeline"], &format!("{what}.timeline"));
        each_sent::<Commit>(&v["commitList"], &format!("{what}.commitList"));
        each_sent::<ChangedFile>(&v["files"], &format!("{what}.files"));
    }
    let t = d["timeline"].as_array().unwrap().iter().find(|e| e["kind"] == "thread").unwrap();
    each_sent::<Reply>(&t["replies"], "detail.timeline.replies");

    sent::<Folder>(&read("folder"), "folder");
    for m in ["prepare", "clonePlan"] {
        let p: Value = read(m);
        sent::<Plan>(&p, m);
        sent::<Folder>(&p["folder"], &format!("{m}.folder"));
        sent::<Pr>(&p["pr"], &format!("{m}.pr"));
    }
    sent::<ExecResult>(&read("execute"), "execute");
    sent::<SetPath>(&read("setClonePath"), "setClonePath");
    sent::<FetchedImage>(&read("image"), "image");
    each_sent::<FileDiff>(&read("prDiff"), "prDiff");
    let c: Value = read("commitDiff");
    sent::<CommitDiff>(&c, "commitDiff");
    each_sent::<FileDiff>(&c["files"], "commitDiff.files");
    sent::<DeviceCode>(&read("loginStart"), "loginStart");
    sent::<LoginDone>(&read("loginWait"), "loginWait");
}

#[test]
fn hello_and_setup() {
    let h: Hello = read("hello");
    filled!(h.demo, h.version, h.viewer, h.refresh, h.editor, h.filters, h.commands);
    for f in &h.filters {
        filled!(f.name, f.query);
    }
    let r = h.commands.iter().find(|c| c.name == "Rebase").expect("a Rebase command");
    filled!(r.key, r.run, r.bar, r.confirm, r.branch, r.clean);
    let open = h.commands.iter().find(|c| c.name == "Open in editor").expect("an Open in editor command");
    filled!(open.action);

    let s: SetupStatus = read("setupStatus");
    filled!(s.gh_installed, s.signed_in, s.login, s.editor, s.folders, s.editors);
    let code = s.folders.iter().find(|f| f.path == "~/code").expect("~/code in folders");
    filled!(code.clones, code.saved);
    for e in &s.editors {
        filled!(e.name, e.label);
    }
}

#[test]
fn pr_lists() {
    let cached: SearchResult = read("cachedSearch");
    filled!(cached.found, cached.at);
    let fresh: SearchResult = read("search");
    for r in [&cached, &fresh] {
        filled!(r.viewer, r.prs);
        r.prs.iter().for_each(check_pr);
    }
    let p = fresh.prs.iter().find(|p| p.number == 4821).unwrap();
    filled!(p.review_decision, p.comments);
    assert!(p.conflict());
    assert!(fresh.prs.iter().any(|p| p.is_draft), "a draft PR");
    let raw: Value = read("search");
    for p in raw["prs"].as_array().unwrap() {
        round_trips::<Pr>(p, "pr");
    }
}

#[test]
fn pr_detail() {
    let cached: CachedDetail = read("cachedDetail");
    filled!(cached.fresh);
    assert!(cached.detail.is_some());
    let d: Detail = read("detail");
    check_pr(&d.pr);
    filled!(d.node_id, d.body, d.additions, d.deletions, d.changed_files, d.commits, d.labels, d.reviewers, d.checks);
    filled!(d.timeline, d.commit_list, d.files);
    for r in &d.reviewers {
        filled!(r.login, r.state);
    }
    for c in &d.checks {
        filled!(c.name, c.state);
    }
    for f in &d.files {
        filled!(f.path, f.change_type, f.additions);
    }
    let c = &d.commit_list[0];
    filled!(c.oid, c.short, c.headline, c.at, c.author, c.additions, c.check_state);

    for e in &d.timeline {
        filled!(e.kind, e.author, e.body, e.at);
    }
    let review = d.timeline.iter().find(|e| e.kind == "review").expect("a review");
    filled!(review.state);
    let t = d.timeline.iter().find(|e| e.kind == "thread").expect("a code thread");
    filled!(t.path, t.line, t.diff_hunk, t.thread_id, t.side, t.replies);
    let r = &t.replies[0];
    filled!(r.author, r.body, r.at);
}

#[test]
fn commands() {
    let f: Folder = read("folder");
    filled!(f.exists, f.path, f.clone, f.remote, f.branch, f.changes);

    let raw: Value = read("prepare");
    round_trips::<Plan>(&raw, "plan");
    let p: Plan = read("prepare");
    filled!(p.title, p.confirm, p.pre, p.script, p.changes, p.folder.path, p.pr.number);

    let x: ExecResult = read("execute");
    filled!(x.title, x.output, x.error);
    assert!(!x.ok && x.output.contains("CONFLICT"));

    let raw: Value = read("clonePlan");
    round_trips::<Plan>(&raw, "plan");
    let c: Plan = read("clonePlan");
    filled!(c.title, c.action, c.script, c.confirm, c.pr.repo);
}

#[test]
fn clones() {
    let found: Vec<String> = read("findClones");
    filled!(found);
    let set: SetPath = read("setClonePath");
    filled!(set.path);
}

#[test]
fn diffs() {
    let files: Vec<FileDiff> = read("prDiff");
    filled!(files);
    for f in &files {
        filled!(f.path, f.status, f.additions, f.patch);
    }
    let c: CommitDiff = read("commitDiff");
    filled!(c.oid, c.message, c.author, c.at, c.files);
    assert!(crate::diff::parse(&c.files[0].patch).len() > 1);
}

#[test]
fn image() {
    let img: FetchedImage = read("image");
    filled!(img.data, img.kind);
    let bytes = base64::engine::general_purpose::STANDARD.decode(&img.data).unwrap();
    assert!(bytes.starts_with(b"\x89PNG"));
}

#[test]
fn sign_in_and_writes() {
    let code: DeviceCode = read("loginStart");
    filled!(code.code, code.url);
    let done: LoginDone = read("loginWait");
    filled!(done.ok, done.login);
    // The app only needs these to succeed.
    for m in ["loginCancel", "installGh", "saveSetup", "comment", "review", "replyThread"] {
        let v: Value = read(m);
        assert_eq!(v["ok"], Value::Bool(true), "{m}");
    }
}
