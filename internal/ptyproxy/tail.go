package ptyproxy

import (
	"bytes"
	"strings"
	"sync"
	"unicode/utf8"
)

// A command that fails often says why on the terminal it was handed, and that
// screen is gone when the TUI draws again. The proxy keeps the end of the
// command's output as plain text, so the TUI can name the command's own reason
// instead of only its exit status.

// tailSize is how much cleaned output is kept, in bytes.
const tailSize = 2048

// tailLineSize caps one line, so a command that never breaks a line cannot grow
// the buffer without limit. The end of the line is kept, which is what a
// failure needs.
const tailLineSize = 1024

// maxTailLine caps one line in the text the TUI shows, so the line last printed
// still fits beside the earlier one in a notice.
const maxTailLine = 160

// tailBuffer keeps the last tailSize bytes of the command's output with escape
// sequences and control characters removed.
type tailBuffer struct {
	mu    sync.Mutex
	done  []byte // finished lines, each ending in \n
	line  []byte // the line being printed
	cr    bool   // the line may still be overwritten by a carriage return
	state tailState
}

type tailState int

const (
	tailPlain   tailState = iota // printable text
	tailEscape                   // after ESC
	tailCSI                      // ESC [ ... final byte
	tailOSC                      // ESC ] ... BEL or ST
	tailString                   // DCS, SOS, PM, APC: ... ST
	tailST                       // after ESC in an OSC or string: maybe ST
	tailCharset                  // ESC ( ) * + - . / # %: one byte follows
)

// Write adds output from the command. It never fails.
func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range p {
		t.byteIn(c)
	}
	return len(p), nil
}

func (t *tailBuffer) byteIn(c byte) {
	switch t.state {
	case tailPlain:
		switch {
		case c == 0x1b:
			t.state = tailEscape
		case c == '\n':
			t.cr = false
			t.endLine()
		case c == '\r':
			// A carriage return redraws the line: keep it until the next byte
			// says whether the line ended or was overwritten.
			t.cr = true
		case c == '\t':
			t.startByte()
			t.line = append(t.line, ' ')
			t.capLine()
		case c >= 0x20 && c != 0x7f:
			t.startByte()
			t.line = append(t.line, c)
			t.capLine()
		}
	case tailEscape:
		switch c {
		case '[':
			t.state = tailCSI
		case ']':
			t.state = tailOSC
		case 'P', 'X', '^', '_':
			t.state = tailString
		case '(', ')', '*', '+', '-', '.', '/', '#', '%':
			t.state = tailCharset
		default:
			t.state = tailPlain
		}
	case tailCSI:
		// Parameter and intermediate bytes come first, then one final byte.
		if c >= 0x40 && c <= 0x7e {
			t.state = tailPlain
		}
	case tailOSC, tailString:
		switch c {
		case 0x07: // BEL ends an OSC
			t.state = tailPlain
		case 0x1b:
			t.state = tailST
		}
	case tailST:
		t.state = tailPlain
	case tailCharset:
		t.state = tailPlain
	}
}

// startByte drops a line the terminal was about to overwrite after a carriage
// return.
func (t *tailBuffer) startByte() {
	if t.cr {
		t.line = t.line[:0]
		t.cr = false
	}
}

// capLine keeps the end of a line that grows past tailLineSize.
func (t *tailBuffer) capLine() {
	if len(t.line) > tailLineSize {
		t.line = append(t.line[:0], t.line[len(t.line)-tailLineSize:]...)
	}
}

// endLine keeps the line just printed, without its trailing spaces.
func (t *tailBuffer) endLine() {
	line := strings.TrimRight(string(t.line), " ")
	t.line = t.line[:0]
	if line == "" {
		return
	}
	t.done = append(t.done, line...)
	t.done = append(t.done, '\n')
	if len(t.done) > tailSize {
		// Keep the end, and start at a line boundary when there is one, so the
		// oldest kept line is not cut in the middle.
		t.done = t.done[len(t.done)-tailSize:]
		if i := bytes.IndexByte(t.done, '\n'); i >= 0 {
			t.done = t.done[i+1:]
		}
	}
}

// lastLines is up to n of the last non-empty lines the command printed,
// joined by a space. It is "" when the command printed no plain text.
func (t *tailBuffer) lastLines(n int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n < 1 {
		return ""
	}
	lines := strings.Split(string(t.done), "\n")
	if pending := strings.TrimSpace(string(t.line)); pending != "" {
		lines = append(lines, pending)
	}
	kept := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			kept = append([]string{head(l, maxTailLine)}, kept...)
		}
	}
	return strings.Join(kept, " ")
}

// head is the start of s, at most n bytes and never half a rune.
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimSpace(s)
}
