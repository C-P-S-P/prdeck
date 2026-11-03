package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/MasterPushers/prdeck/internal/config"
	"github.com/MasterPushers/prdeck/internal/github"
	"github.com/MasterPushers/prdeck/internal/runner"
)

// Plan is a command ready to show and run.
type Plan struct {
	Title    string    `json:"title"`
	Pre      string    `json:"pre"`    // runs first: stash/drop changes, check out the PR branch
	Script   string    `json:"script"` // the main command
	Action   string    `json:"action"` // "", "open", "browse", "rerun", "clone", or a GitHub write (see Writes)
	Input    string    `json:"input"`  // the text a GitHub write sends: a comment, what should change
	Folder   Folder    `json:"folder"`
	Confirm  bool      `json:"confirm"`
	Terminal bool      `json:"terminal"` // hand the terminal to the main command
	Silent   bool      `json:"silent"`   // don't show output unless it fails
	Changes  []string  `json:"changes"`  // uncommitted changes that must be stashed or dropped first
	PR       github.PR `json:"pr"`
}

// Result is what running a Plan produced.
type Result struct {
	Plan   Plan
	Output string
	Err    error
}

// FullScript is every shell command the plan runs, in order.
func (p Plan) FullScript() string {
	switch {
	case p.Pre == "":
		return p.Script
	case p.Script == "":
		return p.Pre
	}
	return p.Pre + " && " + p.Script
}

// Dir is where the plan runs.
func (p Plan) Dir() string {
	if p.Action == "clone" || p.Action == "browse" {
		home, _ := os.UserHomeDir()
		return home
	}
	return p.Folder.Path
}

// WithStash stashes the uncommitted changes (untracked files too) before the plan.
func (p Plan) WithStash() Plan {
	msg := fmt.Sprintf("prdeck: before %s on #%d", strings.ToLower(p.Title), p.PR.Number)
	return p.withPre("git stash push --include-untracked -m " + runner.Quote(msg))
}

// WithDrop throws the uncommitted changes away before the plan. Ignored files stay.
func (p Plan) WithDrop() Plan {
	return p.withPre("git reset --hard --quiet && git clean -fd --quiet")
}

func (p Plan) withPre(cmd string) Plan {
	if p.Pre != "" {
		cmd += " && " + p.Pre
	}
	p.Pre, p.Changes = cmd, nil
	return p
}

// Allowed reports whether a command's `when:` conditions match the PR.
func Allowed(c config.Command, pr github.PR, viewer string) (bool, string) {
	for _, w := range c.When {
		ok := true
		switch w {
		case "authored":
			ok = viewer != "" && strings.EqualFold(pr.Author, viewer)
		case "others":
			ok = viewer == "" || !strings.EqualFold(pr.Author, viewer)
		case "draft":
			ok = pr.IsDraft
		case "ready":
			ok = !pr.IsDraft
		case "ci_failed":
			ok = pr.CIFailed()
		case "conflict":
			ok = pr.Mergeable == "CONFLICTING"
		}
		if !ok {
			what := "PRs that are " + strings.ReplaceAll(w, "_", " ")
			switch w {
			case "authored":
				what = "your own PRs"
			case "others":
				what = "other people's PRs"
			case "ready":
				what = "PRs that are ready for review"
			case "ci_failed":
				what = "PRs whose CI failed"
			}
			return false, fmt.Sprintf("%s only runs on %s", c.Name, what)
		}
	}
	return true, ""
}

// Prepare turns a command into a Plan for pr. input fills {input} for commands with a prompt.
func (s *Service) Prepare(c config.Command, pr github.PR, input string) (Plan, error) {
	if ok, why := Allowed(c, pr, s.Viewer()); !ok {
		return Plan{}, errors.New(why)
	}
	if c.Action == "browse" {
		return Plan{Title: c.Name, Action: "browse", Script: openURLCommand() + " " + runner.Quote(pr.URL), PR: pr}, nil
	}
	if c.Action == "rerun" {
		d, _ := s.cache.Detail(pr)
		return RerunPlan(c, pr, d), nil
	}
	if Writes(c.Action) {
		d, _ := s.cache.Detail(pr)
		return WritePlan(c, pr, d, input), nil
	}
	f, err := s.ResolveFolder(pr, c.Worktree)
	if err != nil {
		return Plan{}, err
	}
	return BuildPlan(s.cfg, c, pr, f, input)
}

// BuildPlan is Prepare once the folder is known. It decides whether the PR's branch
// must be checked out first and whether uncommitted changes are in the way.
func BuildPlan(cfg *config.Config, c config.Command, pr github.PR, f Folder, input string) (Plan, error) {
	p := Plan{Title: c.Name, Action: c.Action, Confirm: c.Confirm, Terminal: c.Output == "terminal", Silent: c.Output == "silent", PR: pr, Folder: f}
	vals := values(pr, f, input)

	switch c.Action {
	case "open":
		ed, ok := cfg.Editor.Editors[cfg.Editor.Default]
		if !ok {
			return Plan{}, fmt.Errorf("editor %q is not set up in config", cfg.Editor.Default)
		}
		p.Title = "Open in " + cfg.Editor.Default
		if c.Worktree {
			p.Title += " (worktree)"
		}
		p.Script = runner.Fill(ed.Run, vals)
		p.Terminal = ed.Terminal
	case "":
		p.Script = runner.Fill(c.Run, vals)
	default:
		return Plan{}, fmt.Errorf("unknown action %q", c.Action)
	}

	// A new worktree is created on the PR's branch, so it starts clean and switched.
	if !f.Exists || f.Rebasing {
		return p, nil
	}
	switchBranch := c.Branch && f.Branch != pr.HeadRefName
	if switchBranch {
		p.Pre = "gh pr checkout " + runner.Quote(pr.URL)
	}
	if (switchBranch || c.Clean) && len(f.Changes) > 0 {
		p.Changes = f.Changes
	}
	if c.Branch && c.Run == "" && c.Action == "" && !switchBranch {
		// Checkout on the branch already: bring it up to date instead.
		p.Script = "gh pr checkout " + runner.Quote(pr.URL)
	}
	return p, nil
}

// RerunPlan re-runs the failed jobs of a PR's GitHub Actions runs. It needs no clone
// and no shell: Execute asks GitHub for the PR's checks again and re-runs each failed
// run with the gh login. Script shows the gh commands that do the same, for the
// failed runs in d (the checks on screen; nil when they are not loaded).
func RerunPlan(c config.Command, pr github.PR, d *github.Detail) Plan {
	p := Plan{Title: c.Name, Action: "rerun", Confirm: c.Confirm, Silent: c.Output == "silent", PR: pr}
	if d != nil {
		runs, _ := d.FailedRuns()
		var cmds []string
		for _, r := range runs {
			cmds = append(cmds, fmt.Sprintf("gh run rerun %d --failed -R %s", r.ID, runner.Quote(pr.Repo)))
		}
		p.Script = strings.Join(cmds, " && ")
	}
	return p
}

// RerunFailed re-runs the failed jobs of each failed GitHub Actions run in d with
// rerun, and says what it did. Failed checks from other CI services and commit
// statuses cannot be re-run this way; they are named as skipped.
func RerunFailed(p Plan, d *github.Detail, rerun func(runID int64) error) Result {
	runs, other := d.FailedRuns()
	var log, failed []string
	for _, r := range runs {
		what := fmt.Sprintf("run %d: %s", r.ID, someOf(r.Checks))
		if err := rerun(r.ID); err != nil {
			failed = append(failed, what+" ("+err.Error()+")")
			continue
		}
		log = append(log, "Re-running the failed jobs of "+what)
	}
	switch len(other) {
	case 0:
	case 1:
		log = append(log, "Skipped "+other[0]+": not a GitHub Actions job; re-run it from its own page")
	default:
		log = append(log, "Skipped "+someOf(other)+": not GitHub Actions jobs; re-run them from their own pages")
	}
	var err error
	switch {
	case len(runs) == 0:
		err = errors.New("no failed GitHub Actions runs to re-run")
	case len(failed) > 0:
		err = errors.New("could not re-run " + strings.Join(failed, "; "))
	}
	return Result{Plan: p, Output: strings.Join(log, "\n"), Err: err}
}

// someOf names the first few checks: a matrix can fail dozens of jobs in one run.
func someOf(names []string) string {
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
	}
	return strings.Join(names, ", ")
}

// Writes reports the built-in actions that change the PR on GitHub with the gh login:
// approve, request_changes, comment (they send Plan.Input) and automerge. Like
// Re-run failed they need no clone and no shell.
func Writes(action string) bool {
	switch action {
	case "approve", "request_changes", "comment", "automerge":
		return true
	}
	return false
}

// WritePlan is a command that changes the PR on GitHub; input is the text it sends.
// Script shows the gh command that does the same, so `y` copies something that works;
// for Merge when ready it follows the PR as last loaded (d; nil when not loaded yet).
func WritePlan(c config.Command, pr github.PR, d *github.Detail, input string) Plan {
	p := Plan{Title: c.Name, Action: c.Action, Input: input, Confirm: c.Confirm, Silent: c.Output == "silent", PR: pr}
	n, repo, body := strconv.Itoa(pr.Number), " -R "+runner.Quote(pr.Repo), ""
	if input != "" {
		body = " --body " + runner.Quote(input)
	}
	switch c.Action {
	case "approve":
		p.Script = "gh pr review " + n + " --approve" + body + repo
	case "request_changes":
		p.Script = "gh pr review " + n + " --request-changes" + body + repo
	case "comment":
		p.Script = "gh pr comment " + n + body + repo
	case "automerge":
		how := " --auto"
		if d != nil && readyNow(d.MergeStatus) {
			how = ""
		}
		if d != nil && len(d.MergeMethods) > 0 {
			how += " --" + strings.ToLower(d.MergeMethods[0])
		}
		p.Script = "gh pr merge " + n + how + repo
	}
	return p
}

// Reviewer comments on and reviews PRs: the Service, or the demo's sample PRs.
type Reviewer interface {
	Comment(ctx context.Context, prID, body string) error
	Review(ctx context.Context, prID, event, body string, threads []github.DraftThread) error
}

// PostReview sends what a review command's plan asks for (approve, request changes
// or comment on the PR whose node ID is prID) and says what it did. It is the same
// write the desktop's review box makes.
func PostReview(ctx context.Context, p Plan, prID string, r Reviewer) Result {
	var err error
	did := "Commented on"
	switch p.Action {
	case "approve":
		did, err = "Approved", r.Review(ctx, prID, github.Approve, p.Input, nil)
	case "request_changes":
		did, err = "Requested changes on", r.Review(ctx, prID, github.RequestChanges, p.Input, nil)
	default:
		err = r.Comment(ctx, prID, p.Input)
	}
	if err != nil {
		return Result{Plan: p, Err: github.Refusal(err)}
	}
	return Result{Plan: p, Output: fmt.Sprintf("%s %s#%d", did, p.PR.Repo, p.PR.Number)}
}

// readyNow reports the merge states in which GitHub would merge a PR at once (all
// checks and reviews passed), and so refuses to turn on auto-merge for it.
func readyNow(status string) bool { return status == "CLEAN" || status == "HAS_HOOKS" }

// MergeWhenReady turns on auto-merge, so GitHub merges the PR once its checks and
// reviews pass, or merges it now when nothing is left to wait for. d is the PR as
// GitHub sees it now. enable and merge take the merge method: the viewer's default
// from the repo's allowed ones, as on GitHub's merge button.
func MergeWhenReady(p Plan, d *github.Detail, enable func(method string) error, merge func(method, head string) error) Result {
	ref := fmt.Sprintf("%s#%d", d.Repo, d.Number)
	refuse := func(why string) Result { return Result{Plan: p, Err: errors.New(why)} }
	switch {
	case d.State == "MERGED":
		return refuse(ref + " is already merged")
	case d.State == "CLOSED":
		return refuse(ref + " is closed")
	case d.AutoMerge != nil:
		return Result{Plan: p, Output: fmt.Sprintf("Auto-merge is already on for %s (%s, turned on by %s)", ref, MergeWord(d.AutoMerge.Method), d.AutoMerge.By)}
	case len(d.MergeMethods) == 0:
		return refuse(d.Repo + " allows no merge method")
	}
	method := d.MergeMethods[0]
	switch {
	case readyNow(d.MergeStatus):
		// Merge the commit whose checks passed, not one pushed since.
		head := ""
		if n := len(d.CommitList); n > 0 {
			head = d.CommitList[n-1].OID
		}
		if err := merge(method, head); err != nil {
			return refuse("GitHub did not merge " + ref + ": " + err.Error())
		}
		return Result{Plan: p, Output: fmt.Sprintf("Merged %s (%s): it was ready, so there was nothing to wait for", ref, MergeWord(method))}
	case d.MergeStatus == "UNSTABLE":
		// GitHub would merge now, and refuses auto-merge, though checks are not green.
		return refuse("checks on " + ref + " are failing or still running, and none of them is required, so GitHub cannot wait for them. Try again once they pass")
	case d.MergeStatus == "DIRTY":
		// Auto-merge could never finish; say what to do in plain words.
		return refuse(ref + " has conflicts with " + d.BaseRefName + ": rebase it first")
	case d.MergeStatus == "DRAFT":
		return refuse(ref + " is a draft: mark it ready for review first")
	case !d.AutoMergeAllowed:
		return refuse(d.Repo + " does not allow auto-merge (an admin can turn on \"Allow auto-merge\" in its settings). Merge it on GitHub once it is ready")
	}
	if err := enable(method); err != nil {
		return refuse("GitHub did not turn on auto-merge for " + ref + ": " + err.Error())
	}
	return Result{Plan: p, Output: fmt.Sprintf("Auto-merge is on: GitHub will %s %s once checks and reviews pass", MergeWord(method), ref)}
}

// MergeWord names a merge method the way GitHub's merge button does.
func MergeWord(method string) string {
	switch method {
	case "SQUASH":
		return "squash and merge"
	case "REBASE":
		return "rebase and merge"
	}
	return "merge"
}

// ClonePlan offers to clone a repo that has no local clone yet.
func (s *Service) ClonePlan(pr github.PR) Plan {
	dir := "~"
	if len(s.cfg.Repos.Scan) > 0 {
		dir = s.cfg.Repos.Scan[0]
	}
	target := filepath.Join(config.ExpandHome(dir), pr.Name())
	return Plan{
		Title:   "Clone " + pr.Repo,
		Action:  "clone",
		Script:  "gh repo clone " + runner.Quote(pr.Repo) + " " + runner.Quote(target),
		Confirm: true,
		PR:      pr,
	}
}

func values(pr github.PR, f Folder, input string) map[string]string {
	remote := f.Remote
	if remote == "" {
		remote = "origin"
	}
	return map[string]string{
		"number": strconv.Itoa(pr.Number),
		"branch": pr.HeadRefName,
		"base":   pr.BaseRefName,
		"repo":   pr.Repo,
		"owner":  pr.Owner(),
		"name":   pr.Name(),
		"title":  pr.Title,
		"author": pr.Author,
		"url":    pr.URL,
		"remote": remote,
		"path":   f.Path,
		"input":  input,
	}
}

func openURLCommand() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}
