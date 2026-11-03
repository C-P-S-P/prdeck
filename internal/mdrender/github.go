package mdrender

import (
	"regexp"
	"strings"
)

// The GitHub cleanup step: PR bodies and bot comments are full of HTML and GitHub-only
// syntax. This turns the common cases into plain markdown before parsing.

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
