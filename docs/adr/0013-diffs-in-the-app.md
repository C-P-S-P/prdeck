# 0013. Diffs in the app

- Status: accepted (updates the "review in your editor" stance of the first desktop version)
- Date: 2026-11-03

## Context

The first desktop version listed commits and changed files but left the code itself to the editor: "Open in zed". That works for a deep review, but reading what one commit changed, or glancing at a small PR, should not need a checkout and a second app.

## Decision

- The core loads patches from GitHub's REST API, since GraphQL has none: `GET /repos/{repo}/commits/{sha}` for one commit (message, author, files) and `GET /repos/{repo}/pulls/{n}/files` for the whole PR (up to 1,000 files). A commit's diff is kept after the first load, since commits never change. `prdeck serve` exposes them as `commitDiff` and `prDiff`; the demo has sample patches.
- **Commits**: clicking a commit opens it: message, author, +/− and its diff, with Previous / Next and a link to GitHub. `esc` or "All commits" goes back. Clicking the hash copies it.
- **Files changed**: every file's diff, each under a header (status, path, +/−) that opens and closes it, with Expand all / Collapse all. Files open until about 1,500 lines are showing; the rest start closed, and one file shows at most 2,000 lines. Binary or very large files say so.
- Diffs show old and new line numbers, red and green lines, and hunk headers in blue, in the app's monospace font. Long lines scroll sideways instead of wrapping.
- A run of lines of one kind is drawn as a single text element with its line numbers inside, rather than one element per line. That took a 1,500-line diff from 52 ms to 9 ms per frame (release build), so long diffs scroll smoothly.

## Consequences

- Reading a commit or a small PR needs no checkout. The editor is still one click away for anything deeper.
- No syntax colors in diffs yet; red and green carry the meaning.
- Line comments on the diff (pending reviews) can now be built on this ([0012](0012-comment-and-review-from-the-app.md)).

## Alternatives considered

- **`git diff` in the local clone.** Needs a clone and a fetch, and does not work for PRs from forks without more setup. GitHub's patches always match what reviewers see.
- **One element per diff line.** Simpler to style, but 6 elements per line made big diffs stutter.
- **Virtualized list.** Draws only visible lines, but the diff sits inside the PR's scrolling page; grouping lines was enough.
