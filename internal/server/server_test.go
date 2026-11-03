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
