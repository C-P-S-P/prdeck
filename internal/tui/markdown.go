package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// markdown renders PR bodies and comments with glamour, caching by text for the current width.
type markdown struct {
	style string
	width int
	r     *glamour.TermRenderer
	cache map[string]string
}

func newMarkdown(style string) *markdown {
	if style == "" {
		style = "dark"
	}
	return &markdown{style: style, cache: map[string]string{}}
}

func (md *markdown) render(src string, width int) string {
	src = strings.TrimSpace(strings.ReplaceAll(src, "\r\n", "\n"))
	if src == "" {
		return ""
	}
	if width < 20 {
		width = 20
	}
	if md.r == nil || width != md.width {
		r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(md.style), glamour.WithWordWrap(width-4), glamour.WithEmoji())
		if err != nil {
			r, _ = glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width-4))
		}
		md.r, md.width, md.cache = r, width, map[string]string{}
	}
	if out, ok := md.cache[src]; ok {
		return out
	}
	out, err := md.r.Render(src)
	if err != nil {
		out = src
	}
	out = trimBlankLines(out)
	md.cache[src] = out
	return out
}

func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	blank := func(l string) bool { return strings.TrimSpace(stripANSI(l)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
