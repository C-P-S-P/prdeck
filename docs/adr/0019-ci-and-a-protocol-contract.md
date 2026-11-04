# 0019. CI, and a contract test between Go and Rust

- Status: accepted
- Date: 2026-11-04

## Context

Nothing ran the checks on a push or a pull request; they ran when someone remembered. And the `prdeck serve` protocol is written twice, as Go structs and as Rust serde types ([0009](0009-desktop-uses-the-core-over-prdeck-serve.md)), with nothing checking that they agree. A field renamed on the Go side does not even fail in the app: serde fills a missing field with its default, so the app just shows nothing there.

## Decision

- `.github/workflows/ci.yml` runs on every push to main and every pull request, with a read-only token; a newer push cancels the run still going. Two jobs:
  - **go** (Linux): `gofmt -l` must print nothing, then `go vet ./...` and `go test -race ./...`.
  - **desktop** (macOS on Apple silicon, the only place it builds): Go for `build.rs`, stable Rust, `cargo build --locked` and `cargo test --locked`, with the Rust build cached.
- **Contract test.** `TestContract` (`internal/server`) runs the demo backend, calls each method the desktop app uses with the parameters the app sends, and compares each reply with `desktop/tests/fixtures/<method>.json`. Times and what is installed on the machine are blanked first, so a file changes only when the protocol does. `go test ./internal/server -run TestContract -update` rewrites them.
- `desktop/src/contract_tests.rs` reads every fixture into the type the app reads that reply into (`model.rs`) and checks that the fields the app uses have values. It also checks that every field each type reads is in the reply, using the names serde's derive lists, so a rename fails even where the demo's value is empty or false. In a list, a field needs to be in at least one item, because Go leaves empty `omitempty` fields out (a check that is not a GitHub Actions job has no run id); fields of a single reply, such as `unread` and `new`, are always sent. Plans and PRs, which the app sends back, must keep every field the server sent. A fixture without a check fails.
- `openConfig` is left out, since it opens an editor. `hello` lists editors sorted, so its reply is the same every time.

## Consequences

- A protocol change shows up in the pull request as a changed fixture. `go test` fails until the fixtures are rewritten, and `cargo test` fails if the app can no longer read them.
- Changing the demo data changes the fixtures too: rerun with `-update` and look at the diff.
- Only replies are recorded. What the app sends is checked by hand (the Go test sends the same shapes), and error replies are covered by the existing server tests.
- The desktop job takes several minutes the first time (GPUI is large); the cache makes later runs shorter.

## Alternatives considered

- **Generate one side's types from the other** (JSON Schema, typeshare). A new tool and build step for about twenty types.
- **Rust tests that start `prdeck serve --demo` and talk to it.** No files to update, but the Rust tests would need Go, and a Go change that breaks the app would only fail on the Mac job.
- **Everything on a Mac runner.** Mac runners start slower and cost more minutes; the Go checks do not need one.
