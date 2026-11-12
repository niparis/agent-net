package daemon

import (
	"testing"
	"time"
)

func TestParsePSLineReadsOneProcess(t *testing.T) {
	line := "  4242     1   501 Ss   Tue Nov 11 04:51:02 2026   12345 /Applications/Codex.app/Contents/MacOS/Codex --type=renderer  --x"
	v, ok := parsePSLine(line)
	if !ok {
		t.Fatal("line not parsed")
	}
	if v.PID != 4242 || v.PPID != 1 || v.UID != 501 || v.EUID != 501 {
		t.Fatalf("ids: %+v", v)
	}
	if want := time.Date(2026, 11, 11, 4, 51, 2, 0, time.Local).UTC(); !v.Started.Equal(want) {
		t.Fatalf("started %v, want %v", v.Started, want)
	}
	if v.Token != "Tue Nov 11 04:51:02 2026" {
		t.Fatalf("token %q", v.Token)
	}
	if v.RSSKB != 12345 || v.Zombie {
		t.Fatalf("rss %d zombie %v", v.RSSKB, v.Zombie)
	}
	if want := "/Applications/Codex.app/Contents/MacOS/Codex --type=renderer  --x"; v.Command != want {
		t.Fatalf("command %q, want %q", v.Command, want)
	}
}

func TestParsePSLineZombieWithoutCommand(t *testing.T) {
	v, ok := parsePSLine("900 800 501 Z+ Mon Nov  9 10:00:00 2026 0 ")
	if !ok {
		t.Fatal("line not parsed")
	}
	if !v.Zombie || v.Command != "[unknown]" {
		t.Fatalf("zombie %v command %q", v.Zombie, v.Command)
	}
}

func TestParsePSLineRejectsWhatIsNotAProcess(t *testing.T) {
	for _, line := range []string{"", "   ", "PID PPID UID STAT STARTED RSS COMMAND", "x 1 501 S Tue Nov 11 04:51:02 2026 1 cmd"} {
		if v, ok := parsePSLine(line); ok {
			t.Errorf("%q parsed as %+v", line, v)
		}
	}
}
