//go:build !unix && !windows

package runner

import "os/exec"

// killGroup does nothing here: cancelling stops only the shell.
func killGroup(c *exec.Cmd) (started, done func()) { return func() {}, func() {} }
