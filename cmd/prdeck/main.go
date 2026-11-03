// Command prdeck is a terminal dashboard for the pull requests you're involved in.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/patelchaitany/prdeck/internal/cache"
	"github.com/patelchaitany/prdeck/internal/config"
	"github.com/patelchaitany/prdeck/internal/core"
	"github.com/patelchaitany/prdeck/internal/demo"
	"github.com/patelchaitany/prdeck/internal/github"
	"github.com/patelchaitany/prdeck/internal/tui"
)

var version = "dev"

const usage = `prdeck — pull requests you're involved in, with one-key git commands

Usage:
  prdeck                   open the dashboard
  prdeck list [query]      print open PRs (default: your first filter), e.g. prdeck list author:@me
  prdeck demo              try the dashboard with sample PRs (no GitHub, commands don't run)
  prdeck config init       write a starter config file
  prdeck config path       print where the config file lives
  prdeck version

Config: ` + "`prdeck config path`" + ` · Auth: uses your gh login (gh auth login)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "prdeck:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	case "version", "--version":
		fmt.Println("prdeck", version)
		return nil
	case "config":
		return configCmd(args[1:])
	case "demo":
		cfg, err := config.Load(config.DefaultPath())
		if err != nil {
			return err
		}
		_, err = tea.NewProgram(tui.New(demo.New(cfg)), tea.WithAltScreen()).Run()
		return err
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	gh, err := github.NewClient()
	if err != nil {
		return err
	}
	svc := core.New(cfg, gh, cache.Open(cache.DefaultPath()))

	switch cmd {
	case "":
		p := tea.NewProgram(tui.New(svc), tea.WithAltScreen())
		_, err := p.Run()
		return err
	case "list":
		return list(svc, cfg, strings.Join(args[1:], " "))
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}

func list(svc *core.Service, cfg *config.Config, query string) error {
	if query == "" {
		query = "involves:@me"
		if len(cfg.Filters) > 0 {
			query = cfg.Filters[0].Query
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prs, err := svc.Search(ctx, query)
	if err != nil {
		return err
	}
	if len(prs) == 0 {
		fmt.Println("No open PRs for", github.SearchQuery(query))
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tPR\tTITLE\tCI\tREVIEW\tUPDATED")
	for _, p := range prs {
		title := p.Title
		if r := []rune(title); len(r) > 60 {
			title = string(r[:59]) + "…"
		}
		if p.IsDraft {
			title = "[draft] " + title
		}
		fmt.Fprintf(tw, "%s\t#%d\t%s\t%s\t%s\t%s\n", p.Repo, p.Number, title, ciWord(p.CheckState), reviewWord(p), since(p.UpdatedAt))
	}
	return tw.Flush()
}

func ciWord(s string) string {
	switch s {
	case "SUCCESS":
		return "pass"
	case "FAILURE", "ERROR":
		return "fail"
	case "PENDING", "EXPECTED":
		return "running"
	}
	return "-"
}

func reviewWord(p github.PR) string {
	if p.Mergeable == "CONFLICTING" {
		return "conflict"
	}
	switch p.ReviewDecision {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes"
	case "REVIEW_REQUIRED":
		return "needs review"
	}
	return "-"
}

func since(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
}

func configCmd(args []string) error {
	path := config.DefaultPath()
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "path", "":
		fmt.Println(path)
		return nil
	case "init":
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; edit it, or delete it and run this again", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(config.Example), 0o644); err != nil {
			return err
		}
		fmt.Println("Wrote", path)
		return nil
	default:
		return fmt.Errorf("unknown config command %q (try: init, path)", sub)
	}
}
