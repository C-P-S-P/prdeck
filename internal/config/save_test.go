package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveRepoPathKeepsTheRest(t *testing.T) {
	p := write(t, "# my settings\neditor:\n  default: cursor  # the one I use\nrepos:\n  scan: [~/src]\n")
	if err := SaveRepoPath(p, "acme/tool", "/somewhere/tool"); err != nil {
		t.Fatal(err)
	}
	if err := SaveRepoPath(p, "acme/tool", "/moved/tool"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	text := string(raw)
	for _, want := range []string{"# my settings", "# the one I use", "default: cursor", "scan: [~/src]", "acme/tool: /moved/tool"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Count(text, "acme/tool") != 1 {
		t.Errorf("repo saved twice:\n%s", text)
	}
	cfg, err := Load(p)
	if err != nil || cfg.Repos.Paths["acme/tool"] != "/moved/tool" || cfg.Editor.Default != "cursor" {
		t.Errorf("reloaded: %+v %v", cfg.Repos, err)
	}
}

func TestSaveRepoPathCreatesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new", "config.yaml")
	if err := SaveRepoPath(p, "acme/tool", "/x/tool"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil || cfg.Repos.Paths["acme/tool"] != "/x/tool" {
		t.Errorf("paths = %v, err = %v", cfg.Repos.Paths, err)
	}
}

func TestOverrideKeepsConfirm(t *testing.T) {
	cfg, err := Load(write(t, "commands:\n  - name: Push\n    key: P\n"))
	if err != nil {
		t.Fatal(err)
	}
	push, _ := find(cfg.Commands, "Push")
	if !push.Confirm || !push.Branch || push.Key != "P" {
		t.Errorf("push = %+v", push)
	}
}

func TestWorktreeOffByDefault(t *testing.T) {
	if Default().Editor.UseWorktree() {
		t.Error("worktrees must be opt-in")
	}
}
