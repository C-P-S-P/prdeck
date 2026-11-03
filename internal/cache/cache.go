// Package cache keeps the last PR lists and PR details on disk so the app opens instantly,
// and when you last opened each PR, so both apps can show what is new.
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

	"github.com/MasterPushers/prdeck/internal/github"
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
	seenFile
}

// seenFile is when you last opened each PR (by key), as of the update you saw, and
// when PR Deck began keeping track: what was updated before then counts as seen.
type seenFile struct {
	Seen      map[string]time.Time `json:"seen"`
	SeenSince time.Time            `json:"seenSince"`
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
	if c.data.Seen == nil {
		c.data.Seen = map[string]time.Time{}
	}
	if c.data.SeenSince.IsZero() {
		c.data.SeenSince = time.Now() // first run: what is listed now counts as seen
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
	if !pr.UpdatedAt.IsZero() && (e.Detail.UpdatedAt.Before(pr.UpdatedAt) || e.Detail.CheckState != pr.CheckState) {
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
	for k, t := range c.data.Seen {
		if time.Since(t) > keepDetails {
			delete(c.data.Seen, k) // SeenAt never looks back that far
		}
	}
	c.mu.Unlock()
	c.save()
}

// SeenAt is when you last opened the PR, as of the update you saw then. A PR never
// opened counts as seen when PR Deck began keeping track, and nothing older than
// keepDetails is unread.
func (c *Cache) SeenAt(key string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seenAt(key)
}

func (c *Cache) seenAt(key string) time.Time {
	t := c.data.SeenSince
	if floor := time.Now().Add(-keepDetails); floor.After(t) {
		t = floor
	}
	if s := c.data.Seen[key]; s.After(t) {
		t = s
	}
	return t
}

// Unread reports a PR updated since you last opened it.
func (c *Cache) Unread(pr github.PR) bool { return pr.UpdatedAt.After(c.SeenAt(pr.Key())) }

// MarkSeen records that you opened the PR as it was at at (GitHub's time of the
// newest thing shown), and returns when you had seen it before. It is saved with the
// next change.
func (c *Cache) MarkSeen(key string, at time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	last := c.seenAt(key)
	if at.After(last) {
		c.data.Seen[key] = at
	}
	return last
}

func (c *Cache) save() {
	// The terminal and desktop apps share this file: keep the PRs the other one
	// opened since this one read it. Two saving at the same moment can still lose a
	// mark, which only shows that PR as unread again.
	var disk seenFile
	if raw, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(raw, &disk)
	}
	c.mu.Lock()
	for k, t := range disk.Seen {
		if t.After(c.data.Seen[k]) {
			c.data.Seen[k] = t
		}
	}
	if !disk.SeenSince.IsZero() && disk.SeenSince.Before(c.data.SeenSince) {
		c.data.SeenSince = disk.SeenSince
	}
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
