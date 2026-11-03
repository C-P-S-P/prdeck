// Package demo is a stand-in backend with sample PRs. `prdeck demo` uses it to show the
// app without a GitHub login, and the README screenshots are taken from it. Commands
// print what they would do; nothing runs and nothing is saved.
package demo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/C-P-S-P/prdeck/internal/cache"
	"github.com/C-P-S-P/prdeck/internal/config"
	"github.com/C-P-S-P/prdeck/internal/core"
	"github.com/C-P-S-P/prdeck/internal/github"
	"github.com/C-P-S-P/prdeck/internal/repos"
	"github.com/C-P-S-P/prdeck/internal/runner"
)

type Backend struct {
	cfg     *config.Config
	prs     []github.PR
	flags   map[int][]string
	details map[int]*github.Detail
	start   time.Time

	mu        sync.Mutex
	clones    map[string]*clone // by repo; a missing repo has no clone yet
	worktrees map[string]bool
}

// clone is the pretend state of a local checkout.
type clone struct {
	path    string
	branch  string
	changes []string
}

func New(cfg *config.Config) *Backend {
	b := &Backend{cfg: cfg, start: time.Now(), flags: map[int][]string{}, details: map[int]*github.Detail{}, worktrees: map[string]bool{}}
	b.clones = map[string]*clone{
		// Uncommitted work in the registry clone, so rebasing #4821 asks to stash or drop.
		"northwind/registry":   {path: "~/code/registry", branch: "main", changes: []string{" M sdk/python/registry/online_writer.py", "?? notes/push-source.md"}},
		"northwind/operator":   {path: "~/code/operator", branch: "fix/secret-rotation"},
		"northwind/console":    {path: "~/code/console", branch: "main"},
		"northwind/sdk-python": {path: "~/code/sdk-python", branch: "main"},
		// upstream/datakit has no clone: commands offer to find it, set a path or clone it.
	}
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

func (b *Backend) ResolveFolder(pr github.PR, wantWorktree bool) (core.Folder, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.clones[pr.Repo]
	if !ok {
		return core.Folder{}, fmt.Errorf("%w of %s", core.ErrNoClone, pr.Repo)
	}
	if wantWorktree || b.cfg.Editor.UseWorktree() {
		p := "~/prdeck/" + pr.Name() + "-" + strconv.Itoa(pr.Number)
		f := core.Folder{Path: p, Clone: c.path, Remote: "origin", Worktree: true, Exists: b.worktrees[p]}
		if f.Exists {
			f.Branch = pr.HeadRefName
		}
		return f, nil
	}
	return core.Folder{Path: c.path, Clone: c.path, Remote: "origin", Exists: true, Branch: c.branch, Changes: append([]string(nil), c.changes...)}, nil
}

func (b *Backend) Prepare(c config.Command, pr github.PR, input string) (core.Plan, error) {
	if ok, why := core.Allowed(c, pr, "you"); !ok {
		return core.Plan{}, fmt.Errorf("%s", why)
	}
	if c.Action == "browse" {
		return core.Plan{Title: c.Name, Action: "browse", Script: "open " + pr.URL, PR: pr}, nil
	}
	f, err := b.ResolveFolder(pr, c.Worktree)
	if err != nil {
		return core.Plan{}, err
	}
	return core.BuildPlan(b.cfg, c, pr, f, input)
}

func (b *Backend) ClonePlan(pr github.PR) core.Plan {
	return core.Plan{Title: "Clone " + pr.Repo, Action: "clone", Script: "gh repo clone " + pr.Repo + " ~/code/" + pr.Name(), Confirm: true, PR: pr}
}

func (b *Backend) FindClones(_ context.Context, repo string) []string {
	time.Sleep(900 * time.Millisecond)
	if repo == "upstream/datakit" {
		return []string{"/Users/you/work/datakit", "/Users/you/old/datakit-fork"}
	}
	return nil
}

// ListClones pretends these clones are on disk, for the fuzzy path prompt.
func (b *Backend) ListClones(context.Context) []repos.DiskClone {
	time.Sleep(400 * time.Millisecond)
	home, _ := os.UserHomeDir()
	at := func(p string, rs ...string) repos.DiskClone {
		return repos.DiskClone{Path: filepath.Join(home, p), Repos: rs}
	}
	return []repos.DiskClone{
		at("code/registry", "northwind/registry"),
		at("code/operator", "northwind/operator"),
		at("code/console", "northwind/console"),
		at("code/sdk-python", "northwind/sdk-python"),
		at("work/datakit", "upstream/datakit", "you/datakit"),
		at("old/datakit-fork", "you/datakit"),
		at("code/dotfiles", "you/dotfiles"),
		at("scratch/notes"),
	}
}

func (b *Backend) SetClonePath(repo, path string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clones[repo] = &clone{path: config.ShortenHome(config.ExpandHome(path)), branch: "main"}
	return "", nil
}

func (b *Backend) ClonePath(repo string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.clones[repo]; ok {
		return c.path
	}
	return ""
}

// Setup pretends to create the worktree and run the Pre commands, updating the
// pretend clone so the next command sees the new state.
func (b *Backend) Setup(_ context.Context, p core.Plan) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pr := p.PR
	var out []string
	if p.Folder.Worktree && !p.Folder.Exists {
		b.worktrees[p.Folder.Path] = true
		out = append(out, "Created worktree "+p.Folder.Path, "Switched to a new branch '"+pr.HeadRefName+"'")
	}
	c := b.clones[pr.Repo]
	if strings.Contains(p.Pre, "git stash") {
		out = append(out, "Saved working directory and index state On "+c.branch+": prdeck: before "+strings.ToLower(p.Title)+" on #"+strconv.Itoa(pr.Number))
		c.changes = nil
	}
	if strings.Contains(p.Pre, "git reset --hard") {
		out = append(out, "HEAD is now at 2c41a9e Merge pull request #4799 from northwind/fix-ci")
		c.changes = nil
	}
	if strings.Contains(p.Pre, "gh pr checkout") {
		out = append(out, "Switched to branch '"+pr.HeadRefName+"'", "Your branch is up to date with 'origin/"+pr.HeadRefName+"'.")
		c.branch = pr.HeadRefName
	}
	return strings.Join(out, "\n"), nil
}

func (b *Backend) Execute(ctx context.Context, p core.Plan) core.Result {
	time.Sleep(300 * time.Millisecond)
	pre, _ := b.Setup(ctx, p)
	out := []string{}
	if pre != "" {
		out = append(out, pre)
	}
	pr := p.PR
	switch {
	case p.Action == "open":
		out = append(out, "Opened "+p.Folder.Path)
	case p.Action == "browse":
		out = append(out, "Opened "+pr.URL)
	case p.Action == "clone":
		b.mu.Lock()
		b.clones[pr.Repo] = &clone{path: "~/code/" + pr.Name(), branch: "main"}
		b.mu.Unlock()
		out = append(out, "Cloning into '~/code/"+pr.Name()+"'...")
	case p.Script == "":
	case strings.Contains(p.Script, "rebase --continue"):
		out = append(out, "Successfully rebased and updated refs/heads/"+pr.HeadRefName+".")
	case strings.Contains(p.Script, "rebase --abort"):
		out = append(out, "Rebase aborted.")
	case strings.Contains(p.Script, "git rebase"):
		out = append(out, "From github.com:"+pr.Repo, " * branch            "+pr.BaseRefName+" -> FETCH_HEAD")
		if pr.Mergeable == "CONFLICTING" {
			out = append(out,
				"Auto-merging sdk/python/registry/online_writer.py",
				"CONFLICT (content): Merge conflict in sdk/python/registry/online_writer.py",
				"error: could not apply 3f9c2e1... Batch online writes every 200 ms",
				"hint: Resolve all conflicts manually, mark them as resolved with \"git add\", then press C to continue.")
			return core.Result{Plan: p, Output: strings.Join(out, "\n"), Err: errExit(1)}
		}
		out = append(out, "Successfully rebased and updated refs/heads/"+pr.HeadRefName+".")
	case strings.Contains(p.Script, "git push"):
		out = append(out, "To github.com:"+pr.Repo+".git", " + 3f9c2e1...a81d0b4 "+pr.HeadRefName+" -> "+pr.HeadRefName+" (forced update)")
	case strings.Contains(p.Script, "git fetch"):
		out = append(out, "From github.com:"+pr.Repo, "   2c41a9e..8be07f3  "+pr.BaseRefName+"       -> origin/"+pr.BaseRefName)
	case strings.Contains(p.Script, "gh pr checkout"):
		out = append(out, "Already on '"+pr.HeadRefName+"'", "Your branch is up to date with 'origin/"+pr.HeadRefName+"'.")
	default:
		out = append(out, "(demo) would run: "+p.Script)
	}
	return core.Result{Plan: p, Output: strings.Join(out, "\n")}
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
