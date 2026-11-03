// Package repos finds local clones of GitHub repos and manages per-PR worktrees.
package repos

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/MasterPushers/prdeck/internal/config"
)

// Clone is a local checkout and the remote in it that points at the GitHub repo.
type Clone struct {
	Path   string
	Remote string
}

// Index maps "owner/name" (lower case) to a local clone.
type Index struct {
	byRepo map[string]Clone
}

// Build scans the given folders (two levels deep) and adds explicit paths on top.
func Build(scan []string, explicit map[string]string) *Index {
	ix := &Index{byRepo: map[string]Clone{}}
	for _, dir := range scan {
		ix.scanDir(config.ExpandHome(dir), 2)
	}
	for repo, p := range explicit {
		p = config.ExpandHome(p)
		remote := "origin"
		for _, r := range readRemotes(filepath.Join(p, ".git", "config")) {
			if strings.EqualFold(r.repo, repo) {
				remote = r.name
				break
			}
		}
		ix.byRepo[strings.ToLower(repo)] = Clone{Path: p, Remote: remote}
	}
	return ix
}

func (ix *Index) scanDir(dir string, depth int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		gitDir := filepath.Join(p, ".git")
		if st, err := os.Stat(gitDir); err == nil && st.IsDir() {
			ix.add(p, readRemotes(filepath.Join(gitDir, "config")))
			continue
		}
		if depth > 1 {
			ix.scanDir(p, depth-1)
		}
	}
}

// add registers a clone under each GitHub repo its remotes point at. A clone whose origin
// points at the repo beats one that only has it as another remote (a fork's "upstream");
// otherwise the clone found first wins.
func (ix *Index) add(path string, remotes []remote) {
	for _, r := range remotes {
		key := strings.ToLower(r.repo)
		if old, ok := ix.byRepo[key]; ok && (old.Remote == "origin" || r.name != "origin") {
			continue
		}
		ix.byRepo[key] = Clone{Path: path, Remote: r.name}
	}
}

// Lookup returns the clone for "owner/name".
func (ix *Index) Lookup(repo string) (Clone, bool) {
	c, ok := ix.byRepo[strings.ToLower(repo)]
	return c, ok
}

func (ix *Index) Len() int { return len(ix.byRepo) }

type remote struct{ name, repo string }

var remoteHeader = regexp.MustCompile(`^\[remote "([^"]+)"\]$`)

// readRemotes reads [remote "x"] url = … entries from a .git/config file.
func readRemotes(gitConfig string) []remote {
	f, err := os.Open(gitConfig)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []remote
	current := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			current = ""
			if m := remoteHeader.FindStringSubmatch(line); m != nil {
				current = m[1]
			}
			continue
		}
		if current == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "url" {
			continue
		}
		if repo, ok := ParseRemote(strings.TrimSpace(v)); ok {
			out = append(out, remote{name: current, repo: repo})
		}
	}
	// Put origin first so it wins in add().
	for i, r := range out {
		if r.name == "origin" && i > 0 {
			out[0], out[i] = out[i], out[0]
		}
	}
	return out
}

var remotePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^https?://(?:[^@/]+@)?github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^(?:ssh://)?git@github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`),
}

// ParseRemote extracts "owner/name" from a GitHub remote URL.
func ParseRemote(url string) (string, bool) {
	for _, re := range remotePatterns {
		if m := re.FindStringSubmatch(url); m != nil {
			return m[1] + "/" + m[2], true
		}
	}
	return "", false
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// WorktreePath is <dir>/<name>-<number>. It is built from the repo name and PR number only,
// never the branch name, so a branch like "../../x" cannot escape the folder.
func WorktreePath(dir, repo string, number int) string {
	_, name, _ := strings.Cut(repo, "/")
	name = strings.Trim(unsafeChars.ReplaceAllString(name, "-"), ".-")
	if name == "" {
		name = "repo"
	}
	return filepath.Join(config.ExpandHome(dir), name+"-"+strconv.Itoa(number))
}

// BranchWorktree returns the worktree (or the clone itself) that has branch checked out.
func BranchWorktree(clone, branch string) string {
	out, err := exec.Command("git", "-C", clone, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	path := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = filepath.FromSlash(strings.TrimPrefix(line, "worktree ")) // git on Windows writes C:/x
		case line == "branch refs/heads/"+branch:
			return path
		}
	}
	return ""
}

// CurrentBranch returns the checked-out branch, or "HEAD" when detached (mid-rebase).
func CurrentBranch(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
