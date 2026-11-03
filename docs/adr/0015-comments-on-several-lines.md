# 0015. Comments on several lines

- Status: accepted (extends [0014](0014-inline-review.md))
- Date: 2026-11-04

## Context

Inline review took comments on one line at a time. Review comments are often about a block (a loop, a few related calls), and GitHub supports comments on a range of lines, plus suggested changes that replace those lines.

## Decision

- In Files changed, the line-number column of each diff block takes the mouse: press on a line number and drag to select several lines, or click one and Shift-click another. The selection stays inside one hunk, as GitHub requires, and is highlighted (yellow numbers, brighter lines) while the comment box is open.
- The column is one element per block that records its bounds and turns the mouse position into a line (every diff line is 19 px tall), so the diff keeps its single-text-element-per-block speed ([0013](0013-diffs-in-the-app.md)).
- The comment carries GitHub's `startLine`/`startSide` with `line`/`side`; a range may start on removed lines and end on new ones ("lines −14 to +14").
- The comment box has **Copy lines** (the selected code, without diff markers) and, when the selection is on new lines, **Suggest change**, which adds a ```` ```suggestion ```` block with those lines to edit.
- Pending comments and GitHub threads on several lines show their range in their header.

## Consequences

- Reviewing a block of code takes one comment, and a fix can be proposed as a suggestion the author applies on GitHub.
- There is still no general text selection in diffs; Copy lines covers copying code.
- Suggestions apply on github.com; PR Deck shows them as a labelled code block.

## Alternatives considered

- **Text selection in the diff, then comment.** Natural, but GPUI text has no selection of its own here, and selecting text inside a single-element block would not map cleanly to lines and sides.
- **A click target per line.** Brings back thousands of elements per diff.
