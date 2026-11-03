package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/C-P-S-P/prdeck/internal/github"
)

// Colors follow the UI design: dark, low-chroma panels with a few signal colors.
var (
	cText   = lipgloss.Color("#e4e6ea")
	cSoft   = lipgloss.Color("#9aa1ad")
	cDim    = lipgloss.Color("#7c8390")
	cBorder = lipgloss.Color("#3a3f48")
	cGreen  = lipgloss.Color("#8cc47a")
	cRed    = lipgloss.Color("#e57b7b")
	cYellow = lipgloss.Color("#e0c070")
	cBlue   = lipgloss.Color("#6fa8f5")
	cPurple = lipgloss.Color("#b98ee6")
	cSelBg  = lipgloss.Color("#2b3a52")
	cSelDim = lipgloss.Color("#232a35")
	cInk    = lipgloss.Color("#0f1114")
	cKeyBg  = lipgloss.Color("#3a3f48")
)

var (
	sText   = lipgloss.NewStyle().Foreground(cText)
	sBold   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sSoft   = lipgloss.NewStyle().Foreground(cSoft)
	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sGreen  = lipgloss.NewStyle().Foreground(cGreen)
	sRed    = lipgloss.NewStyle().Foreground(cRed)
	sYellow = lipgloss.NewStyle().Foreground(cYellow)
	sBlue   = lipgloss.NewStyle().Foreground(cBlue)
	sPurple = lipgloss.NewStyle().Foreground(cPurple)
	sBadge  = lipgloss.NewStyle().Background(cBlue).Foreground(cInk).Bold(true).Padding(0, 1)
	sKey    = lipgloss.NewStyle().Background(cKeyBg).Foreground(cText).Padding(0, 1)
	sKeyHot = lipgloss.NewStyle().Background(cYellow).Foreground(cInk).Padding(0, 1)
	sSel    = lipgloss.NewStyle().Background(cSelBg).Foreground(lipgloss.Color("#ffffff"))
	sSelDim = lipgloss.NewStyle().Background(cSelDim).Foreground(cText)
)

// box draws a sharp-cornered panel w×h (borders included) with the title set into the top edge.
func box(title string, body []string, w, h int, focused bool) string {
	if w < 4 || h < 2 {
		return ""
	}
	bc, tc := cBorder, cSoft
	if focused {
		bc, tc = cGreen, cGreen
	}
	bs := lipgloss.NewStyle().Foreground(bc)
	ts := lipgloss.NewStyle().Foreground(tc).Bold(focused)
	inner := w - 2
	t := " " + title + " "
	if ansi.StringWidth(t) > inner-1 {
		t = ansi.Truncate(t, inner-1, "…")
	}
	tw := ansi.StringWidth(t)
	lines := make([]string, 0, h)
	lines = append(lines, bs.Render("┌─")+ts.Render(t)+bs.Render(strings.Repeat("─", max(0, inner-1-tw))+"┐"))
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(body) {
			l = body[i]
		}
		lines = append(lines, bs.Render("│")+fit(l, inner)+bs.Render("│"))
	}
	lines = append(lines, bs.Render("└"+strings.Repeat("─", inner)+"┘"))
	return strings.Join(lines, "\n")
}

// fit truncates or pads s (which may hold ANSI styles) to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// highlight paints a whole row with a selection background.
func highlight(s string, w int, st lipgloss.Style) string {
	return st.Render(fit(ansi.Strip(s), w))
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

func stateGlyph(p github.PR) string {
	if p.IsDraft {
		return sDim.Render("◌")
	}
	return sGreen.Render("●")
}

func ciGlyph(state string) string {
	switch state {
	case "SUCCESS":
		return sGreen.Render("✓")
	case "FAILURE", "ERROR":
		return sRed.Render("✕")
	case "PENDING", "EXPECTED":
		return sYellow.Render("●")
	}
	return " "
}

func reviewGlyph(p github.PR) string {
	if p.Mergeable == "CONFLICTING" {
		return sYellow.Render("⚠")
	}
	switch p.ReviewDecision {
	case "APPROVED":
		return sGreen.Render("✓")
	case "CHANGES_REQUESTED":
		return sRed.Render("✗")
	}
	return " "
}

func reviewWord(state string) (string, lipgloss.Style) {
	switch state {
	case "APPROVED":
		return "approved", sGreen
	case "CHANGES_REQUESTED":
		return "requested changes", sRed
	case "DISMISSED":
		return "review dismissed", sDim
	case "PENDING":
		return "review pending", sYellow
	}
	return "reviewed", sSoft
}

// rule draws "── label ─────" across w cells.
func rule(label string, w int, st lipgloss.Style) string {
	head := "── " + label + " "
	return st.Render(head + strings.Repeat("─", max(0, w-ansi.StringWidth(head))))
}

// wrapItems lays out items left to right, wrapping at w, two spaces apart.
func wrapItems(items []string, w int) []string {
	var lines []string
	cur, cw := "", 0
	for _, it := range items {
		iw := ansi.StringWidth(it)
		if cw > 0 && cw+2+iw > w {
			lines = append(lines, cur)
			cur, cw = "", 0
		}
		if cw > 0 {
			cur += "  "
			cw += 2
		}
		cur += it
		cw += iw
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
