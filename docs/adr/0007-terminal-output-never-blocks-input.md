# 0007. Terminal output never blocks input

- Status: accepted
- Date: 2026-11-03

## Context

prdeck froze in one setup: Ghostty with a session recorder in between. A `sample` of all three processes showed a write deadlock: the terminal had stopped reading while busy, the recorder was blocked writing to it, and prdeck was blocked writing a frame. Bubble Tea writes frames while holding the lock its event loop needs, so a stuck write also stopped prdeck from reading keys; even `q` did nothing.

## Decision

`internal/termout` sits between Bubble Tea and the terminal:

- Writes go into memory and return at once; a separate goroutine writes them out. Only after 8 MB of unwritten output (hundreds of frames) does a write wait.
- Frames get smaller without looking different: long runs of spaces become "erase n, move right n" (ECH + CUF), and a reset followed by a style becomes one escape sequence.
- Redraws are capped at 30 a second.

## Consequences

- A terminal that stalls can no longer freeze the app; keys keep working and `q` quits.
- About a fifth less output while scrolling.
- If the terminal never comes back, output piles up in memory until the 8 MB limit.

## Alternatives considered

- **Lower the frame rate only.** Makes the freeze rarer, not impossible.
- **Patch Bubble Tea.** Its renderer holds the lock by design; a writer in front of it is less invasive.
