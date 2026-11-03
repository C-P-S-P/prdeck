package repos

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func realClone(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func TestChangesAndBranch(t *testing.T) {
	dir := realClone(t, "https://github.com/acme/tool.git")
	if c := Changes(dir); len(c) != 0 {
		t.Fatalf("clean clone reports changes: %v", c)
	}
	if b := CurrentBranch(dir); b != "main" {
		t.Errorf("branch = %q", b)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644)
	if c := Changes(dir); len(c) != 2 {
		t.Errorf("changes = %q, want an edit and an untracked file", c)
	}
	if Rebasing(dir) {
		t.Error("not rebasing")
	}
}

func TestInspect(t *testing.T) {
	dir := realClone(t, "git@github.com:acme/tool.git")
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	top, remote, err := Inspect(sub, "acme/tool")
	if err != nil || remote != "origin" {
		t.Fatalf("top=%q remote=%q err=%v", top, remote, err)
	}
	if real, _ := filepath.EvalSymlinks(dir); top != real && top != dir {
		t.Errorf("top = %q, want %q", top, dir)
	}
	if _, remote, _ := Inspect(dir, "other/repo"); remote != "" {
		t.Errorf("remote for another repo = %q", remote)
	}
	if _, _, err := Inspect(t.TempDir(), "acme/tool"); err != ErrNotGit {
		t.Errorf("plain folder: err = %v", err)
	}
}

func TestFindClones(t *testing.T) {
	home := t.TempDir()
	fakeClone(t, filepath.Join(home, "work", "deep", "tool"), map[string]string{"origin": "https://github.com/acme/tool.git"})
	fakeClone(t, filepath.Join(home, "Library", "tool"), map[string]string{"origin": "https://github.com/acme/tool.git"})
	fakeClone(t, filepath.Join(home, "other"), map[string]string{"origin": "https://github.com/acme/other.git"})
	got := FindClones(context.Background(), "acme/tool", []string{home}, 4)
	if len(got) != 1 || got[0] != filepath.Join(home, "work", "deep", "tool") {
		t.Errorf("found %v (Library must be skipped)", got)
	}
}
