# 0001. One Go core behind both apps

- Status: accepted
- Date: 2026-11-03

## Context

PR Deck started as a terminal app and grew a macOS app. Both need the same things: read the config, search GitHub, load a PR, find the local clone, decide what a command will do, and run it safely. Writing that twice means two sets of bugs and two apps that slowly disagree about what `r` (Rebase) does.

## Decision

All of that lives in one Go core (`internal/core` and the packages it uses). The front-ends only draw and take input:

- The terminal app (`internal/tui`, Bubble Tea) calls `core.Service` directly.
- The desktop app (`desktop/`, Rust) runs the same binary as `prdeck serve` and calls it over a pipe (see [0009](0009-desktop-uses-the-core-over-prdeck-serve.md)).

A command goes through the same two steps in both apps: `Prepare` turns a config command into a `Plan` (the exact shell commands, the folder, whether it needs a confirmation, which uncommitted changes are in the way), and `Execute` runs it. The UI shows the plan before anything runs.

## Consequences

- One config file, one set of custom commands and one cache serve both apps.
- A fix to command planning (for example the stash/drop prompt) reaches both apps at once.
- The front-ends hold no git or GitHub logic: only drawing, input and calls to the core.
- The desktop app needs the Go binary next to it; `desktop/scripts/bundle.sh` puts it inside PR Deck.app.

## Alternatives considered

- **Port the core to Rust for the desktop app.** One language per app, but two cores to keep in step (about 2,100 lines of tested Go).
- **A shared library through FFI** (cgo `c-archive` linked into Rust). No extra process, but a fragile build and Go's runtime inside a GPUI process.
