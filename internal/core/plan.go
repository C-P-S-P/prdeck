package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/github"
	"github.com/MasterPushers/prdeck/internal/runner"
)

// Plan is a command ready to show and run.
type Plan struct {
	Title    string    `json:"title"`
	Pre      string    `json:"pre"`    // runs first: stash/drop changes, check out the PR branch
	Script   string    `json:"script"` // the main command
	Action   string    `json:"action"` // "", "open", "browse", "clone"
	Folder   Folder    `json:"folder"`
	Confirm  bool      `json:"confirm"`
	Terminal bool      `json:"terminal"` // hand the terminal to the main command
	Silent   bool      `json:"silent"`   // don't show output unless it fails
	Changes  []string  `json:"changes"`  // uncommitted changes that must be stashed or dropped first
	PR       github.PR `json:"pr"`
}

// Result is what running a Plan produced.
type Result struct {
	Plan   Plan
	Output string
	Err    error
}

// FullScript is every shell command the plan runs, in order.
func (p Plan) FullScript() string {
	switch {
	case p.Pre == "":
		return p.Script
	case p.Script == "":
		return p.Pre
	}
	return p.Pre + " && " + p.Script
}

// Dir is where the plan runs.
func (p Plan) Dir() string {
	if p.Action == "clone" || p.Action == "browse" {
		home, _ := os.UserHomeDir()
		return home
	}
	return p.Folder.Path
}

// WithStash stashes the uncommitted changes (untracked files too) before the plan.
func (p Plan) WithStash() Plan {
	msg := fmt.Sprintf("prdeck: before %s on #%d", strings.ToLower(p.Title), p.PR.Number)
	return p.withPre("git stash push --include-untracked -m " + runner.Quote(msg))
}

// WithDrop throws the uncommitted changes away before the plan. Ignored files stay.
func (p Plan) WithDrop() Plan {
	return p.withPre("git reset --hard --quiet && git clean -fd --quiet")
}

func (p Plan) withPre(cmd string) Plan {
	if p.Pre != "" {
		cmd += " && " + p.Pre
	}
	p.Pre, p.Changes = cmd, nil
	return p
}

// Allowed reports whether a command's `when:` conditions match the PR.
func Allowed(c config.Command, pr github.PR, viewer string) (bool, string) {
	for _, w := range c.When {
		ok := true
		switch w {
		case "authored":
			ok = viewer != "" && strings.EqualFold(pr.Author, viewer)
		case "others":
			ok = viewer == "" || !strings.EqualFold(pr.Author, viewer)
		case "draft":
			ok = pr.IsDraft
		case "ready":
			ok = !pr.IsDraft
		case "ci_failed":
			ok = pr.CIFailed()
		case "conflict":
			ok = pr.Mergeable == "CONFLICTING"
		}
		if !ok {
			return false, fmt.Sprintf("%s only runs on PRs that are %s", c.Name, strings.ReplaceAll(w, "_", " "))
		}
	}
	return true, ""
}

// Prepare turns a command into a Plan for pr. input fills {input} for commands with a prompt.
func (s *Service) Prepare(c config.Command, pr github.PR, input string) (Plan, error) {
	if ok, why := Allowed(c, pr, s.Viewer()); !ok {
		return Plan{}, errors.New(why)
	}
	if c.Action == "browse" {
		return Plan{Title: c.Name, Action: "browse", Script: openCommand(runtime.GOOS) + " " + runner.Quote(pr.URL), PR: pr}, nil
	}
	f, err := s.ResolveFolder(pr, c.Worktree)
	if err != nil {
		return Plan{}, err
	}
	return BuildPlan(s.cfg, c, pr, f, input)
}

// BuildPlan is Prepare once the folder is known. It decides whether the PR's branch
// must be checked out first and whether uncommitted changes are in the way.
func BuildPlan(cfg *config.Config, c config.Command, pr github.PR, f Folder, input string) (Plan, error) {
	p := Plan{Title: c.Name, Action: c.Action, Confirm: c.Confirm, Terminal: c.Output == "terminal", Silent: c.Output == "silent", PR: pr, Folder: f}
	vals := values(pr, f, input)

	switch c.Action {
	case "open":
		ed, ok := cfg.Editor.Editors[cfg.Editor.Default]
		if !ok {
			return Plan{}, fmt.Errorf("editor %q is not set up in config", cfg.Editor.Default)
		}
		p.Title = "Open in " + cfg.Editor.Default
		if c.Worktree {
			p.Title += " (worktree)"
		}
		p.Script = runner.Fill(ed.Run, vals)
		p.Terminal = ed.Terminal
	case "":
		p.Script = runner.Fill(c.Run, vals)
	default:
		return Plan{}, fmt.Errorf("unknown action %q", c.Action)
	}

	// A new worktree is created on the PR's branch, so it starts clean and switched.
	if !f.Exists || f.Rebasing {
		return p, nil
	}
	switchBranch := c.Branch && f.Branch != pr.HeadRefName
	if switchBranch {
		p.Pre = "gh pr checkout " + runner.Quote(pr.URL)
	}
	if (switchBranch || c.Clean) && len(f.Changes) > 0 {
		p.Changes = f.Changes
	}
	if c.Branch && c.Run == "" && c.Action == "" && !switchBranch {
		// Checkout on the branch already: bring it up to date instead.
		p.Script = "gh pr checkout " + runner.Quote(pr.URL)
	}
	return p, nil
}

// ClonePlan offers to clone a repo that has no local clone yet.
func (s *Service) ClonePlan(pr github.PR) Plan {
	dir := "~"
	if len(s.cfg.Repos.Scan) > 0 {
		dir = s.cfg.Repos.Scan[0]
	}
	target := filepath.Join(config.ExpandHome(dir), pr.Name())
	return Plan{
		Title:   "Clone " + pr.Repo,
		Action:  "clone",
		Script:  "gh repo clone " + runner.Quote(pr.Repo) + " " + runner.Quote(target),
		Confirm: true,
		PR:      pr,
	}
}

func values(pr github.PR, f Folder, input string) map[string]string {
	remote := f.Remote
	if remote == "" {
		remote = "origin"
	}
	return map[string]string{
		"number": strconv.Itoa(pr.Number),
		"branch": pr.HeadRefName,
		"base":   pr.BaseRefName,
		"repo":   pr.Repo,
		"owner":  pr.Owner(),
		"name":   pr.Name(),
		"title":  pr.Title,
		"author": pr.Author,
		"url":    pr.URL,
		"remote": remote,
		"path":   f.Path,
		"input":  input,
	}
}

// openCommand is the shell command that opens a URL in the browser on goos. On Windows
// it runs from Git's sh, where `start` (a cmd.exe built-in) is not there, and explorer
// exits with 1 even when it worked.
func openCommand(goos string) string {
	switch goos {
	case "darwin":
		return "open"
	case "windows":
		return "rundll32 url.dll,FileProtocolHandler"
	}
	return "xdg-open"
}
