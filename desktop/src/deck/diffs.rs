//! Changes inside the app: Commits opens a commit with its diff, and Files changed
//! shows every file's diff, each one collapsible. Patches come from GitHub through
//! the core (`prDiff`, `commitDiff`) and are parsed once.

use std::sync::Arc;

use super::*;
use crate::diff::{self, Kind, Line};

/// A file's change, parsed.
pub(super) struct ParsedFile {
    pub(super) file: FileDiff,
    lines: Vec<Line>,
}

pub(super) enum DiffState {
    Loading,
    Files(Arc<Vec<ParsedFile>>),
    Commit(Arc<CommitDiff>, Arc<Vec<ParsedFile>>),
    Failed(String),
}

fn parse_all(files: Vec<FileDiff>) -> Arc<Vec<ParsedFile>> {
    Arc::new(files.into_iter().map(|f| ParsedFile { lines: diff::parse(&f.patch), file: f }).collect())
}

/// Files expand until about this many diff lines are on screen; the rest start
/// collapsed, so a huge PR stays quick. A click opens any of them.
const OPEN_BUDGET: usize = 1500;
/// A single file shows at most this many lines.
const FILE_LIMIT: usize = 2000;

fn pr_key(pr: &Pr) -> String {
    format!("pr:{}", pr.key())
}

fn commit_key(repo: &str, sha: &str) -> String {
    format!("commit:{}@{sha}", repo.to_lowercase())
}

impl Deck {
    pub(super) fn load_pr_diff(&mut self, pr: Pr, cx: &mut Context<Self>) {
        let key = pr_key(&pr);
        if matches!(self.diffs.get(&key), Some(DiffState::Loading | DiffState::Files(_))) {
            return;
        }
        let Some(rpc) = self.rpc.clone() else { return };
        self.diffs.insert(key.clone(), DiffState::Loading);
        let res = rpc.call::<Vec<FileDiff>>("prDiff", json!({ "pr": pr }));
        cx.spawn(async move |this, cx| {
            let res = res.await;
            let _ = this.update(cx, |this, cx| {
                let st = match res {
                    Ok(files) => DiffState::Files(parse_all(files)),
                    Err(e) => DiffState::Failed(e.message),
                };
                this.diffs.insert(key, st);
                cx.notify();
            });
        })
        .detach();
    }

    fn load_commit_diff(&mut self, repo: String, sha: String, cx: &mut Context<Self>) {
        let key = commit_key(&repo, &sha);
        if matches!(self.diffs.get(&key), Some(DiffState::Loading | DiffState::Commit(..))) {
            return;
        }
        let Some(rpc) = self.rpc.clone() else { return };
        self.diffs.insert(key.clone(), DiffState::Loading);
        let res = rpc.call::<CommitDiff>("commitDiff", json!({ "repo": repo, "sha": sha }));
        cx.spawn(async move |this, cx| {
            let res = res.await;
            let _ = this.update(cx, |this, cx| {
                let st = match res {
                    Ok(mut c) => {
                        let files = std::mem::take(&mut c.files);
                        DiffState::Commit(Arc::new(c), parse_all(files))
                    }
                    Err(e) => DiffState::Failed(e.message),
                };
                this.diffs.insert(key, st);
                cx.notify();
            });
        })
        .detach();
    }

    /// Opens a commit of the selected PR (None goes back to the list).
    pub(super) fn open_commit(&mut self, sha: Option<String>, cx: &mut Context<Self>) {
        if let (Some(sha), Some(pr)) = (sha.clone(), self.selected_pr()) {
            self.load_commit_diff(pr.repo.clone(), sha, cx);
        }
        self.commit = sha;
        self.detail_scroll.set_offset(point(px(0.), px(0.)));
        cx.notify();
    }

    /// Loads what the current section needs: the PR's diff for Files changed.
    pub(super) fn load_section(&mut self, cx: &mut Context<Self>) {
        if self.section == Section::Files {
            if let Some(pr) = self.selected_pr() {
                self.load_pr_diff(pr, cx);
            }
        }
    }

    fn toggle_file(&mut self, id: String, open_now: bool, cx: &mut Context<Self>) {
        self.file_open.insert(id, !open_now);
        cx.notify();
    }

    fn set_all_files(&mut self, prefix: &str, paths: Vec<String>, open: bool, cx: &mut Context<Self>) {
        for p in paths {
            self.file_open.insert(format!("{prefix}|{p}"), open);
        }
        cx.notify();
    }

    // ── views ───────────────────────────────────────────────────────────────

    pub(super) fn commits_view(&self, d: &Detail, cx: &mut Context<Self>) -> AnyElement {
        if let Some(sha) = self.commit.clone() {
            if let Some(i) = d.commit_list.iter().position(|c| c.oid == sha) {
                return self.commit_view(d, i, cx);
            }
        }
        if d.commit_list.is_empty() {
            return self.handoff(format!("{} commits", d.commits), cx);
        }
        let url = d.pr.url.clone();
        let more = d.commits.saturating_sub(d.commit_list.len() as u64);
        let rows = d.commit_list.iter().enumerate().map(|(i, c)| {
            let sha = c.oid.clone();
            let short = c.short.clone();
            let open = format!("{url}/commits/{}", c.oid);
            let first_body_line = c.body.lines().find(|l| !l.trim().is_empty()).map(|l| l.to_string());
            div()
                .id(("commit", i))
                .flex()
                .items_start()
                .gap(px(10.))
                .px(px(14.))
                .py(px(10.))
                .border_t_1()
                .border_color(rgb(BORDER))
                .cursor_pointer()
                .hover(|s| s.bg(rgb(BG_HOVER)))
                .on_click(cx.listener(move |this, _, _, cx| this.open_commit(Some(sha.clone()), cx)))
                .child(div().pt(px(2.)).child(icon("git-commit-horizontal", 15., DIM)))
                .child(
                    div()
                        .flex_1()
                        .min_w(px(0.))
                        .flex()
                        .flex_col()
                        .gap(px(4.))
                        .child(div().truncate().text_color(rgb(TEXT)).font_weight(FontWeight::MEDIUM).child(c.headline.clone()))
                        .when_some(first_body_line, |d, l| d.child(div().truncate().text_size(px(12.)).text_color(rgb(DIM)).child(l)))
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .gap(px(6.))
                                .text_size(px(12.))
                                .text_color(rgb(SOFT))
                                .child(self.images.face(&c.author, 16.))
                                .child(div().text_color(rgb(TEXT_SOFT)).child(c.author.clone()))
                                .child(format!("committed {} ago", ago(&c.at)))
                                .when(c.additions + c.deletions > 0, |d| {
                                    d.child(div().text_color(rgb(GREEN)).child(format!("+{}", c.additions)))
                                        .child(div().text_color(rgb(RED)).child(format!("−{}", c.deletions)))
                                }),
                        ),
                )
                .children(ci(&c.check_state).map(|(g, col, _)| div().pt(px(2.)).text_color(rgb(col)).child(g)))
                .child(sha_chip(("sha", i), short, c.oid.clone(), cx))
                .child(
                    div()
                        .id(("commit-gh", i))
                        .pt(px(2.))
                        .cursor_pointer()
                        .on_click(cx.listener(move |_, _, _, cx| {
                            cx.stop_propagation();
                            cx.open_url(&open);
                        }))
                        .child(icon("external-link", 13., DIM)),
                )
        });
        card()
            .child(
                card_head()
                    .child(icon("git-commit-horizontal", 14., SOFT))
                    .child(div().text_color(rgb(TEXT)).child(format!("{} commit{}", d.commits, if d.commits == 1 { "" } else { "s" })))
                    .child(div().text_color(rgb(DIM)).child("· click one to see what it changed")),
            )
            .children(rows)
            .when(more > 0, |c| {
                c.child(
                    div()
                        .px(px(14.))
                        .py(px(10.))
                        .border_t_1()
                        .border_color(rgb(BORDER))
                        .text_color(rgb(DIM))
                        .child(format!("{more} earlier commits are not shown")),
                )
            })
            .into_any_element()
    }

    fn commit_view(&self, d: &Detail, i: usize, cx: &mut Context<Self>) -> AnyElement {
        let c = &d.commit_list[i];
        let key = commit_key(&d.pr.repo, &c.oid);
        let open = format!("{}/commits/{}", d.pr.url, c.oid);
        let prev = i.checked_sub(1).and_then(|j| d.commit_list.get(j)).map(|c| c.oid.clone());
        let next = d.commit_list.get(i + 1).map(|c| c.oid.clone());
        let nav = |id: &'static str, label: &'static str, to: Option<String>, cx: &mut Context<Self>| {
            let on = to.is_some();
            div()
                .id(id)
                .h(px(26.))
                .px(px(8.))
                .flex()
                .items_center()
                .text_size(px(12.))
                .border_1()
                .border_color(rgb(BORDER_3))
                .text_color(rgb(if on { TEXT } else { DIM }))
                .when_some(to, |b, sha| {
                    b.cursor_pointer().hover(|s| s.bg(rgb(BG_HOVER))).on_click(cx.listener(move |this, _, _, cx| this.open_commit(Some(sha.clone()), cx)))
                })
                .child(label)
        };
        let (message, files): (String, Option<Arc<Vec<ParsedFile>>>) = match self.diffs.get(&key) {
            Some(DiffState::Commit(m, f)) => (m.message.clone(), Some(f.clone())),
            _ => (c.headline.clone(), None),
        };
        let body = message.split_once('\n').map(|(_, b)| b.trim().to_string()).filter(|b| !b.is_empty());

        let head = card()
            .child(
                card_head()
                    .child(
                        div()
                            .id("commits-back")
                            .flex()
                            .items_center()
                            .gap(px(4.))
                            .text_color(rgb(SOFT))
                            .cursor_pointer()
                            .hover(|s| s.text_color(rgb(TEXT)))
                            .on_click(cx.listener(|this, _, _, cx| this.open_commit(None, cx)))
                            .child(icon("chevron-left", 13., SOFT))
                            .child("All commits"),
                    )
                    .child(div().text_color(rgb(DIM)).child(format!("{} of {}", i + 1, d.commit_list.len())))
                    .child(div().flex_1())
                    .child(nav("commit-prev", "‹ Previous", prev, cx))
                    .child(nav("commit-next", "Next ›", next, cx)),
            )
            .child(
                div()
                    .px(px(16.))
                    .py(px(12.))
                    .flex()
                    .flex_col()
                    .gap(px(8.))
                    .child(
                        div()
                            .flex()
                            .items_start()
                            .gap(px(10.))
                            .child(div().flex_1().text_size(px(15.)).font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT_BRIGHT)).child(c.headline.clone()))
                            .child(sha_chip("commit-sha", c.short.clone(), c.oid.clone(), cx))
                            .child(
                                button_look("commit-github")
                                    .h(px(26.))
                                    .text_size(px(12.))
                                    .on_click(cx.listener(move |_, _, _, cx| cx.open_url(&open)))
                                    .child("GitHub")
                                    .child(icon("external-link", 12., SOFT)),
                            ),
                    )
                    .when_some(body, |d, b| {
                        d.child(div().font_family(MONO).text_size(px(12.)).text_color(rgb(SOFT)).children(b.lines().map(|l| div().child(l.to_string()))))
                    })
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap(px(6.))
                            .text_size(px(12.))
                            .text_color(rgb(SOFT))
                            .child(self.images.face(&c.author, 18.))
                            .child(div().text_color(rgb(TEXT_SOFT)).child(c.author.clone()))
                            .child(format!("committed {} ago", ago(&c.at)))
                            .when_some(files.as_ref(), |d, f| {
                                let (a, del) = f.iter().fold((0, 0), |(a, d), p| (a + p.file.additions, d + p.file.deletions));
                                d.child(div().text_color(rgb(GREEN)).child(format!("+{a}")))
                                    .child(div().text_color(rgb(RED)).child(format!("−{del}")))
                                    .child(format!("in {} file{}", f.len(), if f.len() == 1 { "" } else { "s" }))
                            }),
                    ),
            );

        let diffs = match self.diffs.get(&key) {
            Some(DiffState::Commit(_, files)) => self.file_diffs(&key, files, None, cx),
            Some(DiffState::Failed(e)) => div().text_color(rgb(RED)).child(e.clone()).into_any_element(),
            _ => div().text_color(rgb(DIM)).child("Loading the changes…").into_any_element(),
        };
        div().flex().flex_col().gap(px(14.)).child(head).child(diffs).into_any_element()
    }

    pub(super) fn files_view(&self, d: &Detail, cx: &mut Context<Self>) -> AnyElement {
        let key = pr_key(&d.pr);
        let editor = self.hello.editor.clone();
        let head = card_head()
            .child(icon("file-diff", 14., SOFT))
            .child(div().text_color(rgb(TEXT)).child(format!("{} files changed", d.changed_files)))
            .child(div().text_color(rgb(GREEN)).child(format!("+{}", d.additions)))
            .child(div().text_color(rgb(RED)).child(format!("−{}", d.deletions)))
            .child(div().flex_1());
        let (head, body) = match self.diffs.get(&key) {
            Some(DiffState::Files(files)) => {
                let paths: Vec<String> = files.iter().map(|f| f.file.path.clone()).collect();
                let (k1, k2, p2) = (key.clone(), key.clone(), paths.clone());
                (
                    head.child(small_button("expand-all", "Expand all", cx.listener(move |this, _, _, cx| this.set_all_files(&k1, paths.clone(), true, cx))))
                        .child(small_button("collapse-all", "Collapse all", cx.listener(move |this, _, _, cx| this.set_all_files(&k2, p2.clone(), false, cx)))),
                    self.file_diffs(&key, files, Some(d), cx),
                )
            }
            Some(DiffState::Failed(e)) => (head, div().p(px(12.)).text_color(rgb(RED)).child(e.clone()).into_any_element()),
            _ => (head, div().p(px(12.)).text_color(rgb(DIM)).child("Loading the changes…").into_any_element()),
        };
        let more = d.changed_files.saturating_sub(match self.diffs.get(&key) {
            Some(DiffState::Files(f)) => f.len() as u64,
            _ => d.changed_files,
        });
        let pending = self.pending_count(&d.pr.key());
        div()
            .flex()
            .flex_col()
            .gap(px(14.))
            .when(pending > 0, |c| {
                c.child(
                    div()
                        .flex()
                        .items_center()
                        .gap(px(10.))
                        .px(px(12.))
                        .py(px(8.))
                        .border_1()
                        .border_color(rgb(0x5a4a26))
                        .bg(rgb(0x2a2418))
                        .text_color(rgb(YELLOW))
                        .child(format!("Review in progress: {pending} pending comment{}", if pending == 1 { "" } else { "s" }))
                        .child(div().flex_1())
                        .child(small_button("finish-review", "Finish your review ↓", cx.listener(|this, _, window, cx| this.focus_review_box(window, cx)))),
                )
            })
            .child(
                card().child(head.child(small_button(
                    "files-open",
                    &format!("Open in {editor}"),
                    cx.listener(|this, _, window, cx| {
                        if let Some(c) = this.find_cmd(|c| c.action == "open" && !c.worktree) {
                            this.start(c, window, cx);
                        }
                    }),
                ))),
            )
            .child(div().text_size(px(12.)).text_color(rgb(DIM)).child("Click a line number to comment on that line."))
            .child(body)
            .when(more > 0, |c| c.child(div().text_color(rgb(DIM)).child(format!("{more} more files are not shown; open the branch in {editor} for all of them."))))
            .child(self.composer(d, cx))
            .into_any_element()
    }

    /// Every file of a diff, each with a header that opens and closes it.
    /// `review` makes line numbers clickable and shows comments inline (Files changed).
    fn file_diffs(&self, key: &str, files: &Arc<Vec<ParsedFile>>, review: Option<&Detail>, cx: &mut Context<Self>) -> AnyElement {
        let mut budget = OPEN_BUDGET;
        let mut cards = Vec::with_capacity(files.len());
        for (i, f) in files.iter().enumerate() {
            let id = format!("{key}|{}", f.file.path);
            let fits = f.lines.len() <= budget && f.lines.len() <= FILE_LIMIT / 2;
            let open = self.file_open.get(&id).copied().unwrap_or(fits);
            if open {
                budget = budget.saturating_sub(f.lines.len());
            }
            cards.push(self.file_diff(i, id, f, open, review, cx));
        }
        div().flex().flex_col().gap(px(10.)).children(cards).into_any_element()
    }

    fn file_diff(&self, i: usize, id: String, f: &ParsedFile, open: bool, review: Option<&Detail>, cx: &mut Context<Self>) -> AnyElement {
        let (badge, color) = match f.file.status.as_str() {
            "added" => ("A", GREEN),
            "removed" => ("D", RED),
            "renamed" => ("R", BLUE),
            "copied" => ("C", BLUE),
            _ => ("M", YELLOW),
        };
        let (dir, base) = match f.file.path.rsplit_once('/') {
            Some((d, b)) => (format!("{d}/"), b.to_string()),
            None => (String::new(), f.file.path.clone()),
        };
        let header = div()
            .id(("file-head", i))
            .h(px(34.))
            .px(px(10.))
            .flex()
            .items_center()
            .gap(px(8.))
            .bg(rgb(BG_HEAD))
            .when(open, |d| d.border_b_1().border_color(rgb(BORDER_2)))
            .cursor_pointer()
            .hover(|s| s.bg(rgb(BG_HOVER)))
            .on_click(cx.listener(move |this, _, _, cx| this.toggle_file(id.clone(), open, cx)))
            .child(icon(if open { "chevron-down" } else { "chevron-right" }, 13., SOFT))
            .child(
                div()
                    .size(px(18.))
                    .flex_none()
                    .flex()
                    .items_center()
                    .justify_center()
                    .border_1()
                    .border_color(rgb(color))
                    .text_color(rgb(color))
                    .text_size(px(10.5))
                    .font_family(MONO)
                    .child(badge),
            )
            .child(
                div()
                    .flex_1()
                    .min_w(px(0.))
                    .truncate()
                    .font_family(MONO)
                    .text_size(px(12.5))
                    .child(
                        div()
                            .flex()
                            .when(!f.file.previous_path.is_empty(), |d| d.child(div().text_color(rgb(DIM)).child(format!("{} → ", f.file.previous_path))))
                            .child(div().text_color(rgb(DIM)).child(dir))
                            .child(div().text_color(rgb(TEXT)).child(base)),
                    ),
            )
            .child(div().text_size(px(12.)).text_color(rgb(GREEN)).child(format!("+{}", f.file.additions)))
            .child(div().text_size(px(12.)).text_color(rgb(RED)).child(format!("−{}", f.file.deletions)));

        let body = if !open {
            None
        } else if f.lines.is_empty() {
            Some(
                div()
                    .px(px(12.))
                    .py(px(10.))
                    .text_size(px(12.))
                    .text_color(rgb(DIM))
                    .child(if f.file.status == "renamed" && f.file.additions + f.file.deletions == 0 {
                        "Renamed without changes."
                    } else {
                        "No diff to show: a binary file, or too large for GitHub to send. Open it in your editor."
                    })
                    .into_any_element(),
            )
        } else {
            let shown = f.lines.len().min(FILE_LIMIT);
            let mut cards = review.map(|d| self.inline_cards(d, &f.file.path, &f.lines[..shown], cx)).unwrap_or_default();
            let click = review.map(|d| Click { deck: cx.entity().downgrade(), pr: d.pr.key(), path: f.file.path.clone(), file: i });
            let mut rows: Vec<AnyElement> = diff_blocks(&f.lines[..shown], &mut cards, click.as_ref());
            if f.lines.len() > shown {
                rows.push(
                    div()
                        .px(px(12.))
                        .py(px(8.))
                        .text_size(px(12.))
                        .text_color(rgb(DIM))
                        .child(format!("{} more lines; open the file in your editor to see them.", f.lines.len() - shown))
                        .into_any_element(),
                );
            }
            Some(
                div()
                    .id(SharedString::from(format!("diff-body-{i}-{}", f.file.path)))
                    .overflow_x_scroll()
                    .py(px(2.))
                    .child(div().flex().flex_col().children(rows))
                    .into_any_element(),
            )
        };
        card().child(header).children(body).into_any_element()
    }
}

/// Clicking a line number opens a comment box on that line.
#[derive(Clone)]
struct Click {
    deck: WeakEntity<Deck>,
    pr: String,
    path: String,
    file: usize,
}

/// Lines in blocks: each run of lines of one kind is a single text element with its
/// line numbers inside it, so a long diff costs a few dozen elements, not thousands.
/// A run ends at a line with comments under it, and the comment cards follow it.
fn diff_blocks(lines: &[Line], cards: &mut std::collections::HashMap<usize, Vec<AnyElement>>, click: Option<&Click>) -> Vec<AnyElement> {
    let mut out = Vec::new();
    let mut i = 0;
    while i < lines.len() {
        let kind = lines[i].kind;
        let mut j = i + 1;
        while j < lines.len() && lines[j].kind == kind && kind != Kind::Hunk && !cards.contains_key(&(j - 1)) {
            j += 1;
        }
        out.push(diff_block(kind, i, &lines[i..j], click));
        if let Some(c) = cards.remove(&(j - 1)) {
            out.extend(c);
        }
        i = j;
    }
    out
}

fn diff_block(kind: Kind, start: usize, lines: &[Line], click: Option<&Click>) -> AnyElement {
    let (bg, fg, mark) = match kind {
        Kind::Hunk => (0x1f2633, BLUE, ""),
        Kind::Add => (0x1d2f22, 0xb5dca8, "+"),
        Kind::Del => (0x36202a, 0xf2b0b0, "−"),
        Kind::Note => (BG_SIDE, DIM, ""),
        Kind::Context => (BG_SIDE, TEXT_SOFT, " "),
    };
    let num = |n: Option<u32>| n.map(|n| format!("{n:>5}")).unwrap_or_else(|| "     ".to_string());
    let mut text = String::new();
    let mut dim: Vec<(std::ops::Range<usize>, HighlightStyle)> = Vec::new();
    let mut ranges: Vec<std::ops::Range<usize>> = Vec::new();
    let mut targets: Vec<(u32, &'static str)> = Vec::new();
    let number_style = HighlightStyle { color: Some(rgb(0x5d636e).into()), ..Default::default() };
    for (k, l) in lines.iter().enumerate() {
        if k > 0 {
            text.push('\n');
        }
        let begin = text.len();
        match kind {
            Kind::Hunk | Kind::Note => text.push_str(&format!("{:12}{}", "", l.text)),
            _ => text.push_str(&format!("{} {}", num(l.old), num(l.new))),
        }
        if !matches!(kind, Kind::Hunk | Kind::Note) {
            dim.push((begin..text.len(), number_style));
            if let Some((side, n)) = inline::anchor(l) {
                ranges.push(begin..text.len());
                targets.push((n, side));
            }
            text.push_str(&format!(" {mark} {}", l.text));
        }
    }
    let styled = StyledText::new(text).with_highlights(dim);
    let content = match click {
        Some(c) if !ranges.is_empty() => {
            let c = c.clone();
            InteractiveText::new(ElementId::Name(format!("dl-{}-{start}", c.file).into()), styled)
                .on_click(ranges, move |ix, window, cx| {
                    let (line, side) = targets[ix];
                    let (pr, path) = (c.pr.clone(), c.path.clone());
                    let _ = c.deck.update(cx, |this, cx| this.open_line_editor(pr, path, line, side, window, cx));
                })
                .into_any_element()
        }
        _ => styled.into_any_element(),
    };
    div()
        .w_full()
        .bg(rgb(bg))
        .text_color(rgb(fg))
        .font_family(MONO)
        .text_size(px(12.))
        .line_height(px(19.))
        .whitespace_nowrap()
        .when(kind == Kind::Hunk, |d| d.py(px(2.)))
        .child(content)
        .into_any_element()
}

/// A short commit hash that copies the full one on click.
fn sha_chip(id: impl Into<ElementId>, short: String, full: String, cx: &mut Context<Deck>) -> impl IntoElement {
    div()
        .id(id)
        .px(px(6.))
        .border_1()
        .border_color(rgb(BORDER_3))
        .font_family(MONO)
        .text_size(px(11.5))
        .text_color(rgb(BLUE_LIGHT))
        .cursor_pointer()
        .hover(|s| s.bg(rgb(BG_CODE)))
        .on_click(cx.listener(move |this, _, _, cx| {
            cx.stop_propagation();
            cx.write_to_clipboard(ClipboardItem::new_string(full.clone()));
            this.notice = Some(format!("Copied {full}"));
            cx.notify();
        }))
        .child(short)
}

fn small_button(id: &'static str, label: &str, on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static) -> impl IntoElement {
    div()
        .id(id)
        .px(px(8.))
        .h(px(24.))
        .flex()
        .items_center()
        .border_1()
        .border_color(rgb(BORDER_3))
        .text_color(rgb(TEXT))
        .cursor_pointer()
        .hover(|s| s.bg(rgb(BG_HOVER)))
        .on_click(on_click)
        .child(label.to_string())
}
