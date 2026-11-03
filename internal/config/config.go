// Package config loads PR Deck's YAML config and merges it over the built-in defaults.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	GitHub   GitHub    `yaml:"github"`
	Repos    Repos     `yaml:"repos"`
	Editor   Editor    `yaml:"editor"`
	Filters  []Filter  `yaml:"filters"`
	Commands []Command `yaml:"commands"`
	Theme    Theme     `yaml:"theme"`
	UI       UI        `yaml:"ui"`

	// Path is the file the user config was read from ("" when only defaults are in use).
	Path string `yaml:"-"`
	// Warnings are problems found while loading that did not stop the app.
	Warnings []string `yaml:"-"`
}

type GitHub struct {
	Refresh Duration `yaml:"refresh"`
	Limit   int      `yaml:"limit"`
}

type Repos struct {
	// Scan lists folders whose sub-folders are git clones (searched two levels deep).
	Scan []string `yaml:"scan"`
	// Paths maps "owner/name" to a clone, for clones outside the scan folders.
	Paths map[string]string `yaml:"paths"`
}

type Editor struct {
	Default     string                `yaml:"default"`
	Worktree    *bool                 `yaml:"worktree"`
	WorktreeDir string                `yaml:"worktree_dir"`
	Editors     map[string]EditorSpec `yaml:"editors"`
}

// UseWorktree reports whether every command runs in a per-PR git worktree. Off by
// default: commands run in the user's clone, and worktrees are made only on request.
func (e Editor) UseWorktree() bool { return e.Worktree != nil && *e.Worktree }

type EditorSpec struct {
	Run      string `yaml:"run"`
	Terminal bool   `yaml:"terminal"` // takes over the terminal (nvim, helix…)
}

// UnmarshalYAML accepts either `zed: zed {path}` or `nvim: {run: nvim {path}, terminal: true}`.
func (e *EditorSpec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		e.Run = n.Value
		return nil
	}
	type plain EditorSpec
	return n.Decode((*plain)(e))
}

type Filter struct {
	Name   string `yaml:"name"`
	Query  string `yaml:"query"`
	Hidden bool   `yaml:"hidden"`
}

type Command struct {
	Name     string     `yaml:"name"`
	Key      string     `yaml:"key"`
	Run      string     `yaml:"run"`
	Action   string     `yaml:"action"` // built-in actions: open, browse, approve, request_changes, comment, automerge, rerun, setpath
	Bar      *bool      `yaml:"bar"`
	Confirm  bool       `yaml:"confirm"`
	Prompt   string     `yaml:"prompt"`
	Output   string     `yaml:"output"` // panel (default) | terminal | silent
	When     StringList `yaml:"when"`
	Hidden   bool       `yaml:"hidden"`
	Branch   bool       `yaml:"branch"`   // check out the PR's branch first (an empty run just checks out)
	Clean    bool       `yaml:"clean"`    // needs no uncommitted changes
	Worktree bool       `yaml:"worktree"` // run in a worktree made for this PR
}

// OnBar reports whether the command is pinned to the bottom bar.
func (c Command) OnBar() bool { return c.Bar != nil && *c.Bar }

type Theme struct {
	Markdown string `yaml:"markdown"` // no longer used (one built-in style); kept so old configs still load
}

type UI struct {
	Mouse *bool `yaml:"mouse"` // click and scroll; on by default
}

// MouseOn reports whether mouse support is enabled.
func (u UI) MouseOn() bool { return u.Mouse == nil || *u.Mouse }

// Duration reads "2m", "90s" and similar.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %q is not a duration (try 2m or 90s)", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// StringList reads a single string or a list of strings.
type StringList []string

func (s *StringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*s = []string{n.Value}
		return nil
	}
	var l []string
	if err := n.Decode(&l); err != nil {
		return err
	}
	*s = l
	return nil
}

// Reserved keys drive navigation and cannot be bound to commands.
var Reserved = map[string]bool{
	"j": true, "k": true, "q": true, "?": true, "/": true, "R": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true,
}

// DefaultPath is $PRDECK_CONFIG, else $XDG_CONFIG_HOME/prdeck/config.yaml, else ~/.config/prdeck/config.yaml.
func DefaultPath() string {
	if p := os.Getenv("PRDECK_CONFIG"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "prdeck", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "prdeck", "config.yaml")
}

// Load reads the config at path (a missing file means defaults only) and merges it over the defaults.
func Load(path string) (*Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg.validate()
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	var user Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&user); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.merge(&user)
	cfg.Path = path
	cfg.validate()
	return cfg, nil
}

func (c *Config) merge(u *Config) {
	if u.GitHub.Refresh != 0 {
		c.GitHub.Refresh = u.GitHub.Refresh
	}
	if u.GitHub.Limit != 0 {
		c.GitHub.Limit = u.GitHub.Limit
	}
	if u.Repos.Scan != nil {
		c.Repos.Scan = u.Repos.Scan
	}
	for k, v := range u.Repos.Paths {
		if c.Repos.Paths == nil {
			c.Repos.Paths = map[string]string{}
		}
		c.Repos.Paths[k] = v
	}
	if u.Editor.Default != "" {
		c.Editor.Default = u.Editor.Default
	}
	if u.Editor.Worktree != nil {
		c.Editor.Worktree = u.Editor.Worktree
	}
	if u.Editor.WorktreeDir != "" {
		c.Editor.WorktreeDir = u.Editor.WorktreeDir
	}
	for k, v := range u.Editor.Editors {
		c.Editor.Editors[k] = v
	}
	if u.Theme.Markdown != "" {
		c.Theme.Markdown = u.Theme.Markdown
	}
	if u.UI.Mouse != nil {
		c.UI.Mouse = u.UI.Mouse
	}
	c.Filters = mergeFilters(c.Filters, u.Filters)
	c.Commands = mergeCommands(c.Commands, u.Commands)
}

// mergeFilters replaces filters with the same name, drops hidden ones and appends new ones.
func mergeFilters(base, user []Filter) []Filter {
	out := append([]Filter(nil), base...)
	for _, f := range user {
		i := indexOf(len(out), func(i int) bool { return strings.EqualFold(out[i].Name, f.Name) })
		switch {
		case f.Hidden && i >= 0:
			out = append(out[:i], out[i+1:]...)
		case f.Hidden:
		case i >= 0:
			out[i] = f
		default:
			out = append(out, f)
		}
	}
	return out
}

// mergeCommands works like mergeFilters; a user command also takes its key away from any other command.
func mergeCommands(base, user []Command) []Command {
	out := append([]Command(nil), base...)
	for _, c := range user {
		i := indexOf(len(out), func(i int) bool { return strings.EqualFold(out[i].Name, c.Name) })
		if c.Hidden {
			if i >= 0 {
				out = append(out[:i], out[i+1:]...)
			}
			continue
		}
		if i >= 0 {
			// Without a run line the override keeps the built-in's behavior, so
			// `{name: Push, key: P}` only moves the key and Push still asks first.
			old := out[i]
			if c.Run == "" && c.Action == "" && !c.Branch {
				c.Run, c.Action = old.Run, old.Action
				c.Confirm = c.Confirm || old.Confirm
				c.Branch, c.Clean, c.Worktree = old.Branch, c.Clean || old.Clean, c.Worktree || old.Worktree
				if c.Output == "" {
					c.Output = old.Output
				}
				if c.Prompt == "" {
					c.Prompt = old.Prompt
				}
				if len(c.When) == 0 {
					c.When = old.When
				}
			}
			if c.Key == "" {
				c.Key = old.Key
			}
			if c.Bar == nil {
				c.Bar = old.Bar
			}
			out[i] = c
		} else {
			out = append(out, c)
		}
		for j := range out {
			if out[j].Name != c.Name && c.Key != "" && out[j].Key == c.Key {
				out[j].Key = ""
			}
		}
	}
	return out
}

func indexOf(n int, match func(int) bool) int {
	for i := 0; i < n; i++ {
		if match(i) {
			return i
		}
	}
	return -1
}

func (c *Config) validate() {
	seen := map[string]string{}
	for i, cmd := range c.Commands {
		if cmd.Run == "" && cmd.Action == "" && !cmd.Branch {
			c.Warnings = append(c.Warnings, fmt.Sprintf("command %q has no run line", cmd.Name))
		}
		if cmd.Key == "" {
			continue
		}
		if Reserved[cmd.Key] {
			c.Warnings = append(c.Warnings, fmt.Sprintf("command %q: key %q is reserved for navigation", cmd.Name, cmd.Key))
			c.Commands[i].Key = ""
			continue
		}
		if other, ok := seen[cmd.Key]; ok {
			c.Warnings = append(c.Warnings, fmt.Sprintf("key %q is used by both %q and %q", cmd.Key, other, cmd.Name))
		}
		seen[cmd.Key] = cmd.Name
	}
	if _, ok := c.Editor.Editors[c.Editor.Default]; !ok {
		c.Warnings = append(c.Warnings, fmt.Sprintf("editor %q is not defined under editor.editors", c.Editor.Default))
	}
	if c.GitHub.Refresh < Duration(30*time.Second) {
		c.GitHub.Refresh = Duration(30 * time.Second)
	}
	if c.GitHub.Limit <= 0 || c.GitHub.Limit > 100 {
		c.GitHub.Limit = 50
	}
}

// ExpandHome turns a leading ~ into the user's home folder.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// ShortenHome turns the user's home folder back into ~ for display.
func ShortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
