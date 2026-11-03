# 0011. First-run setup signs in through gh

- Status: accepted
- Date: 2026-11-03

## Context

A new user of the desktop app may not have the GitHub CLI, may not be signed in, and has no config file. The terminal app can say "run `gh auth login`"; a Mac app should walk them through it.

## Decision

A three-step setup in the window, shown on first launch, whenever nobody is signed in, and from PR Deck → Set Up…:

1. **GitHub.** If `gh` is missing, install it with Homebrew (or link to cli.github.com). If not signed in, run `gh auth login --web`, show the one-time code it prints, copy it, and open github.com/login/device; when the user approves, the core picks up the new login without a restart. PR Deck still never sees or stores a token ([0002](0002-github-through-the-gh-login.md)).
2. **Code folders.** The folders that hold clones, found on disk and grouped (`~/projects: 63 clones`), with the current `repos.scan` checked; more can be added.
3. **Editor.** Zed, Cursor, VS Code and Neovim, marked installed or not. An editor whose app is installed without its command-line tool is opened with `open -a`. Optionally, a worktree per PR ([0004](0004-commands-run-in-your-clone.md)).

The choices are written into the config file without touching the rest ([0003](0003-yaml-config-and-custom-commands.md)); a signed-in user can skip setup and keep their settings.

`prdeck serve` starts even when nobody is signed in, so the app can show setup instead of an error.

## Consequences

- A new Mac user goes from download to their PRs without opening a terminal.
- The sign-in depends on `gh`'s output format for the code; if it changes, setup shows gh's message and the user can run `gh auth login` themselves.
- `--demo --signed-out --setup` shows the whole flow without touching the real login.

## Alternatives considered

- **Ask for a personal access token.** A secret to paste and store, and scopes to explain.
- **Our own OAuth device flow.** A registered app to maintain, and a token to store.
