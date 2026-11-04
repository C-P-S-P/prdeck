package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

// Git for Windows' sh reads its command line by Cygwin's rules, which halve a \\ that Go
// writes as is: a UNC path or a regex must still reach the script whole.
func TestRunKeepsBackslashes(t *testing.T) {
	const s = `\\server\share\x "q" a\\b\`
	out, err := Run(context.Background(), "printf '%s' "+Quote(s), t.TempDir())
	if err != nil || out != s {
		t.Errorf("out=%q err=%v, want %q", out, err, s)
	}
}

func TestRunNoPrompt(t *testing.T) {
	out, err := Run(context.Background(), `echo "$GIT_TERMINAL_PROMPT $GIT_EDITOR"`, t.TempDir())
	if err != nil || strings.TrimSpace(out) != "0 true" {
		t.Errorf("out=%q err=%v", out, err)
	}
}

// Output lines end in \n everywhere; elsewhere than Windows output is kept as it is.
func TestRunLineEnds(t *testing.T) {
	out, err := Run(context.Background(), `printf 'a\r\nb\r\n'`, t.TempDir())
	want := "a\r\nb\r"
	if runtime.GOOS == "windows" {
		want = "a\nb"
	}
	if err != nil || out != want {
		t.Errorf("out=%q err=%v, want %q", out, err, want)
	}
}

func TestFindShell(t *testing.T) {
	git := filepath.Join("/pf", "Git", "cmd", "git.exe")
	bin := filepath.Join("/pf", "Git", "bin", "sh.exe")
	usr := filepath.Join("/pf", "Git", "usr", "bin", "sh.exe")
	user := filepath.Join("/local", "Programs", "Git", "bin", "sh.exe")
	env := map[string]string{"ProgramFiles": "/nowhere", "LOCALAPPDATA": "/local"}
	cases := []struct {
		name, goos string
		path       map[string]string // what lookPath finds
		files      []string
		want       string
	}{
		{"mac", "darwin", nil, nil, "/bin/sh"},
		{"linux", "linux", map[string]string{"sh": "/usr/bin/sh"}, nil, "/bin/sh"},
		{"sh on PATH", "windows", map[string]string{"sh": usr, "git": git}, []string{bin}, usr},
		{"next to git", "windows", map[string]string{"git": git}, []string{bin, usr}, bin},
		{"only usr/bin", "windows", map[string]string{"git": git}, []string{usr}, usr},
		{"git in mingw64", "windows", map[string]string{"git": filepath.Join("/pf", "Git", "mingw64", "bin", "git.exe")}, []string{bin}, bin},
		{"git not on PATH", "windows", nil, []string{user}, user},
		{"no git", "windows", nil, nil, ""},
	}
	for _, c := range cases {
		lookPath := func(name string) (string, error) {
			if p, ok := c.path[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		}
		isFile := func(p string) bool { return slices.Contains(c.files, p) }
		got, err := findShell(c.goos, lookPath, func(k string) string { return env[k] }, isFile)
		if got != c.want || (err != nil) != (c.want == "") {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// Windows has no process groups: cancelling has to stop the commands sh started, or a
// hung command after && keeps Run waiting on its output.
func TestCancelStopsTreeOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only: there the process tree is killed with taskkill")
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	start := time.Now()
	_, err := Run(ctx, "sleep 10 && echo done", t.TempDir())
	if took := time.Since(start); err == nil || took > 5*time.Second {
		t.Errorf("Run returned after %v, err=%v", took, err)
	}
}
