package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MasterPushers/prdeck/internal/runner"
)

func finds(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, n := range names {
			if n == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", exec.ErrNotFound
	}
}

func TestTerminalScript(t *testing.T) {
	const script, dir = "nvim /w/tool", "/w/my tool"
	cases := []struct {
		goos  string
		found []string
		want  string
	}{
		// The Mac keeps its Terminal.app script, byte for byte.
		{"darwin", nil, `osascript -e 'tell application "Terminal" to do script "cd '\''/w/my tool'\'' && nvim /w/tool"' -e 'tell application "Terminal" to activate'`},
		{"linux", []string{"xterm", "konsole"}, `konsole -e /bin/sh -c 'unset GIT_TERMINAL_PROMPT GH_PROMPT_DISABLED GIT_EDITOR NO_COLOR; cd '\''/w/my tool'\'' && nvim /w/tool'`},
		{"linux", []string{"x-terminal-emulator", "gnome-terminal"}, `x-terminal-emulator -e /bin/sh -c 'unset GIT_TERMINAL_PROMPT GH_PROMPT_DISABLED GIT_EDITOR NO_COLOR; cd '\''/w/my tool'\'' && nvim /w/tool'`},
		{"linux", []string{"gnome-terminal"}, `gnome-terminal -- /bin/sh -c 'unset GIT_TERMINAL_PROMPT GH_PROMPT_DISABLED GIT_EDITOR NO_COLOR; cd '\''/w/my tool'\'' && nvim /w/tool'`},
		{"linux", []string{"xterm", "kgx"}, `kgx -- /bin/sh -c 'unset GIT_TERMINAL_PROMPT GH_PROMPT_DISABLED GIT_EDITOR NO_COLOR; cd '\''/w/my tool'\'' && nvim /w/tool'`},
		{"linux", []string{"kitty"}, `kitty /bin/sh -c 'unset GIT_TERMINAL_PROMPT GH_PROMPT_DISABLED GIT_EDITOR NO_COLOR; cd '\''/w/my tool'\'' && nvim /w/tool'`},
		{"linux", []string{"wezterm"}, `wezterm start -- /bin/sh -c 'unset GIT_TERMINAL_PROMPT GH_PROMPT_DISABLED GIT_EDITOR NO_COLOR; cd '\''/w/my tool'\'' && nvim /w/tool'`},
		{"windows", nil, `PRDECK_SCRIPT='unset GIT_TERMINAL_PROMPT GH_PROMPT_DISABLED GIT_EDITOR NO_COLOR; cd '\''/w/my tool'\'' && nvim /w/tool' MSYS2_ENV_CONV_EXCL=PRDECK_SCRIPT MSYS2_ARG_CONV_EXCL='*' cmd /c start "" /bin/sh -c 'eval "$PRDECK_SCRIPT"'`},
	}
	for _, c := range cases {
		got, err := terminalScript(c.goos, "/bin/sh", script, dir, finds(c.found...))
		if err != nil || got != c.want {
			t.Errorf("%s %v:\n got %s (%v)\nwant %s", c.goos, c.found, got, err, c.want)
		}
	}
	_, err := terminalScript("linux", "/bin/sh", script, dir, finds())
	if err == nil || !strings.Contains(err.Error(), "xterm") {
		t.Errorf("no terminal: %v", err)
	}
}

// The scripts run for real, with stand-ins for the terminal (and for cmd.exe's start)
// that run what they are given: the command must arrive whole, in its folder.
func TestTerminalScriptRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-ins are sh scripts")
	}
	bin := t.TempDir()
	stub := map[string]string{
		"konsole": `shift; exec "$@"`,                                                                         // konsole -e CMD...
		"cmd":     `[ "$1 $2 $3" = "/c start " ] && [ "$MSYS2_ARG_CONV_EXCL" = "*" ] && shift 3 && exec "$@"`, // cmd /c start "" CMD...
	}
	for name, body := range stub {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, goos := range []string{"linux", "windows"} {
		dir := filepath.Join(t.TempDir(), "it's a dir")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		title := `"; touch pwned; echo "$HOME & 100%`
		run := `printf '%s' {title} > out; printf '%s' "$GIT_EDITOR$NO_COLOR$GH_PROMPT_DISABLED$GIT_TERMINAL_PROMPT" > env`
		script, err := terminalScript(goos, "/bin/sh", runner.Fill(run, map[string]string{"title": title}), dir, finds("konsole"))
		if err != nil {
			t.Fatal(err)
		}
		if out, err := runner.Run(context.Background(), script, t.TempDir()); err != nil {
			t.Fatalf("%s: %v: %s", goos, err, out)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "out"))
		if string(got) != title {
			t.Errorf("%s: wrote %q, want %q", goos, got, title)
		}
		if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
			t.Errorf("%s: the title ran as a command", goos)
		}
		// Run, like Execute, turns prompts off for what it captures. The editor gets the
		// user's environment, as in Terminal.app: GIT_EDITOR=true would break git commit.
		if env, _ := os.ReadFile(filepath.Join(dir, "env")); len(env) > 0 {
			t.Errorf("%s: the editor got the no-prompt variables: %q", goos, env)
		}
	}
}

func TestGhInstaller(t *testing.T) {
	cases := []struct {
		goos  string
		found []string
		want  string
	}{
		{"darwin", []string{"brew", "winget"}, "brew"},
		{"darwin", nil, ""},
		{"windows", []string{"winget", "brew"}, "winget"},
		{"windows", nil, ""},
		{"linux", []string{"brew", "apt"}, ""}, // packages need sudo: setup only says how
	}
	for _, c := range cases {
		if got := ghInstaller(c.goos, finds(c.found...)); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.goos, c.found, got, c.want)
		}
	}
}

func TestGhInstallHint(t *testing.T) {
	cases := map[string]string{
		"ID=ubuntu\nID_LIKE=debian\n":                                    "sudo apt install gh",
		"NAME=\"Linux Mint\"\nID=linuxmint\nID_LIKE=\"ubuntu debian\"\n": "sudo apt install gh",
		"ID=fedora\n": "sudo dnf install gh",
		"ID_LIKE=\"rhel centos fedora\"\nID=\"rocky\"\n":        "sudo dnf install gh",
		"ID=manjaro\nID_LIKE=arch\n":                            "sudo pacman -S github-cli",
		"ID=\"opensuse-tumbleweed\"\nID_LIKE=\"opensuse suse\"": "sudo zypper install gh",
		"ID=alpine\n": "",
		"":            "",
	}
	for osRelease, want := range cases {
		if got := linuxGhCommand(osRelease); got != want {
			t.Errorf("%q: %q, want %q", osRelease, got, want)
		}
		hint := ghInstallHint("linux", osRelease)
		if !strings.Contains(hint, ghDocs) || !strings.Contains(hint, want) {
			t.Errorf("%q: hint %q", osRelease, hint)
		}
	}
	if h := ghInstallHint("windows", ""); !strings.Contains(h, "winget install --id GitHub.cli") {
		t.Errorf("windows hint %q", h)
	}
}

func TestWindowsEditor(t *testing.T) {
	local := `C:\Users\me\AppData\Local`
	vscode := filepath.Join(local, `Programs\Microsoft VS Code\bin\code.cmd`)
	getenv := func(k string) string { return map[string]string{"LOCALAPPDATA": local}[k] }
	cases := []struct {
		name, cli string
		path      map[string]string // what lookPath finds
		files     []string
		run       string
		ok        bool
	}{
		{"zed", "zed", map[string]string{"zed": `C:\Zed\bin\zed.exe`}, nil, "", true},
		// VS Code's bin has an sh script next to code.cmd: sh finds it by name.
		{"vscode", "code", map[string]string{"code": `C:\VSC\bin\code.cmd`}, []string{`C:\VSC\bin\code`}, "", true},
		{"vscode", "code", map[string]string{"code": `C:\VSC\bin\code.cmd`}, nil, "C:/VSC/bin/code.cmd {path}", true},
		{"vscode", "code", nil, []string{vscode, strings.TrimSuffix(vscode, ".cmd")}, "'C:/Users/me/AppData/Local/Programs/Microsoft VS Code/bin/code' {path}", true},
		{"vscode", "code", nil, []string{vscode}, "'C:/Users/me/AppData/Local/Programs/Microsoft VS Code/bin/code.cmd' {path}", true},
		{"cursor", "cursor", nil, nil, "", false},
		{"nvim", "nvim", nil, nil, "", false},
	}
	for _, c := range cases {
		lookPath := func(name string) (string, error) {
			if p, ok := c.path[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		}
		isFile := func(p string) bool {
			for _, f := range c.files {
				if f == p {
					return true
				}
			}
			return false
		}
		run, ok := windowsEditor(c.name, c.cli, lookPath, getenv, isFile)
		if run != c.run || ok != c.ok {
			t.Errorf("%s %v %v: %q %v, want %q %v", c.name, c.path, c.files, run, ok, c.run, c.ok)
		}
	}
}

// setupStatus tells the desktop how gh can be installed here; brewFound keeps its meaning.
func TestSetupStatusInstaller(t *testing.T) {
	var st setupStatus
	if err := json.Unmarshal(start(t).call("setupStatus", nil).Result, &st); err != nil {
		t.Fatal(err)
	}
	if st.Installer != ghInstaller(runtime.GOOS, exec.LookPath) || st.BrewFound != (st.Installer == "brew") || st.InstallHint == "" {
		t.Errorf("installer %q, brewFound %v, hint %q", st.Installer, st.BrewFound, st.InstallHint)
	}
}

func TestTextEditorRun(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "open -t {path}", "windows": "notepad {path}", "linux": "xdg-open {path}"} {
		if got := textEditorRun(goos); got != want {
			t.Errorf("%s: %q, want %q", goos, got, want)
		}
	}
}

func TestCleanProgress(t *testing.T) {
	in := "Found GitHub CLI\r\n  -\r  \\\r  ██      1 MB / 10 MB\r  ██████ 10 MB / 10 MB\r\nSuccessfully installed\r\n"
	if got, want := cleanProgress(in), "Found GitHub CLI\n  ██████ 10 MB / 10 MB\nSuccessfully installed\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
