package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/demo"
	"github.com/MasterPushers/prdeck/internal/github"
)

// client runs a demo server and sends it requests one at a time.
type client struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Scanner
	id  int64
}

func start(t *testing.T) *client {
	t.Helper()
	cfg, err := config.Load(t.TempDir() + "/none.yaml")
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { _ = Serve(demo.New(cfg), Info{Version: "test", Demo: true}, inR, outW); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	return &client{t: t, in: inW, out: sc}
}

type reply struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *respError      `json:"error"`
}

func (c *client) call(method string, params any) reply {
	c.t.Helper()
	c.id++
	b, _ := json.Marshal(map[string]any{"id": c.id, "method": method, "params": params})
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	done := make(chan reply, 1)
	go func() {
		var r reply
		if c.out.Scan() {
			_ = json.Unmarshal(c.out.Bytes(), &r)
		}
		done <- r
	}()
	select {
	case r := <-done:
		if r.ID != c.id {
			c.t.Fatalf("%s: got reply for id %d, want %d", method, r.ID, c.id)
		}
		return r
	case <-time.After(5 * time.Second):
		c.t.Fatalf("%s: no reply", method)
	}
	return reply{}
}

func TestHelloAndSearch(t *testing.T) {
	c := start(t)
	var hello struct {
		Demo     bool
		Filters  []struct{ Name, Query string }
		Commands []struct{ Name, Key string }
	}
	if err := json.Unmarshal(c.call("hello", nil).Result, &hello); err != nil {
		t.Fatal(err)
	}
	if !hello.Demo || len(hello.Filters) == 0 || len(hello.Commands) == 0 {
		t.Fatalf("hello: %+v", hello)
	}
	var res struct{ PRs []github.PR }
	if err := json.Unmarshal(c.call("search", map[string]string{"query": hello.Filters[0].Query}).Result, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.PRs) == 0 {
		t.Fatal("search found no PRs")
	}
	if r := c.call("nope", nil); r.Error == nil || r.Error.Code != "bad_request" {
		t.Errorf("unknown method: %+v", r)
	}
}

func TestPlansAndChanges(t *testing.T) {
	c := start(t)
	var res struct{ PRs []github.PR }
	_ = json.Unmarshal(c.call("search", map[string]string{"query": "involves:@me"}).Result, &res)
	var registry, noClone github.PR
	for _, p := range res.PRs {
		switch {
		case p.Repo == "northwind/registry" && registry.Number == 0:
			registry = p
		case p.Repo == "upstream/datakit":
			noClone = p
		}
	}
	if registry.Number == 0 || noClone.Number == 0 {
		t.Fatal("demo data changed: need a registry PR and a datakit PR")
	}

	// The registry clone has uncommitted changes, so checking out the PR needs a choice.
	r := c.call("prepare", map[string]any{"command": "Checkout", "pr": registry})
	if r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	var plan map[string]any
	_ = json.Unmarshal(r.Result, &plan)
	if ch, _ := plan["changes"].([]any); len(ch) == 0 {
		t.Fatalf("plan should list the uncommitted changes: %v", plan)
	}
	if r := c.call("execute", map[string]any{"plan": plan}); r.Error == nil || r.Error.Code != "dirty" {
		t.Fatalf("executing over changes should be refused: %+v", r)
	}
	r = c.call("execute", map[string]any{"plan": plan, "changes": "stash"})
	if r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	var out struct {
		OK     bool
		Output string
	}
	_ = json.Unmarshal(r.Result, &out)
	if !out.OK || !strings.Contains(out.Output, "Saved working directory") {
		t.Errorf("stash then checkout: %+v", out)
	}

	if r := c.call("prepare", map[string]any{"command": "Fetch", "pr": noClone}); r.Error == nil || r.Error.Code != "no_clone" {
		t.Errorf("a repo without a clone should say so: %+v", r)
	}
}

func TestDetailForDesktop(t *testing.T) {
	d := forDisplay(&github.Detail{
		Body:     "<!-- hidden -->\n> [!NOTE]\n> hi @mlin, see <img width=\"300\" src=\"https://x/y.png\">",
		Timeline: []github.Event{{Body: "a<br>b `@code`"}},
	})
	for _, want := range []string{"> [!NOTE]", "[@mlin](https://github.com/mlin)", `<img width="300"`} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("body should keep %q: %q", want, d.Body)
		}
	}
	if strings.Contains(d.Body, "<!--") {
		t.Errorf("comment left in: %q", d.Body)
	}
	if d.Timeline[0].Body != "a<br>b `@code`" {
		t.Errorf("timeline body changed: %q", d.Timeline[0].Body)
	}
	if d.Labels == nil || d.Checks == nil || d.CommitList == nil || d.Files == nil {
		t.Error("empty lists should be [] in JSON")
	}
}

func TestCommentReviewAndReply(t *testing.T) {
	c := start(t)
	var res struct{ PRs []github.PR }
	_ = json.Unmarshal(c.call("search", map[string]string{"query": "involves:@me"}).Result, &res)
	var mine, theirs github.PR
	for _, p := range res.PRs {
		switch {
		case p.Author == "you" && mine.Number == 0:
			mine = p
		case p.Author != "you" && theirs.Number == 0:
			theirs = p
		}
	}
	detail := func(pr github.PR) github.Detail {
		var d github.Detail
		if err := json.Unmarshal(c.call("detail", map[string]any{"pr": pr}).Result, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := detail(theirs)
	if d.NodeID == "" {
		t.Fatal("detail has no node ID to comment on")
	}
	before := len(d.Timeline)

	if r := c.call("comment", map[string]any{"prId": d.NodeID, "body": "  "}); r.Error == nil {
		t.Error("an empty comment should be refused")
	}
	if r := c.call("comment", map[string]any{"prId": d.NodeID, "body": "Looks good to me"}); r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	if r := c.call("review", map[string]any{"prId": d.NodeID, "event": "REQUEST_CHANGES"}); r.Error == nil {
		t.Error("requesting changes without saying what should be refused")
	}
	if r := c.call("review", map[string]any{"prId": d.NodeID, "event": "APPROVE"}); r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	d = detail(theirs)
	if len(d.Timeline) != before+2 || d.Timeline[before].Body != "Looks good to me" || d.Timeline[before+1].State != "APPROVED" {
		t.Errorf("timeline after comment and approval: %+v", d.Timeline[before:])
	}

	m := detail(mine)
	if r := c.call("review", map[string]any{"prId": m.NodeID, "event": "APPROVE"}); r.Error == nil {
		t.Error("approving your own PR should be refused")
	}
	for _, e := range m.Timeline {
		if e.Kind == "thread" {
			if r := c.call("replyThread", map[string]any{"threadId": e.ThreadID, "body": "Fixed"}); r.Error != nil {
				t.Fatal(r.Error.Message)
			}
			for _, e2 := range detail(mine).Timeline {
				if e2.ThreadID == e.ThreadID && (len(e2.Replies) == 0 || e2.Replies[len(e2.Replies)-1].Body != "Fixed") {
					t.Errorf("reply missing: %+v", e2.Replies)
				}
			}
			return
		}
	}
	t.Error("demo data changed: need a review thread on one of your PRs")
}

func TestDiffs(t *testing.T) {
	c := start(t)
	var res struct{ PRs []github.PR }
	_ = json.Unmarshal(c.call("search", map[string]string{"query": "involves:@me"}).Result, &res)
	var pr github.PR
	for _, p := range res.PRs {
		if p.Number == 4821 {
			pr = p
		}
	}
	var files []github.FileDiff
	if err := json.Unmarshal(c.call("prDiff", map[string]any{"pr": pr}).Result, &files); err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 || !strings.Contains(files[1].Patch, "class StreamPushSource") || files[1].Additions == 0 {
		t.Fatalf("PR diff: %+v", files)
	}
	var d github.Detail
	_ = json.Unmarshal(c.call("detail", map[string]any{"pr": pr}).Result, &d)
	var commit github.CommitDiff
	r := c.call("commitDiff", map[string]any{"repo": pr.Repo, "sha": d.CommitList[4].OID})
	if r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	_ = json.Unmarshal(r.Result, &commit)
	if len(commit.Files) != 1 || !strings.Contains(commit.Files[0].Patch, "+    def push(self, name, df, *, allow_registry_cache=False):") {
		t.Errorf("commit diff: %+v", commit)
	}
	if r := c.call("commitDiff", map[string]any{"repo": pr.Repo, "sha": "nope"}); r.Error == nil {
		t.Error("an unknown commit should be an error")
	}
}

func TestReviewWithLineComments(t *testing.T) {
	c := start(t)
	var res struct{ PRs []github.PR }
	_ = json.Unmarshal(c.call("search", map[string]string{"query": "involves:@me"}).Result, &res)
	var pr github.PR
	for _, p := range res.PRs {
		if p.Number == 4807 {
			pr = p
		}
	}
	var d github.Detail
	_ = json.Unmarshal(c.call("detail", map[string]any{"pr": pr}).Result, &d)
	var files []github.FileDiff
	_ = json.Unmarshal(c.call("prDiff", map[string]any{"pr": pr}).Result, &files)
	path := files[0].Path

	bad := []map[string]any{{"path": path, "line": 0, "side": "RIGHT", "body": "x"}}
	if r := c.call("review", map[string]any{"prId": d.NodeID, "event": "COMMENT", "threads": bad}); r.Error == nil {
		t.Error("a comment on line 0 should be refused")
	}
	threads := []map[string]any{
		{"path": path, "line": 14, "side": "RIGHT", "body": "Why load this twice?"},
		{"path": path, "line": 13, "side": "LEFT", "body": "This was the only caller."},
	}
	if r := c.call("review", map[string]any{"prId": d.NodeID, "event": "COMMENT", "threads": threads}); r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	_ = json.Unmarshal(c.call("detail", map[string]any{"pr": pr}).Result, &d)
	var got []github.Event
	for _, e := range d.Timeline {
		if e.Kind == "thread" && e.Author == "you" {
			got = append(got, e)
		}
	}
	if len(got) != 2 || got[0].Path != path || got[0].Line != 14 || got[0].Side != "RIGHT" || got[1].Side != "LEFT" || got[0].ThreadID == "" {
		t.Fatalf("threads: %+v", got)
	}
	if !strings.HasPrefix(got[0].DiffHunk, "@@") {
		t.Errorf("thread should carry its diff context: %q", got[0].DiffHunk)
	}
}

// panicky is the demo backend with a search that panics.
type panicky struct{ *demo.Backend }

func (panicky) Search(context.Context, string) ([]github.PR, error) { panic("boom") }

func TestAPanicFailsOnlyItsRequest(t *testing.T) {
	cfg, err := config.Load(t.TempDir() + "/none.yaml")
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { _ = Serve(panicky{demo.New(cfg)}, Info{Version: "test", Demo: true}, inR, outW); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	c := &client{t: t, in: inW, out: bufio.NewScanner(outR)}
	if r := c.call("search", map[string]any{"query": "involves:@me"}); r.Error == nil || !strings.Contains(r.Error.Message, "boom") {
		t.Fatalf("search: %+v", r.Error)
	}
	if r := c.call("hello", nil); r.Error != nil {
		t.Fatal(r.Error.Message)
	}

	// The demo's stash used to read a clone that a repo without one lacks.
	c = start(t)
	plan := map[string]any{"title": "Check out", "pr": map[string]any{"repo": "upstream/datakit", "number": 1}}
	if r := c.call("execute", map[string]any{"plan": plan, "changes": "stash"}); r.Error != nil {
		t.Fatal(r.Error.Message)
	}
}

func TestInputClosingStopsAWaitingSignIn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in gh is a shell script")
	}
	// A gh that shows a code, then waits for an approval that never comes.
	bin := t.TempDir()
	gh := "#!/bin/sh\necho 'First copy your one-time code: ABCD-1234'\necho 'Open https://github.com/login/device'\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg, err := config.Load(t.TempDir() + "/none.yaml")
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan struct{})
	go func() { _ = Serve(demo.New(cfg), Info{Version: "test"}, inR, outW); close(served) }()
	sc := bufio.NewScanner(outR)
	c := &client{t: t, in: inW, out: sc}
	if r := c.call("loginStart", nil); r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	if _, err := io.WriteString(inW, `{"id": 9, "method": "loginWait"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let loginWait start waiting
	inW.Close()
	go func() {
		for sc.Scan() { // loginWait's answer
		}
	}()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("prdeck serve kept running after its input closed, waiting on gh")
	}
}

func TestReviewOnSeveralLines(t *testing.T) {
	c := start(t)
	var res struct{ PRs []github.PR }
	_ = json.Unmarshal(c.call("search", map[string]string{"query": "involves:@me"}).Result, &res)
	var pr github.PR
	for _, p := range res.PRs {
		if p.Number == 4807 {
			pr = p
		}
	}
	var d github.Detail
	_ = json.Unmarshal(c.call("detail", map[string]any{"pr": pr}).Result, &d)
	var files []github.FileDiff
	_ = json.Unmarshal(c.call("prDiff", map[string]any{"pr": pr}).Result, &files)
	backwards := []map[string]any{{"path": files[0].Path, "line": 13, "side": "RIGHT", "startLine": 15, "startSide": "RIGHT", "body": "x"}}
	if r := c.call("review", map[string]any{"prId": d.NodeID, "event": "COMMENT", "threads": backwards}); r.Error == nil {
		t.Error("a range that ends before it starts should be refused")
	}
	ranged := []map[string]any{{"path": files[0].Path, "line": 15, "side": "RIGHT", "startLine": 13, "startSide": "LEFT", "body": "These go together"}}
	if r := c.call("review", map[string]any{"prId": d.NodeID, "event": "COMMENT", "threads": ranged}); r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	_ = json.Unmarshal(c.call("detail", map[string]any{"pr": pr}).Result, &d)
	last := d.Timeline[len(d.Timeline)-1]
	if last.Kind != "thread" || last.StartLine != 13 || last.StartSide != "LEFT" || last.Line != 15 {
		t.Errorf("ranged thread: %+v", last)
	}
}
