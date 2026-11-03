package repos

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	top = strings.TrimSpace(string(out))
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

// FindClones walks roots (depth levels deep) for clones with a remote pointing at repo.
// It stops when ctx ends and returns what it found so far.
func FindClones(ctx context.Context, repo string, roots []string, depth int) []string {
	var found []string
	seen := map[string]bool{}
	var walk func(dir string, d int)
	walk = func(dir string, d int) {
		if ctx.Err() != nil {
			return
		}
		if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
			for _, r := range readRemotes(filepath.Join(dir, ".git", "config")) {
				if strings.EqualFold(r.repo, repo) && !seen[dir] {
					seen[dir] = true
					found = append(found, dir)
				}
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
