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
