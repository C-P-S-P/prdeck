package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/patelchaitany/prdeck/internal/config"
	"github.com/patelchaitany/prdeck/internal/github"
)

func stripANSI(s string) string { return ansi.Strip(s) }

type layout struct {
	leftW, rightW, bodyH, reposH, prsH int
	bottom                             []string
}

func (m Model) layout() layout {
	bottom := m.bottomLines()
	bodyH := max(m.h-1-len(bottom), 6)
	leftW := min(max(m.w*2/5, 34), 60)
	if m.w < 80 {
		leftW = m.w / 2
	}
	reposH := min(len(m.repoRows())+2, max(4, bodyH/3))
	return layout{leftW: leftW, rightW: m.w - leftW, bodyH: bodyH, reposH: reposH, prsH: bodyH - reposH, bottom: bottom}
}

// syncViewport keeps the detail viewport and list scroll in step with the layout. It runs
// after every Update so that scrolling knows the current content and size.
func (m *Model) syncViewport() {
	if m.w == 0 {
		return
	}
	l := m.layout()
	m.vp.Width, m.vp.Height = l.rightW-2, l.bodyH-2

	listH := l.prsH - 3
	if m.cursor < m.listTop {
		m.listTop = m.cursor
	}
	if listH > 0 && m.cursor >= m.listTop+listH {
		m.listTop = m.cursor - listH + 1
	}

	key := m.contentKey(m.vp.Width)
	if key != m.vpKey {
		prev, _, _ := strings.Cut(m.vpKey, "|")
		cur, _, _ := strings.Cut(key, "|")
		m.vp.SetContent(m.detailContent(m.vp.Width))
		if prev != cur {
			m.vp.GotoTop()
		}
		m.vpKey = key
	}
}

func (m Model) contentKey(w int) string {
	pr, ok := m.selected()
	k := "none"
	if ok {
		k = pr.Key()
	}
	if m.help {
		k = "help"
	}
	var upd time.Time
	if d := m.details[k]; d != nil {
		upd = d.UpdatedAt
	}
	return fmt.Sprintf("%s|%d|%s|%s|%s|%v", k, w, upd, m.folders[k], m.detErr[k], m.fetching[k])
}

// Update wraps update so the viewport is synced after every change.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.update(msg)
	mm := nm.(Model)
	mm.syncViewport()
	return mm, cmd
}

func (m Model) View() string {
	if m.w == 0 || m.h == 0 {
		return "prdeck: starting…"
	}
	l := m.layout()
	left := lipgloss.JoinVertical(lipgloss.Left, m.reposBox(l.leftW, l.reposH), m.prsBox(l.leftW, l.prsH))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, m.detailBox(l.rightW, l.bodyH))
	return m.topBar() + "\n" + body + "\n" + strings.Join(l.bottom, "\n")
}

func (m Model) topBar() string {
	left := sBadge.Render("prdeck") + " " + sGreen.Render("❯") + " " + sText.Render("is:open "+m.query())
	var right string
	switch {
	case m.loading:
		right = m.spin.View() + sSoft.Render(" refreshing")
	case m.err != "":
		right = sRed.Render("! " + m.err)
	default:
		when := "synced " + ago(m.synced) + " ago"
		if ago(m.synced) == "now" {
			when = "synced just now"
		}
		if m.synced.IsZero() {
			when = "not synced"
		}
		right = sSoft.Render(fmt.Sprintf("%d PRs · %s", len(m.prs), when))
	}
	if n := len(m.cfg.Warnings); n > 0 {
		right = sYellow.Render(fmt.Sprintf("⚠ %d config warning(s), see ?  ", n)) + right
	}
	rw := ansi.StringWidth(right)
	left = fit(left, max(m.w-rw-1, 10))
	return left + " " + right
}

func (m Model) reposBox(w, h int) string {
	rows := m.repoRows()
	inner, listH := w-2, h-2
	top := 0
	if m.repoCursor >= listH {
		top = m.repoCursor - listH + 1
	}
	var body []string
	for i := top; i < len(rows) && len(body) < listH; i++ {
		r := rows[i]
		mark := "  "
		if r.name == m.repoSel {
			mark = sGreen.Render("▸ ")
		}
		name := sText.Render("All repositories")
		if r.name != "" {
			owner, repo, _ := strings.Cut(r.name, "/")
			name = sDim.Render(owner+"/") + sText.Render(repo)
		}
		count := strconv.Itoa(r.count)
		row := fit(mark+name, inner-len(count)-1) + " " + sSoft.Render(count)
		if m.focus == paneRepos && i == m.repoCursor {
			row = highlight(row, inner, sSel)
		}
		body = append(body, row)
	}
	return box("Repositories", body, w, h, m.focus == paneRepos)
}

func (m Model) prsBox(w, h int) string {
	inner := w - 2
	var tabs []string
	for i, f := range m.filters {
		label := strconv.Itoa(i+1) + " " + f.Name
		if i == m.active && m.custom == "" {
			tabs = append(tabs, lipgloss.NewStyle().Background(cGreen).Foreground(cInk).Render(" "+label+" "))
		} else {
			tabs = append(tabs, sDim.Render(label))
		}
	}
	if m.custom != "" {
		tabs = append(tabs, lipgloss.NewStyle().Background(cGreen).Foreground(cInk).Render(" / custom "))
	}
	body := []string{strings.Join(tabs, " ")}

	v := m.visible()
	listH := h - 3
	switch {
	case len(v) == 0 && m.loading:
		body = append(body, sSoft.Render("Loading pull requests…"))
	case len(v) == 0 && m.err != "":
		body = append(body, sRed.Render("Could not load PRs."), sSoft.Render("Press R to retry."))
	case len(v) == 0:
		body = append(body, sSoft.Render("No open PRs for this filter."))
	}
	numW := 0
	for _, p := range v {
		numW = max(numW, len(strconv.Itoa(p.Number))+1)
	}
	for i := m.listTop; i < len(v) && i < m.listTop+listH; i++ {
		body = append(body, m.prRow(v[i], i, inner, numW))
	}
	title := "Pull requests"
	if m.custom != "" {
		title += " · " + m.custom
	} else if len(m.filters) > 0 {
		title += " · " + m.filters[m.active].Name
	}
	return box(title, body, w, h, m.focus == panePRs)
}

func (m Model) prRow(p github.PR, i, inner, numW int) string {
	right := " " + reviewGlyph(p) + " " + ciGlyph(p.CheckState) + " " + sDim.Render(fmt.Sprintf("%3s", ago(p.UpdatedAt)))
	num := fmt.Sprintf("%-*s", numW, "#"+strconv.Itoa(p.Number))
	title := sText.Render(p.Title)
	if m.repoSel == "" {
		title = sDim.Render(p.Name()+" ") + title
	}
	row := fit(stateGlyph(p)+" "+sSoft.Render(num)+" "+title, inner-ansi.StringWidth(right)) + right
	if i == m.cursor {
		st := sSelDim
		if m.focus == panePRs {
			st = sSel
		}
		row = highlight(row, inner, st)
	}
	return row
}

func (m Model) detailBox(w, h int) string {
	title := "Pull request"
	if m.help {
		title = "Help"
	} else if pr, ok := m.selected(); ok {
		title = "#" + strconv.Itoa(pr.Number) + " · " + pr.Repo
	}
	vp := m.vp
	vp.Width, vp.Height = w-2, h-2
	body := strings.Split(vp.View(), "\n")
	if pct := vp.ScrollPercent(); vp.TotalLineCount() > vp.Height && len(body) > 0 {
		body[len(body)-1] = fit(body[len(body)-1], w-8) + sDim.Render(fmt.Sprintf("%4d%%", int(pct*100)))
	}
	return box(title, body, w, h, m.focus == paneDetail)
}

func (m Model) detailContent(w int) string {
	if m.help {
		return m.helpContent(w)
	}
	pr, ok := m.selected()
	if !ok {
		return sSoft.Render("Select a pull request.")
	}
	d := m.details[pr.Key()]
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	wrap := lipgloss.NewStyle().Width(w)

	line(wrap.Render(sBold.Render(pr.Title)))
	state := lipgloss.NewStyle().Background(cGreen).Foreground(cInk).Bold(true).Render(" OPEN ")
	if pr.IsDraft {
		state = lipgloss.NewStyle().Background(cKeyBg).Foreground(cText).Bold(true).Render(" DRAFT ")
	}
	line(wrap.Render(state + " " + sText.Render(pr.Author) + sSoft.Render(" → ") + sText.Render(pr.BaseRefName) + sSoft.Render(" ← ") + sBlue.Render(pr.HeadRefName)))

	if d != nil {
		pass, fail, pend := d.CheckCounts()
		var facts []string
		facts = append(facts, plural(d.Commits, "commit")+" · "+plural(d.ChangedFiles, "file")+" · "+sGreen.Render(fmt.Sprintf("+%d", d.Additions))+" "+sRed.Render(fmt.Sprintf("−%d", d.Deletions)))
		if len(d.Checks) > 0 {
			c := "checks " + sGreen.Render(fmt.Sprintf("✓%d", pass))
			if fail > 0 {
				c += " " + sRed.Render(fmt.Sprintf("✕%d", fail))
			}
			if pend > 0 {
				c += " " + sYellow.Render(fmt.Sprintf("●%d", pend))
			}
			facts = append(facts, c)
		}
		line(wrap.Render(sSoft.Render(strings.Join(facts, sDim.Render("  ·  ")))))
		if fail > 0 {
			var names []string
			for _, c := range d.Checks {
				switch c.State {
				case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "STARTUP_FAILURE":
					names = append(names, c.Name)
				}
			}
			line(wrap.Render(sRed.Render("failing: " + strings.Join(names, ", "))))
		}
		if len(d.Reviewers) > 0 {
			var rs []string
			for _, r := range d.Reviewers {
				word, st := reviewWord(r.State)
				if strings.EqualFold(r.Login, m.be.Viewer()) && r.State == "PENDING" {
					word, st = "waiting on you", sBlue
				}
				rs = append(rs, sText.Render(r.Login)+" "+st.Render(word))
			}
			line(wrap.Render(sSoft.Render("reviews  ") + strings.Join(rs, sDim.Render(" · "))))
		}
		if len(d.Labels) > 0 {
			line(wrap.Render(sSoft.Render("labels   ") + sPurple.Render(strings.Join(d.Labels, ", "))))
		}
	}
	if pr.Mergeable == "CONFLICTING" {
		line(sYellow.Render("⚠ conflicts with " + pr.BaseRefName + ": rebase with r"))
	}
	if f := m.folders[pr.Key()]; f != "" {
		line(sSoft.Render("folder   ") + sText.Render(f))
	}
	line("")

	switch {
	case d == nil && m.detErr[pr.Key()] != "":
		line(sRed.Render("Could not load the conversation: " + m.detErr[pr.Key()]))
		return b.String()
	case d == nil:
		line(sSoft.Render("Loading conversation…"))
		return b.String()
	}

	if body := m.md.render(d.Body, w); body != "" {
		line(body)
	} else {
		line(sDim.Render("  No description."))
	}

	for _, ev := range d.Timeline {
		line("")
		when := ago(ev.At)
		if when != "now" {
			when += " ago"
		}
		switch ev.Kind {
		case "comment":
			line(rule(ev.Author+" commented · "+when, w, sSoft))
			line(m.md.render(ev.Body, w))
		case "review":
			word, st := reviewWord(ev.State)
			line(rule(ev.Author+" "+word+" · "+when, w, st))
			if strings.TrimSpace(ev.Body) != "" {
				line(m.md.render(ev.Body, w))
			}
		case "thread":
			label := ev.Author + " on " + ev.Path
			if ev.Line > 0 {
				label += ":" + strconv.Itoa(ev.Line)
			}
			label += " · " + when
			if ev.Resolved {
				label += " · resolved"
			} else if ev.Outdated {
				label += " · outdated"
			}
			st := sSoft
			if ev.Resolved {
				st = sDim
			}
			line(rule(label, w, st))
			for _, h := range lastLines(ev.DiffHunk, 4) {
				line(fit(diffLine(h), w))
			}
			line(m.md.render(ev.Body, w))
			for _, r := range ev.Replies {
				rw := ago(r.At)
				if rw != "now" {
					rw += " ago"
				}
				line(sDim.Render("  ↳ ") + sText.Render(r.Author) + sDim.Render(" · "+rw))
				line(m.md.render(r.Body, w))
			}
		}
	}
	if len(d.Timeline) == 0 {
		line(sDim.Render("  No comments yet."))
	}
	return strings.TrimRight(b.String(), "\n")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if s == "" {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func diffLine(l string) string {
	prefix := sDim.Render("  │ ")
	switch {
	case strings.HasPrefix(l, "@@"):
		return prefix + sBlue.Render(l)
	case strings.HasPrefix(l, "+"):
		return prefix + sGreen.Render(l)
	case strings.HasPrefix(l, "-"):
		return prefix + sRed.Render(l)
	}
	return prefix + sSoft.Render(l)
}

func (m Model) helpContent(w int) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	key := func(k, what string) string { return sKey.Render(k) + " " + sSoft.Render(what) }
	line(sBold.Render("Keys"))
	for _, l := range wrapItems([]string{
		key("j/k", "move"), key("h/l", "switch pane"), key("tab", "next pane"), key("enter", "open"),
		key("esc", "back / dismiss"), key("g/G", "top / bottom"), key("ctrl+d/u", "page"),
		key("1-"+strconv.Itoa(max(1, len(m.filters))), "filters"), key("/", "filter palette"),
		key("R", "refresh"), key("?", "help"), key("q", "quit"),
	}, w) {
		line(l)
	}
	line("")
	line(sBold.Render("Commands") + sDim.Render("  (run in the PR's folder)"))
	for _, c := range m.cfg.Commands {
		k := c.Key
		if k == "" {
			k = "·"
		}
		what := c.Run
		switch c.Action {
		case "open":
			what = "opens the PR folder in " + m.cfg.Editor.Default
		case "browse":
			what = "opens the PR in your browser"
		}
		row := sKey.Render(fmt.Sprintf("%-2s", k)) + " " + sText.Render(fmt.Sprintf("%-16s", c.Name)) + " " + sDim.Render(what)
		line(fit(row, w))
	}
	line("")
	path := m.cfg.Path
	if path == "" {
		path = config.ShortenHome(config.DefaultPath()) + " (not created yet: run prdeck config init)"
	} else {
		path = config.ShortenHome(path)
	}
	line(sSoft.Render("config  ") + sText.Render(path))
	mode := "one worktree per PR under " + m.cfg.Editor.WorktreeDir
	if !m.cfg.Editor.UseWorktree() {
		mode = "in your existing clone"
	}
	line(sSoft.Render("folders ") + sText.Render(mode))
	if len(m.cfg.Warnings) > 0 {
		line("")
		line(sYellow.Render("Config warnings"))
		for _, wn := range m.cfg.Warnings {
			line(sYellow.Render("  • " + wn))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) bottomLines() []string {
	var lines []string
	w := max(m.w, 20)
	maxOut := max(2, m.h/5)
	switch {
	case m.palette:
		lines = append(lines, fit(m.pin.View(), w))
		items := m.paletteItems()
		for i, it := range items {
			if i >= 8 {
				break
			}
			row := "  " + sText.Render(it.label) + "  " + sDim.Render(it.query)
			if i == m.pcur {
				row = highlight(row, w, sSel)
			}
			lines = append(lines, fit(row, w))
		}
		lines = append(lines, sDim.Render("↑↓ choose · enter apply · esc close"))
	case m.prompt != nil:
		lines = append(lines, fit(m.promptIn.View(), w), sDim.Render("enter run · esc cancel"))
	case m.plan != nil:
		p := m.plan
		cmd := lipgloss.NewStyle().Width(w).Render(sYellow.Render(p.Title) + "  " + sGreen.Render("$ ") + sText.Render(p.Script))
		lines = append(lines, strings.Split(cmd, "\n")...)
		if p.Warn != "" {
			lines = append(lines, fit(sYellow.Render("! "+p.Warn), w))
		}
		if !p.Folder.Exists && p.Folder.Path != "" {
			lines = append(lines, fit(sSoft.Render("first creates the worktree "+config.ShortenHome(p.Folder.Path)), w))
		}
		where := ""
		if p.Dir() != "" {
			where = "in " + config.ShortenHome(p.Dir()) + "  ·  "
		}
		lines = append(lines, fit(sDim.Render(where)+sText.Render("enter")+sDim.Render(" run · ")+sText.Render("y")+sDim.Render(" copy · ")+sText.Render("esc")+sDim.Render(" cancel"), w))
	case m.running != "":
		lines = append(lines, m.spin.View()+" "+sYellow.Render("Running "+m.running+"…")+sDim.Render("  esc cancels"))
	case m.out != nil:
		head := sGreen.Render("✓ " + m.out.title)
		if !m.out.ok {
			head = sRed.Render("✕ " + m.out.title + " failed")
		}
		if m.out.dir != "" {
			head += sDim.Render("  in " + config.ShortenHome(m.out.dir))
		}
		lines = append(lines, fit(head+sDim.Render("  · esc dismisses"), w))
		ol := m.out.lines
		if len(ol) > maxOut {
			ol = ol[len(ol)-maxOut:]
		}
		for _, l := range ol {
			lines = append(lines, fit(sSoft.Render("  "+cleanOutput(l)), w))
		}
	}
	if m.flash != "" {
		st := sGreen
		if strings.HasPrefix(m.flash, "!") {
			st = sRed
		}
		lines = append(lines, fit(st.Render(m.flash), w))
	}
	return append(lines, m.keybar()...)
}

func cleanOutput(l string) string {
	if i := strings.LastIndex(l, "\r"); i >= 0 {
		l = l[i+1:]
	}
	return strings.ReplaceAll(ansi.Strip(l), "\t", "    ")
}

func (m Model) keybar() []string {
	var items []string
	for _, c := range m.cfg.Commands {
		if !c.OnBar() || c.Key == "" {
			continue
		}
		st := sKey
		if m.plan != nil && m.plan.Title == c.Name {
			st = sKeyHot
		}
		items = append(items, st.Render(c.Key)+" "+sSoft.Render(strings.ToLower(c.Name)))
	}
	items = append(items,
		sKey.Render("/")+" "+sSoft.Render("filter"),
		sKey.Render("tab")+" "+sSoft.Render("pane"),
		sKey.Render("?")+" "+sSoft.Render("help"),
		sKey.Render("q")+" "+sSoft.Render("quit"),
	)
	return wrapItems(items, max(m.w, 20))
}
