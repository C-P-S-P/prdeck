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

func TestSaveKeepsACommentsOnlyFile(t *testing.T) {
	p := write(t, "# github:\n#   limit: 20\n# my notes\n")
	if err := SaveRepoPath(p, "a/b", "/x/y"); err != nil {
		t.Fatal(err)
	}
	if err := SaveRepoPath(p, "c/d", "/z"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	for _, want := range []string{"# github:", "#   limit: 20", "# my notes", "a/b: /x/y", "c/d: /z"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %q in\n%s", want, raw)
		}
	}
	cfg, err := Load(p)
	if err != nil || cfg.Repos.Paths["a/b"] != "/x/y" {
		t.Errorf("paths = %v, err = %v", cfg.Repos.Paths, err)
	}
}

func TestSaveWritesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles.yaml")
	if err := os.WriteFile(real, []byte("ui:\n  mouse: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := SaveRepoPath(link, "a/b", "/x/y"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v %v", st.Mode(), err)
	}
	raw, _ := os.ReadFile(real)
	if !strings.Contains(string(raw), "a/b: /x/y") || !strings.Contains(string(raw), "mouse: true") {
		t.Errorf("target not updated:\n%s", raw)
	}
	if st, _ := os.Stat(real); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestSaveThroughALinkIntoAReadOnlyFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(store, "config.yaml")
	if err := os.WriteFile(real, []byte("ui:\n  mouse: true\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	os.Chmod(store, 0o555)
	defer os.Chmod(store, 0o755)
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := SaveRepoPath(link, "a/b", "/x/y"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(link)
	if !strings.Contains(string(raw), "a/b: /x/y") || !strings.Contains(string(raw), "mouse: true") {
		t.Errorf("config not saved:\n%s", raw)
	}
	if st, _ := os.Lstat(link); !st.Mode().IsRegular() || st.Mode().Perm()&0o200 == 0 {
		t.Errorf("mode = %v, want a writable file", st.Mode())
	}
}

func TestSaveWorktreeDirKeepsTheRest(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	orig := "editor:\n  default: zed # my editor\n  worktree_dir: ~/prdeck\nrepos:\n  scan: [~/src]\n"
	if err := os.WriteFile(file, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if err := SaveWorktreeDir(file, filepath.Join(home, "trees")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	for _, want := range []string{"default: zed # my editor", "worktree_dir: ~/trees", "scan: [~/src]"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(string(got), "worktree_dir") != 1 {
		t.Errorf("worktree_dir written twice:\n%s", got)
	}
}
