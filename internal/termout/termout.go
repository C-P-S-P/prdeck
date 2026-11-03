// Package termout is a buffered terminal writer for the UI.
//
// Bubble Tea writes each frame while holding the lock its event loop also needs, so a
// terminal that stops reading for a while (a busy recorder in between, a stalled
// terminal thread) also stops prdeck from reading keys and mouse input. When both sides
// wait on each other, everything freezes. Writer takes frames into memory and writes
// them to the terminal from its own goroutine, so input keeps flowing and `q` still
// quits.
//
// It also shrinks frames: long runs of spaces (pane padding) become "erase n
// characters, move right n", which looks the same and is a fraction of the bytes.
package termout

import (
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// maxPending is how much output may wait for a stalled terminal before Write blocks.
// That is a few hundred frames, far more than any burst of scrolling makes.
const maxPending = 8 << 20

// Writer is safe for concurrent use. It satisfies Bubble Tea's terminal file
// interface, so window size detection keeps working.
type Writer struct {
	f       *os.File
	mu      sync.Mutex
	cond    *sync.Cond
	pending []byte
	writing bool
	closed  bool
	err     error
}

// New starts a writer for f (usually os.Stdout).
func New(f *os.File) *Writer {
	w := &Writer{f: f}
	w.cond = sync.NewCond(&w.mu)
	go w.drain()
	return w
}

// Write queues p and returns at once, unless a stalled terminal already has
// maxPending bytes waiting.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(w.pending) > maxPending && !w.closed && w.err == nil {
		w.cond.Wait()
	}
	if w.err != nil {
		return 0, w.err
	}
	w.pending = append(w.pending, Compress(p)...)
	w.cond.Broadcast()
	return len(p), nil
}

func (w *Writer) drain() {
	for {
		w.mu.Lock()
		for len(w.pending) == 0 && !w.closed {
			w.cond.Wait()
		}
		if len(w.pending) == 0 && w.closed {
			w.mu.Unlock()
			return
		}
		chunk := w.pending
		w.pending, w.writing = nil, true
		w.mu.Unlock()

		_, err := w.f.Write(chunk)

		w.mu.Lock()
		w.writing = false
		if err != nil {
			w.err = err
		}
		w.cond.Broadcast()
		w.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// Flush waits until everything queued reached the terminal, or until timeout. It
// reports whether it got there.
func (w *Writer) Flush(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		w.mu.Lock()
		for (len(w.pending) > 0 || w.writing) && w.err == nil {
			w.cond.Wait()
		}
		w.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Close flushes (briefly) and stops the writer.
func (w *Writer) Close() error {
	w.Flush(2 * time.Second)
	w.mu.Lock()
	w.closed = true
	w.cond.Broadcast()
	w.mu.Unlock()
	return nil
}

func (w *Writer) Read(p []byte) (int, error) { return w.f.Read(p) }
func (w *Writer) Fd() uintptr                { return w.f.Fd() }

// minRun is the shortest run of spaces worth replacing; the replacement costs ~10 bytes.
const minRun = 12

// resetThenStyle is a reset immediately followed by another style, which can be one
// sequence: "\x1b[0m\x1b[1;34m" → "\x1b[0;1;34m".
var resetThenStyle = regexp.MustCompile(`\x1b\[0?m\x1b\[([0-9;:]*)m`)

// Compress makes a frame smaller without changing what it draws:
//   - runs of plain spaces become ECH + CUF ("\x1b[nX\x1b[nC"): erase n cells with the
//     current background and move past them (spaces inside escape sequences are kept);
//   - a style reset followed by a new style becomes one sequence.
func Compress(p []byte) []byte {
	if len(p) < minRun {
		return p
	}
	return resetThenStyle.ReplaceAll(compressSpaces(p), []byte("\x1b[0;${1}m"))
}

func compressSpaces(p []byte) []byte {
	out := make([]byte, 0, len(p))
	const (
		text = iota
		esc
		csi
		osc
		oscEsc
	)
	state := text
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch state {
		case text:
			if c == 0x1b {
				state = esc
				out = append(out, c)
				continue
			}
			if c == ' ' {
				j := i
				for j < len(p) && p[j] == ' ' {
					j++
				}
				if n := j - i; n >= minRun {
					s := strconv.Itoa(n)
					out = append(out, "\x1b["+s+"X\x1b["+s+"C"...)
					i = j - 1
					continue
				}
			}
			out = append(out, c)
		case esc:
			out = append(out, c)
			switch c {
			case '[':
				state = csi
			case ']':
				state = osc
			default:
				state = text
			}
		case csi:
			out = append(out, c)
			if c >= 0x40 && c <= 0x7e {
				state = text
			}
		case osc:
			out = append(out, c)
			switch c {
			case 0x07:
				state = text
			case 0x1b:
				state = oscEsc
			}
		case oscEsc:
			out = append(out, c)
			state = text
		}
	}
	return out
}
