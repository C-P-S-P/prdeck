# prdeck

A dashboard for the pull requests you're involved in, in the terminal and on the Mac. Pick a PR, read it the way GitHub shows it, then fetch, rebase, push or open it in your editor with one key.

**On the Mac** ([details](#desktop-app-macos)):

![PR Deck on the Mac: repositories and PRs on the left, the PR with its conversation on the right, commands along the bottom](docs/screenshots/desktop.png)

**In the terminal:**

![prdeck: repos, the PRs that involve you, and the selected PR rendered like GitHub](docs/screenshots/main.png)

| | |
| --- | --- |
| ![Filter palette](docs/screenshots/palette.png) | ![Review requested filter](docs/screenshots/review.png) |
| **`/` filter palette**: pick a filter or type any GitHub qualifier | **`3` Review requested**: who is waiting on you |
| ![Uncommitted changes](docs/screenshots/changes.png) | ![Rebase preview](docs/screenshots/rebase.png) |
| **Uncommitted changes**: stash or drop them first, your choice | **`r` rebase**: the exact commands, run only on `enter` |
| ![Rebase output](docs/screenshots/conflict.png) | ![Help](docs/screenshots/help.png) |
| **Output**: conflicts show right away; fix them, then `C` | **`?` help**: every key and the command it runs |

Screenshots use sample data from `prdeck demo`. Regenerate them with `scripts/screenshots/capture.sh`.

## Install

Needs Go 1.26+, `git`, and the GitHub CLI signed in (`gh auth login`). prdeck uses your gh login; it never stores a token.

```bash
git clone https://github.com/C-P-S-P/prdeck && cd prdeck
go build -o ~/.local/bin/prdeck ./cmd/prdeck
```

## Use

```bash
prdeck                     # the dashboard
prdeck demo                # try it with sample PRs: no GitHub needed, commands don't run
prdeck list                # print your PRs (first filter)
prdeck list author:@me     # any GitHub search qualifier
prdeck config init         # write a starter config
```

| Key | What it does |
| --- | --- |
| `j` `k` / arrows | Move |
| `h` `l` / `tab` | Switch pane (repos, PRs, PR view) |
| `1`–`9` | Switch filter (Involves me, Authored, Review requested, Assigned, Mentions) |
| `/` | Filter palette: pick a filter or type any qualifier (`label:bug repo:org/x`) |
| `R` | Refresh now (it also refreshes every 2 minutes) |
| `f` `c` `u` | Fetch, check out the PR's branch, pull |
| `r` `C` `A` | Rebase on the base branch, continue, abort |
| `p` | Push with `--force-with-lease` |
| `o` | Check out the PR's branch and open your clone in your editor (Zed by default) |
| `w` | Open the PR in a separate git worktree instead |
| `b` | Open the PR on GitHub |
| `L` | Find your clone of this repo with fuzzy search; saved to the config |
| `?` | Help: every key and the exact command it runs |
| Mouse | Click a repo, PR, filter tab or a key in the bottom bar; click the selected PR again to open it; the wheel scrolls the pane under it |

PR descriptions and comments render like on GitHub, inside the pane:

- **Links** show only their text and are clickable (terminal hyperlinks); long bare URLs are shortened to `host/path…`. With mouse support on, hold ⌘ (iTerm2, Ghostty, WezTerm) or your terminal's modifier to click them.
- **Tables** shrink to fit the pane; long cells wrap and short columns stay on one line. Bot reports from Codecov or CodeRabbit stay readable.
- **Code** blocks are syntax highlighted and never wrap; a line too long for the pane ends in `…`.
- **GitHub extras:** alerts (`> [!NOTE]`, colored by type), task lists, `<details>` sections, emoji codes (`:warning:` → ⚠️), `@mentions`, suggestion blocks, and images as `[image: …]` links. A newline is a line break, as on GitHub.

`prdeck md FILE [WIDTH]` renders a markdown file the same way, to check something without opening a PR.

With mouse support on, hold Option (or Shift, depending on your terminal) to select text. Set `ui.mouse: false` to turn it off.

prdeck stays responsive when the terminal falls behind: screen updates are buffered and written separately from input, at most 30 a second, and padding is sent as compact erase codes. If a terminal tab still freezes (seen with Ghostty 1.4.0 and Cursor's terminal recorder together during fast scrolling), close the tab, and set `ui.mouse: false` until Ghostty 1.4.1.

Commands that rewrite history (rebase, abort, push) show the exact command first and run on `enter`; `y` copies it, `esc` cancels.

### Where commands run

Commands run in **your existing clone** of the PR's repo. The PR view's `folder` line shows which clone, what it has checked out, and whether it has uncommitted changes.

- **Branch:** checkout, pull, rebase, push and open first check out the PR's branch (`gh pr checkout`, which also handles PRs from forks). Fetch, continue and abort run as they are.
- **Uncommitted changes:** when a command would switch branches or needs a clean tree (pull, rebase), prdeck lists the changes and asks: `s` stash them (`git stash push --include-untracked`, so nothing is lost), `d` drop them (asks a second time, then `git reset --hard` and `git clean -fd`), or `esc` to cancel.
- **Worktrees** are made only when you ask: `w` opens the PR in `~/prdeck/<repo>-<number>`, leaving your clone alone. Set `editor.worktree: true` to use a worktree for every command. If the PR's branch is already checked out in a worktree, prdeck uses that one.

**Finding your clone.** prdeck scans `~/projects`, `~/code` and `~/src` (two levels deep) for GitHub remotes. If a repo isn't there, the first command on it searches your home folder and lists the clones it finds; pick one with `1`–`9`, search for it with `p`, or clone it with `c`. Press `L` any time to point a repo at a different clone.

The clone search (`L` or `p`) is fuzzy, like fzf: it lists every git clone under your home folder, with clones of this repo first, and narrows as you type (`odh fea` finds `~/projects/odh-feast/feast`). Pick with `↑↓` and `enter` or a click; `tab` copies the path into the box to edit; a typed path (`~/work/x`) can be used as is. Your choice is saved under `repos.paths` in the config, so you only do it once.

## Desktop app (macOS)

The same PRs and commands in a native window, built with [GPUI](https://www.gpui.rs), the Rust UI framework Zed is made with. The window is Rust; everything behind it (config, GitHub, clones, commands) is the same Go core, which the app runs in the background as `prdeck serve`. Your config file and custom commands work in both.

| | |
| --- | --- |
| ![PR text with an image, a table and a task list](docs/screenshots/desktop-markdown.png) | ![Desktop commits](docs/screenshots/desktop-commits.png) |
| **PR text like github.com**: images, tables, highlighted code, alerts | **Commits** and **Files changed**, straight from GitHub |
| ![Desktop setup](docs/screenshots/desktop-setup.png) | ![Go to a pull request](docs/screenshots/desktop-goto.png) |
| **First-run setup**: sign in with GitHub (through `gh`), pick your code folders and editor | **`⌘T` go to PR**: any open PR by number, title, repo or author |
| ![Uncommitted changes](docs/screenshots/desktop-changes.png) | ![Missing clone](docs/screenshots/desktop-noclone.png) |
| **Uncommitted changes**: `s` stash or `d` drop, then the command runs | **No clone yet**: use one found on disk, type a path, or clone |
| ![Comment and review](docs/screenshots/desktop-review.png) | ![A commit with its diff](docs/screenshots/desktop-commit.png) |
| **Comment, approve or request changes**, and reply on code threads | **Click a commit** to see what it changed |
| ![Files changed](docs/screenshots/desktop-files.png) | ![Inline review](docs/screenshots/desktop-inline.png) |
| **Files changed**: every file's diff, each one collapsible | **Inline review**: click a line number to comment; send them all as one review |

- **PR text like github.com**: images (your GitHub login is used for GitHub-hosted ones), `<img width>`, `<details>`, tables of screenshots side by side, syntax-highlighted code, `[!NOTE]`/`[!WARNING]` boxes, emoji and linked @mentions. Text is selectable.
- **Comment and review**: a reply box at the end of every conversation (Write / Preview) with Comment, Approve and Request changes, and Reply on each code thread. Drafts are kept per PR. `⌘⇧C` or the Review button jumps there; `⌘↵` comments.
- **Diffs**: click a commit to see what it changed (Previous / Next steps through them), or open Files changed for every file's diff with line numbers. Big PRs open the first ~1,500 lines and keep the rest one click away.
- **Inline review**: click a line number in Files changed to comment on that line. Add a single comment, or start a review: comments stay pending under their lines until you Submit review, Approve or Request changes, and go to GitHub together. Existing review threads show at their lines with Reply.
- **Profile photos** from GitHub next to every author and reviewer.
- **Resizable panes**: drag the edges of the sidebar, the repositories list and the PR's side panel. Sizes are remembered.
- **First-run setup** signs you in with GitHub's device code in the browser (via the GitHub CLI, so PR Deck never stores a token), finds the folders that hold your clones, and picks your editor. It shows on first launch or when signed out, and from PR Deck → Set Up….

Needs Rust (`rustup`) and Go. From the repo:

```bash
cd desktop
cargo run --release -- --demo   # sample data, nothing runs
cargo run --release             # your PRs (uses your gh login)
```

`desktop/scripts/bundle.sh` builds `desktop/dist/PR Deck.app` to keep in Applications.

| Keys | |
| --- | --- |
| `j` `k` / arrows | move through PRs |
| `⌘T` or `⌘P` | go to a PR |
| `⌘K` or `/` | filters; `1`–`9` pick one |
| `⌘W` / `⌘⇧T` | close tab / reopen it |
| `⌘1`–`⌘9`, `⌘⇧[` `⌘⇧]`, `⌃Tab` | switch tabs |
| `⌘R` | refresh |
| `⌘O` / `⌘⇧O` | open in editor / on GitHub |
| `⌘⇧C` / `⌘↵` | write a comment or review / post the comment |
| `esc` | back from a commit to the list |
| `⌘B` | hide the sidebar |
| `⌘,` | edit the config |
| your command keys | run that command; `esc` closes the command bar |

Screenshots: `cargo run --features snapshot -- --demo --snapshot out.png [--state commits|goto|palette|changes|noclone]`; add `--signed-out --setup --state setup-signin` for setup.

## Configuration

`~/.config/prdeck/config.yaml` (or `$PRDECK_CONFIG`). Everything is optional. `prdeck config init` writes a commented starter file.

```yaml
editor:
  default: cursor              # zed | vscode | cursor | nvim, or your own
  editors:
    helix: {run: hx {path}, terminal: true}

filters:
  - name: Waiting on me
    query: review-requested:@me -is:draft

commands:
  - name: Run tests
    key: t
    run: make test
    branch: true               # check out the PR's branch first
    output: terminal           # panel (default) | terminal | silent
  - name: Squash into one commit
    key: S
    run: git reset --soft $(git merge-base HEAD {remote}/{base}) && git commit -m {title}
    branch: true
    clean: true                # asks to stash or drop changes first
    when: authored             # only on your own PRs
    confirm: true
  - name: Push
    key: P                     # same name as a built-in: overrides just the key
  - name: Abort
    hidden: true               # removes a built-in
```

**Command fields:** `name`, `key`, `run`, `bar` (show in the bottom bar), `confirm`, `prompt` (asks for text, fills `{input}`), `output`, `branch` (check out the PR's branch first), `clean` (needs no uncommitted changes), `worktree` (run in a worktree for the PR), `when` (`authored`, `others`, `draft`, `ready`, `ci_failed`, `conflict`), `hidden`.

**Placeholders:** `{number}` `{branch}` `{base}` `{repo}` `{owner}` `{name}` `{title}` `{author}` `{url}` `{remote}` (the remote that points at the PR's repo) `{path}` (the PR folder) `{input}`. Values are shell-quoted, so a PR title can never run as a command.

Keys `j k q ? / R 1-9` are kept for navigation.

## How it's built

The design and the reasons behind it are written up as decision records in [docs/adr](docs/adr/README.md), with diagrams.

One Go core (`internal/core`, `config`, `github`, `cache`, `repos`, `runner`) with two front-ends:

- **Terminal**: Bubble Tea (`internal/tui`). PR text is parsed with [goldmark](https://github.com/yuin/goldmark) and drawn by `internal/mdrender`, which keeps every line inside the pane.
- **Desktop**: Rust and GPUI with [GPUI Kit](https://gpui-kit.com) components (`desktop/`). It talks to the core over `prdeck serve` (`internal/server`): one JSON request per line on stdin, one response per line on stdout.

GitHub data comes from the GraphQL API through [go-gh](https://github.com/cli/go-gh).

```bash
go test ./...
cd desktop && cargo build && cargo test
```

CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs on every push to main and every pull request: `gofmt`, `go vet` and `go test -race` on Linux, and the desktop app's build and tests on a Mac.

The replies the desktop app reads from `prdeck serve` are saved in `desktop/tests/fixtures`, one file per method. `go test` checks that the demo replies still match them and `cargo test` reads them into the app's types, so the Go and Rust sides cannot drift apart. After changing a reply on purpose, rewrite them and commit the diff:

```bash
go test ./internal/server -run TestContract -update
```

## Roadmap

1. **Core** — config, GitHub search, cache, `prdeck list`. Done.
2. **Terminal app** — panes, PR view, command bar, worktree per PR, open in editor. Done.
3. **Conversations** — reply, review, resolve threads; repo-level `.prdeck.yaml` commands with a trust prompt.
4. **Mac app** — native GPUI window over the same core. Started; next: notifications, a signed release, Homebrew.
