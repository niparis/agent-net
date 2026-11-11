package ptyproxy

import (
	"strings"
	"testing"
)

// The tail is what a failure message shows, so it must be plain text: no
// escape sequences, no control characters, and no more than the last lines.
func TestTailStripsEscapeSequences(t *testing.T) {
	var b tailBuffer
	for _, part := range []string{
		"\x1b[2J\x1b[31mfirst\x1b[0m line\r\n",
		"\x1b]0;a title\x07second\tline\n",
		"\x1b(Bcharset\x1b[?25l\n",
		"\x1b[3", // split in the middle of a sequence
		"1mred\n",
		"\x00\x07\x08control\x7f\n",
	} {
		if _, err := b.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := b.lastLines(2), "red control"; got != want {
		t.Fatalf("last lines %q, want %q", got, want)
	}
	if got, want := b.lastLines(10), "first line second line charset red control"; got != want {
		t.Fatalf("all lines %q, want %q", got, want)
	}
}

// A line without a final newline is still the last line, as printf leaves it.
func TestTailKeepsAnUnterminatedLastLine(t *testing.T) {
	var b tailBuffer
	b.Write([]byte("booting\n\x1b[1merror: terminal already has an attached client"))
	if got, want := b.lastLines(2), "booting error: terminal already has an attached client"; got != want {
		t.Fatalf("last lines %q, want %q", got, want)
	}
}

// Only the end is kept: the buffer is bounded, and the newest lines win.
func TestTailKeepsOnlyTheEnd(t *testing.T) {
	var b tailBuffer
	b.Write([]byte("oldest line\n"))
	b.Write([]byte(strings.Repeat("x", 4000) + "\n"))
	b.Write([]byte(strings.Repeat("y", 4000) + "\n"))
	b.Write([]byte("newest line\n"))
	got := b.lastLines(2)
	if strings.Contains(got, "oldest") {
		t.Fatalf("old output kept: %q", got)
	}
	if !strings.HasSuffix(got, "newest line") {
		t.Fatalf("newest line lost: %q", got)
	}
	if len(b.done) > tailSize || len(b.line) > tailLineSize {
		t.Fatalf("kept %d bytes of lines and %d of one line", len(b.done), len(b.line))
	}
	if len(got) > 2*maxTailLine+1 {
		t.Fatalf("notice text is %d bytes: %q", len(got), got)
	}
}

// A carriage return redraws a line, so the redrawn text replaces it.
func TestTailReplacesALineRedrawnWithCarriageReturn(t *testing.T) {
	var b tailBuffer
	b.Write([]byte("progress 1\rprogress 2\rerror: attach failed\n"))
	if got, want := b.lastLines(2), "error: attach failed"; got != want {
		t.Fatalf("last lines %q, want %q", got, want)
	}
	b.Write([]byte("plain\r\nnext\r\n"))
	if got, want := b.lastLines(2), "plain next"; got != want {
		t.Fatalf("CRLF last lines %q, want %q", got, want)
	}
}
