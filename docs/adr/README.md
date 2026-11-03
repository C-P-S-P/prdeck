# How PR Deck is designed

PR Deck shows the open pull requests you are involved in, renders each one the way GitHub does, and runs git commands on its branch with one key. It comes as a terminal app (`prdeck`) and a macOS app (PR Deck.app). Both are front-ends over one Go core.

This folder holds the architecture decision records (ADRs): one short file per decision, saying what was decided, why, and what it costs. Read this page for the overall shape, then the records for the reasons.

## The two apps

**Terminal** (`prdeck`)

![prdeck in the terminal: repositories, the PRs that involve you, and the selected PR](../screenshots/main.png)

**Desktop** (PR Deck.app)

![PR Deck on the desktop: repositories and PRs on the left, the PR with its conversation on the right, commands along the bottom](../screenshots/desktop.png)

| | |
| --- | --- |
| ![PR text with an image, a table and a task list](../screenshots/desktop-markdown.png) | ![Commits from GitHub](../screenshots/desktop-commits.png) |
| PR text as on github.com: images, tables, code, alerts | Commits and changed files from GitHub |
| ![Uncommitted changes: stash or drop first](../screenshots/desktop-changes.png) | ![A repo without a clone: use one found on disk, type a path, or clone](../screenshots/desktop-noclone.png) |
| A command meets uncommitted work: stash or drop, your choice | No clone of the repo: pick one found on disk, or clone it |
| ![Go to a pull request](../screenshots/desktop-goto.png) | ![First-run setup](../screenshots/desktop-setup.png) |
| `⌘T` jumps to any PR | First-run setup: GitHub sign-in, folders, editor |
| ![Files changed](../screenshots/desktop-files.png) | ![Comment and review](../screenshots/desktop-review.png) |
| Every changed file's diff, each one collapsible | Comment, approve or request changes, reply on threads |
| ![A commit with its diff](../screenshots/desktop-commit.png) | |
| A commit opened: message, author and what it changed | |

All screenshots use the sample data from `prdeck demo` / `--demo`.

## The shape

```mermaid
flowchart LR
    subgraph fronts["Front-ends"]
        tui["Terminal app<br/>Go · Bubble Tea<br/>internal/tui"]
        desk["Desktop app<br/>Rust · GPUI<br/>desktop/"]
    end
    subgraph core["Go core (one process per front-end)"]
        server["prdeck serve<br/>JSON lines on stdin/stdout<br/>internal/server"]
        svc["core.Service<br/>internal/core"]
        cfg["config<br/>YAML over defaults"]
        gh["github<br/>GraphQL via gh login"]
        cache["cache<br/>last lists and PRs on disk"]
        repos["repos<br/>finds your clones"]
        runner["runner<br/>runs shell commands"]
    end
    tui -- "Go calls" --> svc
    desk -- "stdin / stdout" --> server --> svc
    svc --> cfg & gh & cache & repos & runner
    gh -- HTTPS --> api[("GitHub API")]
    runner -- "git · gh · editor" --> disk[("Your clones")]
```

The terminal app links the core directly. The desktop app starts the same binary as `prdeck serve` and talks to it over a pipe, so both apps read the same config, run the same commands, and find the same clones.

### Opening a PR and running a command

```mermaid
sequenceDiagram
    actor You
    participant UI as Front-end
    participant Core as Go core
    participant GH as GitHub
    participant Git as Your clone
    You->>UI: pick a filter (Involves me)
    UI->>Core: cachedSearch, then search
    Core-->>UI: saved list at once
    Core->>GH: GraphQL search
    GH-->>Core: open PRs
    Core-->>UI: fresh list
    You->>UI: select a PR
    UI->>Core: cachedDetail, then detail
    Core->>GH: body, comments, reviews, checks, commits, files
    Core-->>UI: detail
    You->>UI: press r (Rebase)
    UI->>Core: prepare Rebase
    Core->>Git: which branch? uncommitted changes?
    Core-->>UI: plan: gh pr checkout … && git rebase …, 2 changes in the way
    You->>UI: s (stash and run)
    UI->>Core: execute plan, stash first
    Core->>Git: git stash, gh pr checkout, git fetch, git rebase
    Core-->>UI: output
```

## Where things live

| Path | What it does |
| --- | --- |
| `cmd/prdeck` | The binary: the terminal app, `list`, `demo`, `config`, and `serve` for the desktop app. |
| `internal/config` | Loads `~/.config/prdeck/config.yaml`, merges it over the built-in defaults, and edits it in place (keeping comments). |
| `internal/github` | GitHub GraphQL with the gh login: search, PR detail, the viewer, image downloads. |
| `internal/cache` | The last PR lists and PR details as JSON, so both apps open instantly. |
| `internal/repos` | Finds clones on disk, reads their branch and uncommitted changes, plans worktrees. |
| `internal/runner` | Fills `{placeholders}` (shell-quoted) and runs commands with `/bin/sh -c`. |
| `internal/core` | `Service`: ties the above together; turns a command into a `Plan` and runs it. |
| `internal/server` | `prdeck serve`: the core as one-JSON-object-per-line requests; first-run setup. |
| `internal/tui`, `internal/mdrender`, `internal/termout` | The terminal app, its markdown renderer, and its terminal writer. |
| `internal/demo` | Sample PRs and pretend clones for `prdeck demo`, `--demo` and the screenshots. |
| `desktop/src` | The desktop app: window (`deck.rs`), setup (`deck/setup.rs`), PR text (`markdown.rs`), the pipe to the core (`rpc.rs`). |

## Decisions

| # | Decision |
| --- | --- |
| [0001](0001-one-go-core-two-front-ends.md) | One Go core behind both apps |
| [0002](0002-github-through-the-gh-login.md) | Talk to GitHub through the gh login and GraphQL |
| [0003](0003-yaml-config-and-custom-commands.md) | One YAML file for settings and custom commands |
| [0004](0004-commands-run-in-your-clone.md) | Commands run in your clone; worktrees only on request |
| [0005](0005-show-saved-data-then-refresh.md) | Show saved data at once, then refresh |
| [0006](0006-own-terminal-markdown-renderer.md) | Our own markdown renderer for the terminal |
| [0007](0007-terminal-output-never-blocks-input.md) | Terminal output never blocks input |
| [0008](0008-desktop-app-in-rust-with-gpui.md) | The desktop app is Rust and GPUI |
| [0009](0009-desktop-uses-the-core-over-prdeck-serve.md) | The desktop app uses the core over `prdeck serve` |
| [0010](0010-pr-text-rendered-like-github.md) | PR text on the desktop is rendered like github.com |
| [0011](0011-first-run-setup.md) | First-run setup signs in through gh |
| [0012](0012-comment-and-review-from-the-app.md) | Comment and review from the app |
| [0013](0013-diffs-in-the-app.md) | Diffs in the app |

To add a record, copy the shape of an existing one (Status, Context, Decision, Consequences, Alternatives) and give it the next number. A record is not rewritten when things change; a new one supersedes it.
