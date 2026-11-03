#!/usr/bin/env bash
# Regenerates docs/screenshots/*.png from `prdeck demo` (sample data, nothing real).
# `capture.sh request blocked` writes only the shots named; the keys still run in order.
# Needs: tmux, Google Chrome, python3 with Pillow.
set -euo pipefail

ONLY="$*" # the shots to write; empty writes every one

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
  [[ -z "$ONLY" || " $ONLY " == *" $1 "* ]] || return 0
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

keys "n"
shot unread "prdeck — what changed since you last looked"
keys "g" Escape

keys "/"
shot palette "prdeck — filter palette"
keys Escape

keys "r"
shot changes "prdeck — uncommitted changes"
keys "s"
shot rebase "prdeck — rebase preview"
keys Enter
shot conflict "prdeck — rebase output" 1.5
keys Escape

keys "3"
shot review "prdeck — review requested" 1.5

keys "?"
shot help "prdeck — help"
keys "?"

keys "1"
sleep 1.5
keys "F"
shot rerun "prdeck — re-run failed checks" 1.5
keys Escape

keys "j"
sleep 1
keys "a"
shot approve "prdeck — approve"
keys Enter
sleep 1.5
keys "M"
sleep 0.5
keys Enter
shot merge "prdeck — merge when ready" 1.5
keys Escape

# #312 has an approval since you last looked; #1188 and #77 are still unread.
keys "j"
sleep 0.5
keys "j"
shot newreview "prdeck — approved since you last looked" 1.5

keys "j"
sleep 0.5
keys "j"
sleep 1
keys "x"
sleep 0.5
keys "Page size should come from the table settings, not a hard-coded 50"
shot request "prdeck — request changes"
keys Enter
shot requested "prdeck — changes requested" 1.5
keys Escape

keys "g"
sleep 1
keys "M"
sleep 0.5
keys Enter
shot blocked "prdeck — merge when ready, with conflicts" 1.5
