package daemon

import (
	"context"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// codexConfirmNode is a node with a Codex app server (the fake), with the
// delivery check on or off (decision 0171, part b).
func codexConfirmNode(t *testing.T, g *testGateway, f *fakeShared, confirm bool) (*node, *logBuf) {
	t.Helper()
	logs := &logBuf{}
	n := startNode(t, g, nodeOpts{cmdline: "pi", logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.CodexHome = f.home
		c.CodexInterval = time.Second
		c.testNoDeliveryConfirm = !confirm
		c.deliveryConfirmWait = scaled(500 * time.Millisecond)
	}})
	eventually(t, "the thread is a session", func() bool { return n.d.SessionID("codex", "t-open") != "" })
	return n, logs
}

// codexDeliver delivers m to the open thread through the Codex adapter, as the
// daemon does for a session.
func codexDeliver(t *testing.T, n *node, m protocol.Message) {
	t.Helper()
	a := n.d.adapters["codex"].(*codexAdapter)
	// The daemon fills the repo of a session from its folder, and the start block
	// is the charter of that repo's project.
	info := protocol.SessionInfo{Harness: "codex", NativeID: "t-open", SessionID: n.d.SessionID("codex", "t-open"), Repo: protocol.RepoInfo{Project: "api"}}
	if res := a.Deliver(context.Background(), info, m); !res.Delivered {
		t.Fatalf("delivery refused: %s", res.Detail)
	}
}

// queuedCopies counts the queue calls for one message ID.
func queuedCopies(f *fakeShared, msgID string) int {
	n := 0
	for _, q := range f.queue() {
		if q.ClientID == msgID {
			n++
		}
	}
	return n
}

// queuedText is the text of the newest queue call for one message ID.
func queuedText(f *fakeShared, msgID string) string {
	text := ""
	for _, q := range f.queue() {
		if q.ClientID == msgID {
			text = q.Text
		}
	}
	return text
}

// turnTexts is the prompt of each turn/start the fake got, in order.
func (f *fakeShared) turnTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.turns...)
}

// A queued message whose turn Codex starts is confirmed from the notice, with
// no read of the thread, and is queued once. Codex starts the turn of an idle
// thread at once, so the notice can reach the daemon before the queue call
// returns. The fake sends it there: the test has no timing of its own, and the
// check has the notice in hand when its timer fires. Both the confirm log and
// the calls are read once, after the check has ended.
func TestCodexQueuedMessageTakenIsNotSentAgain(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t-open", Cwd: "/work/api", Status: "idle", Source: "cli"})
	f.mu.Lock()
	f.noticeBeforeReply = true
	f.mu.Unlock()
	n, logs := codexConfirmNode(t, g, f, true)
	m := protocol.Message{ID: "msg-codex-taken", Body: "check the build", From: "agent-a", FromName: "agent-a"}
	codexDeliver(t, n, m)
	eventually(t, "the delivery confirmed", func() bool { return len(logs.lines("session.delivery_confirmed msg="+m.ID)) == 1 })
	if got := queuedCopies(f, m.ID); got != 1 {
		t.Fatalf("queue calls for a taken message: %d, want 1", got)
	}
	if lines := logs.lines("session.delivery_retry", "session.restart_for_delivery"); len(lines) != 0 {
		t.Fatalf("a taken message was sent again: %v", lines)
	}
}

// A queued message that Codex never starts a turn for is queued once more, and
// then a turn starts with it as the prompt.
func TestCodexQueuedMessageRetriedThenRestarted(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t-open", Cwd: "/work/api", Status: "idle", Source: "cli"})
	n, logs := codexConfirmNode(t, g, f, true)
	m := protocol.Message{ID: "msg-codex-stuck", Body: "please restart this", From: "agent-a", FromName: "agent-a"}
	codexDeliver(t, n, m)
	eventually(t, "the restart", func() bool { return len(logs.lines("session.restart_for_delivery msg="+m.ID)) == 1 })
	if got := len(logs.lines("session.delivery_retry msg=" + m.ID)); got != 1 {
		t.Fatalf("retries logged: %d, want 1:\n%s", got, logs.String())
	}
	if got := queuedCopies(f, m.ID); got != 2 {
		t.Fatalf("queue calls: %d, want 2 (the message and its one retry)", got)
	}
	// The daemon logs the restart before it sends turn/start, so the fake may not
	// have the turn yet. Wait for it, then check exactly one turn holds the message.
	eventually(t, "the restart starts a turn", func() bool { return len(f.turnTexts()) >= 1 })
	turns := f.turnTexts()
	if len(turns) != 1 || !strings.Contains(turns[0], "please restart this") {
		t.Fatalf("the restart did not start a turn with the message: %q", turns)
	}
}

// After a compaction the next message to a Codex session carries the session's
// start block again, once. Codex has no hook for it, so the daemon sends it.
func TestCodexSessionStartBlockAgainAfterCompaction(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t-open", Cwd: "/work/api", Status: "idle", Source: "cli"})
	n, _ := codexConfirmNode(t, g, f, false)
	// The charter of the session's node is its start block.
	cliClient(t, n).mustCall("charter_set", map[string]any{"node": "api", "attr": "purpose", "value": "The API service that the owner runs"})
	ctx := context.Background()
	sid := n.d.SessionID("codex", "t-open")
	block := n.d.codexStartBlock(ctx, sid, "api")
	if block == "" {
		t.Fatal("the session has no start block to send")
	}
	first := protocol.Message{ID: "msg-codex-c1", Body: "first", From: "agent-a", FromName: "agent-a"}
	codexDeliver(t, n, first)
	if got := queuedText(f, first.ID); got != codexFrame(first) {
		t.Fatalf("a message before any compaction carries the block: %q", got)
	}
	n.d.noteCompacted("codex", "t-open", time.Now().Unix(), "fill 80%")
	second := protocol.Message{ID: "msg-codex-c2", Body: "second", From: "agent-a", FromName: "agent-a"}
	codexDeliver(t, n, second)
	if got := queuedText(f, second.ID); !strings.HasPrefix(got, block) || !strings.HasSuffix(got, codexFrame(second)) {
		t.Fatalf("the first message after a compaction lacks the start block: %q", got)
	}
	third := protocol.Message{ID: "msg-codex-c3", Body: "third", From: "agent-a", FromName: "agent-a"}
	codexDeliver(t, n, third)
	if got := queuedText(f, third.ID); got != codexFrame(third) {
		t.Fatalf("the start block was sent twice for one compaction: %q", got)
	}
}

// Five messages to a busy session leave one delivery check, the one for the
// newest message. The check ends when the session goes offline.
func TestCodexBusySessionKeepsOneCheck(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t-open", Cwd: "/work/api", Status: "active", Source: "cli"})
	n, _ := codexConfirmNode(t, g, f, true)
	for i := 1; i <= 5; i++ {
		codexDeliver(t, n, protocol.Message{ID: fmt.Sprintf("msg-busy-%d", i), Body: "while busy", From: "agent-a", FromName: "agent-a"})
	}
	eventually(t, "one live check for the session", func() bool { return n.d.checkRuns.Load() == 1 })
	f.remove("t-open")
	f.notify("thread/closed")
	eventually(t, "the check ends when the session goes offline", func() bool { return n.d.checkRuns.Load() == 0 })
}
