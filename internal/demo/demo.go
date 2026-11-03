// Package demo is a stand-in backend with sample PRs. `prdeck demo` uses it to show the
// app without a GitHub login, and the README screenshots are taken from it. Commands
// print what they would do; nothing runs.
package demo

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/patelchaitany/prdeck/internal/cache"
	"github.com/patelchaitany/prdeck/internal/config"
	"github.com/patelchaitany/prdeck/internal/core"
	"github.com/patelchaitany/prdeck/internal/github"
	"github.com/patelchaitany/prdeck/internal/runner"
)

type Backend struct {
	cfg     *config.Config
	prs     []github.PR
	flags   map[int][]string
	details map[int]*github.Detail
	start   time.Time
}

func New(cfg *config.Config) *Backend {
	b := &Backend{cfg: cfg, start: time.Now(), flags: map[int][]string{}, details: map[int]*github.Detail{}}
	b.load()
	return b
}

func (b *Backend) Config() *config.Config { return b.cfg }
func (b *Backend) Viewer() string         { return "you" }

func (b *Backend) match(query string) []github.PR {
	want := ""
	switch {
	case strings.Contains(query, "author:@me"):
		want = "authored"
	case strings.Contains(query, "review-requested:@me"):
		want = "review"
	case strings.Contains(query, "assignee:@me"):
		want = "assigned"
	case strings.Contains(query, "mentions:@me"):
		want = "mentioned"
	}
	var out []github.PR
	for _, p := range b.prs {
		if want == "" || contains(b.flags[p.Number], want) {
			if strings.Contains(query, "is:draft") && !p.IsDraft {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

func (b *Backend) CachedSearch(q string) (cache.SearchEntry, bool) {
	return cache.SearchEntry{At: time.Now(), PRs: b.match(q)}, true
}

func (b *Backend) Search(_ context.Context, q string) ([]github.PR, error) {
	time.Sleep(150 * time.Millisecond)
	return b.match(q), nil
}

func (b *Backend) CachedDetail(pr github.PR) (*github.Detail, bool) {
	d, ok := b.details[pr.Number]
	return d, ok
}

func (b *Backend) Detail(_ context.Context, pr github.PR) (*github.Detail, error) {
	if d, ok := b.details[pr.Number]; ok {
		return d, nil
	}
	return &github.Detail{PR: pr}, nil
}

func (b *Backend) ResolveFolder(pr github.PR) (core.Folder, error) {
	return core.Folder{
		Path:   "~/prdeck/" + pr.Name() + "-" + strconv.Itoa(pr.Number),
		Clone:  "~/code/" + pr.Name(),
		Remote: "origin",
		Exists: pr.Author == "you",
	}, nil
}

func (b *Backend) Prepare(c config.Command, pr github.PR, input string) (core.Plan, error) {
	f, _ := b.ResolveFolder(pr)
	p := core.Plan{Title: c.Name, Action: c.Action, Confirm: c.Confirm, Folder: f, PR: pr, Silent: c.Output == "silent"}
	vals := map[string]string{
		"number": strconv.Itoa(pr.Number), "branch": pr.HeadRefName, "base": pr.BaseRefName,
		"repo": pr.Repo, "owner": pr.Owner(), "name": pr.Name(), "title": pr.Title,
		"author": pr.Author, "url": pr.URL, "remote": f.Remote, "path": f.Path, "input": input,
	}
	switch c.Action {
	case "open":
		p.Title = "Open in " + b.cfg.Editor.Default
		p.Script = runner.Fill(b.cfg.Editor.Editors[b.cfg.Editor.Default].Run, vals)
	case "browse":
		p.Script = "open " + pr.URL
	default:
		p.Script = runner.Fill(c.Run, vals)
	}
	return p, nil
}

func (b *Backend) ClonePlan(pr github.PR) core.Plan {
	return core.Plan{Title: "Clone " + pr.Repo, Action: "clone", Script: "gh repo clone " + pr.Repo, Confirm: true, PR: pr}
}

func (b *Backend) Execute(_ context.Context, p core.Plan) core.Result {
	time.Sleep(300 * time.Millisecond)
	pr := p.PR
	var out string
	if !p.Folder.Exists && p.Action != "browse" && p.Action != "clone" {
		out = "Created worktree " + p.Folder.Path + "\nSwitched to a new branch '" + pr.HeadRefName + "'\n"
	}
	switch {
	case p.Action == "open":
		out += "Opened " + p.Folder.Path
	case p.Action == "browse":
		out += "Opened " + pr.URL
	case strings.Contains(p.Script, "rebase --continue"):
		out += "Successfully rebased and updated refs/heads/" + pr.HeadRefName + "."
	case strings.Contains(p.Script, "rebase --abort"):
		out += "Rebase aborted."
	case strings.Contains(p.Script, "git rebase"):
		if pr.Mergeable == "CONFLICTING" {
			out += "From github.com:" + pr.Repo + "\n * branch            " + pr.BaseRefName + " -> FETCH_HEAD\n" +
				"Auto-merging sdk/python/registry/online_writer.py\n" +
				"CONFLICT (content): Merge conflict in sdk/python/registry/online_writer.py\n" +
				"error: could not apply 3f9c2e1... Batch online writes every 200 ms\n" +
				"hint: Resolve all conflicts manually, mark them as resolved with \"git add\", then press C to continue."
			return core.Result{Plan: p, Output: out, Err: errExit(1)}
		}
		out += "Successfully rebased and updated refs/heads/" + pr.HeadRefName + "."
	case strings.Contains(p.Script, "git push"):
		out += "To github.com:" + pr.Repo + ".git\n + 3f9c2e1...a81d0b4 " + pr.HeadRefName + " -> " + pr.HeadRefName + " (forced update)"
	case strings.Contains(p.Script, "git fetch"):
		out += "From github.com:" + pr.Repo + "\n   2c41a9e..8be07f3  " + pr.BaseRefName + "       -> origin/" + pr.BaseRefName
	case strings.Contains(p.Script, "gh pr checkout"):
		out += "Already on '" + pr.HeadRefName + "'"
	default:
		out += "(demo) would run: " + p.Script
	}
	return core.Result{Plan: p, Output: out}
}

func (b *Backend) EnsureFolder(context.Context, github.PR, core.Folder) (string, error) {
	return "", nil
}

func (b *Backend) TerminalCmd(p core.Plan) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", "echo '(demo) would run: '"+runner.Quote(p.Script)+"; sleep 1")
}

type errExit int

func (e errExit) Error() string { return "exit status " + strconv.Itoa(int(e)) }

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
