# 0016. Windows and Linux

- Status: accepted
- Date: 2026-11-04

## Context

PR Deck was built on a Mac. The terminal app already ran on Linux, but the core assumed a Mac or a Unix in several places: every command ran with `/bin/sh -c`, links opened with `open`, a terminal editor from the desktop app opened in Terminal.app through `osascript`, setup installed `gh` with Homebrew and found editors in `/Applications`, and cancelling a command stopped only the shell. On Windows almost nothing ran. The Mac must keep working exactly as before.

## Decision

The same features on every system, each difference kept in a small function that takes the OS as a parameter, so its tests run on any machine. On the Mac every one of them returns what PR Deck did before.

| | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Shell for commands | `/bin/sh` | `/bin/sh` | `sh.exe` from Git for Windows |
| Open a PR on GitHub | `open` | `xdg-open` | `rundll32 url.dll,FileProtocolHandler` |
| Terminal editor from the desktop app | Terminal.app (`osascript`) | the first of x-terminal-emulator, gnome-terminal, ptyxis, kgx (GNOME Console), konsole, xfce4-terminal, kitty, alacritty, wezterm, foot, xterm | a new console window (`cmd /c start`), which is Windows Terminal when that is the default terminal |
| Install `gh` in setup | Homebrew | not installed: setup says the command for the distribution (apt, dnf, pacman, zypper) | winget |
| Editors in setup | command on PATH, else the app in `/Applications` (`open -a`) | command on PATH | command on PATH, else the usual install folder |
| Copy a command (`y`) | `pbcopy` | `wl-copy` or `xclip` | the Windows clipboard API |
| Cancel a command | as before | as before | stop the shell and everything it started (`taskkill /T`): Windows has no process groups to signal |

- **One POSIX shell everywhere.** Commands in the config are POSIX shell, and placeholders are filled with POSIX single quotes so a PR title can never run ([0003](0003-yaml-config-and-custom-commands.md)). cmd.exe and PowerShell quote differently, so on Windows commands run with the `sh.exe` of Git for Windows, which a Windows user of git has already. It is found on PATH, else next to `git.exe` (`Git\cmd\git.exe` → `Git\bin\sh.exe`), else in `Program Files\Git`. Without it, running a command says that Git for Windows is needed. `runner.Shell` is the one place that decides; every script goes through `runner.Command`.
- **Windows commands that start other programs** are written for that shell: `start` is a cmd.exe built-in and explorer exits with 1 even when it worked, hence rundll32 for links. A terminal editor's script travels to the new console in an environment variable, so cmd.exe never reads it. On Linux and Windows the new window inherits the core's environment (Terminal.app starts afresh), so the script first unsets the variables that keep captured commands from prompting (`GIT_EDITOR=true` would break a commit made from the editor). An editor whose command is a `.cmd` shim, which sh cannot find by name, gets a run line with its full path, or the sh script next to it (VS Code and Cursor ship one).
- **Linux setup does not install gh**, because the package manager needs sudo. It reads `/etc/os-release` and shows the right command, with the link to gh's install page.
- **Paths.** The config stays at `~/.config/prdeck/config.yaml` on every system. Paths git prints as `C:/x` are read as `C:\x`, `~\x` is expanded like `~/x`, a typed `C:\x` is a path in the clone search, and the clone search skips `AppData`.
- **Output.** Windows programs end lines with `\r\n`; command output on Windows has them turned into `\n`, so both apps show it as on the Mac.
- **setupStatus** keeps `brewFound` and adds `installer` (`brew`, `winget` or empty) and `installHint` (how to install gh by hand here), so the desktop can offer the right button on each system.

## Consequences

- The terminal app and the core work on Windows and Linux like on the Mac, with the same config file and the same custom commands.
- Windows needs Git for Windows. Custom commands are written for sh on every system; a command that calls a Windows-only tool still works, since sh runs `.exe` files.
- The Linux terminal list and the Windows install folders are lists to keep up to date; a terminal that is not on the list makes a terminal editor in the desktop app fail with a clear message.
- Windows behavior is checked by unit tests of the pure functions on every machine and by CI on Windows; the Mac keeps its old code paths, which the tests pin down.

## Alternatives considered

- **Translate commands to cmd.exe or PowerShell.** Every custom command and the quoting would differ per system, and a PR title could break out of the quotes.
- **Windows Terminal through `wt.exe`.** It splits its command line at `;`, and a new tab may not get the caller's environment. A new console already opens in Windows Terminal when it is the default.
- **`clip.exe` for copying.** It reads text in the console code page unless fed UTF-16; the clipboard package Bubble Tea's text input already uses does it right.
- **Job objects to cancel on Windows.** Needs Windows-only code to create and assign the job; `taskkill /T` stops the same tree.
