//! Inline review: click a line number in Files changed to comment on that line, or
//! drag across line numbers (or click one and Shift-click another) to comment on
//! several. "Add single comment" posts at once; "Start a review" keeps the comment
//! pending under its lines until the review is submitted from the review box with
//! Comment, Approve or Request changes. Existing review threads show at their lines.

use std::collections::HashMap;

use super::*;
use crate::diff::{Kind, Line};
use gpui_kit::component::input::{Textarea, TextareaState};

pub(super) struct LineEditor {
    pr: String,
    path: String,
    /// The selected lines, as indices into the file's diff, and the first one clicked.
    from: usize,
    to: usize,
    anchor: usize,
    /// Where the comment ends (GitHub's line and side) and, for several lines, starts.
    line: u32,
    side: &'static str,
    start: Option<(&'static str, u32)>,
    /// Editing a pending comment (its index) instead of writing a new one.
    editing: Option<usize>,
    pub(super) input: Entity<TextareaState>,
    posting: bool,
    error: Option<String>,
    _sub: Subscription,
}

/// Lines being dragged over, before the comment box opens.
pub(super) struct LineDrag {
    pr: String,
    path: String,
    from: usize,
    to: usize,
}

/// Where a diff line can take a comment: removed lines by their old number on the
/// LEFT, added and unchanged lines by their new number on the RIGHT.
pub(super) fn anchor(l: &Line) -> Option<(&'static str, u32)> {
    match l.kind {
        Kind::Del => l.old.map(|n| ("LEFT", n)),
        Kind::Add | Kind::Context => l.new.map(|n| ("RIGHT", n)),
        _ => None,
    }
}

/// The first and last content line of the hunk holding `ix`; GitHub only takes
/// comments on several lines within one hunk.
fn hunk_bounds(lines: &[Line], ix: usize) -> (usize, usize) {
    let mut a = ix;
    while a > 0 && lines[a].kind != Kind::Hunk {
        a -= 1;
    }
    if lines[a].kind == Kind::Hunk {
        a += 1;
    }
    let mut b = ix;
    while b + 1 < lines.len() && lines[b + 1].kind != Kind::Hunk {
        b += 1;
    }
    (a, b.max(a))
}

/// `from..=to` sorted and kept inside the hunk of the line `fixed` (the first click).
fn span(lines: &[Line], fixed: usize, other: usize) -> (usize, usize) {
    let (a, b) = hunk_bounds(lines, fixed);
    let other = other.clamp(a, b);
    (fixed.min(other), fixed.max(other))
}

impl Deck {
    /// The diff lines of one file of a PR, once loaded.
    fn file_lines(&self, pr: &str, path: &str) -> Option<&[Line]> {
        match self.diffs.get(&format!("pr:{pr}")) {
            Some(DiffState::Files(files)) => files.iter().find(|f| f.file.path == path).map(|f| f.lines.as_slice()),
            _ => None,
        }
    }

    /// The lines to highlight in a file: being dragged over, or under the open box.
    pub(super) fn selection(&self, pr: &str, path: &str) -> Option<(usize, usize)> {
        if let Some(d) = self.drag.as_ref().filter(|d| d.pr == pr && d.path == path) {
            return Some((d.from.min(d.to), d.from.max(d.to)));
        }
        self.line_editor.as_ref().filter(|e| e.pr == pr && e.path == path).map(|e| (e.from, e.to))
    }

    /// A press on a line number: Shift extends the open comment box to this line;
    /// otherwise a selection starts here.
    pub(super) fn line_press(&mut self, pr: String, path: String, ix: usize, shift: bool, window: &mut Window, cx: &mut Context<Self>) {
        if shift {
            if let Some(ed) = self.line_editor.as_ref().filter(|e| e.pr == pr && e.path == path && e.editing.is_none()) {
                let anchor = ed.anchor;
                if let Some((from, to)) = self.file_lines(&pr, &path).map(|lines| span(lines, anchor, ix)) {
                    self.set_editor_range(from, to, cx);
                    return;
                }
            }
        }
        self.drag = Some(LineDrag { pr, path, from: ix, to: ix });
        let _ = window;
        cx.notify();
    }

    pub(super) fn line_drag(&mut self, pr: String, path: String, ix: usize, cx: &mut Context<Self>) {
        let Some(d) = self.drag.as_ref().filter(|d| d.pr == pr && d.path == path) else { return };
        let Some((from, to)) = self.file_lines(&pr, &path).map(|lines| span(lines, d.from, ix)) else { return };
        let to = if ix < d.from { from } else { to };
        if let Some(d) = self.drag.as_mut() {
            if d.to != to {
                d.to = to;
                cx.notify();
            }
        }
    }

    /// The mouse came up: open the comment box on the dragged lines.
    pub(super) fn line_release(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(d) = self.drag.take() else { return };
        self.open_range(d.pr, d.path, d.from, d.to, None, String::new(), window, cx);
    }

    /// Opens the box on one line, by its number and side (screenshot tests).
    #[cfg(feature = "snapshot")]
    pub(super) fn open_line_editor(&mut self, pr: String, path: String, line: u32, side: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        let ix = self.file_lines(&pr, &path).and_then(|lines| lines.iter().position(|l| anchor(l) == Some((side, line))));
        if let Some(ix) = ix {
            self.open_range(pr, path, ix, ix, None, String::new(), window, cx);
        }
    }

    /// Opens the comment box on lines `fixed..other` of a file (indices into its diff).
    #[allow(clippy::too_many_arguments)]
    fn open_range(
        &mut self,
        pr: String,
        path: String,
        fixed: usize,
        other: usize,
        editing: Option<usize>,
        text: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(lines) = self.file_lines(&pr, &path) else { return };
        let (from, to) = span(lines, fixed, other);
        let Some((side, line, start)) = ends(lines, from, to) else { return };
        let input = cx.new(|cx| TextareaState::new(window, cx).auto_grow(3, 12).placeholder("Leave a comment. Markdown works."));
        if !text.is_empty() {
            input.update(cx, |s, cx| s.set_value(text, window, cx));
        }
        let sub = cx.subscribe_in(&input, window, |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { secondary: true, .. } = ev {
                this.submit_line(false, window, cx);
            }
        });
        input.update(cx, |s, cx| s.focus(window, cx));
        self.line_editor = Some(LineEditor {
            pr,
            path,
            from,
            to,
            anchor: fixed,
            line,
            side,
            start,
            editing,
            input,
            posting: false,
            error: None,
            _sub: sub,
        });
        cx.notify();
    }

    fn set_editor_range(&mut self, from: usize, to: usize, cx: &mut Context<Self>) {
        let Some(ed) = self.line_editor.as_ref() else { return };
        let Some(found) = self.file_lines(&ed.pr, &ed.path).and_then(|lines| ends(lines, from, to)) else { return };
        if let Some(ed) = self.line_editor.as_mut() {
            (ed.side, ed.line, ed.start) = found;
            ed.from = from;
            ed.to = to;
        }
        cx.notify();
    }

    pub(super) fn close_line_editor(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.line_editor = None;
        self.focus.focus(window, cx);
        cx.notify();
    }

    /// The selected code, without diff markers.
    fn selected_code(&self, new_only: bool) -> Option<String> {
        let ed = self.line_editor.as_ref()?;
        let lines = self.file_lines(&ed.pr, &ed.path)?;
        let picked: Vec<&str> = lines[ed.from..=ed.to]
            .iter()
            .filter(|l| match l.kind {
                Kind::Add | Kind::Context => true,
                Kind::Del => !new_only,
                _ => false,
            })
            .map(|l| l.text.as_str())
            .collect();
        Some(picked.join("\n"))
    }

    #[cfg(feature = "snapshot")]
    pub(super) fn suggest_change_for_test(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.suggest_change(window, cx);
    }

    /// Adds a GitHub suggestion with the selected lines, ready to edit into the change.
    fn suggest_change(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(code) = self.selected_code(true) else { return };
        let Some(ed) = self.line_editor.as_ref() else { return };
        let mut text = ed.input.read(cx).value().to_string();
        if !text.is_empty() && !text.ends_with('\n') {
            text.push('\n');
        }
        text.push_str(&format!("```suggestion\n{code}\n```"));
        ed.input.update(cx, |s, cx| {
            s.set_value(text, window, cx);
            s.focus(window, cx);
        });
        cx.notify();
    }

    /// Adds the comment to the pending review, or (`single`) posts it on its own now.
    pub(super) fn submit_line(&mut self, single: bool, window: &mut Window, cx: &mut Context<Self>) {
        let Some(ed) = self.line_editor.as_mut() else { return };
        if ed.posting {
            return;
        }
        let body = ed.input.read(cx).value().trim().to_string();
        if body.is_empty() {
            ed.error = Some("Write a comment first.".into());
            cx.notify();
            return;
        }
        let draft = DraftThread {
            path: ed.path.clone(),
            line: ed.line,
            side: ed.side,
            start_line: ed.start.map(|s| s.1),
            start_side: ed.start.map(|s| s.0),
            body,
        };
        let pr = ed.pr.clone();
        if !single {
            let list = self.pending.entry(pr).or_default();
            match ed.editing {
                Some(i) if i < list.len() => list[i] = draft,
                _ => list.push(draft),
            }
            return self.close_line_editor(window, cx);
        }
        let (Some(rpc), Some(d)) = (self.rpc.clone(), self.details.get(&pr)) else { return };
        let call = rpc.call::<serde_json::Value>("review", json!({ "prId": d.node_id, "event": "COMMENT", "body": "", "threads": [draft] }));
        let pr_info = d.pr.clone();
        ed.posting = true;
        ed.error = None;
        cx.notify();
        cx.spawn_in(window, async move |this, cx| {
            let res = call.await;
            let _ = this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(_) => {
                        this.close_line_editor(window, cx);
                        this.say_done("Comment posted", cx);
                        this.refresh_detail(pr_info, cx);
                    }
                    Err(e) => {
                        if let Some(ed) = this.line_editor.as_mut() {
                            ed.posting = false;
                            ed.error = Some(e.message);
                        }
                    }
                }
                cx.notify();
            });
        })
        .detach();
    }

    pub(super) fn pending_count(&self, pr_key: &str) -> usize {
        self.pending.get(pr_key).map_or(0, |l| l.len())
    }

    /// Cards to show under lines of one file: review threads from GitHub, pending
    /// comments, and the comment box, by line index.
    pub(super) fn inline_cards(&self, d: &Detail, path: &str, lines: &[Line], cx: &mut Context<Self>) -> HashMap<usize, Vec<AnyElement>> {
        let pr = d.pr.key();
        let mut at: HashMap<(&str, u32), usize> = HashMap::new();
        for (i, l) in lines.iter().enumerate() {
            if let Some(a) = anchor(l) {
                at.insert(a, i);
            }
        }
        let mut out: HashMap<usize, Vec<AnyElement>> = HashMap::new();
        for e in d.timeline.iter().filter(|e| e.kind == "thread" && e.path == path && !e.outdated) {
            let side = if e.side == "LEFT" { "LEFT" } else { "RIGHT" };
            if let Some(&i) = at.get(&(side, e.line as u32)) {
                out.entry(i).or_default().push(self.inline_thread(&pr, e, cx));
            }
        }
        if let Some(list) = self.pending.get(&pr) {
            for (n, t) in list.iter().enumerate().filter(|(_, t)| t.path == path) {
                let editing = self.line_editor.as_ref().is_some_and(|ed| ed.pr == pr && ed.editing == Some(n));
                if let (Some(&i), false) = (at.get(&(t.side, t.line)), editing) {
                    out.entry(i).or_default().push(self.pending_card(&pr, n, t, cx));
                }
            }
        }
        if let Some(ed) = self.line_editor.as_ref().filter(|ed| ed.pr == pr && ed.path == path) {
            if let Some(&i) = at.get(&(ed.side, ed.line)) {
                out.entry(i).or_default().push(self.line_editor_view(ed, cx));
            }
        }
        out
    }

    fn inline_box() -> Div {
        div().mx(px(10.)).my(px(6.)).flex().flex_col().border_1().border_color(rgb(BORDER_2)).bg(rgb(BG)).font_family(".SystemUIFont").whitespace_normal()
    }

    fn inline_thread(&self, pr: &str, e: &Event, cx: &mut Context<Self>) -> AnyElement {
        let mut body = div().px(px(12.)).py(px(8.)).flex().flex_col().gap(px(8.)).text_size(px(13.)).text_color(rgb(TEXT_BODY)).child(md_body(
            format!("inline-{}-0", e.thread_id),
            &e.body,
            &self.images,
        ));
        for (j, r) in e.replies.iter().enumerate() {
            body = body.child(
                div()
                    .pt(px(8.))
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
                            .child(self.images.face(&r.author, 18.))
                            .child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT)).child(r.author.clone()))
                            .child(div().text_color(rgb(DIM)).child(format!("{} ago", ago(&r.at)))),
                    )
                    .child(md_body(format!("inline-{}-{}", e.thread_id, j + 1), &r.body, &self.images)),
            );
        }
        if let Some(reply) = self.thread_reply_view(pr, e, cx) {
            body = body.child(reply);
        }
        Self::inline_box()
            .child(
                div()
                    .h(px(32.))
                    .px(px(12.))
                    .flex()
                    .items_center()
                    .gap(px(8.))
                    .bg(rgb(BG_HEAD))
                    .border_b_1()
                    .border_color(rgb(BORDER_2))
                    .text_size(px(12.))
                    .text_color(rgb(SOFT))
                    .child(self.images.face(&e.author, 20.))
                    .child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT)).child(e.author.clone()))
                    .child(div().text_color(rgb(DIM)).child(format!("{} ago", ago(&e.at))))
                    .when(e.start_line > 0, |d| {
                        let s = if e.start_side == "LEFT" { "LEFT" } else { "RIGHT" };
                        let side = if e.side == "LEFT" { "LEFT" } else { "RIGHT" };
                        d.child(div().text_color(rgb(DIM)).child(format!("lines {}", lines_label(s, e.start_line as u32, side, e.line as u32))))
                    })
                    .when(e.resolved, |d| d.child(div().ml_auto().text_color(rgb(GREEN)).child("resolved"))),
            )
            .child(body)
            .into_any_element()
    }

    fn pending_card(&self, pr: &str, n: usize, t: &DraftThread, cx: &mut Context<Self>) -> AnyElement {
        let (pr_e, pr_d) = (pr.to_string(), pr.to_string());
        let t2 = t.clone();
        Self::inline_box()
            .border_color(rgb(0x5a4a26))
            .child(
                div()
                    .h(px(32.))
                    .px(px(12.))
                    .flex()
                    .items_center()
                    .gap(px(8.))
                    .bg(rgb(BG_HEAD))
                    .border_b_1()
                    .border_color(rgb(BORDER_2))
                    .text_size(px(12.))
                    .text_color(rgb(SOFT))
                    .child(self.images.face(&self.hello.viewer, 20.))
                    .child(div().font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT)).child(if self.hello.viewer.is_empty() { "you".to_string() } else { self.hello.viewer.clone() }))
                    .child(div().px(px(6.)).border_1().border_color(rgb(0x5a4a26)).text_color(rgb(YELLOW)).text_size(px(11.)).child("Pending"))
                    .when_some(t.start_line.zip(t.start_side), |d, (a, s)| d.child(div().text_color(rgb(DIM)).child(format!("lines {}", lines_label(s, a, t.side, t.line)))))
                    .child(div().flex_1())
                    .child(
                        div()
                            .id(SharedString::from(format!("pending-edit-{n}")))
                            .cursor_pointer()
                            .hover(|s| s.text_color(rgb(TEXT)))
                            .on_click(cx.listener(move |this, _, window, cx| {
                                let found = this.file_lines(&pr_e, &t2.path).and_then(|lines| {
                                    let end = lines.iter().position(|l| anchor(l) == Some((t2.side, t2.line)))?;
                                    let begin = match (t2.start_side, t2.start_line) {
                                        (Some(s), Some(n)) => lines.iter().position(|l| anchor(l) == Some((s, n))).unwrap_or(end),
                                        _ => end,
                                    };
                                    Some((begin, end))
                                });
                                if let Some((begin, end)) = found {
                                    this.open_range(pr_e.clone(), t2.path.clone(), begin, end, Some(n), t2.body.clone(), window, cx);
                                }
                            }))
                            .child("Edit"),
                    )
                    .child(
                        div()
                            .id(SharedString::from(format!("pending-delete-{n}")))
                            .cursor_pointer()
                            .hover(|s| s.text_color(rgb(RED)))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                if let Some(list) = this.pending.get_mut(&pr_d) {
                                    if n < list.len() {
                                        list.remove(n);
                                    }
                                }
                                cx.notify();
                            }))
                            .child("Delete"),
                    ),
            )
            .child(div().px(px(12.)).py(px(8.)).text_size(px(13.)).text_color(rgb(TEXT_BODY)).child(md_body(
                format!("pending-{pr}-{n}-{}", t.body.len()),
                &t.body,
                &self.images,
            )))
            .into_any_element()
    }

    fn line_editor_view(&self, ed: &LineEditor, cx: &mut Context<Self>) -> AnyElement {
        let pending = self.pending_count(&ed.pr);
        let what = match ed.start {
            Some((s, a)) => format!("Comment on lines {} of {}", lines_label(s, a, ed.side, ed.line), ed.path),
            None if ed.side == "LEFT" => format!("Comment on removed line {} of {}", ed.line, ed.path),
            None => format!("Comment on line {} of {}", ed.line, ed.path),
        };
        let can_suggest = ed.side == "RIGHT" && ed.start.is_none_or(|s| s.0 == "RIGHT");
        Self::inline_box()
            .border_color(rgb(RING_ACTIVE))
            .p(px(8.))
            .gap(px(8.))
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap(px(8.))
                    .text_size(px(12.))
                    .text_color(rgb(SOFT))
                    .child(div().flex_1().min_w(px(0.)).truncate().child(what))
                    .child(div().text_color(rgb(DIM)).child("Shift-click a line to extend"))
                    .child(
                        button_look("copy-lines")
                            .h(px(24.))
                            .text_size(px(12.))
                            .on_click(cx.listener(|this, _, _, cx| {
                                if let Some(code) = this.selected_code(false) {
                                    cx.write_to_clipboard(ClipboardItem::new_string(code));
                                    this.say_done("Lines copied", cx);
                                }
                            }))
                            .child("Copy lines"),
                    )
                    .when(can_suggest, |d| {
                        d.child(
                            button_look("suggest")
                                .h(px(24.))
                                .text_size(px(12.))
                                .on_click(cx.listener(|this, _, window, cx| this.suggest_change(window, cx)))
                                .child("Suggest change"),
                        )
                    }),
            )
            .child(Textarea::new(&ed.input))
            .child(
                div()
                    .flex()
                    .flex_wrap()
                    .items_center()
                    .gap(px(8.))
                    .child(div().text_size(px(11.5)).text_color(rgb(DIM)).child(key_hint(if ed.editing.is_some() {
                        "⌘↵ saves"
                    } else {
                        "⌘↵ adds to your review"
                    })))
                    .when_some(ed.error.clone(), |d, e| d.child(div().text_size(px(12.)).text_color(rgb(RED)).child(e)))
                    .child(
                        div()
                            .ml_auto()
                            .flex()
                            .gap(px(8.))
                            .child(
                                button_look("line-cancel")
                                    .h(px(26.))
                                    .text_size(px(12.))
                                    .on_click(cx.listener(|this, _, window, cx| this.close_line_editor(window, cx)))
                                    .child("Cancel"),
                            )
                            .when(ed.editing.is_none() && pending == 0, |d| {
                                d.child(
                                    button_look("line-single")
                                        .h(px(26.))
                                        .text_size(px(12.))
                                        .on_click(cx.listener(|this, _, window, cx| this.submit_line(true, window, cx)))
                                        .child(if ed.posting { "Posting…" } else { "Add single comment" }),
                                )
                            })
                            .child(
                                primary_look("line-review")
                                    .h(px(26.))
                                    .text_size(px(12.))
                                    .on_click(cx.listener(|this, _, window, cx| this.submit_line(false, window, cx)))
                                    .child(if ed.editing.is_some() {
                                        "Save"
                                    } else if pending == 0 {
                                        "Start a review"
                                    } else {
                                        "Add review comment"
                                    }),
                            ),
                    ),
            )
            .into_any_element()
    }
}

/// Where a comment on lines `from..=to` ends (side and line) and, for several lines,
/// starts. Hunk headers and notes inside the range are skipped.
fn ends(lines: &[Line], from: usize, to: usize) -> Option<(&'static str, u32, Option<(&'static str, u32)>)> {
    let last = (from..=to).rev().find_map(|i| anchor(&lines[i]))?;
    let first = (from..=to).find_map(|i| anchor(&lines[i]))?;
    let start = (first != last).then_some(first);
    Some((last.0, last.1, start))
}

/// "13–15", or "−14 to +14" when the range runs from removed lines into new ones.
fn lines_label(start_side: &str, start: u32, side: &str, end: u32) -> String {
    if start_side == side {
        format!("{start}–{end}")
    } else {
        let sign = |s: &str| if s == "LEFT" { "−" } else { "+" };
        format!("{}{start} to {}{end}", sign(start_side), sign(side))
    }
}
