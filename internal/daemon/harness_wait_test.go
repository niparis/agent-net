package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// harnessWaiting is a discovered session whose harness says it waits for its
// user, as the Claude adapter reports it from the session file (decision 0057):
// status unknown, with the wait.
func harnessWaiting(native, name, text string) protocol.SessionInfo {
	s := fakeSession(native, name)
	s.Status = "unknown"
	s.HarnessWaits = true
	s.Waiting = &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: text}
	return s
}

func TestHarnessWaitStartsAndEndsWithTheHarness(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice has an id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	id := fa.host.SessionID("fake", "na")

	fa.setSessions(harnessWaiting("na", "alice", "Merge branch w_1 into main?"))
	var since int64
	eventually(t, "the harness wait reaches the gateway", func() bool {
		w := waitingOf(t, n, id)
		if w == nil || w.Text != "Merge branch w_1 into main?" || w.Since == 0 {
			return false
		}
		since = w.Since
		return true
	})
	// Every poll reports the wait again: it is the same wait.
	fa.setSessions(harnessWaiting("na", "alice", "Merge branch w_1 into main?"))
	fa.setSessions(harnessWaiting("na", "alice", "Merge branch w_1 into main?"))
	if w := waitingOf(t, n, id); w == nil || w.Since != since {
		t.Fatalf("a poll moved the wait: %+v, want since %d", w, since)
	}

	// The user answered and the turn goes on: still busy, no longer waiting.
	busy := fakeSession("na", "alice")
	busy.Status = "busy"
	fa.setSessions(busy)
	eventually(t, "the wait ends with the harness", func() bool { return waitingOf(t, n, id) == nil })

	// An explicit ask at the end of a turn is not the harness's to clear.
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "fake", "native_id": "na", "kind": "question", "text": "Release now?", "explicit": true})
	eventually(t, "explicit ask stored", func() bool { w := waitingOf(t, n, id); return w != nil && w.Explicit })
	idle := fakeSession("na", "alice")
	fa.setSessions(idle)
	fa.setSessions(idle)
	if w := waitingOf(t, n, id); w == nil || w.Text != "Release now?" {
		t.Fatalf("an idle poll cleared the explicit ask: %+v", w)
	}
}

// The hook and the session file describe the same dialog. The wait the hook
// started keeps its start time when the harness reports it, and ends when the
// harness stops waiting, even though no hook said so.
func TestHookWaitAndHarnessWaitAreOneWait(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	running := fakeSession("na", "alice")
	running.Status = "busy"
	fa.setSessions(running)
	eventually(t, "alice has an id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	id := fa.host.SessionID("fake", "na")

	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "fake", "native_id": "na", "kind": "permission", "text": "Bash(git push origin main)"})
	var since int64
	eventually(t, "hook wait stored", func() bool {
		w := waitingOf(t, n, id)
		if w == nil {
			return false
		}
		since = w.Since
		return true
	})
	s := harnessWaiting("na", "alice", "")
	s.Waiting = &protocol.Waiting{Kind: protocol.WaitingPermission, Text: "Bash(git push origin main)"}
	fa.setSessions(s)
	fa.setSessions(s)
	if w := waitingOf(t, n, id); w == nil || w.Since != since || w.Text != "Bash(git push origin main)" {
		t.Fatalf("harness report changed the hook's wait: %+v, want since %d", w, since)
	}
	fa.setSessions(running)
	eventually(t, "harness end clears the hook's wait", func() bool { return waitingOf(t, n, id) == nil })
}

// Codex says a thread waits for an approval or for the user's input with the
// thread's active flags.
func TestCodexThreadFlagsAreAWait(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t-1", Cwd: "/work/api", Status: "active", Source: "cli", Flags: []string{"waitingOnApproval"}})
	n := codexNode(t, g, f)
	cli := cliClient(t, n)
	eventually(t, "approval wait", func() bool {
		a, ok := codexAgents(t, cli)["/work/api"]
		return ok && a.Waiting != nil && a.Waiting.Kind == protocol.WaitingPermission && a.Waiting.Text == "approve a command or a change" && a.Status == "busy"
	})
	f.set(fakeThread{ID: "t-1", Cwd: "/work/api", Status: "active", Source: "cli", Flags: []string{"waitingOnUserInput"}})
	f.notify("thread/status/changed")
	eventually(t, "input wait", func() bool {
		a := codexAgents(t, cli)["/work/api"]
		return a.Waiting != nil && a.Waiting.Kind == protocol.WaitingQuestion && a.Waiting.Text == "answer a question"
	})
	f.set(fakeThread{ID: "t-1", Cwd: "/work/api", Status: "active", Source: "cli"})
	f.notify("thread/status/changed")
	eventually(t, "answered", func() bool {
		a := codexAgents(t, cli)["/work/api"]
		return a.Waiting == nil && a.Status == "busy"
	})
}

// A request to a session that waits for its user reaches it as usual, and
// its sender hears once from agent-net, through the real gateway and both
// daemons. A second request in the same wait gets no notice; after the wait,
// none either.
func TestWaitingSessionAnswersItsSenderOnceEndToEnd(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fb := &fakeAdapter{harness: "fake"}
	recv := startNode(t, g, nodeOpts{adapter: fb})
	fb.setSessions(fakeSession("nb", "bob"))
	fa := &fakeAdapter{harness: "fake"}
	snd := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("na", "alice"))
	var from, to string
	eventually(t, "both sessions registered", func() bool {
		from, to = snd.d.SessionID("fake", "na"), recv.d.SessionID("fake", "nb")
		return from != "" && to != ""
	})
	fb.setSessions(harnessWaiting("nb", "bob", "Merge branch w_1 into main?"))
	eventually(t, "bob's wait reaches the gateway", func() bool {
		a, ok, err := g.gw.Store().Agent(to)
		return err == nil && ok && a.Waiting != nil
	})
	store := g.gw.Store()
	sendAndDeliver := func(key string) protocol.Message {
		t.Helper()
		m, _, err := store.StoreMessage(protocol.MsgSend{Sender: from, Recipient: to, Body: "status of the merge?", IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		recv.d.onDeliver(context.Background(), m)
		eventually(t, "request delivered", func() bool {
			got, _, _ := store.Message(m.ID)
			return got.State == protocol.StateDelivered
		})
		return m
	}
	first := sendAndDeliver("first")
	eventually(t, "alice's harness gets the notice", func() bool { return len(noticesTo(fa)) == 1 })
	n := noticesTo(fa)[0]
	// The notice includes bob's question and when the wait started.
	if !strings.HasPrefix(n.Body, "[agent-net] bob is waiting for its user's answer to: Merge branch w_1 into main? (since ") ||
		!strings.HasSuffix(n.Body, " UTC). Your message was delivered and will be read when the user answers. No reply is needed.") ||
		n.Type != protocol.TypeNotification || n.FromHarness != "agent-net" || n.ParentID != first.ID {
		t.Fatalf("notice: %+v", n)
	}
	if got := fb.delivered(); len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("bob did not get the request: %+v", got)
	}

	sendAndDeliver("second")
	fb.setSessions(fakeSession("nb", "bob"))
	eventually(t, "bob's wait ends at the gateway", func() bool {
		a, ok, err := store.Agent(to)
		return err == nil && ok && a.Waiting == nil
	})
	sendAndDeliver("third")
	node, err := store.SessionNode(from)
	if err != nil {
		t.Fatal(err)
	}
	all, err := store.Messages(node, protocol.QueryMessages{SessionID: from, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range all {
		if m.From == "agent-net" && m.To == from {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want one notice for alice, got %d", count)
	}
}

// A daemon restart keeps a wait the harness still reports, as one wait, and
// clears a wait that ended while the daemon was down.
func TestDaemonRestartKeepsOrClearsTheWait(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(harnessWaiting("na", "alice", "Merge?"))
	var id string
	var since int64
	eventually(t, "wait at the gateway", func() bool {
		id = n.d.SessionID("fake", "na")
		a, ok, err := g.gw.Store().Agent(id)
		if err != nil || !ok || a.Waiting == nil {
			return false
		}
		since = a.Waiting.Since
		return true
	})

	n.restart()
	fa.setSessions(harnessWaiting("na", "alice", "Merge?"))
	eventually(t, "the wait is kept across the restart", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Online && a.Waiting != nil && a.Waiting.Since == since
	})

	n.restart()
	answered := fakeSession("na", "alice")
	answered.Status = "busy"
	fa.setSessions(answered)
	eventually(t, "a wait that ended while the daemon was down is cleared", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Online && a.Waiting == nil
	})
}
