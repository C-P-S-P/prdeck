// Package github talks to the GitHub GraphQL API using the user's gh login.
package github

import (
	"sort"
	"strings"
	"time"
)

// PR is the summary shown in the PR list.
type PR struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	IsDraft        bool      `json:"isDraft"`
	State          string    `json:"state"`
	UpdatedAt      time.Time `json:"updatedAt"`
	CreatedAt      time.Time `json:"createdAt"`
	HeadRefName    string    `json:"headRefName"`
	BaseRefName    string    `json:"baseRefName"`
	IsCrossRepo    bool      `json:"isCrossRepository"`
	Mergeable      string    `json:"mergeable"`      // MERGEABLE, CONFLICTING, UNKNOWN
	ReviewDecision string    `json:"reviewDecision"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	Author         string    `json:"author"`
	Repo           string    `json:"repo"` // owner/name
	Comments       int       `json:"comments"`
	CheckState     string    `json:"checkState"` // SUCCESS, FAILURE, ERROR, PENDING, EXPECTED, ""
}

// Key identifies a PR across repos.
func (p PR) Key() string { return strings.ToLower(p.Repo) + "#" + itoa(p.Number) }

func (p PR) Owner() string { o, _, _ := strings.Cut(p.Repo, "/"); return o }
func (p PR) Name() string  { _, n, _ := strings.Cut(p.Repo, "/"); return n }

// CIFailed reports a failing or errored check rollup.
func (p PR) CIFailed() bool { return p.CheckState == "FAILURE" || p.CheckState == "ERROR" }

// Detail is everything the PR view shows.
type Detail struct {
	PR
	NodeID       string     `json:"nodeId"` // GitHub's ID for the PR, for comments and reviews
	Body         string     `json:"body"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changedFiles"`
	Commits      int        `json:"commits"`
	Labels       []string   `json:"labels"`
	Reviewers    []Reviewer `json:"reviewers"`
	Checks       []Check    `json:"checks"`
	Timeline     []Event    `json:"timeline"`
	CommitList   []Commit   `json:"commitList"` // oldest first, up to the last 100
	Files        []File     `json:"files"`      // up to the first 100

	// Merging: GitHub's merge state (CLEAN, BLOCKED, BEHIND, UNSTABLE, DIRTY,
	// HAS_HOOKS, DRAFT, UNKNOWN), auto-merge when it is on, and how the repo merges.
	MergeStatus      string     `json:"mergeStatus,omitempty"`
	AutoMerge        *AutoMerge `json:"autoMerge,omitempty"`
	AutoMergeAllowed bool       `json:"autoMergeAllowed,omitempty"`
	MergeMethods     []string   `json:"mergeMethods,omitempty"` // allowed ones (MERGE, SQUASH, REBASE), the viewer's default first
}

// AutoMerge is turned on for a PR: GitHub merges it once checks and reviews pass.
type AutoMerge struct {
	Method string    `json:"method"` // MERGE, SQUASH or REBASE
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

// MergeMethods lists the methods a repo allows, the default one first: the viewer's
// last used method, which GitHub's merge button also starts with.
func MergeMethods(def string, merge, squash, rebase bool) []string {
	var out []string
	for _, m := range []struct {
		name string
		ok   bool
	}{{"MERGE", merge}, {"SQUASH", squash}, {"REBASE", rebase}} {
		switch {
		case !m.ok:
		case m.name == def:
			out = append([]string{m.name}, out...)
		default:
			out = append(out, m.name)
		}
	}
	return out
}

// Commit is one commit on the PR branch.
type Commit struct {
	OID        string    `json:"oid"`
	Short      string    `json:"short"`
	Headline   string    `json:"headline"`
	Body       string    `json:"body"`
	At         time.Time `json:"at"`
	Author     string    `json:"author"` // GitHub login, or the git author name
	Additions  int       `json:"additions"`
	Deletions  int       `json:"deletions"`
	CheckState string    `json:"checkState"`
}

// File is one changed file.
type File struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	ChangeType string `json:"changeType"` // ADDED, MODIFIED, DELETED, RENAMED, COPIED, CHANGED
}

type Reviewer struct {
	Login string `json:"login"`
	State string `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING (requested, not yet reviewed)
}

type Check struct {
	Name  string `json:"name"`
	State string `json:"state"`           // SUCCESS, FAILURE, PENDING, NEUTRAL, SKIPPED, CANCELLED…
	URL   string `json:"url,omitempty"`   // its log or details page (http or https only)
	RunID int64  `json:"runId,omitempty"` // its GitHub Actions run; 0 for other CI and commit statuses
}

// Failed reports a check that failed, errored, timed out or was cancelled.
func (c Check) Failed() bool {
	switch c.State {
	case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return true
	}
	return false
}

// FailedRun is a GitHub Actions run with failed jobs, and the checks that failed in it.
type FailedRun struct {
	ID     int64
	Checks []string
}

// Event is one entry in the conversation: a comment, a review, or a code thread.
type Event struct {
	Kind     string    `json:"kind"` // comment, review, thread
	Author   string    `json:"author"`
	State    string    `json:"state"` // reviews: APPROVED, CHANGES_REQUESTED, COMMENTED
	Body     string    `json:"body"`
	At       time.Time `json:"at"`
	Path     string    `json:"path"`
	Line     int       `json:"line"`
	DiffHunk string    `json:"diffHunk"`
	ThreadID string    `json:"threadId"` // review threads: GitHub's ID, for replies
	Side     string    `json:"side"`     // review threads: RIGHT (new lines) or LEFT (removed lines)
	// Review threads on several lines: where they start (Line is where they end).
	StartLine int     `json:"startLine"`
	StartSide string  `json:"startSide"`
	Resolved  bool    `json:"resolved"`
	Outdated  bool    `json:"outdated"`
	Replies   []Reply `json:"replies"`
}

type Reply struct {
	Author string    `json:"author"`
	Body   string    `json:"body"`
	At     time.Time `json:"at"`
}

// CheckCounts returns passed, failed and pending check counts.
func (d *Detail) CheckCounts() (pass, fail, pend int) {
	for _, c := range d.Checks {
		switch {
		case c.State == "SUCCESS" || c.State == "NEUTRAL" || c.State == "SKIPPED":
			pass++
		case c.Failed():
			fail++
		default:
			pend++
		}
	}
	return
}

// FailedRuns groups the failed checks by GitHub Actions run, in the order they are
// listed. Failed checks that are not Actions jobs (other CI services, commit
// statuses) come back in other: they can only be re-run from their own page.
func (d *Detail) FailedRuns() (runs []FailedRun, other []string) {
	at := map[int64]int{}
	for _, c := range d.Checks {
		switch {
		case !c.Failed():
		case c.RunID == 0:
			other = append(other, c.Name)
		default:
			i, ok := at[c.RunID]
			if !ok {
				i = len(runs)
				at[c.RunID] = i
				runs = append(runs, FailedRun{ID: c.RunID})
			}
			runs[i].Checks = append(runs[i].Checks, c.Name)
		}
	}
	return runs, other
}

func sortEvents(ev []Event) {
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].At.Before(ev[j].At) })
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
