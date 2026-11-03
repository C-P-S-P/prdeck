//! First-run setup: sign in to GitHub (through the GitHub CLI, so PR Deck never holds
//! a token), choose the folders that hold your clones, and choose an editor. It shows
//! on the first launch, whenever nobody is signed in, and from PR Deck → Set Up….

use super::*;

#[derive(Clone, Copy, PartialEq)]
pub(super) enum SetupStep {
    GitHub,
    Folders,
    Editor,
}

pub(super) enum LoginState {
    Idle,
    Starting,
    /// Waiting for the user to enter the code on GitHub.
    Code(DeviceCode),
    Installing,
    Failed(String),
}

pub(super) struct Setup {
    step: SetupStep,
    status: Option<SetupStatus>,
    login: LoginState,
    /// Folder, clones in it, chosen.
    folders: Vec<(String, u64, bool)>,
    add: Entity<InputState>,
    editor: String,
    worktree: bool,
    saving: bool,
    error: Option<String>,
    _sub: Subscription,
}

impl Setup {
    pub(super) fn add_input(&self) -> &Entity<InputState> {
        &self.add
    }
}

impl Deck {
    /// Opens setup at the first step that still needs doing.
    pub(super) fn open_setup(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let add = cx.new(|cx| InputState::new(window, cx).placeholder("Add a folder, like ~/work"));
        let sub = cx.subscribe_in(&add, window, |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { .. } = ev {
                this.add_folder(window, cx);
            }
        });
        self.setup = Some(Setup {
            step: SetupStep::GitHub,
            status: None,
            login: LoginState::Idle,
            folders: vec![],
            add,
            editor: self.hello.editor.clone(),
            worktree: false,
            saving: false,
            error: None,
            _sub: sub,
        });
        self.refresh_setup(cx);
        cx.notify();
    }

    /// Asks prdeck for the login, folders and editors again.
    fn refresh_setup(&mut self, cx: &mut Context<Self>) {
        let Some(rpc) = self.rpc.clone() else { return };
        let status = rpc.call::<SetupStatus>("setupStatus", json!({}));
        cx.spawn(async move |this, cx| {
            let res = status.await;
            let _ = this.update(cx, |this, cx| {
                let Some(setup) = this.setup.as_mut() else { return };
                match res {
                    Ok(st) => {
                        if setup.folders.is_empty() {
                            let any_saved = st.folders.iter().any(|f| f.saved);
                            setup.folders = st
                                .folders
                                .iter()
                                .enumerate()
                                .map(|(i, f)| (f.path.clone(), f.clones, f.saved || (!any_saved && i == 0 && f.clones > 0)))
                                .collect();
                            setup.worktree = st.worktree;
                            let current = st.editors.iter().find(|e| e.name == st.editor && e.installed);
                            setup.editor = current
                                .or_else(|| st.editors.iter().find(|e| e.installed))
                                .map(|e| e.name.clone())
                                .unwrap_or_else(|| st.editor.clone());
                        }
                        if st.signed_in && matches!(setup.login, LoginState::Code(_) | LoginState::Starting) {
                            setup.login = LoginState::Idle;
                        }
                        setup.status = Some(st);
                    }
                    Err(e) => setup.error = Some(e.message),
                }
                cx.notify();
            });
        })
        .detach();
    }

    pub(super) fn sign_in(&mut self, cx: &mut Context<Self>) {
        let (Some(rpc), Some(setup)) = (self.rpc.clone(), self.setup.as_mut()) else { return };
        setup.login = LoginState::Starting;
        setup.error = None;
        cx.notify();
        let start = rpc.call::<DeviceCode>("loginStart", json!({}));
        cx.spawn(async move |this, cx| {
            let code = match start.await {
                Ok(code) => code,
                Err(e) => {
                    let _ = this.update(cx, |this, cx| {
                        if let Some(s) = this.setup.as_mut() {
                            s.login = LoginState::Failed(e.message);
                        }
                        cx.notify();
                    });
                    return;
                }
            };
            let _ = this.update(cx, |this, cx| {
                if !this.hello.demo {
                    cx.write_to_clipboard(ClipboardItem::new_string(code.code.clone()));
                    cx.open_url(&code.url);
                }
                if let Some(s) = this.setup.as_mut() {
                    s.login = LoginState::Code(code);
                }
                cx.notify();
            });
            let done = rpc.call::<LoginDone>("loginWait", json!({})).await;
            let _ = this.update(cx, |this, cx| {
                let Some(s) = this.setup.as_mut() else { return };
                if !matches!(s.login, LoginState::Code(_)) {
                    return; // cancelled
                }
                s.login = match done {
                    Ok(_) => LoginState::Idle,
                    Err(e) => LoginState::Failed(e.message),
                };
                this.refresh_setup(cx);
                cx.notify();
            });
        })
        .detach();
    }

    fn cancel_sign_in(&mut self, cx: &mut Context<Self>) {
        if let Some(rpc) = self.rpc.clone() {
            let _ = rpc.call::<serde_json::Value>("loginCancel", json!({}));
        }
        if let Some(s) = self.setup.as_mut() {
            s.login = LoginState::Idle;
        }
        cx.notify();
    }

    fn install_gh(&mut self, cx: &mut Context<Self>) {
        let (Some(rpc), Some(setup)) = (self.rpc.clone(), self.setup.as_mut()) else { return };
        setup.login = LoginState::Installing;
        cx.notify();
        let res = rpc.call::<serde_json::Value>("installGh", json!({}));
        cx.spawn(async move |this, cx| {
            let res = res.await;
            let _ = this.update(cx, |this, cx| {
                if let Some(s) = this.setup.as_mut() {
                    s.login = match res {
                        Ok(_) => LoginState::Idle,
                        Err(e) => LoginState::Failed(e.message),
                    };
                }
                this.refresh_setup(cx);
                cx.notify();
            });
        })
        .detach();
    }

    fn add_folder(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(setup) = self.setup.as_mut() else { return };
        let path = setup.add.read(cx).value().trim().to_string();
        if path.is_empty() {
            return;
        }
        match setup.folders.iter_mut().find(|f| f.0 == path) {
            Some(f) => f.2 = true,
            None => setup.folders.push((path, 0, true)),
        }
        setup.add.update(cx, |s, cx| s.set_value("", window, cx));
        cx.notify();
    }

    pub(super) fn setup_step(&mut self, step: SetupStep, cx: &mut Context<Self>) {
        if let Some(s) = self.setup.as_mut() {
            s.step = step;
            s.error = None;
        }
        cx.notify();
    }

    /// Saves the choices, then starts PR Deck with them.
    fn finish_setup(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let (Some(rpc), Some(setup)) = (self.rpc.clone(), self.setup.as_mut()) else { return };
        let scan: Vec<String> = setup.folders.iter().filter(|f| f.2).map(|f| f.0.clone()).collect();
        let run = setup
            .status
            .as_ref()
            .and_then(|st| st.editors.iter().find(|e| e.name == setup.editor))
            .map(|e| e.run.clone())
            .unwrap_or_default();
        setup.saving = true;
        cx.notify();
        let save = rpc.call::<serde_json::Value>(
            "saveSetup",
            json!({ "scan": scan, "editor": setup.editor, "editorRun": run, "worktree": setup.worktree }),
        );
        cx.spawn_in(window, async move |this, cx| {
            let res = save.await;
            let _ = this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(_) => {
                        this.setup = None;
                        this.layout.onboarded = true;
                        this.layout.save();
                        this.focus.focus(window, cx);
                        this.say_hello(window, cx);
                    }
                    Err(e) => {
                        if let Some(s) = this.setup.as_mut() {
                            s.saving = false;
                            s.error = Some(e.message);
                        }
                    }
                }
                cx.notify();
            });
        })
        .detach();
    }

    /// Enter moves setup forward.
    pub(super) fn setup_enter(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(s) = self.setup.as_ref() else { return };
        let signed_in = s.status.as_ref().is_some_and(|st| st.signed_in);
        match s.step {
            SetupStep::GitHub if signed_in => self.setup_step(SetupStep::Folders, cx),
            SetupStep::GitHub if matches!(s.login, LoginState::Idle | LoginState::Failed(_)) => self.sign_in(cx),
            SetupStep::Folders => self.setup_step(SetupStep::Editor, cx),
            SetupStep::Editor if !s.saving => self.finish_setup(window, cx),
            _ => {}
        }
    }

    pub(super) fn setup_view(&self, cx: &mut Context<Self>) -> AnyElement {
        let Some(s) = self.setup.as_ref() else { return div().into_any_element() };
        let steps = [(SetupStep::GitHub, "GitHub"), (SetupStep::Folders, "Code folders"), (SetupStep::Editor, "Editor")];
        let progress = div().flex().items_center().gap(px(10.)).children(steps.iter().enumerate().map(|(i, (step, label))| {
            let active = s.step == *step;
            let done = (s.step as u8) > (*step as u8);
            div()
                .flex()
                .items_center()
                .gap(px(8.))
                .when(i > 0, |d| d.child(div().w(px(28.)).h(px(1.)).bg(rgb(BORDER_3))))
                .child(
                    div()
                        .size(px(20.))
                        .flex()
                        .items_center()
                        .justify_center()
                        .border_1()
                        .border_color(rgb(if active { BLUE } else if done { GREEN } else { BORDER_3 }))
                        .text_color(rgb(if active { BLUE } else if done { GREEN } else { DIM }))
                        .text_size(px(11.))
                        .child(if done { "✓".to_string() } else { (i + 1).to_string() }),
                )
                .child(div().text_color(rgb(if active { TEXT } else { SOFT })).child(*label))
        }));

        let body = match s.step {
            SetupStep::GitHub => self.setup_github(s, cx),
            SetupStep::Folders => self.setup_folders(s, cx),
            SetupStep::Editor => self.setup_editor(s, cx),
        };

        div()
            .id("setup")
            .flex_1()
            .min_h(px(0.))
            .overflow_y_scroll()
            .bg(rgb(BG))
            .flex()
            .flex_col()
            .items_center()
            .py(px(48.))
            .child(
                div()
                    .w(px(640.))
                    .flex()
                    .flex_col()
                    .gap(px(20.))
                    .child(
                        div()
                            .flex()
                            .flex_col()
                            .gap(px(6.))
                            .child(div().text_size(px(24.)).font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT_BRIGHT)).child("Set up PR Deck"))
                            .child(div().text_color(rgb(SOFT)).child(
                                "Your pull requests, rendered like GitHub, with one-key git commands. Three quick steps.",
                            )),
                    )
                    .child(progress)
                    .child(card().p(px(24.)).gap(px(14.)).child(body))
                    .when_some(s.error.clone(), |d, e| d.child(div().text_color(rgb(RED)).child(e)))
                    .when(s.status.as_ref().is_some_and(|st| st.signed_in), |d| {
                        d.child(
                            div()
                                .id("setup-skip")
                                .text_color(rgb(DIM))
                                .cursor_pointer()
                                .hover(|st| st.text_color(rgb(TEXT)))
                                .on_click(cx.listener(|this, _, window, cx| {
                                    this.setup = None;
                                    this.layout.onboarded = true;
                                    this.layout.save();
                                    this.focus.focus(window, cx);
                                    // Nobody was signed in at launch, so the list was never loaded.
                                    if this.synced.is_none() && !this.loading {
                                        this.load(cx);
                                    }
                                    cx.notify();
                                }))
                                .child("Skip and keep my current settings"),
                        )
                    }),
            )
            .into_any_element()
    }

    fn setup_github(&self, s: &Setup, cx: &mut Context<Self>) -> AnyElement {
        let title = div().text_size(px(17.)).font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT_BRIGHT)).child("Sign in to GitHub");
        let blurb = div().text_color(rgb(SOFT)).child(
            "PR Deck uses the GitHub CLI's login, the one `gh` and your terminal already use. It never stores a token itself.",
        );
        let Some(st) = s.status.as_ref() else {
            return div().flex().flex_col().gap(px(12.)).child(title).child(blurb).child(div().text_color(rgb(DIM)).child("Checking…")).into_any_element();
        };
        let mut col = div().flex().flex_col().gap(px(14.)).child(title).child(blurb);
        let failed = if let LoginState::Failed(e) = &s.login { Some(e.clone()) } else { None };

        if !st.gh_installed {
            col = col.child(div().text_color(rgb(YELLOW)).child("The GitHub CLI (gh) is not installed."));
            col = col.when_some(failed, |c, e| c.child(div().text_color(rgb(RED)).child(e)));
            col = match &s.login {
                LoginState::Installing => col.child(div().text_color(rgb(SOFT)).child("Installing gh with Homebrew…")),
                _ => col.child(
                    div()
                        .flex()
                        .gap(px(8.))
                        .when(st.brew_found, |d| {
                            d.child(
                                primary_look("install-gh")
                                    .on_click(cx.listener(|this, _, _, cx| this.install_gh(cx)))
                                    .child("Install with Homebrew"),
                            )
                        })
                        .when(!st.brew_found, |d| {
                            d.child(
                                primary_look("get-gh")
                                    .on_click(cx.listener(|_, _, _, cx| cx.open_url("https://cli.github.com")))
                                    .child("Get the GitHub CLI")
                                    .child(icon("external-link", 12., 0xffffff)),
                            )
                        })
                        .child(button_look("check-gh").on_click(cx.listener(|this, _, _, cx| this.refresh_setup(cx))).child("Check again")),
                ),
            };
        } else if st.signed_in && !matches!(s.login, LoginState::Starting | LoginState::Code(_)) {
            // Signing in to another account shows its code below, as when signed out.
            col = col
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap(px(10.))
                        .p(px(12.))
                        .border_1()
                        .border_color(rgb(0x2f4a2c))
                        .bg(rgb(0x1c2a1f))
                        .child(self.images.face(&st.login, 28.))
                        .child(
                            div()
                                .flex()
                                .flex_col()
                                .child(div().text_color(rgb(TEXT)).child(format!("Signed in as @{}", st.login)))
                                .child(div().text_size(px(12.)).text_color(rgb(SOFT)).child(st.gh_version.clone())),
                        )
                        .child(div().ml_auto().text_color(rgb(GREEN)).child("✓")),
                )
                .when_some(failed, |c, e| c.child(div().text_color(rgb(RED)).child(e)))
                .child(
                    div()
                        .flex()
                        .gap(px(8.))
                        .child(
                            primary_look("gh-continue")
                                .on_click(cx.listener(|this, _, _, cx| this.setup_step(SetupStep::Folders, cx)))
                                .child("Continue ↵"),
                        )
                        .child(button_look("gh-switch").on_click(cx.listener(|this, _, _, cx| this.sign_in(cx))).child("Use another account")),
                );
        } else {
            col = match &s.login {
                LoginState::Idle | LoginState::Failed(_) => {
                    col.when_some(failed, |c, e| c.child(div().text_color(rgb(RED)).child(e))).child(
                        div().flex().child(
                            primary_look("sign-in")
                                .on_click(cx.listener(|this, _, _, cx| this.sign_in(cx)))
                                .child(icon("github", 14., 0xffffff))
                                .child("Sign in with GitHub ↵"),
                        ),
                    )
                }
                LoginState::Starting | LoginState::Installing => col.child(div().text_color(rgb(SOFT)).child("Starting sign-in…")),
                LoginState::Code(code) => {
                    let (copy, url) = (code.code.clone(), code.url.clone());
                    col.child(div().text_color(rgb(TEXT_SOFT)).child("Enter this code on the GitHub page that just opened:"))
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .gap(px(12.))
                                .child(
                                    div()
                                        .px(px(16.))
                                        .py(px(8.))
                                        .border_1()
                                        .border_color(rgb(RING_ACTIVE))
                                        .bg(rgb(BG_ACTIVE))
                                        .font_family(MONO)
                                        .text_size(px(26.))
                                        .text_color(rgb(TEXT_BRIGHT))
                                        .child(code.code.clone()),
                                )
                                .child(div().text_size(px(12.)).text_color(rgb(DIM)).child("Copied to your clipboard")),
                        )
                        .child(
                            div()
                                .flex()
                                .gap(px(8.))
                                .child(
                                    button_look("copy-code")
                                        .on_click(cx.listener(move |_, _, _, cx| cx.write_to_clipboard(ClipboardItem::new_string(copy.clone()))))
                                        .child("Copy code"),
                                )
                                .child(
                                    button_look("open-device")
                                        .on_click(cx.listener(move |_, _, _, cx| cx.open_url(&url)))
                                        .child("Open github.com/login/device")
                                        .child(icon("external-link", 12., SOFT)),
                                )
                                .child(button_look("cancel-login").on_click(cx.listener(|this, _, _, cx| this.cancel_sign_in(cx))).child("Cancel")),
                        )
                        .child(div().text_color(rgb(DIM)).child("Waiting for you to approve PR Deck on GitHub…"))
                }
            };
        }
        col.into_any_element()
    }

    fn setup_folders(&self, s: &Setup, cx: &mut Context<Self>) -> AnyElement {
        let rows = s.folders.iter().enumerate().map(|(i, (path, clones, on))| {
            let on = *on;
            div()
                .id(("folder", i))
                .h(px(34.))
                .px(px(10.))
                .flex()
                .items_center()
                .gap(px(10.))
                .border_1()
                .border_color(rgb(if on { RING_ACTIVE } else { BORDER_2 }))
                .when(on, |d| d.bg(rgb(BG_ACTIVE)))
                .cursor_pointer()
                .hover(|st| st.border_color(rgb(BORDER_POP)))
                .on_click(cx.listener(move |this, _, _, cx| {
                    if let Some(f) = this.setup.as_mut().and_then(|s| s.folders.get_mut(i)) {
                        f.2 = !f.2;
                    }
                    cx.notify();
                }))
                .child(check_box(on))
                .child(div().flex_1().font_family(MONO).text_color(rgb(TEXT)).child(path.clone()))
                .child(div().text_size(px(12.)).text_color(rgb(DIM)).child(match clones {
                    0 => "no clones yet".to_string(),
                    1 => "1 clone".to_string(),
                    n => format!("{n} clones"),
                }))
        });
        let chosen = s.folders.iter().filter(|f| f.2).count();
        div()
            .flex()
            .flex_col()
            .gap(px(12.))
            .child(div().text_size(px(17.)).font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT_BRIGHT)).child("Where do you keep your code?"))
            .child(div().text_color(rgb(SOFT)).child(
                "PR Deck opens each pull request in your clone of its repo, found in these folders. A clone elsewhere can be set per repo later.",
            ))
            .child(div().flex().flex_col().gap(px(6.)).children(rows).when(s.folders.is_empty(), |d| {
                d.child(div().text_color(rgb(DIM)).child(if s.status.is_none() { "Looking for clones…" } else { "No clones found yet." }))
            }))
            .child(
                div()
                    .flex()
                    .gap(px(8.))
                    .child(div().flex_1().child(Input::new(&s.add)))
                    .child(button_look("add-folder").on_click(cx.listener(|this, _, window, cx| this.add_folder(window, cx))).child("Add")),
            )
            .child(
                div()
                    .flex()
                    .gap(px(8.))
                    .child(button_look("folders-back").on_click(cx.listener(|this, _, _, cx| this.setup_step(SetupStep::GitHub, cx))).child("Back"))
                    .child(
                        primary_look("folders-next")
                            .on_click(cx.listener(|this, _, _, cx| this.setup_step(SetupStep::Editor, cx)))
                            .child(format!("Continue with {chosen} folder{} ↵", if chosen == 1 { "" } else { "s" })),
                    ),
            )
            .into_any_element()
    }

    fn setup_editor(&self, s: &Setup, cx: &mut Context<Self>) -> AnyElement {
        let editors = s.status.as_ref().map(|st| st.editors.clone()).unwrap_or_default();
        let rows = editors.into_iter().enumerate().map(|(i, e)| {
            let on = s.editor == e.name;
            let name = e.name.clone();
            div()
                .id(("editor", i))
                .h(px(36.))
                .px(px(10.))
                .flex()
                .items_center()
                .gap(px(10.))
                .border_1()
                .border_color(rgb(if on { RING_ACTIVE } else { BORDER_2 }))
                .when(on, |d| d.bg(rgb(BG_ACTIVE)))
                .when(e.installed, |d| {
                    d.cursor_pointer().hover(|st| st.border_color(rgb(BORDER_POP))).on_click(cx.listener(move |this, _, _, cx| {
                        if let Some(s) = this.setup.as_mut() {
                            s.editor = name.clone();
                        }
                        cx.notify();
                    }))
                })
                .child(radio(on))
                .child(div().flex_1().text_color(rgb(if e.installed { TEXT } else { DIM })).child(e.label.clone()))
                .child(div().text_size(px(12.)).text_color(rgb(if e.installed { GREEN } else { DIM })).child(if !e.installed {
                    "not found"
                } else if e.run.is_empty() {
                    "installed"
                } else {
                    "app found (opens with open -a)"
                }))
        });
        let worktree = s.worktree;
        div()
            .flex()
            .flex_col()
            .gap(px(12.))
            .child(div().text_size(px(17.)).font_weight(FontWeight::SEMIBOLD).text_color(rgb(TEXT_BRIGHT)).child("Open pull requests in"))
            .child(div().text_color(rgb(SOFT)).child("“Open in editor” checks out the PR's branch, then opens the folder here. Change it any time in the config (⌘,)."))
            .child(div().flex().flex_col().gap(px(6.)).children(rows))
            .child(
                div()
                    .id("worktree-toggle")
                    .flex()
                    .items_start()
                    .gap(px(10.))
                    .pt(px(6.))
                    .cursor_pointer()
                    .on_click(cx.listener(|this, _, _, cx| {
                        if let Some(s) = this.setup.as_mut() {
                            s.worktree = !s.worktree;
                        }
                        cx.notify();
                    }))
                    .child(check_box(worktree))
                    .child(
                        div()
                            .flex_1()
                            .min_w(px(0.))
                            .flex()
                            .flex_col()
                            .gap(px(2.))
                            .child(div().text_color(rgb(TEXT)).child("Give every PR its own git worktree"))
                            .child(div().text_size(px(12.)).text_color(rgb(DIM)).child(
                                "Keeps your clone untouched while you review. Off: PR Deck switches your clone's branch, and asks before touching uncommitted work.",
                            )),
                    ),
            )
            .child(
                div()
                    .flex()
                    .gap(px(8.))
                    .child(button_look("editor-back").on_click(cx.listener(|this, _, _, cx| this.setup_step(SetupStep::Folders, cx))).child("Back"))
                    .child(
                        primary_look("finish")
                            .on_click(cx.listener(|this, _, window, cx| this.finish_setup(window, cx)))
                            .child(if s.saving { "Saving…" } else { "Start using PR Deck ↵" }),
                    ),
            )
            .into_any_element()
    }
}

fn check_box(on: bool) -> Div {
    div()
        .size(px(16.))
        .flex_none()
        .flex()
        .items_center()
        .justify_center()
        .border_1()
        .border_color(rgb(if on { BLUE } else { BORDER_POP }))
        .when(on, |d| d.bg(rgb(PRIMARY)))
        .text_color(rgb(0xffffff))
        .text_size(px(11.))
        .child(if on { "✓" } else { "" })
}

fn radio(on: bool) -> Div {
    div()
        .size(px(16.))
        .flex_none()
        .flex()
        .items_center()
        .justify_center()
        .border_1()
        .border_color(rgb(if on { BLUE } else { BORDER_POP }))
        .child(div().size(px(8.)).when(on, |d| d.bg(rgb(BLUE))))
}
