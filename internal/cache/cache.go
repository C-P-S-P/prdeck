// Package cache keeps the last PR lists and PR details on disk so the app opens instantly.
//
// The ARD plans SQLite once threads and drafts are written locally (phase 3);
// until then a small JSON file is enough.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/C-P-S-P/prdeck/internal/github"
)

const keepDetails = 30 * 24 * time.Hour

type Cache struct {
	path string
	mu   sync.Mutex
	data file
}

type file struct {
	Viewer   string                 `json:"viewer"`
	Searches map[string]SearchEntry `json:"searches"`
	Details  map[string]DetailEntry `json:"details"`
}

type SearchEntry struct {
	At  time.Time   `json:"at"`
	PRs []github.PR `json:"prs"`
}

type DetailEntry struct {
	At     time.Time      `json:"at"`
	Detail *github.Detail `json:"detail"`
}

// DefaultPath is <user cache dir>/prdeck/cache.json.
func DefaultPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "prdeck", "cache.json")
}

// Open reads the cache file; a missing or broken file starts an empty cache.
func Open(path string) *Cache {
	c := &Cache{path: path}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &c.data)
	}
	if c.data.Searches == nil {
		c.data.Searches = map[string]SearchEntry{}
	}
	if c.data.Details == nil {
		c.data.Details = map[string]DetailEntry{}
	}
	return c
}

func (c *Cache) Viewer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data.Viewer
}

func (c *Cache) Search(query string) (SearchEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data.Searches[query]
	return e, ok
}

func (c *Cache) PutSearch(query, viewer string, prs []github.PR) {
	c.mu.Lock()
	c.data.Searches[query] = SearchEntry{At: time.Now(), PRs: prs}
	if viewer != "" {
		c.data.Viewer = viewer
	}
	c.mu.Unlock()
	c.save()
}

// Detail returns the cached detail only if it is as new as the PR summary. CI
// finishing does not change a PR's update time, so a different check rollup also
// makes it stale.
func (c *Cache) Detail(pr github.PR) (*github.Detail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data.Details[pr.Key()]
	if !ok || e.Detail == nil {
		return nil, false
	}
	if !pr.UpdatedAt.IsZero() && e.Detail.UpdatedAt.Before(pr.UpdatedAt) || e.Detail.CheckState != pr.CheckState {
		return e.Detail, false // stale, but still useful to show while refreshing
	}
	return e.Detail, true
}

func (c *Cache) PutDetail(d *github.Detail) {
	c.mu.Lock()
	c.data.Details[d.Key()] = DetailEntry{At: time.Now(), Detail: d}
	for k, e := range c.data.Details {
		if time.Since(e.At) > keepDetails {
			delete(c.data.Details, k)
		}
	}
	c.mu.Unlock()
	c.save()
}

func (c *Cache) save() {
	c.mu.Lock()
	raw, err := json.Marshal(c.data)
	c.mu.Unlock()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return
	}
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, c.path)
	}
}
