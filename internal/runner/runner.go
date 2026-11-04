// Package runner fills command templates and runs them.
package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
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
	c := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	c.Dir = dir
	c.Env = os.Environ()
	if captured {
		c.Env = append(c.Env, "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1", "GIT_EDITOR=true", "NO_COLOR=1")
	}
	return c
}

// Run runs a script in dir and returns its combined output. Cancelling ctx stops the
// whole script: in `a && b` the shell is only the parent, and b would keep running and
// keep Run waiting on its output.
func Run(ctx context.Context, script, dir string) (string, error) {
	c := Command(ctx, script, dir, true)
	done := killGroup(c)
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	done()
	return strings.TrimRight(buf.String(), "\n"), err
}
