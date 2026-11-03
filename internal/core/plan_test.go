package core

import (
	"context"
	"errors"
	"fmt"
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

	// A big matrix failing names a few jobs, not a wall of them.
	matrix := &github.Detail{}
	for i := range 40 {
		matrix.Checks = append(matrix.Checks, github.Check{Name: fmt.Sprintf("test (%d)", i), State: "FAILURE", RunID: 11})
	}
	r = RerunFailed(p, matrix, func(int64) error { return nil })
	if r.Output != "Re-running the failed jobs of run 11: test (0), test (1), test (2) and 37 more" || r.Err != nil {
		t.Errorf("matrix: output = %q, err = %v", r.Output, r.Err)
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

func TestWritePlans(t *testing.T) {
	cases := []struct {
		cmd, input string
		d          *github.Detail
		want       string
	}{
		{"Approve", "", nil, "gh pr review 7 --approve -R acme/tool"},
		{"Request changes", "add a test", nil, "gh pr review 7 --request-changes --body 'add a test' -R acme/tool"},
		{"Comment", "it's fine; ship it", nil, `gh pr comment 7 --body 'it'\''s fine; ship it' -R acme/tool`},
		{"Merge when ready", "", nil, "gh pr merge 7 --auto -R acme/tool"},
		{"Merge when ready", "", &github.Detail{MergeStatus: "BLOCKED", MergeMethods: []string{"REBASE", "MERGE"}}, "gh pr merge 7 --auto --rebase -R acme/tool"},
		{"Merge when ready", "", &github.Detail{MergeStatus: "CLEAN", MergeMethods: []string{"SQUASH"}}, "gh pr merge 7 --squash -R acme/tool"},
	}
	for _, c := range cases {
		cmd := command(c.cmd)
		p := WritePlan(cmd, pr, c.d, c.input)
		if !Writes(p.Action) || p.Script != c.want || p.Input != c.input || p.Confirm != cmd.Confirm {
			t.Errorf("%s: plan = %+v", c.cmd, p)
		}
	}
	if Writes("rerun") || Writes("") || Writes("open") {
		t.Error("only the GitHub writes count")
	}
}

type fakeReviewer struct{ sent []string }

func (f *fakeReviewer) Comment(_ context.Context, id, body string) error {
	f.sent = append(f.sent, "comment "+id+" "+body)
	return nil
}

func (f *fakeReviewer) Review(_ context.Context, id, event, body string, threads []github.DraftThread) error {
	if err := github.CheckReview(event, body, threads); err != nil {
		return err
	}
	f.sent = append(f.sent, strings.TrimSpace(event+" "+id+" "+body))
	return nil
}

func TestPostReview(t *testing.T) {
	ctx := context.Background()
	r := &fakeReviewer{}
	for _, c := range []struct{ cmd, input, out string }{
		{"Approve", "", "Approved acme/tool#7"},
		{"Request changes", "add a test", "Requested changes on acme/tool#7"},
		{"Comment", "thanks", "Commented on acme/tool#7"},
	} {
		res := PostReview(ctx, WritePlan(command(c.cmd), pr, nil, c.input), "PR_7", r)
		if res.Err != nil || res.Output != c.out {
			t.Errorf("%s: %q %v", c.cmd, res.Output, res.Err)
		}
	}
	if strings.Join(r.sent, "|") != "APPROVE PR_7|REQUEST_CHANGES PR_7 add a test|comment PR_7 thanks" {
		t.Errorf("sent = %q", r.sent)
	}
	if res := PostReview(ctx, WritePlan(command("Request changes"), pr, nil, ""), "PR_7", r); res.Err == nil {
		t.Error("requesting changes without saying what should change was sent")
	}
}

func TestMergeWhenReady(t *testing.T) {
	open := github.Detail{PR: pr, MergeStatus: "BLOCKED", AutoMergeAllowed: true, MergeMethods: []string{"SQUASH", "MERGE"},
		CommitList: []github.Commit{{OID: "old"}, {OID: "head"}}}
	open.State = "OPEN"
	with := func(f func(d *github.Detail)) *github.Detail { d := open; f(&d); return &d }
	cases := []struct {
		name          string
		d             *github.Detail
		enableErr     error
		did, out, err string
	}{
		{"waits for checks and reviews", &open, nil, "enable SQUASH", "Auto-merge is on: GitHub will squash and merge acme/tool#7 once checks and reviews pass", ""},
		{"behind or still computing", with(func(d *github.Detail) { d.MergeStatus = "UNKNOWN" }), nil, "enable SQUASH", "Auto-merge is on", ""},
		{"ready: merges the checked commit now", with(func(d *github.Detail) { d.MergeStatus = "CLEAN" }), nil, "merge SQUASH head", "Merged acme/tool#7 (squash and merge): it was ready", ""},
		{"ready with hooks", with(func(d *github.Detail) { d.MergeStatus, d.MergeMethods = "HAS_HOOKS", []string{"REBASE"} }), nil, "merge REBASE head", "(rebase and merge)", ""},
		{"checks not passing, none required", with(func(d *github.Detail) { d.MergeStatus = "UNSTABLE" }), nil, "", "", "none of them is required"},
		{"conflicts", with(func(d *github.Detail) { d.MergeStatus, d.BaseRefName = "DIRTY", "main" }), nil, "", "", "acme/tool#7 has conflicts with main: rebase it first"},
		{"made a draft since the list loaded", with(func(d *github.Detail) { d.MergeStatus = "DRAFT" }), nil, "", "", "is a draft"},
		{"repo without auto-merge", with(func(d *github.Detail) { d.AutoMergeAllowed = false }), nil, "", "", `does not allow auto-merge (an admin can turn on "Allow auto-merge"`},
		{"ready in a repo without auto-merge still merges", with(func(d *github.Detail) { d.AutoMergeAllowed, d.MergeStatus = false, "CLEAN" }), nil, "merge SQUASH head", "Merged", ""},
		{"already on", with(func(d *github.Detail) { d.AutoMerge = &github.AutoMerge{Method: "MERGE", By: "mlin"} }), nil, "", "already on for acme/tool#7 (merge, turned on by mlin)", ""},
		{"already merged", with(func(d *github.Detail) { d.State = "MERGED" }), nil, "", "", "acme/tool#7 is already merged"},
		{"closed", with(func(d *github.Detail) { d.State = "CLOSED" }), nil, "", "", "is closed"},
		{"GitHub refuses", &open, errors.New("Pull request is in dirty status"), "enable SQUASH", "", "GitHub did not turn on auto-merge for acme/tool#7: Pull request is in dirty status"},
	}
	for _, c := range cases {
		var did []string
		r := MergeWhenReady(Plan{Action: "automerge"}, c.d,
			func(m string) error { did = append(did, "enable "+m); return c.enableErr },
			func(m, head string) error { did = append(did, "merge "+m+" "+head); return nil })
		got := ""
		if r.Err != nil {
			got = r.Err.Error()
		}
		if strings.Join(did, ",") != c.did || !strings.Contains(r.Output, c.out) || (c.err == "") != (r.Err == nil) || !strings.Contains(got, c.err) {
			t.Errorf("%s: did %v, output %q, err %v", c.name, did, r.Output, r.Err)
		}
	}
}

func TestAllowedSaysWhy(t *testing.T) {
	mine := pr
	mine.Author, mine.IsDraft = "you", true
	if _, why := Allowed(command("Approve"), mine, "you"); why != "Approve only runs on other people's PRs" {
		t.Errorf("why = %q", why)
	}
	if _, why := Allowed(command("Merge when ready"), mine, "you"); why != "Merge when ready only runs on PRs that are ready for review" {
		t.Errorf("why = %q", why)
	}
	if _, why := Allowed(command("Re-run failed"), mine, "you"); why != "Re-run failed only runs on PRs whose CI failed" {
		t.Errorf("why = %q", why)
	}
	if ok, _ := Allowed(command("Approve"), pr, "you"); !ok {
		t.Error("approving someone else's PR was refused")
	}
}
