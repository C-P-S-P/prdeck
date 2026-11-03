package server

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/C-P-S-P/prdeck/internal/config"
	"github.com/C-P-S-P/prdeck/internal/demo"
	"github.com/C-P-S-P/prdeck/internal/github"
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

func TestChecksAndRerun(t *testing.T) {
	c := start(t)
	var res struct{ PRs []github.PR }
	_ = json.Unmarshal(c.call("search", map[string]string{"query": "involves:@me"}).Result, &res)
	var failed, green github.PR
	for _, p := range res.PRs {
		switch {
		case p.CIFailed() && failed.Number == 0:
			failed = p
		case p.CheckState == "SUCCESS" && green.Number == 0:
			green = p
		}
	}
	if failed.Number == 0 || green.Number == 0 {
		t.Fatal("demo data changed: need a PR whose CI failed and one whose CI passed")
	}

	// Each check carries its log link; Actions jobs also carry their run.
	var d struct {
		Checks []struct {
			Name, State, URL string
			RunID            int64
		}
	}
	_ = json.Unmarshal(c.call("detail", map[string]any{"pr": failed}).Result, &d)
	var runs, links int
	for _, ch := range d.Checks {
		if ch.RunID > 0 {
			runs++
		}
		if strings.HasPrefix(ch.URL, "https://") {
			links++
		}
	}
	if links != len(d.Checks) || runs == 0 || runs == len(d.Checks) {
		t.Errorf("checks should link to their logs, some with a run and some without: %+v", d.Checks)
	}

	if r := c.call("prepare", map[string]any{"command": "Re-run failed", "pr": green}); r.Error == nil {
		t.Error("re-running a PR whose CI passed should be refused")
	}
	r := c.call("prepare", map[string]any{"command": "Re-run failed", "pr": failed})
	if r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	var plan map[string]any
	_ = json.Unmarshal(r.Result, &plan)
	if plan["action"] != "rerun" || !strings.HasPrefix(plan["script"].(string), "gh run rerun ") {
		t.Fatalf("plan: %v", plan)
	}
	r = c.call("execute", map[string]any{"plan": plan})
	if r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	var out struct {
		OK     bool
		Output string
	}
	_ = json.Unmarshal(r.Result, &out)
	if !out.OK || !strings.Contains(out.Output, "Re-running the failed jobs of run ") || !strings.Contains(out.Output, "Skipped codecov/patch") {
		t.Errorf("re-run: %+v", out)
	}
}

func TestReviewCommandsAndMergeWhenReady(t *testing.T) {
	c := start(t)
	var res struct{ PRs []github.PR }
	_ = json.Unmarshal(c.call("search", map[string]string{"query": "involves:@me"}).Result, &res)
	prs := map[int]github.PR{}
	for _, p := range res.PRs {
		prs[p.Number] = p
	}
	run := func(command string, pr github.PR, input string) (out string, err string) {
		t.Helper()
		r := c.call("prepare", map[string]any{"command": command, "pr": pr, "input": input})
		if r.Error != nil {
			return "", r.Error.Message
		}
		var plan map[string]any
		_ = json.Unmarshal(r.Result, &plan)
		r = c.call("execute", map[string]any{"plan": plan})
		var done struct {
			OK            bool
			Output, Error string
		}
		_ = json.Unmarshal(r.Result, &done)
		return done.Output, done.Error
	}
	detail := func(pr github.PR) github.Detail {
		var d github.Detail
		_ = json.Unmarshal(c.call("detail", map[string]any{"pr": pr}).Result, &d)
		return d
	}
	deprecate, reviewMe, ready, flight := prs[2398], prs[4807], prs[312], prs[2410]
	if deprecate.Author == "you" || reviewMe.Author == "you" || flight.Repo != "upstream/datakit" {
		t.Fatal("demo data changed: need #2398 and #4807 by others, #312 ready, #2410 in upstream/datakit")
	}

	// upstream/datakit has no clone: GitHub writes need none.
	if out, err := run("Approve", deprecate, ""); err != "" || !strings.Contains(out, "Approved upstream/datakit#2398") {
		t.Fatalf("approve: %q %q", out, err)
	}
	if d := detail(deprecate); d.Timeline[len(d.Timeline)-1].State != "APPROVED" {
		t.Errorf("approval not in the conversation: %+v", d.Timeline)
	}
	if _, err := run("Request changes", flight, "x"); !strings.Contains(err, "other people's PRs") {
		t.Errorf("request changes on your own PR: %q", err)
	}
	if out, err := run("Comment", flight, "Rebased; ptal"); err != "" || !strings.Contains(out, "Commented on upstream/datakit#2410") {
		t.Errorf("comment: %q %q", out, err)
	}

	if out, err := run("Merge when ready", reviewMe, ""); err != "" || !strings.Contains(out, "Auto-merge is on: GitHub will squash and merge") {
		t.Fatalf("merge when ready: %q %q", out, err)
	}
	if a := detail(reviewMe).AutoMerge; a == nil || a.Method != "SQUASH" || a.By != "you" {
		t.Errorf("auto-merge not on: %+v", a)
	}
	if out, _ := run("Merge when ready", ready, ""); !strings.Contains(out, "Merged northwind/operator#312") {
		t.Errorf("a ready PR should merge at once: %q", out)
	}
	if _, err := run("Merge when ready", flight, ""); !strings.Contains(err, "does not allow auto-merge") {
		t.Errorf("repo without auto-merge: %q", err)
	}
}
