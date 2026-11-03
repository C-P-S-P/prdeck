package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenNeverOverPlainHTTP(t *testing.T) {
	cases := map[string]string{
		"http://github.com/a.png":                        "https://github.com/a.png",
		"HTTP://user-images.githubusercontent.com/a.png": "https://user-images.githubusercontent.com/a.png",
		"https://github.com/a.png":                       "https://github.com/a.png",
		"httpx://github.com/a.png":                       "httpx://github.com/a.png",
	}
	for in, want := range cases {
		if got := withHTTPS(in); got != want {
			t.Errorf("withHTTPS(%q) = %q, want %q", in, got, want)
		}
	}

	req := func(u string) *http.Request {
		p, _ := url.Parse(u)
		return &http.Request{URL: p}
	}
	from := []*http.Request{req("https://github.com/a.png")}
	if httpsOnly(req("http://github.com/b.png"), from) == nil {
		t.Error("followed a redirect to plain http")
	}
	if err := httpsOnly(req("https://avatars.githubusercontent.com/b.png"), from); err != nil {
		t.Errorf("refused an https redirect: %v", err)
	}
	many := make([]*http.Request, 10)
	for i := range many {
		many[i] = from[0]
	}
	if httpsOnly(req("https://github.com/c.png"), many) == nil {
		t.Error("followed an 11th redirect")
	}
}

func TestPRImagesNeverReachThisMachine(t *testing.T) {
	if os.Getenv("HTTPS_PROXY")+os.Getenv("https_proxy")+os.Getenv("HTTP_PROXY")+os.Getenv("http_proxy") != "" {
		t.Skip("a proxy is set, so the proxy does the dialing")
	}
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Store(true) }))
	defer srv.Close()
	if _, _, err := FetchPublicImage(context.Background(), srv.URL+"/admin/reset"); err == nil || hit.Load() {
		t.Errorf("an image URL on 127.0.0.1 was fetched (err %v)", err)
	}
}

func TestGitHubImagesShareOneLoggedInClient(t *testing.T) {
	t.Setenv("GH_HOST", "github.com")
	t.Setenv("GH_TOKEN", "test")
	c := &Client{}
	hc := c.imageClient()
	if hc == plainHTTP || c.imageClient() != hc {
		t.Error("each GitHub image looked up the login again")
	}
	if hc.CheckRedirect == nil || hc.Timeout != 30*time.Second {
		t.Error("the logged-in client lost its redirect policy or timeout")
	}
}
