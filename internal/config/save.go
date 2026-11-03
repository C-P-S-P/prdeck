package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SaveRepoPath records `repos.paths[repo] = clonePath` in the config file, creating the
// file if needed. It edits the YAML tree, so the user's other settings and comments stay.
func SaveRepoPath(file, repo, clonePath string) error {
	return edit(file, func(root *yaml.Node) {
		set(child(child(root, "repos"), "paths"), repo, ShortenHome(clonePath))
	})
}

// Setup is what first-run setup in the desktop app chooses.
type Setup struct {
	Scan      []string // folders that hold clones
	Editor    string   // name under editor.editors
	EditorRun string   // its run line, when the built-in one would not work (no CLI on PATH)
	Worktree  bool
}

// SaveSetup writes the setup choices into the config file, keeping everything else.
func SaveSetup(file string, s Setup) error {
	return edit(file, func(root *yaml.Node) {
		scan := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, d := range s.Scan {
			scan.Content = append(scan.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: ShortenHome(d)})
		}
		setNode(child(root, "repos"), "scan", scan)
		ed := child(root, "editor")
		if s.Editor != "" {
			set(ed, "default", s.Editor)
		}
		setNode(ed, "worktree", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(s.Worktree)})
		if s.EditorRun != "" {
			set(child(ed, "editors"), s.Editor, s.EditorRun)
		}
	})
}

// edit loads the config file's YAML tree (or starts a new one), applies change, and
// writes it back atomically.
func edit(file string, change func(root *yaml.Node)) error {
	var doc yaml.Node
	raw, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{
			Kind:        yaml.MappingNode,
			HeadComment: "PR Deck config. Run `prdeck config init` elsewhere to see every option.",
		}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected a mapping at the top level", file)
	}
	change(root)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// child returns the mapping under key, creating it (or replacing an empty value).
func child(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind != yaml.MappingNode {
				*v = yaml.Node{Kind: yaml.MappingNode}
			}
			return v
		}
	}
	v := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	return v
}

func set(m *yaml.Node, key, value string) {
	setNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Value: value})
}

func setNode(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if strings.EqualFold(m.Content[i].Value, key) {
			value.LineComment = m.Content[i+1].LineComment
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
}
