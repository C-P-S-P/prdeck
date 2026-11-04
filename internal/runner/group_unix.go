//go:build unix

package runner

import (
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"
)

// killGrace is how long a cancelled script gets to stop on SIGTERM before SIGKILL.
const killGrace = 3 * time.Second

// killGroup starts c in a session of its own and makes cancelling it signal the whole
// group, so every command of the script stops, not only the shell. started must be
// called once c has started (it does nothing here), done once it has finished.
//
// Its own session also means no controlling terminal, as a captured command should
// have: something that would ask on /dev/tty (an ssh passphrase, an unknown host key)
// fails at once with its own message instead of stopping in the background until
// cancelled.
func killGroup(c *exec.Cmd) (started, done func()) {
	var finished atomic.Bool
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// After a cancel, Wait gives up on the process and its pipes after this long.
	c.WaitDelay = killGrace + time.Second
	c.Cancel = func() error {
		pid := c.Process.Pid
		err := syscall.Kill(-pid, syscall.SIGTERM)
		// A stopped process only gets the SIGTERM once it runs again.
		_ = syscall.Kill(-pid, syscall.SIGCONT)
		// Whatever ignores SIGTERM (a trap, a slow graceful shutdown) is killed.
		time.AfterFunc(killGrace, func() {
			if !finished.Load() {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		})
		if err == syscall.ESRCH {
			return os.ErrProcessDone // the script ended as ctx was cancelled
		}
		return err
	}
	return func() {}, func() { finished.Store(true) }
}
