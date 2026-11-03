package cache

import (
	"path/filepath"
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
	c := Open(filepath.Join(t.TempDir(), "cache.json"))
	c.data.SeenSince = time.Now().Add(-90 * 24 * time.Hour)
	old := github.PR{Number: 1, Repo: "acme/tool", UpdatedAt: time.Now().Add(-40 * 24 * time.Hour)}
	c.data.Seen[old.Key()] = old.UpdatedAt // opened 40 days ago
	c.PutDetail(&github.Detail{PR: github.PR{Number: 2, Repo: "acme/tool"}})
	if _, kept := c.data.Seen[old.Key()]; kept {
		t.Error("a mark older than keepDetails should be dropped")
	}
	if c.Unread(old) {
		t.Error("nothing that old is unread, mark or not")
	}
}
