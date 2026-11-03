package config

import "time"

func on() *bool { t := true; return &t }

// Default is the configuration used when the user has no config file.
func Default() *Config {
	return &Config{
		GitHub: GitHub{Refresh: Duration(2 * time.Minute), Limit: 50},
		Repos:  Repos{Scan: []string{"~/projects", "~/code", "~/src"}},
		Editor: Editor{
			Default:     "zed",
			WorktreeDir: "~/prdeck",
			Editors: map[string]EditorSpec{
				"zed":    {Run: "zed {path}"},
				"vscode": {Run: "code {path}"},
				"cursor": {Run: "cursor {path}"},
				"nvim":   {Run: "nvim {path}", Terminal: true},
			},
		},
		Filters: []Filter{
			{Name: "Involves me", Query: "involves:@me"},
			{Name: "Authored", Query: "author:@me"},
			{Name: "Review requested", Query: "review-requested:@me"},
			{Name: "Assigned", Query: "assignee:@me"},
			{Name: "Mentions", Query: "mentions:@me"},
		},
		Commands: DefaultCommands(),
		Theme:    Theme{Markdown: "dark"},
	}
}

// DefaultCommands are the built-in git commands. Users override them by name in config.yaml.
//
// They run in the user's clone. `branch: true` checks out the PR's branch first (an
// empty run just checks out); `clean: true` needs no uncommitted changes. Either one
// asks to stash or drop changes when there are some. Only "Open in worktree" makes a
// worktree, unless editor.worktree is turned on.
//
// Placeholders: {number} {branch} {base} {repo} {owner} {name} {title} {author} {url}
// {remote} (the remote that points at the PR's repo) {path} (the PR folder) {input}.
func DefaultCommands() []Command {
	return []Command{
		{Name: "Fetch", Key: "f", Run: "git fetch {remote} --prune", Bar: on()},
		{Name: "Checkout", Key: "c", Branch: true, Bar: on()},
		{Name: "Pull", Key: "u", Run: "git pull --rebase", Branch: true, Clean: true, Bar: on()},
		{Name: "Rebase", Key: "r", Run: "git fetch {remote} {base} && git rebase {remote}/{base}", Branch: true, Clean: true, Confirm: true, Bar: on()},
		{Name: "Continue", Key: "C", Run: "git -c core.editor=true rebase --continue", Bar: on()},
		{Name: "Abort", Key: "A", Run: "git rebase --abort", Confirm: true, Bar: on()},
		{Name: "Push", Key: "p", Run: "git push --force-with-lease", Branch: true, Confirm: true, Bar: on()},
		{Name: "Open in editor", Key: "o", Action: "open", Branch: true, Bar: on()},
		{Name: "Open in worktree", Key: "w", Action: "open", Worktree: true, Bar: on()},
		{Name: "Open on GitHub", Key: "b", Action: "browse", Bar: on()},
		{Name: "Set clone path", Key: "L", Action: "setpath"},
	}
}

// Example is written by `prdeck config init`.
const Example = `# PR Deck config. Everything here is optional; delete what you don't change.
# Docs: https://github.com/MasterPushers/prdeck#configuration

github:
  refresh: 2m          # how often the PR list refreshes
  limit: 50            # PRs per filter (max 100)

repos:
  scan: [~/projects, ~/code]   # folders that hold your clones (searched two levels deep)
  # paths:                     # clones that live somewhere else; L in the app sets these
  #   owner/name: ~/work/name

editor:
  default: zed                 # zed | vscode | cursor | nvim, or one you add below
  worktree: false              # true: every PR gets its own worktree; false: use your clone
  worktree_dir: ~/prdeck       # where w (Open in worktree) puts them
  # editors:
  #   helix: {run: hx {path}, terminal: true}

# Filters show as 1-9 in the PR pane. Any GitHub search qualifier works.
filters:
  - name: Waiting on me
    query: review-requested:@me -is:draft

# Commands run in your clone of the PR's repo. Same name as a built-in = override it.
# Fields: name, key, run, bar, confirm, prompt, output (panel|terminal|silent),
#         branch (check out the PR first), clean (needs no uncommitted changes),
#         worktree, when (authored | others | draft | ready | ci_failed | conflict), hidden.
commands:
  - name: Run tests
    key: t
    run: make test
    branch: true
    output: terminal
  - name: Squash into one commit
    key: S
    run: git reset --soft $(git merge-base HEAD {remote}/{base}) && git commit -m {title}
    branch: true
    clean: true
    when: authored
    confirm: true
  - name: Commit all
    key: m
    prompt: Commit message
    run: git commit -am {input}
    branch: true

ui:
  mouse: true                  # click and scroll; hold Option (or Shift) to select text
`
