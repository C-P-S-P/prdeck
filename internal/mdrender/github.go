package mdrender

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark-emoji/definition"
)

// The GitHub cleanup step: PR bodies and bot comments are full of HTML and GitHub-only
// syntax. This turns the common cases into plain markdown before parsing.

var (
	fence        = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
	alertLine    = regexp.MustCompile(`^(\s*>\s*)\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)
	htmlComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	commentStart = regexp.MustCompile(`^ {0,3}<!--`)
	imgTag       = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	attrAlt      = regexp.MustCompile(`(?i)\balt\s*=\s*"([^"]*)"`)
	attrSrc      = regexp.MustCompile(`(?i)\bsrc\s*=\s*"([^"]*)"`)
	anchorTag    = regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*"([^"]*)"[^>]*>(.*?)</a>`)
	summaryTag   = regexp.MustCompile(`(?is)<summary>\s*(.*?)\s*</summary>`)
	brTag        = regexp.MustCompile(`(?i)<br\s*/?>`)
	codeTag      = regexp.MustCompile(`(?is)<(kbd|code)>(.*?)</(kbd|code)>`)
	boldTag      = regexp.MustCompile(`(?is)<(b|strong)>(.*?)</(b|strong)>`)
	italicTag    = regexp.MustCompile(`(?is)<(i|em)>(.*?)</(i|em)>`)
	otherTag     = regexp.MustCompile(`(?i)</?(details|summary|p|div|span|sub|sup|center|picture|source|ins|del|u|small|big|h[1-6]|a|dl|dt|dd|blockquote|b|strong|i|em|code|kbd)\b[^>]*>`)
	mention      = regexp.MustCompile(`(^|[\s(\[])@([A-Za-z0-9][A-Za-z0-9-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)?)`)
	alertHeading = map[string]string{"NOTE": "Note", "TIP": "Tip", "IMPORTANT": "Important", "WARNING": "Warning", "CAUTION": "Caution"}
)

// GitHubToMarkdown rewrites GitHub-only syntax into markdown glamour understands.
// Fenced code blocks pass through untouched.
func GitHubToMarkdown(src string) string {
	var out, text []string
	flush := func() {
		if len(text) > 0 {
			out = append(out, convertHTML(strings.Join(text, "\n")))
			text = nil
		}
	}
	open := ""       // the fence of the code block we are in
	comment := false // in an HTML comment, where a fence is only text
	for _, line := range strings.Split(src, "\n") {
		if next, ok := fenceLine(line, open); ok && !comment {
			if open == "" {
				flush()
				if lang := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "`~")); lang == "suggestion" {
					out = append(out, "**Suggested change**", "")
					line = strings.Replace(line, "suggestion", "", 1)
				}
			}
			open = next
			out = append(out, line)
			continue
		}
		if open != "" {
			out = append(out, line)
			continue
		}
		comment = inComment(line, comment)
		if m := alertLine.FindStringSubmatch(line); m != nil {
			line = m[1] + "**" + alertHeading[strings.ToUpper(m[2])] + "**"
		}
		text = append(text, line)
	}
	flush()
	return strings.Join(out, "\n")
}

// fenceLine reports whether line opens a code block (open is "") or closes the one
// open began, read as CommonMark does: a backtick fence has no backtick after it
// (```npm install``` is inline code), and a closing fence is the same character, at
// least as many of them, with nothing after. next is the fence then open, or "".
func fenceLine(line, open string) (next string, ok bool) {
	m := fence.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	if open == "" {
		return m[1], m[1][0] == '~' || !strings.Contains(m[2], "`")
	}
	return "", m[1][0] == open[0] && len(m[1]) >= len(open) && strings.TrimSpace(m[2]) == ""
}

// inComment reports whether an HTML comment is open after line, given whether one
// was before it. A comment that starts a line runs to the line with "-->", as in
// CommonMark, so a commented-out template block ending "``` -->" is not left open.
func inComment(line string, open bool) bool {
	if !open {
		if !commentStart.MatchString(line) {
			return false
		}
		line = line[strings.Index(line, "<!--")+4:]
	}
	return !strings.Contains(line, "-->")
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

// GitHubForDesktop prepares a PR or comment body for the desktop app, whose renderer
// understands HTML (<img width>, <details>, <br>) and GitHub alerts itself. It only
// drops HTML comments, labels suggestion blocks, and links @mentions to their
// profiles. Fenced code blocks pass through untouched.
func GitHubForDesktop(src string) string {
	var out []string
	open := ""       // the fence of the code block we are in
	comment := false // in an HTML comment, where a fence is only text
	text := []string{}
	flush := func() {
		if len(text) > 0 {
			s := htmlComment.ReplaceAllString(strings.Join(text, "\n"), "")
			out = append(out, linkMentions(s))
			text = text[:0]
		}
	}
	for _, line := range strings.Split(src, "\n") {
		if next, ok := fenceLine(line, open); ok && !comment {
			if open == "" {
				flush()
				if lang := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "`~")); lang == "suggestion" {
					out = append(out, "**Suggested change**", "")
					line = strings.Replace(line, "suggestion", "diff", 1)
				}
			}
			open = next
			out = append(out, line)
			continue
		}
		if open != "" {
			out = append(out, line)
			continue
		}
		comment = inComment(line, comment)
		text = append(text, line)
	}
	flush()
	return strings.Join(out, "\n")
}

// mentionLink is a mention not already inside a link or an email address.
var mentionLink = regexp.MustCompile(`(^|[\s(])@([A-Za-z0-9][A-Za-z0-9-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)?)`)

var (
	shortcode = regexp.MustCompile(`:([a-z0-9_+\-]+):`)
	emojis    = definition.Github()
)

// emojiShortcodes turns :rocket: into 🚀, as GitHub does.
func emojiShortcodes(s string) string {
	return shortcode.ReplaceAllStringFunc(s, func(m string) string {
		if e, ok := emojis.Get(m[1 : len(m)-1]); ok && len(e.Unicode) > 0 {
			return string(e.Unicode)
		}
		return m
	})
}

// linkMentions turns @user into a link to their profile (teams link to the org's
// team page) and emoji shortcodes into emoji, skipping inline code.
func linkMentions(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		parts := strings.Split(line, "`")
		for j := 0; j < len(parts); j += 2 {
			parts[j] = mentionLink.ReplaceAllStringFunc(parts[j], func(m string) string {
				sub := mentionLink.FindStringSubmatch(m)
				who := sub[2]
				href := "https://github.com/" + who
				if org, team, ok := strings.Cut(who, "/"); ok {
					href = "https://github.com/orgs/" + org + "/teams/" + team
				}
				return sub[1] + "[@" + who + "](" + href + ")"
			})
			parts[j] = emojiShortcodes(parts[j])
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "\n")
}
