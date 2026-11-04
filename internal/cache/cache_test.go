package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MasterPushers/prdeck/internal/github"
)

func TestDetailStaleWhenChecksChange(t *testing.T) {
	c := Open(filepath.Join(t.TempDir(), "cache.json"))
	pr := github.PR{Number: 7, Repo: "acme/tool", UpdatedAt: time.Now(), CheckState: "PENDING"}
	c.PutDetail(&github.Detail{PR: pr})
	if _, fresh := c.Detail(pr); !fresh {
		t.Error("same PR, same checks: should be fresh")
	}
	// CI finished: the PR's update time stays, its check rollup does not.
	pr.CheckState = "FAILURE"
	if d, fresh := c.Detail(pr); d == nil || fresh {
		t.Errorf("checks changed: got %v, fresh %v; want the old detail, stale", d, fresh)
	}
	// A PR known only by its number says nothing about its checks either.
	if _, fresh := c.Detail(github.PR{Number: 7, Repo: "acme/tool"}); !fresh {
		t.Error("a PR without a summary should take the saved detail as it is")
	}
}

func TestUnreadSinceLastOpened(t *testing.T) {
	c := Open(filepath.Join(t.TempDir(), "cache.json"))
	pr := github.PR{Number: 7, Repo: "acme/tool", UpdatedAt: time.Now().Add(-time.Hour)}
	if c.Unread(pr) {
		t.Error("first run: a PR listed now should count as seen")
	}
	pr.UpdatedAt = time.Now().Add(time.Minute) // someone commented after PR Deck started
	if !c.Unread(pr) {
		t.Error("updated after the last visit: should be unread")
	}
	before := c.MarkSeen(pr.Key(), pr.UpdatedAt)
	if c.Unread(pr) || !before.Before(pr.UpdatedAt) {
		t.Errorf("opened: unread %v, seen before %v", c.Unread(pr), before)
	}
	if again := c.MarkSeen(pr.Key(), pr.UpdatedAt.Add(-time.Hour)); !again.Equal(pr.UpdatedAt) {
		t.Errorf("an older detail must not move the mark back: %v", again)
	}
}

func TestSeenSharedBetweenApps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	term, desk := Open(path), Open(path)
	pr := github.PR{Number: 7, Repo: "acme/tool", UpdatedAt: time.Now().Add(time.Minute)}
	term.MarkSeen(pr.Key(), pr.UpdatedAt)
	term.PutSearch("involves:@me", "you", []github.PR{pr})
	if !desk.Unread(pr) {
		t.Fatal("the desktop has not refreshed yet")
	}
	desk.PutSearch("involves:@me", "you", []github.PR{pr}) // its next refresh
	if desk.Unread(pr) {
		t.Error("opened in the terminal: the desktop should see it on its next refresh")
	}
	if Open(path).Unread(pr) {
		t.Error("the desktop's save dropped the terminal's mark")
	}
}

func TestOldSeenPruned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	c := Open(path)
	c.data.SeenSince = time.Now().Add(-90 * 24 * time.Hour)
	old := github.PR{Number: 1, Repo: "acme/tool", UpdatedAt: time.Now().Add(-40 * 24 * time.Hour)}
	c.mu.Lock()
	c.data.Seen[old.Key()] = old.UpdatedAt // opened 40 days ago
	c.mu.Unlock()
	raw, _ := json.Marshal(c.data)
	_ = os.WriteFile(path, raw, 0o600) // and saved then
	c.PutDetail(&github.Detail{PR: github.PR{Number: 2, Repo: "acme/tool"}})
	if _, kept := Open(path).data.Seen[old.Key()]; kept {
		t.Error("a mark older than keepDetails should be dropped, not merged back from the file")
	}
	if c.Unread(old) {
		t.Error("nothing that old is unread, mark or not")
	}
}

func TestSavesSideBySide(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	term, desk := Open(path), Open(path)
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := term
			if i%2 == 1 {
				c = desk
			}
			pr := github.PR{Number: i, Repo: "acme/tool", Title: strings.Repeat("x", 4000), UpdatedAt: time.Now()}
			c.MarkSeen(pr.Key(), pr.UpdatedAt)
			c.PutSearch("involves:@me", "you", []github.PR{pr})
		}()
	}
	wg.Wait()
	raw, err := os.ReadFile(path)
	if err != nil || !json.Valid(raw) {
		t.Fatalf("saves at the same time left a broken file (%v)", err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp")); len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestConcurrentSavesLeaveTheWholeCache(t *testing.T) {
	dir := t.TempDir()
	for round := 0; round < 100; round++ {
		path := filepath.Join(dir, strconv.Itoa(round), "cache.json")
		c := Open(path)
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.PutSearch("q"+strconv.Itoa(g), "me", make([]github.PR, g*4)) // different sizes
			}()
		}
		wg.Wait()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var f file
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("round %d: cache.json is broken: %v", round, err)
		}
		if len(f.Searches) != 8 {
			t.Fatalf("round %d: cache.json has %d searches, want 8 (an older save landed last)", round, len(f.Searches))
		}
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp")); len(left) > 0 {
			t.Fatalf("round %d: temp files left behind: %v", round, left)
		}
	}
}
