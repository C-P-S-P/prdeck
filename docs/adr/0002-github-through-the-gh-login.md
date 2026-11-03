# 0002. Talk to GitHub through the gh login and GraphQL

- Status: accepted
- Date: 2026-11-03

## Context

PR Deck reads PRs, comments, reviews, checks, commits and files, and runs `gh pr checkout` for branches. It needs a GitHub login. Storing our own token means a settings screen, a keychain entry, token scopes to explain, and one more secret on disk.

## Decision

- Use the GitHub CLI's login. [go-gh](https://github.com/cli/go-gh) finds the token `gh auth login` stored (keychain, `hosts.yml`, or `GH_TOKEN`). PR Deck never writes a token anywhere.
- Use GraphQL: one search query fills the PR list (title, branch, review decision, check rollup, mergeable state), and one detail query fills a PR (body, comments, reviews, review threads with diff hunks, checks, labels, the last 100 commits, the first 100 files).
- Searches always add `is:pr is:open archived:false sort:updated-desc`, so a filter is just the interesting part (`involves:@me`, `review-requested:@me -is:draft`).
- Images in PR text are downloaded by the core: with the gh login for GitHub's own hosts (attachments, private repo images), and with a plain request for any other host, so the token never leaves GitHub.

## Consequences

- No account screen to build; signing in is `gh auth login` (or the desktop setup, which runs it: [0011](0011-first-run-setup.md)).
- PR Deck sees exactly what `gh` can see, including private repos and orgs with SSO.
- Two queries per PR keep GitHub's rate limit far away; the cache ([0005](0005-show-saved-data-then-refresh.md)) avoids most of them.
- `gh` must be installed for branch checkout and sign-in.

## Alternatives considered

- **REST.** Several calls per PR (comments, reviews, checks are separate endpoints).
- **Our own OAuth app.** A client ID to register and maintain, and a token to store.
