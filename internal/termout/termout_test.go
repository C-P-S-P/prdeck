package termout

import (
	"bytes"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCompress(t *testing.T) {
	sp := strings.Repeat(" ", 40)
	cases := map[string]string{
		"a" + sp + "b":                      "a\x1b[40X\x1b[40Cb",
		"short   gap":                       "short   gap",
		"\x1b[48;2;1;2;3m" + sp + "\x1b[0m": "\x1b[48;2;1;2;3m\x1b[40X\x1b[40C\x1b[0m",
		// Spaces inside an OSC payload or a CSI are never touched.
		"\x1b]2;title" + sp + "\x07x":                    "\x1b]2;title" + sp + "\x07x",
		"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\" + sp: "\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\\x1b[40X\x1b[40C",
		// A reset then a style merge; a reset then a non-style sequence does not.
		"\x1b[0m\x1b[1;38;2;1;2;3mbold text here": "\x1b[0;1;38;2;1;2;3mbold text here",
		"\x1b[0m\x1b[K and erase the rest":        "\x1b[0m\x1b[K and erase the rest",
		"\x1b[m\x1b[4munderlined words here":      "\x1b[0;4munderlined words here",
	}
	for in, want := range cases {
		if got := string(Compress([]byte(in))); got != want {
			t.Errorf("Compress(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestWriterKeepsOrderAndFlushes(t *testing.T) {
	r, wf, _ := os.Pipe()
	w := New(wf)
	var want bytes.Buffer
	for i := 0; i < 200; i++ {
		line := []byte(strings.Repeat("x", i%7) + "\n")
		want.Write(line)
		w.Write(line)
	}
	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); got <- b }()
	if !w.Flush(time.Second) {
		t.Fatal("flush timed out")
	}
	w.Close()
	wf.Close()
	if b := <-got; !bytes.Equal(b, want.Bytes()) {
		t.Errorf("output differs: got %d bytes, want %d", len(b), want.Len())
	}
}

// A terminal that stops reading must not block Write: that is what froze the UI.
func TestWriteDoesNotBlockOnStalledTerminal(t *testing.T) {
	r, wf, _ := os.Pipe() // nobody ever reads this pipe
	defer r.Close()
	w := New(wf)
	frame := bytes.Repeat([]byte("frame "), 2000) // 12 KB, more than a pipe buffer holds
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			w.Write(frame)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on a terminal that stopped reading")
	}
	if w.Flush(100 * time.Millisecond) {
		t.Error("flush should time out while the terminal is stalled")
	}
	runtime.KeepAlive(r)
}
