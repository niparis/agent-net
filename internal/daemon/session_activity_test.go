package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func testSession(t *testing.T, n *node, native string) *localSession {
	t.Helper()
	n.d.mu.Lock()
	defer n.d.mu.Unlock()
	ls := n.d.sessions[key("pi", native)]
	if ls == nil {
		t.Fatalf("session %s is not known", native)
	}
	return ls
}

func TestSessionLastActiveSurvivesRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "pi"}})
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "s1", Status: "idle", Cwd: "/work/x"}})

	active := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	n.d.touchSession(testSession(t, n, "s1"), active)
	if got := n.d.infoOf(testSession(t, n, "s1")).LastActive; got != active.Unix() {
		t.Fatalf("last active before restart: %d, want %d", got, active.Unix())
	}

	n.restart()
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "s1", Status: "idle", Cwd: "/work/x"}})
	ls := testSession(t, n, "s1")
	if got := n.d.infoOf(ls).LastActive; got != active.Unix() {
		t.Fatalf("last active after restart: %d, want %d", got, active.Unix())
	}

	// The same discovery report again must not move it, even though the
	// daemon was restarted between the two.
	before := n.d.infoOf(ls).LastActive
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "s1", Status: "idle", Cwd: "/work/x"}})
	if got := n.d.infoOf(testSession(t, n, "s1")).LastActive; got != before {
		t.Fatalf("a re-report moved last active from %d to %d", before, got)
	}

	// A real status change is activity.
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "s1", Status: "busy", Cwd: "/work/x"}})
	if got := n.d.infoOf(testSession(t, n, "s1")).LastActive; got <= before {
		t.Fatalf("a status change did not move last active: %d, still %d", got, before)
	}
}

// A local caller alias can share the owner's gateway ID. Message activity must
// not upsert that alias as a new session and change which owner its workers see.
func TestMessageActivityKeepsSharedGatewayID(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "pi"}})
	eventually(t, "owner session", func() bool { return n.d.ownerSessionID() != "" })
	owner := n.d.ownerSessionID()
	n.d.SetSessions("pi", []protocol.SessionInfo{{NativeID: "recipient", Status: "idle"}})
	recipient := n.d.SessionID("pi", "recipient")
	alias := &localSession{info: protocol.SessionInfo{Harness: "claude", NativeID: "caller-alias"}, sessionID: owner}
	n.d.mu.Lock()
	n.d.sessions[key("claude", "caller-alias")] = alias
	n.d.mu.Unlock()
	if got := n.d.activitySession(owner); got != nil {
		t.Fatal("ambiguous gateway ID selected a session for activity")
	}
	for range 16 {
		if err := n.d.Send(context.Background(), protocol.MsgSend{Sender: owner, Recipient: recipient,
			Type: protocol.TypeNotification, Body: "activity"}); err != nil {
			t.Fatal(err)
		}
		n.d.mu.Lock()
		got := alias.sessionID
		n.d.mu.Unlock()
		if got != owner {
			t.Fatalf("message activity re-registered caller alias: %s, want %s", got, owner)
		}
	}
}

func TestRepeatedWaitKeepsActivity(t *testing.T) {
	d := &Daemon{sessions: map[string]*localSession{}}
	ls := &localSession{info: protocol.SessionInfo{Harness: "pi", NativeID: "waiting"}}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	d.setWaiting(ls, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Continue?", Since: old.Unix()})
	// The harness re-reports the existing wait without supplying its start.
	d.mu.Lock()
	ls.lastActive = old
	d.mu.Unlock()
	d.setWaiting(ls, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Continue?"})
	if got := d.infoOf(ls); got.LastActive != old.Unix() || got.Waiting.Since != old.Unix() {
		t.Fatalf("a repeated wait moved activity: %+v", got)
	}
}
