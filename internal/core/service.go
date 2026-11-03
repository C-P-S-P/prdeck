// Package core is the part both front-ends share: it wires config, GitHub, the cache,
// local clones and the command runner behind one Service.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/patelchaitany/prdeck/internal/cache"
	"github.com/patelchaitany/prdeck/internal/config"
	"github.com/patelchaitany/prdeck/internal/github"
	"github.com/patelchaitany/prdeck/internal/repos"
	"github.com/patelchaitany/prdeck/internal/runner"
)

type Service struct {
	cfg   *config.Config
	gh    *github.Client
	cache *cache.Cache

	mu    sync.RWMutex
	index *repos.Index
}

func New(cfg *config.Config, gh *github.Client, c *cache.Cache) *Service {
	s := &Service{cfg: cfg, gh: gh, cache: c}
	s.Rescan()
	return s
}

func (s *Service) Config() *config.Config { return s.cfg }

// Viewer is the signed-in GitHub login, as last seen by a search.
func (s *Service) Viewer() string { return s.cache.Viewer() }

// Rescan rebuilds the map of local clones.
func (s *Service) Rescan() {
	ix := repos.Build(s.cfg.Repos.Scan, s.cfg.Repos.Paths)
	s.mu.Lock()
	s.index = ix
	s.mu.Unlock()
}

func (s *Service) clone(repo string) (repos.Clone, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.index.Lookup(repo)
}

// CachedSearch returns the last saved results for a filter query.
func (s *Service) CachedSearch(query string) (cache.SearchEntry, bool) { return s.cache.Search(query) }

// Search asks GitHub for open PRs matching a filter query and saves them.
func (s *Service) Search(ctx context.Context, query string) ([]github.PR, error) {
	prs, viewer, err := s.gh.Search(ctx, query, s.cfg.GitHub.Limit)
	if err != nil {
		return nil, err
	}
	s.cache.PutSearch(query, viewer, prs)
	return prs, nil
}

// CachedDetail returns a saved detail and whether it is current for pr.
func (s *Service) CachedDetail(pr github.PR) (*github.Detail, bool) { return s.cache.Detail(pr) }

// Detail loads a PR's description, conversation, reviewers and checks.
func (s *Service) Detail(ctx context.Context, pr github.PR) (*github.Detail, error) {
	d, err := s.gh.Detail(ctx, pr.Repo, pr.Number)
	if err != nil {
		return nil, err
	}
	s.cache.PutDetail(d)
	return d, nil
}

// ErrNoClone means no local clone of the PR's repo was found.
var ErrNoClone = errors.New("no local clone")

// Folder is where a PR's commands run.
type Folder struct {
	Path   string
	Clone  string
	Remote string
	Exists bool   // false: a worktree will be created first
	Branch string // what is checked out there now (non-worktree mode)
}

// ResolveFolder finds the PR's folder without changing anything on disk.
func (s *Service) ResolveFolder(pr github.PR) (Folder, error) {
	c, ok := s.clone(pr.Repo)
	if !ok {
		return Folder{}, fmt.Errorf("%w of %s", ErrNoClone, pr.Repo)
	}
	f := Folder{Clone: c.Path, Remote: c.Remote}
	if !s.cfg.Editor.UseWorktree() {
		f.Path, f.Exists, f.Branch = c.Path, true, repos.CurrentBranch(c.Path)
		return f, nil
	}
	if p := repos.BranchWorktree(c.Path, pr.HeadRefName); p != "" {
		f.Path, f.Exists = p, true
		return f, nil
	}
	f.Path = repos.WorktreePath(s.cfg.Editor.WorktreeDir, pr.Repo, pr.Number)
	if st, err := os.Stat(f.Path); err == nil && st.IsDir() {
		f.Exists = true
	}
	return f, nil
}

// EnsureFolder creates the PR's worktree when it does not exist yet.
func (s *Service) EnsureFolder(ctx context.Context, pr github.PR, f Folder) (string, error) {
	if f.Exists {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return "", err
	}
	script := strings.Join([]string{
		"git -C " + runner.Quote(f.Clone) + " fetch " + runner.Quote(f.Remote) + " --quiet",
		"git -C " + runner.Quote(f.Clone) + " worktree add --detach --quiet " + runner.Quote(f.Path),
		"cd " + runner.Quote(f.Path) + " && gh pr checkout " + runner.Quote(pr.URL),
	}, " && ")
	out, err := runner.Run(ctx, script, f.Clone)
	if err != nil {
		return out, fmt.Errorf("creating worktree %s: %w", config.ShortenHome(f.Path), err)
	}
	return "Created worktree " + config.ShortenHome(f.Path) + "\n" + out, nil
}

// Plan is a command ready to show and run.
type Plan struct {
	Title    string
	Script   string // the exact shell line
	Action   string // "", "open", "browse", "clone"
	Folder   Folder
	Confirm  bool
	Terminal bool   // hand the terminal to the process
	Silent   bool   // don't show output unless it fails
	Warn     string // shown in the preview, forces Confirm
	PR       github.PR
}

// Dir is where the plan runs.
func (p Plan) Dir() string {
	if p.Action == "clone" || p.Action == "browse" {
		home, _ := os.UserHomeDir()
		return home
	}
	return p.Folder.Path
}

// Allowed reports whether a command's `when:` conditions match the PR.
func (s *Service) Allowed(c config.Command, pr github.PR) (bool, string) {
	viewer := s.Viewer()
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
	if ok, why := s.Allowed(c, pr); !ok {
		return Plan{}, errors.New(why)
	}
	p := Plan{Title: c.Name, Action: c.Action, Confirm: c.Confirm, Terminal: c.Output == "terminal", Silent: c.Output == "silent", PR: pr}

	if c.Action == "browse" {
		p.Script = openURLCommand() + " " + runner.Quote(pr.URL)
		return p, nil
	}

	f, err := s.ResolveFolder(pr)
	if err != nil {
		return Plan{}, err
	}
	p.Folder = f
	vals := s.values(pr, f, input)

	switch c.Action {
	case "open":
		ed, ok := s.cfg.Editor.Editors[s.cfg.Editor.Default]
		if !ok {
			return Plan{}, fmt.Errorf("editor %q is not set up in config", s.cfg.Editor.Default)
		}
		p.Title = "Open in " + s.cfg.Editor.Default
		p.Script = runner.Fill(ed.Run, vals)
		p.Terminal = ed.Terminal
	case "":
		p.Script = runner.Fill(c.Run, vals)
	default:
		return Plan{}, fmt.Errorf("unknown action %q", c.Action)
	}

	if !s.cfg.Editor.UseWorktree() && f.Branch != "" && f.Branch != pr.HeadRefName && f.Branch != "HEAD" && c.Action != "open" && !strings.Contains(c.Run, "gh pr checkout") {
		p.Warn = fmt.Sprintf("%s is on %q, not this PR's branch %q", config.ShortenHome(f.Path), f.Branch, pr.HeadRefName)
		p.Confirm = true
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

func (s *Service) values(pr github.PR, f Folder, input string) map[string]string {
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
		"remote": f.Remote,
		"path":   f.Path,
		"input":  input,
	}
}

// Result is what running a Plan produced.
type Result struct {
	Plan   Plan
	Output string
	Err    error
}

// Execute runs a captured plan: creates the worktree if needed, then runs the script.
// GUI editors are started and left running.
func (s *Service) Execute(ctx context.Context, p Plan) Result {
	var log []string
	if p.Action != "browse" && p.Action != "clone" {
		out, err := s.EnsureFolder(ctx, p.PR, p.Folder)
		if out != "" {
			log = append(log, out)
		}
		if err != nil {
			return Result{Plan: p, Output: strings.Join(log, "\n"), Err: err}
		}
	}
	if p.Action == "open" {
		c := runner.Command(context.Background(), p.Script, p.Dir(), true)
		if err := c.Start(); err != nil {
			return Result{Plan: p, Output: strings.Join(log, "\n"), Err: err}
		}
		go func() { _ = c.Wait() }()
		log = append(log, "Opened "+config.ShortenHome(p.Folder.Path))
		return Result{Plan: p, Output: strings.Join(log, "\n")}
	}
	out, err := runner.Run(ctx, p.Script, p.Dir())
	if out != "" {
		log = append(log, out)
	}
	if p.Action == "clone" && err == nil {
		s.Rescan()
	}
	return Result{Plan: p, Output: strings.Join(log, "\n"), Err: err}
}

// TerminalCmd is the process for plans that take over the terminal. Call EnsureFolder first.
func (s *Service) TerminalCmd(p Plan) *exec.Cmd {
	return runner.Command(context.Background(), p.Script, p.Dir(), false)
}

func openURLCommand() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}
