package demo

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/C-P-S-P/prdeck/internal/github"
)

// Sample data: fictional repos and people, the same ones the UI design uses.

type sample struct {
	pr        github.PR
	flags     []string
	commits   int
	files     int
	add, del  int
	labels    []string
	reviewers [][2]string
	checks    [][2]string
	body      []string
	timeline  []github.Event
}

func (b *Backend) ago(d time.Duration) time.Time { return b.start.Add(-d) }

func (b *Backend) load() {
	h, m, day := time.Hour, time.Minute, 24*time.Hour
	pass := func(names ...string) [][2]string {
		var out [][2]string
		for _, n := range names {
			out = append(out, [2]string{n, "SUCCESS"})
		}
		return out
	}
	pr := func(n int, repo, title, author, head string, upd time.Duration) github.PR {
		return github.PR{
			Number: n, Repo: repo, Title: title, Author: author, HeadRefName: head, BaseRefName: "main",
			URL: "https://github.com/" + repo + "/pull/" + strconv.Itoa(n), State: "OPEN",
			UpdatedAt: b.ago(upd), CreatedAt: b.ago(upd + 3*day), Mergeable: "MERGEABLE", CheckState: "SUCCESS",
		}
	}

	samples := []sample{
		{
			pr: with(pr(4821, "northwind/registry", "Add streaming push source to the registry", "you", "feat/stream-push-source", 18*m), func(p *github.PR) {
				p.CheckState, p.ReviewDecision, p.Mergeable, p.Comments = "FAILURE", "CHANGES_REQUESTED", "CONFLICTING", 12
			}),
			flags: []string{"authored"}, commits: 6, files: 14, add: 412, del: 38, labels: []string{"feature", "registry"},
			reviewers: [][2]string{{"mlin", "CHANGES_REQUESTED"}, {"rdavis", "COMMENTED"}, {"kopal", "PENDING"}},
			checks:    append(pass("lint", "unit", "type-check"), [2]string{"integration-redis", "FAILURE"}, [2]string{"codecov/patch", "FAILURE"}),
			body: []string{
				"## Summary",
				"Adds a `StreamPushSource` that accepts streaming writes and lands them in the online store without waiting for the next materialization run.",
				"",
				"> [!NOTE]",
				"> Needs registry migration `0042` to run first. cc @rdavis",
				"",
				"## Changes",
				"- New `StreamPushSource` type registered in the registry proto",
				"- `push()` validates the payload against the feature view schema",
				"- Online writes are batched every 200 ms (set with `push_batch_ms`)",
				"",
				"```python",
				"source = StreamPushSource(name=\"clicks\", schema=click_schema)",
				"store.push(\"clicks\", df, allow_registry_cache=False)  # lands in ~200 ms",
				"```",
				"",
				"## Results",
				`<img width="560" alt="p99 write latency before and after" src="` + latencyChart() + `">`,
				"",
				"| Online store | p99 before | p99 after |",
				"| --- | --- | --- |",
				"| Redis | 1.8 s | 210 ms |",
				"| DynamoDB | 2.1 s | 340 ms |",
				"",
				"## Test plan",
				"- [x] Unit tests for schema validation",
				"- [x] Integration test against the Redis online store",
				"- [ ] Load test at 5k events/s",
			},
			timeline: []github.Event{
				{Kind: "review", Author: "mlin", State: "CHANGES_REQUESTED", At: b.ago(2 * h), Body: "Looks close. One thing before this can land."},
				{Kind: "thread", Author: "mlin", At: b.ago(2 * h), Path: "sdk/python/registry/push_source.py", Line: 44,
					DiffHunk: "@@ -41,6 +41,9 @@ class StreamPushSource:\n+    def push(self, name, df, *, allow_registry_cache=True):\n+        fv = self._get_feature_view(name, allow_registry_cache)\n+        self._validate(fv, df)",
					Body:     "Should `allow_registry_cache` default to `False` here? A stale registry would silently drop new columns.",
					Replies:  []github.Reply{{Author: "you", At: b.ago(1 * h), Body: "Good catch. Switched the default and added a test for the stale-registry case."}}},
				{Kind: "comment", Author: "rdavis", At: b.ago(18 * m), Body: "Heads up @you: the Flight transport work on `main` changed the same writer, so expect a conflict in `online_writer.py` when you rebase."},
			},
		},
		{
			pr:    with(pr(4807, "northwind/registry", "Cache project metadata between materialization runs", "rdavis", "perf/registry-cache", 2*h), func(p *github.PR) { p.ReviewDecision, p.Comments = "REVIEW_REQUIRED", 4 }),
			flags: []string{"review"}, commits: 3, files: 5, add: 188, del: 21, labels: []string{"performance"},
			reviewers: [][2]string{{"mlin", "APPROVED"}, {"you", "PENDING"}},
			checks:    pass("lint", "unit", "type-check", "integration-redis"),
			body: []string{
				"## Why",
				"Each materialization run re-reads project metadata from the registry, about 1.8 s per run on large projects.",
				"",
				"## What",
				"- Cache `ProjectMetadata` in memory with a 60 s TTL",
				"- Invalidate on `apply()` and when the registry version changes",
				"",
				"## Benchmarks",
				"",
				"| Project | Before | After |",
				"|---|---:|---:|",
				"| 40 feature views | 0.3 s | 0.1 s |",
				"| 400 feature views | 1.8 s | 0.2 s |",
				"",
				"<details>",
				"<summary>How invalidation works</summary>",
				"",
				"The cache key includes the registry version, so an `apply()` from any client<br>bumps the version and the next run reloads.",
				"</details>",
				"",
				"<!-- Reviewers: please check the TTL default -->",
				"> [!WARNING]",
				"> Runs that call `apply()` mid-way still re-read metadata once.",
			},
			timeline: []github.Event{{Kind: "review", Author: "mlin", State: "APPROVED", At: b.ago(1 * h), Body: "Nice win. LGTM once CI is green."}},
		},
		{
			pr:    with(pr(4790, "northwind/registry", "Fix TTL handling for on-demand feature views", "kopal", "fix/odfv-ttl", 1*day), func(p *github.PR) { p.IsDraft, p.CheckState, p.Comments = true, "PENDING", 2 }),
			flags: []string{"mentioned"}, commits: 2, files: 3, add: 41, del: 9, labels: []string{"bug"},
			checks: [][2]string{{"lint", "SUCCESS"}, {"unit", "PENDING"}},
			body:   []string{"On-demand views ignore the `ttl` of their source views, so stale rows come back from the online store.", "", "Still a draft. @you, is \"no TTL means never expire\" still the rule?"},
		},
		{
			pr:    with(pr(312, "northwind/operator", "Reconcile registry secrets on rotation", "you", "fix/secret-rotation", 3*h), func(p *github.PR) { p.ReviewDecision, p.Comments = "APPROVED", 3 }),
			flags: []string{"authored"}, commits: 4, files: 6, add: 230, del: 17, labels: []string{"operator", "security"},
			reviewers: [][2]string{{"rdavis", "APPROVED"}, {"tnguyen", "APPROVED"}},
			checks:    pass("lint", "unit", "e2e-kind", "image-build"),
			body:      []string{"The operator now watches the registry `Secret` and restarts dependent deployments when its data changes."},
			timeline:  []github.Event{{Kind: "review", Author: "rdavis", State: "APPROVED", At: b.ago(3 * h), Body: "Clean. Matches what the console chart does."}},
		},
		{
			pr:    with(pr(305, "northwind/operator", "Bump controller-runtime to v0.19.1", "renovate", "renovate/controller-runtime", 2*day), func(p *github.PR) {}),
			flags: []string{"assigned"}, commits: 1, files: 2, add: 14, del: 14, labels: []string{"dependencies"},
			reviewers: [][2]string{{"you", "PENDING"}},
			checks:    pass("lint", "unit", "e2e-kind"),
			body:      []string{"Updates `sigs.k8s.io/controller-runtime` from `v0.18.4` to `v0.19.1`."},
		},
		{
			pr:    with(pr(1188, "northwind/console", "Paginate the feature view table", "tnguyen", "feat/fv-pagination", 5*h), func(p *github.PR) { p.ReviewDecision, p.Comments = "REVIEW_REQUIRED", 1 }),
			flags: []string{"review"}, commits: 2, files: 7, add: 156, del: 44, labels: []string{"ui"},
			reviewers: [][2]string{{"you", "PENDING"}},
			checks:    pass("lint", "unit", "e2e"),
			body:      []string{"The feature view table rendered every row at once and froze on projects with 2k+ views. It now pages on the server, 50 rows at a time."},
		},
		{
			pr:    with(pr(1179, "northwind/console", "Dark mode tokens for charts", "you", "feat/chart-dark-tokens", 4*day), func(p *github.PR) { p.IsDraft, p.CheckState = true, "PENDING" }),
			flags: []string{"authored"}, commits: 3, files: 9, add: 120, del: 64, labels: []string{"ui"},
			checks: [][2]string{{"lint", "SUCCESS"}, {"unit", "PENDING"}},
			body:   []string{"Moves chart colors into theme tokens so charts follow dark mode.", "", "- [x] Line and bar charts", "- [ ] Heatmap legend"},
		},
		{
			pr:    with(pr(77, "northwind/sdk-python", "Typed client for online features", "mlin", "feat/typed-client", 1*day), func(p *github.PR) { p.Comments = 6 }),
			flags: []string{"mentioned"}, commits: 8, files: 11, add: 640, del: 52, labels: []string{"sdk"},
			reviewers: [][2]string{{"you", "COMMENTED"}, {"kopal", "PENDING"}},
			checks:    pass("lint", "unit", "type-check"),
			body:      []string{"Generates a typed client from feature view schemas, so `get_online_features()` returns typed rows instead of dicts."},
		},
		{
			pr:    with(pr(2410, "upstream/datakit", "Support Arrow Flight transport", "you", "feat/flight-transport", 6*h), func(p *github.PR) { p.ReviewDecision, p.Comments = "REVIEW_REQUIRED", 2 }),
			flags: []string{"authored"}, commits: 11, files: 18, add: 1290, del: 210, labels: []string{"transport"},
			reviewers: [][2]string{{"jfoster", "PENDING"}},
			checks:    pass("lint", "unit", "integration", "docs"),
			body:      []string{"Adds Arrow Flight as an optional transport for bulk reads. Falls back to HTTP when the server does not advertise Flight."},
		},
		{
			pr:    with(pr(2398, "upstream/datakit", "Deprecate legacy CLI flags", "jfoster", "chore/deprecate-flags", 3*day), func(p *github.PR) { p.ReviewDecision, p.Comments = "APPROVED", 5 }),
			flags: []string{"commented"}, commits: 2, files: 4, add: 37, del: 6, labels: []string{"cli"},
			reviewers: [][2]string{{"you", "APPROVED"}},
			checks:    pass("lint", "unit", "integration"),
			body:      []string{"Prints a deprecation warning for `--legacy-auth` and `--no-verify`. Both go away in 3.0."},
		},
	}

	for _, s := range samples {
		b.prs = append(b.prs, s.pr)
		commits, files := sampleCommits(s, b.start), sampleFiles(s)
		b.flags[s.pr.Number] = s.flags
		for i := range s.timeline {
			if s.timeline[i].Kind == "thread" {
				s.timeline[i].ThreadID = fmt.Sprintf("demo-thread-%d-%d", s.pr.Number, i)
			}
		}
		d := &github.Detail{NodeID: fmt.Sprintf("demo-pr-%d", s.pr.Number), PR: s.pr, Body: strings.Join(s.body, "\n"), Commits: s.commits, ChangedFiles: s.files, Additions: s.add, Deletions: s.del, Labels: s.labels, Timeline: s.timeline, CommitList: commits, Files: files}
		for _, r := range s.reviewers {
			d.Reviewers = append(d.Reviewers, github.Reviewer{Login: r[0], State: r[1]})
		}
		// The checks are jobs of one GitHub Actions run, linked to their logs. A name
		// with a slash (codecov/patch) is a commit status from another service: it
		// links to that service and has no run to re-run.
		run := 11873400000 + int64(s.pr.Number)
		for i, c := range s.checks {
			ch := github.Check{Name: c[0], State: c[1]}
			if strings.Contains(c[0], "/") {
				ch.URL = "https://app.codecov.io/gh/" + s.pr.Repo + "/pull/" + strconv.Itoa(s.pr.Number)
			} else {
				ch.RunID = run
				ch.URL = fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", s.pr.Repo, run, 33093400000+int64(s.pr.Number)*10+int64(i))
			}
			d.Checks = append(d.Checks, ch)
		}
		b.details[s.pr.Number] = d
	}
}

// sampleCommits makes up a commit history for a sample PR, oldest first.
func sampleCommits(s sample, now time.Time) []github.Commit {
	headlines := map[int][]string{
		4821: {
			"Add StreamPushSource to the registry proto",
			"Validate pushed frames against the feature view schema",
			"Batch online writes every push_batch_ms",
			"Add Redis integration test for streaming pushes",
			"Default allow_registry_cache to False",
			"Test pushes against a stale registry",
		},
	}[s.pr.Number]
	var out []github.Commit
	for i := 0; i < s.commits; i++ {
		h := "Update " + s.pr.HeadRefName + " (" + strconv.Itoa(i+1) + "/" + strconv.Itoa(s.commits) + ")"
		if i < len(headlines) {
			h = headlines[i]
		}
		oid := fmt.Sprintf("%040x", uint64(s.pr.Number)*1_000_003+uint64(i)*7919)
		st := "SUCCESS"
		if i == s.commits-1 {
			st = s.pr.CheckState
		}
		out = append(out, github.Commit{
			OID: oid, Short: oid[len(oid)-7:], Headline: h, Author: s.pr.Author, CheckState: st,
			At:        now.Add(-time.Duration(s.commits-i) * 5 * time.Hour),
			Additions: s.add / max(s.commits, 1), Deletions: s.del / max(s.commits, 1),
		})
	}
	return out
}

// sampleFiles makes up the changed files for a sample PR.
func sampleFiles(s sample) []github.File {
	if s.pr.Number == 4821 {
		return []github.File{
			{Path: "protos/registry/push_source.proto", Additions: 34, ChangeType: "ADDED"},
			{Path: "sdk/python/registry/push_source.py", Additions: 168, Deletions: 4, ChangeType: "ADDED"},
			{Path: "sdk/python/registry/online_writer.py", Additions: 61, Deletions: 22, ChangeType: "MODIFIED"},
			{Path: "sdk/python/registry/schema.py", Additions: 27, Deletions: 6, ChangeType: "MODIFIED"},
			{Path: "sdk/python/tests/test_push_source.py", Additions: 96, ChangeType: "ADDED"},
			{Path: "sdk/python/tests/integration/test_redis_push.py", Additions: 26, Deletions: 6, ChangeType: "MODIFIED"},
		}
	}
	var out []github.File
	for i := 0; i < min(s.files, 6); i++ {
		out = append(out, github.File{Path: s.pr.Name() + "/src/file_" + strconv.Itoa(i+1) + ".go", Additions: s.add / max(s.files, 1), Deletions: s.del / max(s.files, 1), ChangeType: "MODIFIED"})
	}
	return out
}

// latencyChart is a small bar chart as a data URL, so the demo shows an image
// without going online.
func latencyChart() string {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="560" height="180" viewBox="0 0 560 180">` +
		`<rect width="560" height="180" fill="#16181c"/>` +
		`<text x="16" y="26" fill="#c9cdd4" font-family="Helvetica" font-size="13">p99 online write latency (ms)</text>` +
		`<text x="16" y="70" fill="#9aa1ad" font-family="Helvetica" font-size="12">Redis</text>` +
		`<rect x="96" y="52" width="420" height="14" fill="#e57b7b"/><text x="96" y="82" fill="#8a919d" font-family="Helvetica" font-size="11">before 1800</text>` +
		`<rect x="96" y="88" width="49" height="14" fill="#8cc47a"/><text x="152" y="100" fill="#8a919d" font-family="Helvetica" font-size="11">after 210</text>` +
		`<text x="16" y="136" fill="#9aa1ad" font-family="Helvetica" font-size="12">DynamoDB</text>` +
		`<rect x="96" y="118" width="440" height="14" fill="#e57b7b"/><rect x="96" y="140" width="71" height="14" fill="#8cc47a"/>` +
		`<text x="174" y="152" fill="#8a919d" font-family="Helvetica" font-size="11">after 340</text></svg>`
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

func with(p github.PR, f func(*github.PR)) github.PR {
	f(&p)
	return p
}
