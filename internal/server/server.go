// Package server lets other front-ends use prdeck's core over stdin and stdout. The
// desktop app starts `prdeck serve` and talks to it with one JSON object per line:
//
//	→ {"id": 1, "method": "search", "params": {"query": "involves:@me"}}
//	← {"id": 1, "result": {"prs": [...], "viewer": "mlin"}}
//	← {"id": 2, "error": {"code": "no_clone", "message": "no local clone of org/repo"}}
//
// Requests run concurrently, so a slow GitHub call never holds up a quick one;
// responses can come back in any order and are matched by id.
package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/MasterPushers/prdeck/internal/cache"
	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/core"
	"github.com/MasterPushers/prdeck/internal/github"
	"github.com/MasterPushers/prdeck/internal/mdrender"
	"github.com/MasterPushers/prdeck/internal/repos"
)

// Backend is what the server needs from the core. core.Service and the demo backend
// both provide it.
type Backend interface {
	Config() *config.Config
	Viewer() string
	CachedSearch(query string) (cache.SearchEntry, bool)
	Search(ctx context.Context, query string) ([]github.PR, error)
	CachedDetail(pr github.PR) (*github.Detail, bool)
	Detail(ctx context.Context, pr github.PR) (*github.Detail, error)
	ResolveFolder(pr github.PR, wantWorktree bool) (core.Folder, error)
	Prepare(c config.Command, pr github.PR, input string) (core.Plan, error)
	ClonePlan(pr github.PR) core.Plan
	Execute(ctx context.Context, p core.Plan) core.Result
	FindClones(ctx context.Context, repo string) []string
	SetClonePath(repo, path string) (string, error)
	ClonePath(repo string) string
	ListClones(ctx context.Context) []repos.DiskClone
}

// writer is a backend that can comment on and review PRs.
type writer interface {
	Comment(ctx context.Context, prID, body string) error
	Review(ctx context.Context, prID, event, body string, threads []github.DraftThread) error
	ReplyThread(ctx context.Context, threadID, body string) error
}

// differ is a backend that can load diffs.
type differ interface {
	CommitDiff(ctx context.Context, repo, sha string) (*github.CommitDiff, error)
	PRDiff(ctx context.Context, pr github.PR) ([]github.FileDiff, error)
}

// imageFetcher is a backend that can download images with the user's GitHub login.
type imageFetcher interface {
	FetchImage(ctx context.Context, url string) ([]byte, string, error)
}

// Info describes the process to the client.
type Info struct {
	Version    string
	ConfigPath string
	Demo       bool
	// DemoSignedOut starts demo mode signed out, to try the sign-in screen.
	DemoSignedOut bool
}

type request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	ID     int64      `json:"id"`
	Result any        `json:"result,omitempty"`
	Error  *respError `json:"error,omitempty"`
}

type respError struct {
	Code    string `json:"code"` // no_clone, needs_terminal, bad_request, error
	Message string `json:"message"`
}

// codedError carries a code the client can act on.
type codedError struct {
	code string
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }

// Serve answers requests from in until it is closed.
func Serve(b Backend, info Info, in io.Reader, out io.Writer) error {
	s := &server{b: b, info: info, enc: json.NewEncoder(out), demoSignedIn: !info.DemoSignedOut}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 32<<20)
	var wg sync.WaitGroup
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.send(response{Error: &respError{Code: "bad_request", Message: err.Error()}})
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handle(req)
		}()
	}
	wg.Wait()
	s.close()
	return sc.Err()
}

type server struct {
	b    Backend
	info Info
	mu   sync.Mutex // guards enc and login
	enc  *json.Encoder

	login        *login
	demoSignedIn bool
	demoLoginAt  time.Time
}

func (s *server) send(r response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.enc.Encode(r)
}

// Close stops a sign-in still waiting when the app quits.
func (s *server) close() { s.loginCancel() }

func (s *server) handle(req request) {
	result, err := s.call(req.Method, req.Params)
	if err == nil {
		s.send(response{ID: req.ID, Result: result})
		return
	}
	code := "error"
	var ce codedError
	switch {
	case errors.As(err, &ce):
		code = ce.code
	case errors.Is(err, core.ErrNoClone):
		code = "no_clone"
	}
	s.send(response{ID: req.ID, Error: &respError{Code: code, Message: err.Error()}})
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, codedError{"bad_request", err}
	}
	return v, nil
}

func (s *server) call(method string, raw json.RawMessage) (any, error) {
	switch method {
	case "hello":
		return s.hello(), nil

	case "cachedSearch":
		p, err := decode[struct{ Query string }](raw)
		if err != nil {
			return nil, err
		}
		e, ok := s.b.CachedSearch(p.Query)
		return map[string]any{"found": ok, "at": e.At, "prs": nonNil(e.PRs), "viewer": s.b.Viewer()}, nil

	case "search":
		p, err := decode[struct{ Query string }](raw)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		prs, err := s.b.Search(ctx, p.Query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"prs": nonNil(prs), "viewer": s.b.Viewer(), "at": time.Now()}, nil

	case "cachedDetail":
		p, err := decode[struct{ PR github.PR }](raw)
		if err != nil {
			return nil, err
		}
		d, fresh := s.b.CachedDetail(p.PR)
		if d != nil && d.Commits > 0 && len(d.CommitList) == 0 {
			fresh = false // saved before commits were fetched
		}
		return map[string]any{"detail": forDisplay(d), "fresh": fresh}, nil

	case "detail":
		p, err := decode[struct{ PR github.PR }](raw)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		d, err := s.b.Detail(ctx, p.PR)
		if err != nil {
			return nil, err
		}
		return forDisplay(d), nil

	case "folder":
		p, err := decode[struct {
			PR       github.PR
			Worktree bool
		}](raw)
		if err != nil {
			return nil, err
		}
		return s.b.ResolveFolder(p.PR, p.Worktree)

	case "prepare":
		p, err := decode[struct {
			Command string
			PR      github.PR
			Input   string
		}](raw)
		if err != nil {
			return nil, err
		}
		c, ok := s.command(p.Command)
		if !ok {
			return nil, fmt.Errorf("no command named %q", p.Command)
		}
		return s.b.Prepare(c, p.PR, p.Input)

	case "clonePlan":
		p, err := decode[struct{ PR github.PR }](raw)
		if err != nil {
			return nil, err
		}
		return s.b.ClonePlan(p.PR), nil

	case "execute":
		p, err := decode[struct {
			Plan    core.Plan
			Changes string // "stash" or "drop" the uncommitted changes first
		}](raw)
		if err != nil {
			return nil, err
		}
		return s.execute(p.Plan, p.Changes)

	case "image":
		p, err := decode[struct{ URL string }](raw)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		var data []byte
		var kind string
		if f, ok := s.b.(imageFetcher); ok {
			data, kind, err = f.FetchImage(ctx, p.URL)
		} else {
			data, kind, err = github.FetchPublicImage(ctx, p.URL)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"data": base64.StdEncoding.EncodeToString(data), "type": kind}, nil

	case "comment", "review", "replyThread":
		p, err := decode[struct {
			PRID, ThreadID, Event, Body string
			Threads                     []github.DraftThread
		}](raw)
		if err != nil {
			return nil, err
		}
		w, ok := s.b.(writer)
		if !ok {
			return nil, errors.New("this backend cannot write to GitHub")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		switch method {
		case "comment":
			err = w.Comment(ctx, p.PRID, p.Body)
		case "review":
			err = w.Review(ctx, p.PRID, p.Event, p.Body, p.Threads)
		default:
			err = w.ReplyThread(ctx, p.ThreadID, p.Body)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "commitDiff", "prDiff":
		p, err := decode[struct {
			Repo, SHA string
			PR        github.PR
		}](raw)
		if err != nil {
			return nil, err
		}
		d, ok := s.b.(differ)
		if !ok {
			return nil, errors.New("this backend has no diffs")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if method == "commitDiff" {
			c, err := d.CommitDiff(ctx, p.Repo, p.SHA)
			if err != nil {
				return nil, err
			}
			c.Files = nonNil(c.Files)
			return c, nil
		}
		files, err := d.PRDiff(ctx, p.PR)
		if err != nil {
			return nil, err
		}
		return nonNil(files), nil

	case "setupStatus":
		return s.setupStatus()

	case "loginStart":
		return s.loginStart()

	case "loginWait":
		return s.loginWait()

	case "loginCancel":
		s.loginCancel()
		return map[string]any{"ok": true}, nil

	case "installGh":
		return s.installGh()

	case "saveSetup":
		p, err := decode[config.Setup](raw)
		if err != nil {
			return nil, err
		}
		return s.saveSetup(p)

	case "openConfig":
		return s.openConfig()

	case "findClones":
		p, err := decode[struct{ Repo string }](raw)
		if err != nil {
			return nil, err
		}
		return nonNil(s.b.FindClones(context.Background(), p.Repo)), nil

	case "listClones":
		return nonNil(s.b.ListClones(context.Background())), nil

	case "setClonePath":
		p, err := decode[struct{ Repo, Path string }](raw)
		if err != nil {
			return nil, err
		}
		note, err := s.b.SetClonePath(p.Repo, p.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"note": note, "path": s.b.ClonePath(p.Repo)}, nil

	case "clonePath":
		p, err := decode[struct{ Repo string }](raw)
		if err != nil {
			return nil, err
		}
		return s.b.ClonePath(p.Repo), nil
	}
	return nil, codedError{"bad_request", fmt.Errorf("unknown method %q", method)}
}

func (s *server) command(name string) (config.Command, bool) {
	for _, c := range s.b.Config().Commands {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return config.Command{}, false
}

func (s *server) execute(p core.Plan, changes string) (any, error) {
	switch changes {
	case "stash":
		p = p.WithStash()
	case "drop":
		p = p.WithDrop()
	case "":
		if len(p.Changes) > 0 {
			return nil, codedError{"dirty", fmt.Errorf("%s has uncommitted changes; stash or drop them first", config.ShortenHome(p.Folder.Path))}
		}
	default:
		return nil, codedError{"bad_request", fmt.Errorf("changes must be stash or drop, not %q", changes)}
	}
	if p.Terminal {
		// The desktop app has no terminal to hand over. Open a terminal editor in a
		// terminal window (Terminal.app on the Mac); refuse other terminal commands with
		// a clear reason.
		if p.Action != "open" {
			return nil, codedError{"needs_terminal", fmt.Errorf("%s runs in a terminal (output: terminal); run it from prdeck in a terminal", p.Title)}
		}
		script, err := inTerminal(p.Script, p.Dir())
		if err != nil {
			return nil, codedError{"needs_terminal", fmt.Errorf("%s opens in a terminal, but %w; run it from prdeck in a terminal", p.Title, err)}
		}
		p.Script = script
		p.Terminal = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	r := s.b.Execute(ctx, p)
	out := map[string]any{"output": r.Output, "ok": r.Err == nil, "title": r.Plan.Title}
	if r.Err != nil {
		out["error"] = r.Err.Error()
	}
	return out, nil
}

// terminalApp wraps a command so it opens in a new Terminal.app window.
func terminalApp(script, dir string) string {
	inner := "cd " + shellQuote(dir) + " && " + script
	osa := `tell application "Terminal" to do script ` + appleQuote(inner)
	return "osascript -e " + shellQuote(osa) + " -e " + shellQuote(`tell application "Terminal" to activate`)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (s *server) hello() any {
	cfg := s.b.Config()
	type filter struct {
		Name  string `json:"name"`
		Query string `json:"query"`
	}
	type command struct {
		Name     string   `json:"name"`
		Key      string   `json:"key"`
		Run      string   `json:"run"`
		Action   string   `json:"action"`
		Bar      bool     `json:"bar"`
		Confirm  bool     `json:"confirm"`
		Prompt   string   `json:"prompt"`
		Output   string   `json:"output"`
		When     []string `json:"when"`
		Branch   bool     `json:"branch"`
		Clean    bool     `json:"clean"`
		Worktree bool     `json:"worktree"`
	}
	var fs []filter
	for _, f := range cfg.Filters {
		fs = append(fs, filter{f.Name, f.Query})
	}
	var cs []command
	for _, c := range cfg.Commands {
		cs = append(cs, command{c.Name, c.Key, c.Run, c.Action, c.OnBar(), c.Confirm, c.Prompt, c.Output, nonNil([]string(c.When)), c.Branch, c.Clean, c.Worktree})
	}
	var editors []string
	for name := range cfg.Editor.Editors {
		editors = append(editors, name)
	}
	return map[string]any{
		"version":    s.info.Version,
		"demo":       s.info.Demo,
		"configPath": s.info.ConfigPath,
		"viewer":     s.b.Viewer(),
		"warnings":   nonNil(cfg.Warnings),
		"refresh":    time.Duration(cfg.GitHub.Refresh).Seconds(),
		"editor":     cfg.Editor.Default,
		"editors":    nonNil(editors),
		"filters":    nonNil(fs),
		"commands":   nonNil(cs),
	}
}

// forDisplay returns a copy of d ready for the desktop app's renderer: HTML comments
// dropped and @mentions linked; HTML, images and alerts are left for the renderer.
func forDisplay(d *github.Detail) *github.Detail {
	if d == nil {
		return nil
	}
	c := *d
	c.Body = mdrender.GitHubForDesktop(d.Body)
	c.Timeline = make([]github.Event, len(d.Timeline))
	for i, e := range d.Timeline {
		e.Body = mdrender.GitHubForDesktop(e.Body)
		replies := make([]github.Reply, len(e.Replies))
		for j, r := range e.Replies {
			r.Body = mdrender.GitHubForDesktop(r.Body)
			replies[j] = r
		}
		e.Replies = replies
		c.Timeline[i] = e
	}
	c.Labels = nonNil(c.Labels)
	c.Reviewers = nonNil(c.Reviewers)
	c.Checks = nonNil(c.Checks)
	c.CommitList = nonNil(c.CommitList)
	c.Files = nonNil(c.Files)
	return &c
}

// nonNil keeps empty lists as [] in JSON rather than null.
func nonNil[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return l
}
