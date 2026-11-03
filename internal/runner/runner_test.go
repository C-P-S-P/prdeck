package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFill(t *testing.T) {
	vals := map[string]string{"base": "main", "remote": "origin", "title": `Fix "quotes"; rm -rf ~`, "path": "/tmp/a b"}
	cases := map[string]string{
		"git fetch {remote} {base} && git rebase {remote}/{base}": "git fetch origin main && git rebase origin/main",
		"git commit -m {title}":                                   `git commit -m 'Fix "quotes"; rm -rf ~'`,
		"zed {path}":                                              "zed '/tmp/a b'",
		"echo {unknown}":                                          "echo {unknown}",
	}
	for in, want := range cases {
		if got := Fill(in, vals); got != want {
			t.Errorf("Fill(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"":          "''",
		"feat/x-1":  "feat/x-1",
		"it's":      `'it'\''s'`,
		"~/home":    "'~/home'",
		"$(whoami)": "'$(whoami)'",
		"a`b`":      "'a`b`'",
		"x\ny":      "'x\ny'",
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

// A hostile PR title must reach the command as plain text.
func TestFilledTitleStaysText(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	title := `"; touch ` + marker + `; echo "`
	out, err := Run(context.Background(), Fill("printf '%s' {title}", map[string]string{"title": title}), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("title was executed as a command")
	}
	if out != title {
		t.Errorf("output = %q", out)
	}
}

func TestRunNoPrompt(t *testing.T) {
	out, err := Run(context.Background(), `echo "$GIT_TERMINAL_PROMPT $GIT_EDITOR"`, t.TempDir())
	if err != nil || strings.TrimSpace(out) != "0 true" {
		t.Errorf("out=%q err=%v", out, err)
	}
}
