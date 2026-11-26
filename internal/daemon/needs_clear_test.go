package daemon

import (
	"testing"
)

// These tests hold the "needs you" clearing rules: a wait ends as soon as its
// reason is solved, by every harness and for every normal way to solve it.

// A hook reported a permission wait and the session file never showed
// "waiting" (the poll missed it). The owner denies or interrupts, so the turn
// ends and the session goes idle. No later tool call clears the wait.
func TestHookWaitClearsWhenTheTurnEndsWithoutAHarnessWait(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	busy := fakeSession("na", "alice")
	busy.Status = "busy"
	fa.setSessions(busy)
	eventually(t, "session", func() bool { return fa.host.SessionID("fake", "na") != "" })
	id := fa.host.SessionID("fake", "na")

	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "fake", "native_id": "na", "kind": "permission", "text": "Bash: git push"})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })

	fa.setSessions(busy) // a poll while the dialog is open keeps the wait
	eventually(t, "kept while busy", func() bool { return waitingOf(t, n, id) != nil })

	fa.setSessions(fakeSession("na", "alice")) // the turn ended: idle
	eventually(t, "the turn end clears the permission wait", func() bool { return waitingOf(t, n, id) == nil })
}

// An explicit ask (agent_owner_ask) outlives the turn that raised it: the
// session ends its turn and waits for the owner. The idle rule must not touch it.
func TestExplicitAskSurvivesTheTurnEnd(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	busy := fakeSession("na", "alice")
	busy.Status = "busy"
	fa.setSessions(busy)
	eventually(t, "session", func() bool { return fa.host.SessionID("fake", "na") != "" })
	id := fa.host.SessionID("fake", "na")
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "fake", "native_id": "na", "kind": "question", "text": "Merge?", "explicit": true})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })
	fa.setSessions(fakeSession("na", "alice"))
	fa.setSessions(fakeSession("na", "alice"))
	if w := waitingOf(t, n, id); w == nil || w.Text != "Merge?" {
		t.Fatalf("explicit ask lost at the turn end: %+v", w)
	}
}

// OpenCode and Pi plugins report a permission wait while a turn runs. When the
// owner interrupts, the turn ends with status idle and no clear event.
func TestPluginPermissionWaitClearsWhenTheSessionGoesIdle(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)
	pi.mustCall("status", map[string]any{"status": "busy"})
	pi.mustCall("waiting", map[string]any{"kind": "permission", "text": "edit main.go"})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })
	pi.mustCall("status", map[string]any{"status": "idle"})
	eventually(t, "idle clears the permission wait", func() bool { return waitingOf(t, n, id) == nil })
}

// The plugin process restarted while the session works. The old wait is from
// the process before; a hello that says busy clears it.
func TestPluginHelloBusyClearsAnOldWait(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)
	pi.mustCall("waiting", map[string]any{"kind": "permission", "text": "edit main.go"})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })

	lc := dialLocal(t, n.sock)
	defer lc.c.Close()
	lc.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "nb", "cwd": "/work/bob", "status": "busy"})
	eventually(t, "a busy hello clears the wait", func() bool { return waitingOf(t, n, id) == nil })
}
