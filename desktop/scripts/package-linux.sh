#!/usr/bin/env bash
# Packs PR Deck for Linux into desktop/dist/prdeck-desktop-<version>-linux-<arch>.tar.gz:
# the app, the prdeck Go core it starts, a .desktop launcher, an icon and install.sh.
# Usage (on Linux): desktop/scripts/package-linux.sh
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
root="$(cd "$here/.." && pwd)"

cargo build --release --locked --manifest-path "$here/Cargo.toml"
version="$(grep -m1 '^version' "$here/Cargo.toml" | cut -d'"' -f2)"
name="prdeck-desktop-$version-linux-$(uname -m)"
out="$here/dist/$name"
rm -rf "$out"
mkdir -p "$out"
cp "${CARGO_TARGET_DIR:-$here/target}/release/prdeck-desktop" "$out/prdeck-desktop"
(cd "$root" && go build -o "$out/prdeck" ./cmd/prdeck)
# The pull request icon PR Deck shows in its lists (Lucide, ISC), in the app's blue.
sed 's/currentColor/#6fa8f5/' "$here/assets/icons/git-pull-request.svg" > "$out/prdeck.svg"
cp "$here/assets/icons/LICENSE-LUCIDE" "$out/"

# The window's app id is "prdeck", so the desktop matches it to this launcher.
cat > "$out/prdeck.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=PR Deck
Comment=Your pull requests, with one-key git commands
Exec=prdeck-desktop
Icon=prdeck
Terminal=false
Categories=Development;
StartupWMClass=prdeck
DESKTOP

cat > "$out/install.sh" <<'INSTALL'
#!/bin/sh
# Installs PR Deck for you alone: the app and prdeck in ~/.local/bin (the app starts
# the prdeck next to it), the launcher and its icon in ~/.local/share.
set -e
cd "$(dirname "$0")"
bin="$HOME/.local/bin"
share="${XDG_DATA_HOME:-$HOME/.local/share}"
mkdir -p "$bin" "$share/applications" "$share/icons/hicolor/scalable/apps"
cp prdeck-desktop prdeck "$bin/"
cp prdeck.svg "$share/icons/hicolor/scalable/apps/"
sed "s|^Exec=.*|Exec=\"$bin/prdeck-desktop\"|" prdeck.desktop > "$share/applications/prdeck.desktop"
echo "Installed: PR Deck is in your applications, and prdeck (the terminal app) in $bin."
INSTALL
chmod +x "$out/install.sh"

cat > "$out/README.txt" <<README
PR Deck $version for Linux

  ./install.sh      adds PR Deck to your applications (files go to ~/.local)
  ./prdeck-desktop  runs it from here; keep prdeck next to it

Needs git and the GitHub CLI (gh); PR Deck's first-run setup says how to install
gh on your distribution. Drawing uses Vulkan, or OpenGL where Vulkan is missing
(Mesa provides both). Works on Wayland and X11.

https://github.com/MasterPushers/prdeck
README

tar -C "$here/dist" -czf "$here/dist/$name.tar.gz" "$name"
echo "Built $here/dist/$name.tar.gz"
