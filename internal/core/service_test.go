package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MasterPushers/prdeck/internal/cache"
	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/github"
)

func TestOpeningMarksSeen(t *testing.T) {
	c := cache.Open(filepath.Join(t.TempDir(), "cache.json"))
	s := New(&config.Config{}, "", nil, c)
	start := time.Now()
	pr := github.PR{Number: 7, Repo: "acme/tool", UpdatedAt: start.Add(2 * time.Minute)}
	c.PutSearch("involves:@me", "you", []github.PR{pr})
	c.PutDetail(&github.Detail{PR: pr, Timeline: []github.Event{
		{Kind: "comment", Author: "mlin", At: start.Add(time.Minute)},
		{Kind: "comment", Author: "you", At: start.Add(2 * time.Minute)},
	}})

	if e, _ := s.CachedSearch("involves:@me"); !e.PRs[0].Unread {
		t.Fatal("mlin commented after PR Deck started: the PR should be unread")
	}
	d, _ := s.CachedDetail(pr)
	if !d.Timeline[0].New || d.Timeline[1].New {
		t.Errorf("mlin's comment is new, yours is not: %+v", d.Timeline)
	}
	if e, _ := s.CachedSearch("involves:@me"); e.PRs[0].Unread {
		t.Error("opened: no longer unread")
	}
	if d, _ := s.CachedDetail(pr); d.Timeline[0].New || !d.LastSeen.Equal(pr.UpdatedAt) {
		t.Errorf("opened again: nothing is new since %v: %+v", d.LastSeen, d.Timeline)
	}
	if e, _ := c.Search("involves:@me"); e.PRs[0].Unread {
		t.Error("the saved list was changed")
	}
}

func TestCommentAfterUpdateTimeIsNewOnce(t *testing.T) {
	c := cache.Open(filepath.Join(t.TempDir(), "cache.json"))
	s := New(&config.Config{}, "", nil, c)
	pr := github.PR{Number: 8, Repo: "acme/tool", UpdatedAt: time.Now().Add(time.Minute)}
	// A review dated after the PR's update time, as in the demo's #4807.
	c.PutDetail(&github.Detail{PR: pr, Timeline: []github.Event{{Kind: "review", Author: "mlin", At: pr.UpdatedAt.Add(time.Hour)}}})
	if d, _ := s.CachedDetail(pr); !d.Timeline[0].New {
		t.Fatal("first visit: the review is new")
	}
	if d, _ := s.CachedDetail(pr); d.Timeline[0].New {
		t.Error("seen once, it must not stay new on every visit")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// realClone is a git repo with one commit on main and origin pointing at remote.
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

// fakeGh puts a gh on PATH that prints msg and exits 1.
func fakeGh(t *testing.T, msg string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho '"+msg+"' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// When gh pr checkout fails in a new worktree, the worktree must go: left behind, it
// would be opened next time as it is, without the PR's branch.
func TestFailedWorktreeIsRemoved(t *testing.T) {
	fakeGh(t, "checkout failed")
	clone := realClone(t, "https://github.com/acme/tool.git")
	wt := filepath.Join(t.TempDir(), "tool-7")
	f := Folder{Path: wt, Clone: clone, Remote: ".", Worktree: true}
	if _, err := (&Service{}).createWorktree(context.Background(), pr, f); err == nil {
		t.Fatal("no error from a failed checkout")
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("the half-made worktree is still there")
	}
}

// A worktree made meanwhile (a second request, or a plan run again after it worked)
// makes git worktree add fail. That worktree is not this call's to remove.
func TestFailedAddKeepsWorktree(t *testing.T) {
	clone := realClone(t, "https://github.com/acme/tool.git")
	wt := filepath.Join(t.TempDir(), "tool-7")
	git(t, clone, "worktree", "add", "-q", "--detach", wt)
	if err := os.WriteFile(filepath.Join(wt, "work.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := Folder{Path: wt, Clone: clone, Remote: ".", Worktree: true}
	if _, err := (&Service{}).createWorktree(context.Background(), pr, f); err == nil {
		t.Fatal("no error from adding a worktree that is there")
	}
	if _, err := os.Stat(filepath.Join(wt, "work.txt")); err != nil {
		t.Errorf("the existing worktree was removed: %v", err)
	}
}

// An editor command that fails at once (not installed) must be reported, not
// "Opened". One that keeps running is left running.
func TestOpenReportsFailedEditor(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // where the editor's output goes
	open := func(script string) Result {
		return (&Service{}).Execute(context.Background(), Plan{Action: "open", Script: script, Folder: Folder{Path: t.TempDir(), Exists: true}})
	}
	if r := open("no-such-editor-prdeck ."); r.Err == nil || !strings.Contains(r.Output, "not found") {
		t.Errorf("missing editor: err=%v output=%q", r.Err, r.Output)
	}
	if r := open("true"); r.Err != nil || !strings.HasPrefix(r.Output, "Opened ") {
		t.Errorf("editor: err=%v output=%q", r.Err, r.Output)
	}
	start := time.Now()
	if r := open("sleep 3"); r.Err != nil || !strings.HasPrefix(r.Output, "Opened ") || time.Since(start) > 2*time.Second {
		t.Errorf("editor left running: err=%v output=%q after %v", r.Err, r.Output, time.Since(start))
	}
	if runtime.GOOS == "windows" {
		// Windows cannot remove the folder a running program is in: let it end first.
		t.Cleanup(func() { time.Sleep(time.Until(start.Add(3500 * time.Millisecond))) })
	}
	// Nothing is left in the temp folder if prdeck quits before the editor does.
	if left, _ := os.ReadDir(tmp); len(left) > 0 {
		t.Errorf("left in the temp folder: %v", left)
	}
}

// prdeck serve runs requests at once: setClonePath must not change the repo paths
// while listClones or a rescan reads them.
func TestSetClonePathWhileScanning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home folder on Windows
	clone := realClone(t, "https://github.com/acme/tool.git")
	cfg := config.Default()
	cfg.Repos.Scan, cfg.Repos.Paths = nil, map[string]string{"acme/tool": clone}
	s := New(cfg, filepath.Join(home, "config.yml"), nil, nil)
	done := make(chan bool)
	go func() {
		for i := 0; i < 20; i++ {
			s.Rescan()
			s.ListClones(context.Background())
		}
		close(done)
	}()
	for i := 0; i < 20; i++ {
		if _, err := s.SetClonePath("acme/r"+strconv.Itoa(i), clone); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if s.ClonePath("acme/r19") == "" || s.ClonePath("acme/tool") == "" {
		t.Errorf("paths = %v", s.Config().Repos.Paths)
	}
}

// alice:main -> acme:main. gh checks it out as alice/main, so the maintainer's own main
// is never this PR's branch, and w must make a worktree, not open the clone.
func TestForkPRFromMain(t *testing.T) {
	clone := realClone(t, "https://github.com/acme/tool.git")
	git(t, clone, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	cfg := config.Default()
	cfg.Repos.Scan, cfg.Editor.WorktreeDir = nil, t.TempDir()
	cfg.Repos.Paths = map[string]string{"acme/tool": clone}
	s := New(cfg, "", nil, nil)
	fork := github.PR{Number: 9, Repo: "acme/tool", URL: "https://github.com/acme/tool/pull/9", HeadRefName: "main", BaseRefName: "main", IsCrossRepo: true}

	if f, _ := s.ResolveFolder(fork, true); f.Path != filepath.Join(cfg.Editor.WorktreeDir, "tool-9") {
		t.Errorf("w: path=%q, want a new worktree", f.Path)
	}
	push := func(pr github.PR) string {
		f, err := s.ResolveFolder(pr, false)
		if err != nil {
			t.Fatal(err)
		}
		p, err := BuildPlan(cfg, command("Push"), pr, f, "")
		if err != nil {
			t.Fatal(err)
		}
		return p.Pre
	}
	if pre := push(fork); pre != "gh pr checkout https://github.com/acme/tool/pull/9" {
		t.Errorf("push: pre=%q, want a checkout first", pre)
	}
	// The same branch names within one repo: main is the PR's branch.
	same := fork
	same.IsCrossRepo = false
	if pre := push(same); pre != "" {
		t.Errorf("push in the same repo: pre=%q", pre)
	}
	// With a remote that may be the fork (the author's own clone), gh checks the PR
	// out as main, so main is the PR's branch.
	git(t, clone, "remote", "add", "alice", "git@github.com:alice/tool.git")
	if pre := push(fork); pre != "" {
		t.Errorf("push with the fork as a remote: pre=%q", pre)
	}
}

// Worktrees are named <name>-<number>, so acme/api#12 and globex/api#12 want the same
// folder. One repo's PR must never run in the other repo's worktree.
func TestWorktreeOfSameNamedRepo(t *testing.T) {
	acme := realClone(t, "https://github.com/acme/api.git")
	globex := realClone(t, "https://github.com/globex/api.git")
	dir := t.TempDir()
	git(t, acme, "worktree", "add", "-q", "--detach", filepath.Join(dir, "api-12"))
	cfg := config.Default()
	cfg.Repos.Scan, cfg.Editor.WorktreeDir = nil, dir
	cfg.Repos.Paths = map[string]string{"acme/api": acme, "globex/api": globex}
	s := New(cfg, "", nil, nil)

	cases := []struct {
		repo   string
		number int
		want   string
		exists bool
	}{
		{"acme/api", 12, "api-12", true},
		{"globex/api", 12, "globex-api-12", false},
		{"globex/api", 13, "api-13", false},
	}
	for _, c := range cases {
		f, err := s.ResolveFolder(github.PR{Repo: c.repo, Number: c.number, HeadRefName: "feat"}, true)
		if err != nil || f.Path != filepath.Join(dir, c.want) || f.Exists != c.exists {
			t.Errorf("%s#%d: path=%q exists=%v err=%v, want %s", c.repo, c.number, f.Path, f.Exists, err, c.want)
		}
	}
}

// Every placeholder the config knows (and warns about when quoted) is filled, and
// nothing is filled that the config does not know.
func TestPlaceholdersMatchConfig(t *testing.T) {
	got := values(github.PR{}, Folder{}, "")
	if len(got) != len(config.Placeholders) {
		t.Errorf("values fills %d placeholders, config lists %d", len(got), len(config.Placeholders))
	}
	for _, p := range config.Placeholders {
		if _, ok := got[p]; !ok {
			t.Errorf("config lists {%s} but values does not fill it", p)
		}
	}
}

// A fork PR from the base's default branch is checked out by gh as <owner>/main. On
// that branch, prdeck is on the PR: no checkout, and edits are not "in the way".
// On the user's own main, it must switch.
func TestForkPRFromMainCountsAsCheckedOut(t *testing.T) {
	cfg := config.Default()
	push := config.Command{Name: "Push", Key: "p", Run: "git push", Branch: true}
	pr := github.PR{Number: 7, Repo: "acme/api", URL: "https://github.com/acme/api/pull/7", HeadRefName: "main", HeadOwner: "rdavis", IsCrossRepo: true}
	on := Folder{Path: "/x", Clone: "/x", Exists: true, OwnBranch: true, Branch: "rdavis/main", Changes: []string{" M a.go"}}
	p, err := BuildPlan(cfg, push, pr, on, "")
	if err != nil || p.Pre != "" || len(p.Changes) != 0 {
		t.Errorf("on rdavis/main: pre=%q changes=%v err=%v", p.Pre, p.Changes, err)
	}
	own := on
	own.Branch = "main"
	if p, _ := BuildPlan(cfg, push, pr, own, ""); !strings.Contains(p.Pre, "gh pr checkout") || len(p.Changes) == 0 {
		t.Errorf("on the user's own main: pre=%q changes=%v", p.Pre, p.Changes)
	}
	pr.HeadOwner = "" // an older cached PR: the local name is unknown, so always check out
	if p, _ := BuildPlan(cfg, push, pr, on, ""); !strings.Contains(p.Pre, "gh pr checkout") {
		t.Errorf("without the head owner: pre=%q", p.Pre)
	}
}

func TestCappedBufferKeepsTheStart(t *testing.T) {
	var b cappedBuffer
	big := strings.Repeat("x", 100<<10)
	if n, _ := b.Write([]byte("start ")); n != 6 {
		t.Fatal(n)
	}
	if n, _ := b.Write([]byte(big)); n != len(big) {
		t.Errorf("Write reported %d of %d bytes", n, len(big))
	}
	if s := b.String(); len(s) != 64<<10 || !strings.HasPrefix(s, "start ") {
		t.Errorf("kept %d bytes", len(s))
	}
}

// A worktree folder chosen in the app is saved and used for the next worktree; one
// inside a clone is refused, since every worktree would show up in it.
func TestSetWorktreeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home folder on Windows
	clone := realClone(t, "https://github.com/acme/tool.git")
	cfg := config.Default()
	cfg.Repos.Scan, cfg.Repos.Paths = nil, map[string]string{"acme/tool": clone}
	file := filepath.Join(home, "config.yml")
	s := New(cfg, file, nil, nil)
	if _, err := s.SetWorktreeDir(filepath.Join(clone, "trees")); err == nil {
		t.Error("a folder inside a clone was accepted")
	}
	got, err := s.SetWorktreeDir(filepath.Join(home, "trees"))
	short := "~" + string(filepath.Separator) + "trees"
	if err != nil || got != short {
		t.Fatalf("SetWorktreeDir = %q, %v", got, err)
	}
	if saved, _ := os.ReadFile(file); !strings.Contains(string(saved), "worktree_dir: "+short) {
		t.Errorf("config file:\n%s", saved)
	}
	f, err := s.ResolveFolder(github.PR{Repo: "acme/tool", Number: 7}, true)
	if err != nil || f.Path != filepath.Join(home, "trees", "tool-7") || f.Exists {
		t.Errorf("ResolveFolder = %+v, %v", f, err)
	}
}
