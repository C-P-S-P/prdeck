//! The main window: repositories and pull requests on the left, the selected PR on
//! the right, the command bar at the bottom, and the ⌘K filter palette on top.

use std::collections::{HashMap, HashSet};
use std::time::{Duration, Instant};

use base64::Engine as _;
use futures::StreamExt as _;
use gpui_kit::component::input::{Input, InputEvent, InputState};
use gpui_kit::component::resizable::{h_resizable, resizable_panel, v_resizable};
use gpui_kit::prelude::*;
use gpui_kit::*;
use serde_json::json;

use crate::layout::Layout;
use crate::markdown::{self, Images};
use crate::model::*;
use crate::rpc::Rpc;
use crate::theme::*;

mod diffs;
mod inline;
mod review;
mod setup;
use diffs::*;
use inline::*;
use review::*;
use setup::*;

gpui_kit::actions!(
    prdeck,
    [GoToPr, Filters, CloseTab, ReopenTab, Refresh, NextTab, PrevTab, OpenConfig, OpenInEditor, OpenOnGitHub, ToggleSidebar, RunSetup, CommentOnPr]
);

#[derive(Clone, Copy, PartialEq)]
enum Section {
    Conversation,
    Checks,
    Commits,
    Files,
}

enum FolderState {
    Loading,
    Ready(Folder),
    NoClone,
    Failed(String),
}

/// A command on its way from key press to output, shown above the bottom bar.
struct Run {
    pr: Pr,
    cmd: Cmd,
    input: String,
    stage: Stage,
    _sub: Option<Subscription>,
}

enum Stage {
    /// The command asks for {input} first.
    Prompt(Entity<InputState>),
    Preparing,
    /// Shown before running: the command asks to be confirmed, or uncommitted
    /// changes must be stashed or dropped first.
    Ready { plan: Plan, drop_armed: bool },
    Running(Plan),
    Done { title: String, ok: bool, output: String },
    /// The PR's repo has no local clone: pick one found on disk, type a path, or clone.
    NoClone { found: Option<Vec<String>>, path: Entity<InputState> },
    Failed(String),
}

#[derive(Clone, Copy, PartialEq)]
enum PaletteMode {
    /// ⌘K: saved filters, or any GitHub search qualifier.
    Filters,
    /// ⌘T / ⌘P: jump to a pull request by number, title, repo or author.
    Prs,
}

struct Palette {
    input: Entity<InputState>,
    hi: usize,
    mode: PaletteMode,
    _sub: Subscription,
}

#[derive(Clone)]
enum Apply {
    Filter(usize),
    Query(String),
    Pr(Pr),
}

struct PaletteItem {
    group: &'static str,
    label: String,
    qual: String,
    apply: Apply,
    active: bool,
}

pub struct Deck {
    rpc: Option<Rpc>,
    focus: FocusHandle,
    hello: Hello,
    banner: Option<String>,
    filter_ix: usize,
    custom: Option<String>,
    prs: Vec<Pr>,
    synced: Option<Instant>,
    loading: bool,
    seq: u64,
    repo: Option<String>,
    selected: Option<String>,
    tabs: Vec<Pr>,
    details: HashMap<String, Detail>,
    detail_loading: HashSet<String>,
    detail_err: HashMap<String, String>,
    folders: HashMap<String, FolderState>,
    section: Section,
    run: Option<Run>,
    palette: Option<Palette>,
    images: Images,
    layout: Layout,
    /// Recently closed tabs, for ⌘⇧T.
    closed: Vec<Pr>,
    /// A PR to open once a search returns it (key: "owner/repo#n").
    want: Option<String>,
    /// Scroll position of the PR's conversation; back to the top on a new PR.
    detail_scroll: ScrollHandle,
    /// Set by n or Jump to first new: the first new item in the conversation scrolls
    /// itself to the top the next time it is laid out.
    jump_new: std::rc::Rc<std::cell::Cell<bool>>,
    /// The selected PR once its detail has come in: loading it again while it stays
    /// selected keeps what was new when you opened it.
    looked: Option<String>,
    /// Diffs loaded from GitHub (by "pr:…" or "commit:…"), the commit open in
    /// Commits, and files opened or closed by hand in a diff.
    diffs: HashMap<String, DiffState>,
    commit: Option<String>,
    file_open: HashMap<String, bool>,
    /// Line comments waiting to be sent with a review, by PR key, and the comment box
    /// open on a line.
    pending: HashMap<String, Vec<DraftThread>>,
    line_editor: Option<LineEditor>,
    drag: Option<LineDrag>,
    /// Reply boxes, by PR key, and the code thread being replied to.
    drafts: HashMap<String, Draft>,
    thread_reply: Option<ThreadReply>,
    /// A short green note after posting.
    notice: Option<String>,
    /// First-run setup, while it shows.
    setup: Option<Setup>,
    force_setup: bool,
    _timer: Option<Task<()>>,
}

impl Deck {
    pub fn new(demo: bool, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let (images, queue) = Images::new();
        let mut this = Self {
            rpc: None,
            focus,
            hello: Hello::default(),
            banner: None,
            filter_ix: 0,
            custom: None,
            prs: vec![],
            synced: None,
            loading: true,
            seq: 0,
            repo: None,
            selected: None,
            tabs: vec![],
            details: HashMap::new(),
            detail_loading: HashSet::new(),
            detail_err: HashMap::new(),
            folders: HashMap::new(),
            section: Section::Conversation,
            run: None,
            palette: None,
            images,
            layout: Layout::load(),
            closed: vec![],
            want: None,
            detail_scroll: ScrollHandle::new(),
            jump_new: Default::default(),
            looked: None,
            setup: None,
            diffs: HashMap::new(),
            commit: None,
            file_open: HashMap::new(),
            pending: HashMap::new(),
            line_editor: None,
            drag: None,
            drafts: HashMap::new(),
            thread_reply: None,
            notice: None,
            force_setup: std::env::args().any(|a| a == "--setup"),
            _timer: None,
        };
        let signed_out = std::env::args().any(|a| a == "--signed-out");
        match Rpc::start(demo, signed_out) {
            Ok(rpc) => {
                this.rpc = Some(rpc);
                this.say_hello(window, cx);
                this.load_images(queue, cx);
            }
            Err(e) => {
                this.loading = false;
                this.banner = Some(format!("Could not start prdeck: {e:#}"));
            }
        }
        this
    }

    /// Downloads images from PR text as the markdown view asks for them, several at
    /// a time, and redraws as each arrives.
    fn load_images(&mut self, mut queue: futures::channel::mpsc::UnboundedReceiver<String>, cx: &mut Context<Self>) {
        let (Some(rpc), images) = (self.rpc.clone(), self.images.clone()) else { return };
        cx.spawn(async move |this, cx| {
            while let Some(url) = queue.next().await {
                let fetch = rpc.call::<FetchedImage>("image", json!({ "url": url }));
                let (images, this) = (images.clone(), this.clone());
                cx.spawn(async move |cx| {
                    let bytes = fetch.await.ok().and_then(|f| {
                        let data = base64::engine::general_purpose::STANDARD.decode(f.data).ok()?;
                        Some((data, f.kind))
                    });
                    images.put(url, bytes);
                    let _ = this.update(cx, |_, cx| cx.notify());
                })
                .detach();
            }
        })
        .detach();
    }

    // ── data ────────────────────────────────────────────────────────────────

    fn say_hello(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let hello = rpc.call::<Hello>("hello", json!({}));
        let status = rpc.call::<SetupStatus>("setupStatus", json!({}));
        cx.spawn_in(window, async move |this, cx| {
            let res = hello.await;
            let status = status.await.ok();
            let _ = this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(h) => {
                        if !h.warnings.is_empty() {
                            this.banner = Some(format!("Config: {}", h.warnings.join(" · ")));
                        }
                        let demo = h.demo;
                        this.images.show_faces(!demo);
                        this.hello = h;
                        let signed_in = status.as_ref().is_none_or(|st| st.signed_in);
                        // Setup shows on the first launch and whenever nobody is signed
                        // in; the demo skips it unless asked (--setup).
                        let first_run = !this.layout.onboarded && !demo;
                        if this.setup.is_none() && (!signed_in || first_run || this.force_setup) {
                            this.force_setup = false;
                            this.open_setup(window, cx);
                        }
                        if signed_in {
                            this.load(cx);
                        } else {
                            this.loading = false;
                        }
                        this.start_timer(cx);
                    }
                    Err(e) => {
                        this.loading = false;
                        this.banner = Some(e.message);
                    }
                }
                cx.notify();
            });
        })
        .detach();
    }

    /// Redraws every 30 seconds ("Synced 2m ago") and searches again every
    /// `github.refresh`.
    fn start_timer(&mut self, cx: &mut Context<Self>) {
        let every = Duration::from_secs_f64(self.hello.refresh.max(30.0));
        self._timer = Some(cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_secs(30)).await;
                let ok = this.update(cx, |this, cx| {
                    if this.synced.is_some_and(|t| t.elapsed() >= every) && !this.loading {
                        this.load(cx);
                    }
                    cx.notify();
                });
                if ok.is_err() {
                    break;
                }
            }
        }));
    }

    fn query(&self) -> String {
        self.custom
            .clone()
            .or_else(|| self.hello.filters.get(self.filter_ix).map(|f| f.query.clone()))
            .unwrap_or_else(|| "involves:@me".into())
    }

    fn query_label(&self) -> String {
        match &self.custom {
            Some(q) => q.clone(),
            None => self.hello.filters.get(self.filter_ix).map(|f| f.name.clone()).unwrap_or_default(),
        }
    }

    /// Shows saved results at once, then asks GitHub.
    fn load(&mut self, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        self.seq += 1;
        let seq = self.seq;
        let q = self.query();
        self.loading = true;
        let cached = rpc.call::<SearchResult>("cachedSearch", json!({ "query": q }));
        let fresh = rpc.call::<SearchResult>("search", json!({ "query": q }));
        cx.spawn(async move |this, cx| {
            if let Ok(r) = cached.await {
                if r.found {
                    let _ = this.update(cx, |this, cx| {
                        if this.seq == seq {
                            this.set_prs(r.prs, cx);
                            cx.notify();
                        }
                    });
                }
            }
            let res = fresh.await;
            let _ = this.update(cx, |this, cx| {
                if this.seq != seq {
                    return;
                }
                this.loading = false;
                match res {
                    Ok(r) => {
                        this.set_prs(r.prs, cx);
                        this.synced = Some(Instant::now());
                        if !r.viewer.is_empty() {
                            this.hello.viewer = r.viewer;
                        }
                        if this.banner.as_deref().is_some_and(|b| b.starts_with("GitHub:")) {
                            this.banner = None;
                        }
                    }
                    Err(e) => this.banner = Some(format!("GitHub: {}", e.message)),
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn set_prs(&mut self, prs: Vec<Pr>, cx: &mut Context<Self>) {
        for t in self.tabs.iter_mut() {
            if let Some(p) = prs.iter().find(|p| p.key() == t.key()) {
                *t = p.clone();
            }
        }
        self.prs = prs;
        // The PR on screen changed since it was loaded (a comment, or your own push):
        // load it again, as the terminal does, which tags what is new and marks it seen.
        if let Some(pr) = self.selected_pr().filter(|p| p.unread) {
            self.load_detail(pr, cx);
        }
        if let Some(want) = self.want.clone() {
            if let Some(pr) = self.prs.iter().find(|p| p.key() == want).cloned() {
                self.want = None;
                self.select(pr, cx);
                return;
            }
        }
        if self.selected.is_none() {
            if let Some(first) = self.visible().first().cloned() {
                self.select(first, cx);
            }
        }
    }

    fn visible(&self) -> Vec<Pr> {
        self.prs.iter().filter(|p| self.repo.as_ref().is_none_or(|r| &p.repo == r)).cloned().collect()
    }

    fn repos(&self) -> Vec<(String, usize)> {
        let mut counts: Vec<(String, usize)> = vec![];
        for p in &self.prs {
            match counts.iter_mut().find(|(r, _)| r == &p.repo) {
                Some((_, n)) => *n += 1,
                None => counts.push((p.repo.clone(), 1)),
            }
        }
        counts.sort_by(|a, b| b.1.cmp(&a.1).then(a.0.cmp(&b.0)));
        counts
    }

    fn selected_pr(&self) -> Option<Pr> {
        let key = self.selected.as_ref()?;
        self.prs.iter().chain(self.tabs.iter()).find(|p| &p.key() == key).cloned()
    }

    fn select(&mut self, pr: Pr, cx: &mut Context<Self>) {
        let key = pr.key();
        if self.selected.as_ref() != Some(&key) {
            self.detail_scroll.set_offset(point(px(0.), px(0.)));
            self.commit = None;
            self.jump_new.set(false);
            self.looked = None;
        }
        self.selected = Some(key.clone());
        match self.tabs.iter_mut().find(|t| t.key() == key) {
            Some(t) => *t = pr.clone(),
            None => {
                self.tabs.push(pr.clone());
                if self.tabs.len() > 6 {
                    self.tabs.remove(0);
                }
            }
        }
        self.load_detail(pr.clone(), cx);
        self.load_folder(pr, cx);
        self.load_section(cx);
        cx.notify();
    }

    fn close_tab(&mut self, key: &str, cx: &mut Context<Self>) {
        let Some(i) = self.tabs.iter().position(|t| t.key() == key) else { return };
        let closed = self.tabs.remove(i);
        self.closed.retain(|p| p.key() != closed.key());
        self.closed.push(closed);
        if self.selected.as_deref() == Some(key) {
            self.selected = None;
            if let Some(next) = self.tabs.get(i.min(self.tabs.len().saturating_sub(1))).cloned() {
                self.select(next, cx);
            }
        }
        cx.notify();
    }

    fn move_selection(&mut self, delta: isize, cx: &mut Context<Self>) {
        let list = self.visible();
        if list.is_empty() {
            return;
        }
        let cur = self.selected.as_ref().and_then(|k| list.iter().position(|p| &p.key() == k));
        let next = match cur {
            Some(i) => (i as isize + delta).clamp(0, list.len() as isize - 1) as usize,
            None => 0,
        };
        self.select(list[next].clone(), cx);
    }

    fn load_detail(&mut self, pr: Pr, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let key = pr.key();
        if !self.detail_loading.insert(key.clone()) {
            return;
        }
        let cached = rpc.call::<CachedDetail>("cachedDetail", json!({ "pr": pr }));
        cx.spawn(async move |this, cx| {
            let fresh = match cached.await {
                Ok(c) => {
                    let fresh = c.fresh;
                    if let Some(d) = c.detail {
                        let _ = this.update(cx, |this, cx| {
                            this.put_detail(d);
                            cx.notify();
                        });
                    }
                    fresh
                }
                Err(_) => false,
            };
            if !fresh {
                let res = rpc.call::<Detail>("detail", json!({ "pr": pr })).await;
                let _ = this.update(cx, |this, _| match res {
                    Ok(d) => {
                        this.detail_err.remove(&pr.key());
                        this.put_detail(d);
                    }
                    Err(e) => {
                        this.detail_err.insert(pr.key(), e.message);
                    }
                });
            }
            let _ = this.update(cx, |this, cx| {
                this.detail_loading.remove(&pr.key());
                cx.notify();
            });
        })
        .detach();
    }

    /// Shows a PR's detail. The core counts that as opening the PR, so its dot goes.
    /// A refresh or your own comment loads the selected PR again; the New tags stay.
    fn put_detail(&mut self, mut d: Detail) {
        let key = d.pr.key();
        for p in self.prs.iter_mut().chain(self.tabs.iter_mut()) {
            if p.key() == key {
                p.unread = false;
            }
        }
        if self.selected.as_ref() == Some(&key) {
            if let Some(shown) = self.details.get(&key).filter(|_| self.looked.as_ref() == Some(&key)) {
                d.keep_new(shown);
            }
            self.looked = Some(key.clone());
        }
        self.details.insert(key, d);
    }

    /// Scrolls to the first comment, review or reply after your last visit (n, or
    /// Jump to first new), in Conversation.
    fn jump_to_new(&mut self, cx: &mut Context<Self>) {
        if !self.selected.as_ref().and_then(|k| self.details.get(k)).is_some_and(|d| d.new_count() > 0) {
            return self.say_done("Nothing new since you last looked", cx);
        }
        self.section = Section::Conversation;
        self.jump_new.set(true);
        cx.notify();
    }

    fn load_folder(&mut self, pr: Pr, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let key = pr.key();
        self.folders.entry(key.clone()).or_insert(FolderState::Loading);
        let res = rpc.call::<Folder>("folder", json!({ "pr": pr, "worktree": false }));
        cx.spawn(async move |this, cx| {
            let res = res.await;
            let _ = this.update(cx, |this, cx| {
                let st = match res {
                    Ok(f) => FolderState::Ready(f),
                    Err(e) if e.code == "no_clone" => FolderState::NoClone,
                    Err(e) => FolderState::Failed(e.message),
                };
                this.folders.insert(key, st);
                cx.notify();
            });
        })
        .detach();
    }

    // ── commands ────────────────────────────────────────────────────────────

    fn find_cmd(&self, f: impl Fn(&Cmd) -> bool) -> Option<Cmd> {
        self.hello.commands.iter().find(|c| f(c)).cloned()
    }

    fn start(&mut self, cmd: Cmd, window: &mut Window, cx: &mut Context<Self>) {
        let Some(pr) = self.selected_pr() else {
            self.banner = Some("Select a pull request first.".into());
            cx.notify();
            return;
        };
        if cmd.reviews() {
            // The review box comments and reviews here, with a preview and line
            // comments; its keys open it rather than asking again in the command bar.
            return self.focus_composer(window, cx);
        }
        if cmd.prompt.is_empty() {
            self.prepare(pr, cmd, String::new(), window, cx);
            return;
        }
        let input = cx.new(|cx| InputState::new(window, cx).placeholder(cmd.prompt.clone()));
        let sub = cx.subscribe_in(&input, window, |this, input, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { .. } = ev {
                let value = input.read(cx).value().to_string();
                if let Some(run) = this.run.take() {
                    this.prepare(run.pr, run.cmd, value, window, cx);
                }
            }
        });
        input.update(cx, |s, cx| s.focus(window, cx));
        self.run = Some(Run { pr, cmd, input: String::new(), stage: Stage::Prompt(input), _sub: Some(sub) });
        cx.notify();
    }

    fn prepare(&mut self, pr: Pr, cmd: Cmd, input: String, window: &mut Window, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let plan = rpc.call::<Plan>("prepare", json!({ "command": cmd.name, "pr": pr, "input": input }));
        self.run = Some(Run { pr: pr.clone(), cmd, input, stage: Stage::Preparing, _sub: None });
        self.focus.focus(window, cx);
        cx.notify();
        cx.spawn_in(window, async move |this, cx| {
            let res = plan.await;
            let _ = this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(plan) if !plan.changes.is_empty() || plan.confirm => {
                        this.set_stage(Stage::Ready { plan, drop_armed: false });
                    }
                    Ok(plan) => this.execute(plan, "", cx),
                    Err(e) if e.code == "no_clone" => this.no_clone(pr, window, cx),
                    Err(e) => this.set_stage(Stage::Failed(e.message)),
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn set_stage(&mut self, stage: Stage) {
        if let Some(run) = self.run.as_mut() {
            run.stage = stage;
        }
    }

    fn execute(&mut self, plan: Plan, changes: &str, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let res = rpc.call::<ExecResult>("execute", json!({ "plan": plan, "changes": changes }));
        let pr = plan.pr.clone();
        let reload = plan.action == "clone" || !plan.pre.is_empty() || plan.action.is_empty();
        let refresh = plan.action == "rerun" || plan.action == "automerge";
        let merge = plan.action == "automerge";
        let silent = plan.silent;
        self.set_stage(Stage::Running(plan));
        cx.notify();
        cx.spawn(async move |this, cx| {
            let res = res.await;
            let _ = this.update(cx, |this, cx| {
                if merge && res.as_ref().is_ok_and(|r| r.ok) {
                    // A PR merged at once leaves the list of open ones.
                    this.load(cx);
                }
                let stage = match res {
                    Ok(r) => {
                        let mut out = r.output.trim().to_string();
                        if !r.ok && !r.error.is_empty() {
                            out = if out.is_empty() { r.error } else { format!("{out}\n{}", r.error) };
                        }
                        if r.ok && silent {
                            out.clear();
                        }
                        Stage::Done { title: r.title, ok: r.ok, output: out }
                    }
                    Err(e) => Stage::Done { title: "Command".into(), ok: false, output: e.message },
                };
                this.set_stage(stage);
                if refresh {
                    // Show the re-run jobs as queued, or auto-merge as on.
                    this.refresh_detail(pr.clone(), cx);
                }
                if reload {
                    this.load_folder(pr, cx);
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn no_clone(&mut self, pr: Pr, window: &mut Window, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let path = cx.new(|cx| InputState::new(window, cx).placeholder(format!("Path to your clone of {}", pr.repo)));
        let sub = cx.subscribe_in(&path, window, |this, input, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { .. } = ev {
                let value = input.read(cx).value().to_string();
                this.set_clone_path(value, window, cx);
            }
        });
        if let Some(run) = self.run.as_mut() {
            run.stage = Stage::NoClone { found: None, path };
            run._sub = Some(sub);
        }
        let found = rpc.call::<Vec<String>>("findClones", json!({ "repo": pr.repo }));
        cx.spawn(async move |this, cx| {
            let found = found.await.unwrap_or_default();
            let _ = this.update(cx, |this, cx| {
                if let Some(Run { stage: Stage::NoClone { found: f, .. }, .. }) = this.run.as_mut() {
                    *f = Some(found);
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn set_clone_path(&mut self, path: String, window: &mut Window, cx: &mut Context<Self>) {
        let (Some(rpc), Some(run)) = (self.rpc.clone(), self.run.as_ref()) else { return };
        let (pr, cmd, input) = (run.pr.clone(), run.cmd.clone(), run.input.clone());
        let res = rpc.call::<SetPath>("setClonePath", json!({ "repo": pr.repo, "path": path }));
        cx.spawn_in(window, async move |this, cx| {
            let res = res.await;
            let _ = this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(r) => {
                        if !r.note.is_empty() {
                            this.banner = Some(r.note);
                        }
                        this.folders.remove(&pr.key());
                        this.load_folder(pr.clone(), cx);
                        this.prepare(pr, cmd, input, window, cx);
                    }
                    Err(e) => this.banner = Some(e.message),
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn clone_repo(&mut self, cx: &mut Context<Self>) {
        let (Some(rpc), Some(run)) = (self.rpc.clone(), self.run.as_ref()) else { return };
        let plan = rpc.call::<Plan>("clonePlan", json!({ "pr": run.pr }));
        cx.spawn(async move |this, cx| {
            let res = plan.await;
            let _ = this.update(cx, |this, cx| {
                match res {
                    Ok(plan) => this.set_stage(Stage::Ready { plan, drop_armed: false }),
                    Err(e) => this.set_stage(Stage::Failed(e.message)),
                }
                cx.notify();
            });
        })
        .detach();
    }

    /// Runs the plan waiting for confirmation, stashing or dropping changes as asked.
    fn confirm(&mut self, changes: &str, cx: &mut Context<Self>) {
        let Some(Run { stage: Stage::Ready { plan, drop_armed }, .. }) = self.run.as_mut() else { return };
        if changes == "drop" && !*drop_armed {
            *drop_armed = true;
            cx.notify();
            return;
        }
        if plan.changes.is_empty() != changes.is_empty() {
            return;
        }
        let plan = plan.clone();
        self.execute(plan, changes, cx);
    }

    fn close_run(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if matches!(self.run.as_ref().map(|r| &r.stage), Some(Stage::Running(_))) {
            return;
        }
        self.run = None;
        self.focus.focus(window, cx);
        cx.notify();
    }

    // ── palette ─────────────────────────────────────────────────────────────

    fn open_palette(&mut self, mode: PaletteMode, window: &mut Window, cx: &mut Context<Self>) {
        let hint = match mode {
            PaletteMode::Filters => "Type a filter, or a qualifier like involves:@me",
            PaletteMode::Prs => "Go to a pull request: number, title, repo or author",
        };
        let input = cx.new(|cx| InputState::new(window, cx).placeholder(hint));
        let sub = cx.subscribe_in(&input, window, |this, _, ev: &InputEvent, window, cx| match ev {
            InputEvent::Change => {
                if let Some(p) = this.palette.as_mut() {
                    p.hi = 0;
                }
                cx.notify();
            }
            InputEvent::PressEnter { .. } => {
                let hi = this.palette.as_ref().map(|p| p.hi).unwrap_or(0);
                if let Some(item) = this.palette_items(cx).into_iter().nth(hi) {
                    this.apply(item.apply, window, cx);
                }
            }
            _ => {}
        });
        input.update(cx, |s, cx| s.focus(window, cx));
        self.palette = Some(Palette { input, hi: 0, mode, _sub: sub });
        cx.notify();
    }

    /// Opens the palette in `mode`, or closes it when it is already showing that.
    fn toggle_palette(&mut self, mode: PaletteMode, window: &mut Window, cx: &mut Context<Self>) {
        if self.palette.as_ref().is_some_and(|p| p.mode == mode) {
            self.close_palette(window, cx);
        } else {
            self.open_palette(mode, window, cx);
        }
    }

    fn close_palette(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.palette = None;
        self.focus.focus(window, cx);
        cx.notify();
    }

    fn palette_items(&self, cx: &App) -> Vec<PaletteItem> {
        let Some(p) = self.palette.as_ref() else { return vec![] };
        let q = p.input.read(cx).value().to_string();
        let needle = q.trim().to_lowercase();
        if p.mode == PaletteMode::Prs {
            let mut seen = HashSet::new();
            let mut scored: Vec<(usize, PaletteItem)> = self
                .prs
                .iter()
                .chain(self.tabs.iter())
                .filter(|pr| seen.insert(pr.key()))
                .filter_map(|pr| {
                    let hay = format!("#{} {} {} {}", pr.number, pr.title, pr.repo, pr.author).to_lowercase();
                    let score = fuzzy_score(&needle, &hay)?;
                    Some((
                        score,
                        PaletteItem {
                            group: "Pull requests",
                            label: format!("#{}  {}", pr.number, pr.title),
                            qual: pr.repo.clone(),
                            apply: Apply::Pr(pr.clone()),
                            active: self.selected.as_ref() == Some(&pr.key()),
                        },
                    ))
                })
                .collect();
            scored.sort_by_key(|(s, _)| *s);
            return scored.into_iter().map(|(_, it)| it).take(80).collect();
        }
        let mut items: Vec<PaletteItem> = self
            .hello
            .filters
            .iter()
            .enumerate()
            .filter(|(_, f)| {
                needle.is_empty() || f.name.to_lowercase().contains(&needle) || f.query.to_lowercase().contains(&needle)
            })
            .map(|(i, f)| PaletteItem {
                group: "Filters",
                label: f.name.clone(),
                qual: f.query.clone(),
                apply: Apply::Filter(i),
                active: self.custom.is_none() && i == self.filter_ix,
            })
            .collect();
        if needle.contains(':') {
            items.push(PaletteItem {
                group: "Search GitHub",
                label: format!("Use “{}”", q.trim()),
                qual: "custom query".into(),
                apply: Apply::Query(q.trim().to_string()),
                active: false,
            });
        }
        items
    }

    fn apply(&mut self, apply: Apply, window: &mut Window, cx: &mut Context<Self>) {
        match apply {
            Apply::Filter(i) => {
                self.filter_ix = i;
                self.custom = None;
            }
            Apply::Query(q) => self.custom = Some(q),
            Apply::Pr(pr) => {
                self.close_palette(window, cx);
                self.select(pr, cx);
                return;
            }
        }
        self.repo = None;
        self.close_palette(window, cx);
        self.load(cx);
    }

    // ── shortcuts ───────────────────────────────────────────────────────────

    fn on_go_to_pr(&mut self, _: &GoToPr, window: &mut Window, cx: &mut Context<Self>) {
        self.toggle_palette(PaletteMode::Prs, window, cx);
    }

    fn on_filters(&mut self, _: &Filters, window: &mut Window, cx: &mut Context<Self>) {
        self.toggle_palette(PaletteMode::Filters, window, cx);
    }

    /// ⌘W closes the PR tab; with no tabs left it closes the window, like any Mac app.
    fn on_close_tab(&mut self, _: &CloseTab, window: &mut Window, cx: &mut Context<Self>) {
        if self.palette.is_some() {
            return self.close_palette(window, cx);
        }
        match self.selected.clone() {
            Some(key) if self.tabs.iter().any(|t| t.key() == key) => self.close_tab(&key, cx),
            _ => window.remove_window(),
        }
    }

    fn on_reopen_tab(&mut self, _: &ReopenTab, _: &mut Window, cx: &mut Context<Self>) {
        if let Some(pr) = self.closed.pop() {
            self.select(pr, cx);
        }
    }

    fn on_refresh(&mut self, _: &Refresh, _: &mut Window, cx: &mut Context<Self>) {
        self.load(cx);
        if let Some(pr) = self.selected_pr() {
            self.details.remove(&pr.key());
            self.load_detail(pr.clone(), cx);
            self.load_folder(pr, cx);
        }
    }

    fn step_tab(&mut self, delta: isize, cx: &mut Context<Self>) {
        if self.tabs.is_empty() {
            return;
        }
        let n = self.tabs.len() as isize;
        let cur = self.selected.as_ref().and_then(|k| self.tabs.iter().position(|t| &t.key() == k)).unwrap_or(0) as isize;
        let next = self.tabs[((cur + delta) % n + n) as usize % n as usize].clone();
        self.select(next, cx);
    }

    fn on_next_tab(&mut self, _: &NextTab, _: &mut Window, cx: &mut Context<Self>) {
        self.step_tab(1, cx);
    }

    fn on_prev_tab(&mut self, _: &PrevTab, _: &mut Window, cx: &mut Context<Self>) {
        self.step_tab(-1, cx);
    }

    /// ⌘1–⌘8 pick that tab, ⌘9 the last one, as in browsers and Zed.
    fn activate_tab(&mut self, n: usize, cx: &mut Context<Self>) {
        let pick = if n == 9 { self.tabs.last() } else { self.tabs.get(n.saturating_sub(1)) };
        if let Some(pr) = pick.cloned() {
            self.select(pr, cx);
        }
    }

    fn on_open_config(&mut self, _: &OpenConfig, _: &mut Window, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let res = rpc.call::<serde_json::Value>("openConfig", json!({}));
        cx.spawn(async move |this, cx| {
            if let Err(e) = res.await {
                let _ = this.update(cx, |this, cx| {
                    this.banner = Some(format!("Opening the config: {}", e.message));
                    cx.notify();
                });
            }
        })
        .detach();
    }

    fn on_open_in_editor(&mut self, _: &OpenInEditor, window: &mut Window, cx: &mut Context<Self>) {
        if let Some(c) = self.find_cmd(|c| c.action == "open" && !c.worktree) {
            self.start(c, window, cx);
        }
    }

    fn on_open_on_github(&mut self, _: &OpenOnGitHub, _: &mut Window, cx: &mut Context<Self>) {
        if let Some(pr) = self.selected_pr() {
            cx.open_url(&pr.url);
        }
    }

    fn on_comment(&mut self, _: &CommentOnPr, window: &mut Window, cx: &mut Context<Self>) {
        self.focus_composer(window, cx);
    }

    fn on_run_setup(&mut self, _: &RunSetup, window: &mut Window, cx: &mut Context<Self>) {
        self.open_setup(window, cx);
    }

    fn on_toggle_sidebar(&mut self, _: &ToggleSidebar, _: &mut Window, cx: &mut Context<Self>) {
        self.layout.sidebar_hidden = !self.layout.sidebar_hidden;
        self.layout.save();
        cx.notify();
    }

    fn palette_key(&mut self, ev: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        let n = self.palette_items(cx).len();
        let Some(p) = self.palette.as_mut() else { return };
        match ev.keystroke.key.as_str() {
            "escape" => {
                self.close_palette(window, cx);
                cx.stop_propagation();
            }
            "down" => {
                p.hi = if n == 0 { 0 } else { (p.hi + 1) % n };
                cx.stop_propagation();
                cx.notify();
            }
            "up" => {
                p.hi = if n == 0 { 0 } else { (p.hi + n - 1) % n };
                cx.stop_propagation();
                cx.notify();
            }
            _ => {}
        }
    }

    // ── keys ────────────────────────────────────────────────────────────────

    fn typing(&self, window: &Window, cx: &App) -> bool {
        let focused = |s: &Entity<InputState>| s.read(cx).focus_handle(cx).is_focused(window);
        if self.setup.as_ref().is_some_and(|s| focused(s.add_input())) || self.typing_review(window, cx) {
            return true;
        }
        match self.run.as_ref().map(|r| &r.stage) {
            Some(Stage::Prompt(s)) => focused(s),
            Some(Stage::NoClone { path, .. }) => focused(path),
            _ => false,
        }
    }

    fn on_key(&mut self, ev: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        let ks = &ev.keystroke;
        let m = &ks.modifiers;
        if self.palette.is_some() {
            return;
        }
        if self.setup.is_some() {
            if ks.key == "enter" && !self.typing(window, cx) && !m.modified() {
                self.setup_enter(window, cx);
            }
            return;
        }
        if m.platform && !m.shift && !m.alt && !m.control {
            if let Ok(n @ 1..=9) = ks.key.parse::<usize>() {
                self.activate_tab(n, cx);
            }
            return;
        }
        if self.typing(window, cx) {
            if ks.key == "escape" {
                if self.line_editor.is_some() {
                    self.close_line_editor(window, cx);
                } else {
                    self.close_run(window, cx);
                }
            }
            return;
        }
        if m.control || m.platform || m.alt || m.function {
            return;
        }
        match ks.key.as_str() {
            "escape" => {
                if self.run.is_none() && self.commit.is_some() && self.section == Section::Commits {
                    return self.open_commit(None, cx);
                }
                return self.close_run(window, cx);
            }
            "enter" => return self.confirm("", cx),
            "down" => return self.move_selection(1, cx),
            "up" => return self.move_selection(-1, cx),
            _ => {}
        }
        let ch = ks.key_char.clone().filter(|c| !c.is_empty()).unwrap_or_else(|| ks.key.clone());
        // Waiting on uncommitted changes: s stashes, d drops (press twice).
        if let Some(Run { stage: Stage::Ready { plan, .. }, .. }) = self.run.as_ref() {
            if !plan.changes.is_empty() {
                match ch.as_str() {
                    "s" => return self.confirm("stash", cx),
                    "d" => return self.confirm("drop", cx),
                    _ => {}
                }
            }
        }
        match ch.as_str() {
            "j" => return self.move_selection(1, cx),
            "k" => return self.move_selection(-1, cx),
            "R" => return self.load(cx),
            "/" => return self.open_palette(PaletteMode::Filters, window, cx),
            d if d.len() == 1 && ('1'..='9').contains(&d.chars().next().unwrap()) => {
                let i = d.parse::<usize>().unwrap() - 1;
                if i < self.hello.filters.len() {
                    self.apply(Apply::Filter(i), window, cx);
                }
                return;
            }
            _ => {}
        }
        if let Some(cmd) = self.find_cmd(|c| !c.key.is_empty() && c.key == ch) {
            self.start(cmd, window, cx);
        } else if ch == "n" {
            // After the commands, so one of yours on n still wins.
            self.jump_to_new(cx);
        }
    }
}

// ── view ────────────────────────────────────────────────────────────────────

fn state_color(p: &Pr) -> u32 {
    if p.is_draft {
        DIM
    } else if p.conflict() {
        YELLOW
    } else {
        GREEN
    }
}

fn review(p: &Pr) -> Option<(&'static str, u32)> {
    if p.conflict() {
        return Some(("conflict", YELLOW));
    }
    match p.review_decision.as_str() {
        "APPROVED" => Some(("approved", GREEN)),
        "CHANGES_REQUESTED" => Some(("changes requested", RED)),
        "REVIEW_REQUIRED" => Some(("review required", SOFT)),
        _ => None,
    }
}

fn ci(state: &str) -> Option<(&'static str, u32, &'static str)> {
    match state {
        "SUCCESS" => Some(("✓", GREEN, "Checks passed")),
        "FAILURE" | "ERROR" => Some(("✕", RED, "Checks failed")),
        "PENDING" | "EXPECTED" => Some(("●", YELLOW, "Checks running")),
        _ => None,
    }
}

fn code_chip(text: impl Into<SharedString>, color: u32) -> Div {
    div()
        .px(px(6.))
        .bg(rgb(BG_HOVER))
        .border_1()
        .border_color(rgb(BORDER_2))
        .text_color(rgb(color))
        .text_size(px(11.5))
        .font_family(MONO)
        .child(text.into())
}

fn card() -> Div {
    div().flex().flex_col().border_1().border_color(rgb(BORDER_2)).bg(rgb(BG_SIDE))
}

fn card_head() -> Div {
    div()
        .h(px(36.))
        .px(px(12.))
        .flex()
        .items_center()
        .gap(px(8.))
        .bg(rgb(BG_HEAD))
        .border_b_1()
        .border_color(rgb(BORDER_2))
        .text_size(px(12.))
        .text_color(rgb(SOFT))
}

fn md_body(id: String, body: &str, images: &Images) -> AnyElement {
    if body.trim().is_empty() {
        return div().italic().text_color(rgb(DIM)).child("No description provided.").into_any_element();
    }
    markdown::view(id, body, images).into_any_element()
}

impl Deck {
    fn header(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let synced = if self.loading {
            "Syncing…".to_string()
        } else {
            match self.synced {
                Some(t) => format!("Synced {} ago", ago_secs(t.elapsed().as_secs() as i64).replace("now", "just now"))
                    .replace("just now ago", "just now"),
                None => String::new(),
            }
        };
        let stop = |_: &MouseDownEvent, _: &mut Window, cx: &mut App| cx.stop_propagation();
        div()
            .id("header")
            .h(px(38.))
            .flex_none()
            .flex()
            .items_center()
            .gap(px(10.))
            .pl(px(84.))
            .pr(px(10.))
            .bg(rgb(BG_DEEP))
            .border_b_1()
            .border_color(rgb(BORDER))
            .on_mouse_down(MouseButton::Left, |ev, window, _| {
                if ev.click_count == 2 {
                    window.titlebar_double_click();
                } else {
                    window.start_window_move();
                }
            })
            .child(div().text_size(px(12.)).text_color(rgb(SOFT)).font_family(MONO).child("prdeck"))
            .child(
                div().flex_1().flex().justify_center().child(
                    div()
                        .id("query")
                        .w_full()
                        .max_w(px(520.))
                        .h(px(26.))
                        .px(px(8.))
                        .flex()
                        .items_center()
                        .gap(px(8.))
                        .bg(rgb(BG_INPUT))
                        .border_1()
                        .border_color(rgb(BORDER_2))
                        .cursor_pointer()
                        .hover(|s| s.border_color(rgb(BORDER_3)))
                        .on_mouse_down(MouseButton::Left, stop)
                        .on_click(cx.listener(|this, _, window, cx| this.open_palette(PaletteMode::Filters, window, cx)))
                        .child(icon("search", 13., DIM))
                        .child(
                            div()
                                .flex_1()
                                .min_w(px(0.))
                                .truncate()
                                .font_family(MONO)
                                .text_size(px(12.))
                                .text_color(rgb(TEXT_SOFT))
                                .child(self.query()),
                        )
                        .child(kbd("⌘K")),
                ),
            )
            .child(div().text_size(px(12.)).text_color(rgb(DIM)).child(synced))
            .child(
                div()
                    .id("refresh")
                    .size(px(28.))
                    .flex()
                    .items_center()
                    .justify_center()
                    .cursor_pointer()
                    .hover(|s| s.bg(rgb(BG_HOVER)))
                    .on_mouse_down(MouseButton::Left, stop)
                    .on_click(cx.listener(|this, _, _, cx| this.load(cx)))
                    .child(icon("refresh-cw", 14., SOFT)),
            )
            .when(!self.hello.viewer.is_empty(), |d| d.child(self.images.face(&self.hello.viewer, 24.)))
    }

    fn sidebar(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let repos = self.repos();
        let visible = self.visible();
        let mut repo_rows: Vec<AnyElement> = vec![];
        let all = std::iter::once((None, "All repositories".to_string(), self.prs.len()))
            .chain(repos.iter().map(|(r, n)| (Some(r.clone()), r.clone(), *n)));
        for (i, (id, label, n)) in all.enumerate() {
            let selected = self.repo == id;
            let (owner, name) = match label.split_once('/') {
                Some((o, n)) if id.is_some() => (format!("{o}/"), n.to_string()),
                _ => (String::new(), label.clone()),
            };
            repo_rows.push(
                div()
                    .id(("repo", i))
                    .h(px(30.))
                    .flex_none()
                    .px(px(12.))
                    .flex()
                    .items_center()
                    .gap(px(8.))
                    .cursor_pointer()
                    .when(selected, |d| d.bg(rgb(BG_SEL)))
                    .hover(|s| s.bg(rgb(BG_HOVER)))
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.repo = id.clone();
                        cx.notify();
                    }))
                    .child(icon("folder-git-2", 14., if selected { BLUE } else { DIM }))
                    .child(
                        div()
                            .flex_1()
                            .min_w(px(0.))
                            .truncate()
                            .child(div().flex().child(div().text_color(rgb(DIM)).child(owner)).child(name)),
                    )
                    .child(
                        div().text_size(px(11.)).text_color(rgb(if selected { BLUE } else { DIM })).child(n.to_string()),
                    )
                    .into_any_element(),
            );
        }

        let chips: Vec<AnyElement> = self
            .hello
            .filters
            .iter()
            .enumerate()
            .map(|(i, f)| {
                let active = self.custom.is_none() && i == self.filter_ix;
                div()
                    .id(("chip", i))
                    .h(px(24.))
                    .px(px(8.))
                    .flex()
                    .items_center()
                    .text_size(px(12.))
                    .border_1()
                    .border_color(rgb(if active { RING_ACTIVE } else { BORDER_2 }))
                    .when(active, |d| d.bg(rgb(BG_ACTIVE)))
                    .text_color(rgb(if active { TEXT } else { SOFT }))
                    .cursor_pointer()
                    .hover(|s| s.border_color(rgb(BORDER_POP)))
                    .on_click(cx.listener(move |this, _, window, cx| this.apply(Apply::Filter(i), window, cx)))
                    .child(f.name.clone())
                    .into_any_element()
            })
            .collect();

        let rows: Vec<AnyElement> = visible.iter().enumerate().map(|(i, p)| self.pr_row(i, p, cx)).collect();
        let empty = rows.is_empty();

        let repos = div()
            .size_full()
            .flex()
            .flex_col()
            .child(section_head("Repositories", None))
            .child(div().id("repos").flex_1().min_h(px(0.)).py(px(4.)).overflow_y_scroll().children(repo_rows));
        let deck = cx.entity().downgrade();
        div()
            .size_full()
            .flex()
            .flex_col()
            .bg(rgb(BG_SIDE))
            .border_r_1()
            .border_color(rgb(BORDER))
            .child(
                v_resizable("sidebar-split")
                    .on_resize(move |state, _, cx| {
                        if let Some(h) = state.read(cx).sizes().first().copied() {
                            let _ = deck.update(cx, |d, _| {
                                d.layout.repos = f32::from(h);
                                d.layout.save();
                            });
                        }
                    })
                    .child(resizable_panel().size(px(self.layout.repos)).size_range(px(64.)..px(1200.)).child(repos))
                    .child(resizable_panel().child(
                div()
                    .size_full()
                    .flex()
                    .flex_col()
                    .child(
                        section_head("Pull requests", Some(visible.len())).child(
                            div()
                                .id("scope")
                                .h(px(24.))
                                .px(px(6.))
                                .flex()
                                .items_center()
                                .gap(px(4.))
                                .text_size(px(12.))
                                .text_color(rgb(TEXT_SOFT))
                                .cursor_pointer()
                                .hover(|s| s.bg(rgb(BG_HOVER)))
                                .on_click(cx.listener(|this, _, window, cx| this.open_palette(PaletteMode::Filters, window, cx)))
                                .child(div().max_w(px(150.)).truncate().child(self.query_label()))
                                .child(icon("chevron-down", 12., SOFT)),
                        ),
                    )
                    .child(
                        div()
                            .flex_none()
                            .flex()
                            .flex_wrap()
                            .gap(px(6.))
                            .px(px(10.))
                            .py(px(8.))
                            .border_b_1()
                            .border_color(rgb(BORDER))
                            .children(chips),
                    )
                    .child(
                        div()
                            .id("prs")
                            .flex_1()
                            .min_h(px(0.))
                            .py(px(4.))
                            .overflow_y_scroll()
                            .children(rows)
                            .when(empty, |d| {
                                d.child(
                                    div().p(px(16.)).text_size(px(12.)).text_color(rgb(DIM)).child(if self.loading {
                                        "Loading pull requests…"
                                    } else {
                                        "No open pull requests match this filter."
                                    }),
                                )
                            }),
                    ),
                    )),
            )
    }

    fn pr_row(&self, i: usize, p: &Pr, cx: &mut Context<Self>) -> AnyElement {
        let selected = self.selected.as_ref() == Some(&p.key());
        let pr = p.clone();
        div()
            .id(("pr", i))
            .relative()
            .flex_none()
            .px(px(12.))
            .py(px(8.))
            .flex()
            .gap(px(8.))
            .border_l_2()
            .border_color(rgb(if selected { BLUE } else { BG_SIDE }))
            .when(selected, |d| d.bg(rgb(BG_SEL)))
            .cursor_pointer()
            .hover(|s| s.bg(rgb(BG_HOVER)))
            .on_click(cx.listener(move |this, _, _, cx| this.select(pr.clone(), cx)))
            // Unread: a blue dot in the left margin, as in Mail.
            .when(p.unread, |d| d.child(div().absolute().left(px(2.)).top(px(13.)).size(px(6.)).rounded(px(3.)).bg(rgb(BLUE))))
            .child(
                div()
                    .pt(px(2.))
                    .child(icon(if p.is_draft { "git-pull-request-draft" } else { "git-pull-request" }, 14., state_color(p))),
            )
            .child(
                div()
                    .flex_1()
                    .min_w(px(0.))
                    .flex()
                    .flex_col()
                    .gap(px(3.))
                    .child(
                        div()
                            .flex()
                            .gap(px(8.))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w(px(0.))
                                    .truncate()
                                    .text_color(rgb(if p.unread { TEXT_BRIGHT } else { TEXT }))
                                    .when(p.unread, |d| d.font_weight(FontWeight::SEMIBOLD))
                                    .child(p.title.clone()),
                            )
                            .child(div().flex_none().text_size(px(11.)).text_color(rgb(if p.unread { BLUE } else { DIM })).child(ago(&p.updated_at))),
                    )
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap(px(8.))
                            .text_size(px(11.5))
                            .text_color(rgb(DIM))
                            .child(div().min_w(px(0.)).truncate().child(format!("#{} · {}", p.number, p.name())))
                            .children(review(p).map(|(t, c)| div().flex_none().text_size(px(11.)).text_color(rgb(c)).child(t)))
                            .children(ci(&p.check_state).map(|(g, c, _)| div().flex_none().text_color(rgb(c)).child(g)))
                            .when(p.comments > 0, |d| {
                                d.child(
                                    div()
                                        .flex_none()
                                        .flex()
                                        .items_center()
                                        .gap(px(3.))
                                        .child(icon("message-square", 11., DIM))
                                        .child(p.comments.to_string()),
                                )
                            }),
                    ),
            )
            .into_any_element()
    }

    fn tabs(&self, cx: &mut Context<Self>) -> impl IntoElement {
        div()
            .id("tabs")
            .h(px(34.))
            .flex_none()
            .flex()
            .bg(rgb(BG_DEEP))
            .border_b_1()
            .border_color(rgb(BORDER))
            .overflow_x_scroll()
            .children(self.tabs.iter().enumerate().map(|(i, t)| {
                let active = self.selected.as_ref() == Some(&t.key());
                let (pr, key) = (t.clone(), t.key());
                div()
                    .id(("tab", i))
                    .flex_none()
                    .flex()
                    .items_center()
                    .gap(px(6.))
                    .pl(px(12.))
                    .pr(px(4.))
                    .max_w(px(280.))
                    .border_r_1()
                    .border_color(rgb(BORDER))
                    .relative()
                    .when(active, |d| {
                        d.bg(rgb(BG)).child(div().absolute().top_0().left_0().right_0().h(px(2.)).bg(rgb(BLUE)))
                    })
                    .text_color(rgb(if active { TEXT } else { SOFT }))
                    .cursor_pointer()
                    .on_click(cx.listener(move |this, _, _, cx| this.select(pr.clone(), cx)))
                    .child(icon(if t.is_draft { "git-pull-request-draft" } else { "git-pull-request" }, 13., state_color(t)))
                    .child(div().text_size(px(11.5)).text_color(rgb(DIM)).child(format!("#{}", t.number)))
                    .child(div().min_w(px(0.)).truncate().text_size(px(12.)).child(t.title.clone()))
                    .child(
                        div()
                            .id(("close", i))
                            .size(px(22.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .hover(|s| s.bg(rgb(BG_HOVER)))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                cx.stop_propagation();
                                this.close_tab(&key, cx);
                            }))
                            .child(icon("close", 12., DIM)),
                    )
            }))
    }

    /// Sidebar and PR, with a handle between them to drag.
    fn body(&self, cx: &mut Context<Self>) -> AnyElement {
        if self.layout.sidebar_hidden {
            return div().flex_1().min_h(px(0.)).flex().child(self.main_area(cx)).into_any_element();
        }
        let deck = cx.entity().downgrade();
        div()
            .flex_1()
            .min_h(px(0.))
            .child(
                h_resizable("body-split")
                    .on_resize(move |state, _, cx| {
                        if let Some(w) = state.read(cx).sizes().first().copied() {
                            let _ = deck.update(cx, |d, _| {
                                d.layout.sidebar = f32::from(w);
                                d.layout.save();
                            });
                        }
                    })
                    .child(resizable_panel().size(px(self.layout.sidebar)).size_range(px(220.)..px(900.)).child(self.sidebar(cx)))
                    .child(resizable_panel().child(self.main_area(cx))),
            )
            .into_any_element()
    }

    fn main_area(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let body = match self.selected_pr() {
            None => div()
                .flex_1()
                .flex()
                .items_center()
                .justify_center()
                .text_color(rgb(DIM))
                .child("Select a pull request from the list.")
                .into_any_element(),
            Some(pr) => self.pr_view(pr, cx).into_any_element(),
        };
        div().size_full().min_w(px(0.)).flex().flex_col().bg(rgb(BG)).child(self.tabs(cx)).child(body)
    }

    fn pr_view(&self, pr: Pr, cx: &mut Context<Self>) -> impl IntoElement {
        let key = pr.key();
        let detail = self.details.get(&key);
        let editor = if self.hello.editor.is_empty() { "editor".to_string() } else { self.hello.editor.clone() };
        let commits = detail.map(|d| d.commits).unwrap_or(0);
        // Merged from here (Merge when ready on a ready PR): the PR as loaded again says so.
        let merged = detail.is_some_and(|d| d.pr.state == "MERGED");
        let (state_label, state_bg, state_icon, state_fg) = if merged {
            ("Merged", 0x241f30, "git-merge", PURPLE)
        } else if pr.is_draft {
            ("Draft", BG_HOVER, "git-pull-request-draft", state_color(&pr))
        } else if pr.conflict() {
            ("Conflict", 0x2a2418, "git-pull-request", state_color(&pr))
        } else {
            ("Open", 0x1c2a1f, "git-pull-request", state_color(&pr))
        };

        let header = div()
            .flex_none()
            .px(px(20.))
            .pt(px(16.))
            .border_b_1()
            .border_color(rgb(BORDER))
            .flex()
            .flex_col()
            .gap(px(10.))
            .child(
                div()
                    .flex()
                    .items_start()
                    .gap(px(16.))
                    .child(
                        div()
                            .flex_1()
                            .min_w(px(0.))
                            .flex()
                            .flex_col()
                            .gap(px(6.))
                            .child(
                                div().text_size(px(12.)).text_color(rgb(DIM)).child(format!("{} · #{}", pr.repo, pr.number)),
                            )
                            .child(
                                div()
                                    .text_size(px(20.))
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(rgb(TEXT_BRIGHT))
                                    .child(pr.title.clone()),
                            )
                            .child(
                                div()
                                    .flex()
                                    .flex_wrap()
                                    .items_center()
                                    .gap(px(8.))
                                    .text_size(px(12.))
                                    .text_color(rgb(SOFT))
                                    .child(
                                        div()
                                            .h(px(22.))
                                            .px(px(8.))
                                            .flex()
                                            .items_center()
                                            .gap(px(5.))
                                            .bg(rgb(state_bg))
                                            .border_1()
                                            .border_color(rgb(state_fg))
                                            .text_color(rgb(state_fg))
                                            .child(icon(state_icon, 12., state_fg))
                                            .child(state_label),
                                    )
                                    .child(
                                        div()
                                            .flex()
                                            .gap(px(4.))
                                            .child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT)).child(pr.author.clone()))
                                            .child(match (merged, commits) {
                                                (true, _) => "merged into".to_string(),
                                                (false, 0) => "wants to merge into".to_string(),
                                                (false, n) => format!("wants to merge {n} commit{} into", if n == 1 { "" } else { "s" }),
                                            }),
                                    )
                                    .child(code_chip(pr.base.clone(), TEXT_SOFT))
                                    .child("from")
                                    .child(code_chip(pr.head.clone(), BLUE_LIGHT)),
                            ),
                    )
                    .child(
                        div()
                            .flex_none()
                            .flex()
                            .gap(px(8.))
                            .child(
                                primary_look("open")
                                    .on_click(cx.listener(|this, _, window, cx| {
                                        if let Some(c) = this.find_cmd(|c| c.action == "open" && !c.worktree) {
                                            this.start(c, window, cx);
                                        }
                                    }))
                                    .child(format!("Open in {editor}")),
                            )
                            .when(self.find_cmd(|c| c.action == "open" && c.worktree).is_some(), |d| {
                                d.child(
                                    button_look("worktree")
                                        .on_click(cx.listener(|this, _, window, cx| {
                                            if let Some(c) = this.find_cmd(|c| c.action == "open" && c.worktree) {
                                                this.start(c, window, cx);
                                            }
                                        }))
                                        .child(icon("folder-git-2", 13., SOFT))
                                        .child("Worktree"),
                                )
                            })
                            .child(
                                button_look("review")
                                    .on_click(cx.listener(|this, _, window, cx| this.focus_composer(window, cx)))
                                    .child(icon("message-square", 13., SOFT))
                                    .child(match self.pending_count(&pr.key()) {
                                        0 => "Review".to_string(),
                                        n => format!("Review ({n})"),
                                    }),
                            )
                            .when_some(self.find_cmd(|c| c.branch && c.run.is_empty() && c.action.is_empty()), |d, c| {
                                d.child(
                                    button_look("checkout")
                                        .on_click(cx.listener(move |this, _, window, cx| this.start(c.clone(), window, cx)))
                                        .child(icon("git-branch", 13., SOFT))
                                        .child("Checkout"),
                                )
                            })
                            .when_some(self.find_cmd(|c| c.action == "browse"), |d, c| {
                                d.child(
                                    button_look("github")
                                        .on_click(cx.listener(move |this, _, window, cx| this.start(c.clone(), window, cx)))
                                        .child("GitHub")
                                        .child(icon("external-link", 12., SOFT)),
                                )
                            }),
                    ),
            )
            .child(self.section_tabs(detail, cx));

        let content = match (detail, self.detail_err.get(&key)) {
            (Some(d), _) => {
                let deck = cx.entity().downgrade();
                h_resizable("detail-split")
                    .on_resize(move |state, _, cx| {
                        if let Some(w) = state.read(cx).sizes().get(1).copied() {
                            let _ = deck.update(cx, |d, _| {
                                d.layout.aside = f32::from(w);
                                d.layout.save();
                            });
                        }
                    })
                    .child(
                        resizable_panel().child(
                            div()
                                .id(SharedString::from(format!("scroll-{key}")))
                                .size_full()
                                .overflow_y_scroll()
                                .track_scroll(&self.detail_scroll)
                                .child(
                                    div()
                                        .px(px(20.))
                                        .pt(px(20.))
                                        .pb(px(64.))
                                        .flex()
                                        .flex_col()
                                        .gap(px(16.))
                                        .child(self.section_body(d, cx)),
                                ),
                        ),
                    )
                    .child(
                        resizable_panel().size(px(self.layout.aside)).size_range(px(200.)..px(560.)).child(
                            div()
                                .id(SharedString::from(format!("aside-{key}")))
                                .size_full()
                                .overflow_y_scroll()
                                .pl(px(4.))
                                .pr(px(20.))
                                .pt(px(20.))
                                .pb(px(64.))
                                .child(self.aside(d, cx)),
                        ),
                    )
                    .into_any_element()
            }
            (None, Some(err)) => div().p(px(20.)).text_color(rgb(RED)).child(err.clone()).into_any_element(),
            (None, None) => div().p(px(20.)).text_color(rgb(DIM)).child("Loading…").into_any_element(),
        };

        div()
            .flex_1()
            .min_h(px(0.))
            .flex()
            .flex_col()
            .child(header)
            .child(div().flex_1().min_h(px(0.)).child(content))
    }

    fn section_tabs(&self, d: Option<&Detail>, cx: &mut Context<Self>) -> impl IntoElement {
        let items = [
            (Section::Conversation, "Conversation", d.map(|d| d.timeline.len() as u64)),
            (Section::Checks, "Checks", d.map(|d| d.checks.len() as u64)),
            (Section::Commits, "Commits", d.map(|d| d.commits)),
            (Section::Files, "Files changed", d.map(|d| d.changed_files)),
        ];
        div().flex().children(items.into_iter().enumerate().map(|(i, (s, label, n))| {
            let active = self.section == s;
            div()
                .id(("section", i))
                .h(px(34.))
                .px(px(12.))
                .flex()
                .items_center()
                .gap(px(6.))
                .border_b_2()
                .border_color(rgb(if active { BLUE } else { BG }))
                .text_color(rgb(if active { TEXT } else { SOFT }))
                .cursor_pointer()
                .hover(|st| st.text_color(rgb(TEXT)))
                .on_click(cx.listener(move |this, _, _, cx| {
                    if this.section == s && s == Section::Commits {
                        this.commit = None;
                    }
                    this.section = s;
                    this.load_section(cx);
                    cx.notify();
                }))
                .child(label)
                .children(n.map(|n| {
                    div().px(px(5.)).bg(rgb(BG_BADGE)).text_size(px(11.)).text_color(rgb(SOFT)).child(n.to_string())
                }))
        }))
    }

    fn section_body(&self, d: &Detail, cx: &mut Context<Self>) -> AnyElement {
        match self.section {
            Section::Conversation => self.conversation(d, cx).into_any_element(),
            Section::Checks => checks_view(d, self.rerun_button("rerun-checks", d, cx)).into_any_element(),
            Section::Commits => self.commits_view(d, cx),
            Section::Files => self.files_view(d, cx),
        }
    }

    /// The Re-run failed button, when checks failed and the command is set up. The
    /// PR list's rollup must say failed too, since that is what the command checks.
    fn rerun_button(&self, id: &'static str, d: &Detail, cx: &mut Context<Self>) -> Option<Stateful<Div>> {
        let cmd = self.find_cmd(|c| c.action == "rerun")?;
        let ci_failed = self.selected_pr().is_some_and(|p| p.check_state == "FAILURE" || p.check_state == "ERROR");
        if d.check_counts().1 == 0 || !ci_failed {
            return None;
        }
        let (name, key) = (cmd.name.clone(), cmd.key.clone());
        Some(
            button_look(id)
                .on_click(cx.listener(move |this, _, window, cx| this.start(cmd.clone(), window, cx)))
                .child(icon("refresh-cw", 12., SOFT))
                .child(name)
                .when(!key.is_empty(), |b| b.child(kbd(key))),
        )
    }

    fn handoff(&self, what: String, cx: &mut Context<Self>) -> AnyElement {
        let editor = self.hello.editor.clone();
        card()
            .border_dashed()
            .border_color(rgb(BORDER_3))
            .bg(rgb(BG))
            .px(px(24.))
            .py(px(28.))
            .gap(px(10.))
            .items_start()
            .child(div().text_size(px(15.)).text_color(rgb(TEXT_BRIGHT)).child(format!("Review {what} in your editor")))
            .child(div().text_color(rgb(SOFT)).child(format!(
                "Open the branch in {editor} to read the changes with your own tools."
            )))
            .child(
                primary_look("handoff")
                    .on_click(cx.listener(|this, _, window, cx| {
                        if let Some(c) = this.find_cmd(|c| c.action == "open" && !c.worktree) {
                            this.start(c, window, cx);
                        }
                    }))
                    .child(format!("Open in {editor}")),
            )
            .into_any_element()
    }

    fn conversation(&self, d: &Detail, cx: &mut Context<Self>) -> impl IntoElement {
        let key = d.pr.key();
        let mut items: Vec<AnyElement> = vec![
            card()
                .child(
                    card_head()
                        .child(self.images.face(&d.pr.author, 20.))
                        .child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT)).child(d.pr.author.clone()))
                        .child(format!("opened this {} ago", ago(&d.pr.created_at)))
                        .child(div().px(px(6.)).border_1().border_color(rgb(BORDER_3)).text_size(px(11.)).child("Author")),
                )
                .child(
                    div()
                        .px(px(18.))
                        .pt(px(8.))
                        .pb(px(16.))
                        .text_size(px(14.))
                        .text_color(rgb(TEXT_BODY))
                        .child(md_body(format!("body-{key}"), &d.body, &self.images)),
                )
                .into_any_element(),
        ];
        let first_new = d.timeline.iter().position(|e| e.has_new());
        for (i, e) in d.timeline.iter().enumerate() {
            let reply = self.thread_reply_view(&key, e, cx);
            let ev = event_view(&key, i, e, &self.images, reply);
            items.push(if first_new == Some(i) { self.jump_target(ev) } else { ev });
        }
        if first_new.is_some() {
            items.insert(0, self.new_bar(d.new_count(), cx));
        }
        items.push(self.composer(d, cx));
        div().flex().flex_col().gap(px(14.)).children(items)
    }

    /// "2 new since you last looked", with a button to the first of them.
    fn new_bar(&self, n: usize, cx: &mut Context<Self>) -> AnyElement {
        div()
            .flex()
            .items_center()
            .gap(px(10.))
            .pl(px(12.))
            .pr(px(6.))
            .py(px(5.))
            .bg(rgb(BG_ACTIVE))
            .border_1()
            .border_color(rgb(RING_ACTIVE))
            .text_size(px(12.5))
            .text_color(rgb(BLUE_LIGHT))
            .child(div().size(px(6.)).rounded(px(3.)).bg(rgb(BLUE)))
            .child(div().flex_1().child(format!("{n} new since you last looked")))
            .child(
                button_look("jump-new")
                    .h(px(26.))
                    .on_click(cx.listener(|this, _, _, cx| this.jump_to_new(cx)))
                    .child("Jump to first new")
                    // A command of yours on n runs instead.
                    .when(self.find_cmd(|c| c.key == "n").is_none(), |b| b.child(kbd("n"))),
            )
            .into_any_element()
    }

    /// The first new item: when a jump is asked for, it scrolls the conversation so it
    /// sits at the top, from where it was laid out.
    fn jump_target(&self, item: AnyElement) -> AnyElement {
        let (jump, scroll) = (self.jump_new.clone(), self.detail_scroll.clone());
        let at = canvas(
            move |b, window, _| {
                if jump.replace(false) {
                    let (off, max) = (scroll.offset(), scroll.max_offset());
                    let y = off.y - (b.top() - scroll.bounds().top()) + px(12.);
                    scroll.set_offset(point(off.x, y.clamp(-max.y, px(0.))));
                    window.request_animation_frame();
                }
            },
            |_, _, _, _| {},
        );
        div().relative().child(item).child(at.absolute().top_0().left_0().size_full()).into_any_element()
    }

    fn aside(&self, d: &Detail, cx: &mut Context<Self>) -> impl IntoElement {
        let pr = &d.pr;
        let (pass, fail, pend) = d.check_counts();
        let (glyph, color, summary) = if d.checks.is_empty() {
            ("–", DIM, "No checks".to_string())
        } else if fail > 0 {
            ("✕", RED, format!("{fail} failing, {pass} passed"))
        } else if pend > 0 {
            ("●", YELLOW, format!("{pend} running, {pass} passed"))
        } else {
            ("✓", GREEN, format!("All {pass} checks passed"))
        };
        let local = match self.folders.get(&pr.key()) {
            Some(FolderState::Ready(f)) => {
                let on = if f.branch == pr.head { "on this branch".to_string() } else { format!("on {}", f.branch) };
                let mut lines = vec![
                    div().text_color(rgb(TEXT_SOFT)).truncate().child(short_home(&f.path)).into_any_element(),
                    div().text_color(rgb(SOFT)).child(on).into_any_element(),
                ];
                if f.rebasing {
                    lines.push(div().text_color(rgb(YELLOW)).child("Rebase in progress").into_any_element());
                }
                if !f.changes.is_empty() {
                    lines.push(
                        div().text_color(rgb(YELLOW)).child(format!("{} uncommitted changes", f.changes.len())).into_any_element(),
                    );
                }
                lines
            }
            Some(FolderState::NoClone) => vec![div().text_color(rgb(DIM)).child("No local clone").into_any_element()],
            Some(FolderState::Failed(e)) => vec![div().text_color(rgb(RED)).child(e.clone()).into_any_element()],
            _ => vec![div().text_color(rgb(DIM)).child("Looking for a clone…").into_any_element()],
        };
        let rebase = self.find_cmd(|c| c.name.eq_ignore_ascii_case("rebase"));
        let base = pr.base.clone();

        div()
            .w_full()
            .flex()
            .flex_col()
            .text_size(px(12.))
            .border_1()
            .border_color(rgb(BORDER))
            .bg(rgb(BG_SIDE))
            .child(
                aside_section("Branch")
                    .child(div().font_family(MONO).text_color(rgb(BLUE_LIGHT)).truncate().child(pr.head.clone()))
                    .child(div().text_color(rgb(SOFT)).child(format!("into {}", pr.base)))
                    .when_some(d.auto_merge.as_ref(), |s, a| {
                        s.child(
                            div()
                                .flex()
                                .items_center()
                                .gap(px(6.))
                                .text_color(rgb(GREEN))
                                .child(icon("git-merge", 12., GREEN))
                                .child("Auto-merge on"),
                        )
                        .child(div().text_color(rgb(SOFT)).child(format!("GitHub will {} once checks and reviews pass · by {}", a.word(), a.by)))
                    })
                    .when(pr.conflict(), |s| {
                        s.child(
                            div()
                                .px(px(8.))
                                .py(px(6.))
                                .border_1()
                                .border_color(rgb(0x5a4a26))
                                .bg(rgb(0x2a2418))
                                .text_color(rgb(YELLOW))
                                .child(format!("Merge conflict with {}", pr.base)),
                        )
                    })
                    .when_some(rebase, |s, c| {
                        s.child(
                            button_look("rebase")
                                .h(px(28.))
                                .justify_center()
                                .on_click(cx.listener(move |this, _, window, cx| this.start(c.clone(), window, cx)))
                                .child(format!("Rebase on {base}")),
                        )
                    }),
            )
            .child(aside_section("Local clone").children(local))
            .child(aside_section("Reviewers").children(if d.reviewers.is_empty() {
                vec![div().text_color(rgb(DIM)).child("No reviewers yet").into_any_element()]
            } else {
                d.reviewers
                    .iter()
                    .map(|r| {
                        let (label, color) = match r.state.as_str() {
                            "APPROVED" => ("approved", GREEN),
                            "CHANGES_REQUESTED" => ("changes", RED),
                            "COMMENTED" => ("commented", SOFT),
                            "DISMISSED" => ("dismissed", DIM),
                            _ => ("requested", DIM),
                        };
                        div()
                            .flex()
                            .items_center()
                            .gap(px(8.))
                            .child(self.images.face(&r.login, 18.))
                            .child(div().flex_1().min_w(px(0.)).truncate().text_color(rgb(TEXT)).child(r.login.clone()))
                            .child(div().text_color(rgb(color)).child(label))
                            .into_any_element()
                    })
                    .collect()
            }))
            .child(
                aside_section("Checks")
                    .child(
                        div()
                            .id("checks-link")
                            .flex()
                            .items_center()
                            .gap(px(8.))
                            .py(px(4.))
                            .cursor_pointer()
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.section = Section::Checks;
                                cx.notify();
                            }))
                            .child(div().text_color(rgb(color)).child(glyph))
                            .child(div().flex_1().text_color(rgb(TEXT)).child(summary))
                            .child(div().text_color(rgb(DIM)).child("View")),
                    )
                    // The first few failing checks; a big failing matrix would push the rest of the panel away.
                    .children(d.checks.iter().enumerate().filter(|(_, c)| matches!(check_kind(&c.state), CheckKind::Fail)).take(5).map(|(i, c)| {
                        let row = div()
                            .id(("failed-check", i))
                            .flex()
                            .items_center()
                            .gap(px(8.))
                            .px(px(4.))
                            .py(px(2.))
                            .child(div().text_color(rgb(RED)).child("✕"))
                            .child(div().flex_1().min_w(px(0.)).truncate().text_color(rgb(TEXT_SOFT)).child(c.name.clone()));
                        opens_log(row, c, 11.)
                    }))
                    .when(d.check_counts().1 > 5, |s| {
                        s.child(
                            div()
                                .id("more-failed")
                                .px(px(4.))
                                .py(px(2.))
                                .cursor_pointer()
                                .text_color(rgb(DIM))
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.section = Section::Checks;
                                    cx.notify();
                                }))
                                .child(format!("and {} more", d.check_counts().1 - 5)),
                        )
                    })
                    .children(self.rerun_button("rerun-aside", d, cx).map(|b| b.h(px(28.)).justify_center())),
            )
            .child(
                aside_section("Changes").child(
                    div()
                        .flex()
                        .gap(px(8.))
                        .child(div().text_color(rgb(GREEN)).child(format!("+{}", d.additions)))
                        .child(div().text_color(rgb(RED)).child(format!("−{}", d.deletions)))
                        .child(div().text_color(rgb(SOFT)).child(format!("{} files", d.changed_files))),
                ),
            )
            .when(!d.labels.is_empty(), |a| {
                a.child(aside_section("Labels").child(div().flex().flex_wrap().gap(px(6.)).children(d.labels.iter().map(|l| {
                    div().px(px(6.)).border_1().border_color(rgb(BORDER_3)).text_size(px(11.)).text_color(rgb(TEXT_SOFT)).child(l.clone())
                }))))
            })
    }

    fn footer(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let pr = self.selected_pr();
        let branch = match pr.as_ref().and_then(|p| self.folders.get(&p.key())) {
            Some(FolderState::Ready(f)) => {
                let mut row = div().flex().items_center().gap(px(6.)).child(div().max_w(px(220.)).truncate().child(f.branch.clone()));
                if !f.changes.is_empty() {
                    row = row.child(div().text_color(rgb(YELLOW)).child(format!("±{}", f.changes.len())));
                }
                row.into_any_element()
            }
            Some(FolderState::NoClone) => div().text_color(rgb(DIM)).child("no local clone").into_any_element(),
            _ => div().text_color(rgb(DIM)).child("–").into_any_element(),
        };
        let buttons: Vec<AnyElement> = self
            .hello
            .commands
            .iter()
            .enumerate()
            // Reviews and Merge when ready are buttons of the review box; Review goes there.
            .filter(|(_, c)| c.bar && !c.reviews() && c.action != "automerge")
            .map(|(i, c)| {
                let cmd = c.clone();
                let active = self.run.as_ref().is_some_and(|r| r.cmd.name == c.name);
                div()
                    .id(("cmd", i))
                    .h(px(28.))
                    .px(px(8.))
                    .flex()
                    .items_center()
                    .gap(px(6.))
                    .border_1()
                    .border_color(rgb(if active { RING_ACTIVE } else { BG_DEEP }))
                    .when(active, |d| d.bg(rgb(BG_ACTIVE)))
                    .cursor_pointer()
                    .hover(|s| s.bg(rgb(BG_HOVER)).border_color(rgb(BORDER_2)))
                    .on_click(cx.listener(move |this, _, window, cx| this.start(cmd.clone(), window, cx)))
                    .when(!c.key.is_empty(), |d| d.child(kbd(c.key.clone())))
                    .child(div().text_color(rgb(TEXT)).child(c.name.clone()))
                    .into_any_element()
            })
            .collect();
        div()
            .flex_none()
            .flex()
            .items_center()
            .gap(px(4.))
            .px(px(8.))
            .py(px(5.))
            .bg(rgb(BG_DEEP))
            .border_t_1()
            .border_color(rgb(BORDER))
            .text_size(px(12.))
            .child(
                div()
                    .h(px(28.))
                    .flex()
                    .items_center()
                    .gap(px(6.))
                    .pl(px(4.))
                    .pr(px(10.))
                    .mr(px(4.))
                    .border_r_1()
                    .border_color(rgb(BORDER))
                    .text_size(px(11.5))
                    .text_color(rgb(TEXT_SOFT))
                    .font_family(MONO)
                    .child(icon("git-branch", 13., DIM))
                    .child(branch),
            )
            .child(div().id("bar").flex_1().min_w(px(0.)).flex().gap(px(4.)).overflow_x_scroll().children(buttons))
            .child(
                div()
                    .id("filters")
                    .h(px(28.))
                    .px(px(8.))
                    .flex()
                    .items_center()
                    .gap(px(6.))
                    .border_1()
                    .border_color(rgb(BORDER_2))
                    .cursor_pointer()
                    .hover(|s| s.bg(rgb(BG_HOVER)))
                    .on_click(cx.listener(|this, _, window, cx| this.open_palette(PaletteMode::Filters, window, cx)))
                    .child(kbd("⌘K"))
                    .child(div().text_color(rgb(TEXT)).child("Filters")),
            )
    }

    fn run_bar(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let run = self.run.as_ref()?;
        let label = |t: String| div().flex_none().text_size(px(11.)).text_color(rgb(DIM)).child(t.to_uppercase());
        let close = div()
            .id("run-close")
            .size(px(26.))
            .flex_none()
            .flex()
            .items_center()
            .justify_center()
            .cursor_pointer()
            .hover(|s| s.bg(rgb(BG_HOVER)))
            .on_click(cx.listener(|this, _, window, cx| this.close_run(window, cx)))
            .child(icon("close", 13., DIM));
        let row = || div().flex().items_center().gap(px(10.)).px(px(12.)).py(px(8.));
        let script = |s: String| {
            div()
                .flex_1()
                .min_w(px(0.))
                .truncate()
                .font_family(MONO)
                .text_size(px(12.5))
                .text_color(rgb(TEXT))
                .child(div().flex().child(div().text_color(rgb(GREEN)).child("$ ")).child(s))
        };

        let body: Vec<AnyElement> = match &run.stage {
            Stage::Prompt(input) => vec![
                row()
                    .child(label(run.cmd.name.clone()))
                    .child(div().flex_1().child(Input::new(input)))
                    .child(div().text_size(px(11.)).text_color(rgb(DIM)).child("↵ continue · esc cancel"))
                    .child(close)
                    .into_any_element(),
            ],
            Stage::Preparing => vec![
                row()
                    .child(label(run.cmd.name.clone()))
                    .child(div().flex_1().text_color(rgb(SOFT)).child("Preparing…"))
                    .child(close)
                    .into_any_element(),
            ],
            Stage::Ready { plan, drop_armed } => {
                let full = plan.full_script();
                let copy = full.clone();
                let mut rows = vec![
                    row()
                        .child(label(plan.title.clone()))
                        .child(script(full))
                        .child(div().flex_none().text_size(px(11.)).text_color(rgb(DIM)).child(plan.dir()))
                        .child(
                            button_look("copy")
                                .h(px(26.))
                                .text_size(px(12.))
                                .on_click(cx.listener(move |_, _, _, cx| cx.write_to_clipboard(ClipboardItem::new_string(copy.clone()))))
                                .child("Copy"),
                        )
                        .when(plan.changes.is_empty(), |r| {
                            r.child(
                                primary_look("run").h(px(26.)).text_size(px(12.)).on_click(cx.listener(|this, _, _, cx| this.confirm("", cx))).child("Run ↵"),
                            )
                        })
                        .child(close)
                        .into_any_element(),
                ];
                if !plan.changes.is_empty() {
                    let n = plan.changes.len();
                    let shown: Vec<String> = plan.changes.iter().take(4).map(|c| c.trim().to_string()).collect();
                    let more = if n > 4 { format!(" and {} more", n - 4) } else { String::new() };
                    rows.push(
                        row()
                            .pt(px(0.))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w(px(0.))
                                    .flex()
                                    .flex_col()
                                    .gap(px(2.))
                                    .text_size(px(12.))
                                    .child(div().text_color(rgb(YELLOW)).child(format!(
                                        "{} has {n} uncommitted change{}. Stash or drop them first.",
                                        short_home(&plan.folder.path),
                                        if n == 1 { "" } else { "s" }
                                    )))
                                    .child(div().truncate().font_family(MONO).text_color(rgb(DIM)).child(format!("{}{more}", shown.join("  ")))),
                            )
                            .child(
                                button_look("stash")
                                    .h(px(26.))
                                    .text_size(px(12.))
                                    .on_click(cx.listener(|this, _, _, cx| this.confirm("stash", cx)))
                                    .child(kbd("s"))
                                    .child("Stash & run"),
                            )
                            .child(
                                button_look("drop")
                                    .h(px(26.))
                                    .text_size(px(12.))
                                    .when(*drop_armed, |b| b.border_color(rgb(RED)).text_color(rgb(RED)))
                                    .on_click(cx.listener(|this, _, _, cx| this.confirm("drop", cx)))
                                    .child(kbd("d"))
                                    .child(if *drop_armed { format!("Drop {n} changes for good") } else { "Drop & run".to_string() }),
                            )
                            .into_any_element(),
                    );
                }
                rows
            }
            Stage::Running(plan) => vec![
                row()
                    .child(label(plan.title.clone()))
                    .child(script(plan.full_script()))
                    .child(div().flex_none().text_color(rgb(SOFT)).child("Running…"))
                    .into_any_element(),
            ],
            Stage::Done { title, ok, output } => {
                let (g, c) = if *ok { ("✓", GREEN) } else { ("✕", RED) };
                let mut rows = vec![
                    row()
                        .child(div().text_color(rgb(c)).child(g))
                        .child(div().flex_1().text_color(rgb(TEXT)).child(if *ok { format!("{title} finished") } else { format!("{title} failed") }))
                        .child(close)
                        .into_any_element(),
                ];
                if !output.is_empty() {
                    rows.push(
                        div()
                            .id("run-output")
                            .max_h(px(180.))
                            .overflow_y_scroll()
                            .px(px(12.))
                            .pb(px(10.))
                            .font_family(MONO)
                            .text_size(px(12.))
                            .text_color(rgb(if *ok { TEXT_SOFT } else { 0xf2b0b0 }))
                            .children(output.lines().map(|l| div().child(l.to_string())))
                            .into_any_element(),
                    );
                }
                rows
            }
            Stage::NoClone { found, path } => {
                let mut rows = vec![
                    row()
                        .child(label(run.cmd.name.clone()))
                        .child(div().flex_1().text_color(rgb(YELLOW)).child(format!("No local clone of {} found.", run.pr.repo)))
                        .child(
                            button_look("clone")
                                .h(px(26.))
                                .text_size(px(12.))
                                .on_click(cx.listener(|this, _, _, cx| this.clone_repo(cx)))
                                .child(format!("Clone {}", run.pr.repo)),
                        )
                        .child(close)
                        .into_any_element(),
                ];
                match found {
                    None => rows.push(row().pt(px(0.)).text_color(rgb(DIM)).child("Searching your disk for clones…").into_any_element()),
                    Some(list) => {
                        for (i, p) in list.iter().enumerate() {
                            let path = p.clone();
                            rows.push(
                                div()
                                    .id(("found", i))
                                    .mx(px(12.))
                                    .px(px(8.))
                                    .h(px(28.))
                                    .flex()
                                    .items_center()
                                    .gap(px(8.))
                                    .cursor_pointer()
                                    .hover(|s| s.bg(rgb(BG_HOVER)))
                                    .on_click(cx.listener(move |this, _, window, cx| this.set_clone_path(path.clone(), window, cx)))
                                    .child(icon("folder-git-2", 13., BLUE))
                                    .child(div().font_family(MONO).text_color(rgb(TEXT)).child(short_home(p)))
                                    .child(div().text_color(rgb(DIM)).child("use this clone"))
                                    .into_any_element(),
                            );
                        }
                    }
                }
                rows.push(
                    row()
                        .child(div().flex_none().text_size(px(12.)).text_color(rgb(SOFT)).child("Or type a path"))
                        .child(div().flex_1().child(Input::new(path)))
                        .into_any_element(),
                );
                rows
            }
            Stage::Failed(msg) => vec![
                row()
                    .child(div().text_color(rgb(RED)).child("✕"))
                    .child(div().flex_1().text_color(rgb(0xf2b0b0)).child(msg.clone()))
                    .child(close)
                    .into_any_element(),
            ],
        };
        Some(
            div()
                .flex_none()
                .flex()
                .flex_col()
                .bg(rgb(BG_INPUT))
                .border_t_1()
                .border_color(rgb(BORDER_POP))
                .children(body)
                .into_any_element(),
        )
    }

    fn palette_view(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = self.palette.as_ref()?;
        let items = self.palette_items(cx);
        let mut list: Vec<AnyElement> = vec![];
        let mut group = "";
        for (i, it) in items.iter().enumerate() {
            if it.group != group {
                group = it.group;
                list.push(div().px(px(14.)).pt(px(8.)).pb(px(4.)).child(caption(group)).into_any_element());
            }
            let hi = i == p.hi;
            let apply = it.apply.clone();
            list.push(
                div()
                    .id(("item", i))
                    .h(px(34.))
                    .px(px(14.))
                    .flex()
                    .items_center()
                    .gap(px(10.))
                    .when(hi, |d| d.bg(rgb(0x2a3140)))
                    .cursor_pointer()
                    .on_mouse_move(cx.listener(move |this, _, _, cx| {
                        if let Some(p) = this.palette.as_mut() {
                            if p.hi != i {
                                p.hi = i;
                                cx.notify();
                            }
                        }
                    }))
                    .on_click(cx.listener(move |this, _, window, cx| this.apply(apply.clone(), window, cx)))
                    .child(div().w(px(12.)).text_color(rgb(BLUE)).child(if it.active { "✓" } else { "" }))
                    .child(div().flex_1().min_w(px(0.)).truncate().child(it.label.clone()))
                    .child(div().font_family(MONO).text_size(px(12.)).text_color(rgb(SOFT)).child(it.qual.clone()))
                    .into_any_element(),
            );
        }
        if items.is_empty() {
            list.push(
                div()
                    .px(px(14.))
                    .py(px(18.))
                    .text_color(rgb(SOFT))
                    .child("No saved filter matches. Add a qualifier such as label:bug to search GitHub directly.")
                    .into_any_element(),
            );
        }
        let hint = |k: &str, t: &str| div().flex().gap(px(4.)).child(kbd(k.to_string())).child(t.to_string());
        Some(
            div()
                .id("palette-backdrop")
                .absolute()
                .top_0()
                .left_0()
                .size_full()
                .bg(rgba(0x0a0b0d99))
                .flex()
                .flex_col()
                .items_center()
                .pt(px(56.))
                .on_click(cx.listener(|this, _, window, cx| this.close_palette(window, cx)))
                .capture_key_down(cx.listener(Self::palette_key))
                .child(
                    div()
                        .id("palette")
                        .w(px(640.))
                        .flex()
                        .flex_col()
                        .bg(rgb(0x202328))
                        .border_1()
                        .border_color(rgb(BORDER_POP))
                        .on_click(|_, _, cx| cx.stop_propagation())
                        .child(
                            div()
                                .h(px(46.))
                                .pl(px(14.))
                                .pr(px(10.))
                                .flex()
                                .items_center()
                                .gap(px(10.))
                                .border_b_1()
                                .border_color(rgb(BORDER_2))
                                .child(icon("search", 15., SOFT))
                                .child(div().flex_1().child(Input::new(&p.input).appearance(false)))
                                .child(kbd("esc")),
                        )
                        .child(div().id("palette-list").max_h(px(340.)).overflow_y_scroll().py(px(4.)).children(list))
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .gap(px(14.))
                                .px(px(14.))
                                .py(px(6.))
                                .bg(rgb(0x1c1e23))
                                .border_t_1()
                                .border_color(rgb(BORDER_2))
                                .text_size(px(11.))
                                .text_color(rgb(DIM))
                                .child(hint("↑↓", "move"))
                                .child(hint("↵", if p.mode == PaletteMode::Prs { "open" } else { "apply" }))
                                .child(div().ml_auto().font_family(MONO).text_color(rgb(SOFT)).child(if p.mode == PaletteMode::Prs {
                                    "⌘T go to PR · ⌘K filters"
                                } else {
                                    "author: involves: review-requested: label: repo:"
                                })),
                        ),
                )
                .into_any_element(),
        )
    }

    fn banner_view(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        if self.banner.is_none() {
            let note = self.notice.clone()?;
            return Some(
                div()
                    .flex_none()
                    .flex()
                    .items_center()
                    .gap(px(8.))
                    .px(px(14.))
                    .py(px(6.))
                    .bg(rgb(0x1c2a1f))
                    .border_b_1()
                    .border_color(rgb(0x2f4a2c))
                    .text_size(px(12.))
                    .text_color(rgb(GREEN))
                    .child("✓")
                    .child(note)
                    .into_any_element(),
            );
        }
        let msg = self.banner.clone()?;
        Some(
            div()
                .flex_none()
                .flex()
                .items_center()
                .gap(px(10.))
                .px(px(14.))
                .py(px(6.))
                .bg(rgb(0x2a2418))
                .border_b_1()
                .border_color(rgb(0x5a4a26))
                .text_size(px(12.))
                .text_color(rgb(YELLOW))
                .child(div().flex_1().child(msg))
                .child(
                    div()
                        .id("banner-close")
                        .cursor_pointer()
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.banner = None;
                            cx.notify();
                        }))
                        .child(icon("close", 12., YELLOW)),
                )
                .into_any_element(),
        )
    }
}

fn section_head(title: &str, count: Option<usize>) -> Div {
    div()
        .h(px(30.))
        .flex_none()
        .flex()
        .items_center()
        .justify_between()
        .pl(px(12.))
        .pr(px(6.))
        .border_b_1()
        .border_color(rgb(BORDER))
        .child(
            div()
                .flex()
                .gap(px(6.))
                .child(caption(title.to_string()))
                .children(count.map(|n| div().text_size(px(11.)).text_color(rgb(TEXT_SOFT)).child(n.to_string()))),
        )
}

fn aside_section(title: &str) -> Div {
    div()
        .p(px(12.))
        .flex()
        .flex_col()
        .gap(px(6.))
        .border_b_1()
        .border_color(rgb(BORDER))
        .child(caption(title.to_string()))
}

fn checks_view(d: &Detail, rerun: Option<Stateful<Div>>) -> impl IntoElement {
    let (pass, fail, pend) = d.check_counts();
    let (glyph, color) = if fail > 0 { ("✕", RED) } else if pend > 0 { ("●", YELLOW) } else { ("✓", GREEN) };
    card()
        .child(
            card_head()
                .child(div().text_color(rgb(color)).child(glyph))
                .child(div().flex_1().text_color(rgb(TEXT)).child(format!("{pass} passed · {fail} failing · {pend} running")))
                .children(rerun.map(|b| b.h(px(26.)).text_size(px(12.)))),
        )
        .children(d.checks.iter().enumerate().map(|(i, c)| {
            let (g, col, status) = match check_kind(&c.state) {
                CheckKind::Pass => ("✓", GREEN, c.state.to_lowercase()),
                CheckKind::Fail => ("✕", RED, c.state.to_lowercase()),
                CheckKind::Pending => ("●", YELLOW, c.state.to_lowercase()),
            };
            let row = div()
                .id(("check", i))
                .h(px(36.))
                .px(px(12.))
                .flex()
                .items_center()
                .gap(px(10.))
                .border_t_1()
                .border_color(rgb(BORDER))
                .text_size(px(12.5))
                .child(div().w(px(14.)).text_color(rgb(col)).child(g))
                .child(div().flex_1().min_w(px(0.)).truncate().text_color(rgb(TEXT)).child(c.name.clone()))
                .child(div().text_size(px(12.)).text_color(rgb(col)).child(status));
            opens_log(row, c, 12.)
        }))
        .when(d.checks.is_empty(), |c| c.child(div().p(px(12.)).text_color(rgb(DIM)).child("No checks reported.")))
}

/// Makes a check's row open its log in the browser, when it has a link.
fn opens_log(row: Stateful<Div>, c: &Check, size: f32) -> Stateful<Div> {
    match c.log_url().map(str::to_string) {
        Some(url) => row
            .cursor_pointer()
            .hover(|s| s.bg(rgb(BG_HOVER)))
            .on_click(move |_, _, cx| cx.open_url(&url))
            .child(icon("external-link", size, DIM)),
        None => row.child(div().w(px(size)).flex_none()),
    }
}

fn event_view(key: &str, i: usize, e: &Event, images: &Images, reply: Option<AnyElement>) -> AnyElement {
    let (verb, verb_color, border) = match (e.kind.as_str(), e.state.as_str()) {
        ("review", "APPROVED") => ("approved these changes".to_string(), GREEN, 0x2f4a2c),
        ("review", "CHANGES_REQUESTED") => ("requested changes".to_string(), RED, 0x5a3236),
        ("review", _) => ("reviewed".to_string(), SOFT, BORDER_2),
        ("thread", _) => {
            let at = if e.line > 0 { format!("{}:{}", e.path, e.line) } else { e.path.clone() };
            (format!("commented on {at}"), SOFT, BORDER_2)
        }
        _ => ("commented".to_string(), SOFT, BORDER_2),
    };
    // A review with no text is a one-line event, not a card.
    if e.kind == "review" && e.body.trim().is_empty() {
        let glyph = match e.state.as_str() {
            "APPROVED" => "✓",
            "CHANGES_REQUESTED" => "✕",
            _ => "•",
        };
        return div()
            .flex()
            .items_center()
            .gap(px(8.))
            .px(px(4.))
            .text_size(px(12.))
            .text_color(rgb(SOFT))
            .child(
                div()
                    .size(px(20.))
                    .flex()
                    .items_center()
                    .justify_center()
                    .border_1()
                    .border_color(rgb(verb_color))
                    .text_color(rgb(verb_color))
                    .text_size(px(11.))
                    .child(glyph),
            )
            .child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT)).child(e.author.clone()))
            .child(verb)
            .child(div().text_color(rgb(DIM)).child(format!("{} ago", ago(&e.at))))
            .when(e.new, |d| d.child(new_tag()))
            .into_any_element();
    }

    let mut body = div().px(px(14.)).py(px(12.)).flex().flex_col().gap(px(10.)).text_size(px(13.5)).text_color(rgb(TEXT_BODY));
    if e.kind == "thread" && !e.diff_hunk.is_empty() {
        let lines: Vec<&str> = e.diff_hunk.lines().collect();
        let tail = &lines[lines.len().saturating_sub(8)..];
        body = body.child(
            div()
                .border_1()
                .border_color(rgb(BORDER_2))
                .child(
                    div()
                        .h(px(28.))
                        .px(px(10.))
                        .flex()
                        .items_center()
                        .gap(px(6.))
                        .bg(rgb(BG_HEAD))
                        .border_b_1()
                        .border_color(rgb(BORDER_2))
                        .text_size(px(11.5))
                        .text_color(rgb(TEXT_SOFT))
                        .font_family(MONO)
                        .child(icon("file-diff", 12., DIM))
                        .child(e.path.clone())
                        .when(e.resolved, |d| d.child(div().ml_auto().text_color(rgb(GREEN)).child("resolved")))
                        .when(e.outdated, |d| d.child(div().ml_auto().text_color(rgb(DIM)).child("outdated"))),
                )
                .child(div().font_family(MONO).text_size(px(12.)).children(tail.iter().map(|l| {
                    let (bg, fg) = if l.starts_with("@@") {
                        (0x1f2633, BLUE)
                    } else if l.starts_with('+') {
                        (0x1d2f22, 0xb5dca8)
                    } else if l.starts_with('-') {
                        (0x36202a, 0xf2b0b0)
                    } else {
                        (BG_SIDE, TEXT_SOFT)
                    };
                    div().px(px(10.)).bg(rgb(bg)).text_color(rgb(fg)).truncate().child(l.to_string())
                }))),
        );
    }
    body = body.child(md_body(format!("ev-{key}-{i}"), &e.body, images));
    for (j, r) in e.replies.iter().enumerate() {
        body = body.child(
            div()
                .pt(px(10.))
                .border_t_1()
                .border_color(rgb(BORDER))
                .flex()
                .flex_col()
                .gap(px(6.))
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap(px(8.))
                        .text_size(px(12.))
                        .text_color(rgb(SOFT))
                        .child(images.face(&r.author, 18.))
                        .child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT)).child(r.author.clone()))
                        .child(div().text_color(rgb(DIM)).child(format!("{} ago", ago(&r.at))))
                        .when(r.new, |d| d.child(new_tag())),
                )
                .child(md_body(format!("ev-{key}-{i}-{j}"), &r.body, images)),
        );
    }
    if let Some(reply) = reply {
        body = body.child(reply);
    }
    card()
        .border_color(rgb(border))
        .child(
            card_head()
                .h(px(34.))
                .child(images.face(&e.author, 20.))
                .child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT)).child(e.author.clone()))
                .child(div().min_w(px(0.)).truncate().text_color(rgb(verb_color)).child(verb))
                .child(div().text_color(rgb(DIM)).child(format!("{} ago", ago(&e.at))))
                .when(e.new, |d| d.child(new_tag())),
        )
        .child(body)
        .into_any_element()
}

/// Marks a comment, review or reply that came after your last visit.
fn new_tag() -> Div {
    div().flex_none().px(px(6.)).border_1().border_color(rgb(RING_ACTIVE)).text_color(rgb(BLUE)).text_size(px(11.)).child("New")
}

impl Render for Deck {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.ensure_draft(window, cx);
        div()
            .id("deck")
            .key_context("Deck")
            .track_focus(&self.focus)
            .on_key_down(cx.listener(Self::on_key))
            .on_mouse_up(MouseButton::Left, cx.listener(|this, _, window, cx| this.line_release(window, cx)))
            .on_action(cx.listener(Self::on_go_to_pr))
            .on_action(cx.listener(Self::on_filters))
            .on_action(cx.listener(Self::on_close_tab))
            .on_action(cx.listener(Self::on_reopen_tab))
            .on_action(cx.listener(Self::on_refresh))
            .on_action(cx.listener(Self::on_next_tab))
            .on_action(cx.listener(Self::on_prev_tab))
            .on_action(cx.listener(Self::on_open_config))
            .on_action(cx.listener(Self::on_open_in_editor))
            .on_action(cx.listener(Self::on_open_on_github))
            .on_action(cx.listener(Self::on_toggle_sidebar))
            .on_action(cx.listener(Self::on_run_setup))
            .on_action(cx.listener(Self::on_comment))
            .size_full()
            .relative()
            .flex()
            .flex_col()
            .bg(rgb(BG))
            .text_color(rgb(TEXT))
            .text_size(px(13.))
            .child(self.header(cx))
            .children(self.banner_view(cx))
            .when(self.setup.is_some(), |d| d.child(self.setup_view(cx)))
            .when(self.setup.is_none(), |d| d.child(self.body(cx)).children(self.run_bar(cx)).child(self.footer(cx)))
            .children(self.palette_view(cx))
    }
}

/// Puts the window in a known state for README screenshots (demo data only).
#[cfg(feature = "snapshot")]
impl Deck {
    /// One line about the window, for checking shortcuts.
    pub fn describe(&self) -> String {
        let sel = self.selected_pr().map(|p| format!("#{}", p.number)).unwrap_or("-".into());
        let tabs: Vec<String> = self.tabs.iter().map(|t| format!("#{}", t.number)).collect();
        let palette = match self.palette.as_ref().map(|p| p.mode) {
            Some(PaletteMode::Prs) => "go-to-PR",
            Some(PaletteMode::Filters) => "filters",
            None => "closed",
        };
        format!(
            "selected {sel}, tabs [{}], palette {palette}, sidebar {}, line box {}",
            tabs.join(" "),
            if self.layout.sidebar_hidden { "hidden" } else { "shown" },
            if self.line_editor.is_some() { "open" } else { "closed" }
        )
    }

    pub fn show_state(&mut self, state: &str, window: &mut Window, cx: &mut Context<Self>) {
        let pick = |this: &Self, n: u64| this.prs.iter().find(|p| p.number == n).cloned();
        match state {
            "goto" => {
                self.open_palette(PaletteMode::Prs, window, cx);
                if let Some(p) = self.palette.as_ref() {
                    p.input.update(cx, |s, cx| s.set_value("reg", window, cx));
                }
            }
            s if s.starts_with("pr:") => {
                // pr:owner/repo#123 opens that PR from a search of its repo.
                if let Some((repo, _)) = s[3..].split_once('#') {
                    self.want = Some(s[3..].to_lowercase());
                    self.custom = Some(format!("repo:{repo}"));
                    self.selected = None;
                    self.load(cx);
                }
            }
            s if s.starts_with("scroll:") => {
                let y: f32 = s[7..].parse().unwrap_or(0.);
                self.detail_scroll.set_offset(point(px(0.), px(-y)));
            }
            "compose" => self.focus_composer(window, cx),
            s if s.starts_with("line:") => {
                // line:<file index>:<LEFT|RIGHT>:<line>, or line:add / line:single
                match &s[5..] {
                    "add" => self.submit_line(false, window, cx),
                    "single" => self.submit_line(true, window, cx),
                    spec => {
                        let parts: Vec<&str> = spec.split(':').collect();
                        if let (Some(pr), [f, side, n]) = (self.selected_pr(), parts.as_slice()) {
                            let path = match self.diffs.get(&format!("pr:{}", pr.key())) {
                                Some(DiffState::Files(files)) => f.parse::<usize>().ok().and_then(|i| files.get(i)).map(|p| p.file.path.clone()),
                                _ => None,
                            };
                            if let (Some(path), Ok(n)) = (path, n.parse::<u32>()) {
                                let side = if *side == "LEFT" { "LEFT" } else { "RIGHT" };
                                self.open_line_editor(pr.key(), path, n, side, window, cx);
                            }
                        }
                    }
                }
            }
            s if s.starts_with("select:") || s.starts_with("shift:") => {
                // select:<file>:<from>:<to> drags over lines; shift:<file>:<ix> extends
                let parts: Vec<usize> = s.split(':').skip(1).filter_map(|n| n.parse().ok()).collect();
                let path = self.selected_pr().and_then(|pr| match self.diffs.get(&format!("pr:{}", pr.key())) {
                    Some(DiffState::Files(files)) => parts.first().and_then(|&i| files.get(i)).map(|f| (pr.key(), f.file.path.clone())),
                    _ => None,
                });
                if let Some((pr, path)) = path {
                    match (s.starts_with("shift:"), parts.as_slice()) {
                        (true, [_, ix]) => self.line_press(pr, path, *ix, true, window, cx),
                        (false, [_, from, to]) => {
                            self.line_press(pr.clone(), path.clone(), *from, false, window, cx);
                            self.line_drag(pr, path, *to, cx);
                            self.line_release(window, cx);
                        }
                        _ => {}
                    }
                }
            }
            "suggest" => self.suggest_change_for_test(window, cx),
            s if s.starts_with("linedraft:") => {
                if let Some(ed) = self.line_editor.as_ref() {
                    let text = s[10..].to_string();
                    ed.input.update(cx, |st, cx| st.set_value(text, window, cx));
                }
            }
            s if s.starts_with("draft:") => {
                if let Some(d) = self.selected.as_ref().and_then(|k| self.drafts.get(k)) {
                    let text = s[6..].replace("\\n", "\n");
                    d.input.update(cx, |st, cx| st.set_value(text, window, cx));
                }
            }
            "post:comment" | "post:approve" | "post:changes" => {
                if let Some(key) = self.selected.clone() {
                    let what = match state {
                        "post:comment" => Post::Comment,
                        "post:approve" => Post::Approve,
                        _ => Post::RequestChanges,
                    };
                    self.post(&key, what, window, cx);
                }
            }
            "reply:first" => {
                let found = self.selected.as_ref().and_then(|k| {
                    self.details.get(k)?.timeline.iter().find(|e| !e.thread_id.is_empty()).map(|e| (k.clone(), e.thread_id.clone()))
                });
                if let Some((key, thread)) = found {
                    self.start_reply(key, thread, window, cx);
                }
            }
            s if s.starts_with("replydraft:") => {
                if let Some(r) = self.thread_reply.as_ref() {
                    let text = s[11..].to_string();
                    r.input.update(cx, |st, cx| st.set_value(text, window, cx));
                }
            }
            "post:reply" => self.send_reply(window, cx),
            "setup-signin" => self.sign_in(cx),
            "setup-folders" => self.setup_step(SetupStep::Folders, cx),
            "setup-editor" => self.setup_step(SetupStep::Editor, cx),
            "commits" => self.section = Section::Commits,
            "files" => {
                self.section = Section::Files;
                self.load_section(cx);
            }
            s if s.starts_with("commit:") => {
                self.section = Section::Commits;
                let sha = s[7..].parse::<usize>().ok().and_then(|i| self.selected_pr().and_then(|p| self.details.get(&p.key())?.commit_list.get(i).map(|c| c.oid.clone())));
                self.open_commit(sha, cx);
            }
            "palette" => {
                self.open_palette(PaletteMode::Filters, window, cx);
                if let Some(p) = self.palette.as_ref() {
                    p.input.update(cx, |s, cx| s.set_value("re", window, cx));
                }
            }
            "changes" => {
                if let Some(pr) = pick(self, 4821) {
                    self.select(pr, cx);
                }
                if let Some(c) = self.find_cmd(|c| c.name == "Rebase") {
                    self.start(c, window, cx);
                }
            }
            "checks" => {
                self.section = Section::Checks;
            }
            "new" => self.jump_to_new(cx),
            "rerun" => {
                if let Some(c) = self.find_cmd(|c| c.action == "rerun") {
                    self.start(c, window, cx);
                }
            }
            "merge" => {
                if let Some(c) = self.find_cmd(|c| c.action == "automerge") {
                    self.start(c, window, cx);
                }
            }
            "confirm" => self.confirm("", cx),
            "noclone" => {
                if let Some(pr) = self.prs.iter().find(|p| p.repo == "upstream/datakit").cloned() {
                    self.select(pr, cx);
                }
                if let Some(c) = self.find_cmd(|c| c.name == "Fetch") {
                    self.start(c, window, cx);
                }
            }
            _ => {}
        }
        cx.notify();
    }
}

/// Scores `hay` against a query: every word must appear, in any order. Lower is
/// better; None means no match. Matches at the start of a word rank first.
fn fuzzy_score(query: &str, hay: &str) -> Option<usize> {
    let mut score = 0;
    for word in query.split_whitespace() {
        let word = word.trim_start_matches('#');
        let at = hay.find(word)?;
        let boundary = at == 0 || !hay.as_bytes()[at - 1].is_ascii_alphanumeric();
        score += at + if boundary { 0 } else { 50 };
    }
    Some(score)
}
