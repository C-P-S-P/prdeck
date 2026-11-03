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

func TestSaveSetupKeepsTheRest(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# mine\ngithub:\n  refresh: 5m   # slower\nrepos:\n  scan: [~/old]\n  paths:\n    a/b: ~/x\neditor:\n  default: nvim\n"
	if err := os.WriteFile(file, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	err := SaveSetup(file, Setup{Scan: []string{home + "/projects", "~/code"}, Editor: "zed", EditorRun: `open -a "Zed" {path}`, Worktree: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	got := string(raw)
	for _, want := range []string{"# mine", "refresh: 5m", "# slower", "a/b: ~/x", "scan: [~/projects, ~/code]", "default: zed", "worktree: true", `zed: open -a "Zed" {path}`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Editor.Default != "zed" || !cfg.Editor.UseWorktree() || cfg.Editor.Editors["zed"].Run != `open -a "Zed" {path}` {
		t.Errorf("loaded back: %+v", cfg.Editor)
	}
}

func TestSaveSetupNewFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := SaveSetup(file, Setup{Scan: []string{"~/src"}, Editor: "cursor"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repos.Scan) != 1 || cfg.Repos.Scan[0] != "~/src" || cfg.Editor.Default != "cursor" || cfg.Editor.UseWorktree() {
		t.Errorf("got scan %v editor %+v", cfg.Repos.Scan, cfg.Editor)
	}
}
