package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// Cancelling must stop every command of a script, not only the shell, so a hung
// `git fetch` after `&&` cannot keep Run waiting.
func TestCancelStopsWholeScript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := Run(ctx, "sleep 5 && echo done", t.TempDir())
	if took := time.Since(start); err == nil || took > time.Second {
		t.Errorf("Run returned after %v, err=%v", took, err)
	}
}

// A command stopped by the terminal (ssh asking for a passphrase from a background
// process group) must be stopped by cancelling too: a stopped process that handles
// SIGTERM, as ssh does, keeps it pending until it runs again.
func TestCancelStopsStoppedCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, "trap 'exit 1' TERM; kill -STOP $$; echo late", t.TempDir())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("no error from a cancelled run")
		}
	case <-time.After(3 * time.Second):
		t.Error("Run did not return after cancel")
	}
}

// A command that ignores SIGTERM is killed after the grace period, so Run still returns.
func TestCancelKillsWhatIgnoresTerm(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := Run(ctx, "trap '' TERM; sleep 30", t.TempDir())
	if took := time.Since(start); err == nil || took > killGrace+2*time.Second {
		t.Errorf("Run returned after %v, err=%v", took, err)
	}
}

// Captured scripts have no controlling terminal: opening /dev/tty fails at once rather
// than stopping the script in the background.
func TestCapturedScriptHasNoTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// In a subshell: dash, Linux's sh, exits when exec's redirection fails.
	out, err := Run(ctx, "(exec 3</dev/tty) && echo has-tty || echo no-tty", t.TempDir())
	if err != nil || !strings.Contains(out, "no-tty") {
		t.Errorf("out=%q err=%v", out, err)
	}
}
