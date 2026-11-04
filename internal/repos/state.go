package repos

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Changes lists uncommitted changes in dir as `git status --porcelain` lines
// ("M  file.go", "?? notes.md"). Ignored files are not included.
func Changes(dir string) []string {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain=v1", "--untracked-files=normal").Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// Rebasing reports whether a rebase is in progress in dir.
func Rebasing(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return false
	}
	gitDir := strings.TrimSpace(string(out))
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if st, err := os.Stat(filepath.Join(gitDir, name)); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// ErrNotGit means a folder is not inside a git repository.
var ErrNotGit = errors.New("not a git repository")

// Inspect checks that path is a git clone and returns its top folder and the remote
// that points at repo ("" when none does).
func Inspect(path, repo string) (top, remote string, err error) {
	out, err := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", ErrNotGit
	}
	top = filepath.FromSlash(strings.TrimSpace(string(out))) // git on Windows writes C:/x
	for _, r := range readRemotes(filepath.Join(top, ".git", "config")) {
		if strings.EqualFold(r.repo, repo) {
			return top, r.name, nil
		}
	}
	return top, "", nil
}

// skipDirs are never searched for clones: they are large and never hold them.
var skipDirs = map[string]bool{
	"Library": true, "Applications": true, "Pictures": true, "Music": true, "Movies": true,
	"Public": true, "node_modules": true, "vendor": true, "venv": true, "__pycache__": true,
	"target": true, "dist": true, "build": true,
}

func init() {
	if runtime.GOOS == "windows" {
		skipDirs["AppData"] = true // hidden there by an attribute, not a leading dot
	}
}

// FindClones walks roots (depth levels deep) for clones with a remote pointing at repo.
// It stops when ctx ends and returns what it found so far.
func FindClones(ctx context.Context, repo string, roots []string, depth int) []string {
	var found []string
	for _, c := range ListClones(ctx, roots, depth) {
		for _, r := range c.Repos {
			if strings.EqualFold(r, repo) {
				found = append(found, c.Path)
				break
			}
		}
	}
	return found
}

// DiskClone is a git clone found on disk and the GitHub repos its remotes point at.
type DiskClone struct {
	Path  string   `json:"path"`
	Repos []string `json:"repos"`
}

// ListClones walks roots (depth levels deep) for every git clone. It stops when ctx
// ends and returns what it found so far.
func ListClones(ctx context.Context, roots []string, depth int) []DiskClone {
	var found []DiskClone
	seen := map[string]bool{}
	var walk func(dir string, d int)
	walk = func(dir string, d int) {
		if ctx.Err() != nil {
			return
		}
		if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
			if !seen[dir] {
				seen[dir] = true
				c := DiskClone{Path: dir}
				for _, r := range readRemotes(filepath.Join(dir, ".git", "config")) {
					c.Repos = append(c.Repos, r.repo)
				}
				found = append(found, c)
			}
			return // don't look inside clones
		}
		if d == 0 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || skipDirs[name] {
				continue
			}
			walk(filepath.Join(dir, name), d-1)
		}
	}
	for _, r := range roots {
		walk(r, depth)
	}
	return found
}
