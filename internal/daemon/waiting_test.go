package daemon

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// waitingOf asks the daemon for a session's agent view, as another machine's
// TUI would see it through the gateway.
func waitingOf(t *testing.T, n *node, id string) *protocol.Waiting {
	t.Helper()
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	res := cli.mustCall("get", map[string]any{"session_id": id})
	var r struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	return r.Agent.Waiting
}

func TestPluginWaitingGoesToTheGatewayAndBusyClearsIt(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)

	pi.mustCall("waiting", map[string]any{"kind": "permission", "text": "Bash(git push origin main)"})
	eventually(t, "waiting reaches the gateway", func() bool {
		w := waitingOf(t, n, id)
		return w != nil && w.Kind == protocol.WaitingPermission && w.Text == "Bash(git push origin main)" && w.Since != 0
	})

	// The session answers and works again.
	pi.mustCall("status", map[string]any{"status": "busy"})
	eventually(t, "busy clears waiting", func() bool { return waitingOf(t, n, id) == nil })
}

func TestWaitingSurvivesDiscoveryAndClearsOnBusy(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice has an id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	id := fa.host.SessionID("fake", "na")

	// A hook that names its session, like `agent-net needs-hook` does.
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "fake", "native_id": "na", "kind": "input", "text": "next step?"})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })

	// Every discovery poll rewrites the session info; the wait must stay.
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "waiting survives a poll", func() bool {
		w := waitingOf(t, n, id)
		return w != nil && w.Text == "next step?"
	})

	// A session that becomes busy again is not waiting.
	busy := fakeSession("na", "alice")
	busy.Status = "busy"
	fa.setSessions(busy)
	eventually(t, "busy clears waiting", func() bool { return waitingOf(t, n, id) == nil })
}

func TestAnnounceSetsAndClearsWaiting(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)

	// The fallback for every harness: the waiting question in one line.
	pi.mustCall("announce", map[string]any{"task": "deploy", "waiting": "Merge #817?"})
	eventually(t, "announce waiting", func() bool {
		w := waitingOf(t, n, id)
		return w != nil && w.Kind == protocol.WaitingInput && w.Text == "Merge #817?"
	})

	pi.mustCall("announce", map[string]any{"task": "deploy"})
	eventually(t, "a later announce clears waiting", func() bool { return waitingOf(t, n, id) == nil })
}

// A waiting request from the command line names its session and changes
// nothing else. An unknown session is silent, not an error: the hook runs in
// the middle of a Claude turn and must never disturb it.
func TestWaitingFromCLIIsSilentForUnknownSessions(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "claude", "native_id": "gone", "kind": "input", "text": "x"})
}

// A stale wait left by an old idle_prompt notification carries only the
// generic text. It must not stay in "Needs you": a later request with that
// text clears it, and a discovery refresh drops it as well.
func TestIdlePromptWaitingClearsTheStaleWait(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)

	pi.mustCall("waiting", map[string]any{"kind": "input", "text": "next step?"})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })

	pi.mustCall("waiting", map[string]any{"kind": "input", "text": idlePromptText})
	eventually(t, "the generic idle text clears waiting", func() bool { return waitingOf(t, n, id) == nil })
}

func TestKeepWaitingDropsTheGenericIdleText(t *testing.T) {
	old := protocol.SessionInfo{Status: "idle", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: idlePromptText}}
	if w := keepWaiting(old, protocol.SessionInfo{Status: "idle"}); w != nil {
		t.Fatalf("a stale idle_prompt wait was kept: %+v", w)
	}
	old.Waiting.Text = "next step?"
	if w := keepWaiting(old, protocol.SessionInfo{Status: "idle"}); w == nil || w.Text != "next step?" {
		t.Fatalf("a real wait was dropped: %+v", w)
	}
}

// A plugin reload says hello again for the same session. The wait belongs to
// the daemon, so it must survive; a session that is really working again sends
// status busy.
func TestPluginReconnectKeepsWaiting(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)
	pi.mustCall("waiting", map[string]any{"kind": "question", "text": "which branch?"})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })

	// A new connection says hello for the same session, as a reloaded plugin
	// does. The daemon closes the old one itself.
	again, id2 := helloPi(t, n, "nb", "/work/bob", false)
	defer again.c.Close()
	if id2 != id {
		t.Fatalf("a reconnect made a new session: %s -> %s", id, id2)
	}
	eventually(t, "waiting survives the reconnect", func() bool {
		w := waitingOf(t, n, id)
		return w != nil && w.Text == "which branch?"
	})
}
