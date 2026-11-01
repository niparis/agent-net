package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestCleanRateNoteKeepsOneLineAndCuts(t *testing.T) {
	long := "first line\nsecond\tline\x00 " + strings.Repeat("x", 400)
	got := CleanRateNote(long)
	if strings.ContainsAny(got, "\n\t\x00") {
		t.Fatalf("control characters survived: %q", got)
	}
	if len(got) > RateNoteMax {
		t.Fatalf("note is %d bytes, want at most %d: %q", len(got), RateNoteMax, got)
	}
	if !strings.HasPrefix(got, "first line second line ") {
		t.Fatalf("note was not collapsed: %q", got)
	}
	if got != strings.TrimSpace(got) {
		t.Fatalf("note has outer spaces: %q", got)
	}
}

func TestFormatRateAskNamesTheWorkerAndTheScale(t *testing.T) {
	line := FormatRateAsk("s_w1", "deepseek-v4.1-flash", "normal", 23*time.Minute)
	for _, want := range []string{"s_w1", "deepseek-v4.1-flash", "normal", "1 to 5", "one line"} {
		if !strings.Contains(line, want) {
			t.Errorf("rate ask lacks %q: %q", want, line)
		}
	}
	if strings.Contains(line, "\n") {
		t.Errorf("rate ask is more than one line: %q", line)
	}
}
