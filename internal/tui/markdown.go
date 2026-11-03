package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
)

// markdown renders PR bodies and comments, caching by text for the current width.
//
// Rendering happens in three steps:
//  1. githubToMarkdown turns the GitHub-only parts (HTML tags, alerts, suggestion
//     blocks, @mentions) into plain markdown, leaving code blocks untouched.
//  2. Glamour renders it with a GitHub-like style and no wrapping.
//  3. wrapLines wraps to the pane width. Glamour's own wrapping wraps twice at some
//     widths and leaves single words on their own line.
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
		md.r = newRenderer(md.style)
	}
	if width != md.width {
		md.width, md.cache = width, map[string]string{}
	}
	if out, ok := md.cache[src]; ok {
		return out
	}
	out, err := md.r.Render(githubToMarkdown(src))
	if err != nil {
		out = src
	}
	out = wrapLines(trimBlankLines(out), width-1)
	md.cache[src] = out
	return out
}

func newRenderer(style string) *glamour.TermRenderer {
	opts := []glamour.TermRendererOption{glamour.WithWordWrap(0), glamour.WithEmoji()}
	if style == "dark" {
		opts = append(opts, glamour.WithStyles(githubDark()))
	} else {
		opts = append(opts, glamour.WithStandardStyle(style))
	}
	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		r, _ = glamour.NewTermRenderer(glamour.WithStyles(githubDark()), glamour.WithWordWrap(0))
	}
	return r
}

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }

// githubDark is glamour's dark style reworked to read like GitHub in the app's colors:
// headings without "##", subtle inline code, blue links, gray quotes.
func githubDark() gansi.StyleConfig {
	s := styles.DarkStyleConfig
	prim := func(color string, bold bool) gansi.StylePrimitive {
		return gansi.StylePrimitive{Color: sp(color), Bold: bp(bold)}
	}
	s.Document.StylePrimitive.Color = sp("#d0d4da")
	s.Heading = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{BlockSuffix: "\n", Color: sp("#e4e6ea"), Bold: bp(true)}}
	s.H1 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Color: sp("#ffffff"), Bold: bp(true), Underline: bp(true)}}
	s.H2 = gansi.StyleBlock{StylePrimitive: prim("#6fa8f5", true)}
	s.H3 = gansi.StyleBlock{StylePrimitive: prim("#e4e6ea", true)}
	s.H4 = gansi.StyleBlock{StylePrimitive: prim("#c9cdd4", true)}
	s.H5 = gansi.StyleBlock{StylePrimitive: prim("#c9cdd4", true)}
	s.H6 = gansi.StyleBlock{StylePrimitive: prim("#9aa1ad", true)}
	s.Strong = gansi.StylePrimitive{Bold: bp(true), Color: sp("#f0f2f5")}
	s.Code = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: " ", Suffix: " ", Color: sp("#e6e8eb"), BackgroundColor: sp("#2d323b")}}
	s.Link = gansi.StylePrimitive{Color: sp("#6fa8f5"), Underline: bp(true)}
	s.LinkText = gansi.StylePrimitive{Color: sp("#6fa8f5"), Bold: bp(true)}
	s.Image = gansi.StylePrimitive{Color: sp("#7c8390")}
	s.ImageText = gansi.StylePrimitive{Color: sp("#b98ee6"), Format: "[image: {{.text}}]"}
	s.BlockQuote = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Color: sp("#9aa1ad")}, Indent: s.BlockQuote.Indent, IndentToken: sp("▎ ")}
	s.HorizontalRule = gansi.StylePrimitive{Color: sp("#3a3f48"), Format: "\n" + strings.Repeat("─", 40) + "\n"}
	s.Task = gansi.StyleTask{Ticked: "[✓] ", Unticked: "[ ] "}
	return s
}

var (
	fence        = regexp.MustCompile("^ {0,3}(```|~~~)")
	alertLine    = regexp.MustCompile(`^(\s*>\s*)\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)
	htmlComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	imgTag       = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	attrAlt      = regexp.MustCompile(`(?i)\balt\s*=\s*"([^"]*)"`)
	attrSrc      = regexp.MustCompile(`(?i)\bsrc\s*=\s*"([^"]*)"`)
	anchorTag    = regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*"([^"]*)"[^>]*>(.*?)</a>`)
	summaryTag   = regexp.MustCompile(`(?is)<summary>\s*(.*?)\s*</summary>`)
	brTag        = regexp.MustCompile(`(?i)<br\s*/?>`)
	codeTag      = regexp.MustCompile(`(?is)<(kbd|code)>(.*?)</(kbd|code)>`)
	boldTag      = regexp.MustCompile(`(?is)<(b|strong)>(.*?)</(b|strong)>`)
	italicTag    = regexp.MustCompile(`(?is)<(i|em)>(.*?)</(i|em)>`)
	otherTag     = regexp.MustCompile(`(?i)</?(details|summary|p|div|span|sub|sup|center|picture|source|ins|del|u|small|big|h[1-6]|a)\b[^>]*>`)
	mention      = regexp.MustCompile(`(^|[\s(\[])@([A-Za-z0-9][A-Za-z0-9-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)?)`)
	alertHeading = map[string]string{"NOTE": "Note", "TIP": "Tip", "IMPORTANT": "Important", "WARNING": "Warning", "CAUTION": "Caution"}
)

// githubToMarkdown rewrites GitHub-only syntax into markdown glamour understands.
// Fenced code blocks pass through untouched.
func githubToMarkdown(src string) string {
	var out, text []string
	flush := func() {
		if len(text) > 0 {
			out = append(out, convertHTML(strings.Join(text, "\n")))
			text = nil
		}
	}
	inCode := false
	for _, line := range strings.Split(src, "\n") {
		if fence.MatchString(line) {
			if !inCode {
				flush()
				if lang := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "`~")); lang == "suggestion" {
					out = append(out, "**Suggested change**", "")
					line = strings.Replace(line, "suggestion", "", 1)
				}
			}
			inCode = !inCode
			out = append(out, line)
			continue
		}
		if inCode {
			out = append(out, line)
			continue
		}
		if m := alertLine.FindStringSubmatch(line); m != nil {
			line = m[1] + "**" + alertHeading[strings.ToUpper(m[2])] + "**"
		}
		text = append(text, line)
	}
	flush()
	return strings.Join(out, "\n")
}

// convertHTML handles the HTML that commonly appears in PR bodies and bot comments.
func convertHTML(s string) string {
	s = htmlComment.ReplaceAllString(s, "")
	s = imgTag.ReplaceAllStringFunc(s, func(tag string) string {
		alt, src := "image", ""
		if m := attrAlt.FindStringSubmatch(tag); m != nil && strings.TrimSpace(m[1]) != "" {
			alt = strings.TrimSpace(m[1])
		}
		if m := attrSrc.FindStringSubmatch(tag); m != nil {
			src = m[1]
		}
		return "![" + alt + "](" + src + ")"
	})
	s = anchorTag.ReplaceAllString(s, "[$2]($1)")
	s = summaryTag.ReplaceAllString(s, "\n**▸ $1**\n")
	// A real line break would fall out of list items, so <br> becomes a space.
	s = brTag.ReplaceAllString(s, " ")
	s = codeTag.ReplaceAllString(s, "`$2`")
	s = boldTag.ReplaceAllString(s, "**$2**")
	s = italicTag.ReplaceAllString(s, "*$2*")
	s = otherTag.ReplaceAllString(s, "")
	return boldMentions(s)
}

// boldMentions makes @user stand out like on GitHub, skipping inline code.
func boldMentions(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		parts := strings.Split(line, "`")
		for j := 0; j < len(parts); j += 2 {
			parts[j] = mention.ReplaceAllString(parts[j], "$1**@$2**")
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "\n")
}

var listMarker = regexp.MustCompile(`^(• |\d+\. |\[[✓ x]\] |▎ )`)

// wrapLines word-wraps each rendered line to width. Continuation lines are indented
// under the text of list items and quotes; table rows are cut, not wrapped.
func wrapLines(s string, width int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " ")
		if ansi.StringWidth(line) <= width {
			out = append(out, line)
			continue
		}
		plain := ansi.Strip(line)
		trimmed := strings.TrimLeft(plain, " ")
		indent := len(plain) - len(trimmed)
		if strings.Contains(trimmed, " │ ") || strings.Contains(trimmed, "─┼─") {
			out = append(out, ansi.Truncate(line, width, "…"))
			continue
		}
		hang := indent
		if m := listMarker.FindString(trimmed); m != "" {
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
