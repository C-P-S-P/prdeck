# 0009. The desktop app uses the core over `prdeck serve`

- Status: accepted
- Date: 2026-11-03

## Context

The desktop app is Rust ([0008](0008-desktop-app-in-rust-with-gpui.md)); the core is Go ([0001](0001-one-go-core-two-front-ends.md)). They need a way to talk that is easy to build, easy to debug and cannot freeze the window.

## Decision

The app starts `prdeck serve` and talks to it over its standard input and output, one JSON object per line:

```
→ {"id": 7, "method": "prepare", "params": {"command": "Rebase", "pr": {…}}}
← {"id": 7, "result": {"title": "Rebase", "pre": "gh pr checkout …", "script": "git fetch …", "changes": [" M writer.py"], …}}
← {"id": 8, "error": {"code": "no_clone", "message": "no local clone of org/repo"}}
```

- Requests run concurrently in the server, so a slow GitHub call never holds up a quick one; replies are matched by `id`.
- Methods: `hello` (config, filters, commands), `cachedSearch` / `search`, `cachedDetail` / `detail`, `folder`, `prepare` / `execute` / `clonePlan`, `findClones` / `listClones` / `setClonePath`, `image`, `openConfig`, and the setup methods ([0011](0011-first-run-setup.md)).
- Error codes the app acts on: `no_clone` (offer clones), `dirty` (stash or drop), `needs_terminal`, `no_gh`, `gone` (the server stopped; its stderr is shown).
- In Rust, each call returns a future (`desktop/src/rpc.rs`); a reader thread resolves them, so the UI thread never waits.
- The app finds the binary in order: `$PRDECK_BIN`, next to itself (inside PR Deck.app), the one `build.rs` compiled, then `PATH`. It passes the login shell's `PATH`, so `gh` and `git` from Homebrew work when the app is opened from Finder.
- The server exits when its input closes, so it never outlives the app.
- `prdeck serve --demo` serves the sample data; `PRDECK_DEBUG=1` logs every request and reply.

## Consequences

- Both apps share every behavior, and the protocol is plain enough to test with `printf … | prdeck serve`.
- One extra process (a few MB) per desktop window.
- Types are written twice (Go structs, Rust serde structs); the field names are the contract.

## Alternatives considered

- **gRPC or a local HTTP server.** Code generation, ports and more moving parts for one client.
- **cgo library linked into Rust.** See [0001](0001-one-go-core-two-front-ends.md).
