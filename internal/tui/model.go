// Package tui is the terminal front-end: three panes, a filter palette and a command bar.
package tui

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/MasterPushers/prdeck/internal/cache"
	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/core"
	"github.com/MasterPushers/prdeck/internal/fuzzy"
	"github.com/MasterPushers/prdeck/internal/github"
	"github.com/MasterPushers/prdeck/internal/repos"
)

// Backend is what the UI needs from the core. *core.Service implements it.
type Backend interface {
	Config() *config.Config
	Viewer() string
	CachedSearch(query string) (cache.SearchEntry, bool)
	Search(ctx context.Context, query string) ([]github.PR, error)
	CachedDetail(pr github.PR) (*github.Detail, bool)
	Detail(ctx context.Context, pr github.PR) (*github.Detail, error)
	ResolveFolder(pr github.PR, wantWorktree bool) (core.Folder, error)
	Prepare(c config.Command, pr github.PR, input string) (core.Plan, error)
	ClonePlan(pr github.PR) core.Plan
	Execute(ctx context.Context, p core.Plan) core.Result
	Setup(ctx context.Context, p core.Plan) (string, error)
	TerminalCmd(p core.Plan) *exec.Cmd
	FindClones(ctx context.Context, repo string) []string
	SetClonePath(repo, path string) (string, error)
	ClonePath(repo string) string
	ListClones(ctx context.Context) []repos.DiskClone
}

// missingClone is shown when a command needs a clone that was not found: prdeck
// searches the disk for it and offers to set a path or clone it.
type missingClone struct {
	pr        github.PR
	cmd       config.Command
	input     string
	searching bool
	found     []string
}

type pane int

const (
	paneRepos pane = iota
	panePRs
	paneDetail
)

type output struct {
	title string
	ok    bool
	lines []string
	dir   string
	wrap  bool // GitHub's answer to a write: wrapped, not cut, so all of it shows
}

type Model struct {
	be  Backend
	cfg *config.Config
	w   int
	h   int

	filters []config.Filter
	active  int
	custom  string

	prs     []github.PR
	loading bool
	synced  time.Time
	err     string
	seq     int

	repoSel    string
	repoCursor int
	cursor     int
	listTop    int
	focus      pane

	details  map[string]*github.Detail
	fetching map[string]bool
	detErr   map[string]string
	folders  map[string]string
	vp       viewport.Model
	vpKey    string
	newAt    int // the PR view's line of the first new comment, review or reply; -1 if none
	// The PR on screen, and when you had last seen it as you opened it: loading it
	// again while it stays on screen keeps what was new then.
	openKey   string
	openSince time.Time
	start     tea.Cmd // what the first PR needs loaded; Init runs it

	palette bool
	pin     textinput.Model
	pcur    int

	prompt   *config.Command
	promptPR github.PR // the PR the prompt was opened on
	pathRepo string    // the path prompt is open for this repo
	promptIn textinput.Model

	// The path prompt fuzzy-searches every clone under the home folder.
	pathCands   []repos.DiskClone
	pathLoading bool
	pathCur     int

	missing     *missingClone
	dropConfirm bool

	plan    *core.Plan
	running string
	cancel  context.CancelFunc
	out     *output
	flash   string

	help bool
	spin spinner.Model
	md   *markdown
}

// New builds the UI model.
func New(be Backend) Model {
	cfg := be.Config()
	var filters []config.Filter
	for _, f := range cfg.Filters {
		if !f.Hidden {
			filters = append(filters, f)
		}
	}
	pin := textinput.New()
	pin.Prompt = "❯ "
	pin.Placeholder = "filter name, or any GitHub qualifier like involves:@me label:bug"
	pi := textinput.New()
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = sYellow
	m := Model{
		be: be, cfg: cfg, filters: filters, focus: panePRs,
		details: map[string]*github.Detail{}, fetching: map[string]bool{},
		detErr: map[string]string{}, folders: map[string]string{},
		pin: pin, promptIn: pi, spin: sp, md: newMarkdown(),
		vp:  viewport.New(80, 20),
		seq: 1, loading: true, newAt: -1,
	}
	m.loadCached()
	// The first PR is opened here, not in Init: Init works on a copy of m, so the
	// first search would count as a second visit and drop its new tags.
	m.start = m.selectionCmds()
	return m
}

type (
	searchMsg struct {
		seq int
		prs []github.PR
		err error
	}
	detailMsg struct {
		key string
		d   *github.Detail
		err error
	}
	folderMsg struct{ key, text string }
	findMsg   struct {
		repo  string
		paths []string
	}
	clonesMsg     struct{ clones []repos.DiskClone }
	wantDetailMsg struct{ key string }
	tickMsg       time.Time
	resultMsg     core.Result
	preparedMsg   struct {
		plan core.Plan
		out  string
		err  error
	}
	termDoneMsg struct {
		plan core.Plan
		err  error
	}
	clearFlashMsg struct{ text string }
)

// Init starts the first search (New already counted it as m.seq) and loads what the
// first PR needs.
//
// Throughout this file, a method that changes the model is called on its own line before
// `return m, cmd`: Go does not fix the order between reading m and calling the method.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchSearch(m.seq), m.tick(), m.spin.Tick, m.start)
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(time.Duration(m.cfg.GitHub.Refresh), func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) query() string {
	if m.custom != "" {
		return m.custom
	}
	if len(m.filters) == 0 {
		return "involves:@me"
	}
	return m.filters[m.active].Query
}

func (m *Model) loadCached() {
	if e, ok := m.be.CachedSearch(m.query()); ok {
		m.prs, m.synced = e.PRs, e.At
	} else {
		m.prs = nil
	}
	m.clampCursor()
}

func (m *Model) searchCmd() tea.Cmd {
	m.seq++
	m.loading = true
	return m.fetchSearch(m.seq)
}

func (m Model) fetchSearch(seq int) tea.Cmd {
	q, be := m.query(), m.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		prs, err := be.Search(ctx, q)
		return searchMsg{seq: seq, prs: prs, err: err}
	}
}

// visible is the PR list after the repo filter.
func (m Model) visible() []github.PR {
	if m.repoSel == "" {
		return m.prs
	}
	var out []github.PR
	for _, p := range m.prs {
		if strings.EqualFold(p.Repo, m.repoSel) {
			out = append(out, p)
		}
	}
	return out
}

type repoRow struct {
	name  string
	count int
}

func (m Model) repoRows() []repoRow {
	counts := map[string]int{}
	for _, p := range m.prs {
		counts[p.Repo]++
	}
	rows := make([]repoRow, 0, len(counts)+1)
	for r, c := range counts {
		rows = append(rows, repoRow{r, c})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].name < rows[j].name
	})
	return append([]repoRow{{"", len(m.prs)}}, rows...)
}

func (m Model) selected() (github.PR, bool) {
	v := m.visible()
	if m.cursor < 0 || m.cursor >= len(v) {
		return github.PR{}, false
	}
	return v[m.cursor], true
}

func (m *Model) clampCursor() {
	v := m.visible()
	if m.cursor >= len(v) {
		m.cursor = len(v) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	rows := m.repoRows()
	if m.repoCursor >= len(rows) {
		m.repoCursor = 0
		m.repoSel = ""
	}
}

// selectionCmds loads what the selected PR needs: its detail (debounced) and its folder.
func (m *Model) selectionCmds() tea.Cmd {
	pr, ok := m.selected()
	if !ok {
		return nil
	}
	key := pr.Key()
	visit := key != m.openKey // opened now, not a refresh of the PR on screen
	if visit {
		m.openKey, m.openSince = key, time.Time{}
	}
	m.markRead(key)
	if d, fresh := m.be.CachedDetail(pr); d != nil {
		// Opened again, the saved copy replaces the one from your last visit, and
		// with it that visit's new tags.
		if cur, ok := m.details[key]; !ok || cur.UpdatedAt.Before(d.UpdatedAt) || visit && !cur.UpdatedAt.After(d.UpdatedAt) {
			m.showDetail(key, d)
		}
		if fresh {
			return m.folderCmd(pr)
		}
	}
	return tea.Batch(
		tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return wantDetailMsg{key} }),
		m.folderCmd(pr),
	)
}

// showDetail keeps d as the detail for key. While the PR stays on screen, a refresh,
// CI finishing or your own comment loads it again; what was new when you opened it
// stays tagged.
func (m *Model) showDetail(key string, d *github.Detail) {
	if key == m.openKey {
		if m.openSince.IsZero() {
			m.openSince = d.LastSeen
		} else if m.openSince.Before(d.LastSeen) {
			d = d.SinceSeen(m.openSince, m.be.Viewer())
		}
	}
	m.details[key] = d
	m.markRead(key)
}

// markRead clears a PR's dot: the core marks it seen when it shows its detail, which
// may come in after the list was refreshed.
func (m *Model) markRead(key string) {
	for i := range m.prs {
		if m.prs[i].Key() == key {
			m.prs[i].Unread = false
		}
	}
}

func (m Model) detailCmd(pr github.PR) tea.Cmd {
	be := m.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		d, err := be.Detail(ctx, pr)
		return detailMsg{key: pr.Key(), d: d, err: err}
	}
}

func (m Model) folderCmd(pr github.PR) tea.Cmd {
	be := m.be
	return func() tea.Msg {
		return folderMsg{pr.Key(), describeFolder(be, pr)}
	}
}

// describeFolder is the "folder" line in the PR view: where commands run and its state.
func describeFolder(be Backend, pr github.PR) string {
	f, err := be.ResolveFolder(pr, false)
	switch {
	case errors.Is(err, core.ErrNoClone):
		return "no local clone found · press L to set its path"
	case err != nil:
		return err.Error()
	case !f.Exists:
		return config.ShortenHome(f.Path) + " (worktree, created on first command)"
	}
	s := config.ShortenHome(f.Path)
	var notes []string
	switch {
	case f.Rebasing:
		notes = append(notes, "rebase in progress")
	case f.Branch == pr.HeadRefName:
		notes = append(notes, "on this PR's branch")
	case f.Branch != "":
		notes = append(notes, "on "+f.Branch)
	}
	if n := len(f.Changes); n > 0 {
		notes = append(notes, plural(n, "uncommitted change"))
	}
	if len(notes) > 0 {
		s += " (" + strings.Join(notes, ", ") + ")"
	}
	return s
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.vpKey = ""
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tickMsg:
		cmd := m.searchCmd()
		return m, tea.Batch(cmd, m.tick())

	case filterMsg:
		m.active, m.custom = msg.i, msg.custom
		m.cursor, m.listTop, m.repoSel, m.repoCursor = 0, 0, "", 0
		m.loadCached()
		search := m.searchCmd()
		sel := m.selectionCmds()
		return m, tea.Batch(search, sel)

	case searchMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = firstLine(msg.err.Error())
			return m, nil
		}
		m.err = ""
		key := ""
		if pr, ok := m.selected(); ok {
			key = pr.Key()
		}
		m.prs, m.synced = msg.prs, time.Now()
		m.restoreSelection(key)
		sel := m.selectionCmds()
		return m, sel

	case wantDetailMsg:
		pr, ok := m.selected()
		if !ok || pr.Key() != msg.key || m.fetching[msg.key] {
			return m, nil
		}
		if d, ok := m.details[msg.key]; ok && !d.UpdatedAt.Before(pr.UpdatedAt) && !pr.UpdatedAt.IsZero() && d.CheckState == pr.CheckState {
			return m, nil
		}
		m.fetching[msg.key] = true
		return m, m.detailCmd(pr)

	case detailMsg:
		delete(m.fetching, msg.key)
		if msg.err != nil {
			m.detErr[msg.key] = firstLine(msg.err.Error())
		} else {
			delete(m.detErr, msg.key)
			m.showDetail(msg.key, msg.d)
		}
		m.vpKey = ""
		return m, nil

	case folderMsg:
		m.folders[msg.key] = msg.text
		m.vpKey = ""
		return m, nil

	case resultMsg:
		m.running, m.cancel = "", nil
		r := core.Result(msg)
		if r.Plan.Silent && r.Err == nil {
			cmd := m.setFlash("✓ " + r.Plan.Title)
			return m, cmd
		}
		m.out = &output{title: r.Plan.Title, ok: r.Err == nil, dir: r.Plan.Dir(), lines: outputLines(r.Output, r.Err), wrap: core.Writes(r.Plan.Action)}
		var cmds []tea.Cmd
		if pr, ok := m.selected(); ok {
			cmds = append(cmds, m.folderCmd(pr))
		}
		if r.Err == nil && r.Plan.Action != "open" && r.Plan.Action != "browse" {
			cmds = append(cmds, m.searchCmd())
		}
		if r.Err == nil && core.Writes(r.Plan.Action) {
			// Load the PR again, so the new review, comment or auto-merge shows.
			m.fetching[r.Plan.PR.Key()] = true
			cmds = append(cmds, m.detailCmd(r.Plan.PR))
		}
		return m, tea.Batch(cmds...)

	case clonesMsg:
		m.pathCands, m.pathLoading = msg.clones, false
		return m, nil

	case findMsg:
		if m.missing == nil || m.missing.pr.Repo != msg.repo {
			return m, nil
		}
		m.missing.searching, m.missing.found = false, msg.paths
		return m, nil

	case preparedMsg:
		if msg.err != nil {
			m.running = ""
			m.out = &output{title: msg.plan.Title, ok: false, lines: outputLines(msg.out, msg.err)}
			return m, nil
		}
		m.running = ""
		p := msg.plan
		return m, tea.ExecProcess(m.be.TerminalCmd(p), func(err error) tea.Msg { return termDoneMsg{plan: p, err: err} })

	case termDoneMsg:
		if msg.err != nil {
			m.out = &output{title: msg.plan.Title, ok: false, lines: outputLines("", msg.err)}
		} else {
			m.out = &output{title: msg.plan.Title, ok: true, dir: msg.plan.Dir(), lines: []string{"finished"}}
		}
		cmd := m.searchCmd()
		return m, cmd

	case clearFlashMsg:
		if m.flash == msg.text {
			m.flash = ""
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m *Model) restoreSelection(key string) {
	m.clampCursor()
	if key == "" {
		return
	}
	for i, p := range m.visible() {
		if p.Key() == key {
			m.cursor = i
			return
		}
	}
}

func (m *Model) setFlash(s string) tea.Cmd {
	m.flash = s
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{s} })
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	}

	switch {
	case m.prompt != nil || m.pathRepo != "":
		return m.promptKey(k)
	case m.missing != nil:
		return m.missingKey(key)
	case m.palette:
		return m.paletteKey(k)
	case m.plan != nil:
		return m.planKey(key)
	case m.help:
		if key == "esc" || key == "?" || key == "q" {
			m.help = false
			m.vpKey = ""
		}
		return m, nil
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "?":
		m.help = true
		m.vpKey = ""
		return m, nil
	case "/":
		m.palette = true
		m.pcur = 0
		m.pin.SetValue("")
		return m, m.pin.Focus()
	case "R", "ctrl+r":
		cmd := m.searchCmd()
		return m, cmd
	case "tab":
		m.focus = (m.focus + 1) % 3
		return m, nil
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
		return m, nil
	case "esc":
		switch {
		case m.running != "" && m.cancel != nil:
			m.cancel()
		case m.out != nil:
			m.out = nil
		case m.focus == paneDetail:
			m.focus = panePRs
		}
		return m, nil
	}

	if n, err := strconv.Atoi(key); err == nil && n >= 1 && n <= len(m.filters) {
		return m, m.switchFilter(n-1, "")
	}

	switch key {
	case "j", "down", "k", "up", "pgdown", "pgup", "ctrl+d", "ctrl+u", "g", "home", "G", "end", "enter", "h", "left", "l", "right":
		return m.navKey(key)
	}

	for _, c := range m.cfg.Commands {
		if c.Key != "" && c.Key == key {
			cmd := c
			return m.startCommand(&cmd, "")
		}
	}
	if key == "n" { // after the commands, so one of yours on n still wins
		return m.jumpToNew()
	}
	return m, nil
}

// jumpKeyFree reports that n jumps to the first new item: none of your commands is on
// n, so the PR view and ? help can say so.
func (m Model) jumpKeyFree() bool {
	for _, c := range m.cfg.Commands {
		if c.Key == "n" {
			return false
		}
	}
	return true
}

// jumpToNew scrolls the PR view to the first comment, review or reply that came after
// your last visit.
func (m Model) jumpToNew() (tea.Model, tea.Cmd) {
	if m.newAt < 0 {
		cmd := m.setFlash("Nothing new since you last looked")
		return m, cmd
	}
	m.focus = paneDetail
	m.vp.SetYOffset(m.newAt)
	return m, nil
}

func (m Model) switchFilter(i int, custom string) tea.Cmd {
	return func() tea.Msg { return filterMsg{i, custom} }
}

type filterMsg struct {
	i      int
	custom string
}

func (m Model) navKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "h", "left":
		if m.focus > paneRepos {
			m.focus--
		}
		return m, nil
	case "l", "right":
		if m.focus < paneDetail {
			m.focus++
		}
		return m, nil
	}

	switch m.focus {
	case paneDetail:
		switch key {
		case "j", "down":
			m.vp.ScrollDown(1)
		case "k", "up":
			m.vp.ScrollUp(1)
		case "pgdown", "ctrl+d":
			m.vp.HalfPageDown()
		case "pgup", "ctrl+u":
			m.vp.HalfPageUp()
		case "g", "home":
			m.vp.GotoTop()
		case "G", "end":
			m.vp.GotoBottom()
		}
		return m, nil

	case paneRepos:
		rows := m.repoRows()
		switch key {
		case "j", "down":
			m.repoCursor = min(m.repoCursor+1, len(rows)-1)
		case "k", "up":
			m.repoCursor = max(m.repoCursor-1, 0)
		case "g", "home":
			m.repoCursor = 0
		case "G", "end":
			m.repoCursor = len(rows) - 1
		case "enter":
			m.focus = panePRs
			return m, nil
		}
		m.repoSel = rows[m.repoCursor].name
		m.cursor, m.listTop = 0, 0
		sel := m.selectionCmds()
		return m, sel

	default:
		n := len(m.visible())
		prev := m.cursor
		switch key {
		case "j", "down":
			m.cursor = min(m.cursor+1, n-1)
		case "k", "up":
			m.cursor = max(m.cursor-1, 0)
		case "pgdown", "ctrl+d":
			m.cursor = min(m.cursor+10, n-1)
		case "pgup", "ctrl+u":
			m.cursor = max(m.cursor-10, 0)
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = n - 1
		case "enter":
			m.focus = paneDetail
			return m, nil
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		if m.cursor != prev {
			m.vp.GotoTop()
			sel := m.selectionCmds()
			return m, sel
		}
		return m, nil
	}
}

// startCommand asks for input if needed, then shows or runs the plan.
func (m Model) startCommand(c *config.Command, input string) (tea.Model, tea.Cmd) {
	pr, ok := m.selected()
	if !ok {
		cmd := m.setFlash("Select a pull request first")
		return m, cmd
	}
	return m.startOn(pr, c, input)
}

// startOn is startCommand for a given PR: the one a prompt was opened on, which a
// refresh while typing can move or take out of the list.
func (m Model) startOn(pr github.PR, c *config.Command, input string) (tea.Model, tea.Cmd) {
	if c.Action == "setpath" {
		return m.askPath(pr.Repo)
	}
	if ok, why := core.Allowed(*c, pr, m.be.Viewer()); !ok {
		// Say so before asking for text that could not be used.
		cmd := m.setFlash("! " + why)
		return m, cmd
	}
	if c.Prompt != "" && input == "" {
		m.prompt, m.promptPR = c, pr
		m.promptIn.Prompt = c.Prompt + ": "
		m.promptIn.Placeholder = "" // the path prompt's hint would stay otherwise
		m.promptIn.SetValue("")
		return m, m.promptIn.Focus()
	}
	plan, err := m.be.Prepare(*c, pr, input)
	if errors.Is(err, core.ErrNoClone) {
		m.missing = &missingClone{pr: pr, cmd: *c, input: input, searching: true}
		be, repo := m.be, pr.Repo
		return m, func() tea.Msg { return findMsg{repo, be.FindClones(context.Background(), repo)} }
	}
	if err != nil {
		cmd := m.setFlash("! " + err.Error())
		return m, cmd
	}
	if plan.Confirm || len(plan.Changes) > 0 {
		m.plan = &plan
		return m, nil
	}
	return m.run(plan)
}

func (m Model) askPath(repo string) (tea.Model, tea.Cmd) {
	m.pathRepo, m.pathCur = repo, 0
	m.promptIn.Prompt = "Clone of " + repo + " ❯ "
	m.promptIn.Placeholder = "type part of a folder name, or a full path"
	m.promptIn.SetValue("")
	cmds := []tea.Cmd{m.promptIn.Focus()}
	if m.pathCands == nil && !m.pathLoading {
		m.pathLoading = true
		be := m.be
		cmds = append(cmds, func() tea.Msg { return clonesMsg{be.ListClones(context.Background())} })
	}
	return m, tea.Batch(cmds...)
}

// pathMatch is one row of the path prompt's list.
type pathMatch struct {
	path    string // full path to use
	disp    string // what is shown (~ for home)
	idx     []int  // matched byte offsets in disp
	repos   []string
	ours    bool // a remote points at the repo being set
	literal bool // the typed text itself
	score   int
}

// pathMatches ranks clones on disk against the typed text. Clones of the repo being
// set come first; a typed path ("~/x", "/x", "./x") is offered as the first row.
func (m Model) pathMatches() []pathMatch {
	q := strings.TrimSpace(m.promptIn.Value())
	_, repoName, _ := strings.Cut(m.pathRepo, "/")
	var out []pathMatch
	for _, c := range m.pathCands {
		disp := config.ShortenHome(c.Path)
		score, idx, ok := fuzzy.Match(q, disp)
		if !ok {
			continue
		}
		if q == "" {
			// Nothing typed yet: float folders named like the repo to the top.
			if hint, _, ok := fuzzy.Match(repoName, disp); ok {
				score += hint
			}
		}
		ours := false
		for _, r := range c.Repos {
			if strings.EqualFold(r, m.pathRepo) {
				ours = true
			}
		}
		if ours {
			score += 1000
		}
		out = append(out, pathMatch{path: c.Path, disp: disp, idx: idx, repos: c.Repos, ours: ours, score: score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].disp < out[j].disp
	})
	if strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~") || strings.HasPrefix(q, ".") {
		lit := pathMatch{path: q, disp: q, literal: true}
		for _, o := range out {
			if o.disp == q || o.path == q {
				return out // the typed path is already a listed clone
			}
		}
		out = append([]pathMatch{lit}, out...)
	}
	return out
}

// useClone saves path as repo's clone and, when a command was waiting for it, runs it.
func (m Model) useClone(repo, path string) (tea.Model, tea.Cmd) {
	note, err := m.be.SetClonePath(repo, path)
	if err != nil {
		cmd := m.setFlash("! " + err.Error())
		return m, cmd
	}
	msg := "Saved " + config.ShortenHome(config.ExpandHome(path)) + " as the clone of " + repo
	if note != "" {
		msg += " (" + note + ")"
	}
	pending := m.missing
	m.missing = nil
	flash := m.setFlash(msg)
	var folder tea.Cmd
	if pr, ok := m.selected(); ok {
		folder = m.folderCmd(pr)
	}
	if pending != nil {
		nm, cmd := m.startOn(pending.pr, &pending.cmd, pending.input)
		return nm, tea.Batch(flash, folder, cmd)
	}
	return m, tea.Batch(flash, folder)
}

func (m Model) missingKey(key string) (tea.Model, tea.Cmd) {
	mc := *m.missing
	switch key {
	case "esc", "q":
		m.missing = nil
	case "f":
		if !mc.searching {
			mc.searching, mc.found = true, nil
			m.missing = &mc
			be, repo := m.be, mc.pr.Repo
			return m, func() tea.Msg { return findMsg{repo, be.FindClones(context.Background(), repo)} }
		}
	case "p":
		return m.askPath(mc.pr.Repo)
	case "c":
		p := m.be.ClonePlan(mc.pr)
		m.missing = nil
		m.plan = &p
	case "enter":
		if len(mc.found) > 0 {
			return m.useClone(mc.pr.Repo, mc.found[0])
		}
	default:
		if n, err := strconv.Atoi(key); err == nil && n >= 1 && n <= len(mc.found) {
			return m.useClone(mc.pr.Repo, mc.found[n-1])
		}
	}
	return m, nil
}

func (m Model) run(p core.Plan) (tea.Model, tea.Cmd) {
	m.plan = nil
	m.out = nil
	m.running = p.Title
	be := m.be
	if p.Terminal {
		return m, func() tea.Msg {
			out, err := be.Setup(context.Background(), p)
			return preparedMsg{plan: p, out: out, err: err}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return m, func() tea.Msg { return resultMsg(be.Execute(ctx, p)) }
}

func (m Model) planKey(key string) (tea.Model, tea.Cmd) {
	p := *m.plan
	if m.dropConfirm {
		m.dropConfirm = false
		if key == "y" {
			return m.continuePlan(p.WithDrop())
		}
		return m, nil // back to the stash / drop choice
	}
	if len(p.Changes) > 0 {
		switch key {
		case "s":
			return m.continuePlan(p.WithStash())
		case "d":
			m.dropConfirm = true
		case "esc", "n", "q":
			m.plan = nil
		}
		return m, nil
	}
	switch key {
	case "enter":
		return m.run(p)
	case "y":
		cmd := m.setFlash(copyToClipboard(p.FullScript()))
		m.plan = nil
		return m, cmd
	case "esc", "n", "q":
		m.plan = nil
	}
	return m, nil
}

// continuePlan goes on once changes are dealt with: ask first if the command
// confirms, otherwise run it.
func (m Model) continuePlan(p core.Plan) (tea.Model, tea.Cmd) {
	if p.Confirm {
		m.plan = &p
		return m, nil
	}
	return m.run(p)
}

func (m Model) promptKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.prompt, m.pathRepo = nil, ""
		m.promptIn.Blur()
		return m, nil
	case "enter":
		if repo := m.pathRepo; repo != "" {
			matches := m.pathMatches()
			if len(matches) == 0 {
				return m, nil
			}
			path := matches[min(m.pathCur, len(matches)-1)].path
			m.pathRepo = ""
			m.promptIn.Blur()
			return m.useClone(repo, path)
		}
		c, in := m.prompt, strings.TrimSpace(m.promptIn.Value())
		m.prompt = nil
		m.promptIn.Blur()
		if in == "" {
			cmd := m.setFlash("Nothing entered; command not run")
			return m, cmd
		}
		return m.startOn(m.promptPR, c, in)
	}
	if m.pathRepo != "" {
		n := len(m.pathMatches())
		switch k.String() {
		case "up", "ctrl+p", "shift+tab":
			m.pathCur = max(m.pathCur-1, 0)
			return m, nil
		case "down", "ctrl+n":
			m.pathCur = min(m.pathCur+1, max(n-1, 0))
			return m, nil
		case "tab": // put the chosen path in the box to edit it
			if n > 0 {
				m.promptIn.SetValue(m.pathMatches()[min(m.pathCur, n-1)].disp)
				m.promptIn.CursorEnd()
				m.pathCur = 0
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.promptIn, cmd = m.promptIn.Update(k)
	if m.pathRepo != "" {
		m.pathCur = 0
	}
	return m, cmd
}

// paletteItem is a filter suggestion; idx -1 means "use the typed text as a query".
type paletteItem struct {
	label, query string
	idx          int
}

func (m Model) paletteItems() []paletteItem {
	in := strings.TrimSpace(m.pin.Value())
	needle := strings.ToLower(in)
	var items []paletteItem
	for i, f := range m.filters {
		if needle == "" || strings.Contains(strings.ToLower(f.Name), needle) || strings.Contains(strings.ToLower(f.Query), needle) {
			items = append(items, paletteItem{f.Name, f.Query, i})
		}
	}
	if in != "" {
		items = append(items, paletteItem{"Search GitHub for", in, -1})
	}
	return items
}

func (m Model) paletteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.paletteItems()
	switch k.String() {
	case "esc":
		m.palette = false
		m.pin.Blur()
		return m, nil
	case "up", "ctrl+p":
		m.pcur = max(m.pcur-1, 0)
		return m, nil
	case "down", "ctrl+n", "tab":
		m.pcur = min(m.pcur+1, len(items)-1)
		return m, nil
	case "enter":
		m.palette = false
		m.pin.Blur()
		if len(items) == 0 {
			return m, nil
		}
		it := items[min(m.pcur, len(items)-1)]
		if it.idx >= 0 {
			return m, m.switchFilter(it.idx, "")
		}
		return m, m.switchFilter(m.active, it.query)
	}
	var cmd tea.Cmd
	m.pin, cmd = m.pin.Update(k)
	m.pcur = 0
	return m, cmd
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func outputLines(out string, err error) []string {
	var lines []string
	if strings.TrimSpace(out) != "" {
		lines = strings.Split(strings.TrimRight(out, "\n"), "\n")
	}
	if err != nil {
		lines = append(lines, "error: "+err.Error())
	}
	if len(lines) == 0 {
		lines = []string{"done, no output"}
	}
	return lines
}

func copyToClipboard(s string) string {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("pbcopy")
	default:
		c = exec.Command("sh", "-c", "wl-copy 2>/dev/null || xclip -selection clipboard")
	}
	c.Stdin = strings.NewReader(s)
	if err := c.Run(); err != nil {
		return "! could not copy: " + err.Error()
	}
	return "Copied command"
}
