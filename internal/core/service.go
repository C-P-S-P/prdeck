// Package core is the part both front-ends share: it wires config, GitHub, the cache,
// local clones and the command runner behind one Service.
package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/C-P-S-P/prdeck/internal/cache"
	"github.com/C-P-S-P/prdeck/internal/config"
	"github.com/C-P-S-P/prdeck/internal/github"
	"github.com/C-P-S-P/prdeck/internal/repos"
	"github.com/C-P-S-P/prdeck/internal/runner"
)

type Service struct {
	cfg     *config.Config
	cfgPath string
	cache   *cache.Cache

	ghMu sync.RWMutex
	gh   *github.Client // nil until the user signs in to GitHub

	mu    sync.RWMutex
	index *repos.Index

	diffMu      sync.Mutex
	commitDiffs map[string]*github.CommitDiff // by "owner/name@sha"

	diskMu sync.Mutex
	disk   []repos.DiskClone // every clone under the home folder, from the last scan
	diskAt time.Time
}

// ErrSignedOut means there is no GitHub login yet (`gh auth login`).
var ErrSignedOut = errors.New("not signed in to GitHub; run `gh auth login`")

// New wires the service. cfgPath is where SetClonePath saves repo paths. gh may be
// nil when nobody is signed in yet; Connect picks the login up later.
func New(cfg *config.Config, cfgPath string, gh *github.Client, c *cache.Cache) *Service {
	s := &Service{cfg: cfg, cfgPath: cfgPath, gh: gh, cache: c}
	s.Rescan()
	return s
}

func (s *Service) Config() *config.Config { return s.cfg }

// Viewer is the signed-in GitHub login, as last seen by a search.
func (s *Service) Viewer() string { return s.cache.Viewer() }

// Rescan rebuilds the map of local clones.
func (s *Service) Rescan() {
	s.mu.RLock()
	scan, paths := s.cfg.Repos.Scan, s.cfg.Repos.Paths
	s.mu.RUnlock()
	ix := repos.Build(scan, paths)
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
	gh := s.client()
	if gh == nil {
		return nil, ErrSignedOut
	}
	prs, viewer, err := gh.Search(ctx, query, s.cfg.GitHub.Limit)
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
	gh := s.client()
	if gh == nil {
		return nil, ErrSignedOut
	}
	d, err := gh.Detail(ctx, pr.Repo, pr.Number)
	if err != nil {
		return nil, err
	}
	s.cache.PutDetail(d)
	return d, nil
}

// FetchImage downloads an image from a PR body, with the gh login for GitHub's hosts.
func (s *Service) FetchImage(ctx context.Context, url string) ([]byte, string, error) {
	if gh := s.client(); gh != nil {
		return gh.FetchImage(ctx, url)
	}
	return github.FetchPublicImage(ctx, url)
}

func (s *Service) client() *github.Client {
	s.ghMu.RLock()
	defer s.ghMu.RUnlock()
	return s.gh
}

// Connect (re)reads the gh login, after the user signs in.
func (s *Service) Connect() error {
	gh, err := github.NewClient()
	if err != nil {
		return err
	}
	s.ghMu.Lock()
	s.gh = gh
	s.ghMu.Unlock()
	return nil
}

// Login is the signed-in GitHub user, or "" when nobody is signed in.
func (s *Service) Login(ctx context.Context) (string, error) {
	gh := s.client()
	if gh == nil {
		return "", ErrSignedOut
	}
	return gh.Login(ctx)
}

// Comment adds a comment to the PR whose node ID is prID.
func (s *Service) Comment(ctx context.Context, prID, body string) error {
	gh := s.client()
	if gh == nil {
		return ErrSignedOut
	}
	return gh.Comment(ctx, prID, body)
}

// Review approves the PR, requests changes, or leaves a review comment, with any
// comments on lines of the diff.
func (s *Service) Review(ctx context.Context, prID, event, body string, threads []github.DraftThread) error {
	gh := s.client()
	if gh == nil {
		return ErrSignedOut
	}
	return gh.Review(ctx, prID, event, body, threads)
}

// ReplyThread answers a review thread.
func (s *Service) ReplyThread(ctx context.Context, threadID, body string) error {
	gh := s.client()
	if gh == nil {
		return ErrSignedOut
	}
	return gh.ReplyThread(ctx, threadID, body)
}

// CommitDiff loads a commit's changes. Commits never change, so each is fetched once.
func (s *Service) CommitDiff(ctx context.Context, repo, sha string) (*github.CommitDiff, error) {
	gh := s.client()
	if gh == nil {
		return nil, ErrSignedOut
	}
	key := strings.ToLower(repo) + "@" + sha
	s.diffMu.Lock()
	d, ok := s.commitDiffs[key]
	s.diffMu.Unlock()
	if ok {
		return d, nil
	}
	d, err := gh.CommitDiff(ctx, repo, sha)
	if err != nil {
		return nil, err
	}
	s.diffMu.Lock()
	if s.commitDiffs == nil {
		s.commitDiffs = map[string]*github.CommitDiff{}
	}
	s.commitDiffs[key] = d
	s.diffMu.Unlock()
	return d, nil
}

// PRDiff loads the patches of every file a PR changes.
func (s *Service) PRDiff(ctx context.Context, pr github.PR) ([]github.FileDiff, error) {
	gh := s.client()
	if gh == nil {
		return nil, ErrSignedOut
	}
	return gh.PRDiff(ctx, pr.Repo, pr.Number)
}

// ConfigPath is the config file the service reads and writes.
func (s *Service) ConfigPath() string { return s.cfgPath }

// Reload reads the config file again, after setup changed it.
func (s *Service) Reload() error {
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		return err
	}
	s.mu.Lock()
	*s.cfg = *cfg
	s.mu.Unlock()
	s.Rescan()
	s.diskMu.Lock()
	s.disk = nil
	s.diskMu.Unlock()
	return nil
}

// ErrNoClone means no local clone of the PR's repo was found.
var ErrNoClone = errors.New("no local clone")

// Folder is where a PR's commands run and what state it is in.
type Folder struct {
	Path     string   `json:"path"`
	Clone    string   `json:"clone"`
	Remote   string   `json:"remote"`
	Worktree bool     `json:"worktree"` // Path is a worktree for this PR
	Exists   bool     `json:"exists"`   // false: the worktree is created first
	Branch   string   `json:"branch"`   // checked out now ("HEAD" when detached)
	Changes  []string `json:"changes"`  // uncommitted changes, `git status --porcelain` lines
	Rebasing bool     `json:"rebasing"`
	// The local branch named like the PR's head is the user's own, not the PR's (a PR
	// from a fork's main, which gh checks out as <owner>/main).
	OwnBranch bool `json:"ownBranch"`
}

// ResolveFolder finds where commands for pr run, without changing anything (it only
// drops a worktree of the PR whose folder was deleted). Commands run in the user's
// clone, or in a worktree that already has the PR's branch. A new worktree is planned
// only when asked for (wantWorktree) or turned on in config.
func (s *Service) ResolveFolder(pr github.PR, wantWorktree bool) (Folder, error) {
	c, ok := s.clone(pr.Repo)
	if !ok {
		return Folder{}, fmt.Errorf("%w of %s", ErrNoClone, pr.Repo)
	}
	f := Folder{Path: c.Path, Clone: c.Path, Remote: c.Remote, Exists: true}
	f.OwnBranch = pr.IsCrossRepo && repos.OwnBranch(c.Path, c.Remote, pr.Repo, pr.HeadRefName)
	if p := repos.BranchWorktree(c.Path, pr.HeadRefName); p != "" && !f.OwnBranch {
		f.Path, f.Worktree = p, p != c.Path
	} else if wantWorktree || s.cfg.Editor.UseWorktree() {
		dir := s.cfg.Editor.WorktreeDir
		f.Path, f.Worktree = repos.WorktreePath(dir, pr.Repo, pr.Number), true
		st, err := os.Stat(f.Path)
		if err == nil && st.IsDir() && !repos.SameClone(f.Path, c.Path) {
			// <name>-<number> is taken by another repo of the same name (another org's
			// api): use <owner>-<name>-<number>. WorktreePath drops what is before the "/".
			f.Path = repos.WorktreePath(dir, "/"+pr.Owner()+"-"+pr.Name(), pr.Number)
			st, err = os.Stat(f.Path)
		}
		f.Exists = err == nil && st.IsDir()
	}
	if f.Exists {
		f.Branch = repos.CurrentBranch(f.Path)
		f.Changes = repos.Changes(f.Path)
		f.Rebasing = repos.Rebasing(f.Path)
	}
	return f, nil
}

// Setup does what must happen before a plan's main command: create its worktree if
// needed, then run its Pre commands (stash or drop changes, check out the branch).
func (s *Service) Setup(ctx context.Context, p Plan) (string, error) {
	var log []string
	if p.Folder.Worktree && !p.Folder.Exists && p.Action != "browse" && p.Action != "clone" {
		out, err := s.createWorktree(ctx, p.PR, p.Folder)
		if out != "" {
			log = append(log, out)
		}
		if err != nil {
			return strings.Join(log, "\n"), err
		}
	}
	if p.Pre != "" {
		out, err := runner.Run(ctx, p.Pre, p.Dir())
		if out != "" {
			log = append(log, out)
		}
		if err != nil {
			return strings.Join(log, "\n"), err
		}
	}
	return strings.Join(log, "\n"), nil
}

func (s *Service) createWorktree(ctx context.Context, pr github.PR, f Folder) (string, error) {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return "", err
	}
	script := strings.Join([]string{
		"git -C " + runner.Quote(f.Clone) + " fetch " + runner.Quote(f.Remote) + " --quiet",
		"git -C " + runner.Quote(f.Clone) + " worktree add --detach --quiet " + runner.Quote(f.Path),
	}, " && ")
	out, err := runner.Run(ctx, script, f.Clone)
	if err == nil {
		var co string
		co, err = runner.Run(ctx, "gh pr checkout "+runner.Quote(pr.URL), f.Path)
		if err != nil {
			// Left behind, a worktree whose checkout failed would be opened next time as
			// it is, without the PR's branch. Removed even when ctx was cancelled, but only
			// once add worked: if add failed, the folder may be another request's worktree.
			_, _ = runner.Run(context.Background(), "git worktree remove --force "+runner.Quote(f.Path), f.Clone)
		}
		if out != "" && co != "" {
			out += "\n"
		}
		out += co
	}
	if err != nil {
		return out, fmt.Errorf("creating worktree %s: %w", config.ShortenHome(f.Path), err)
	}
	return "Created worktree " + config.ShortenHome(f.Path) + "\n" + out, nil
}

// Execute runs a plan: Setup, then the main command. GUI editors are started and left
// running.
func (s *Service) Execute(ctx context.Context, p Plan) Result {
	var log []string
	out, err := s.Setup(ctx, p)
	if out != "" {
		log = append(log, out)
	}
	if err != nil {
		return Result{Plan: p, Output: strings.Join(log, "\n"), Err: err}
	}
	if p.Action == "open" {
		c := runner.Command(context.Background(), p.Script, p.Dir(), true)
		// The output goes to a file, not a pipe, so an editor left running never keeps
		// Wait from returning. A command that fails at once (no such editor) is reported.
		tmp, _ := os.CreateTemp("", "prdeck-open-*")
		output := func() string { // reads and closes the file
			if tmp == nil {
				return ""
			}
			defer tmp.Close()
			b, _ := io.ReadAll(io.NewSectionReader(tmp, 0, 1<<20))
			return strings.TrimRight(string(b), "\n")
		}
		if tmp != nil {
			c.Stdout, c.Stderr = tmp, tmp
			// The editor keeps the file open, so its name can go on return: nothing is
			// left behind when prdeck quits before the editor does.
			defer os.Remove(tmp.Name())
		}
		if err := c.Start(); err != nil {
			output()
			return Result{Plan: p, Output: strings.Join(log, "\n"), Err: err}
		}
		var out string
		done := make(chan error, 1)
		go func() {
			err := c.Wait()
			out = output()
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				if out != "" {
					log = append(log, out)
				}
				return Result{Plan: p, Output: strings.Join(log, "\n"), Err: err}
			}
		case <-time.After(500 * time.Millisecond):
		}
		log = append(log, "Opened "+config.ShortenHome(p.Folder.Path))
		return Result{Plan: p, Output: strings.Join(log, "\n")}
	}
	if p.Script != "" {
		out, err = runner.Run(ctx, p.Script, p.Dir())
		if out != "" {
			log = append(log, out)
		}
	}
	if p.Action == "clone" && err == nil {
		s.Rescan()
	}
	return Result{Plan: p, Output: strings.Join(log, "\n"), Err: err}
}

// TerminalCmd is the process for plans that take over the terminal. Call Setup first.
func (s *Service) TerminalCmd(p Plan) *exec.Cmd {
	return runner.Command(context.Background(), p.Script, p.Dir(), false)
}

// FindClones searches the home folder for clones of repo, beyond the scan folders.
func (s *Service) FindClones(ctx context.Context, repo string) []string {
	var found []string
	for _, c := range s.ListClones(ctx) {
		for _, r := range c.Repos {
			if strings.EqualFold(r, repo) {
				found = append(found, c.Path)
				break
			}
		}
	}
	return found
}

// ListClones is every git clone under the home folder (four levels deep, plus the
// scan folders, configured paths and the GOPATH layout). The scan takes a few seconds,
// so its result is kept for ten minutes.
func (s *Service) ListClones(ctx context.Context) []repos.DiskClone {
	s.diskMu.Lock()
	defer s.diskMu.Unlock()
	if s.disk != nil && time.Since(s.diskAt) < 10*time.Minute {
		return s.disk
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	roots := []string{home, filepath.Join(home, "go", "src", "github.com")}
	s.mu.RLock()
	scan, paths := s.cfg.Repos.Scan, s.cfg.Repos.Paths
	s.mu.RUnlock()
	for _, d := range scan {
		roots = append(roots, config.ExpandHome(d))
	}
	for _, p := range paths {
		roots = append(roots, config.ExpandHome(p))
	}
	var all []repos.DiskClone
	seen := map[string]bool{}
	for i, r := range roots {
		depth := 4
		if i > 0 {
			depth = 3
		}
		for _, c := range repos.ListClones(ctx, []string{r}, depth) {
			if !seen[c.Path] {
				seen[c.Path] = true
				all = append(all, c)
			}
		}
	}
	s.disk, s.diskAt = all, time.Now()
	return all
}

// SetClonePath points repo at a clone on disk and saves it under repos.paths in the
// config file. The returned note is non-empty when the clone has no remote for repo.
func (s *Service) SetClonePath(repo, path string) (string, error) {
	path = config.ExpandHome(strings.TrimSpace(path))
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a folder", config.ShortenHome(path))
	}
	top, remote, err := repos.Inspect(path, repo)
	if err != nil {
		return "", fmt.Errorf("%s is not a git clone", config.ShortenHome(path))
	}
	if err := config.SaveRepoPath(s.cfgPath, repo, top); err != nil {
		return "", fmt.Errorf("saving to config: %w", err)
	}
	// A new map, not an edit: Rescan and ListClones may still be reading the old one.
	s.mu.Lock()
	paths := maps.Clone(s.cfg.Repos.Paths)
	if paths == nil {
		paths = map[string]string{}
	}
	paths[repo] = top
	s.cfg.Repos.Paths = paths
	s.mu.Unlock()
	s.Rescan()
	s.diskMu.Lock()
	s.disk = nil // pick up a clone that was outside the last scan
	s.diskMu.Unlock()
	note := ""
	if remote == "" {
		note = "no remote in it points at " + repo + "; commands will use origin"
	}
	return note, nil
}

// ClonePath is the clone currently used for repo, if any.
func (s *Service) ClonePath(repo string) string {
	c, _ := s.clone(repo)
	return c.Path
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
