# 0008. The desktop app is Rust and GPUI

- Status: accepted
- Date: 2026-11-03

## Context

The design is boxy and dense, like Zed: three panes, a command bar, a ⌘K palette, fast scrolling through long PR text with images. The app should open instantly and feel native on macOS.

## Decision

- [GPUI](https://www.gpui.rs), the GPU-accelerated UI framework Zed is built with, through [GPUI Kit](https://gpui-kit.com) 0.9, which pins a GPUI release and adds components: inputs, resizable panels, a markdown/HTML text view with selection, syntax highlighting.
- The Metal shaders compile when the app starts (`runtime_shaders`), so building needs no extra Xcode download.
- The app is one window with its own title bar (traffic lights inset), the design's colors as constants (`desktop/src/theme.rs`), Mac menus and standard shortcuts (⌘T, ⌘W, ⌘⇧T, ⌘1–9, ⌘,).
- Pane sizes and whether setup was done are kept in `~/Library/Application Support/prdeck/desktop.json`.
- `--features snapshot` builds a screenshot mode that renders the window to a PNG without showing it; the README images come from it.

## Consequences

- Native performance and a 43 MB app bundle, with no browser engine inside.
- GPUI is young and changes often; GPUI Kit pins a version, and the app pins GPUI Kit.
- Rust and Go are both needed to build the desktop app.

## Alternatives considered

- **Wails, Tauri or Electron** (a web UI). Quick to build, but a browser engine for a list and some text, and not the native feel asked for.
- **SwiftUI.** Native, but macOS only, and a third language for the team.
