//go:build unix

package runner

import (
	"os"
	"os/exec"
	"syscall"
)

// killGroup gives c its own process group and makes cancelling it signal the whole
// group, so every command of the script stops, not only the shell.
func killGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		err := syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
		// A command stopped for reading the terminal (ssh asking for a passphrase, now
		// that it is not in the terminal's group) only gets the SIGTERM once it runs.
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGCONT)
		if err == syscall.ESRCH {
			return os.ErrProcessDone // the script ended as ctx was cancelled
		}
		return err
	}
}
