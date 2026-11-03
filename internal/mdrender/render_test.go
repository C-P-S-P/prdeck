package mdrender

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func plain(s string) string { return ansi.Strip(s) }

func TestGitHubToMarkdown(t *testing.T) {
	cases := map[string]string{
		"<!-- template note -->\nHello":                           "\nHello",
		`<img width="600" alt="Console" src="https://x/y.png" />`: "![Console](https://x/y.png)",
		`<img src="https://x/y.png">`:                             "![image](https://x/y.png)",
		"<details>\n<summary>Bench</summary>\n\nbody\n</details>": "\n\n**▸ Bench**\n\n\nbody\n",
		"one<br>two":                                     "one two",
		"Press <kbd>Ctrl</kbd> and <b>go</b>":            "Press `Ctrl` and **go**",
		`see <a href="https://x">docs</a>`:               "see [docs](https://x)",
		"> [!WARNING]\n> careful":                        "> **Warning**\n> careful",
		"cc @mlin and @org/team, not a@b.com or `@code`": "cc **@mlin** and **@org/team**, not a@b.com or `@code`",
		"```suggestion\nreturn x\n```":                   "**Suggested change**\n\n```\nreturn x\n```",
		"```html\n<!-- keep --><br>\n@nobody\n```":       "```html\n<!-- keep --><br>\n@nobody\n```",
		"<dl><dd>\n\n- a\n\n</dd></dl>":                  "\n\n- a\n\n",
	}
	for in, want := range cases {
		if got := GitHubToMarkdown(in); got != want {
			t.Errorf("GitHubToMarkdown(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestBlocks(t *testing.T) {
	src := "## Summary\n\nAdds a `PushSource`.\n\n> [!NOTE]\n> Run the migration.\n\n- [x] tests\n- [ ] docs\n\n1. one\n2. two\n\n~~old~~ :warning:"
	out := plain(Render(src, Options{Width: 60}))
	for _, want := range []string{"Summary", " PushSource ", "▎ Note", "▎ Run the migration.", "[✓] tests", "[ ] docs", "1. one", "2. two", "old ⚠"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, bad := range []string{"##", "[!NOTE]", ":warning:", "**"} {
		if strings.Contains(out, bad) {
			t.Errorf("raw markdown %q left in\n%s", bad, out)
		}
	}
}

func TestLinksShowTextOnly(t *testing.T) {
	long := "https://app.codecov.io/gh/feast-dev/feast/pull/7081?dropdown=coverage&src=pr&el=h1&utm_medium=referral&utm_source=github"
	out := Render("[Codecov]("+long+") Report and "+long, Options{Width: 80, Links: true})
	text := plain(out)
	if strings.Contains(text, "utm_medium") {
		t.Errorf("URL text leaked into the page:\n%s", text)
	}
	if !strings.Contains(text, "Codecov Report") || !strings.Contains(text, "app.codecov.io/gh/feast-dev/feast/pull/7081…") {
		t.Errorf("link text missing:\n%s", text)
	}
	if !strings.Contains(out, "\x1b]8;;"+long+"\x1b\\") {
		t.Error("no terminal hyperlink to the full URL")
	}
	if strings.Contains(Render("[a](https://x)", Options{Width: 40}), "\x1b]8;;") {
		t.Error("hyperlinks should be off unless asked for")
	}
}

func TestTableFitsWidth(t *testing.T) {
	src := "| [Files with missing lines](https://x/y) | Patch % | Lines |\n|---|---|---|\n" +
		"| [sdk/python/feast/schema\\_discovery/introspector.py](https://x/z) | 50.74% | [32 Missing and 1 partial :warning: ](https://x/w) |\n" +
		"| [sdk/python/feast/schema\\_discovery/service.py](https://x/a) | 75.86% | [14 Missing :warning: ](https://x/b) |\n"
	for _, w := range []int{30, 50, 70, 96, 140} {
		out := Render(src, Options{Width: w, Links: true})
		lines := strings.Split(out, "\n")
		for _, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: table line is %d wide: %q", w, ansi.StringWidth(l), plain(l))
			}
		}
		text := plain(out)
		if w < 50 {
			continue // narrow panes may wrap any column; staying inside the pane is what counts
		}
		if !strings.Contains(text, "┌") || !strings.Contains(text, "Patch %") || !strings.Contains(text, "50.74%") {
			t.Errorf("width %d: table content missing:\n%s", w, text)
		}
	}
	// At a comfortable width every row stays on one line.
	if n := len(strings.Split(Render(src, Options{Width: 140}), "\n")); n != 6 {
		t.Errorf("wide table took %d lines, want 6 (top, header, rule, 2 rows, bottom)", n)
	}
}

func TestCodeIsNotWrapped(t *testing.T) {
	src := "```python\ndef push(self, name, df, *, allow_registry_cache=True, and_a_lot_more_arguments=None):\n    return 1\n```"
	out := Render(src, Options{Width: 40})
	lines := strings.Split(plain(out), "\n")
	if len(lines) != 3 { // label, two code lines
		t.Fatalf("code took %d lines:\n%s", len(lines), plain(out))
	}
	if !strings.HasSuffix(strings.TrimRight(lines[1], " "), "…") {
		t.Errorf("long code line should end in …: %q", lines[1])
	}
	for _, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) != 40 {
			t.Errorf("code panel line is %d wide, want 40", ansi.StringWidth(l))
		}
	}
}

func TestNeverWiderThanPane(t *testing.T) {
	src := "Each materialization run re-reads project metadata from the registry, about 1.8 s per run.\n\n" +
		"- a list item that is long enough to wrap onto a second line inside a narrow pane\n" +
		"  - nested item with `inline code` and a [link](https://example.com/a/very/long/path)\n\n" +
		"> quoted text that also wraps at narrow widths because it is long\n\n" +
		"averyveryverylongwordwithoutanyspacesthatmustbesplit"
	for w := 20; w <= 100; w++ {
		for _, l := range strings.Split(Render(src, Options{Width: w, Links: true}), "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: %q is %d wide", w, plain(l), ansi.StringWidth(l))
			}
			if strings.TrimSpace(plain(l)) == "s" {
				t.Fatalf("width %d: orphaned word", w)
			}
		}
	}
}

func TestNewlinesAreLineBreaks(t *testing.T) {
	// GitHub shows a single newline in a PR or comment as a line break.
	out := plain(Render(":x: Patch coverage is low.\n:white_check_mark: Project coverage is fine.", Options{Width: 80}))
	if lines := strings.Split(out, "\n"); len(lines) != 2 || !strings.HasPrefix(lines[1], "✅") {
		t.Errorf("got %q", out)
	}
}

func TestIconImagesAreDropped(t *testing.T) {
	out := plain(Render(`<summary><img src="https://x/cog.svg" alt="image"> Run configuration</summary>`, Options{Width: 60}))
	if strings.Contains(out, "[image") || !strings.Contains(out, "Run configuration") {
		t.Errorf("got %q", out)
	}
	if out := plain(Render("![Impacted file tree](https://x/t.svg)", Options{Width: 60})); !strings.Contains(out, "[image: Impacted file tree]") {
		t.Errorf("a described image should stay: %q", out)
	}
}

func TestWrappedRowsGetDividers(t *testing.T) {
	src := "| a | b |\n|---|---|\n| one | short |\n| two | " + strings.Repeat("long text ", 12) + "|\n"
	out := plain(Render(src, Options{Width: 40}))
	if strings.Count(out, "├") != 2 { // after the header, and between the two rows
		t.Errorf("want a divider between wrapped rows:\n%s", out)
	}
	if out := plain(Render("| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n", Options{Width: 40})); strings.Count(out, "├") != 1 {
		t.Errorf("one-line rows need no dividers:\n%s", out)
	}
}

func TestGitHubForDesktop(t *testing.T) {
	cases := map[string]string{
		"<!-- template -->\nHi @mlin :rocket:":           "\nHi [@mlin](https://github.com/mlin) 🚀",
		"`@code :rocket:` stays":                         "`@code :rocket:` stays",
		"cc @org/team, not a@b.com":                      "cc [@org/team](https://github.com/orgs/org/teams/team), not a@b.com",
		"> [!NOTE]\n> <img width=\"300\" src=\"x.png\">": "> [!NOTE]\n> <img width=\"300\" src=\"x.png\">",
		"```suggestion\nreturn x\n```":                   "**Suggested change**\n\n```diff\nreturn x\n```",
		"time 10:30:00 and :not_an_emoji:":               "time 10:30:00 and :not_an_emoji:",
	}
	for in, want := range cases {
		if got := GitHubForDesktop(in); got != want {
			t.Errorf("GitHubForDesktop(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestHostileTextIsHarmless(t *testing.T) {
	// Each quote or list level takes columns; deep nesting used to panic.
	deep := strings.Repeat("> ", 50) + "---\n\n" + strings.Repeat("- ", 30) + "# x\n\n" + strings.Repeat("> ", 30) + "```\ncode\n```"
	for _, w := range []int{20, 97} {
		for _, l := range strings.Split(Render(deep, Options{Width: w}), "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d: %q is %d wide", w, plain(l), ansi.StringWidth(l))
			}
		}
	}

	// Escape sequences in PR text, written as entities or raw, never reach the terminal.
	src := "a &#27;]52;c;eA==&#7; &#155;2J `&#27;]0;t&#7;` raw\x1b]0;t\x07 ![al&#27;[2Jt](u)\n\n```\nx\x1b[2J\n```\n\n# h&#13;i" +
		"\n\n```suggestion\nrm -rf ~\recho safe\n```"
	out := Render(src, Options{Width: 80, Links: true})
	for _, bad := range []string{"\x1b]52", "\x1b]0", "\x1b[2J", "\a", "\u009b", "\r"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q reached the output:\n%q", bad, out)
		}
	}
	if !strings.Contains(plain(out), "]52;c;eA==") || !strings.Contains(plain(out), "hi") {
		t.Errorf("text around the escapes went missing:\n%s", plain(out))
	}
	// A lone \r ends a line, as on GitHub, so a suggestion can't hide its first part.
	if !strings.Contains(plain(out), " rm -rf ~ ") || !strings.Contains(plain(out), " echo safe ") {
		t.Errorf("a \\r in a code block hid part of it:\n%s", plain(out))
	}
}
