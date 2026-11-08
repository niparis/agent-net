package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestInboxApprovalDismissOperatorLink(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("asker", "asker"))
	eventually(t, "session registered", func() bool { return fa.host.SessionID("fake", "asker") != "" })
	sid := fa.host.SessionID("fake", "asker")
	raw, err := n.d.call(context.Background(), "inbox.create", map[string]string{"session_id": sid, "kind": "approval", "text": "Allow action?", "recommendation": "Allow"})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Item.ID == "" {
		t.Fatalf("create: %s %v", raw, err)
	}
	c := operator.New(n.sock)
	defer c.Close()
	if _, err := c.Call(context.Background(), "inbox_answer", map[string]string{"id": created.Item.ID, "answer": "dismiss"}); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	raw, err = c.Call(context.Background(), "inbox_list", nil)
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
		if item.ID == created.Item.ID && item.Status == "dismissed" {
			return
		}
	}
	t.Fatalf("dismissed item missing: %s", raw)
}

func TestInboxAskReplaceAndCloseDaemon(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	conn, sid := helloPi(t, n, "asker", t.TempDir(), false)

	// Session starts with no waiting
	if w := waitingOf(t, n, sid); w != nil {
		t.Fatalf("initial waiting = %+v, want nil", w)
	}

	// 1. Propose an idea leaves waiting unchanged (nil)
	_, err := conn.call("propose_idea", map[string]any{"text": "A new feature idea", "recommendation": "build it"})
	if err != nil {
		t.Fatalf("propose idea: %v", err)
	}
	if w := waitingOf(t, n, sid); w != nil {
		t.Fatalf("propose_idea set waiting: %+v", w)
	}

	// 2. Create first ask: waiting line matches the ask
	raw, err := conn.call("ask_question", map[string]any{"text": "PR15 + router + Mac", "recommendation": "all three"})
	if err != nil {
		t.Fatalf("first ask: %v", err)
	}
	var res1 struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &res1); err != nil || res1.Item.ID == "" {
		t.Fatalf("first ask parse: %s %v", raw, err)
	}

	eventually(t, "first waiting set", func() bool {
		w := waitingOf(t, n, sid)
		return w != nil && w.Text == "PR15 + router + Mac"
	})

	// Proposing another idea leaves waiting unchanged
	_, err = conn.call("propose_idea", map[string]any{"text": "Second feature idea", "recommendation": "consider"})
	if err != nil {
		t.Fatalf("second propose idea: %v", err)
	}
	if w := waitingOf(t, n, sid); w == nil || w.Text != "PR15 + router + Mac" {
		t.Fatalf("propose_idea altered waiting: %+v", w)
	}

	// 3. Replace with second ask: waiting line matches new ask
	raw, err = conn.call("ask_question", map[string]any{"text": "router + Mac", "recommendation": "both", "replaces": res1.Item.ID})
	if err != nil {
		t.Fatalf("replace ask: %v", err)
	}
	var res2 struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &res2); err != nil || res2.Item.ID == "" {
		t.Fatalf("replace ask parse: %s %v", raw, err)
	}

	eventually(t, "second waiting set", func() bool {
		w := waitingOf(t, n, sid)
		return w != nil && w.Text == "router + Mac"
	})

	// Check Inbox list: first is replaced, second is open
	c := operator.New(n.sock)
	defer c.Close()
	raw, err = c.Call(context.Background(), "inbox_list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Items []protocol.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	for _, it := range listed.Items {
		if it.ID == res1.Item.ID && it.Status != "replaced" {
			t.Fatalf("res1 status = %q, want replaced", it.Status)
		}
		if it.ID == res2.Item.ID && it.Status != "open" {
			t.Fatalf("res2 status = %q, want open", it.Status)
		}
	}

	// 4. Close ask without a new question: waiting line is cleared
	raw, err = conn.call("ask_question", map[string]any{"text": "", "replaces": res2.Item.ID})
	if err != nil {
		t.Fatalf("close ask: %v", err)
	}
	var res3 struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &res3); err != nil || res3.Item.Status != "replaced" {
		t.Fatalf("close ask parse: %s %v", raw, err)
	}

	eventually(t, "waiting cleared", func() bool {
		return waitingOf(t, n, sid) == nil
	})

	raw, err = c.Call(context.Background(), "inbox_list", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	for _, it := range listed.Items {
		if it.ID == res2.Item.ID && it.Status != "replaced" {
			t.Fatalf("res2 status = %q, want replaced", it.Status)
		}
	}
}

// The Desk reads asks and the log through the daemon: an ask a session makes
// is in asks_list until it is answered, and a CTO notice is only in desk_log.
func TestAsksListAndDeskLogOperatorLink(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("asker", "asker"))
	eventually(t, "session registered", func() bool { return fa.host.SessionID("fake", "asker") != "" })
	sid := fa.host.SessionID("fake", "asker")
	raw, err := n.d.call(context.Background(), "inbox.create", map[string]any{"session_id": sid, "kind": "question", "text": "Merge PR #3?", "recommendation": "yes", "options": []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Item.ID == "" {
		t.Fatalf("create: %s %v", raw, err)
	}
	c := operator.New(n.sock)
	defer c.Close()
	asks := func() []protocol.Ask {
		t.Helper()
		raw, err := c.Call(context.Background(), protocol.OpAsksList, nil)
		if err != nil {
			t.Fatal(err)
		}
		var res protocol.AsksResult
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		return res.Asks
	}
	list := asks()
	if len(list) != 1 || list[0].ID != created.Item.ID || list[0].Question != "Merge PR #3?" || list[0].Next != protocol.AskNextOwner || list[0].State != protocol.AskStateOpen {
		t.Fatalf("asks: %+v", list)
	}
	if _, err := c.Call(context.Background(), "inbox_answer", map[string]string{"id": created.Item.ID, "answer": "dismiss"}); err != nil {
		t.Fatal(err)
	}
	if list := asks(); len(list) != 0 {
		t.Fatalf("dismissed ask still listed: %+v", list)
	}
	raw, err = c.Call(context.Background(), protocol.OpDeskLog, protocol.DeskLogQuery{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	var log protocol.DeskLogResult
	if err := json.Unmarshal(raw, &log); err != nil || log.Entries == nil {
		t.Fatalf("log: %s %v", raw, err)
	}
}
