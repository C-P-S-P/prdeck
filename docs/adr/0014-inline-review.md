# 0014. Inline review on the diff

- Status: accepted
- Date: 2026-11-04

## Context

With diffs in the app ([0013](0013-diffs-in-the-app.md)), the review flow was still missing its main part: commenting on a specific line, and sending several such comments as one review the way GitHub does ("Start a review", then "Finish your review").

## Decision

- In Files changed, every line number is clickable. A click opens a comment box under that line: removed lines are commented by their old number (GitHub's LEFT side), added and unchanged lines by their new number (RIGHT).
- **Add single comment** posts it at once (a review with just that comment). **Start a review** keeps it as *pending*: shown under its line with a Pending tag, Edit and Delete, a "Review in progress" bar at the top, and a count on the Review button.
- The review box (end of Conversation and of Files changed) sends the pending comments with whichever is chosen: **Submit review** (a comment review), **Approve** or **Request changes**, in one `addPullRequestReview` mutation with `threads`. Pending comments are cleared once GitHub accepts them.
- Review threads from GitHub (not outdated) show under their line in the diff, with their replies and Reply, as well as in Conversation. The detail query now returns each thread's side.
- Clickable line numbers are ranges inside the existing text blocks (GPUI's `InteractiveText`), and a block ends at a line that has comments under it, so the diff keeps its speed.
- `esc` closes the comment box; while typing, single-key commands are off.

## Consequences

- A full review (read the diff, leave line comments, approve or request changes) happens without leaving the app.
- Pending comments live in memory: quitting the app before submitting loses them. GitHub's own pending reviews (started in the browser) are not loaded.
- Comments go on single lines; ranges (start line to end line) and suggestions are not there yet.
- Outdated threads show only in Conversation, since their lines are no longer in the diff.

## Alternatives considered

- **GitHub's server-side pending review** (`addPullRequestReview` without an event, then `addPullRequestReviewThread` per comment). Survives a quit, but leaves a half-made review on GitHub if the app goes away, and needs more calls. Keeping drafts locally and sending once is simpler; this can move to GitHub's pending review later.
- **One element per line for click targets.** Easier, but brings back the slow diffs [0013](0013-diffs-in-the-app.md) fixed.
