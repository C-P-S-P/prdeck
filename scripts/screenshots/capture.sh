#!/usr/bin/env bash
# Regenerates docs/screenshots/*.png from `prdeck demo` (sample data, nothing real).
# Needs: tmux, Google Chrome, python3 with Pillow.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HERE="$ROOT/scripts/screenshots"
OUT="$ROOT/docs/screenshots"
WORK="$(mktemp -d)"
CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
COLS=150 ROWS=40
SESSION="prdeck-shots-$$"

mkdir -p "$OUT"
go build -o "$WORK/prdeck" "$ROOT/cmd/prdeck"

cleanup() { tmux kill-session -t "$SESSION" 2>/dev/null || true; rm -rf "$WORK"; }
trap cleanup EXIT

# Default config only, true color, so every machine renders the same.
tmux new-session -d -s "$SESSION" -x "$COLS" -y "$ROWS" \
  "env PRDECK_CONFIG=/nonexistent COLORTERM=truecolor TERM=xterm-256color '$WORK/prdeck' demo"
sleep 2

shot() { # name, window title
  sleep "${3:-1}"
  tmux capture-pane -t "$SESSION" -e -p > "$WORK/$1.ansi"
  python3 "$HERE/ansi2html.py" "$WORK/$1.ansi" "$2" > "$WORK/$1.html"
  "$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 \
    --window-size=1400,900 --screenshot="$WORK/$1.raw.png" "file://$WORK/$1.html" >/dev/null 2>&1
  python3 - "$WORK/$1.raw.png" "$OUT/$1.png" <<'PY'
import sys
from PIL import Image, ImageChops
im = Image.open(sys.argv[1]).convert("RGB")
bg = Image.new("RGB", im.size, im.getpixel((2, 2)))
# Ignore the faint drop shadow so the margin is even on all sides.
diff = ImageChops.difference(im, bg).convert("L").point(lambda v: 255 if v > 24 else 0)
box = diff.getbbox()
pad = 48
l, t, r, b = box
im = im.crop((max(l - pad, 0), max(t - pad, 0), min(r + pad, im.width), min(b + pad, im.height)))
im.quantize(colors=256).save(sys.argv[2], optimize=True)  # a terminal has few colors; keeps files small
PY
  echo "wrote docs/screenshots/$1.png"
}

keys() { tmux send-keys -t "$SESSION" "$@"; }

shot main "prdeck — PRs you're involved in"

keys "/"
shot palette "prdeck — filter palette"
keys Escape

keys "r"
shot rebase "prdeck — rebase preview"
keys Enter
shot conflict "prdeck — rebase output" 1.5
keys Escape

keys "3"
shot review "prdeck — review requested" 1.5

keys "?"
shot help "prdeck — help"
