//! Commenting on and reviewing a PR from its conversation: a reply box at the end
//! (Comment, Approve, Request changes, with a markdown preview) and Reply on each
//! code thread. Drafts are kept per PR, so switching PRs never loses text.

use super::*;
use gpui_kit::component::input::{Textarea, TextareaState};

pub(super) struct Draft {
    pub(super) input: Entity<TextareaState>,
    preview: bool,
    posting: Option<&'static str>,
    error: Option<String>,
    _sub: Subscription,
}

pub(super) struct ThreadReply {
    pr: String,
    thread: String,
    pub(super) input: Entity<TextareaState>,
    posting: bool,
    error: Option<String>,
    _sub: Subscription,
}

#[derive(Clone, Copy, PartialEq)]
pub(super) enum Post {
    Comment,
    Approve,
    RequestChanges,
}

impl Deck {
    /// Gives the selected PR a reply box, the first time it is shown.
    pub(super) fn ensure_draft(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(key) = self.selected.clone() else { return };
        if self.drafts.contains_key(&key) {
            return;
        }
        let input = cx.new(|cx| TextareaState::new(window, cx).auto_grow(4, 16).placeholder("Leave a comment. Markdown works."));
        let k = key.clone();
        let sub = cx.subscribe_in(&input, window, move |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { secondary: true, .. } = ev {
                this.post(&k, Post::Comment, window, cx);
            }
        });
        self.drafts.insert(key, Draft { input, preview: false, posting: None, error: None, _sub: sub });
    }

    /// Whether the signed-in user wrote this PR (GitHub refuses self-reviews).
    fn is_mine(&self, pr: &Pr) -> bool {
        !self.hello.viewer.is_empty() && self.hello.viewer.eq_ignore_ascii_case(&pr.author)
    }

    /// Jumps to the reply box of the selected PR (⌘⇧C, or the Review button). It is
    /// at the end of Conversation and of Files changed; elsewhere, Conversation opens.
    pub(super) fn focus_composer(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.section != Section::Files {
            self.section = Section::Conversation;
        }
        self.focus_review_box(window, cx);
    }

    pub(super) fn focus_review_box(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.ensure_draft(window, cx);
        let max = self.detail_scroll.max_offset().y;
        self.detail_scroll.set_offset(point(px(0.), -max - px(2000.)));
        if let Some(d) = self.selected.as_ref().and_then(|k| self.drafts.get_mut(k)) {
            d.preview = false;
            d.input.update(cx, |s, cx| s.focus(window, cx));
        }
        cx.notify();
    }

    pub(super) fn post(&mut self, key: &str, what: Post, window: &mut Window, cx: &mut Context<Self>) {
        let (Some(rpc), Some(d), Some(draft)) = (self.rpc.clone(), self.details.get(key), self.drafts.get_mut(key)) else {
            return;
        };
        if draft.posting.is_some() {
            return;
        }
        if what != Post::Comment && !self.hello.viewer.is_empty() && self.hello.viewer.eq_ignore_ascii_case(&d.pr.author) {
            draft.error = Some("GitHub doesn't let you approve or request changes on your own pull request.".into());
            cx.notify();
            return;
        }
        let body = draft.input.read(cx).value().trim().to_string();
        let threads = self.pending.get(key).cloned().unwrap_or_default();
        let (call, posting, done) = match what {
            Post::Comment if !threads.is_empty() => (
                rpc.call::<serde_json::Value>("review", json!({ "prId": d.node_id, "event": "COMMENT", "body": body, "threads": threads })),
                "Submitting…",
                "Review submitted",
            ),
            Post::Comment => {
                if body.is_empty() {
                    draft.error = Some("Write a comment first.".into());
                    cx.notify();
                    return;
                }
                (rpc.call::<serde_json::Value>("comment", json!({ "prId": d.node_id, "body": body })), "Commenting…", "Comment posted")
            }
            Post::Approve => (
                rpc.call::<serde_json::Value>("review", json!({ "prId": d.node_id, "event": "APPROVE", "body": body, "threads": threads })),
                "Approving…",
                "Approved",
            ),
            Post::RequestChanges => {
                if body.is_empty() {
                    draft.error = Some("Say what should change, then request changes.".into());
                    cx.notify();
                    return;
                }
                (
                    rpc.call::<serde_json::Value>("review", json!({ "prId": d.node_id, "event": "REQUEST_CHANGES", "body": body, "threads": threads })),
                    "Requesting changes…",
                    "Changes requested",
                )
            }
        };
        draft.posting = Some(posting);
        draft.error = None;
        let pr = d.pr.clone();
        let key = key.to_string();
        cx.notify();
        cx.spawn_in(window, async move |this, cx| {
            let res = call.await;
            let _ = this.update_in(cx, |this, window, cx| {
                let Some(draft) = this.drafts.get_mut(&key) else { return };
                draft.posting = None;
                match res {
                    Ok(_) => {
                        draft.input.update(cx, |s, cx| s.set_value("", window, cx));
                        draft.preview = false;
                        this.pending.remove(&key);
                        this.say_done(done, cx);
                        this.refresh_detail(pr, cx);
                    }
                    Err(e) => draft.error = Some(e.message),
                }
                cx.notify();
            });
        })
        .detach();
    }

    pub(super) fn start_reply(&mut self, pr: String, thread: String, window: &mut Window, cx: &mut Context<Self>) {
        let input = cx.new(|cx| TextareaState::new(window, cx).auto_grow(2, 10).placeholder("Reply…"));
        let sub = cx.subscribe_in(&input, window, |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { secondary: true, .. } = ev {
                this.send_reply(window, cx);
            }
        });
        input.update(cx, |s, cx| s.focus(window, cx));
        self.thread_reply = Some(ThreadReply { pr, thread, input, posting: false, error: None, _sub: sub });
        cx.notify();
    }

    pub(super) fn send_reply(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let (Some(rpc), Some(r)) = (self.rpc.clone(), self.thread_reply.as_mut()) else { return };
        if r.posting {
            return;
        }
        let body = r.input.read(cx).value().trim().to_string();
        if body.is_empty() {
            r.error = Some("Write a reply first.".into());
            cx.notify();
            return;
        }
        r.posting = true;
        r.error = None;
        let call = rpc.call::<serde_json::Value>("replyThread", json!({ "threadId": r.thread, "body": body }));
        let key = r.pr.clone();
        cx.notify();
        cx.spawn_in(window, async move |this, cx| {
            let res = call.await;
            let _ = this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(_) => {
                        this.thread_reply = None;
                        this.focus.focus(window, cx);
                        this.say_done("Reply posted", cx);
                        if let Some(pr) = this.details.get(&key).map(|d| d.pr.clone()) {
                            this.refresh_detail(pr, cx);
                        }
                    }
                    Err(e) => {
                        if let Some(r) = this.thread_reply.as_mut() {
                            r.posting = false;
                            r.error = Some(e.message);
                        }
                    }
                }
                cx.notify();
            });
        })
        .detach();
    }

    /// Loads a PR again from GitHub, after commenting on it.
    pub(super) fn refresh_detail(&mut self, pr: Pr, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let res = rpc.call::<Detail>("detail", json!({ "pr": pr }));
        cx.spawn(async move |this, cx| {
            if let Ok(d) = res.await {
                let _ = this.update(cx, |this, cx| {
                    this.details.insert(d.pr.key(), d);
                    cx.notify();
                });
            }
        })
        .detach();
    }

    /// A green note under the header that goes away after a few seconds.
    pub(super) fn say_done(&mut self, msg: &str, cx: &mut Context<Self>) {
        self.notice = Some(msg.to_string());
        let msg = msg.to_string();
        cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_secs(4)).await;
            let _ = this.update(cx, |this, cx| {
                if this.notice.as_deref() == Some(msg.as_str()) {
                    this.notice = None;
                    cx.notify();
                }
            });
        })
        .detach();
    }

    pub(super) fn typing_review(&self, window: &Window, cx: &App) -> bool {
        let draft = self.selected.as_ref().and_then(|k| self.drafts.get(k));
        draft.is_some_and(|d| d.input.read(cx).focus_handle(cx).is_focused(window))
            || self.line_editor.as_ref().is_some_and(|e| e.input.read(cx).focus_handle(cx).is_focused(window))
            || self.thread_reply.as_ref().is_some_and(|r| r.input.read(cx).focus_handle(cx).is_focused(window))
    }

    /// The reply box at the end of the conversation.
    pub(super) fn composer(&self, d: &Detail, cx: &mut Context<Self>) -> AnyElement {
        let key = d.pr.key();
        let Some(draft) = self.drafts.get(&key) else { return div().into_any_element() };
        let mine = self.is_mine(&d.pr);
        let busy = draft.posting.is_some();
        let viewer = if self.hello.viewer.is_empty() { "you".to_string() } else { format!("@{}", self.hello.viewer) };
        let text = draft.input.read(cx).value().to_string();

        let tab = |id: &'static str, label: &'static str, on: bool, preview: bool, cx: &mut Context<Self>| {
            let key = key.clone();
            div()
                .id(id)
                .px(px(8.))
                .h(px(24.))
                .flex()
                .items_center()
                .text_size(px(12.))
                .border_1()
                .border_color(rgb(if on { BORDER_3 } else { BG_HEAD }))
                .when(on, |d| d.bg(rgb(BG_SIDE)))
                .text_color(rgb(if on { TEXT } else { SOFT }))
                .cursor_pointer()
                .on_click(cx.listener(move |this, _, _, cx| {
                    if let Some(d) = this.drafts.get_mut(&key) {
                        d.preview = preview;
                    }
                    cx.notify();
                }))
                .child(label)
        };
        let action = |id: &'static str, label: String, color: u32, what: Post, enabled: bool, cx: &mut Context<Self>| {
            let key = key.clone();
            div()
                .id(id)
                .h(px(28.))
                .px(px(12.))
                .flex()
                .items_center()
                .text_size(px(12.))
                .border_1()
                .border_color(rgb(if enabled { BORDER_3 } else { BORDER }))
                .text_color(rgb(if enabled { color } else { DIM }))
                .when(enabled, |b| {
                    b.cursor_pointer().hover(|s| s.bg(rgb(BG_HOVER))).on_click(cx.listener(move |this, _, window, cx| {
                        this.post(&key, what, window, cx)
                    }))
                })
                .child(label)
        };
        let pending = self.pending_count(&key);
        let label = |what: Post, idle: &str| -> String {
            match draft.posting {
                Some(p) if busy && matches!((what, p), (Post::Comment, "Commenting…" | "Submitting…") | (Post::Approve, "Approving…") | (Post::RequestChanges, "Requesting changes…")) => p.to_string(),
                _ => idle.to_string(),
            }
        };
        let comment_key = key.clone();

        card()
            .id("composer")
            .child(
                card_head()
                    .child(self.images.face(&self.hello.viewer, 20.))
                    .child(div().text_color(rgb(if pending > 0 { YELLOW } else { SOFT })).child(if pending > 0 {
                        format!("Finish your review · {pending} pending line comment{}", if pending == 1 { "" } else { "s" })
                    } else {
                        format!("Reply as {viewer}")
                    }))
                    .child(div().flex_1())
                    .child(tab("tab-write", "Write", !draft.preview, false, cx))
                    .child(tab("tab-preview", "Preview", draft.preview, true, cx)),
            )
            .child(if draft.preview {
                div()
                    .min_h(px(96.))
                    .px(px(14.))
                    .py(px(10.))
                    .text_size(px(13.5))
                    .text_color(rgb(TEXT_BODY))
                    .child(if text.trim().is_empty() {
                        div().text_color(rgb(DIM)).child("Nothing to preview.").into_any_element()
                    } else {
                        md_body(format!("preview-{key}-{}", text.len()), &text, &self.images)
                    })
                    .into_any_element()
            } else {
                div().p(px(8.)).child(Textarea::new(&draft.input)).into_any_element()
            })
            .child(
                div()
                    .flex()
                    .flex_wrap()
                    .items_center()
                    .gap(px(8.))
                    .px(px(10.))
                    .py(px(8.))
                    .border_t_1()
                    .border_color(rgb(BORDER))
                    .child(div().text_size(px(11.5)).text_color(rgb(DIM)).child(match (pending > 0, mine) {
                        (true, true) => "Your line comments go with Submit review · you can't approve your own pull request",
                        (true, false) => "Your line comments go with whichever you choose",
                        (false, true) => "Markdown works · ⌘↵ comments · you can't approve your own pull request",
                        (false, false) => "Markdown works · ⌘↵ comments",
                    }))
                    .when_some(draft.error.clone(), |d, e| d.child(div().text_size(px(12.)).text_color(rgb(RED)).child(e)))
                    .child(
                        div()
                            .ml_auto()
                            .flex()
                            .gap(px(8.))
                    .children(self.merge_button(d, cx))
                    .child(action("request-changes", label(Post::RequestChanges, "Request changes"), RED, Post::RequestChanges, !mine && !busy, cx))
                    .child(action("approve", label(Post::Approve, "Approve"), GREEN, Post::Approve, !mine && !busy, cx))
                    .child(
                        div()
                            .id("comment")
                            .h(px(28.))
                            .px(px(12.))
                            .flex()
                            .items_center()
                            .text_size(px(12.))
                            .bg(rgb(if busy { 0x24402d } else { 0x2f6b3a }))
                            .border_1()
                            .border_color(rgb(0x3d8a4b))
                            .text_color(rgb(0xffffff))
                            .when(!busy, |b| {
                                b.cursor_pointer().hover(|s| s.bg(rgb(0x38804a))).on_click(cx.listener(move |this, _, window, cx| {
                                    this.post(&comment_key, Post::Comment, window, cx)
                                }))
                            })
                            .child(label(Post::Comment, if pending > 0 { "Submit review" } else { "Comment" })),
                    ),
                    ),
            )
            .into_any_element()
    }

    /// Merge when ready, next to the review buttons. It runs the command, which asks
    /// first; once auto-merge is on it says so instead.
    fn merge_button(&self, d: &Detail, cx: &mut Context<Self>) -> Option<AnyElement> {
        let cmd = self.find_cmd(|c| c.action == "automerge")?;
        if let Some(a) = &d.auto_merge {
            return Some(
                div()
                    .h(px(28.))
                    .flex()
                    .items_center()
                    .gap(px(6.))
                    .text_size(px(12.))
                    .text_color(rgb(GREEN))
                    .child(icon("git-merge", 13., GREEN))
                    .child(format!("Auto-merge on · {}", a.word()))
                    .into_any_element(),
            );
        }
        if d.pr.is_draft || matches!(d.pr.state.as_str(), "MERGED" | "CLOSED") {
            return None;
        }
        let (name, key) = (cmd.name.clone(), cmd.key.clone());
        Some(
            button_look("merge-when-ready")
                .h(px(28.))
                .text_size(px(12.))
                .on_click(cx.listener(move |this, _, window, cx| this.start(cmd.clone(), window, cx)))
                .child(icon("git-merge", 13., SOFT))
                .child(name)
                .when(!key.is_empty(), |b| b.child(kbd(key)))
                .into_any_element(),
        )
    }

    /// Under a code thread: a Reply link, or the reply box when replying.
    pub(super) fn thread_reply_view(&self, key: &str, e: &Event, cx: &mut Context<Self>) -> Option<AnyElement> {
        if e.thread_id.is_empty() {
            return None;
        }
        match self.thread_reply.as_ref() {
            Some(r) if r.thread == e.thread_id => Some(
                div()
                    .pt(px(10.))
                    .border_t_1()
                    .border_color(rgb(BORDER))
                    .flex()
                    .flex_col()
                    .gap(px(8.))
                    .child(Textarea::new(&r.input))
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap(px(8.))
                            .child(div().text_size(px(11.5)).text_color(rgb(DIM)).child("⌘↵ replies"))
                            .when_some(r.error.clone(), |d, e| d.child(div().text_size(px(12.)).text_color(rgb(RED)).child(e)))
                            .child(div().flex_1())
                            .child(
                                button_look("reply-cancel")
                                    .h(px(26.))
                                    .text_size(px(12.))
                                    .on_click(cx.listener(|this, _, window, cx| {
                                        this.thread_reply = None;
                                        this.focus.focus(window, cx);
                                        cx.notify();
                                    }))
                                    .child("Cancel"),
                            )
                            .child(
                                primary_look("reply-send")
                                    .h(px(26.))
                                    .text_size(px(12.))
                                    .on_click(cx.listener(|this, _, window, cx| this.send_reply(window, cx)))
                                    .child(if r.posting { "Replying…" } else { "Reply" }),
                            ),
                    )
                    .into_any_element(),
            ),
            _ => {
                let (pr, thread) = (key.to_string(), e.thread_id.clone());
                Some(
                    div()
                        .id(SharedString::from(format!("reply-{}", e.thread_id)))
                        .pt(px(8.))
                        .border_t_1()
                        .border_color(rgb(BORDER))
                        .text_size(px(12.))
                        .text_color(rgb(SOFT))
                        .cursor_pointer()
                        .hover(|s| s.text_color(rgb(TEXT)))
                        .on_click(cx.listener(move |this, _, window, cx| this.start_reply(pr.clone(), thread.clone(), window, cx)))
                        .child("Reply…")
                        .into_any_element(),
                )
            }
        }
    }
}
