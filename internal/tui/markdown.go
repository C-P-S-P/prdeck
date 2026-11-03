package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
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
	if md.r == nil {
		// Glamour's own wrapping wraps twice at some widths and leaves single words on
		// their own line, so it renders unwrapped and wrapLines does the wrapping.
		r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(md.style), glamour.WithWordWrap(0), glamour.WithEmoji())
		if err != nil {
			r, _ = glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(0))
		}
		md.r = r
	}
	if width != md.width {
		md.width, md.cache = width, map[string]string{}
	}
	if out, ok := md.cache[src]; ok {
		return out
	}
	out, err := md.r.Render(src)
	if err != nil {
		out = src
	}
	out = wrapLines(trimBlankLines(out), width-1)
	md.cache[src] = out
	return out
}

var listMarker = regexp.MustCompile(`^(• |\d+\. |\[[✓ x]\] )`)

// wrapLines word-wraps each rendered line to width, indenting continuation lines under
// the text of list items so wrapped bullets stay readable.
func wrapLines(s string, width int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " ")
		if ansi.StringWidth(line) <= width {
			out = append(out, line)
			continue
		}
		plain := ansi.Strip(line)
		indent := len(plain) - len(strings.TrimLeft(plain, " "))
		hang := indent
		if m := listMarker.FindString(plain[indent:]); m != "" {
			hang += ansi.StringWidth(m)
		}
		parts := strings.Split(ansi.Wrap(line, max(width-hang, 20), ""), "\n")
		out = append(out, parts[0])
		for _, part := range parts[1:] {
			out = append(out, strings.Repeat(" ", hang)+strings.TrimLeft(part, " "))
		}
	}
	return strings.Join(out, "\n")
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
