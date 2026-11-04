#!/usr/bin/env python3
"""Turn a `tmux capture-pane -e` dump into an HTML page that looks like a terminal window.

Usage: ansi2html.py SCREEN.ansi "Window title" > screen.html
"""
import html
import re
import sys

BASE16 = [
    "#1b1d22", "#e57b7b", "#8cc47a", "#e0c070", "#6fa8f5", "#b98ee6", "#7fcfcf", "#c9cdd4",
    "#4a505c", "#f09a9a", "#a6d896", "#ecd08a", "#9cc4fa", "#d2b4f2", "#a0e0e0", "#ffffff",
]


def xterm256(n):
    if n < 16:
        return BASE16[n]
    if n < 232:
        n -= 16
        steps = [0, 95, 135, 175, 215, 255]
        return "#%02x%02x%02x" % (steps[n // 36], steps[(n // 6) % 6], steps[n % 6])
    v = 8 + (n - 232) * 10
    return "#%02x%02x%02x" % (v, v, v)


SGR = re.compile(r"\x1b\[([0-9;:]*)m")
# Escape sequences other than colors (cursor moves, modes); SGR ends in "m", so leave those.
OTHER = re.compile(r"\x1b\[[0-9;?]*[A-Za-ln-z]|\x1b[()][A-Z0-9]|\x1b[=>]")
# OSC sequences such as terminal hyperlinks (OSC 8): the linked text stays, the link goes.
OSC = re.compile(r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")


def color(params, i):
    """Read an extended color starting at params[i] (the 38/48). Returns (color, next index)."""
    if i + 1 < len(params) and params[i + 1] == "5" and i + 2 < len(params):
        return xterm256(int(params[i + 2])), i + 3
    if i + 1 < len(params) and params[i + 1] == "2" and i + 4 < len(params):
        r, g, b = (int(x) for x in params[i + 2 : i + 5])
        return "#%02x%02x%02x" % (r, g, b), i + 5
    return None, i + 1


def convert(text):
    st = {}
    out = []

    def apply(codes):
        params = [p for p in re.split(r"[;:]", codes)] if codes else ["0"]
        i = 0
        while i < len(params):
            p = params[i] or "0"
            n = int(p)
            if n == 0:
                st.clear()
            elif n == 1:
                st["bold"] = True
            elif n == 2:
                st["faint"] = True
            elif n == 3:
                st["italic"] = True
            elif n == 4:
                st["underline"] = True
            elif n == 7:
                st["reverse"] = True
            elif n == 22:
                st.pop("bold", None)
                st.pop("faint", None)
            elif n == 23:
                st.pop("italic", None)
            elif n == 24:
                st.pop("underline", None)
            elif n == 27:
                st.pop("reverse", None)
            elif 30 <= n <= 37:
                st["fg"] = BASE16[n - 30]
            elif 90 <= n <= 97:
                st["fg"] = BASE16[n - 90 + 8]
            elif n == 39:
                st.pop("fg", None)
            elif 40 <= n <= 47:
                st["bg"] = BASE16[n - 40]
            elif 100 <= n <= 107:
                st["bg"] = BASE16[n - 100 + 8]
            elif n == 49:
                st.pop("bg", None)
            elif n in (38, 48):
                c, i = color(params, i)
                if c:
                    st["fg" if n == 38 else "bg"] = c
                continue
            i += 1

    def style():
        fg, bg = st.get("fg"), st.get("bg")
        if st.get("reverse"):
            fg, bg = bg or "#0f1114", fg or "#d0d4da"
        css = []
        if fg:
            css.append("color:" + fg)
        if bg:
            css.append("background:" + bg)
        if st.get("bold"):
            css.append("font-weight:700")
        if st.get("faint"):
            css.append("opacity:.7")
        if st.get("italic"):
            css.append("font-style:italic")
        if st.get("underline"):
            css.append("text-decoration:underline")
        return ";".join(css)

    for line in text.rstrip("\n").split("\n"):
        line = OTHER.sub("", OSC.sub("", line))
        pos = 0
        for m in SGR.finditer(line):
            chunk = line[pos : m.start()]
            if chunk:
                s = style()
                out.append('<span style="%s">%s</span>' % (s, html.escape(chunk)) if s else html.escape(chunk))
            apply(m.group(1))
            pos = m.end()
        chunk = line[pos:]
        if chunk:
            s = style()
            out.append('<span style="%s">%s</span>' % (s, html.escape(chunk)) if s else html.escape(chunk))
        # tmux only writes a style when it changes, so styles carry over to the next line.
        out.append("\n")
    return "".join(out)


PAGE = """<!doctype html>
<html><head><meta charset="utf-8"><style>
html,body{margin:0;background:#0b0c0e}
body{display:flex;align-items:flex-start;justify-content:flex-start;padding:32px}
.win{background:#0f1114;border:1px solid #2c3038;border-radius:8px;overflow:hidden;box-shadow:0 18px 48px rgba(0,0,0,.55)}
.bar{height:30px;background:#16181c;border-bottom:1px solid #2c3038;display:flex;align-items:center;gap:8px;padding:0 12px}
.dot{width:12px;height:12px;border-radius:50%%}
.title{flex:1;text-align:center;color:#8a919d;font:12px -apple-system,BlinkMacSystemFont,sans-serif;margin-right:56px}
pre{margin:0;padding:10px 14px 12px;font:13px/1.18 Menlo,Monaco,monospace;color:#d0d4da;background:#0f1114}
</style></head><body><div class="win">
<div class="bar"><span class="dot" style="background:#ff5f57"></span><span class="dot" style="background:#febc2e"></span><span class="dot" style="background:#28c840"></span><span class="title">%s</span></div>
<pre>%s</pre></div></body></html>
"""

if __name__ == "__main__":
    src, title = sys.argv[1], sys.argv[2]
    with open(src, encoding="utf-8", errors="replace") as f:
        body = convert(f.read())
    sys.stdout.write(PAGE % (html.escape(title), body))
