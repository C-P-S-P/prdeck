package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// maxImage is the largest image FetchImage downloads.
const maxImage = 15 << 20

var plainHTTP = &http.Client{Timeout: 30 * time.Second, Transport: imageTransport()}

// imageTransport is Go's default transport, except that it never connects to this
// machine or its link-local network: anyone who can comment could otherwise make
// opening a PR send GET requests to local servers (github.com proxies images, so
// they cannot show there either). The one local address it does dial is a proxy set
// in the environment (often on localhost); Go never sends loopback requests through
// a proxy, so those are still checked here.
func imageTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	proxies := sync.OnceValue(proxyAddrs)
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: func(_, addr string, _ syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(addr)
		ip, err := netip.ParseAddr(host)
		if err != nil || !(ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
			return nil
		}
		if proxies()[addr] {
			return nil
		}
		return fmt.Errorf("%s is a local address", host)
	}}
	t.DialContext = d.DialContext
	return t
}

// proxyAddrs are the addresses ("127.0.0.1:3128") of the proxies set in the environment.
func proxyAddrs() map[string]bool {
	out := map[string]bool{}
	for _, v := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		raw := os.Getenv(v)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		port := u.Port()
		if port == "" {
			port = map[string]string{"https": "443", "socks5": "1080", "socks5h": "1080"}[u.Scheme]
			if port == "" {
				port = "80"
			}
		}
		ips, _ := net.LookupIP(u.Hostname())
		for _, ip := range ips {
			out[net.JoinHostPort(ip.String(), port)] = true
		}
	}
	return out
}

// FetchImage downloads an image from a PR or comment body. Images hosted by GitHub
// (attachments, avatars, private repo images) are fetched with the gh login; any
// other host gets a plain request, so the token never leaves GitHub.
func (c *Client) FetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	client := plainHTTP
	if githubHosted(rawURL) {
		// GitHub answers http:// with a redirect to https, but the token would already
		// have crossed the network in the clear; go to https first.
		rawURL = withHTTPS(rawURL)
		client = c.imageClient()
	}
	return fetch(ctx, client, rawURL)
}

// imageClient is the logged-in client for GitHub-hosted images. It is made once:
// finding the token can mean running `gh auth token`, and a PR's images all load
// at once. If that fails, images get plain requests and the next one tries again.
func (c *Client) imageClient() *http.Client {
	c.imgMu.Lock()
	defer c.imgMu.Unlock()
	if c.img == nil {
		hc, err := api.DefaultHTTPClient()
		if err != nil {
			return plainHTTP
		}
		hc.Timeout = 30 * time.Second
		hc.CheckRedirect = httpsOnly
		c.img = hc
	}
	return c.img
}

// withHTTPS is rawURL with an http: scheme changed to https:.
func withHTTPS(rawURL string) string {
	if len(rawURL) > 5 && strings.EqualFold(rawURL[:5], "http:") {
		return "https:" + rawURL[5:]
	}
	return rawURL
}

// httpsOnly is the redirect policy for requests that carry the token: Go's default
// (at most 10 redirects), but never to plain http.
func httpsOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("%s: redirected to plain http", via[0].URL.Host)
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// FetchPublicImage downloads an image without any login.
func FetchPublicImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	return fetch(ctx, plainHTTP, rawURL)
}

func githubHosted(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "github.com" || strings.HasSuffix(h, ".github.com") || strings.HasSuffix(h, ".githubusercontent.com")
}

func fetch(ctx context.Context, client *http.Client, rawURL string) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, "", fmt.Errorf("not a web address: %q", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	// What a browser sends; some hosts (Codecov) refuse a narrower Accept.
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5")
	req.Header.Set("User-Agent", "prdeck")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%s: %s", u.Host, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImage+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxImage {
		return nil, "", fmt.Errorf("image larger than %d MB", maxImage>>20)
	}
	return data, resp.Header.Get("Content-Type"), nil
}
