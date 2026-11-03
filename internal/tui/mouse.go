package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Screen layout, top to bottom: the top bar (row 0), the panes (bodyH rows), then the
// bottom area (previews, output, the key bar). The left column holds the repos box over
// the PR box; the PR view fills the right.

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.w == 0 || m.prompt != nil {
		return m, nil
	}
	if m.pathRepo != "" {
		l := m.layout()
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y >= 1+l.bodyH {
			return m.clickBottom(msg.Y-1-l.bodyH, msg.X, l)
		}
		return m, nil
	}
	l := m.layout()
	bodyTop, bodyEnd := 1, 1+l.bodyH
	reposEnd := bodyTop + l.reposH
	inLeft := msg.X < l.leftW

	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		if msg.Y < bodyTop || msg.Y >= bodyEnd || m.palette || m.plan != nil {
			return m, nil
		}
		step := 1
		if msg.Button == tea.MouseButtonWheelUp {
			step = -1
		}
		switch {
		case !inLeft:
			if step > 0 {
				m.vp.ScrollDown(3)
			} else {
				m.vp.ScrollUp(3)
			}
			return m, nil
		case msg.Y < reposEnd:
			return m.scrollList(paneRepos, step)
		default:
			return m.scrollList(panePRs, step)
		}
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if m.help {
		m.help = false
		m.vpKey = ""
		return m, nil
	}

	if msg.Y >= bodyEnd {
		return m.clickBottom(msg.Y-bodyEnd, msg.X, l)
	}
	if m.palette || m.plan != nil || msg.Y < bodyTop {
		return m, nil
	}

	if !inLeft {
		m.focus = paneDetail
		return m, nil
	}

	if msg.Y < reposEnd {
		m.focus = paneRepos
		row := msg.Y - bodyTop - 1 // below the top border
		rows := m.repoRows()
		i := m.reposTop(l.reposH-2) + row
		if row < 0 || row >= l.reposH-2 || i >= len(rows) {
			return m, nil
		}
		m.repoCursor, m.repoSel = i, rows[i].name
		m.cursor, m.listTop = 0, 0
		sel := m.selectionCmds()
		return m, sel
	}

	// PR box: top border, the filter tabs, then one row per PR.
	m.focus = panePRs
	row := msg.Y - reposEnd - 1
	if row == 0 {
		_, spans := m.filterTabs()
		x := msg.X - 1
		for i, s := range spans {
			if x >= s[0] && x < s[1] && i < len(m.filters) {
				return m, m.switchFilter(i, "")
			}
		}
		return m, nil
	}
	i := m.listTop + row - 1
	if row < 1 || row > l.prsH-3 || i >= len(m.visible()) {
		return m, nil
	}
	if i == m.cursor {
		m.focus = paneDetail // a second click on the selected PR opens it
		return m, nil
	}
	m.cursor = i
	m.vp.GotoTop()
	sel := m.selectionCmds()
	return m, sel
}

func (m Model) clickBottom(row, x int, l layout) (tea.Model, tea.Cmd) {
	if m.pathRepo != "" {
		if i := row - 1; i >= 0 && i < min(len(m.pathMatches()), pathRows) {
			m.pathCur = i
			return m.promptKey(tea.KeyMsg{Type: tea.KeyEnter})
		}
		return m, nil
	}
	if m.palette {
		items := m.paletteItems()
		if i := row - 1; i >= 0 && i < min(len(items), 8) {
			m.pcur = i
			return m.paletteKey(tea.KeyMsg{Type: tea.KeyEnter})
		}
		return m, nil
	}
	bar := m.keybar()
	line := row - (len(l.bottom) - len(bar))
	if line < 0 || line >= len(bar) {
		return m, nil
	}
	if key := m.barKeyAt(line, x); key != "" {
		return m.handleKey(keyMsg(key))
	}
	return m, nil
}

// barKeyAt finds the key bar item at a line and column, laid out as wrapItems does.
func (m Model) barKeyAt(line, x int) string {
	w := max(m.w, 20)
	ln, col := 0, 0
	for _, it := range m.barItems() {
		iw := ansi.StringWidth(it.text)
		if col > 0 && col+2+iw > w {
			ln, col = ln+1, 0
		}
		if col > 0 {
			col += 2
		}
		if ln == line && x >= col && x < col+iw {
			return it.key
		}
		col += iw
	}
	return ""
}

// scrollList moves the cursor of a list pane as if j/k were pressed there.
func (m Model) scrollList(p pane, step int) (tea.Model, tea.Cmd) {
	saved := m.focus
	m.focus = p
	key := "j"
	if step < 0 {
		key = "k"
	}
	nm, cmd := m.navKey(key)
	mm := nm.(Model)
	mm.focus = saved
	return mm, cmd
}

func keyMsg(key string) tea.KeyMsg {
	if key == "tab" {
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}
