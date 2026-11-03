# 0003. One YAML file for settings and custom commands

- Status: accepted
- Date: 2026-11-03

## Context

Everyone's git workflow differs: one person rebases, another merges main in; one opens Zed, another nvim; teams have their own `make` targets. The command bar has to be the user's, not ours.

## Decision

- One file, `~/.config/prdeck/config.yaml` (or `$XDG_CONFIG_HOME/prdeck/`, or `$PRDECK_CONFIG`). Missing means defaults.
- The user file is merged over the built-in defaults: filters and commands merge by name, `hidden: true` removes a built-in, a user command takes its key from any other command, and an override without `run:` keeps the built-in's behavior (so `{name: Push, key: P}` only moves the key).
- A command is a shell line with placeholders: `{number} {branch} {base} {repo} {owner} {name} {title} {author} {url} {remote} {path} {input}`. Every value is shell-quoted, so a PR title can never run as a command.
- Flags say how it runs: `branch` (check out the PR's branch first), `clean` (needs no uncommitted changes), `confirm` (show the plan and wait), `prompt` (ask for `{input}`), `output: panel | terminal | silent`, `when: authored | others | draft | ready | ci_failed | conflict`, `worktree`.
- Built-in actions cover what a shell line cannot: `open` (the editor), `browse` (GitHub), `setpath` (point a repo at a clone).
- Unknown keys are an error with a line number; a key reserved for navigation (`j k q ? / R 1-9`) gives a warning.
- When PR Deck writes the file (a clone path, the setup choices), it edits the YAML tree, so comments and everything else stay.

## Consequences

- New commands need no code: `{name: Run tests, key: t, run: make test, branch: true, output: terminal}`.
- The desktop app shows the same commands in its bottom bar, with the same keys.
- `prdeck config init` writes a commented example.

## Alternatives considered

- **TOML.** Fine, but YAML lists of commands read better and match what most CI tools use.
- **Commands as scripts in a folder.** More powerful, harder to share, and no place for flags like `confirm`.
