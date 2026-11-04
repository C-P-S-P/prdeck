//go:build unix

package runner

import (
	"context"
	"strings"
	"testing"
	"time"
)

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
