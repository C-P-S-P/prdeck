package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MasterPushers/prdeck/internal/demo"
	"github.com/MasterPushers/prdeck/internal/github"
)

// The desktop app reads serve's replies into its own Rust types (desktop/src/model.rs).
// TestContract saves the demo reply to each method the app calls as
// desktop/tests/fixtures/<method>.json, and the app's tests read the same files into
// those types, so a field renamed or retyped on one side fails a test. After changing
// a reply on purpose, rewrite the files and run the app's tests:
//
//	go test ./internal/server -run TestContract -update
//	cd desktop && cargo test

var update = flag.Bool("update", false, "rewrite the protocol fixtures in desktop/tests/fixtures")

const fixtures = "../../desktop/tests/fixtures"

// pixel is a 1×1 PNG, the answer to every image so nothing goes online.
const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// pixelImages is the demo backend with every image fetched as pixel. A local test
// server would not do: image fetches refuse local addresses.
type pixelImages struct{ *demo.Backend }

func (pixelImages) FetchImage(context.Context, string) ([]byte, string, error) {
	b, err := base64.StdEncoding.DecodeString(pixel)
	return b, "image/png", err
}

func TestContract(t *testing.T) {
	home := t.TempDir() // setupStatus lists folders that exist under home
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home folder on Windows
	if *update {
		if err := os.MkdirAll(fixtures, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	c := startWith(t, func(b *demo.Backend) Backend { return pixelImages{b} })
	// golden calls method, checks its reply against the saved file, and returns the
	// reply as sent, for the calls that follow.
	golden := func(method string, params any) json.RawMessage {
		t.Helper()
		r := c.call(method, params)
		if r.Error != nil {
			t.Fatalf("%s: %s", method, r.Error.Message)
		}
		var v any
		if err := json.Unmarshal(r.Result, &v); err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		enc := json.NewEncoder(&got)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(steady(v, method == "setupStatus")); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(fixtures, method+".json")
		if *update {
			if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			return r.Result
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v; run go test ./internal/server -run TestContract -update to make it", err)
		}
		want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")) // a Windows checkout may end lines with \r\n
		if !bytes.Equal(got.Bytes(), want) {
			t.Errorf("%s: the reply no longer matches %s, %s\nIf the change is meant, run go test ./internal/server -run TestContract -update, then cargo test in desktop/.",
				method, path, firstDiff(got.String(), string(want)))
		}
		return r.Result
	}

	golden("hello", nil)
	golden("setupStatus", nil)
	q := map[string]string{"query": "involves:@me"}
	golden("cachedSearch", q)
	var res struct{ PRs []github.PR }
	_ = json.Unmarshal(golden("search", q), &res)
	// #4821 has a bit of everything: a code thread, commits, uncommitted work in its
	// clone and a conflict. upstream/datakit has no clone.
	var pr, noClone github.PR
	for _, p := range res.PRs {
		switch {
		case p.Number == 4821:
			pr = p
		case p.Repo == "upstream/datakit":
			noClone = p
		}
	}
	if pr.Number == 0 || noClone.Number == 0 {
		t.Fatal("demo data changed: need #4821 and a upstream/datakit PR")
	}

	golden("cachedDetail", map[string]any{"pr": pr})
	var d github.Detail
	_ = json.Unmarshal(golden("detail", map[string]any{"pr": pr}), &d)
	golden("folder", map[string]any{"pr": pr, "worktree": false})
	var plan map[string]any
	_ = json.Unmarshal(golden("prepare", map[string]any{"command": "Rebase", "pr": pr, "input": ""}), &plan)
	golden("execute", map[string]any{"plan": plan, "changes": "stash"})
	golden("clonePlan", map[string]any{"pr": noClone})
	golden("findClones", map[string]any{"repo": noClone.Repo})
	golden("setClonePath", map[string]any{"repo": noClone.Repo, "path": "~/work/datakit"})
	golden("image", map[string]any{"url": "https://github.com/northwind/registry/assets/chart.png"})
	golden("prDiff", map[string]any{"pr": pr})
	golden("commitDiff", map[string]any{"repo": pr.Repo, "sha": d.CommitList[4].OID})

	golden("comment", map[string]any{"prId": d.NodeID, "body": "Rebased on main."})
	threads := []map[string]any{{"path": d.Files[1].Path, "line": 12, "side": "RIGHT", "body": "Is this still needed?"}}
	golden("review", map[string]any{"prId": d.NodeID, "event": "COMMENT", "body": "", "threads": threads})
	for _, e := range d.Timeline {
		if e.Kind == "thread" {
			golden("replyThread", map[string]any{"threadId": e.ThreadID, "body": "Done."})
			break
		}
	}

	// loginWait before loginStart: in demo mode it returns six seconds after loginStart.
	golden("loginWait", nil)
	golden("loginStart", nil)
	golden("loginCancel", nil)
	golden("installGh", nil)
	golden("saveSetup", map[string]any{"scan": []string{"~/code"}, "editor": "zed", "editorRun": "", "worktree": false})
	// openConfig is left out: it opens the config file in an editor.
}

// steady blanks what changes from run to run, so a fixture changes only when the
// protocol does: times (the demo's are relative to when it started), the separator in
// paths under the home folder (~\code on Windows) and, in setupStatus, what is
// installed on this machine.
func steady(v any, machine bool) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			switch {
			case machine && (k == "ghVersion" || k == "brewFound" || k == "installer" || k == "installHint" || k == "installed" || k == "run"):
				v[k] = blank(x)
			default:
				v[k] = steady(x, machine)
			}
		}
	case []any:
		for i, x := range v {
			v[i] = steady(x, machine)
		}
	case string:
		if _, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return "2026-01-02T03:04:05Z"
		}
		if strings.HasPrefix(v, `~\`) {
			return filepath.ToSlash(v)
		}
	}
	return v
}

// blank is the empty value of x's JSON type.
func blank(x any) any {
	switch x.(type) {
	case bool:
		return false
	case string:
		return ""
	}
	return x
}

// firstDiff says where got and want first differ, line by line.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("first at line %d:\n  want %s\n  got  %s", i+1, strings.TrimSpace(wl), strings.TrimSpace(gl))
		}
	}
	return "only in line endings"
}
