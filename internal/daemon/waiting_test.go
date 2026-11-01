package daemon

import (
	"encoding/json"
	"strings"
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

func TestWaitingClearsWhenOwnerMessageDelivered(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "session", func() bool { return fa.host.SessionID("fake", "na") != "" })
	id := fa.host.SessionID("fake", "na")
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "fake", "native_id": "na", "kind": "question", "text": "Merge?"})
	eventually(t, "waiting", func() bool { return waitingOf(t, n, id) != nil })
	cli.mustCall("owner_send", protocol.OwnerSendRequest{To: id, Body: "Yes"})
	eventually(t, "owner answer clears waiting", func() bool { return waitingOf(t, n, id) == nil })
}

func TestCodexTurnStartedClearsWaiting(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	n.d.SetSessions("codex", []protocol.SessionInfo{{Harness: "codex", NativeID: "thread-one", Status: "busy"}})
	eventually(t, "codex session", func() bool { return n.d.SessionID("codex", "thread-one") != "" })
	id := n.d.SessionID("codex", "thread-one")
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "codex", "native_id": "thread-one", "kind": "question", "text": "Continue?"})
	eventually(t, "wait stored", func() bool { return waitingOf(t, n, id) != nil })
	a := &codexAdapter{d: n.d, kick: make(chan struct{}, 1)}
	a.onNote("turn/started", json.RawMessage(`{"threadId":"thread-one"}`))
	eventually(t, "turn start clears wait", func() bool { return waitingOf(t, n, id) == nil })
}

func TestWorkerAskOwnerIsRefused(t *testing.T) {
	d := &Daemon{workers: map[string]*worker{"w_test": {id: "w_test", sessionID: "s_test"}}}
	lc := &localConn{d: d}
	ls := &localSession{sessionID: "s_test"}
	_, err := lc.waitingCall("opencode", ls, json.RawMessage(`{"kind":"question","text":"Merge?","explicit":true}`))
	if err == nil || err.Error() != "you are a worker: put the question in your hand-back or ask your caller with agent_reply" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkerWaitingNotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "wait"})
	rec := readFake(t, repo)
	eventually(t, "worker task delivered", func() bool { return w.d.st.isDelivered(res.Task.ID) })
	owner := w.ownerID()
	blocked := func() []protocol.Message {
		var found []protocol.Message
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "worker "+res.SessionID+" is blocked:") {
				found = append(found, m)
			}
		}
		return found
	}
	send := func(kind, text string) {
		w.cli.mustCall("waiting", map[string]any{"harness": "opencode", "native_id": rec.SessionID, "kind": kind, "text": text})
	}
	send("permission", "shell(git status)")
	eventually(t, "worker blocked notice", func() bool { return len(blocked()) == 1 })
	if got := blocked()[0].Body; !strings.Contains(got, "worker "+res.SessionID+" is blocked: shell(git status)") {
		t.Fatalf("notice: %s", got)
	}
	if got := waitingOf(t, w.node, res.SessionID); got != nil {
		t.Fatalf("worker wait reached Needs you: %+v", got)
	}
	send("permission", "shell(git status)")
	if got := len(blocked()); got != 1 {
		t.Fatalf("repeat sent %d notices: %+v", got, blocked())
	}
	w.cli.mustCall("waiting", map[string]any{"harness": "opencode", "native_id": rec.SessionID, "clear": true})
	send("question", "Which branch?")
	eventually(t, "second blocked notice", func() bool { return len(blocked()) == 2 })
	if !strings.Contains(blocked()[0].Body+blocked()[1].Body, "Which branch?") {
		t.Fatal("question missing")
	}
	w.cli.mustCall("waiting", map[string]any{"harness": "opencode", "native_id": rec.SessionID, "clear": true})
	send("input", "")
	if got := len(blocked()); got != 2 {
		t.Fatalf("empty wait sent notice: %d", got)
	}
	params := map[string]any{"harness": "opencode", "native_id": rec.SessionID, "kind": "question", "text": "May I merge?", "explicit": true}
	for i := 0; i < 2; i++ {
		if _, err := w.cli.call("waiting", params); err == nil || !strings.Contains(err.Error(), "you are a worker") {
			t.Fatalf("explicit worker question not refused: %v", err)
		}
	}
	eventually(t, "explicit question notice", func() bool { return len(blocked()) == 3 })
	if got := waitingOf(t, w.node, res.SessionID); got != nil {
		t.Fatalf("explicit question reached Needs you: %+v", got)
	}
}
