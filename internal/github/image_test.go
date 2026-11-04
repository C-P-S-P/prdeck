package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// With a proxy set, loopback requests still never go out: Go dials them directly
// (it never proxies localhost), so the guard must stay on. Only the proxy's own
// address may be dialed.
func TestProxyDoesNotTurnOffTheLocalGuard(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Store(true) }))
	defer srv.Close()
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	client := &http.Client{Transport: imageTransport()}
	if _, _, err := fetch(context.Background(), client, srv.URL+"/x.png"); err == nil || hit.Load() {
		t.Fatalf("reached %s through the image client with a proxy set (err=%v)", srv.URL, err)
	}
	if !proxyAddrs()["127.0.0.1:1"] {
		t.Errorf("proxy address not allowed: %v", proxyAddrs())
	}
}

// A busy server is asked again, and gets there on the third try; a missing image
// and a refused request fail at once.
func TestFetchTriesAgain(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.URL.Path == "/busy.png" && n.Load() == imageTries {
			_, _ = w.Write([]byte("\x89PNG"))
			return
		}
		if r.URL.Path == "/busy.png" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	ctx := context.Background()
	if data, _, err := fetch(ctx, srv.Client(), srv.URL+"/busy.png"); err != nil || string(data) != "\x89PNG" || n.Load() != imageTries {
		t.Errorf("busy server: data %q, err %v, after %d tries", data, err, n.Load())
	}
	n.Store(0)
	if _, _, err := fetch(ctx, srv.Client(), srv.URL+"/gone.png"); err == nil || n.Load() != 1 {
		t.Errorf("missing image: err %v, after %d tries", err, n.Load())
	}
	start := time.Now()
	if _, _, err := fetch(ctx, &http.Client{Transport: imageTransport()}, srv.URL+"/local.png"); err == nil || time.Since(start) > 900*time.Millisecond {
		t.Errorf("refused request: err %v, took %v", err, time.Since(start))
	}
}
