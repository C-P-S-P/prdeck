package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// Shell is the POSIX shell every script runs in: /bin/sh, or on Windows the sh.exe of
// Git for Windows. Commands in the config are POSIX shell with POSIX quoting (Quote), so
// cmd.exe or PowerShell would not run them; git is needed on Windows anyway.
var Shell = sync.OnceValues(func() (string, error) {
	return findShell(runtime.GOOS, exec.LookPath, os.Getenv, isFile)
})

// ErrNoShell means no sh was found on Windows.
var ErrNoShell = errors.New("PR Deck runs commands with sh from Git for Windows, and it was not found; install Git for Windows from https://gitforwindows.org and start PR Deck again")

func findShell(goos string, lookPath func(string) (string, error), getenv func(string) string, isFile func(string) bool) (string, error) {
	if goos != "windows" {
		return "/bin/sh", nil
	}
	if p, err := lookPath("sh"); err == nil {
		return p, nil
	}
	// Usually only Git\cmd is on PATH. git.exe is in Git\cmd, Git\bin or
	// Git\mingw64\bin; sh.exe is in Git\bin (it sets up PATH) or Git\usr\bin.
	var roots []string
	if git, err := lookPath("git"); err == nil {
		for d, i := filepath.Dir(git), 0; i < 3; d, i = filepath.Dir(d), i+1 {
			roots = append(roots, d)
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if v := getenv(env); v != "" {
			roots = append(roots, filepath.Join(v, "Git"))
		}
	}
	if v := getenv("LOCALAPPDATA"); v != "" {
		roots = append(roots, filepath.Join(v, "Programs", "Git")) // installed for one user
	}
	for _, r := range roots {
		for _, sh := range []string{filepath.Join(r, "bin", "sh.exe"), filepath.Join(r, "usr", "bin", "sh.exe")} {
			if isFile(sh) {
				return sh, nil
			}
		}
	}
	return "", ErrNoShell
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
