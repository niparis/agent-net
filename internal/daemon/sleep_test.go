package daemon

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSleepClockJump(t *testing.T) {
	base := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	w := &sleepWatch{
		lastWall: base,
		read:     func(time.Time) []protocol.NodeSleep { return nil },
	}
	// A normal tick: the wall and monotonic clocks moved together.
	if got := w.observe(base.Add(30*time.Second), 30*time.Second); len(got) != 0 {
		t.Fatalf("normal tick reported %+v", got)
	}
	// Two hours of wall clock in thirty seconds of monotonic clock.
	woke := base.Add(30*time.Second + 2*time.Hour)
	got := w.observe(woke, 60*time.Second)
	if len(got) != 1 {
		t.Fatalf("clock jump reported %d spans, want one: %+v", len(got), got)
	}
	if got[0].Source != "clock" {
		t.Fatalf("span source = %q, want clock", got[0].Source)
	}
	if got[0].From != base.Add(30*time.Second).UnixNano() || got[0].To != woke.UnixNano() {
		t.Fatalf("span = %+v, want %s to %s", got[0], base.Add(30*time.Second), woke)
	}
}

func TestSleepDetectorUsesOSSpan(t *testing.T) {
	base := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	from := base.Add(-time.Minute)
	to := base.Add(20 * time.Minute)
	w := &sleepWatch{
		lastWall: base,
		read: func(since time.Time) []protocol.NodeSleep {
			return []protocol.NodeSleep{{From: from.UnixNano(), To: to.UnixNano(), Source: "pmset"}}
		},
	}
	got := w.observe(to.Add(time.Second), 30*time.Second)
	if len(got) != 1 || got[0].Source != "pmset" {
		t.Fatalf("spans = %+v, want the OS span", got)
	}
	if got[0].From != from.UnixNano() || got[0].To != to.UnixNano() {
		t.Fatalf("span = %+v, want %s to %s", got[0], from, to)
	}
}

func TestParsePMSetLog(t *testing.T) {
	const log = `2026-11-12 08:00:00 +0100 Sleep  Entering Sleep state due to 'Clamshell Sleep':TCPKeepAlive=disabled
2026-11-12 09:15:00 +0100 Wake   DarkWake from Normal Sleep [CDN]: due to EC.ACAttach
2026-11-12 10:00:00 +0100 Sleep  Entering Sleep state due to 'Idle Sleep'
2026-11-12 10:30:00 +0100 Wake   Wake from Normal Sleep [CDNVA] due to HID Activity
`
	since := time.Date(2026, 11, 12, 9, 30, 0, 0, time.FixedZone("CET", 3600))
	got := parsePMSetLog(log, since)
	if len(got) != 1 {
		t.Fatalf("spans = %+v, want one since 09:30", got)
	}
	if got[0].Source != "pmset" {
		t.Fatalf("source = %q", got[0].Source)
	}
	wantFrom := time.Date(2026, 11, 12, 10, 0, 0, 0, time.FixedZone("CET", 3600))
	wantTo := time.Date(2026, 11, 12, 10, 30, 0, 0, time.FixedZone("CET", 3600))
	if got[0].From != wantFrom.UnixNano() || got[0].To != wantTo.UnixNano() {
		t.Fatalf("span = %+v, want %s to %s", got[0], wantFrom, wantTo)
	}
}

func TestParseJournalSleep(t *testing.T) {
	const log = `2026-11-12T10:00:00+0100 host kernel: PM: suspend entry (deep)
2026-11-12T10:30:00+0100 host kernel: PM: suspend exit
`
	since := time.Date(2026, 11, 12, 9, 0, 0, 0, time.FixedZone("CET", 3600))
	got := parseJournalSleep(log, since)
	if len(got) != 1 {
		t.Fatalf("spans = %+v, want one", got)
	}
	if got[0].Source != "journal" {
		t.Fatalf("source = %q", got[0].Source)
	}
	wantFrom := time.Date(2026, 11, 12, 10, 0, 0, 0, time.FixedZone("CET", 3600))
	wantTo := time.Date(2026, 11, 12, 10, 30, 0, 0, time.FixedZone("CET", 3600))
	if got[0].From != wantFrom.UnixNano() || got[0].To != wantTo.UnixNano() {
		t.Fatalf("span = %+v, want %s to %s", got[0], wantFrom, wantTo)
	}
}
