package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Review events, as GitHub names them.
const (
	Approve        = "APPROVE"
	RequestChanges = "REQUEST_CHANGES"
	CommentReview  = "COMMENT"
)

// Comment adds a comment to a PR's conversation. prID is the PR's node ID.
func (c *Client) Comment(ctx context.Context, prID, body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("write a comment first")
	}
	const q = `mutation PRDeckComment($id: ID!, $body: String!) {
  addComment(input: {subjectId: $id, body: $body}) { clientMutationId }
}`
	var resp map[string]any
	return c.gql.DoWithContext(ctx, q, map[string]any{"id": prID, "body": body}, &resp)
}

// DraftThread is a comment on one line of the diff, sent with a review.
type DraftThread struct {
	Path string `json:"path"`
	Line int    `json:"line"` // in the new file (RIGHT) or the old one (LEFT)
	Side string `json:"side"` // RIGHT: added or unchanged lines; LEFT: removed lines
	Body string `json:"body"`
}

// Review submits a review with any line comments: Approve, RequestChanges (needs a
// body) or CommentReview (needs a body or a line comment).
func (c *Client) Review(ctx context.Context, prID, event, body string, threads []DraftThread) error {
	if err := CheckReview(event, body, threads); err != nil {
		return err
	}
	const q = `mutation PRDeckReview($id: ID!, $event: PullRequestReviewEvent!, $body: String, $threads: [DraftPullRequestReviewThread]) {
  addPullRequestReview(input: {pullRequestId: $id, event: $event, body: $body, threads: $threads}) { pullRequestReview { state } }
}`
	vars := map[string]any{"id": prID, "event": event, "body": nil, "threads": nil}
	if strings.TrimSpace(body) != "" {
		vars["body"] = body
	}
	if len(threads) > 0 {
		var ts []map[string]any
		for _, t := range threads {
			ts = append(ts, map[string]any{"path": t.Path, "line": t.Line, "side": t.Side, "body": t.Body})
		}
		vars["threads"] = ts
	}
	var resp map[string]any
	return c.gql.DoWithContext(ctx, q, vars, &resp)
}

// ReplyThread answers a review thread (a code comment). threadID is the thread's node ID.
func (c *Client) ReplyThread(ctx context.Context, threadID, body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("write a reply first")
	}
	const q = `mutation PRDeckReply($id: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $id, body: $body}) { comment { id } }
}`
	var resp map[string]any
	return c.gql.DoWithContext(ctx, q, map[string]any{"id": threadID, "body": body}, &resp)
}

// EnableAutoMerge turns on auto-merge: GitHub merges the PR with method (MERGE, SQUASH
// or REBASE) once its checks and reviews pass. GitHub refuses it for a PR that can be
// merged now, and in repos that do not allow auto-merge.
func (c *Client) EnableAutoMerge(ctx context.Context, prID, method string) error {
	const q = `mutation PRDeckAutoMerge($id: ID!, $method: PullRequestMergeMethod!) {
  enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: $method}) { clientMutationId }
}`
	var resp map[string]any
	return Refusal(c.gql.DoWithContext(ctx, q, map[string]any{"id": prID, "method": method}, &resp))
}

// Merge merges the PR now with method. head is the commit that was checked; GitHub
// refuses the merge if the branch moved on since ("" merges whatever is there).
func (c *Client) Merge(ctx context.Context, prID, method, head string) error {
	const q = `mutation PRDeckMerge($id: ID!, $method: PullRequestMergeMethod!, $head: GitObjectID) {
  mergePullRequest(input: {pullRequestId: $id, mergeMethod: $method, expectedHeadOid: $head}) { clientMutationId }
}`
	vars := map[string]any{"id": prID, "method": method, "head": nil}
	if head != "" {
		vars["head"] = head
	}
	var resp map[string]any
	return Refusal(c.gql.DoWithContext(ctx, q, vars, &resp))
}

// Refusal is GitHub's own words for why it refused a write: the messages of a GraphQL
// error, without go-gh's "GraphQL:" prefix and field paths. Other errors stay as they are.
func Refusal(err error) error {
	var ge *api.GraphQLError
	if !errors.As(err, &ge) || len(ge.Errors) == 0 {
		return err
	}
	var msgs []string
	for _, e := range ge.Errors {
		msgs = append(msgs, e.Message)
	}
	return errors.New(strings.Join(msgs, "; "))
}

// RerunFailedJobs re-runs the failed jobs of one GitHub Actions run, and the jobs
// that depend on them. GraphQL has no mutation for this, so it goes through REST.
func (c *Client) RerunFailedJobs(ctx context.Context, repo string, runID int64) error {
	if !strings.Contains(repo, "/") || runID <= 0 {
		return fmt.Errorf("bad run %s %d", repo, runID)
	}
	resp, err := c.rest.RequestWithContext(ctx, "POST", fmt.Sprintf("repos/%s/actions/runs/%d/rerun-failed-jobs", repo, runID), nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// CheckReview reports what is wrong with a review before it is sent.
func CheckReview(event, body string, threads []DraftThread) error {
	for _, t := range threads {
		if t.Path == "" || t.Line < 1 || (t.Side != "LEFT" && t.Side != "RIGHT") {
			return fmt.Errorf("a line comment needs a file, a line and a side (got %q line %d %q)", t.Path, t.Line, t.Side)
		}
		if strings.TrimSpace(t.Body) == "" {
			return fmt.Errorf("the comment on %s:%d is empty", t.Path, t.Line)
		}
	}
	empty := strings.TrimSpace(body) == ""
	switch event {
	case Approve:
		return nil
	case RequestChanges:
		if empty {
			return errors.New("say what should change before requesting changes")
		}
		return nil
	case CommentReview:
		if empty && len(threads) == 0 {
			return errors.New("write a comment first")
		}
		return nil
	}
	return fmt.Errorf("unknown review event %q", event)
}
