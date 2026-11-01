package daemon

import (
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
