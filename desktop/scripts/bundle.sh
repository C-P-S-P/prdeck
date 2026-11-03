#!/usr/bin/env bash
# Builds "PR Deck.app" (the desktop app plus the prdeck Go core) into desktop/dist/.
# Usage: desktop/scripts/bundle.sh   then open "desktop/dist/PR Deck.app"
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
root="$(cd "$here/.." && pwd)"

cargo build --release --manifest-path "$here/Cargo.toml"
app="$here/dist/PR Deck.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
cp "$here/target/release/prdeck-desktop" "$app/Contents/MacOS/PR Deck"
(cd "$root" && go build -o "$app/Contents/MacOS/prdeck" ./cmd/prdeck)
version="$(grep -m1 '^version' "$here/Cargo.toml" | cut -d'"' -f2)"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>PR Deck</string>
  <key>CFBundleDisplayName</key><string>PR Deck</string>
  <key>CFBundleIdentifier</key><string>dev.prdeck.desktop</string>
  <key>CFBundleExecutable</key><string>PR Deck</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>$version</string>
  <key>CFBundleShortVersionString</key><string>$version</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
echo "Built $app"
