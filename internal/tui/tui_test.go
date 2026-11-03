package tui

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/patelchaitany/prdeck/internal/cache"
	"github.com/patelchaitany/prdeck/internal/config"
	"github.com/patelchaitany/prdeck/internal/core"
	"github.com/patelchaitany/prdeck/internal/github"
)

type fakeBackend struct {
	cfg      *config.Config
	prs      []github.PR
	searched []string
	ran      []core.Plan
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
	return &github.Detail{
		PR: pr, Body: "## Summary\nAdds a `StreamPushSource`.", Commits: 6, ChangedFiles: 14,
		Checks:    []github.Check{{Name: "lint", State: "SUCCESS"}, {Name: "integration", State: "FAILURE"}},
		Reviewers: []github.Reviewer{{Login: "mlin", State: "CHANGES_REQUESTED"}},
		Timeline: []github.Event{
			{Kind: "review", Author: "mlin", State: "CHANGES_REQUESTED", Body: "One thing first.", At: time.Now().Add(-2 * time.Hour)},
			{Kind: "thread", Author: "mlin", Path: "push.py", Line: 41, DiffHunk: "@@ -1 +1 @@\n+def push():", Body: "Default to False?", At: time.Now().Add(-time.Hour)},
		},
	}, nil
}
func (f *fakeBackend) ResolveFolder(pr github.PR) (core.Folder, error) {
	return core.Folder{Path: "/w/" + pr.Name(), Exists: true}, nil
}
func (f *fakeBackend) Prepare(c config.Command, pr github.PR, input string) (core.Plan, error) {
	return core.Plan{Title: c.Name, Script: strings.ReplaceAll(c.Run, "{input}", input), Confirm: c.Confirm, PR: pr, Folder: core.Folder{Path: "/w", Exists: true}}, nil
}
func (f *fakeBackend) ClonePlan(pr github.PR) core.Plan { return core.Plan{Title: "Clone"} }
func (f *fakeBackend) Execute(_ context.Context, p core.Plan) core.Result {
	f.ran = append(f.ran, p)
	return core.Result{Plan: p, Output: "ok"}
}
func (f *fakeBackend) EnsureFolder(context.Context, github.PR, core.Folder) (string, error) {
	return "", nil
}
func (f *fakeBackend) TerminalCmd(core.Plan) *exec.Cmd { return exec.Command("true") }

func newTest(t *testing.T) (*fakeBackend, Model) {
	t.Helper()
	now := time.Now()
	be := &fakeBackend{cfg: config.Default(), prs: []github.PR{
		{Number: 4821, Title: "Add streaming push source", Repo: "northwind/registry", Author: "you", HeadRefName: "feat/push", BaseRefName: "main", CheckState: "FAILURE", ReviewDecision: "CHANGES_REQUESTED", UpdatedAt: now.Add(-18 * time.Minute)},
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

func TestSmallTerminalDoesNotPanic(t *testing.T) {
	_, m := newTest(t)
	m = search(t, m)
	for _, size := range [][2]int{{40, 12}, {20, 8}, {200, 60}} {
		nm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		_ = nm.(Model).View()
	}
}
