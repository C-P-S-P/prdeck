package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTypoSaysUnknownCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRDECK_CONFIG", filepath.Join(dir, "none.yaml"))
	t.Setenv("GH_CONFIG_DIR", dir) // signed out
	t.Setenv("GH_PATH", filepath.Join(dir, "no-gh"))
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	err := run([]string{"lsit"})
	if err == nil || !strings.HasPrefix(err.Error(), `unknown command "lsit"`) {
		t.Errorf("prdeck lsit: %v", err)
	}
}
