package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patelchaitany/prdeck/internal/config"
)

// First-run setup for the desktop app: sign in to GitHub (through gh, so prdeck never
// holds a token), pick the folders that hold clones, and pick an editor.

// account is a backend that knows about the GitHub login and can reload its config.
// core.Service is one; the demo backend is not, so setup is simulated in demo mode.
type account interface {
	Connect() error
	Login(ctx context.Context) (string, error)
	ConfigPath() string
	Reload() error
}

type editorChoice struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Installed bool   `json:"installed"`
	Run       string `json:"run"` // set when the config needs its own run line for it
}

type folderChoice struct {
	Path   string `json:"path"`
	Clones int    `json:"clones"`
	Saved  bool   `json:"saved"` // already in repos.scan
}

type setupStatus struct {
	GhInstalled  bool           `json:"ghInstalled"`
	GhVersion    string         `json:"ghVersion"`
	BrewFound    bool           `json:"brewFound"`
	SignedIn     bool           `json:"signedIn"`
	Login        string         `json:"login"`
	ConfigExists bool           `json:"configExists"`
	ConfigPath   string         `json:"configPath"`
	Folders      []folderChoice `json:"folders"`
	Editors      []editorChoice `json:"editors"`
	Editor       string         `json:"editor"`
	Worktree     bool           `json:"worktree"`
}

// editors PR Deck knows how to open: config name, label, command-line tool, app.
var knownEditors = []struct{ name, label, cli, app string }{
	{"zed", "Zed", "zed", "Zed.app"},
	{"cursor", "Cursor", "cursor", "Cursor.app"},
	{"vscode", "Visual Studio Code", "code", "Visual Studio Code.app"},
	{"nvim", "Neovim (opens in Terminal)", "nvim", ""},
}

func (s *server) setupStatus() (any, error) {
	cfg := s.b.Config()
	st := setupStatus{ConfigPath: s.info.ConfigPath, Editor: cfg.Editor.Default, Worktree: cfg.Editor.UseWorktree()}
	if _, err := os.Stat(s.info.ConfigPath); err == nil {
		st.ConfigExists = true
	}
	if gh, err := exec.LookPath("gh"); err == nil {
		st.GhInstalled = true
		if out, err := exec.Command(gh, "--version").Output(); err == nil {
			st.GhVersion = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
	}
	_, err := exec.LookPath("brew")
	st.BrewFound = err == nil

	if s.info.Demo {
		st.GhInstalled, st.SignedIn, st.Login = true, s.demoSignedIn, "you"
	} else if a, ok := s.b.(account); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if login, err := a.Login(ctx); err == nil {
			st.SignedIn, st.Login = true, login
		}
	}

	st.Folders = s.folders()
	for _, e := range knownEditors {
		c := editorChoice{Name: e.name, Label: e.label}
		if _, err := exec.LookPath(e.cli); err == nil {
			c.Installed = true
		} else if e.app != "" && appInstalled(e.app) {
			// The app is there without its command-line tool: open it with `open -a`.
			c.Installed = true
			c.Run = `open -a "` + strings.TrimSuffix(e.app, ".app") + `" {path}`
		}
		st.Editors = append(st.Editors, c)
	}
	return st, nil
}

func appInstalled(app string) bool {
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
		if _, err := os.Stat(filepath.Join(dir, app)); err == nil {
			return true
		}
	}
	return false
}

// folders groups the clones found on disk by the folder they sit in, most clones
// first, plus the folders already in repos.scan.
func (s *server) folders() []folderChoice {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	counts := map[string]int{}
	for _, c := range s.b.ListClones(ctx) {
		counts[filepath.Dir(c.Path)]++
	}
	saved := map[string]bool{}
	for _, d := range s.b.Config().Repos.Scan {
		p := filepath.Clean(config.ExpandHome(d))
		saved[p] = true
		if _, ok := counts[p]; !ok {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				counts[p] = 0
			}
		}
	}
	home, _ := os.UserHomeDir()
	var out []folderChoice
	for p, n := range counts {
		if p == home && !saved[p] {
			continue // clones straight in the home folder: suggest a set path instead
		}
		out = append(out, folderChoice{Path: config.ShortenHome(p), Clones: n, Saved: saved[p]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Clones != out[j].Clones {
			return out[i].Clones > out[j].Clones
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// login is a running `gh auth login`.
type login struct {
	cmd  *exec.Cmd
	done chan error
	out  strings.Builder
	mu   sync.Mutex
}

var deviceCode = regexp.MustCompile(`\b([A-Z0-9]{4}-[A-Z0-9]{4})\b`)
var deviceURL = regexp.MustCompile(`https://\S+/login/device\S*`)

// loginStart runs gh's browser sign-in and returns the one-time code to enter on
// GitHub. gh keeps running until the user approves; loginWait waits for that.
func (s *server) loginStart() (any, error) {
	s.loginCancel()
	if s.info.Demo {
		s.demoLoginAt = time.Now()
		return map[string]string{"code": "DEMO-1234", "url": "https://github.com/login/device"}, nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return nil, codedError{"no_gh", errors.New("the GitHub CLI (gh) is not installed")}
	}
	cmd := exec.Command(gh, "auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--web", "--skip-ssh-key")
	cmd.Env = append(os.Environ(), "BROWSER=true") // the app opens the page itself
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	l := &login{cmd: cmd, done: make(chan error, 1)}
	go func() {
		err := cmd.Wait()
		pw.Close()
		stdin.Close()
		l.done <- err
	}()
	found := make(chan [2]string, 1)
	go func() {
		var code, url string
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line := sc.Text()
			l.mu.Lock()
			l.out.WriteString(line + "\n")
			l.mu.Unlock()
			if m := deviceCode.FindString(line); m != "" && code == "" {
				code = m
			}
			if m := deviceURL.FindString(line); m != "" {
				url = m
			}
			if strings.Contains(strings.ToLower(line), "press enter") {
				io.WriteString(stdin, "\n")
			}
			if code != "" && url != "" {
				select {
				case found <- [2]string{code, url}:
				default:
				}
			}
		}
	}()
	s.mu.Lock()
	s.login = l
	s.mu.Unlock()
	select {
	case cu := <-found:
		return map[string]string{"code": cu[0], "url": cu[1]}, nil
	case err := <-l.done:
		l.done <- err
		return nil, fmt.Errorf("gh auth login stopped: %s", l.output())
	case <-time.After(20 * time.Second):
		s.loginCancel()
		return nil, errors.New("gh auth login did not show a code")
	}
}

func (l *login) output() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimSpace(l.out.String())
}

// loginWait blocks until the user approves the code on GitHub (or gives up), then
// picks up the new login.
func (s *server) loginWait() (any, error) {
	if s.info.Demo {
		time.Sleep(time.Until(s.demoLoginAt.Add(6 * time.Second)))
		s.demoSignedIn = true
		return map[string]any{"ok": true, "login": "you"}, nil
	}
	s.mu.Lock()
	l := s.login
	s.mu.Unlock()
	if l == nil {
		return nil, errors.New("no sign-in in progress")
	}
	select {
	case err := <-l.done:
		l.done <- err
		if err != nil {
			return nil, fmt.Errorf("signing in failed: %s", l.output())
		}
	case <-time.After(15 * time.Minute):
		s.loginCancel()
		return nil, errors.New("the code expired; start again")
	}
	a, ok := s.b.(account)
	if !ok {
		return map[string]any{"ok": true}, nil
	}
	if err := a.Connect(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	who, _ := a.Login(ctx)
	return map[string]any{"ok": true, "login": who}, nil
}

func (s *server) loginCancel() {
	s.mu.Lock()
	l := s.login
	s.login = nil
	s.mu.Unlock()
	if l != nil && l.cmd.Process != nil {
		_ = l.cmd.Process.Kill()
	}
}

// installGh installs the GitHub CLI with Homebrew.
func (s *server) installGh() (any, error) {
	if s.info.Demo {
		return map[string]any{"ok": true, "output": "(demo) brew install gh"}, nil
	}
	brew, err := exec.LookPath("brew")
	if err != nil {
		return nil, codedError{"no_brew", errors.New("Homebrew is not installed; get gh from https://cli.github.com")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, brew, "install", "gh").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("brew install gh: %s", lastLines(string(out), 6))
	}
	return map[string]any{"ok": true, "output": lastLines(string(out), 6)}, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// saveSetup writes the choices to the config file and reloads it.
func (s *server) saveSetup(p config.Setup) (any, error) {
	if s.info.Demo {
		return map[string]any{"ok": true}, nil
	}
	if err := config.SaveSetup(s.info.ConfigPath, p); err != nil {
		return nil, err
	}
	if a, ok := s.b.(account); ok {
		if err := a.Reload(); err != nil {
			return nil, err
		}
	}
	return map[string]any{"ok": true}, nil
}

// openConfig opens the config file in the configured editor (creating a short one
// first), or in the default text editor.
func (s *server) openConfig() (any, error) {
	path := s.info.ConfigPath
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(config.Example), 0o644); err != nil {
			return nil, err
		}
	}
	cfg := s.b.Config()
	run := `open -t {path}`
	if ed, ok := cfg.Editor.Editors[cfg.Editor.Default]; ok && !ed.Terminal {
		run = ed.Run
	}
	script := strings.ReplaceAll(run, "{path}", shellQuote(path))
	cmd := exec.Command("/bin/sh", "-c", script)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	return map[string]any{"ok": true, "path": path}, nil
}
