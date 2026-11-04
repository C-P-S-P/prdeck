package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/MasterPushers/prdeck/internal/github"
)

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
