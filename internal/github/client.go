package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	ghconfig "github.com/cli/go-gh/v2/pkg/config"
)

// Client runs PR Deck's GraphQL queries. Auth comes from gh (`gh auth login`) or GH_TOKEN.
type Client struct {
	gql  *api.GraphQLClient
	rest *api.RESTClient // diffs: GraphQL has no patches

	imgMu sync.Mutex
	img   *http.Client // images on GitHub's hosts, with the login; made on first use
}

func NewClient() (*Client, error) {
	// go-gh reads gh's config once per process; read it again so a login made
	// since then (Connect after sign-in) is the one used.
	_ = ghconfig.Reload(nil)
	opts := api.ClientOptions{Timeout: 30 * time.Second}
	gql, err := api.NewGraphQLClient(opts)
	if err != nil {
		return nil, fmt.Errorf("GitHub login not found; run `gh auth login` first (%w)", err)
	}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, err
	}
	return &Client{gql: gql, rest: rest}, nil
}

// Login is the signed-in user's GitHub login.
func (c *Client) Login(ctx context.Context) (string, error) {
	var resp struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if err := c.gql.DoWithContext(ctx, "query PRDeckViewer { viewer { login } }", nil, &resp); err != nil {
		return "", err
	}
	return resp.Viewer.Login, nil
}

// SearchQuery turns a filter like "involves:@me" into a full PR search.
func SearchQuery(filter string) string {
	q := strings.TrimSpace(filter)
	if !strings.Contains(q, "is:pr") && !strings.Contains(q, "type:pr") {
		q = "is:pr " + q
	}
	if !strings.Contains(q, "is:open") && !strings.Contains(q, "is:closed") && !strings.Contains(q, "is:merged") && !strings.Contains(q, "state:") {
		q = "is:open " + q
	}
	if !strings.Contains(q, "archived:") {
		q += " archived:false"
	}
	if !strings.Contains(q, "sort:") {
		q += " sort:updated-desc"
	}
	return q
}

const searchQuery = `query PRDeckSearch($q: String!, $n: Int!) {
  viewer { login }
  search(query: $q, type: ISSUE, first: $n) {
    nodes {
      ... on PullRequest {
        number title url isDraft state updatedAt createdAt
        headRefName baseRefName isCrossRepository mergeable reviewDecision
        author { login }
        repository { nameWithOwner }
        comments { totalCount }
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
      }
    }
  }
}`

type login struct {
	Login string `json:"login"`
}

type prNode struct {
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
	Mergeable      string    `json:"mergeable"`
	ReviewDecision string    `json:"reviewDecision"`
	Author         *login    `json:"author"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (n prNode) toPR() PR {
	p := PR{
		Number: n.Number, Title: n.Title, URL: n.URL, IsDraft: n.IsDraft, State: n.State,
		UpdatedAt: n.UpdatedAt, CreatedAt: n.CreatedAt, HeadRefName: n.HeadRefName, BaseRefName: n.BaseRefName,
		IsCrossRepo: n.IsCrossRepo, Mergeable: n.Mergeable, ReviewDecision: n.ReviewDecision,
		Author: loginOf(n.Author), Repo: n.Repository.NameWithOwner, Comments: n.Comments.TotalCount,
	}
	if len(n.Commits.Nodes) > 0 && n.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
		p.CheckState = n.Commits.Nodes[0].Commit.StatusCheckRollup.State
	}
	return p
}

func loginOf(l *login) string {
	if l == nil || l.Login == "" {
		return "ghost"
	}
	return l.Login
}

// Search returns open PRs for a filter, newest first, plus the signed-in user's login.
func (c *Client) Search(ctx context.Context, filter string, limit int) ([]PR, string, error) {
	var resp struct {
		Viewer login `json:"viewer"`
		Search struct {
			Nodes []prNode `json:"nodes"`
		} `json:"search"`
	}
	vars := map[string]any{"q": SearchQuery(filter), "n": limit}
	err := c.gql.DoWithContext(ctx, searchQuery, vars, &resp)
	if err != nil {
		// Orgs with SAML or OAuth app limits the login is not cleared for come back
		// as null nodes with FORBIDDEN errors; show the rest, as gh does.
		var ge *api.GraphQLError
		if !errors.As(err, &ge) || !ge.Match("FORBIDDEN", "search.nodes.") {
			return nil, "", err
		}
	}
	out := make([]PR, 0, len(resp.Search.Nodes))
	for _, n := range resp.Search.Nodes {
		if n.Number == 0 { // a non-PR result
			continue
		}
		out = append(out, n.toPR())
	}
	if err != nil && len(out) == 0 {
		return nil, "", err // none of them can be shown, so say why
	}
	return out, resp.Viewer.Login, nil
}

const detailQuery = `query PRDeckDetail($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      id number title url body isDraft state updatedAt createdAt
      headRefName baseRefName isCrossRepository mergeable reviewDecision
      additions deletions changedFiles
      author { login }
      repository { nameWithOwner }
      comments(last: 50) { totalCount nodes { author { login } body createdAt } }
      commitCount: commits { totalCount }
      commits(last: 1) { nodes { commit { statusCheckRollup { state contexts(first: 60) { nodes {
        __typename
        ... on CheckRun { name status conclusion }
        ... on StatusContext { context state }
      } } } } } }
      commitList: commits(last: 100) { nodes { commit {
        oid abbreviatedOid messageHeadline messageBody committedDate additions deletions
        authors(first: 1) { nodes { name user { login } } }
        statusCheckRollup { state }
      } } }
      files(first: 100) { nodes { path additions deletions changeType } }
      labels(first: 20) { nodes { name } }
      reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } ... on Team { name } } } }
      latestReviews(first: 20) { nodes { author { login } state } }
      reviews(last: 50) { nodes { author { login } state body submittedAt } }
      reviewThreads(last: 50) { nodes {
        id isResolved isOutdated path line diffSide startLine startDiffSide
        comments(first: 30) { nodes { author { login } body createdAt diffHunk } }
      } }
    }
  }
}`

type detailNode struct {
	prNode
	ID           string `json:"id"`
	Body         string `json:"body"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changedFiles"`
	Comments     struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Author    *login    `json:"author"`
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"createdAt"`
		} `json:"nodes"`
	} `json:"comments"`
	CommitCount struct {
		TotalCount int `json:"totalCount"`
	} `json:"commitCount"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []struct {
							Typename   string `json:"__typename"`
							Name       string `json:"name"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							Context    string `json:"context"`
							State      string `json:"state"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	CommitList struct {
		Nodes []struct {
			Commit struct {
				OID            string    `json:"oid"`
				AbbreviatedOID string    `json:"abbreviatedOid"`
				Headline       string    `json:"messageHeadline"`
				Body           string    `json:"messageBody"`
				CommittedDate  time.Time `json:"committedDate"`
				Additions      int       `json:"additions"`
				Deletions      int       `json:"deletions"`
				Authors        struct {
					Nodes []struct {
						Name string `json:"name"`
						User *login `json:"user"`
					} `json:"nodes"`
				} `json:"authors"`
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commitList"`
	Files struct {
		Nodes []File `json:"nodes"`
	} `json:"files"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer *struct {
				Login string `json:"login"`
				Name  string `json:"name"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []struct {
			Author *login `json:"author"`
			State  string `json:"state"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	Reviews struct {
		Nodes []struct {
			Author      *login    `json:"author"`
			State       string    `json:"state"`
			Body        string    `json:"body"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"reviews"`
	ReviewThreads struct {
		Nodes []struct {
			ID         string `json:"id"`
			DiffSide   string `json:"diffSide"`
			StartLine  int    `json:"startLine"`
			StartSide  string `json:"startDiffSide"`
			IsResolved bool   `json:"isResolved"`
			IsOutdated bool   `json:"isOutdated"`
			Path       string `json:"path"`
			Line       int    `json:"line"`
			Comments   struct {
				Nodes []struct {
					Author    *login    `json:"author"`
					Body      string    `json:"body"`
					CreatedAt time.Time `json:"createdAt"`
					DiffHunk  string    `json:"diffHunk"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
}

// Detail loads one PR with its conversation, reviewers and checks.
func (c *Client) Detail(ctx context.Context, repo string, number int) (*Detail, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("bad repo %q", repo)
	}
	var resp struct {
		Repository struct {
			PullRequest *detailNode `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := c.gql.DoWithContext(ctx, detailQuery, vars, &resp); err != nil {
		return nil, err
	}
	n := resp.Repository.PullRequest
	if n == nil {
		return nil, fmt.Errorf("%s#%d not found", repo, number)
	}
	return n.toDetail(), nil
}

func (n *detailNode) toDetail() *Detail {
	// prNode's comments/commits fields are shadowed by the richer ones above; fill them by hand.
	d := &Detail{PR: n.prNode.toPR(), NodeID: n.ID}
	d.Comments = n.Comments.TotalCount
	d.Body, d.Additions, d.Deletions, d.ChangedFiles = n.Body, n.Additions, n.Deletions, n.ChangedFiles
	d.Commits = n.CommitCount.TotalCount
	for _, l := range n.Labels.Nodes {
		d.Labels = append(d.Labels, l.Name)
	}
	for _, c := range n.CommitList.Nodes {
		cm := Commit{
			OID: c.Commit.OID, Short: c.Commit.AbbreviatedOID, Headline: c.Commit.Headline, Body: c.Commit.Body,
			At: c.Commit.CommittedDate, Additions: c.Commit.Additions, Deletions: c.Commit.Deletions,
		}
		if a := c.Commit.Authors.Nodes; len(a) > 0 {
			cm.Author = a[0].Name
			if a[0].User != nil && a[0].User.Login != "" {
				cm.Author = a[0].User.Login
			}
		}
		if c.Commit.StatusCheckRollup != nil {
			cm.CheckState = c.Commit.StatusCheckRollup.State
		}
		d.CommitList = append(d.CommitList, cm)
	}
	d.Files = n.Files.Nodes

	if len(n.Commits.Nodes) > 0 {
		if r := n.Commits.Nodes[0].Commit.StatusCheckRollup; r != nil {
			d.CheckState = r.State
			for _, c := range r.Contexts.Nodes {
				if c.Typename == "CheckRun" {
					st := c.Conclusion
					if st == "" {
						st = "PENDING"
					}
					d.Checks = append(d.Checks, Check{Name: c.Name, State: st})
				} else {
					d.Checks = append(d.Checks, Check{Name: c.Context, State: c.State})
				}
			}
		}
	}

	seen := map[string]bool{}
	for _, r := range n.LatestReviews.Nodes {
		who := loginOf(r.Author)
		seen[who] = true
		d.Reviewers = append(d.Reviewers, Reviewer{Login: who, State: r.State})
	}
	for _, rr := range n.ReviewRequests.Nodes {
		if rr.RequestedReviewer == nil {
			continue
		}
		who := rr.RequestedReviewer.Login
		if who == "" {
			who = rr.RequestedReviewer.Name
		}
		if who != "" && !seen[who] {
			d.Reviewers = append(d.Reviewers, Reviewer{Login: who, State: "PENDING"})
		}
	}

	for _, c := range n.Comments.Nodes {
		d.Timeline = append(d.Timeline, Event{Kind: "comment", Author: loginOf(c.Author), Body: c.Body, At: c.CreatedAt})
	}
	for _, r := range n.Reviews.Nodes {
		// A COMMENTED review with no body only wraps code comments, which arrive as threads.
		if r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "" || r.State == "PENDING" {
			continue
		}
		d.Timeline = append(d.Timeline, Event{Kind: "review", Author: loginOf(r.Author), State: r.State, Body: r.Body, At: r.SubmittedAt})
	}
	for _, t := range n.ReviewThreads.Nodes {
		if len(t.Comments.Nodes) == 0 {
			continue
		}
		first := t.Comments.Nodes[0]
		ev := Event{
			Kind: "thread", Author: loginOf(first.Author), Body: first.Body, At: first.CreatedAt,
			Path: t.Path, Line: t.Line, DiffHunk: first.DiffHunk, Resolved: t.IsResolved, Outdated: t.IsOutdated, ThreadID: t.ID, Side: t.DiffSide, StartLine: t.StartLine, StartSide: t.StartSide,
		}
		for _, c := range t.Comments.Nodes[1:] {
			ev.Replies = append(ev.Replies, Reply{Author: loginOf(c.Author), Body: c.Body, At: c.CreatedAt})
		}
		d.Timeline = append(d.Timeline, ev)
	}
	sortEvents(d.Timeline)
	return d
}
