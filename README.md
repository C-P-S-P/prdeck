# prdeck

A terminal dashboard for the pull requests you're involved in. Pick a PR, read it the way GitHub shows it, then fetch, rebase, push or open it in your editor with one key.

```
 prdeck  ❯ is:open involves:@me                                            10 PRs · synced just now
┌─ Repositories ─────────────────────┐┌─ #4821 · northwind/registry ──────────────────────────────────┐
│▸ All repositories                10││Add streaming push source to the registry                      │
│  northwind/registry               3││ OPEN  you → main ← feat/stream-push-source                    │
│  northwind/operator               2││6 commits · 14 files · +412 −38  ·  checks ✓4 ✕1               │
└────────────────────────────────────┘│failing: integration-redis                                     │
┌─ Pull requests · Involves me ──────┐│folder   ~/prdeck/registry-4821                                │
│ 1 Involves me  2 Authored  3 Revi… ││                                                               │
│● #4821 registry Add stream… ✗ ✕ 18m││  ## Summary                                                   │
│● #4807 registry Cache proj…   ✓  2h││                                                               │
│◌ #4790 registry Fix TTL ha…   ●  1d││  Adds a StreamPushSource that accepts streaming writes        │
│                                    ││  and lands them in the online store.                          │
└────────────────────────────────────┘└───────────────────────────────────────────────────────────────┘
 f fetch  c checkout  u pull  r rebase  C continue  A abort  p push  o open in editor  / filter  ? help
```
(sample data)

## Install

Needs Go 1.26+, `git`, and the GitHub CLI signed in (`gh auth login`). prdeck uses your gh login; it never stores a token.

```bash
git clone https://github.com/patelchaitany/prdeck && cd prdeck
go build -o ~/.local/bin/prdeck ./cmd/prdeck
```

## Use

```bash
prdeck                     # the dashboard
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
| `f` `c` `u` | Fetch, checkout, pull |
| `r` `C` `A` | Rebase on the base branch, continue, abort |
| `p` | Push with `--force-with-lease` |
| `o` | Open the PR's folder in your editor (Zed by default) |
| `b` | Open the PR on GitHub |
| `?` | Help: every key and the exact command it runs |

Commands that rewrite history (rebase, abort, push) show the exact command first and run on `enter`; `y` copies it, `esc` cancels.

### Where commands run

Each PR gets its own git worktree, `~/prdeck/<repo>-<number>`, created the first time you run a command on it. Your normal checkout is never touched, and several PRs can be open at once. If the PR's branch is already checked out somewhere, prdeck uses that folder instead.

prdeck finds your clones by scanning `~/projects`, `~/code` and `~/src` (two levels deep) for GitHub remotes. A PR from a repo you haven't cloned offers `gh repo clone` instead.

Set `editor.worktree: false` to run commands in your existing clone; prdeck then warns when that clone is on a different branch.

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
    output: terminal           # panel (default) | terminal | silent
  - name: Squash into one commit
    key: S
    run: git reset --soft $(git merge-base HEAD {remote}/{base}) && git commit -m {title}
    when: authored             # only on your own PRs
    confirm: true
  - name: Push
    key: P                     # same name as a built-in: overrides just the key
  - name: Abort
    hidden: true               # removes a built-in
```

**Command fields:** `name`, `key`, `run`, `bar` (show in the bottom bar), `confirm`, `prompt` (asks for text, fills `{input}`), `output`, `when` (`authored`, `others`, `draft`, `ready`, `ci_failed`, `conflict`), `hidden`.

**Placeholders:** `{number}` `{branch}` `{base}` `{repo}` `{owner}` `{name}` `{title}` `{author}` `{url}` `{remote}` (the remote that points at the PR's repo) `{path}` (the PR folder) `{input}`. Values are shell-quoted, so a PR title can never run as a command.

Keys `j k q ? / R 1-9` are kept for navigation.

## How it's built

One Go core (`internal/core`, `config`, `github`, `cache`, `repos`, `runner`) behind a Bubble Tea front-end (`internal/tui`). GitHub data comes from the GraphQL API through [go-gh](https://github.com/cli/go-gh); PR text is rendered with [Glamour](https://github.com/charmbracelet/glamour). The same core is meant to back a Mac app later.

```bash
go test ./...
```

## Roadmap

1. **Core** — config, GitHub search, cache, `prdeck list`. Done.
2. **Terminal app** — panes, PR view, command bar, worktree per PR, open in editor. Done.
3. **Conversations** — reply, review, resolve threads; repo-level `.prdeck.yaml` commands with a trust prompt.
4. **Mac app** — native window from the same core, notifications, Homebrew.
