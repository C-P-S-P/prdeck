// Package fuzzy is a small fzf-style matcher for short strings such as paths.
//
// A query is split on spaces; every term must appear in order (not necessarily next to
// each other) in the text. Matches score higher when letters are consecutive, start a
// word (after / - _ . or a space), or fall in the last path element.
package fuzzy

import (
	"sort"
	"strings"
	"unicode"
)

// Match scores query against s. ok is false when some term does not match; idx holds
// the matched byte offsets in s, for highlighting.
func Match(query, s string) (score int, idx []int, ok bool) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 0, nil, true
	}
	lower := strings.ToLower(s)
	base := strings.LastIndex(lower, "/") + 1
	seen := map[int]bool{}
	for _, t := range terms {
		sc, pos, ok := matchTerm(t, lower, base)
		if !ok {
			return 0, nil, false
		}
		score += sc
		for _, p := range pos {
			if !seen[p] {
				seen[p] = true
				idx = append(idx, p)
			}
		}
	}
	sort.Ints(idx)
	// Shorter texts win ties: ~/projects/feast before ~/projects/feast-old/feast.
	score -= len(s) / 8
	return score, idx, true
}

// matchTerm tries every start position for the term's first letter and keeps the best
// scoring greedy match from there.
func matchTerm(term, s string, base int) (int, []int, bool) {
	best, bestPos := -1, []int(nil)
	for start := 0; start < len(s); start++ {
		if s[start] != term[0] {
			continue
		}
		pos := []int{start}
		j := start + 1
		for k := 1; k < len(term); k++ {
			for j < len(s) && s[j] != term[k] {
				j++
			}
			if j == len(s) {
				pos = nil
				break
			}
			pos = append(pos, j)
			j++
		}
		if pos == nil {
			break // later starts cannot match either
		}
		if sc := scorePositions(s, pos, base); sc > best {
			best, bestPos = sc, pos
		}
	}
	return best, bestPos, best >= 0
}

func scorePositions(s string, pos []int, base int) int {
	score := 0
	for i, p := range pos {
		score += 16
		if p == 0 || isBoundary(rune(s[p-1])) {
			score += 12
		}
		if p >= base {
			score += 6
		}
		if i > 0 {
			if gap := p - pos[i-1] - 1; gap == 0 {
				score += 10
			} else {
				score -= min(gap, 8)
			}
		}
	}
	// A term that is the whole last path element is what people usually mean.
	if pos[0] == base && pos[len(pos)-1] == len(s)-1 && len(pos) == len(s)-base {
		score += 30
	}
	return score
}

func isBoundary(r rune) bool {
	return r == '/' || r == '-' || r == '_' || r == '.' || unicode.IsSpace(r)
}
