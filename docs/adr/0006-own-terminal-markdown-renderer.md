# 0006. Our own markdown renderer for the terminal

- Status: accepted
- Date: 2026-11-03

## Context

PR descriptions and bot comments are GitHub markdown with HTML mixed in: `<img>`, `<details>`, `<br>`, `[!NOTE]` alerts, wide tables (Codecov), long URLs with tracking parameters, emoji shortcodes. The first version used glamour. It wrapped text twice (orphaned words), let tables run past the pane, and printed raw URLs. Rust terminal renderers were tried too; the best of them still overflowed tables and had no blockquotes.

## Decision

`internal/mdrender` renders markdown for the terminal itself:

- [goldmark](https://github.com/yuin/goldmark) with GFM and emoji parses; a small cleanup step first turns GitHub's HTML into markdown (images, links, `<details>`, alerts, mentions).
- Every block is laid out to the pane width: words wrap once, long words split, tables fit their columns (short columns keep their width, long ones wrap, rows get dividers when they wrap), and no line is ever wider than the pane.
- Links show their text, with the full URL as a terminal hyperlink (OSC 8); bare URLs are shortened to host and path.
- Code blocks get [chroma](https://github.com/alecthomas/chroma) colors on a panel and are cut with `…` instead of wrapped.

## Consequences

- PR text in the terminal reads like GitHub, and the tests check width at every size from 20 to 140 columns.
- About 1,000 lines to own, instead of a dependency.

## Alternatives considered

- **glamour.** Double wrapping and overflowing tables, see above.
- **Rust renderers** (markdown-to-ansi, termimad, mdcat). Each failed on tables, blockquotes or emoji.
