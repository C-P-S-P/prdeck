package repos

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := map[string]string{
		"https://github.com/feast-dev/feast.git":  "feast-dev/feast",
		"https://github.com/feast-dev/feast":      "feast-dev/feast",
		"https://user@github.com/a/b.git":         "a/b",
		"git@github.com:patelchaitany/prdeck.git": "patelchaitany/prdeck",
		"ssh://git@github.com/org/repo.name.git":  "org/repo.name",
		"https://gitlab.com/a/b.git":              "",
	}
	for in, want := range cases {
		got, ok := ParseRemote(in)
		if got != want || ok != (want != "") {
			t.Errorf("ParseRemote(%q) = %q, %v", in, got, ok)
		}
	}
}

func fakeClone(t *testing.T, dir string, remotes map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("[core]\n\tbare = false\n")
	for name, url := range remotes {
		b.WriteString("[remote \"" + name + "\"]\n\turl = " + url + "\n\tfetch = +refs/heads/*:refs/remotes/" + name + "/*\n")
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuild(t *testing.T) {
	root := t.TempDir()
	// A fork clone found first alphabetically: origin is the fork, upstream is the org repo.
	fakeClone(t, filepath.Join(root, "a-fork"), map[string]string{
		"origin":   "git@github.com:me/feast.git",
		"upstream": "https://github.com/feast-dev/feast.git",
	})
	// A direct clone of the org repo, nested one level deeper.
	fakeClone(t, filepath.Join(root, "work", "feast"), map[string]string{"origin": "https://github.com/feast-dev/feast.git"})
	fakeClone(t, filepath.Join(root, "tool"), map[string]string{"origin": "https://github.com/acme/tool.git"})

	ix := Build([]string{root}, map[string]string{"acme/other": filepath.Join(root, "tool")})

	c, ok := ix.Lookup("Feast-Dev/Feast")
	if !ok || c.Path != filepath.Join(root, "work", "feast") || c.Remote != "origin" {
		t.Errorf("feast-dev/feast -> %+v, %v (want the clone whose origin points at it)", c, ok)
	}
	if c, _ := ix.Lookup("me/feast"); c.Path != filepath.Join(root, "a-fork") {
		t.Errorf("me/feast -> %+v", c)
	}
	if c, ok := ix.Lookup("acme/other"); !ok || c.Path != filepath.Join(root, "tool") {
		t.Errorf("explicit path -> %+v, %v", c, ok)
	}
	if _, ok := ix.Lookup("nobody/nothing"); ok {
		t.Error("found a repo that does not exist")
	}
}

func TestUpstreamUsedWhenOnlyFork(t *testing.T) {
	root := t.TempDir()
	fakeClone(t, filepath.Join(root, "feast"), map[string]string{
		"origin":   "git@github.com:me/feast.git",
		"upstream": "https://github.com/feast-dev/feast.git",
	})
	c, ok := Build([]string{root}, nil).Lookup("feast-dev/feast")
	if !ok || c.Remote != "upstream" {
		t.Errorf("got %+v, %v; want remote upstream", c, ok)
	}
}

func TestWorktreePath(t *testing.T) {
	cases := map[string]string{
		"feast-dev/feast": "/w/feast-42",
		"a/../../etc":     "/w/etc-42",
		"a/weird name!":   "/w/weird-name-42",
	}
	for repo, want := range cases {
		if got := WorktreePath("/w", repo, 42); got != want {
			t.Errorf("WorktreePath(%q) = %q, want %q", repo, got, want)
		}
	}
}

// A worktree folder deleted by hand is still registered with git. BranchWorktree must
// not use it, and must change nothing (it runs on every selection); DropMissingWorktree,
// run before a checkout, frees the branch, also when the folder around it was deleted
// too (a temp folder cleaned up), but leaves a worktree on a disk that is not mounted
// right now alone.
func TestBranchWorktreeDeletedByHand(t *testing.T) {
	clone := realClone(t, "https://github.com/acme/tool.git")
	wt := filepath.Join(t.TempDir(), "tool-42")
	git(t, clone, "worktree", "add", "-q", "-b", "feat", wt)
	unmounted := filepath.Join(t.TempDir(), "Volumes", "Ext")
	other := filepath.Join(unmounted, "tool-43")
	git(t, clone, "worktree", "add", "-q", "-b", "other", other)
	scratch := filepath.Join(t.TempDir(), "session", "scratchpad")
	tmp := filepath.Join(scratch, "tool-44")
	git(t, clone, "worktree", "add", "-q", "-b", "temp", tmp)
	if got := BranchWorktree(clone, "feat"); got == "" {
		t.Fatal("worktree not found")
	}
	for _, dir := range []string{wt, unmounted, filepath.Dir(scratch)} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	listed := func() string {
		out, _ := exec.Command("git", "-C", clone, "worktree", "list").Output()
		return string(out)
	}
	if got := BranchWorktree(clone, "feat"); got != "" {
		t.Errorf("BranchWorktree = %q, a folder that is gone", got)
	}
	if !strings.Contains(listed(), "tool-42") {
		t.Error("BranchWorktree unregistered a worktree; looking up must change nothing")
	}
	DropMissingWorktree(clone, "feat")
	DropMissingWorktree(clone, "other")
	DropMissingWorktree(clone, "temp")
	if l := listed(); strings.Contains(l, "tool-42") || !strings.Contains(l, "tool-43") || strings.Contains(l, "tool-44") {
		t.Errorf("after DropMissingWorktree:\n%s", l)
	}
	git(t, clone, "checkout", "-q", "feat")
	git(t, clone, "checkout", "-q", "temp")
}

func TestOwnBranch(t *testing.T) {
	clone := realClone(t, "https://github.com/acme/tool.git")
	if OwnBranch(clone, "origin", "acme/tool", "main") {
		t.Error("true with the default branch unknown")
	}
	git(t, clone, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if !OwnBranch(clone, "origin", "acme/tool", "main") {
		t.Error("false for the default branch of a clone with only the base remote")
	}
	if OwnBranch(clone, "origin", "acme/tool", "feat") {
		t.Error("true for a branch that is not the default")
	}
	git(t, clone, "remote", "add", "fork", "git@github.com:alice/tool.git")
	if OwnBranch(clone, "origin", "acme/tool", "main") {
		t.Error("true with a remote that may be the fork")
	}
}
