package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// closedOpenCode adds an OpenCode session that the owner closed: offline with
// status 'ended', the state the dotfiles session had.
func (p *wakeProbe) closedOpenCode(t *testing.T) string {
	t.Helper()
	id := p.offline(t, "opencode", "", protocol.AutonomyOff)
	if err := p.s.SetSessionClosed(p.node, id, true); err != nil {
		t.Fatal(err)
	}
	return id
}

// A request to an ended OpenCode session with wake on asks its machine to wake it.
func TestRequestToEndedOpenCodeSessionWakesIt(t *testing.T) {
	p := newWakeProbe(t)
	id := p.closedOpenCode(t)
	m, _, err := p.s.StoreMessage(protocol.MsgSend{Sender: p.from, Recipient: id, Type: protocol.TypeRequest, Body: "do it", IdempotencyKey: "req-ended"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.g.dispatch(context.Background(), m) {
		t.Fatal("dispatch did not start a wake")
	}
	p.expectWake(t, id)
}

// agentNetNotices returns the bodies of the notices agent-net stored for a session.
func (p *wakeProbe) agentNetNotices(t *testing.T, to string) []string {
	t.Helper()
	rows, err := p.s.db.Query(`SELECT body FROM messages WHERE sender='agent-net' AND recipient=? ORDER BY rowid`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// The dotfiles case: the machine answers a wake with success and starts
// nothing, so the session stays offline. The sender is told within the wait,
// not at the 24 hour expiry.
func TestWakeAcceptedButSessionStaysOfflineTellsSender(t *testing.T) {
	p := newWakeProbe(t)
	p.g.wakeOnlineWait = 200 * time.Millisecond
	id := p.closedOpenCode(t)
	p.message(t, id)
	p.expectWake(t, id)
	eventually(t, "the sender is told the session did not come online", func() bool {
		for _, n := range p.agentNetNotices(t, p.from) {
			if strings.Contains(n, "did not come online") {
				return true
			}
		}
		return false
	})
	if reason, ok := p.s.wakeFailure(id); !ok || !strings.Contains(reason, "did not come online") {
		t.Fatalf("wake failure = %q, %v", reason, ok)
	}
}

// A worker that has ended is never woken for a message. The sender hears it
// on the first dispatch, once.
func TestMessageToEndedWorkerTellsSenderAtOnce(t *testing.T) {
	p := newWakeProbe(t)
	id, err := p.s.UpsertSession(p.node, protocol.SessionInfo{Harness: "opencode", NativeID: "w-native", Cwd: "/w/proj",
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: p.from, RunGeneration: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.s.SetSessionOffline(p.node, id); err != nil {
		t.Fatal(err)
	}
	m := p.message(t, id)
	p.g.dispatch(context.Background(), m)
	p.expectNoWake(t)
	notices := p.agentNetNotices(t, p.from)
	if len(notices) != 1 || !strings.Contains(notices[0], "ended worker") || !strings.Contains(notices[0], m.ID) {
		t.Fatalf("notices = %q, want one about the ended worker", notices)
	}
}

// With wake off, the message waits for the session to start by itself, and the
// sender is told so at once.
func TestMessageToSessionWithWakeOffTellsSenderAtOnce(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyOff)
	setWakeOnMessage(t, p.s, id, protocol.WakeOnMessageOff)
	p.message(t, id)
	p.expectNoWake(t)
	notices := p.agentNetNotices(t, p.from)
	if len(notices) != 1 || !strings.Contains(notices[0], "wake is off for this agent") {
		t.Fatalf("notices = %q, want one about wake being off", notices)
	}
}

// A message that finds the session's last wake failed says so, also for a
// sender that had nothing queued when the wake failed.
func TestMessageAfterFailedWakeTellsItsSender(t *testing.T) {
	p := newWakeProbe(t)
	id := p.closedOpenCode(t)
	if claimed, err := p.s.claimWake(id); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if _, err := p.s.failWake(id, "attempt-1", "the OpenCode service is not running"); err != nil {
		t.Fatal(err)
	}
	p.message(t, id)
	notices := p.agentNetNotices(t, p.from)
	if len(notices) != 1 || !strings.Contains(notices[0], "last wake failed: the OpenCode service is not running") {
		t.Fatalf("notices = %q", notices)
	}
}

// A message held for a busy session whose turn never ends, because the session
// went offline, is not left to expire: the next sweep dispatches it and the
// session is woken.
func TestBusyHoldOfSessionThatWentOfflineIsReleased(t *testing.T) {
	p := newWakeProbe(t)
	id, err := p.s.UpsertSession(p.node, protocol.SessionInfo{Harness: "claude", NativeID: "work", Cwd: "/w/proj", Name: "work", Status: "busy", Mode: protocol.ModeBackground})
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := send(p.s, p.from, id, "held", "")
	if err != nil {
		t.Fatal(err)
	}
	if !p.s.HoldForBusy(m) {
		t.Fatal("message not held for the busy session")
	}
	if err := p.s.SetSessionOffline(p.node, id); err != nil {
		t.Fatal(err)
	}
	p.g.sweepBusyHolds(context.Background())
	p.expectWake(t, id)
	got, ok, err := p.s.Message(m.ID)
	if err != nil || !ok || got.HeldForBusy || got.State != protocol.StateStored {
		t.Fatalf("message after sweep: held=%v state=%s ok=%v err=%v", got.HeldForBusy, got.State, ok, err)
	}
}

// A wake that worked but took longer than the online wait is recorded as failed.
// When the session then registers online, the record must go: otherwise the next
// time it is offline, senders hear at once that it will not be woken.
func TestSessionComingOnlineClearsRecordedWakeFailure(t *testing.T) {
	p := newWakeProbe(t)
	id := p.closedOpenCode(t)
	if _, err := p.s.failWake(id, "attempt-late", "the machine accepted the wake, but the session did not come online within 30s"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.s.UpsertSession(p.node, protocol.SessionInfo{Harness: "opencode", NativeID: "opencode-native", Cwd: "/w/proj", Name: "opencode", Status: "idle",
		// The OpenCode server reports activity after the owner closed the session.
		OpenCodeActivity: p.s.now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if reason, ok := p.s.wakeFailure(id); ok {
		t.Fatalf("wake failure still recorded after the session came online: %q", reason)
	}
	if err := p.s.SetSessionOffline(p.node, id); err != nil {
		t.Fatal(err)
	}
	// A new message asks for a wake again and does not say the last wake failed.
	if _, started := p.sendMessage(t, id); !started {
		t.Fatal("the message did not start a wake")
	}
	for _, n := range p.agentNetNotices(t, p.from) {
		if strings.Contains(n, "last wake failed") {
			t.Fatalf("early notice blames an old wake: %q", n)
		}
	}
}

// The failure notice says what happens next, with the real backoff.
func TestWakeFailureNoticeSaysNextMessageAsksAgain(t *testing.T) {
	p := newWakeProbe(t)
	id := p.closedOpenCode(t)
	p.message(t, id)
	p.expectWake(t, id)
	if _, err := p.s.failWake(id, "attempt-x", "boom"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, n := range p.agentNetNotices(t, p.from) {
		if strings.Contains(n, "could not be woken") {
			found = true
			if !strings.Contains(n, "next message sent after 10 minutes") {
				t.Fatalf("notice does not say when agent-net asks again: %q", n)
			}
		}
	}
	if !found {
		t.Fatal("no failure notice")
	}
}

// The wait for a session to come online is no wake in flight. A wake is over
// when the machine answers, so a second message during the wait neither joins
// the wake nor starts another, and its sender hears nothing yet: no failure is
// recorded until the wait ends.
func TestOnlineWaitIsNotAWakeInFlight(t *testing.T) {
	p := newWakeProbe(t)
	p.g.wakeOnlineWait = time.Minute
	id := p.closedOpenCode(t)
	p.message(t, id)
	p.expectWake(t, id)
	eventually(t, "wake over once the machine answered", func() bool { return !p.g.wakeInFlight(id) })
	m := p.message(t, id)
	a, ok, err := p.s.Agent(id)
	if err != nil || !ok {
		t.Fatalf("agent: %v %v", ok, err)
	}
	if reason := p.g.waitingReason(a, m); reason != "" {
		t.Fatalf("waitingReason during the online wait = %q, want none", reason)
	}
	p.expectNoWake(t)
	if n := p.failures(id); n != 0 {
		t.Fatalf("wake failures = %d, want 0 while the session may still come online", n)
	}
}
