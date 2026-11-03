package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

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
	}
	for in, want := range cases {
		if got := githubToMarkdown(in); got != want {
			t.Errorf("githubToMarkdown(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestRenderedMarkdown(t *testing.T) {
	md := newMarkdown("dark")
	out := ansi.Strip(md.render("## Summary\n\nAdds a `PushSource`.\n\n> [!NOTE]\n> Run the migration.\n\n- [x] tests", 80))
	for _, want := range []string{"Summary", "PushSource", "Note", "Run the migration", "[✓] tests"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "##") || strings.Contains(out, "[!NOTE]") {
		t.Errorf("raw markdown left in output:\n%s", out)
	}
}

func TestWrapKeepsWordsTogether(t *testing.T) {
	md := newMarkdown("dark")
	src := "Each materialization run re-reads project metadata from the registry, about 1.8 s per run on large projects."
	for w := 40; w <= 100; w++ {
		for _, line := range strings.Split(ansi.Strip(md.render(src, w)), "\n") {
			if ansi.StringWidth(line) > w {
				t.Fatalf("width %d: line too long: %q", w, line)
			}
			if strings.TrimSpace(line) == "s" {
				t.Fatalf("width %d: orphaned word", w)
			}
		}
	}
}

func TestWideTableIsCutNotWrapped(t *testing.T) {
	md := newMarkdown("dark")
	src := "| name | description |\n|---|---|\n| a | " + strings.Repeat("long text ", 20) + "|"
	for _, line := range strings.Split(ansi.Strip(md.render(src, 50)), "\n") {
		if ansi.StringWidth(line) > 50 {
			t.Errorf("line wider than the pane: %q", line)
		}
	}
}
