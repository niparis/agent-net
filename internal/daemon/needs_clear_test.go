package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// These tests hold the "needs you" clearing rules: a wait ends as soon as its
// reason is solved, by every harness and for every normal way to solve it.

// inboxStatus returns the status of one Inbox item.
func inboxStatus(t *testing.T, n *node, id string) string {
	t.Helper()
	c := operator.New(n.sock)
	defer c.Close()
	raw, err := c.Call(context.Background(), "inbox_list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Items []protocol.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	for _, item := range listed.Items {
		if item.ID == id {
			return item.Status
		}
	}
	t.Fatalf("inbox item %s missing: %s", id, raw)
	return ""
}

func askQuestion(t *testing.T, pi *localClient, text string) string {
	t.Helper()
	raw, err := pi.call("ask_question", map[string]any{"text": text, "recommendation": "yes"})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Item.ID == "" {
		t.Fatalf("ask parse: %s %v", raw, err)
	}
	return res.Item.ID
}

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

// The owner types in a Claude session. The policy hook (UserPromptSubmit)
// sees the prompt even when the needs-hook is not installed: it clears the
// wait and closes the session's open question and approval items.
func TestTypedClaudePromptClearsTheWaitAndClosesTheAsk(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	n.d.SetSessions("claude", []protocol.SessionInfo{{Harness: "claude", NativeID: "c-one", Status: "idle", Cwd: "/work/c"}})
	eventually(t, "claude session", func() bool { return n.d.SessionID("claude", "c-one") != "" })
	id := n.d.SessionID("claude", "c-one")
	raw, err := n.d.call(context.Background(), "inbox.create", map[string]any{"session_id": id, "kind": "approval", "text": "Build the grant?", "recommendation": "go"})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Item.ID == "" {
		t.Fatalf("create: %s %v", raw, err)
	}
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "claude", "native_id": "c-one", "kind": "approval", "text": "Build the grant?", "explicit": true})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })

	// A message from a peer is not the owner: nothing changes.
	cli.mustCall(protocol.MethodPolicy, protocol.PolicyRequest{Harness: "claude", NativeID: "c-one", Prompt: "<cross-session-message from=\"x\">hi</cross-session-message>"})
	if got := inboxStatus(t, n, created.Item.ID); got != "open" {
		t.Fatalf("a peer message closed the ask: %s", got)
	}

	cli.mustCall(protocol.MethodPolicy, protocol.PolicyRequest{Harness: "claude", NativeID: "c-one", Prompt: "go"})
	eventually(t, "typed prompt clears the wait", func() bool { return waitingOf(t, n, id) == nil })
	eventually(t, "typed prompt closes the ask", func() bool { return inboxStatus(t, n, created.Item.ID) != "open" })
}

// A Pi session reports interactive input as owner_prompt. It closes the
// session's open ask the same way.
func TestPiOwnerPromptClosesTheAsk(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", false)
	item := askQuestion(t, pi, "which branch?")
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })
	pi.mustCall("owner_prompt", nil)
	eventually(t, "owner prompt clears the wait", func() bool { return waitingOf(t, n, id) == nil })
	eventually(t, "owner prompt closes the ask", func() bool { return inboxStatus(t, n, item) != "open" })
}

// The owner dismisses or answers the ask in the TUI Inbox. Dismissing sends
// no message, so nothing else clears the session's wait.
func TestInboxDismissAndAnswerClearTheSessionWait(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, id := helloPi(t, n, "nb", "/work/bob", true)
	for _, answer := range []string{"dismiss", "approve", "refuse"} {
		item := askQuestion(t, pi, "which branch? "+answer)
		eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })
		c := operator.New(n.sock)
		if _, err := c.Call(context.Background(), "inbox_answer", map[string]string{"id": item, "answer": answer}); err != nil {
			t.Fatalf("%s: %v", answer, err)
		}
		c.Close()
		eventually(t, answer+" clears the wait", func() bool { return waitingOf(t, n, id) == nil })
	}
}

// A prompt the owner types in Codex starts a userMessage item with a null
// clientId (the shape of a real app server, see codex_real_test.go). A message
// agent-net queued carries its ID there and is not the owner.
func TestCodexTypedPromptClearsTheWaitAndClosesTheAsk(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	n.d.SetSessions("codex", []protocol.SessionInfo{{Harness: "codex", NativeID: "thread-one", Status: "idle", Cwd: "/work/x"}})
	eventually(t, "codex session", func() bool { return n.d.SessionID("codex", "thread-one") != "" })
	id := n.d.SessionID("codex", "thread-one")
	raw, err := n.d.call(context.Background(), "inbox.create", map[string]any{"session_id": id, "kind": "question", "text": "Which branch?", "recommendation": "main"})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Item.ID == "" {
		t.Fatalf("create: %s %v", raw, err)
	}
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("waiting", map[string]any{"harness": "codex", "native_id": "thread-one", "kind": "question", "text": "Which branch?", "explicit": true})
	eventually(t, "waiting stored", func() bool { return waitingOf(t, n, id) != nil })
	a := &codexAdapter{d: n.d, kick: make(chan struct{}, 1)}

	injected := `{"item":{"type":"userMessage","id":"u1","clientId":"m_abc","content":[{"type":"text","text":"please check\n\n[agent-net] message m_abc from pi","text_elements":[]}]},"threadId":"thread-one","turnId":"t1"}`
	a.onNote("item/started", json.RawMessage(injected))
	typedByAgentNet := `{"item":{"type":"userMessage","id":"u2","clientId":null,"content":[{"type":"text","text":"x\n\n[agent-net] message m_def","text_elements":[]}]},"threadId":"thread-one","turnId":"t1"}`
	a.onNote("item/started", json.RawMessage(typedByAgentNet))
	time.Sleep(300 * time.Millisecond)
	if waitingOf(t, n, id) == nil || inboxStatus(t, n, created.Item.ID) != "open" {
		t.Fatal("a message from agent-net counted as the owner")
	}

	typed := `{"item":{"type":"userMessage","id":"u3","clientId":null,"content":[{"type":"text","text":"main","text_elements":[]}]},"threadId":"thread-one","turnId":"t2"}`
	a.onNote("item/started", json.RawMessage(typed))
	eventually(t, "typed prompt clears the wait", func() bool { return waitingOf(t, n, id) == nil })
	eventually(t, "typed prompt closes the ask", func() bool { return inboxStatus(t, n, created.Item.ID) != "open" })
}
