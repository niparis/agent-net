package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// eventually polls cond until it holds, for at most two seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// failures counts the recorded wake failures of a session.
func (p *wakeProbe) failures(id string) int {
	var n int
	if err := p.s.db.QueryRow(`SELECT COUNT(*) FROM session_wake_failures WHERE session_id=?`, id).Scan(&n); err != nil {
		return 0
	}
	return n
}

// notices returns the bodies of agent-net's notices to a session.
func (p *wakeProbe) notices(t *testing.T, to string) []string {
	t.Helper()
	rows, err := p.s.db.Query(`SELECT body FROM messages WHERE sender='agent-net' AND recipient=? ORDER BY rowid`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		out = append(out, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// With autonomy off, a message to an offline session asks no machine to wake
// it, and the sender's receipt says that wake is off. A Claude background
// session is still woken without autonomy, so it gets no such line.
func TestMessageReceiptSaysWakeIsOff(t *testing.T) {
	for _, tc := range []struct{ name, harness, mode string }{
		{"interactive claude", "claude", protocol.ModeForeground},
		{"claude of unknown mode", "claude", ""},
		{"codex", "codex", ""},
		{"opencode", "opencode", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newWakeProbe(t)
			id := p.offline(t, tc.harness, tc.mode, protocol.AutonomyOff)
			m := p.message(t, id)
			p.expectNoWake(t)
			want := "queued; proj is offline and wake is off for this agent"
			if note := p.g.wakeReceiptNote(m, ""); note != want {
				t.Fatalf("note = %q, want %q", note, want)
			}
			if n := len(p.notices(t, p.from)); n != 0 {
				t.Fatalf("sender got %d notices for a message that waits", n)
			}
		})
	}
}

// A wake that the machine refuses for a real reason is kept. The sender with a
// queued message is told once. Later receipts say why the session is still
// asleep. A gateway restart does not retry the wake: only a new message asks.
func TestWakeFailureIsKeptAndSenderIsToldOnce(t *testing.T) {
	const reason = "Claude background record is missing, ambiguous, or not retired"
	p := newWakeProbeRefusing(t, reason)
	id := p.offline(t, "claude", protocol.ModeBackground, protocol.AutonomyOff)
	p.message(t, id)
	p.expectWake(t, id)
	eventually(t, "failure kept", func() bool { return p.failures(id) == 1 })
	if notes := p.notices(t, p.from); len(notes) != 1 || !strings.Contains(notes[0], reason) {
		t.Fatalf("sender notices = %q", notes)
	}

	// Inside the ten-minute limit a second message does not ask again, and its
	// receipt says why the session is still asleep.
	m, _, err := send(p.s, p.from, id, "second", "")
	if err != nil {
		t.Fatal(err)
	}
	p.g.dispatch(context.Background(), m)
	p.expectNoWake(t)
	if note := p.g.wakeReceiptNote(m, ""); !strings.Contains(note, "last wake failed: "+reason) {
		t.Fatalf("receipt = %q", note)
	}

	// The limit has passed, and the gateway restarts with the messages still
	// queued. Nothing new asks for a wake, so none is made.
	if _, err := p.s.db.Exec(`UPDATE retired_session_wakes SET attempted_at=0`); err != nil {
		t.Fatal(err)
	}
	p.g.wakePending(context.Background(), p.node)
	p.expectNoWake(t)
	if n := len(p.notices(t, p.from)); n != 1 {
		t.Fatalf("sender notices = %d, want 1", n)
	}

	// A new message after the limit asks again, and a wake that works clears the failure.
	p.setRefusal("")
	m, _, err = send(p.s, p.from, id, "third", "")
	if err != nil {
		t.Fatal(err)
	}
	p.g.dispatch(context.Background(), m)
	p.expectWake(t, id)
	eventually(t, "failure cleared", func() bool { return p.failures(id) == 0 })
}
