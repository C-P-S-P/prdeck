// Package runner fills command templates and runs them.
package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

var placeholder = regexp.MustCompile(`\{([a-z]+)\}`)

// Fill replaces {name} placeholders with shell-quoted values. Unknown placeholders are left
// as they are. Values come from other people's PRs (titles, branch names), so quoting is
// what keeps a title like `"; rm -rf ~` from becoming part of the command.
func Fill(tmpl string, vals map[string]string) string {
	return placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		v, ok := vals[m[1:len(m)-1]]
		if !ok {
			return m
		}
		return Quote(v)
	})
}

var safe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,~-]+$`)

// Quote returns s as a single shell word.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if safe.MatchString(s) && !strings.HasPrefix(s, "~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Command builds the process for a script run in dir. When captured is true the
// process gets no terminal: prompts are turned off so it can never hang waiting for input.
func Command(ctx context.Context, script, dir string, captured bool) *exec.Cmd {
	sh, err := Shell()
	arg := script
	if runtime.GOOS == "windows" {
		// Git's sh splits its command line by Cygwin's rules, not the ones Go quotes
		// it for (a \\ in quotes becomes \), so the script travels in the environment.
		arg = `eval "$PRDECK_SCRIPT"`
	}
	c := exec.CommandContext(ctx, sh, "-c", arg)
	if err != nil {
		c.Err = err // Start says what is missing
	}
	c.Dir = dir
	c.Env = os.Environ()
	if runtime.GOOS == "windows" {
		c.Cancel = func() error { return killTree(c) }
		c.Env = append(c.Env, "PRDECK_SCRIPT="+script)
	}
	if captured {
		c.Env = append(c.Env, "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1", "GIT_EDITOR=true", "NO_COLOR=1")
	}
	return c
}

// Run runs a script in dir and returns its combined output.
func Run(ctx context.Context, script, dir string) (string, error) {
	c := Command(ctx, script, dir, true)
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	out := buf.String()
	if runtime.GOOS == "windows" {
		out = strings.ReplaceAll(out, "\r\n", "\n") // what Windows programs end lines with
	}
	return strings.TrimRight(out, "\n"), err
}
