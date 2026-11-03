package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// maxImage is the largest image FetchImage downloads.
const maxImage = 15 << 20

var plainHTTP = &http.Client{Timeout: 30 * time.Second}

// FetchImage downloads an image from a PR or comment body. Images hosted by GitHub
// (attachments, avatars, private repo images) are fetched with the gh login; any
// other host gets a plain request, so the token never leaves GitHub.
func (c *Client) FetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	client := plainHTTP
	if githubHosted(rawURL) {
		hc, err := api.DefaultHTTPClient()
		if err == nil {
			hc.Timeout = 30 * time.Second
			client = hc
		}
	}
	return fetch(ctx, client, rawURL)
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
