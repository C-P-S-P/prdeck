# 0020. Windows and Linux

- Status: accepted
- Date: 2026-11-04

## Context

PR Deck was built on a Mac. The terminal app already ran on Linux, but the core assumed a Mac or a Unix in several places: every command ran with `/bin/sh -c`, links opened with `open`, a terminal editor from the desktop app opened in Terminal.app through `osascript`, setup installed `gh` with Homebrew and found editors in `/Applications`, and cancelling a command stopped only the shell. On Windows almost nothing ran. The desktop app was a Mac app: the header drew itself as the title bar beside the traffic lights, all of PR Deck's menu lived in the Mac menu bar, shortcuts were ⌘ keys, code was set in Menlo, its window sizes were saved under `~/Library`, and `prdeck` was looked for without `.exe`. The Mac must keep working exactly as before.

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

- **One POSIX shell everywhere.** Commands in the config are POSIX shell, and placeholders are filled with POSIX single quotes so a PR title can never run ([0003](0003-yaml-config-and-custom-commands.md)). cmd.exe and PowerShell quote differently, so on Windows commands run with the `sh.exe` of Git for Windows, which a Windows user of git has already. It is found on PATH, else next to `git.exe` (`Git\cmd\git.exe` → `Git\bin\sh.exe`), else in `Program Files\Git`. Without it, running a command says that Git for Windows is needed. `runner.Shell` is the one place that decides; every script goes through `runner.Command`. There the script reaches sh in an environment variable (`sh -c 'eval "$PRDECK_SCRIPT"'`), not on its command line: sh reads that by Cygwin's rules, which differ from the ones Go quotes for (a `\\` in quotes, as in a UNC path, would arrive as `\`).
- **Windows commands that start other programs** are written for that shell: `start` is a cmd.exe built-in and explorer exits with 1 even when it worked, hence rundll32 for links. A terminal editor's script travels to the new console in an environment variable, so cmd.exe never reads it. On Linux and Windows the new window inherits the core's environment (Terminal.app starts afresh), so the script first unsets the variables that keep captured commands from prompting (`GIT_EDITOR=true` would break a commit made from the editor). An editor whose command is a `.cmd` shim, which sh cannot find by name, gets a run line with its full path, or the sh script next to it (VS Code and Cursor ship one).
- **Linux setup does not install gh**, because the package manager needs sudo. It reads `/etc/os-release` and shows the right command, with the link to gh's install page.
- **Paths.** The config stays at `~/.config/prdeck/config.yaml` on every system. Paths git prints as `C:/x` are read as `C:\x`, `~\x` is expanded like `~/x`, a typed `C:\x` is a path in the clone search, and the clone search skips `AppData`.
- **Output.** Windows programs end lines with `\r\n`; command output on Windows has them turned into `\n`, so both apps show it as on the Mac.
- **setupStatus** keeps `brewFound` and adds `installer` (`brew`, `winget` or empty) and `installHint` (how to install gh by hand here), so the desktop can offer the right button on each system.

### The desktop app

The same window, data and keys everywhere; GPUI already draws on Windows (DirectX) and Linux (Vulkan or OpenGL, Wayland or X11). What differs is the frame around it.

| | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Title bar | the header, beside the traffic lights | the window manager's; without one (GNOME on Wayland) the header has minimize, maximize and close, and drags the window | the Windows title bar, above the header |
| Menus | the menu bar | none: the items are shortcuts or the title bar's buttons, and a gear in the header opens Set Up… | as on Linux |
| Shortcuts and their hints | `⌘K`, `⌘↵` | `Ctrl+K`, `Ctrl+Enter` | as on Linux |
| Code font | Menlo | DejaVu Sans Mono, else the first installed of Noto Sans Mono, Liberation Mono, Ubuntu Mono | Consolas, else Cascadia Mono |
| PATH and config location for the core | from the login shell | from the login shell, when `$SHELL` is set | the app's own: Windows gives apps the user's PATH |
| Window sizes saved in | `~/Library/Application Support/prdeck` | `$XDG_CONFIG_HOME/prdeck` (`~/.config/prdeck`) | `%APPDATA%\prdeck` |
| gh missing in setup | Install with Homebrew | the distribution's command and a link | Install with winget |
| Package | `scripts/bundle.sh`: PR Deck.app | `scripts/package-linux.sh`: a tar.gz with a `.desktop` launcher and `install.sh` | `scripts/package-windows.ps1`: a zip of `PR Deck.exe` and `prdeck.exe` |

- **Shortcuts are written once, in their Mac form.** `shortcut()` binds `cmd-k` as `ctrl-k` off the Mac and `key_hint()` shows `⌘K` as `Ctrl+K`, so the two never disagree. Hide, Hide Others and `⌘M` stay Mac-only. None of the Ctrl keys clashes: `Ctrl+Tab` was already bound to the same action, and the text box's own Ctrl keys (copy, paste, select all, undo, indent) are other letters.
- **The Windows title bar stays.** Drawing our own minimize, maximize and close there means telling Windows which pixels are buttons and which drag the window, and the clickable search box in the header sits in that area. The native bar costs a few pixels and is always right. On Linux GPUI asks for the window manager's title bar; only when it gets none does the header draw the buttons (gpui-component already draws the shadow and resize edges then).
- **No console windows on Windows.** The release app is a Windows (GUI) program, and it starts `prdeck serve` with `CREATE_NO_WINDOW`; the git, gh and sh that the core starts share that hidden console.
- **Fonts.** A family that is not installed makes GPUI fall back to a proportional font, so off the Mac the code font is the theme's default, which gpui-component checks against the installed fonts. The interface font is the system's (`.SystemUIFont`) everywhere.
- **CI builds, starts and packages it** (`.github/workflows/desktop-builds.yml`, on Ubuntu and Windows): the release build, the Rust and Go tests, then the app with `--demo` for 20 seconds, and the two packages and a screenshot kept as artifacts. Hosted runners have no GPU, so Linux draws with lavapipe (Mesa's software Vulkan) under Xvfb and Windows with WARP. The check is that the app is still running and got answers from `prdeck serve`; a missing screenshot is only a warning. The packages are artifacts, not releases: nothing is signed or versioned yet.

## Consequences

- The terminal app and the core work on Windows and Linux like on the Mac, with the same config file and the same custom commands.
- Windows needs Git for Windows. Custom commands are written for sh on every system; a command that calls a Windows-only tool still works, since sh runs `.exe` files.
- The Linux terminal list and the Windows install folders are lists to keep up to date; a terminal that is not on the list makes a terminal editor in the desktop app fail with a clear message.
- Windows behavior is checked by unit tests of the pure functions on every machine and by CI on Windows; the Mac keeps its old code paths, which the tests pin down.
- The desktop app's windows on Windows and Linux are checked by people and by CI starting it, not by its tests. `cargo check --target` from a Mac only works with that target's C tools (a resource compiler for Windows; a Linux C/C++ cross compiler and fontconfig for Linux), so CI is where it really builds. Building it on Linux needs a C compiler, pkg-config and the fontconfig, xkbcommon, xkbcommon-x11 and xcb development files (Wayland, Vulkan and EGL are loaded when the app starts); on Windows, the MSVC build tools with the Windows SDK (GPUI compiles its resource file and, for release builds, its DirectX shaders with them).
- PR Deck has no app icon yet, on any system: the Linux launcher uses the pull request icon from the app, and the Windows exe has none.

## Alternatives considered

- **Translate commands to cmd.exe or PowerShell.** Every custom command and the quoting would differ per system, and a PR title could break out of the quotes.
- **Windows Terminal through `wt.exe`.** It splits its command line at `;`, and a new tab may not get the caller's environment. A new console already opens in Windows Terminal when it is the default.
- **`clip.exe` for copying.** It reads text in the console code page unless fed UTF-16; the clipboard package Bubble Tea's text input already uses does it right.
- **Job objects to cancel on Windows.** Needs Windows-only code to create and assign the job; `taskkill /T` stops the same tree.
- **gpui-component's in-window menu bar on Windows and Linux.** A second bar under the title bar for menus whose items all have shortcuts; the one item without, Set Up…, got a button instead.
- **gpui-component's `TitleBar` for the header.** It draws the controls on Windows and Linux, but behaves differently on the Mac (height, drag and double click), which must not change.
