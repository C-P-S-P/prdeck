package github

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// FileDiff is one file's change, with its unified-diff patch. GitHub leaves Patch
// empty for binary files and for very large diffs.
type FileDiff struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previousPath"` // renames
	Status       string `json:"status"`       // added, modified, removed, renamed, copied, changed
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Patch        string `json:"patch"`
}

// CommitDiff is a commit with the changes it made.
type CommitDiff struct {
	OID     string     `json:"oid"`
	Message string     `json:"message"`
	Author  string     `json:"author"`
	At      time.Time  `json:"at"`
	Files   []FileDiff `json:"files"`
}

type restFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

func (f restFile) diff() FileDiff {
	return FileDiff{Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch}
}

// CommitDiff loads one commit's message and per-file patches.
func (c *Client) CommitDiff(ctx context.Context, repo, sha string) (*CommitDiff, error) {
	if !strings.Contains(repo, "/") || sha == "" {
		return nil, fmt.Errorf("bad commit %s@%s", repo, sha)
	}
	var resp struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name string    `json:"name"`
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
		Author *login     `json:"author"`
		Files  []restFile `json:"files"`
	}
	path := "repos/" + repo + "/commits/" + url.PathEscape(sha)
	if err := c.rest.DoWithContext(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	d := &CommitDiff{OID: resp.SHA, Message: resp.Commit.Message, Author: resp.Commit.Author.Name, At: resp.Commit.Author.Date}
	if resp.Author != nil && resp.Author.Login != "" {
		d.Author = resp.Author.Login
	}
	for _, f := range resp.Files {
		d.Files = append(d.Files, f.diff())
	}
	return d, nil
}

// maxDiffPages caps a PR's file list at 1,000 files (GitHub serves up to 3,000).
const maxDiffPages = 10

// PRDiff loads the patches of every file a PR changes, in GitHub's order.
func (c *Client) PRDiff(ctx context.Context, repo string, number int) ([]FileDiff, error) {
	var out []FileDiff
	for page := 1; page <= maxDiffPages; page++ {
		var files []restFile
		path := fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100&page=%d", repo, number, page)
		if err := c.rest.DoWithContext(ctx, "GET", path, nil, &files); err != nil {
			return nil, err
		}
		for _, f := range files {
			out = append(out, f.diff())
		}
		if len(files) < 100 {
			break
		}
	}
	return out, nil
}
