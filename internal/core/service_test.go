package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/MasterPushers/prdeck/internal/cache"
	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/github"
)

func TestOpeningMarksSeen(t *testing.T) {
	c := cache.Open(filepath.Join(t.TempDir(), "cache.json"))
	s := New(&config.Config{}, "", nil, c)
	start := time.Now()
	pr := github.PR{Number: 7, Repo: "acme/tool", UpdatedAt: start.Add(2 * time.Minute)}
	c.PutSearch("involves:@me", "you", []github.PR{pr})
	c.PutDetail(&github.Detail{PR: pr, Timeline: []github.Event{
		{Kind: "comment", Author: "mlin", At: start.Add(time.Minute)},
		{Kind: "comment", Author: "you", At: start.Add(2 * time.Minute)},
	}})

	if e, _ := s.CachedSearch("involves:@me"); !e.PRs[0].Unread {
		t.Fatal("mlin commented after PR Deck started: the PR should be unread")
	}
	d, _ := s.CachedDetail(pr)
	if !d.Timeline[0].New || d.Timeline[1].New {
		t.Errorf("mlin's comment is new, yours is not: %+v", d.Timeline)
	}
	if e, _ := s.CachedSearch("involves:@me"); e.PRs[0].Unread {
		t.Error("opened: no longer unread")
	}
	if d, _ := s.CachedDetail(pr); d.Timeline[0].New || !d.LastSeen.Equal(pr.UpdatedAt) {
		t.Errorf("opened again: nothing is new since %v: %+v", d.LastSeen, d.Timeline)
	}
	if e, _ := c.Search("involves:@me"); e.PRs[0].Unread {
		t.Error("the saved list was changed")
	}
}
