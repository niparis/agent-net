package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// wasWoken tells whether the gateway claimed a wake of the session. A claim is
// written before the wake frame goes out, so it is the state that proves a wake.
func wasWoken(t *testing.T, s *Store, session string) bool {
	t.Helper()
	var at int64
	if err := s.db.QueryRow(`SELECT wake_attempted_at FROM sessions WHERE id=?`, session).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at != 0
}

// markStopped sends the frame the session's machine sends after an owner or
// CTO stop.
func markStopped(t *testing.T, g *Gateway, node, session string) {
	t.Helper()
	raw, _ := json.Marshal(protocol.SessionRemove{SessionID: session})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, protocol.FrameSessionStopped, raw); err != nil {
		t.Fatalf("mark stopped: %v", err)
	}
}

// A session the owner or the CTO stopped is never woken by a message (the old
// homelab lead woke for six late replies). The sender is told why. Once the
// owner or the CTO starts it again, the mark is over and the normal wake
// returns.
func TestStoppedSessionIsNotWokenUntilStartedAgain(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeBackground, protocol.AutonomyWake)
	markStopped(t, p.g, p.node, id)

	m, started := p.sendMessage(t, id)
	if started || wasWoken(t, p.s, id) {
		t.Fatal("a message woke a session the owner stopped")
	}
	if reason := p.g.waitingReason(mustAgent(t, p.s, id), m); !strings.Contains(reason, "the owner or the CTO stopped it") {
		t.Fatalf("waiting reason = %q, want that the owner or the CTO stopped it", reason)
	}

	// The owner resumes it, and later it goes offline again.
	if _, err := p.s.UpsertSession(p.node, protocol.SessionInfo{Harness: "claude", NativeID: "claude-native", Cwd: "/w/proj", Mode: protocol.ModeBackground, Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := p.s.SetSessionOffline(p.node, id); err != nil {
		t.Fatal(err)
	}
	p.message(t, id)
	p.expectWake(t, id)
}

// Only the machine that holds a session may mark it stopped.
func TestOnlyTheSessionsMachineMarksItStopped(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeBackground, protocol.AutonomyWake)
	raw, _ := json.Marshal(protocol.SessionRemove{SessionID: id})
	other := mustAgent(t, p.s, p.from).NodeID
	if _, err := p.g.handle(context.Background(), &nodeConn{id: other}, protocol.FrameSessionStopped, raw); err == nil {
		t.Fatal("another machine marked the session stopped")
	}
}

// An archived session is never woken, by a message or by a ready step.
func TestArchivedSessionIsNeverWoken(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeBackground, protocol.AutonomyWake)
	// An archived session is hidden from the views, so keep the one from before.
	view := mustAgent(t, p.s, id)
	if _, err := p.s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{id}}); err != nil {
		t.Fatal(err)
	}
	if _, started := p.sendMessage(t, id); started {
		t.Fatal("a message woke an archived session")
	}
	if p.g.wakeSessionAs(context.Background(), view, false, false) {
		t.Fatal("a step wake woke an archived session")
	}
	if wasWoken(t, p.s, id) {
		t.Fatal("the gateway claimed a wake of an archived session")
	}
}

// When merger.set moves the merger role, the old merger is stood down for the
// new one (the old Fable merger woke for a reply five minutes after the move).
// A late message to it goes to the new merger, with a note that names the
// session it was sent to.
func TestMessageToAMovedMergerGoesToTheNewMerger(t *testing.T) {
	r := newReplaceRedirectRig(t)
	old, fresh := r.sonnet, r.caller
	for _, id := range []string{old, fresh} {
		if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: id}, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.s.SetSessionOffline(r.a, old); err != nil {
		t.Fatal(err)
	}
	m, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.deepseek, Recipient: old, Body: "late reply"})
	if err != nil {
		t.Fatal(err)
	}
	r.g.dispatch(context.Background(), m)
	got := r.waitDelivered(m.ID)
	if got.To != fresh {
		t.Fatalf("delivered to %s, want the new merger %s", got.To, fresh)
	}
	if len(got.CitedNotes) == 0 || !strings.Contains(got.CitedNotes[0], old) || !strings.Contains(got.CitedNotes[0], "merger role moved") {
		t.Fatalf("notes = %q, want a note that names %s and says the merger role moved", got.CitedNotes, old)
	}
	if wasWoken(t, r.s, old) {
		t.Fatal("the old merger was woken")
	}
	receipt, err := r.s.redirectReceipt(m)
	if err != nil || !strings.Contains(receipt, fresh) {
		t.Fatalf("sender receipt = %q (%v), want it to name %s", receipt, err, fresh)
	}
}

// When the merger role ends with no new merger, a late message to the old
// merger goes to the project lead, with the note.
func TestMessageToAnEndedMergerGoesToTheProjectLead(t *testing.T) {
	r := newReplaceRedirectRig(t)
	r.s.graphRefsOn.Store(true)
	old, lead := r.sonnet, r.caller
	if err := r.s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}, Lead: &protocol.GraphRef{Kind: "session", ID: lead}}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: old}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(r.a, old); err != nil {
		t.Fatal(err)
	}
	m, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.deepseek, Recipient: old, Body: "late reply"})
	if err != nil {
		t.Fatal(err)
	}
	r.g.dispatch(context.Background(), m)
	got := r.waitDelivered(m.ID)
	if got.To != lead {
		t.Fatalf("delivered to %s, want the project lead %s", got.To, lead)
	}
	if len(got.CitedNotes) == 0 || !strings.Contains(got.CitedNotes[0], old) || !strings.Contains(got.CitedNotes[0], "the lead of its project") {
		t.Fatalf("notes = %q, want a note that names %s and says it came as project lead", got.CitedNotes, old)
	}
	if wasWoken(t, r.s, old) {
		t.Fatal("the old merger was woken")
	}
}

// When the lead role moves to another session, the old lead session is stood
// down for the new one, and its late messages go there.
func TestMessageToAMovedLeadGoesToTheNewLead(t *testing.T) {
	r := newReplaceRedirectRig(t)
	r.s.graphRefsOn.Store(true)
	old, fresh := r.caller, r.sonnet
	for _, id := range []string{old, fresh} {
		if err := r.s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}, Lead: &protocol.GraphRef{Kind: "session", ID: id}}, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.s.SetSessionOffline(r.a, old); err != nil {
		t.Fatal(err)
	}
	m, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.deepseek, Recipient: old, Body: "late reply"})
	if err != nil {
		t.Fatal(err)
	}
	r.g.dispatch(context.Background(), m)
	got := r.waitDelivered(m.ID)
	if got.To != fresh {
		t.Fatalf("delivered to %s, want the new lead %s", got.To, fresh)
	}
	if len(got.CitedNotes) == 0 || !strings.Contains(got.CitedNotes[0], old) || !strings.Contains(got.CitedNotes[0], "lead role moved") {
		t.Fatalf("notes = %q, want a note that names %s and says the lead role moved", got.CitedNotes, old)
	}
	if wasWoken(t, r.s, old) {
		t.Fatal("the old lead was woken")
	}
}

func mustAgent(t *testing.T, s *Store, id string) protocol.AgentView {
	t.Helper()
	a, ok, err := s.Agent(id)
	if err != nil || !ok {
		t.Fatalf("session %s: ok=%v err=%v", id, ok, err)
	}
	return a
}

// A message is never handed back to its sender: the project lead's own reply
// to an ended merger waits under the merger, and the merger is not woken.
func TestLeadsReplyToAnEndedMergerIsNotHandedBack(t *testing.T) {
	r := newReplaceRedirectRig(t)
	r.s.graphRefsOn.Store(true)
	old, lead := r.sonnet, r.caller
	if err := r.s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}, Lead: &protocol.GraphRef{Kind: "session", ID: lead}}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: old}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(r.a, old); err != nil {
		t.Fatal(err)
	}
	m, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: lead, Recipient: old, Body: "thanks"})
	if err != nil {
		t.Fatal(err)
	}
	if r.g.dispatch(context.Background(), m) || wasWoken(t, r.s, old) {
		t.Fatal("the ended merger was woken")
	}
	stored, ok, err := r.s.Message(m.ID)
	if err != nil || !ok || stored.To != old {
		t.Fatalf("the reply is stored for %q, want it to wait for %s (%v)", stored.To, old, err)
	}
}
