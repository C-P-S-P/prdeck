package tui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MasterPushers/prdeck/internal/cache"
	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/core"
	"github.com/MasterPushers/prdeck/internal/github"
	"github.com/MasterPushers/prdeck/internal/repos"
)

type fakeBackend struct {
	cfg      *config.Config
	prs      []github.PR
	searched []string
	ran      []core.Plan
	branch   string   // checked out in every fake clone ("" = the PR's own branch)
	changes  []string // uncommitted changes in every fake clone
	noClone  map[string]bool
	found    []string
	saved    map[string]string
	loaded   int  // Detail calls
	news     bool // Detail marks mlin's thread new since your last visit
}

func (f *fakeBackend) Config() *config.Config { return f.cfg }
func (f *fakeBackend) Viewer() string         { return "you" }
func (f *fakeBackend) CachedSearch(string) (cache.SearchEntry, bool) {
	return cache.SearchEntry{}, false
}
func (f *fakeBackend) Search(_ context.Context, q string) ([]github.PR, error) {
	f.searched = append(f.searched, q)
	return f.prs, nil
}
func (f *fakeBackend) CachedDetail(github.PR) (*github.Detail, bool) { return nil, false }
func (f *fakeBackend) Detail(_ context.Context, pr github.PR) (*github.Detail, error) {
	f.loaded++
	return &github.Detail{
		PR: pr, Body: "## Summary\nAdds a `StreamPushSource`.", Commits: 6, ChangedFiles: 14,
		Checks:    []github.Check{{Name: "lint", State: "SUCCESS"}, {Name: "integration", State: "FAILURE", URL: "https://github.com/northwind/registry/actions/runs/5/job/9", RunID: 5}},
		Reviewers: []github.Reviewer{{Login: "mlin", State: "CHANGES_REQUESTED"}},
		Timeline: []github.Event{
			{Kind: "review", Author: "mlin", State: "CHANGES_REQUESTED", Body: "One thing first.", At: time.Now().Add(-2 * time.Hour)},
			{Kind: "thread", Author: "mlin", Path: "push.py", Line: 41, DiffHunk: "@@ -1 +1 @@\n+def push():", Body: "Default to False?", At: time.Now().Add(-time.Hour), New: f.news},
		},
	}, nil
}
func (f *fakeBackend) ResolveFolder(pr github.PR, wantWorktree bool) (core.Folder, error) {
	if f.noClone[pr.Repo] && f.saved[pr.Repo] == "" {
		return core.Folder{}, core.ErrNoClone
	}
	branch := f.branch
	if branch == "" {
		branch = pr.HeadRefName
	}
	if wantWorktree {
		return core.Folder{Path: "/wt/" + pr.Name(), Clone: "/w/" + pr.Name(), Worktree: true}, nil
	}
	return core.Folder{Path: "/w/" + pr.Name(), Clone: "/w/" + pr.Name(), Remote: "origin", Exists: true, Branch: branch, Changes: f.changes}, nil
}
func (f *fakeBackend) Prepare(c config.Command, pr github.PR, input string) (core.Plan, error) {
	if c.Action == "browse" {
		return core.Plan{Title: c.Name, Action: "browse", Script: "open " + pr.URL, PR: pr}, nil
	}
	if c.Action == "rerun" {
		if ok, why := core.Allowed(c, pr, "you"); !ok {
			return core.Plan{}, errors.New(why)
		}
		return core.RerunPlan(c, pr, nil), nil
	}
	if core.Writes(c.Action) {
		if ok, why := core.Allowed(c, pr, "you"); !ok {
			return core.Plan{}, errors.New(why)
		}
		return core.WritePlan(c, pr, nil, input), nil
	}
	folder, err := f.ResolveFolder(pr, c.Worktree)
	if err != nil {
		return core.Plan{}, err
	}
	return core.BuildPlan(f.cfg, c, pr, folder, input)
}
func (f *fakeBackend) ClonePlan(pr github.PR) core.Plan {
	return core.Plan{Title: "Clone", Action: "clone", Script: "gh repo clone " + pr.Repo + " /w/" + pr.Name(), Confirm: true}
}
func (f *fakeBackend) Execute(_ context.Context, p core.Plan) core.Result {
	f.ran = append(f.ran, p)
	return core.Result{Plan: p, Output: "ok"}
}
func (f *fakeBackend) Setup(context.Context, core.Plan) (string, error) { return "", nil }
func (f *fakeBackend) TerminalCmd(core.Plan) *exec.Cmd                  { return exec.Command("true") }
func (f *fakeBackend) FindClones(context.Context, string) []string      { return f.found }
func (f *fakeBackend) SetClonePath(repo, path string) (string, error) {
	if f.saved == nil {
		f.saved = map[string]string{}
	}
	f.saved[repo] = path
	return "", nil
}
func (f *fakeBackend) ClonePath(repo string) string { return f.saved[repo] }
func (f *fakeBackend) ListClones(context.Context) []repos.DiskClone {
	return []repos.DiskClone{
		{Path: "/home/me/projects/odh-feast", Repos: []string{"opendatahub-io/feast"}},
		{Path: "/home/me/projects/registry", Repos: []string{"northwind/registry"}},
		{Path: "/home/me/projects/registry-old", Repos: []string{"me/registry"}},
		{Path: "/home/me/projects/cp", Repos: []string{"me/cp"}},
	}
}

func newTest(t *testing.T) (*fakeBackend, Model) {
	t.Helper()
	now := time.Now()
	be := &fakeBackend{cfg: config.Default(), prs: []github.PR{
		{Number: 4821, Title: "Add streaming push source", Repo: "northwind/registry", URL: "https://github.com/northwind/registry/pull/4821", Author: "you", HeadRefName: "feat/push", BaseRefName: "main", CheckState: "FAILURE", ReviewDecision: "CHANGES_REQUESTED", UpdatedAt: now.Add(-18 * time.Minute)},
		{Number: 312, Title: "Reconcile secrets on rotation", Repo: "northwind/operator", Author: "you", CheckState: "SUCCESS", ReviewDecision: "APPROVED", UpdatedAt: now.Add(-3 * time.Hour)},
		{Number: 77, Title: "Typed client", Repo: "northwind/sdk", Author: "mlin", IsDraft: true, UpdatedAt: now.Add(-24 * time.Hour)},
	}}
	m := New(be)
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	return be, m
}

// send runs one message, then feeds back the messages its commands produce (two levels
// deep). Timers (refresh, debounce, spinner) are skipped: they would block the test.
func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	queue := []tea.Msg{msg}
	for i := 0; i < len(queue) && i < 30; i++ {
		nm, cmd := m.Update(queue[i])
		m = nm.(Model)
		queue = append(queue, collect(cmd, 3)...)
	}
	return m
}

func collect(cmd tea.Cmd, depth int) []tea.Msg {
	if cmd == nil || depth == 0 {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(100 * time.Millisecond):
		return nil // a timer
	}
	switch msg := msg.(type) {
	case nil, spinner.TickMsg:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, collect(c, depth-1)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func search(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(searchMsg{seq: m.seq, prs: m.be.(*fakeBackend).prs})
	return nm.(Model)
}

func TestListAndDetail(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	view := ansi.Strip(m.View())
	for _, want := range []string{"Repositories", "All repositories", "northwind/registry", "#4821", "Add streaming push source", "1 Involves me", "fetch", "rebase", "open in editor"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
	pr, _ := m.selected()
	d, _ := m.be.Detail(context.Background(), pr)
	nm, _ := m.Update(detailMsg{key: pr.Key(), d: d})
	m = nm.(Model)
	view = ansi.Strip(m.View())
	for _, want := range []string{"Summary", "StreamPushSource", "mlin requested changes", "push.py:41", "failing: integration"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail missing %q\n%s", want, view)
		}
	}
}

func TestFailingChecksLinkAndRerun(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	pr, _ := m.selected()
	d, _ := m.be.Detail(context.Background(), pr)
	d.Checks = append(d.Checks, github.Check{Name: "deploy", State: "ACTION_REQUIRED"})
	nm, _ := m.Update(detailMsg{key: pr.Key(), d: d})
	m = nm.(Model)
	link := "\x1b]8;;https://github.com/northwind/registry/actions/runs/5/job/9\x1b\\"
	if !strings.Contains(m.View(), link) {
		t.Error("the failing check should link to its log")
	}
	// Every check counted in ✕ is named, the same rule as the count.
	if !strings.Contains(ansi.Strip(m.View()), "failing: integration, deploy") {
		t.Error("the failing line should name every failed check")
	}
	if !strings.Contains(ansi.Strip(m.View()), "F re-run failed") {
		t.Error("the failing line should say which key re-runs the checks")
	}
	m = send(t, m, key("F"))
	if len(be.ran) != 1 || be.ran[0].Action != "rerun" || be.ran[0].PR.Number != 4821 {
		t.Fatalf("ran = %+v", be.ran)
	}

	m = send(t, m, key("j")) // #312, CI passed
	m = send(t, m, key("F"))
	if len(be.ran) != 1 || !strings.Contains(m.flash, "only runs on PRs that are ci failed") {
		t.Errorf("re-run on a green PR: ran %d, flash %q", len(be.ran), m.flash)
	}
}

func TestApproveAsksFirstThenReloads(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("a")) // #4821 is yours
	if m.plan != nil || !strings.Contains(m.flash, "Approve only runs on other people's PRs") {
		t.Fatalf("approving your own PR: plan %+v, flash %q", m.plan, m.flash)
	}
	m = send(t, m, key("j"))
	m = send(t, m, key("j")) // #77, mlin's
	m = send(t, m, key("a"))
	if m.plan == nil || m.plan.Script != "gh pr review 77 --approve -R northwind/sdk" || len(be.ran) != 0 {
		t.Fatalf("approve should show what it does first: %+v", m.plan)
	}
	before := be.loaded
	m = send(t, m, key("enter"))
	if len(be.ran) != 1 || be.ran[0].Action != "approve" || m.out == nil || !m.out.ok {
		t.Fatalf("ran = %+v, out = %+v", be.ran, m.out)
	}
	if be.loaded == before || m.fetching[be.ran[0].PR.Key()] {
		t.Error("the PR was not loaded again after approving")
	}
}

func TestReviewTextAsked(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("x")) // your own PR: refused before any typing
	if m.prompt != nil || !strings.Contains(m.flash, "other people's PRs") {
		t.Fatalf("prompt %v, flash %q", m.prompt, m.flash)
	}
	m = send(t, m, key("L")) // the path prompt has its own hint
	m = send(t, m, key("esc"))
	m = send(t, m, key("i"))
	if m.prompt == nil || !strings.Contains(ansi.Strip(m.View()), "Comment:") {
		t.Fatal("comment should ask for its text")
	}
	if strings.Contains(ansi.Strip(m.View()), "folder name") {
		t.Error("the comment prompt kept the path prompt's hint")
	}
	for _, r := range "rebased, ptal" {
		m = send(t, m, key(string(r)))
	}
	m = send(t, m, key("enter"))
	if len(be.ran) != 1 || be.ran[0].Action != "comment" || be.ran[0].Input != "rebased, ptal" {
		t.Errorf("ran = %+v", be.ran)
	}
}

func TestCommentGoesToThePRItWasTypedFor(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("i")) // on #4821
	for _, r := range "lgtm" {
		m = send(t, m, key(string(r)))
	}
	// A refresh while typing: #4821 was merged and left the list.
	nm, _ := m.Update(searchMsg{seq: m.seq, prs: be.prs[1:]})
	m = send(t, nm.(Model), key("enter"))
	if len(be.ran) != 1 || be.ran[0].PR.Number != 4821 {
		t.Errorf("the comment went to %+v", be.ran)
	}
}

func TestWriteAnswerWrapsAndBarDims(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	m = send(t, m, tea.WindowSizeMsg{Width: 60, Height: 30})
	why := "upstream/datakit does not allow auto-merge (an admin can turn on \"Allow auto-merge\" in its settings). Merge it on GitHub once it is ready"
	nm, _ := m.Update(resultMsg(core.Result{Plan: core.Plan{Title: "Merge when ready", Action: "automerge"}, Err: errors.New(why)}))
	m = nm.(Model)
	v := strings.Join(strings.Fields(ansi.Strip(m.View())), " ")
	if !strings.Contains(v, "once it is ready") {
		t.Errorf("GitHub's answer was cut:\n%s", ansi.Strip(m.View()))
	}
	// #4821 is yours: Approve is on the bar but dimmed; Comment is not.
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(0) // termenv.TrueColor; tests have no colors otherwise
	defer lipgloss.SetColorProfile(old)
	bar := strings.Join(m.keybar(), "\n")
	if !strings.Contains(bar, sDim.Render("approve")) || strings.Contains(bar, sDim.Render("comment")) {
		t.Errorf("bar:\n%q", bar)
	}
}

func TestMergeWhenReadyAndAutoMergeShown(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("M"))
	if m.plan == nil || m.plan.Action != "automerge" || !strings.Contains(m.plan.Script, "gh pr merge 4821 --auto") || len(be.ran) != 0 {
		t.Fatalf("merge when ready should ask first: %+v", m.plan)
	}
	m = send(t, m, key("esc"))
	m = send(t, m, key("j"))
	m = send(t, m, key("j")) // #77 is a draft
	m = send(t, m, key("M"))
	if m.plan != nil || !strings.Contains(m.flash, "ready for review") {
		t.Errorf("draft: plan %+v, flash %q", m.plan, m.flash)
	}

	pr, _ := m.selected()
	d, _ := m.be.Detail(context.Background(), pr)
	d.AutoMerge = &github.AutoMerge{Method: "SQUASH", By: "mlin"}
	nm, _ := m.Update(detailMsg{key: pr.Key(), d: d})
	m = nm.(Model)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "merge    auto-merge on · squash and merge · by mlin") {
		t.Errorf("auto-merge not shown:\n%s", v)
	}
	m = send(t, m, key("?"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Merge when ready") || !strings.Contains(v, "approves the PR on GitHub") {
		t.Errorf("help should list the review commands:\n%s", v)
	}
}

func TestUnreadAndJumpToNew(t *testing.T) {
	be, m := newTest(t)
	be.news = true
	be.prs[1].Unread = true // #312
	m = search(t, m)
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 20})
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(0) // termenv.TrueColor; tests have no colors otherwise
	defer lipgloss.SetColorProfile(old)
	if row := m.prRow(m.prs[1], 1, 56, 5); !strings.Contains(row, sBlue.Render("●")) || !strings.Contains(row, sBold.Render(m.prs[1].Title)) {
		t.Errorf("an unread PR has a blue dot and a bold title: %q", row)
	}
	if row := m.prRow(m.prs[2], 2, 56, 5); !strings.Contains(row, sDim.Render("◌")) || strings.Contains(row, sBold.Render(m.prs[2].Title)) {
		t.Errorf("a read draft looks as before: %q", row)
	}

	m = send(t, m, key("j"))
	if m.prs[1].Unread {
		t.Error("opening #312 should clear its dot")
	}
	pr, _ := m.selected()
	d, _ := be.Detail(context.Background(), pr)
	m = send(t, m, detailMsg{key: pr.Key(), d: d})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "1 since you last looked") || strings.Contains(ansi.Strip(m.vp.View()), "push.py:41") {
		t.Fatalf("the new thread is below; the header says so:\n%s", v)
	}
	m = send(t, m, key("n"))
	if m.focus != paneDetail || !strings.Contains(ansi.Strip(m.vp.View()), "mlin on push.py:41 · 1h ago  new") {
		t.Errorf("n should scroll to the new thread:\n%s", ansi.Strip(m.vp.View()))
	}

	be.news = false
	m = send(t, m, key("esc")) // back to the list
	m = send(t, m, key("j"))
	pr, _ = m.selected()
	d, _ = be.Detail(context.Background(), pr)
	m = send(t, m, detailMsg{key: pr.Key(), d: d})
	m = send(t, m, key("n"))
	if m.flash != "Nothing new since you last looked" {
		t.Errorf("n on #%d with nothing new: flash %q", pr.Number, m.flash)
	}

	// A command of yours on n still runs.
	be.cfg.Commands = append(be.cfg.Commands, config.Command{Name: "Notes", Key: "n", Run: "echo notes"})
	m = send(t, m, key("n"))
	if len(be.ran) != 1 || be.ran[0].Title != "Notes" {
		t.Errorf("your n command should run: %+v", be.ran)
	}
}

func TestMoveAndRepoFilter(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("j"))
	if pr, _ := m.selected(); pr.Number != 312 {
		t.Fatalf("selected #%d after j", pr.Number)
	}
	m = send(t, m, key("h")) // to repos pane
	m = send(t, m, key("j")) // first real repo
	if m.repoSel == "" || len(m.visible()) != 1 {
		t.Fatalf("repo filter not applied: sel=%q visible=%d", m.repoSel, len(m.visible()))
	}
}

func TestFilterSwitchSearches(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("2"))
	if m.query() != "author:@me" {
		t.Fatalf("query = %q", m.query())
	}
	if len(be.searched) == 0 || be.searched[len(be.searched)-1] != "author:@me" {
		t.Errorf("searched = %v", be.searched)
	}
}

func TestPaletteCustomQuery(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("/"))
	for _, r := range "label:bug" {
		m = send(t, m, key(string(r)))
	}
	if items := m.paletteItems(); len(items) == 0 || items[len(items)-1].query != "label:bug" {
		t.Fatalf("items = %+v", items)
	}
	m = send(t, m, key("enter"))
	if m.palette || m.query() != "label:bug" {
		t.Errorf("palette=%v query=%q", m.palette, m.query())
	}
}

func TestConfirmBeforeRebase(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("r"))
	if m.plan == nil || m.plan.Title != "Rebase" {
		t.Fatalf("expected a rebase preview, got %+v", m.plan)
	}
	if len(be.ran) != 0 {
		t.Fatal("rebase ran without confirmation")
	}
	if !strings.Contains(ansi.Strip(m.View()), "enter run") {
		t.Error("preview hint missing")
	}
	m = send(t, m, key("enter"))
	if len(be.ran) != 1 || m.out == nil || !m.out.ok {
		t.Errorf("ran=%d out=%+v", len(be.ran), m.out)
	}
}

func TestFetchRunsAtOnce(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("f"))
	if len(be.ran) != 1 || be.ran[0].Title != "Fetch" {
		t.Errorf("ran = %+v", be.ran)
	}
}

func TestPromptFillsInput(t *testing.T) {
	be, m := newTest(t)
	m.cfg.Commands = append(m.cfg.Commands, config.Command{Name: "Commit", Key: "m", Prompt: "Message", Run: "git commit -m {input}"})
	m = search(t, m)
	m = send(t, m, key("m"))
	if m.prompt == nil {
		t.Fatal("no prompt")
	}
	for _, r := range "wip" {
		m = send(t, m, key(string(r)))
	}
	m = send(t, m, key("enter"))
	if len(be.ran) != 1 || be.ran[0].Script != "git commit -m wip" {
		t.Errorf("ran = %+v", be.ran)
	}
}

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func wheel(x, y int, down bool) tea.MouseMsg {
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: b}
}

func TestMouseSelectsPR(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	l := m.layout()
	firstRow := 1 + l.reposH + 2 // top bar, repos box, PR box border, tabs line
	m = send(t, m, click(5, firstRow+2))
	if pr, _ := m.selected(); pr.Number != 77 || m.focus != panePRs {
		t.Fatalf("selected #%d focus %d", pr.Number, m.focus)
	}
	m = send(t, m, click(5, firstRow+2)) // second click opens the PR
	if m.focus != paneDetail {
		t.Errorf("focus = %d, want detail", m.focus)
	}
}

func TestMouseRepoFilter(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	m = send(t, m, click(5, 3)) // top bar, border, "All", then the first repo
	if m.repoSel == "" || m.focus != paneRepos {
		t.Fatalf("repoSel=%q focus=%d", m.repoSel, m.focus)
	}
}

func TestMouseFilterTab(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	l := m.layout()
	_, spans := m.filterTabs()
	m = send(t, m, click(1+spans[1][0], 1+l.reposH+1))
	if m.query() != "author:@me" || be.searched[len(be.searched)-1] != "author:@me" {
		t.Errorf("query = %q", m.query())
	}
}

func TestMouseKeyBarRunsCommand(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, click(1, m.h-len(m.keybar()))) // "f fetch" starts the bar
	if len(be.ran) != 1 || be.ran[0].Title != "Fetch" {
		t.Errorf("ran = %+v", be.ran)
	}
}

func TestMouseWheelScrollsDetail(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	pr, _ := m.selected()
	d, _ := m.be.Detail(context.Background(), pr)
	d.Body = strings.Repeat("line\n\n", 80)
	nm, _ := m.Update(detailMsg{key: pr.Key(), d: d})
	m = nm.(Model)
	l := m.layout()
	m = send(t, m, wheel(l.leftW+10, 10, true))
	if m.vp.YOffset == 0 {
		t.Error("detail did not scroll")
	}
}

func TestMousePaletteItem(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("/"))
	l := m.layout()
	m = send(t, m, click(4, 1+l.bodyH+3)) // input line, then items: Involves, Authored, Review requested
	if m.palette || m.query() != "review-requested:@me" {
		t.Errorf("palette=%v query=%q", m.palette, m.query())
	}
}

func TestSmallTerminalDoesNotPanic(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	for _, size := range [][2]int{{40, 12}, {20, 8}, {200, 60}} {
		nm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		_ = nm.(Model).View()
	}
}

func TestDirtyCloneAsksToStash(t *testing.T) {
	be, m := newTest(t)
	be.branch, be.changes = "main", []string{" M a.go", "?? notes.md"}
	m = search(t, m)
	m = send(t, m, key("r"))
	if m.plan == nil || len(m.plan.Changes) != 2 {
		t.Fatalf("expected the changes prompt, got %+v", m.plan)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "s stash them") || !strings.Contains(v, "notes.md") {
		t.Errorf("prompt not shown:\n%s", v)
	}
	m = send(t, m, key("s"))
	if m.plan == nil || !strings.HasPrefix(m.plan.Pre, "git stash push --include-untracked") || !strings.Contains(m.plan.Pre, "gh pr checkout") {
		t.Fatalf("stash not planned before checkout: %+v", m.plan)
	}
	if len(be.ran) != 0 {
		t.Fatal("rebase ran before its confirmation")
	}
	m = send(t, m, key("enter"))
	if len(be.ran) != 1 || !strings.Contains(be.ran[0].FullScript(), "git rebase origin/main") {
		t.Errorf("ran = %+v", be.ran)
	}
}

func TestDropNeedsSecondConfirmation(t *testing.T) {
	be, m := newTest(t)
	be.branch, be.changes = "main", []string{" M a.go"}
	m = search(t, m)
	m = send(t, m, key("c"))
	m = send(t, m, key("d"))
	if !m.dropConfirm {
		t.Fatal("drop did not ask again")
	}
	m = send(t, m, key("n"))
	if m.dropConfirm || m.plan == nil || len(m.plan.Changes) == 0 {
		t.Fatal("saying no should go back to the stash/drop choice")
	}
	m = send(t, m, key("d"))
	m = send(t, m, key("y"))
	if len(be.ran) != 1 || !strings.HasPrefix(be.ran[0].Pre, "git reset --hard") {
		t.Errorf("ran = %+v", be.ran)
	}
}

func TestFetchIgnoresChanges(t *testing.T) {
	be, m := newTest(t)
	be.branch, be.changes = "main", []string{" M a.go"}
	m = search(t, m)
	m = send(t, m, key("f"))
	if len(be.ran) != 1 || be.ran[0].Pre != "" {
		t.Errorf("fetch should run as is: %+v", be.ran)
	}
}

func TestNoWorktreeUnlessAsked(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("o"))
	if len(be.ran) != 1 || be.ran[0].Folder.Worktree {
		t.Fatalf("open used a worktree: %+v", be.ran)
	}
	m = send(t, m, key("w"))
	if len(be.ran) != 2 || !be.ran[1].Folder.Worktree {
		t.Errorf("w should use a worktree: %+v", be.ran)
	}
}

func TestMissingCloneFoundAndSaved(t *testing.T) {
	be, m := newTest(t)
	be.noClone = map[string]bool{"northwind/registry": true}
	be.found = []string{"/elsewhere/registry"}
	m = search(t, m)
	m = send(t, m, key("f"))
	if m.missing == nil || len(m.missing.found) != 1 {
		t.Fatalf("missing = %+v", m.missing)
	}
	m = send(t, m, key("1"))
	if be.saved["northwind/registry"] != "/elsewhere/registry" {
		t.Fatalf("saved = %v", be.saved)
	}
	if m.missing != nil || len(be.ran) != 1 || be.ran[0].Title != "Fetch" {
		t.Errorf("fetch should run once the clone is set: missing=%v ran=%+v", m.missing, be.ran)
	}
}

func TestSetClonePathPrompt(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("L"))
	if m.pathRepo != "northwind/registry" || len(m.pathCands) != 4 {
		t.Fatalf("pathRepo = %q, clones = %d", m.pathRepo, len(m.pathCands))
	}
	// With nothing typed, the clone of this repo is listed first.
	if top := m.pathMatches()[0]; top.path != "/home/me/projects/registry" || !top.ours {
		t.Fatalf("top = %+v", top)
	}
	for _, r := range "cp" {
		m = send(t, m, key(string(r)))
	}
	if top := m.pathMatches()[0]; top.path != "/home/me/projects/cp" {
		t.Fatalf("fuzzy top = %+v", top)
	}
	m = send(t, m, key("enter"))
	if be.saved["northwind/registry"] != "/home/me/projects/cp" || m.pathRepo != "" {
		t.Errorf("saved = %v", be.saved)
	}
}

func TestPathPromptTypedPath(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("L"))
	for _, r := range "~/elsewhere/reg" {
		m = send(t, m, key(string(r)))
	}
	if top := m.pathMatches()[0]; !top.literal {
		t.Fatalf("a typed path should be offered first: %+v", top)
	}
	m = send(t, m, key("enter"))
	if be.saved["northwind/registry"] != "~/elsewhere/reg" {
		t.Errorf("saved = %v", be.saved)
	}
}

func TestPathPromptArrowsAndClick(t *testing.T) {
	be, m := newTest(t)
	m = search(t, m)
	m = send(t, m, key("L"))
	m = send(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.pathCur != 1 {
		t.Fatalf("pathCur = %d", m.pathCur)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "✓ northwind/registry") || !strings.Contains(v, "odh-feast") {
		t.Errorf("list not shown:\n%s", v)
	}
	l := m.layout()
	m = send(t, m, click(4, 1+l.bodyH+3)) // input line, then rows 1, 2, 3
	want := m.be.(*fakeBackend).saved["northwind/registry"]
	if want == "" || m.pathRepo != "" {
		t.Errorf("click did not pick a clone: saved=%v", be.saved)
	}
}
