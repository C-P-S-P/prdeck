//! Inline review: click a line number in Files changed to comment on that line.
//! "Add single comment" posts it at once; "Start a review" keeps it as pending, shown
//! under its line, until the review is submitted from the review box with Comment,
//! Approve or Request changes. Existing review threads show at their lines too.

use std::collections::HashMap;

use super::*;
use crate::diff::{Kind, Line};
use gpui_kit::component::input::{Textarea, TextareaState};

pub(super) struct LineEditor {
    pr: String,
    path: String,
    line: u32,
    side: &'static str,
    /// Editing a pending comment (its index) instead of writing a new one.
    editing: Option<usize>,
    pub(super) input: Entity<TextareaState>,
    posting: bool,
    error: Option<String>,
    _sub: Subscription,
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

impl Deck {
    pub(super) fn open_line_editor(
        &mut self,
        pr: String,
        path: String,
        line: u32,
        side: &'static str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.open_editor(pr, path, line, side, None, String::new(), window, cx);
    }

    #[allow(clippy::too_many_arguments)]
    fn open_editor(
        &mut self,
        pr: String,
        path: String,
        line: u32,
        side: &'static str,
        editing: Option<usize>,
        text: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let input = cx.new(|cx| {
            TextareaState::new(window, cx).auto_grow(3, 12).placeholder(format!("Comment on line {line}. Markdown works."))
        });
        if !text.is_empty() {
            input.update(cx, |s, cx| s.set_value(text, window, cx));
        }
        let sub = cx.subscribe_in(&input, window, |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { secondary: true, .. } = ev {
                this.submit_line(false, window, cx);
            }
        });
        input.update(cx, |s, cx| s.focus(window, cx));
        self.line_editor = Some(LineEditor { pr, path, line, side, editing, input, posting: false, error: None, _sub: sub });
        cx.notify();
    }

    pub(super) fn close_line_editor(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.line_editor = None;
        self.focus.focus(window, cx);
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
        let draft = DraftThread { path: ed.path.clone(), line: ed.line, side: ed.side, body };
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
                    .child(div().flex_1())
                    .child(
                        div()
                            .id(SharedString::from(format!("pending-edit-{n}")))
                            .cursor_pointer()
                            .hover(|s| s.text_color(rgb(TEXT)))
                            .on_click(cx.listener(move |this, _, window, cx| {
                                this.open_editor(pr_e.clone(), t2.path.clone(), t2.line, t2.side, Some(n), t2.body.clone(), window, cx)
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
        let side = if ed.side == "LEFT" { "removed line" } else { "line" };
        Self::inline_box()
            .border_color(rgb(RING_ACTIVE))
            .p(px(8.))
            .gap(px(8.))
            .child(div().text_size(px(12.)).text_color(rgb(SOFT)).child(format!("Comment on {side} {} of {}", ed.line, ed.path)))
            .child(Textarea::new(&ed.input))
            .child(
                div()
                    .flex()
                    .flex_wrap()
                    .items_center()
                    .gap(px(8.))
                    .child(div().text_size(px(11.5)).text_color(rgb(DIM)).child(if ed.editing.is_some() {
                        "⌘↵ saves"
                    } else {
                        "⌘↵ adds to your review"
                    }))
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
