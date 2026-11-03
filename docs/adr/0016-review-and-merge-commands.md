# 0016. Approve, Request changes, Comment and Merge when ready as commands

- Status: accepted
- Date: 2026-11-04

## Context

The desktop app comments and reviews from its review box ([0012](0012-comment-and-review-from-the-app.md)), but the terminal app had no way to, and neither app could merge: approving a PR and setting it to merge once CI passes meant opening GitHub. These are one-key jobs.

## Decision

- Four built-in commands ([0003](0003-yaml-config-and-custom-commands.md)), on the bar and in `?` help, moved or hidden by name like the others: **Approve** `a` (`when: others`, asks first), **Request changes** `x` (`when: others`, asks what should change), **Comment** `i` (asks for the text) and **Merge when ready** `M` (`when: ready`, asks first).
- They are config commands with built-in actions that the core runs (`approve`, `request_changes`, `comment`, `automerge`), not shell lines calling `gh pr review|comment|merge`. The core already sends GitHub's GraphQL writes with the gh login ([0002](0002-github-through-the-gh-login.md)), so these commands send exactly what the desktop's review box sends. They need no clone (shell commands run in the clone), GitHub's refusal comes back in its own words instead of a `gh` exit status, and the demo can model them. The plan still shows the matching `gh pr …` command, so `y` copies something that does the same.
- Approve asks first: an approval cannot be taken back, and it can be the last thing auto-merge was waiting for. Request changes and Comment ask for their text, which is their confirmation. The terminal checks `when:` before asking for text, so Request changes on your own PR says so before you type.
- **Merge when ready** reads the PR from GitHub again when it runs. The detail query now also returns `mergeStateStatus`, `autoMergeRequest` and the base repo's `autoMergeAllowed`, allowed merge methods and `viewerDefaultMergeMethod`. Then:
  - The merge method is the viewer's default (what GitHub's merge button starts with) when the repo allows it, else the first one it allows. Nothing is hard-coded.
  - `CLEAN` or `HAS_HOOKS`: nothing is left to wait for, and GitHub refuses auto-merge (“Pull request is in clean status”), so it merges now with `mergePullRequest`, pinned to the head commit whose checks passed (`expectedHeadOid`).
  - `UNSTABLE`: mergeable now, but checks are failing or still running and none of them is required. GitHub would merge it now and refuses auto-merge; PR Deck does not merge red or unfinished checks, and says why.
  - A repo that does not allow auto-merge is told so, with the setting that turns it on. Auto-merge that is already on is reported as such. `UNKNOWN` (GitHub works mergeability out in the background) is asked again twice, two seconds apart.
  - Otherwise it calls `enablePullRequestAutoMerge`, and GitHub merges once checks and reviews pass. When GitHub refuses, its message is shown without the GraphQL wrapping.
- After any of them succeeds the PR is loaded again, so the new review, comment or auto-merge shows: `merge  auto-merge on · squash and merge · by you` in the terminal; on the desktop, in the side panel and in place of the button. A PR merged at once shows as Merged.
- **Desktop**: the review box stays the one place that comments and reviews. Approve, Request changes and Comment are left out of the bottom bar, and their keys open the review box instead of asking again in the command bar. Merge when ready is a button next to Approve, with its key; it runs the command, so it asks first in the command bar like everywhere else. The bottom bar leaves it out too, since the review box has it.
- The demo approves, comments and turns on auto-merge in its sample PRs and says it sent nothing. Its repos cover the cases: northwind squashes and allows auto-merge, upstream/datakit merges and does not, and #312 is ready to merge now.

## Consequences

- From reading a PR to approving it and setting it to merge is `a` `enter` `M` `enter` in the terminal.
- Like commenting, these are public and immediate once confirmed.
- Approve's `confirm` cannot be turned off by an override, the rule for every built-in (an override can only add `confirm`).
- Turning auto-merge off is not offered; GitHub's PR page does that.
- Repos that require a merge queue are not handled separately: when GitHub wants the queue, its answer is shown as is.
- Details saved before this version have no merge state; Merge when ready always asks GitHub first, so only the plan's `gh` line may lack the method.

## Alternatives considered

- **Shell commands calling `gh pr review`, `gh pr comment` and `gh pr merge --auto`.** No code in the core, but they run in the clone and fail without one, they are a second path beside the review box's GraphQL writes, and a refusal is only a failed shell line. The demo could not show them.
- **Always squash, or always merge.** Simple, but wrong for repos that allow only the other one, and not what the user's own GitHub button does.
- **Merge `UNSTABLE` PRs now, as GitHub would.** In repos without required checks that merges while CI is still running, the opposite of "when ready".
- **Asking for review text in the desktop's command bar.** A second, one-line review box next to the real one.
