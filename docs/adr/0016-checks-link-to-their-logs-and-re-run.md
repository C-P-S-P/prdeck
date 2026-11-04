# 0016. Checks link to their logs, and Re-run failed

- Status: accepted
- Date: 2026-11-04

## Context

When CI fails, the next two things are always the same: read the failing job's log, then re-run it if it was flaky. PR Deck showed which checks failed but had no link to their logs, so both steps meant opening the PR on GitHub and finding the Checks tab.

## Decision

- The detail query now asks, for each check run, for its log link (`detailsUrl`) and its GitHub Actions run (`checkSuite { workflowRun { databaseId } }`), and for each commit status, its `targetUrl`. A check keeps `url` and `runId`, both optional in the JSON, so saved data and older apps still read it. Links come from CI services, not GitHub, so the core keeps only plain `http`/`https` URLs without control characters; the desktop app checks the scheme again before opening one.
- **Terminal**: the names on the `failing:` line are terminal hyperlinks to their logs, made the same way as links in PR text ([0006](0006-own-terminal-markdown-renderer.md)). The line ends with the key that re-runs them.
- **Desktop**: every row in Checks opens its log; the side panel lists the first five failing checks, each clickable. Both show a **Re-run failed** button when CI failed.
- **Re-run failed** is a built-in command ([0003](0003-yaml-config-and-custom-commands.md)): key `F` (free in the defaults and not a navigation key), `when: ci_failed`, and a new built-in action, `rerun`, since a shell line cannot find the runs itself. It can be moved, hidden or given `confirm: true` like any built-in.
- Running it asks GitHub for the PR's checks again (the ones on screen can be minutes old), groups the failed ones by Actions run, and calls `POST /repos/{repo}/actions/runs/{id}/rerun-failed-jobs` once per run, with the gh login ([0002](0002-github-through-the-gh-login.md)); GraphQL has no mutation for it. Only the failed jobs (and those that depend on them) run again. Failed checks with no Actions run (other CI services, commit statuses such as Codecov) are skipped and named in the output. The plan shows the matching `gh run rerun ID --failed` commands, so `y` copies something that does the same. The demo says what it would re-run.
- A saved PR detail now also counts as stale when the PR list's check rollup differs from the one it was saved with: CI finishing does not change a PR's update time, so before this a failure that came in after the PR was opened never reached the PR view.

## Consequences

- From a red ✕ to the failing log is one click (⌘-click in the terminal with mouse support on), and a flaky job is re-run with one key.
- Re-running is public and immediate, like commenting: it starts CI on GitHub at once.
- Details saved before this version have no links until the PR or its checks change.
- A run that is still going cannot be re-run yet; GitHub's error is shown as is. So is the refusal on repos where you may not re-run CI.
- A token that may not read Actions (a fine-grained `GH_TOKEN` without that permission) gets an error for each check's run. Those errors are ignored, so the PR view still loads; its checks just have nothing to re-run.

## Alternatives considered

- **A custom command, `gh run rerun --failed`.** Needs the run ID, which the command line has no way to fill in, and a PR's checks often span several runs.
- **Reading the run ID out of the log link.** Works for GitHub's own links today, but GraphQL gives the ID directly.
- **Re-running whole workflows.** Simpler, but spends CI time on jobs that already passed.
