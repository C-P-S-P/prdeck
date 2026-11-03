package mdrender

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Options control rendering.
type Options struct {
	Width int  // no line is wider than this
	Links bool // make links terminal hyperlinks (OSC 8); terminals without support ignore them
}

var parser = goldmark.New(goldmark.WithExtensions(extension.GFM, emoji.Emoji))

type renderer struct {
	src      []byte
	links    bool
	override map[ast.Node]style // per-node styles, e.g. an alert's heading
	depth    int                // list nesting
	quoted   int                // blockquote nesting
}

// lineEnds turns \r\n and a lone \r (both line ends, as on GitHub) into \n. A \r that
// reached the terminal, in a code block say, would move back to the start of the line
// and let the rest of it overwrite what was shown.
var lineEnds = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// Render turns GitHub markdown into terminal lines, joined with newlines.
func Render(markdown string, o Options) string {
	src := []byte(GitHubToMarkdown(noControls(lineEnds.Replace(markdown), "\n\t")))
	doc := parser.Parser().Parse(text.NewReader(src))
	r := &renderer{src: src, links: o.Links, override: map[ast.Node]style{}}
	w := max(o.Width, 20)
	lines := r.blocks(doc, w, false)
	for i, l := range lines {
		if ansi.StringWidth(l) > w { // a safety net; blocks keep within width themselves
			lines[i] = ansi.Truncate(l, w-1, "…")
		}
	}
	return strings.Join(lines, "\n")
}

// blocks renders a node's block children, a blank line between them unless tight.
func (r *renderer) blocks(n ast.Node, w int, tight bool) []string {
	var out []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		lines := r.block(c, w)
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 && !tight {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	return out
}

func (r *renderer) text() style {
	if r.quoted > 0 {
		return style{fg: "#aab0bb"}
	}
	return style{fg: cText}
}

func (r *renderer) block(n ast.Node, w int) []string {
	w = max(w, 1) // each quote or list level takes columns; deep nesting would go below zero
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return r.wrap(r.inline(n, r.text()), w)
	case *ast.Heading:
		st := style{bold: true, fg: cText}
		switch n.Level {
		case 1:
			st.fg = cBright
		case 2:
			st.fg = cBlue
		case 3:
			st.fg = cBright
		}
		lines := r.wrap(r.inline(n, st), w)
		if n.Level <= 2 {
			lines = append(lines, paint(strings.Repeat("─", w), style{fg: cBorder}, false))
		}
		return lines
	case *ast.ThematicBreak:
		return []string{paint(strings.Repeat("─", w), style{fg: cBorder}, false)}
	case *ast.Blockquote:
		return r.quote(n, w)
	case *ast.List:
		return r.list(n, w)
	case *ast.FencedCodeBlock:
		return r.code(n, string(n.Language(r.src)), w)
	case *ast.CodeBlock:
		return r.code(n, "", w)
	case *extast.Table:
		return r.table(n, w)
	case *ast.HTMLBlock:
		return r.html(n, w)
	}
	return r.blocks(n, w, false)
}

var alertColors = map[string]string{"Note": cBlue, "Tip": cGreen, "Important": cPurple, "Warning": cYellow, "Caution": cRed}

// quote draws a bar beside the quoted blocks. GitHub alerts (already turned into a
// bold first word by the cleanup step) get a bar and heading in their own color.
func (r *renderer) quote(n *ast.Blockquote, w int) []string {
	bar := cBorder
	alert := false
	if p, ok := n.FirstChild().(*ast.Paragraph); ok {
		if e, ok := p.FirstChild().(*ast.Emphasis); ok && e.Level == 2 {
			if c, ok := alertColors[plainText(e, r.src)]; ok {
				bar, alert = c, true
				r.override[e] = style{fg: c, bold: true}
			}
		}
	}
	if !alert {
		r.quoted++
		defer func() { r.quoted-- }()
	}
	var out []string
	for _, l := range r.blocks(n, w-2, false) {
		out = append(out, paint("▎", style{fg: bar}, false)+" "+l)
	}
	return out
}

var bullets = []string{"•", "◦", "▪"}

func (r *renderer) list(n *ast.List, w int) []string {
	r.depth++
	defer func() { r.depth-- }()
	count := n.ChildCount()
	start := n.Start
	if start == 0 {
		start = 1
	}
	numW := len(strconv.Itoa(start + count - 1))
	var out []string
	i := 0
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		marker, mst := bullets[(r.depth-1)%len(bullets)]+" ", style{fg: cDim}
		if n.IsOrdered() {
			num := strconv.Itoa(start + i)
			marker = strings.Repeat(" ", numW-len(num)) + num + ". "
		}
		if box := taskBox(item); box != nil {
			marker, mst = "[ ] ", style{fg: cDim}
			if box.IsChecked {
				marker, mst = "[✓] ", style{fg: cGreen}
			}
		}
		mw := ansi.StringWidth(marker)
		body := r.blocks(item, w-mw, n.IsTight)
		if len(body) == 0 {
			body = []string{""}
		}
		if !n.IsTight && i > 0 {
			out = append(out, "")
		}
		for j, l := range body {
			if j == 0 {
				out = append(out, paint(marker, mst, false)+l)
			} else {
				out = append(out, strings.Repeat(" ", mw)+l)
			}
		}
		i++
	}
	return out
}

func taskBox(item ast.Node) *extast.TaskCheckBox {
	if first := item.FirstChild(); first != nil {
		if box, ok := first.FirstChild().(*extast.TaskCheckBox); ok {
			return box
		}
	}
	return nil
}

var (
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// html shows the text inside an HTML block the cleanup step didn't convert.
func (r *renderer) html(n *ast.HTMLBlock, w int) []string {
	var b strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		b.Write(seg.Value(r.src))
	}
	t := strings.TrimSpace(spaceRe.ReplaceAllString(tagRe.ReplaceAllString(b.String(), " "), " "))
	if t == "" {
		return nil
	}
	return r.wrap([]run{{text: t, st: style{fg: cDim}}}, w)
}
