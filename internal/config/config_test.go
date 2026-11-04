package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func find(cmds []Command, name string) (Command, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

func TestMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != "" || len(cfg.Commands) != len(DefaultCommands()) || len(cfg.Warnings) != 0 {
		t.Fatalf("unexpected defaults: path=%q cmds=%d warnings=%v", cfg.Path, len(cfg.Commands), cfg.Warnings)
	}
}

func TestEmptyFile(t *testing.T) {
	if _, err := Load(write(t, "")); err != nil {
		t.Fatal(err)
	}
}

func TestMerge(t *testing.T) {
	cfg, err := Load(write(t, `
github:
  refresh: 5m
editor:
  default: nvim
  worktree: false
  editors:
    helix: hx {path}
filters:
  - name: Mentions
    hidden: true
  - name: Waiting on me
    query: review-requested:@me -is:draft
commands:
  - name: Push
    key: P
  - name: Abort
    hidden: true
  - name: Run tests
    key: t
    run: make test
    when: authored
`))
	if err != nil {
		t.Fatal(err)
	}
	if time.Duration(cfg.GitHub.Refresh) != 5*time.Minute {
		t.Errorf("refresh = %v", time.Duration(cfg.GitHub.Refresh))
	}
	if cfg.Editor.Default != "nvim" || cfg.Editor.UseWorktree() {
		t.Errorf("editor = %+v", cfg.Editor)
	}
	if cfg.Editor.Editors["helix"].Run != "hx {path}" || !cfg.Editor.Editors["nvim"].Terminal {
		t.Errorf("editors = %+v", cfg.Editor.Editors)
	}
	for _, f := range cfg.Filters {
		if f.Name == "Mentions" {
			t.Error("hidden filter still present")
		}
	}
	if cfg.Filters[len(cfg.Filters)-1].Name != "Waiting on me" {
		t.Error("new filter not appended")
	}
	push, _ := find(cfg.Commands, "Push")
	if push.Key != "P" || !strings.Contains(push.Run, "--force-with-lease") || !push.OnBar() {
		t.Errorf("push override lost built-in fields: %+v", push)
	}
	if _, ok := find(cfg.Commands, "Abort"); ok {
		t.Error("hidden command still present")
	}
	tests, ok := find(cfg.Commands, "Run tests")
	if !ok || tests.Run != "make test" || len(tests.When) != 1 || tests.When[0] != "authored" || tests.OnBar() {
		t.Errorf("custom command = %+v", tests)
	}
}

func TestRerunFailedOverrides(t *testing.T) {
	def, _ := find(DefaultCommands(), "Re-run failed")
	if def.Key == "" || Reserved[def.Key] || def.Action != "rerun" || len(def.When) != 1 || def.When[0] != "ci_failed" {
		t.Fatalf("built-in = %+v", def)
	}
	for _, c := range DefaultCommands() {
		if c.Name != def.Name && c.Key == def.Key {
			t.Errorf("%q shares key %q", c.Name, c.Key)
		}
	}
	cfg, err := Load(write(t, "commands:\n  - name: Re-run failed\n    key: x\n"))
	if err != nil {
		t.Fatal(err)
	}
	moved, _ := find(cfg.Commands, "Re-run failed")
	if moved.Key != "x" || moved.Action != "rerun" || len(moved.When) != 1 || len(cfg.Warnings) != 0 {
		t.Errorf("moved key lost the built-in: %+v %v", moved, cfg.Warnings)
	}
	cfg, _ = Load(write(t, "commands:\n  - name: Re-run failed\n    hidden: true\n"))
	if _, ok := find(cfg.Commands, "Re-run failed"); ok {
		t.Error("hidden Re-run failed still present")
	}
}

func TestReviewAndMergeBuiltIns(t *testing.T) {
	keys := map[string]string{}
	for _, c := range DefaultCommands() {
		if other, ok := keys[c.Key]; ok && c.Key != "" {
			t.Errorf("%q and %q share key %q", other, c.Name, c.Key)
		}
		keys[c.Key] = c.Name
	}
	for name, action := range map[string]string{"Approve": "approve", "Request changes": "request_changes", "Comment": "comment", "Merge when ready": "automerge"} {
		c, ok := find(DefaultCommands(), name)
		if !ok || c.Action != action || c.Key == "" || Reserved[c.Key] || !c.OnBar() {
			t.Errorf("built-in %s = %+v", name, c)
		}
	}
	// Text first for the two that send some; asking first for the two that cannot be taken back.
	if rc, _ := find(DefaultCommands(), "Request changes"); rc.Prompt == "" {
		t.Error("Request changes should ask what should change")
	}
	if a, _ := find(DefaultCommands(), "Approve"); !a.Confirm || len(a.When) != 1 || a.When[0] != "others" {
		t.Errorf("Approve = %+v", a)
	}

	cfg, err := Load(write(t, "commands:\n  - name: Comment\n    key: K\n  - name: Merge when ready\n    hidden: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := find(cfg.Commands, "Comment"); c.Key != "K" || c.Action != "comment" || c.Prompt == "" {
		t.Errorf("moved Comment lost the built-in: %+v", c)
	}
	if _, ok := find(cfg.Commands, "Merge when ready"); ok {
		t.Error("hidden Merge when ready still present")
	}
}

func TestKeyTakenByUserCommand(t *testing.T) {
	cfg, err := Load(write(t, "commands:\n  - name: Test\n    key: f\n    run: make test\n"))
	if err != nil {
		t.Fatal(err)
	}
	fetch, _ := find(cfg.Commands, "Fetch")
	if fetch.Key != "" {
		t.Errorf("Fetch kept key %q", fetch.Key)
	}
}

func TestReservedKeyWarns(t *testing.T) {
	cfg, err := Load(write(t, "commands:\n  - name: Jump\n    key: j\n    run: echo hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	jump, _ := find(cfg.Commands, "Jump")
	if jump.Key != "" || len(cfg.Warnings) == 0 {
		t.Errorf("reserved key not rejected: %+v %v", jump, cfg.Warnings)
	}
}

func TestUnknownFieldIsAnError(t *testing.T) {
	if _, err := Load(write(t, "editr:\n  default: zed\n")); err == nil {
		t.Error("typo in a key should be reported")
	}
}

func TestBadDuration(t *testing.T) {
	_, err := Load(write(t, "github:\n  refresh: soon\n"))
	if err == nil || !strings.Contains(err.Error(), "not a duration") {
		t.Errorf("err = %v", err)
	}
}

func TestExampleParses(t *testing.T) {
	cfg, err := Load(write(t, Example))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("example has warnings: %v", cfg.Warnings)
	}
}

// Paths shortened for display expand back on every system: ShortenHome gives ~\code on
// Windows.
func TestHomeRoundTrip(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	p := filepath.Join(home, "code", "tool")
	short := ShortenHome(p)
	if want := "~" + string(filepath.Separator) + filepath.Join("code", "tool"); short != want {
		t.Errorf("ShortenHome(%q) = %q, want %q", p, short, want)
	}
	for _, s := range []string{short, "~/code/tool"} {
		if got := ExpandHome(s); got != p {
			t.Errorf("ExpandHome(%q) = %q, want %q", s, got, p)
		}
	}
	if got := ExpandHome(`~x\y`); got != `~x\y` {
		t.Errorf("ExpandHome(~x) = %q", got)
	}
}

func TestShortenHomeNeedsASeparator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home folder on Windows
	for in, want := range map[string]string{
		home:                               "~",
		filepath.Join(home, "code", "api"): "~" + string(filepath.Separator) + filepath.Join("code", "api"),
		home + "-old/code":                 home + "-old/code",
		"/elsewhere/project":               "/elsewhere/project",
	} {
		if got := ShortenHome(in); got != want {
			t.Errorf("ShortenHome(%q) = %q, want %q", in, got, want)
		}
		if got := ExpandHome(ShortenHome(in)); got != in {
			t.Errorf("ExpandHome(ShortenHome(%q)) = %q", in, got)
		}
	}
}

func TestUnknownWhenAndOutputWarn(t *testing.T) {
	cfg, err := Load(write(t, "commands:\n  - name: Squash\n    run: git reset --soft HEAD~1\n    when: author\n    output: terminl\n"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(cfg.Warnings, "\n")
	if !strings.Contains(text, `"author"`) || !strings.Contains(text, `"terminl"`) {
		t.Errorf("warnings = %v", cfg.Warnings)
	}
}

func TestQuotedPlaceholderWarns(t *testing.T) {
	for run, want := range map[string]string{
		`git commit -m '{title}'`:            "{title}",
		`echo "Fix: {title}"`:                "{title}",
		`git commit -m {title}`:              "",
		`open -a "Zed" {path}`:               "",
		"# don't push\necho {title}":         "",
		`echo it\'s {title}`:                 "",
		`awk '{print}' && echo "a\"b" {url}`: "",
		`echo x#y '{branch}'`:                "{branch}",
	} {
		if got := quotedPlaceholder(run); got != want {
			t.Errorf("quotedPlaceholder(%q) = %q, want %q", run, got, want)
		}
	}
	cfg, err := Load(write(t, "editor:\n  editors:\n    mine: \"my-editor '{path}'\"\ncommands:\n  - name: Note\n    run: echo \"{title}\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(cfg.Warnings, "\n")
	if !strings.Contains(text, `command "Note"`) || !strings.Contains(text, `editor "mine"`) {
		t.Errorf("warnings = %v", cfg.Warnings)
	}
}
