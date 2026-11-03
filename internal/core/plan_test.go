package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/C-P-S-P/prdeck/internal/config"
	"github.com/C-P-S-P/prdeck/internal/github"
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

func TestRerunFailed(t *testing.T) {
	d := &github.Detail{PR: pr, Checks: []github.Check{
		{Name: "lint", State: "SUCCESS", RunID: 11},
		{Name: "unit", State: "FAILURE", RunID: 11},
		{Name: "e2e", State: "FAILURE", RunID: 12},
		{Name: "codecov/patch", State: "FAILURE"},
	}}
	p := RerunPlan(command("Re-run failed"), pr, d)
	if p.Action != "rerun" || p.Script != "gh run rerun 11 --failed -R acme/tool && gh run rerun 12 --failed -R acme/tool" {
		t.Errorf("plan = %+v", p)
	}
	if p := RerunPlan(command("Re-run failed"), pr, nil); p.Action != "rerun" || p.Script != "" {
		t.Errorf("plan without checks = %+v", p)
	}

	var ran []int64
	r := RerunFailed(p, d, func(id int64) error {
		ran = append(ran, id)
		if id == 12 {
			return errors.New("HTTP 403: This workflow is already running")
		}
		return nil
	})
	if len(ran) != 2 || ran[0] != 11 || ran[1] != 12 {
		t.Errorf("re-ran %v", ran)
	}
	want := "Re-running the failed jobs of run 11: unit\nSkipped codecov/patch: not a GitHub Actions job; re-run it from its own page"
	if r.Output != want || r.Err == nil || !strings.Contains(r.Err.Error(), "run 12: e2e (HTTP 403") {
		t.Errorf("output = %q, err = %v", r.Output, r.Err)
	}

	only := &github.Detail{Checks: []github.Check{{Name: "codecov/patch", State: "FAILURE"}}}
	r = RerunFailed(p, only, func(int64) error {
		t.Error("nothing should be re-run")
		return nil
	})
	if r.Err == nil || !strings.Contains(r.Output, "Skipped codecov/patch") {
		t.Errorf("only other CI: output = %q, err = %v", r.Output, r.Err)
	}
}

func TestRerunOnlyWhenCIFailed(t *testing.T) {
	c := command("Re-run failed")
	if ok, _ := Allowed(c, pr, "you"); ok {
		t.Error("re-run allowed on a PR whose CI did not fail")
	}
	failed := pr
	failed.CheckState = "FAILURE"
	if ok, _ := Allowed(c, failed, "you"); !ok {
		t.Error("re-run not allowed on a PR whose CI failed")
	}
}
