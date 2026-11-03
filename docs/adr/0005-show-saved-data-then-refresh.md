# 0005. Show saved data at once, then refresh

- Status: accepted
- Date: 2026-11-03

## Context

A GitHub search takes about a second and a PR detail about as long. Opening the app to a spinner every time feels slow, and the data rarely changed.

## Decision

- The core keeps the last list for each filter and the last detail for each PR in one JSON file in the user cache folder (`~/Library/Caches/prdeck/cache.json` on macOS).
- Every load shows the saved data first, then asks GitHub and replaces it. A saved PR detail counts as current while the PR's `updatedAt` has not moved; an older one is shown while the new one loads.
- Details saved more than 30 days ago are dropped.
- The list refreshes every `github.refresh` (2 minutes by default, 30 seconds at least) and on `R` / `⌘R`.

## Consequences

- Both apps open with data on screen immediately.
- A stale view lasts about a second, and the header says when the list was last synced.
- A JSON file is enough while everything is read-only. If PR Deck starts writing locally (draft replies, thread state), this becomes SQLite.

## Alternatives considered

- **No cache.** Simpler, slower to open, and wasteful of the rate limit.
- **SQLite now.** More than a read-through cache needs.
