package ptyproxy

import (
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// bar is the one-row status line agent-net keeps at the bottom of the real
// terminal while an entered session owns the screen. The child PTY is one row
// shorter, so the bar is out of the child's way. Only a clear-screen from the
// child can reach it; the bar is drawn again after one, and after a resize.
type bar struct {
	mu      sync.Mutex
	out     io.Writer
	text    string
	cols    int
	rows    int
	enabled bool
	closed  bool
	dirty   bool
	timer   *time.Timer
	tail    []byte // the end of the last child output, for split clears
}

// barRedrawDelay is how long the bar waits after a clear so the child can
// finish repainting the screen before agent-net draws over the last row.
const barRedrawDelay = 40 * time.Millisecond

// barTailSize covers the longest sequence clearsScreen looks for.
const barTailSize = 8

// barText is the line the owner sees. It names the way back first.
func barText(name string) string {
	return "agent-net · ctrl+] back to agent-net · " + name
}

// newBar builds the bar. AGENT_NET_NO_BAR=1 turns it off for a terminal that
// mishandles the row.
func newBar(out io.Writer, name string) *bar {
	return &bar{
		out:     out,
		text:    barText(name),
		enabled: os.Getenv("AGENT_NET_NO_BAR") != "1",
		tail:    make([]byte, 0, barTailSize),
	}
}

// barWriter sends the child's output through the bar, so a clear-screen
// redraws the bar and no escape sequence is ever split by one.
type barWriter struct{ b *bar }

func (w barWriter) Write(p []byte) (int, error) { return w.b.writeOutput(p) }

// rowReserved reports whether the child's PTY should be one row shorter.
func (b *bar) rowReserved() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.enabled && !b.closed
}

// writeOutput passes the child's output through and draws the bar again when
// that output cleared the screen.
func (b *bar) writeOutput(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.out.Write(p)
	if b.enabled && !b.closed {
		window := append(append(make([]byte, 0, len(b.tail)+len(p)), b.tail...), p...)
		if clearsScreen(window) {
			b.scheduleLocked()
		}
		if len(window) > barTailSize {
			window = window[len(window)-barTailSize:]
		}
		b.tail = append(b.tail[:0], window...)
	}
	return n, err
}

// resize gives the bar the new terminal size and draws it. The old row is
// erased when the height changed, because the terminal may have moved it.
func (b *bar) resize(cols, rows int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.enabled && !b.closed && b.rows >= 2 && b.rows != rows {
		b.clearLocked()
	}
	b.cols, b.rows = cols, rows
	b.drawLocked()
}

// close erases the bar and stops any pending redraw.
func (b *bar) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.clearLocked()
	b.closed = true
	b.dirty = false
}

func (b *bar) scheduleLocked() {
	b.dirty = true
	delay := barRedrawDelay
	if b.timer == nil {
		b.timer = time.AfterFunc(delay, b.redraw)
		return
	}
	b.timer.Reset(delay)
}

func (b *bar) redraw() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || !b.dirty {
		return
	}
	b.drawLocked()
}

// drawLocked writes the bar on the last row and leaves the child's cursor
// where it was. The row is filled to the full width so old text cannot show.
func (b *bar) drawLocked() {
	b.dirty = false
	if !b.enabled || b.closed || b.rows < 2 || b.cols < 1 {
		return
	}
	text := ansi.Truncate(b.text, b.cols, "")
	if w := ansi.StringWidth(text); w < b.cols {
		text += strings.Repeat(" ", b.cols-w)
	}
	_, _ = io.WriteString(b.out, "\x1b7\x1b["+strconv.Itoa(b.rows)+";1H\x1b[2m"+text+"\x1b[0m\x1b8")
}

func (b *bar) clearLocked() {
	if !b.enabled || b.rows < 2 || b.cols < 1 {
		return
	}
	_, _ = io.WriteString(b.out, "\x1b7\x1b["+strconv.Itoa(b.rows)+";1H\x1b[2K\x1b8")
}

// screenClearers are the sequences that wipe the row the bar is on, or that
// switch to a screen where it was never drawn. The window passed in covers the
// end of the previous write too, so a sequence split across writes still
// counts.
var screenClearers = []string{
	"\x1b[2J", "\x1b[3J", "\x1b[J", "\x1b[0J",
	"\x1b[?47h", "\x1b[?47l",
	"\x1b[?1047h", "\x1b[?1047l",
	"\x1b[?1049h", "\x1b[?1049l",
	"\x1bc",
}

func clearsScreen(p []byte) bool {
	for _, seq := range screenClearers {
		if strings.Contains(string(p), seq) {
			return true
		}
	}
	return false
}
