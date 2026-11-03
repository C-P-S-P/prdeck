// Package mdrender renders GitHub markdown (PR descriptions, comments, bot reports) for a
// terminal pane of a fixed width.
//
// It parses with goldmark and draws every block itself, so that links show only their
// text (as terminal hyperlinks), tables shrink to fit the pane, code is never wrapped,
// and no line is ever wider than the pane.
package mdrender

import (
	"strconv"
	"strings"
)

// Colors match the rest of the app.
const (
	cText   = "#d0d4da"
	cBright = "#f0f2f5"
	cDim    = "#8a919d"
	cFaint  = "#5c636e"
	cBorder = "#3a3f48"
	cBlue   = "#6fa8f5"
	cGreen  = "#8cc47a"
	cYellow = "#e0c070"
	cRed    = "#e57b7b"
	cPurple = "#b98ee6"
	cCodeFg = "#e6e8eb"
	cCodeBg = "#2b3038"
	cPanel  = "#171a1f"
)

// style is one text style. link, when set, makes the text a terminal hyperlink (OSC 8).
type style struct {
	fg, bg                                 string
	bold, italic, underline, strike, faint bool
	link                                   string
}

func (s style) sgr() string {
	var codes []string
	if s.bold {
		codes = append(codes, "1")
	}
	if s.faint {
		codes = append(codes, "2")
	}
	if s.italic {
		codes = append(codes, "3")
	}
	if s.underline {
		codes = append(codes, "4")
	}
	if s.strike {
		codes = append(codes, "9")
	}
	if c := rgb(s.fg); c != "" {
		codes = append(codes, "38;2;"+c)
	}
	if c := rgb(s.bg); c != "" {
		codes = append(codes, "48;2;"+c)
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func rgb(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return ""
	}
	return strconv.Itoa(int(v>>16)) + ";" + strconv.Itoa(int(v>>8&0xff)) + ";" + strconv.Itoa(int(v&0xff))
}

// paint styles text; every piece closes its own style so lines can be cut anywhere.
func paint(text string, s style, links bool) string {
	if text == "" {
		return ""
	}
	out := text
	if sgr := s.sgr(); sgr != "" {
		out = sgr + text + "\x1b[0m"
	}
	if links && s.link != "" {
		out = "\x1b]8;;" + s.link + "\x1b\\" + out + "\x1b]8;;\x1b\\"
	}
	return out
}
