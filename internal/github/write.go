package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// Review submits a review: Approve (the body is optional), RequestChanges or
// CommentReview (both need a body).
func (c *Client) Review(ctx context.Context, prID, event, body string) error {
	if err := CheckReview(event, body); err != nil {
		return err
	}
	const q = `mutation PRDeckReview($id: ID!, $event: PullRequestReviewEvent!, $body: String) {
  addPullRequestReview(input: {pullRequestId: $id, event: $event, body: $body}) { pullRequestReview { state } }
}`
	vars := map[string]any{"id": prID, "event": event, "body": nil}
	if strings.TrimSpace(body) != "" {
		vars["body"] = body
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

// CheckReview reports what is wrong with a review before it is sent.
func CheckReview(event, body string) error {
	switch event {
	case Approve:
		return nil
	case RequestChanges, CommentReview:
		if strings.TrimSpace(body) == "" {
			return errors.New("say what should change before requesting changes")
		}
		return nil
	}
	return fmt.Errorf("unknown review event %q", event)
}
