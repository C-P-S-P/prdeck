package tui

import (
	"strings"

	"github.com/patelchaitany/prdeck/internal/mdrender"
)

// markdown renders PR bodies and comments with mdrender, caching by text for the
// current width.
type markdown struct {
	width int
	cache map[string]string
}

func newMarkdown() *markdown {
	return &markdown{cache: map[string]string{}}
}

func (md *markdown) render(src string, width int) string {
	src = strings.TrimSpace(strings.ReplaceAll(src, "\r\n", "\n"))
	if src == "" {
		return ""
	}
	width = max(width-1, 20)
	if width != md.width {
		md.width, md.cache = width, map[string]string{}
	}
	if out, ok := md.cache[src]; ok {
		return out
	}
	out := indent(mdrender.Render(src, mdrender.Options{Width: width - 2, Links: true}), "  ")
	md.cache[src] = out
	return out
}

func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

// RenderMarkdown renders GitHub markdown for a pane of the given width, as the PR view
// does. Used by `prdeck md` to check rendering.
func RenderMarkdown(src string, width int) string {
	return newMarkdown().render(src, width)
}

// PrepareMarkdown is the GitHub cleanup step alone (HTML, alerts, mentions).
func PrepareMarkdown(src string) string { return mdrender.GitHubToMarkdown(src) }
