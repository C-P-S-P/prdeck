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
