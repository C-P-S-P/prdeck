package mdrender

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
	emojiast "github.com/yuin/goldmark-emoji/ast"
	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"
)

// run is a piece of inline text in one style. br marks a hard line break.
type run struct {
	text string
	st   style
	br   bool
}

// inline turns a block's inline children into styled runs.
func (r *renderer) inline(n ast.Node, base style) []run {
	var out []run
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, r.inlineNode(c, base)...)
	}
	return out
}

func (r *renderer) inlineNode(n ast.Node, base style) []run {
	if st, ok := r.override[n]; ok {
		base = st
	}
	switch n := n.(type) {
	case *ast.Text:
		out := []run{{text: textOf(n.Segment.Value(r.src)), st: base}}
		if n.HardLineBreak() || n.SoftLineBreak() {
			// GitHub shows every newline in a PR or comment as a line break.
			out = append(out, run{br: true})
		}
		return out
	case *ast.String:
		return []run{{text: string(n.Value), st: base}}
	case *ast.CodeSpan:
		st := base
		st.fg, st.bg, st.bold, st.italic = cCodeFg, cCodeBg, false, false
		return []run{{text: " " + plainText(n, r.src) + " ", st: st}}
	case *ast.Emphasis:
		st := base
		if n.Level >= 2 {
			st.bold = true
			if st.fg == "" || st.fg == cText {
				st.fg = cBright
			}
		} else {
			st.italic = true
		}
		return r.inline(n, st)
	case *extast.Strikethrough:
		st := base
		st.strike, st.fg = true, cDim
		return r.inline(n, st)
	case *ast.Link:
		st := base
		st.fg, st.underline, st.link = cBlue, true, string(n.Destination)
		label := plainText(n, r.src)
		if label == string(n.Destination) || label == "" {
			return []run{{text: shortURL(string(n.Destination)), st: st}}
		}
		return r.inline(n, st)
	case *ast.AutoLink:
		u := string(n.URL(r.src))
		st := base
		st.fg, st.underline, st.link = cBlue, true, u
		if n.AutoLinkType == ast.AutoLinkEmail {
			return []run{{text: string(n.Label(r.src)), st: st}}
		}
		return []run{{text: shortURL(u), st: st}}
	case *ast.Image:
		st := base
		st.fg, st.underline = cPurple, false
		if st.link == "" && !strings.HasPrefix(string(n.Destination), "data:") {
			st.link = string(n.Destination)
		}
		alt := strings.TrimSpace(plainText(n, r.src))
		if genericAlt[strings.ToLower(strings.Trim(alt, "'\""))] {
			return nil // an icon or badge with no description says nothing in a terminal
		}
		return []run{{text: "[image: " + alt + "]", st: st}}
	case *emojiast.Emoji:
		if n.Value != nil && len(n.Value.Unicode) > 0 {
			return []run{{text: string(n.Value.Unicode), st: base}}
		}
		return []run{{text: ":" + string(n.ShortName) + ":", st: base}}
	case *extast.TaskCheckBox:
		return nil // drawn as the list marker
	case *ast.RawHTML:
		return nil // the GitHub cleanup already turned the useful tags into markdown
	}
	return r.inline(n, base)
}

// textOf is source text as it reads: "schema\\_discovery" → "schema_discovery", "&amp;" → "&".
func textOf(b []byte) string {
	return string(util.UnescapePunctuations(util.ResolveEntityNames(util.ResolveNumericReferences(b))))
}

var genericAlt = map[string]bool{"": true, "image": true, "img": true, "icon": true, "svg": true, "logo": true, "badge": true}

// plainText is the text content of a node, without markup.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b.WriteString(textOf(c.Segment.Value(src)))
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(c.Value)
		case *emojiast.Emoji:
			if c.Value != nil {
				b.WriteString(string(c.Value.Unicode))
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// shortURL keeps bare links readable: "app.codecov.io/gh/feast-dev/feast/pull/7081…"
// instead of three lines of tracking parameters. The full URL stays in the hyperlink.
func shortURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	s := strings.TrimPrefix(u.Host, "www.") + strings.TrimSuffix(u.EscapedPath(), "/")
	if u.RawQuery != "" || u.Fragment != "" {
		s += "…"
	}
	if r := []rune(s); len(r) > 60 {
		s = string(r[:59]) + "…"
	}
	return s
}

// piece is a word or a run of spaces, possibly made of several styled parts.
type piece struct {
	parts []run
	space bool
	width int
}

// wrap lays runs out in lines no wider than width. Words longer than a line are split.
func (r *renderer) wrap(runs []run, width int) []string {
	if width < 1 {
		width = 1
	}
	var lines []string
	var cur []run
	curW := 0
	flush := func() {
		// Drop trailing spaces.
		for len(cur) > 0 && strings.TrimSpace(cur[len(cur)-1].text) == "" {
			curW -= ansi.StringWidth(cur[len(cur)-1].text)
			cur = cur[:len(cur)-1]
		}
		lines = append(lines, r.join(cur))
		cur, curW = nil, 0
	}
	for _, p := range pieces(runs) {
		if p.parts == nil { // hard break
			flush()
			continue
		}
		if p.space {
			if curW == 0 {
				continue // no leading spaces on a line
			}
			if curW+p.width > width {
				flush()
				continue
			}
			cur, curW = append(cur, p.parts...), curW+p.width
			continue
		}
		if curW+p.width > width && curW > 0 {
			flush()
		}
		if p.width > width { // a word longer than the line: split it
			for _, part := range p.parts {
				for _, ch := range graphemes(part.text) {
					cw := ansi.StringWidth(ch)
					if curW+cw > width {
						flush()
					}
					cur, curW = append(cur, run{text: ch, st: part.st}), curW+cw
				}
			}
			continue
		}
		cur, curW = append(cur, p.parts...), curW+p.width
	}
	if len(cur) > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}

// pieces splits runs into words and spaces; a word may span runs ("**a**b" is one word).
func pieces(runs []run) []piece {
	var out []piece
	var word *piece
	endWord := func() {
		if word != nil {
			out = append(out, *word)
			word = nil
		}
	}
	for _, rn := range runs {
		if rn.br {
			endWord()
			out = append(out, piece{})
			continue
		}
		text := rn.text
		for text != "" {
			i := strings.IndexFunc(text, unicode.IsSpace)
			if i == 0 {
				j := strings.IndexFunc(text, func(r rune) bool { return !unicode.IsSpace(r) })
				if j < 0 {
					j = len(text)
				}
				endWord()
				sp := strings.Repeat(" ", ansi.StringWidth(strings.ReplaceAll(text[:j], "\n", " ")))
				out = append(out, piece{parts: []run{{text: sp, st: rn.st}}, space: true, width: len(sp)})
				text = text[j:]
				continue
			}
			if i < 0 {
				i = len(text)
			}
			if word == nil {
				word = &piece{}
			}
			word.parts = append(word.parts, run{text: text[:i], st: rn.st})
			word.width += ansi.StringWidth(text[:i])
			text = text[i:]
		}
	}
	endWord()
	return out
}

// join paints a line's runs, merging neighbours that share a style.
func (r *renderer) join(runs []run) string {
	var b strings.Builder
	for i := 0; i < len(runs); {
		j, text := i+1, runs[i].text
		for j < len(runs) && runs[j].st == runs[i].st {
			text += runs[j].text
			j++
		}
		b.WriteString(paint(text, runs[i].st, r.links))
		i = j
	}
	return b.String()
}

// graphemes splits text into user-perceived characters, so emoji are never cut in half.
func graphemes(s string) []string {
	var out []string
	state := -1
	for s != "" {
		var g string
		g, s, _, state = uniseg.FirstGraphemeClusterInString(s, state)
		out = append(out, g)
	}
	return out
}
