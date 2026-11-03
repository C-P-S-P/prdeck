package server

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MasterPushers/prdeck/internal/runner"
)

// What differs between macOS, Windows and Linux for the desktop app: opening a terminal,
// installing gh, finding editors. Each piece takes the OS as a parameter so its tests run
// anywhere; macOS keeps exactly what it did before.

// linuxTerminals are tried in order, each with the words after which it runs a command
// given as separate arguments.
var linuxTerminals = []struct{ name, run string }{
	{"x-terminal-emulator", "-e"}, // the one Debian and Ubuntu set as default
	{"gnome-terminal", "--"},
	{"ptyxis", "--"}, // Fedora's default since 41
	{"konsole", "-e"},
	{"xfce4-terminal", "-x"},
	{"kitty", ""},
	{"alacritty", "-e"},
	{"wezterm", "start --"},
	{"foot", ""},
	{"xterm", "-e"},
}

// inTerminal wraps a script so it runs in dir in a new terminal window.
func inTerminal(script, dir string) (string, error) {
	sh, err := runner.Shell()
	if err != nil {
		return "", err
	}
	return terminalScript(runtime.GOOS, sh, script, dir, exec.LookPath)
}

// terminalScript is inTerminal for goos, with sh the shell that runs scripts there.
func terminalScript(goos, sh, script, dir string, lookPath func(string) (string, error)) (string, error) {
	inner := "cd " + runner.Quote(dir) + " && " + script
	switch goos {
	case "darwin":
		return terminalApp(script, dir), nil
	case "windows":
		// A new console window: Windows Terminal when it is the default terminal (as on
		// Windows 11), else a console. The script travels in the environment, so it is
		// never read by cmd.exe (which acts on & and %) or Windows argument quoting.
		// The MSYS2_ variables stop Git's sh rewriting what looks like a path in them
		// (/c would become C:\).
		return "PRDECK_SCRIPT=" + runner.Quote(inner) + ` MSYS2_ENV_CONV_EXCL=PRDECK_SCRIPT MSYS2_ARG_CONV_EXCL='*' cmd /c start "" ` + runner.Quote(sh) + ` -c 'eval "$PRDECK_SCRIPT"'`, nil
	}
	var names []string
	for _, t := range linuxTerminals {
		if _, err := lookPath(t.name); err == nil {
			run := t.name
			if t.run != "" {
				run += " " + t.run
			}
			return run + " " + runner.Quote(sh) + " -c " + runner.Quote(inner), nil
		}
		names = append(names, t.name)
	}
	return "", errors.New("no terminal app was found (looked for " + strings.Join(names, ", ") + ")")
}

// ghInstaller is how setup installs gh on goos: "brew", "winget", or "" when it does
// not. Linux needs sudo for its package manager, so setup only says the command.
func ghInstaller(goos string, lookPath func(string) (string, error)) string {
	tool := map[string]string{"darwin": "brew", "windows": "winget"}[goos]
	if tool == "" {
		return ""
	}
	if _, err := lookPath(tool); err != nil {
		return ""
	}
	return tool
}

// wingetArgs install gh without waiting for anyone to accept the agreements.
var wingetArgs = []string{"install", "--id", "GitHub.cli", "-e", "--source", "winget", "--accept-package-agreements", "--accept-source-agreements", "--silent"}

const ghDocs = "https://github.com/cli/cli#installation"

// ghInstallHint says how to install gh by hand on goos. osRelease is the content of
// /etc/os-release, which names the Linux distribution.
func ghInstallHint(goos, osRelease string) string {
	switch goos {
	case "darwin":
		return "Install it with Homebrew (brew install gh) or from https://cli.github.com"
	case "windows":
		return "Install it with winget (winget install --id GitHub.cli) or from https://cli.github.com"
	}
	if cmd := linuxGhCommand(osRelease); cmd != "" {
		return "Install it with " + cmd + " (other ways: " + ghDocs + ")"
	}
	return "Install it: " + ghDocs
}

// osRelease is /etc/os-release on Linux, which names the distribution.
func osRelease() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		b, _ = os.ReadFile("/usr/lib/os-release")
	}
	return string(b)
}

// linuxGhCommand is the package manager command that installs gh on the distribution
// /etc/os-release describes, or "" when it is not one we know.
func linuxGhCommand(osRelease string) string {
	var ids []string // ID first, then the families in ID_LIKE
	for _, key := range []string{"ID", "ID_LIKE"} {
		for _, line := range strings.Split(osRelease, "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k == key {
				ids = append(ids, strings.Fields(strings.ToLower(strings.Trim(v, `"'`)))...)
			}
		}
	}
	for _, id := range ids {
		switch {
		case id == "debian" || id == "ubuntu":
			return "sudo apt install gh"
		case id == "fedora" || id == "rhel" || id == "centos":
			return "sudo dnf install gh"
		case id == "arch":
			return "sudo pacman -S github-cli"
		case id == "suse" || strings.HasPrefix(id, "opensuse"):
			return "sudo zypper install gh"
		}
	}
	return ""
}

// addGhToPath puts gh's folder on PATH when gh is installed but not on it, as after
// setup installs it with winget: the app's PATH is from before the install.
func addGhToPath() {
	if _, err := exec.LookPath("gh"); err == nil {
		return
	}
	for _, d := range [][2]string{{"ProgramFiles", "GitHub CLI"}, {"LOCALAPPDATA", `Programs\GitHub CLI`}} {
		if v := os.Getenv(d[0]); v != "" && isFile(filepath.Join(v, d[1], "gh.exe")) {
			os.Setenv("PATH", filepath.Join(v, d[1])+string(os.PathListSeparator)+os.Getenv("PATH"))
			return
		}
	}
}

// windowsEditors are where editors install on Windows, for when their command-line tool
// is not on PATH: an environment variable and the path under it.
var windowsEditors = map[string][][2]string{
	"vscode": {{"LOCALAPPDATA", `Programs\Microsoft VS Code\bin\code.cmd`}, {"ProgramFiles", `Microsoft VS Code\bin\code.cmd`}},
	"cursor": {{"LOCALAPPDATA", `Programs\cursor\resources\app\bin\cursor.cmd`}},
	"zed":    {{"LOCALAPPDATA", `Programs\Zed\bin\zed.exe`}},
}

// windowsEditor finds an editor's command-line tool cli on Windows. run is "" when the
// config's line ("code {path}") starts it from sh, else a line that does.
func windowsEditor(name, cli string, lookPath func(string) (string, error), getenv func(string) string, isFile func(string) bool) (run string, ok bool) {
	p, err := lookPath(cli)
	onPath := err == nil
	if !onPath {
		for _, w := range windowsEditors[name] {
			if v := getenv(w[0]); v != "" && isFile(filepath.Join(v, w[1])) {
				p = filepath.Join(v, w[1])
				break
			}
		}
		if p == "" {
			return "", false
		}
	}
	// sh finds a program by name only as name.exe or as a script without extension, so a
	// .cmd shim is run by its full path: the sh script next to it when there is one (VS
	// Code and Cursor ship one), else the shim, which Windows runs through cmd.exe.
	if ext := filepath.Ext(p); strings.EqualFold(ext, ".cmd") || strings.EqualFold(ext, ".bat") {
		if s := strings.TrimSuffix(p, ext); isFile(s) {
			if onPath {
				return "", true
			}
			p = s
		}
	} else if onPath {
		return "", true
	}
	// Forward slashes: sh takes a word with a / as a path, not a name to look up.
	return runner.Quote(strings.ReplaceAll(p, `\`, "/")) + " {path}", true
}

// textEditorRun opens a file in the default text editor on goos.
func textEditorRun(goos string) string {
	switch goos {
	case "darwin":
		return `open -t {path}`
	case "windows":
		return `notepad {path}`
	}
	return `xdg-open {path}`
}

// cleanProgress keeps what a progress bar drawn with \r ends on, line by line.
func cleanProgress(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = l[strings.LastIndex(l, "\r")+1:]
	}
	return strings.Join(lines, "\n")
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
