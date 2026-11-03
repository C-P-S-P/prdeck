# 0018. Unread PRs and new comments since you last looked

- Status: accepted (updates the "read-only" note in [0005](0005-show-saved-data-then-refresh.md))
- Date: 2026-11-04

## Context

The PR list is sorted by update time, so it says that something changed on a PR, not whether you have seen it. Opening a long PR again meant reading the whole conversation to find the two comments that came in since.

## Decision

- **Seen marks in the cache.** `cache.json` ([0005](0005-show-saved-data-then-refresh.md)) keeps, for each PR key, when you last opened it. The time is GitHub's: the PR's update time, or the newest comment, review or reply the detail shows if that is later. A wrong local clock cannot make a PR look unread, and nothing on screen stays new after you have seen it.
- **Opening is showing.** `CachedDetail` and `Detail` are what both apps call to show a PR, so they mark it seen and return the detail with `lastSeen` (the visit before) and `new` on each comment, review and reply from someone else after it. Your own are never new. Selecting a PR is enough, in both apps, since both show the selected PR at once.
- **Unread** is a PR whose update time is after its mark. Lists from `search` and `cachedSearch` come back with `unread` set; the core computes it on a copy, so saved lists are unchanged.
- **First run.** The cache records when it began keeping track (`seenSince`); anything updated before then counts as seen, so upgrading does not light up every PR. Nothing older than 30 days is unread, so marks that old are dropped like old details.
- **Your own actions.** Both apps load a PR again after a comment, review or merge when ready, and load the PR on screen again when a refresh finds it changed (the terminal whenever it is out of date, the desktop when the list says it is unread, which covers a push). That marks it seen as of your change, so prdeck never makes your PR unread. The apps also clear the dot themselves when a detail arrives, in case the list refreshes first.
- **Two apps, one file.** The terminal and desktop apps each hold the cache in memory and write the whole file. Each save now reads the marks in the file first and keeps the newer of each, so a PR opened in one app shows as read in the other after its next refresh.
- **Terminal.** An unread PR's ● or ◌ turns blue and its title bold. In the PR view, new items get a `new` tag, a line under the reviews counts them, and `n` scrolls to the first. `n` is checked after the config commands, so a command someone already put on `n` keeps working; it is not added to the reserved keys.
- **Desktop.** A blue dot in the list's left margin and a brighter title; New tags in the conversation; a bar at its top with the count and Jump to first new, also on `n` from any section. The first new item scrolls itself into place when it is laid out, so the jump works even when the conversation was not showing.
- **Protocol.** Fields only, all optional ([0009](0009-desktop-uses-the-core-over-prdeck-serve.md)): `unread` on a PR, `lastSeen` on a detail, `new` on events and replies. No new method.
- The demo has four PRs that changed since you last looked, with a new reply in a thread and a new comment on #4821.

## Consequences

- The list shows at a glance which PRs need reading again, and a PR opens with what is new marked.
- Moving through the list with `j`/`k` marks the PRs you pass as seen, as in a mail app's preview pane. In the terminal, the tags stay on a PR you already opened until it changes again or the app restarts; on the desktop they go when you open it again.
- Pushes and label changes move a PR's update time, so they make it unread too, but only comments, reviews and replies are tagged. CI finishing does not change the update time and does not make a PR unread.
- The cache now holds local state, not just copies of GitHub. Losing it only loses dots; a JSON file is still enough.
- Two apps saving at the same moment can lose a mark, which shows that PR as unread again; never the other way round.
- Marks are per Mac and separate from GitHub's notifications.

## Alternatives considered

- **GitHub's notifications** (read state per thread). Shared with github.com and other devices, but only for PRs you are subscribed to, another REST call per refresh, and opening a PR in PR Deck would have to mark it read on GitHub, a write you did not ask for.
- **The local clock for marks.** Simpler, but with a clock behind GitHub's, your own comment would look newer than your visit.
- **Mark seen after a short delay**, so passing over a PR with `j`/`k` does not count. More state for a small gain; the detail is already on screen the moment a PR is selected.
- **A separate `markSeen` method.** Both apps would have to call it at the right moment; marking where the detail is shown keeps that rule in the core.
