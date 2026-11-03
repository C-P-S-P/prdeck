package core

import (
	"strings"
	"testing"

	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/github"
)

func command(name string) config.Command {
	for _, c := range config.DefaultCommands() {
		if c.Name == name {
			return c
		}
	}
	panic(name)
}

var pr = github.PR{Number: 7, Repo: "acme/tool", URL: "https://github.com/acme/tool/pull/7", HeadRefName: "feat/x", BaseRefName: "main"}

func TestBuildPlan(t *testing.T) {
	cfg := config.Default()
	onMain := Folder{Path: "/w/tool", Clone: "/w/tool", Remote: "upstream", Exists: true, Branch: "main"}
	onPR := onMain
	onPR.Branch = "feat/x"
	dirty := onMain
	dirty.Changes = []string{" M a.go"}

	cases := []struct {
		name      string
		cmd       string
		folder    Folder
		wantPre   string
		wantMain  string
		wantDirty bool
	}{
		{"fetch never checks out", "Fetch", dirty, "", "git fetch upstream --prune", false},
		{"rebase switches first", "Rebase", onMain, "gh pr checkout https://github.com/acme/tool/pull/7", "git fetch upstream main && git rebase upstream/main", false},
		{"rebase on the branch", "Rebase", onPR, "", "git fetch upstream main && git rebase upstream/main", false},
		{"rebase with changes", "Rebase", dirty, "gh pr checkout https://github.com/acme/tool/pull/7", "git fetch upstream main && git rebase upstream/main", true},
		{"checkout switches", "Checkout", onMain, "gh pr checkout https://github.com/acme/tool/pull/7", "", false},
		{"checkout on the branch updates", "Checkout", onPR, "", "gh pr checkout https://github.com/acme/tool/pull/7", false},
		{"continue mid-rebase ignores changes", "Continue", Folder{Path: "/w", Exists: true, Branch: "HEAD", Changes: []string{"UU a.go"}, Rebasing: true}, "", "git -c core.editor=true rebase --continue", false},
		{"push needs no clean tree", "Push", Folder{Path: "/w", Exists: true, Branch: "feat/x", Changes: []string{" M a.go"}}, "", "git push --force-with-lease", false},
	}
	for _, c := range cases {
		p, err := BuildPlan(cfg, command(c.cmd), pr, c.folder, "")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.Pre != c.wantPre || p.Script != c.wantMain || (len(p.Changes) > 0) != c.wantDirty {
			t.Errorf("%s:\n pre=%q\nmain=%q\nchanges=%v", c.name, p.Pre, p.Script, p.Changes)
		}
	}
}

func TestStashAndDrop(t *testing.T) {
	cfg := config.Default()
	f := Folder{Path: "/w", Exists: true, Branch: "main", Changes: []string{" M a.go"}}
	p, _ := BuildPlan(cfg, command("Rebase"), pr, f, "")
	s := p.WithStash()
	if !strings.HasPrefix(s.Pre, "git stash push --include-untracked -m 'prdeck: before rebase on #7' && gh pr checkout") || s.Changes != nil {
		t.Errorf("stash: %q", s.Pre)
	}
	d := p.WithDrop()
	if !strings.HasPrefix(d.Pre, "git reset --hard --quiet && git clean -fd --quiet && gh pr checkout") {
		t.Errorf("drop: %q", d.Pre)
	}
	if !strings.HasSuffix(s.FullScript(), "git rebase origin/main") {
		t.Errorf("full: %q", s.FullScript())
	}
}

func TestOpenInClone(t *testing.T) {
	cfg := config.Default()
	f := Folder{Path: "/w/tool", Exists: true, Branch: "main"}
	p, err := BuildPlan(cfg, command("Open in editor"), pr, f, "")
	if err != nil || p.Script != "zed /w/tool" || !strings.HasPrefix(p.Pre, "gh pr checkout") || p.Folder.Worktree {
		t.Errorf("plan = %+v, err = %v", p, err)
	}
}

func TestOpenCommand(t *testing.T) {
	for goos, want := range map[string]string{
		"darwin":  "open",
		"linux":   "xdg-open",
		"freebsd": "xdg-open",
		"windows": "rundll32 url.dll,FileProtocolHandler",
	} {
		if got := openCommand(goos); got != want {
			t.Errorf("%s: %q, want %q", goos, got, want)
		}
	}
}
