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

func TestCheckLinksAndRuns(t *testing.T) {
	raw := `{"number": 7, "repository": {"nameWithOwner": "acme/tool"}, "commits": {"nodes": [{"commit": {"statusCheckRollup": {"state": "FAILURE", "contexts": {"nodes": [
		{"__typename": "CheckRun", "name": "lint", "conclusion": "SUCCESS", "detailsUrl": "https://github.com/acme/tool/actions/runs/11/job/1", "checkSuite": {"workflowRun": {"databaseId": 11}}},
		{"__typename": "CheckRun", "name": "unit", "conclusion": "FAILURE", "detailsUrl": "https://github.com/acme/tool/actions/runs/11/job/2", "checkSuite": {"workflowRun": {"databaseId": 11}}},
		{"__typename": "CheckRun", "name": "e2e", "conclusion": "TIMED_OUT", "detailsUrl": "https://github.com/acme/tool/actions/runs/12/job/3", "checkSuite": {"workflowRun": {"databaseId": 12}}},
		{"__typename": "CheckRun", "name": "docs", "conclusion": "CANCELLED", "detailsUrl": "https://github.com/acme/tool/actions/runs/11/job/4", "checkSuite": {"workflowRun": {"databaseId": 11}}},
		{"__typename": "CheckRun", "name": "buildkite", "conclusion": "FAILURE", "detailsUrl": "javascript:alert(1)", "checkSuite": {"workflowRun": null}},
		{"__typename": "StatusContext", "context": "codecov/patch", "state": "FAILURE", "targetUrl": "https://app.codecov.io/gh/acme/tool/pull/7"},
		{"__typename": "StatusContext", "context": "jenkins", "state": "ERROR", "targetUrl": "https://ci.example.com/\u001b]8;;x"}
	]}}}}]}}`
	var n detailNode
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatal(err)
	}
	d := n.toDetail()
	urls := map[string]string{}
	for _, c := range d.Checks {
		urls[c.Name] = c.URL
	}
	if urls["unit"] != "https://github.com/acme/tool/actions/runs/11/job/2" || urls["codecov/patch"] != "https://app.codecov.io/gh/acme/tool/pull/7" {
		t.Errorf("links lost: %v", urls)
	}
	if urls["buildkite"] != "" || urls["jenkins"] != "" {
		t.Errorf("unsafe links kept: %q %q", urls["buildkite"], urls["jenkins"])
	}

	runs, other := d.FailedRuns()
	if len(runs) != 2 || runs[0].ID != 11 || strings.Join(runs[0].Checks, ",") != "unit,docs" || runs[1].ID != 12 {
		t.Errorf("runs = %+v", runs)
	}
	if strings.Join(other, ",") != "buildkite,codecov/patch,jenkins" {
		t.Errorf("other = %v", other)
	}
	if pass, fail, pend := d.CheckCounts(); pass != 1 || fail != 6 || pend != 0 {
		t.Errorf("counts = %d %d %d", pass, fail, pend)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRerunFailedJobs(t *testing.T) {
	var got string
	rest, err := api.NewRESTClient(api.ClientOptions{Host: "github.com", AuthToken: "test", Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		got = r.Method + " " + r.URL.String()
		return &http.Response{StatusCode: 201, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{rest: rest}
	if err := c.RerunFailedJobs(context.Background(), "acme/tool", 11); err != nil {
		t.Fatal(err)
	}
	if got != "POST https://api.github.com/repos/acme/tool/actions/runs/11/rerun-failed-jobs" {
		t.Errorf("request = %q", got)
	}
	if err := c.RerunFailedJobs(context.Background(), "acme/tool", 0); err == nil {
		t.Error("run 0 should be refused")
	}
}
