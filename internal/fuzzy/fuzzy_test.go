package fuzzy

import (
	"sort"
	"strings"
	"testing"
)

func rank(query string, items []string) []string {
	type hit struct {
		s     string
		score int
	}
	var hits []hit
	for _, it := range items {
		if sc, _, ok := Match(query, it); ok {
			hits = append(hits, hit{it, sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	var out []string
	for _, h := range hits {
		out = append(out, h.s)
	}
	return out
}

var paths = []string{
	"~/projects/odh-feast",
	"~/projects/feast",
	"~/projects/rd-feast",
	"~/projects/feast-module-operator",
	"~/work/old/feast-fork",
	"~/projects/cp",
	"~/code/registry",
}

func TestRanking(t *testing.T) {
	cases := map[string]string{
		"feast":     "~/projects/feast",
		"odh":       "~/projects/odh-feast",
		"fmo":       "~/projects/feast-module-operator",
		"proj rd":   "~/projects/rd-feast",
		"old feast": "~/work/old/feast-fork",
		"reg":       "~/code/registry",
	}
	for q, want := range cases {
		got := rank(q, paths)
		if len(got) == 0 || got[0] != want {
			t.Errorf("%q: top = %v, want %s", q, got, want)
		}
	}
}

func TestNoMatch(t *testing.T) {
	if _, _, ok := Match("xyz", "~/projects/feast"); ok {
		t.Error("xyz should not match")
	}
	if _, _, ok := Match("feast zzz", "~/projects/feast"); ok {
		t.Error("every term must match")
	}
}

func TestIndexes(t *testing.T) {
	_, idx, ok := Match("cp", "~/projects/cp")
	if !ok || len(idx) != 2 || idx[0] != 11 || idx[1] != 12 {
		t.Errorf("idx = %v", idx)
	}
}

func TestIndexesPointIntoTheText(t *testing.T) {
	// The Kelvin sign lowercases to a one-byte k, so offsets in the lowercased text
	// are not offsets in s. İ gets one byte shorter and Ⱥ one longer, which leaves the
	// length as it was.
	for _, s := range []string{"~/K/feast", "~/İfeastȺ"} {
		_, idx, ok := Match("feast", s)
		if !ok {
			t.Fatalf("feast should match %q", s)
		}
		for _, i := range idx {
			if !strings.ContainsRune("feast", rune(s[i])) {
				t.Errorf("%q: idx = %v marks byte %q", s, idx, s[i])
			}
		}
	}
}

func TestWordStartAfterNonASCII(t *testing.T) {
	score := func(s string) int { sc, _, _ := Match("x", s); return sc }
	// The last byte of à is 0xA0, which is not a space.
	if a, b := score("~/àx"), score("~/ax"); a != b {
		t.Errorf("x after à scores %d, after a %d", a, b)
	}
	if a, b := score("~/a x"), score("~/a x"); a != b {
		t.Errorf("x after a no-break space scores %d, after a space %d", a, b)
	}
}

func TestEmptyQueryMatchesAll(t *testing.T) {
	if _, _, ok := Match("  ", "anything"); !ok {
		t.Error("empty query should match")
	}
}
