//go:build !unix

package runner

import "os/exec"

// killGroup does nothing here: cancelling stops only the shell.
func killGroup(c *exec.Cmd) (done func()) { return func() {} }
