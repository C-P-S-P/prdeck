# 0012. Comment and review from the app

- Status: accepted
- Date: 2026-11-03

## Context

Reading a PR in PR Deck and then switching to the browser to say "LGTM" or approve breaks the flow the app exists for. The conversation view already shows comments, reviews and code threads; it should take replies too.

## Decision

- The core gets three GitHub writes, as GraphQL mutations with the gh login ([0002](0002-github-through-the-gh-login.md)): `addComment` (a comment on the PR), `addPullRequestReview` (Approve, Request changes, or a review comment) and `addPullRequestReviewThreadReply` (a reply on a code thread). The PR detail query now returns the PR's and each thread's node ID for them.
- `prdeck serve` exposes them as `comment`, `review` and `replyThread` ([0009](0009-desktop-uses-the-core-over-prdeck-serve.md)); the demo backend adds to its sample conversation, so the flow can be tried without posting anything.
- The desktop app shows a reply box at the end of every conversation, as in the design: Write / Preview tabs (the preview uses the same renderer as PR text), and Comment, Approve and Request changes. `⌘↵` comments; `⌘⇧C` or the Review button jumps to the box. Each code thread has Reply.
- Rules checked before sending, with the reason shown: a comment and a change request need text; approving is allowed without. GitHub refuses reviews of your own PR, so on your PRs Approve and Request changes are disabled and say why.
- Drafts are kept per PR while the app runs, so switching PRs never loses text. After posting, the PR is loaded again from GitHub, and a green note confirms it.
- Author and reviewer photos come from `github.com/<login>.png` through the same image loading as PR text ([0010](0010-pr-text-rendered-like-github.md)), with initials while they load and for the sample data.

## Consequences

- Reviewing a PR end to end (read, check out, test, approve) happens in one window.
- Posts are public and immediate; there is no pending-review batch (several line comments sent as one review) yet.
- Drafts are lost when the app quits.

## Alternatives considered

- **`gh pr comment` / `gh pr review` as commands.** Works from the command bar today, but no preview and no place to write more than a line.
- **Pending reviews with line comments.** GitHub's full review flow; it needs a diff view first, which PR Deck does not have yet.
