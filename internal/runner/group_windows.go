//go:build windows

package runner

import (
	"os/exec"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

// killGroup puts c in a job object once it has started and makes cancelling end the
// whole job, so every command of the script stops, not only the shell. Windows has no
// process groups, and taskkill /T misses commands: Git's sh starts a command through a
// process that exits once the command runs, so the command's Windows parent is gone.
// Whatever a process in a job starts is in the job too. started must be called once c
// has started, done once it has finished.
func killGroup(c *exec.Cmd) (started, done func()) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		c.Cancel = func() error { return killTree(c) }
		return func() {}, func() {}
	}
	var inJob atomic.Bool
	// After a cancel, Wait gives up on the process and its pipes after this long.
	c.WaitDelay = 2 * time.Second
	c.Cancel = func() error {
		if inJob.Load() && windows.TerminateJobObject(job, 1) == nil {
			return nil
		}
		return killTree(c)
	}
	started = func() {
		// ponytail: joined just after it starts, not started suspended (exec cannot
		// resume it); sh takes far longer to start its first command than this does.
		h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid))
		if err != nil {
			return
		}
		defer windows.CloseHandle(h)
		inJob.Store(windows.AssignProcessToJobObject(job, h) == nil)
	}
	return started, func() { windows.CloseHandle(job) }
}

// killTree stops a script and the commands taskkill can find under it, for when it
// could not be put in a job.
func killTree(c *exec.Cmd) error {
	if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid)).Run() == nil {
		return nil
	}
	return c.Process.Kill()
}
