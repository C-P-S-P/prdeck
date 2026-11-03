package repos

import (
	"os"
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
