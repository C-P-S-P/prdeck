//! Choosing folders: where new worktrees go, and which clone a repo's commands use.

use serde::Deserialize;
use serde_json::json;

use super::*;

#[derive(Default, Deserialize)]
#[serde(default)]
struct WorktreeDir {
    dir: String,
}

impl Deck {
    /// Opens the system folder picker; `then` gets the folder chosen. Cancelling does
    /// nothing.
    fn choose_folder(
        &mut self,
        button: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
        then: impl FnOnce(&mut Self, String, &mut Window, &mut Context<Self>) + 'static,
    ) {
        let paths = cx.prompt_for_paths(PathPromptOptions {
            files: false,
            directories: true,
            multiple: false,
            prompt: Some(button.to_string().into()),
        });
        cx.spawn_in(window, async move |this, cx| {
            let Ok(Ok(Some(paths))) = paths.await else {
                return;
            };
            let Some(dir) = paths.into_iter().next() else {
                return;
            };
            let _ = this.update_in(cx, |this, window, cx| {
                then(this, dir.to_string_lossy().into_owned(), window, cx)
            });
        })
        .detach();
    }

    /// "Change folder…" next to a worktree about to be made, and in setup.
    pub(super) fn pick_worktree_dir(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.choose_folder("Put Worktrees Here", window, cx, |this, dir, window, cx| {
            this.set_worktree_dir(dir, window, cx)
        });
    }

    /// Saves where new worktrees go, then plans the worktree command waiting in the run
    /// bar again, so it shows the new place.
    pub(super) fn set_worktree_dir(
        &mut self,
        dir: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(rpc) = self.rpc.clone() else { return };
        let res = rpc.call::<WorktreeDir>("setWorktreeDir", json!({ "path": dir }));
        let id = self.run_id;
        cx.spawn_in(window, async move |this, cx| {
            let res = res.await;
            let _ = this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(r) => {
                        if let Some(setup) = this.setup.as_mut() {
                            setup.worktree_dir = r.dir.clone();
                        }
                        let again = match this.run.as_ref() {
                            Some(run)
                                if this.run_id == id
                                    && matches!(run.stage, Stage::Ready { .. }) =>
                            {
                                Some((run.pr.clone(), run.cmd.clone(), run.input.clone()))
                            }
                            _ => None,
                        };
                        if let Some((pr, cmd, input)) = again {
                            this.prepare(pr, cmd, input, window, cx);
                        }
                        this.say_done(&format!("New worktrees go in {}", r.dir), cx);
                    }
                    Err(e) => this.banner = Some(e.message),
                }
                cx.notify();
            });
        })
        .detach();
    }

    /// L, ⌘⇧L or File → Use Another Clone…: pick another clone for the selected PR's repo.
    pub(super) fn on_change_clone(&mut self, _: &ChangeClone, window: &mut Window, cx: &mut Context<Self>) {
        if let Some(pr) = self.selected_pr() {
            self.change_clone(pr, window, cx);
        }
    }

    /// "Use another clone…" under Local clone: pick another clone for the PR's repo.
    /// Commands for every PR of that repo then run there.
    pub(super) fn change_clone(&mut self, pr: Pr, window: &mut Window, cx: &mut Context<Self>) {
        let current = match self.folders.get(&pr.key()) {
            Some(FolderState::Ready(f)) if !f.clone.is_empty() => Some(f.clone.clone()),
            Some(FolderState::Ready(f)) => Some(f.path.clone()),
            _ => None,
        };
        self.run_id += 1;
        self.run = Some(Run {
            pr: pr.clone(),
            cmd: Cmd::default(),
            input: String::new(),
            stage: Stage::Preparing,
            _sub: None,
        });
        self.no_clone(pr, window, cx);
        if let Some(Run {
            stage: Stage::NoClone { current: c, .. },
            ..
        }) = self.run.as_mut()
        {
            *c = current;
        }
        cx.notify();
    }

    /// "Choose folder…" in the clone picker.
    pub(super) fn pick_clone_dir(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.choose_folder("Use This Clone", window, cx, |this, dir, window, cx| {
            this.set_clone_path(dir, window, cx)
        });
    }
}
