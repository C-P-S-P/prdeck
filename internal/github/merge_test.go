package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestMergeState(t *testing.T) {
	raw := `{"number": 7, "repository": {"nameWithOwner": "acme/tool"}, "mergeStateStatus": "BLOCKED",
		"autoMergeRequest": {"mergeMethod": "SQUASH", "enabledAt": "2026-11-04T10:00:00Z", "enabledBy": {"login": "mlin"}},
		"baseRepository": {"autoMergeAllowed": true, "viewerDefaultMergeMethod": "SQUASH", "mergeCommitAllowed": true, "squashMergeAllowed": true, "rebaseMergeAllowed": false}}`
	var n detailNode
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatal(err)
	}
	d := n.toDetail()
	if d.Repo != "acme/tool" || d.MergeStatus != "BLOCKED" || !d.AutoMergeAllowed || strings.Join(d.MergeMethods, ",") != "SQUASH,MERGE" {
		t.Errorf("merge state = %q %v %v (repo %q)", d.MergeStatus, d.AutoMergeAllowed, d.MergeMethods, d.Repo)
	}
	if a := d.AutoMerge; a == nil || a.Method != "SQUASH" || a.By != "mlin" || a.At.IsZero() {
		t.Errorf("auto-merge = %+v", a)
	}

	// Off, and a default the repo no longer allows: the allowed ones in GitHub's order.
	var off detailNode
	_ = json.Unmarshal([]byte(`{"baseRepository": {"viewerDefaultMergeMethod": "MERGE", "squashMergeAllowed": true, "rebaseMergeAllowed": true}}`), &off)
	if d := off.toDetail(); d.AutoMerge != nil || strings.Join(d.MergeMethods, ",") != "SQUASH,REBASE" {
		t.Errorf("off = %+v %v", d.AutoMerge, d.MergeMethods)
	}
}

func TestMergeWrites(t *testing.T) {
	var sent []map[string]any
	answer := `{"data": {}}`
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: "github.com", AuthToken: "test", Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var req struct{ Variables map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&req)
		sent = append(sent, req.Variables)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(answer)), Request: r}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{gql: gql}
	ctx := context.Background()
	if err := c.EnableAutoMerge(ctx, "PR_1", "REBASE"); err != nil {
		t.Fatal(err)
	}
	if err := c.Merge(ctx, "PR_1", "SQUASH", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := c.Merge(ctx, "PR_1", "MERGE", ""); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 3 || sent[0]["method"] != "REBASE" || sent[1]["head"] != "abc123" || sent[2]["head"] != nil {
		t.Errorf("sent = %v", sent)
	}

	// A refusal reads in GitHub's own words.
	answer = `{"data": {"enablePullRequestAutoMerge": null}, "errors": [{"type": "UNPROCESSABLE", "path": ["enablePullRequestAutoMerge"], "message": "Pull request is in clean status"}]}`
	err = c.EnableAutoMerge(ctx, "PR_1", "MERGE")
	if err == nil || err.Error() != "Pull request is in clean status" {
		t.Errorf("err = %v", err)
	}
	if Refusal(nil) != nil {
		t.Error("no error should stay no error")
	}
}
