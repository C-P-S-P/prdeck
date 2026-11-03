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
			Worktree:    on(),
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
// Placeholders: {number} {branch} {base} {repo} {owner} {name} {title} {author} {url}
// {remote} (the remote that points at the PR's repo) {path} (the PR folder) {input}.
func DefaultCommands() []Command {
	return []Command{
		{Name: "Fetch", Key: "f", Run: "git fetch {remote} --prune", Bar: on()},
		{Name: "Checkout", Key: "c", Run: "gh pr checkout {url}", Bar: on()},
		{Name: "Pull", Key: "u", Run: "git pull --rebase", Bar: on()},
		{Name: "Rebase", Key: "r", Run: "git fetch {remote} {base} && git rebase {remote}/{base}", Confirm: true, Bar: on()},
		{Name: "Continue", Key: "C", Run: "git -c core.editor=true rebase --continue", Bar: on()},
		{Name: "Abort", Key: "A", Run: "git rebase --abort", Confirm: true, Bar: on()},
		{Name: "Push", Key: "p", Run: "git push --force-with-lease", Confirm: true, Bar: on()},
		{Name: "Open in editor", Key: "o", Action: "open", Bar: on()},
		{Name: "Open on GitHub", Key: "b", Action: "browse", Bar: on()},
	}
}

// Example is written by `prdeck config init`.
const Example = `# PR Deck config. Everything here is optional; delete what you don't change.
# Docs: https://github.com/patelchaitany/prdeck#configuration

github:
  refresh: 2m          # how often the PR list refreshes
  limit: 50            # PRs per filter (max 100)

repos:
  scan: [~/projects, ~/code]   # folders that hold your clones
  # paths:                     # clones that live somewhere else
  #   owner/name: ~/work/name

editor:
  default: zed                 # zed | vscode | cursor | nvim, or one you add below
  worktree: true               # open each PR in its own folder under worktree_dir
  worktree_dir: ~/prdeck
  # editors:
  #   helix: {run: hx {path}, terminal: true}

# Filters show as 1-9 in the PR pane. Any GitHub search qualifier works.
filters:
  - name: Waiting on me
    query: review-requested:@me -is:draft

# Commands run in the PR's folder. Same name as a built-in = override it.
# Fields: name, key, run, bar, confirm, prompt, output (panel|terminal|silent),
#         when (authored | others | draft | ready | ci_failed | conflict), hidden.
commands:
  - name: Run tests
    key: t
    run: make test
    output: terminal
  - name: Squash into one commit
    key: S
    run: git reset --soft $(git merge-base HEAD {remote}/{base}) && git commit -m {title}
    when: authored
    confirm: true
  - name: Commit all
    key: m
    prompt: Commit message
    run: git commit -am {input}

theme:
  markdown: dark               # dark | light | dracula | notty
`
