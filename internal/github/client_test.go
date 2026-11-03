package github

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// answering is a Client whose GraphQL requests all get body.
func answering(t *testing.T, body string) *Client {
	t.Helper()
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: "github.com", AuthToken: "test", Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	return &Client{gql: gql}
}

func TestSearchSkipsPRsInOrgsTheLoginCannotSee(t *testing.T) {
	c := answering(t, `{"data":{"viewer":{"login":"me"},"search":{"nodes":[{"number":7,"title":"ok","repository":{"nameWithOwner":"a/b"}},null]}},
		"errors":[{"type":"FORBIDDEN","path":["search","nodes",1],"message":"Resource protected by organization SAML enforcement."}]}`)
	prs, viewer, err := c.Search(context.Background(), "involves:@me", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 7 || viewer != "me" {
		t.Errorf("got %d PRs, viewer %q; want #7 and me", len(prs), viewer)
	}

	c = answering(t, `{"data":{"viewer":{"login":"me"},"search":{"nodes":[null]}},
		"errors":[{"type":"FORBIDDEN","path":["search","nodes",0],"message":"Resource protected by organization SAML enforcement."}]}`)
	if _, _, err := c.Search(context.Background(), "involves:@me", 10); err == nil || !strings.Contains(err.Error(), "SAML") {
		t.Errorf("with every PR hidden, the search should still say why: %v", err)
	}

	c = answering(t, `{"data":{"viewer":{"login":"me"},"search":{"nodes":[]}},"errors":[{"type":"INVALID","message":"bad query"}]}`)
	if _, _, err := c.Search(context.Background(), "involves:@me", 10); err == nil {
		t.Error("other GraphQL errors should still fail the search")
	}
}

func TestNewClientSeesALoginMadeAfterStart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GH_CONFIG_DIR", dir)
	t.Setenv("GH_PATH", filepath.Join(dir, "no-gh")) // never ask the real gh
	t.Setenv("GH_HOST", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	login := func(token string) {
		if err := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte("github.com:\n    oauth_token: "+token+"\n    user: me\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewClient(); err != nil {
			t.Fatal(err)
		}
	}
	login("old")
	login("new")
	if token, _ := auth.TokenFromEnvOrConfig("github.com"); token != "new" {
		t.Errorf("token after signing in again = %q, want new", token)
	}
}
