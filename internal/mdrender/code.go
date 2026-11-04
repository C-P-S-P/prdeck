package mdrender

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/ast"
)

// code draws a code block on a dark panel, syntax highlighted. Code is never wrapped:
// a line too long for the pane is cut and ends in "…".
func (r *renderer) code(n ast.Node, lang string, w int) []string {
	var b strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		b.Write(seg.Value(r.src))
	}
	src := strings.TrimRight(strings.ReplaceAll(b.String(), "\t", "    "), "\n")
	panel := style{bg: cPanel}
	inner := w - 2

	var out []string
	if lang != "" {
		label := lang
		if ansi.StringWidth(label) > inner {
			label = ansi.Truncate(label, inner, "")
		}
		out = append(out, paint(" "+pad(label, inner, 0)+" ", style{fg: cFaint, bg: cPanel}, false))
	}
	for _, line := range highlight(src, lang) {
		var b strings.Builder
		for _, rn := range line {
			rn.st.bg = cPanel
			b.WriteString(paint(rn.text, rn.st, false))
		}
		s := b.String()
		if ansi.StringWidth(s) > inner {
			// The cut run's reset lands after the tail, so the "…" starts with one.
			s = ansi.Truncate(s, inner, "\x1b[0m"+paint("…", style{fg: cDim, bg: cPanel}, false))
		}
		out = append(out, paint(" ", panel, false)+s+paint(strings.Repeat(" ", max(inner-ansi.StringWidth(s), 0)+1), panel, false))
	}
	return out
}

var codeStyle = styles.Get("github-dark")

// highlight splits code into lines of colored runs. Unknown languages stay plain.
func highlight(src, lang string) [][]run {
	plain := func() [][]run {
		var out [][]run
		for _, l := range strings.Split(src, "\n") {
			out = append(out, []run{{text: l, st: style{fg: cCodeFg}}})
		}
		return out
	}
	if lang == "" {
		return plain()
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return plain()
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		return plain()
	}
	lines := [][]run{nil}
	for tok := it(); tok != chroma.EOF; tok = it() {
		e := codeStyle.Get(tok.Type)
		st := style{fg: cCodeFg}
		if e.Colour.IsSet() {
			st.fg = e.Colour.String()
		}
		st.bold = e.Bold == chroma.Yes
		st.italic = e.Italic == chroma.Yes
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				lines = append(lines, nil)
			}
			if part != "" {
				lines[len(lines)-1] = append(lines[len(lines)-1], run{text: part, st: st})
			}
		}
	}
	for len(lines) > 1 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}
