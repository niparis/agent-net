package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func agentByID(t *testing.T, lc *localClient, id string) protocol.AgentView {
	t.Helper()
	for _, a := range onlineAgents(t, lc) {
		if a.SessionID == id {
			return a
		}
	}
	t.Fatalf("agent %s is not online", id)
	return protocol.AgentView{}
}

// An adapter's mode and model reach the gateway, and a change of model is
// pushed again.
func TestAdapterModeAndModelReachTheGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	cli := cliClient(t, n)
	info := fakeSession("na", "alice")
	info.Mode, info.Model = protocol.ModeBackground, "claude-opus-5-5"
	fa.setSessions(info)
	eventually(t, "session on the gateway", func() bool { return len(onlineAgents(t, cli)) == 1 })
	a := onlineAgents(t, cli)[0]
	if a.Mode != protocol.ModeBackground || a.Model != "claude-opus-5-5" {
		t.Fatalf("mode %q model %q", a.Mode, a.Model)
	}
	info.Model, info.Mode = "claude-sonnet-5-5", protocol.ModeForeground
	fa.setSessions(info)
	eventually(t, "new model on the gateway", func() bool {
		a := onlineAgents(t, cli)[0]
		return a.Model == "claude-sonnet-5-5" && a.Mode == protocol.ModeForeground
	})
}

// Plugins say their mode and model in hello and change them with status.
func TestPluginReportsModeAndModel(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	lc := dialLocal(t, n.sock)
	res := lc.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "p1", "cwd": "/w/p",
		"status": "idle", "mode": "foreground", "model": "anthropic/claude-sonnet-5\n"})
	var r struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(res, &r)
	cli := cliClient(t, n)
	a := agentByID(t, cli, r.SessionID)
	if a.Mode != protocol.ModeForeground || a.Model != "anthropic/claude-sonnet-5" {
		t.Fatalf("after hello: mode %q model %q", a.Mode, a.Model)
	}

	// A model change alone: status stays, model changes.
	lc.mustCall("status", map[string]any{"model": "openai/gpt-6"})
	eventually(t, "model changed", func() bool { return agentByID(t, cli, r.SessionID).Model == "openai/gpt-6" })
	if a := agentByID(t, cli, r.SessionID); a.Status != "idle" || a.Mode != protocol.ModeForeground {
		t.Fatalf("a model change must not touch the rest: %+v", a)
	}
	// A status change alone keeps the model.
	lc.mustCall("status", map[string]any{"status": "busy"})
	eventually(t, "busy", func() bool { return agentByID(t, cli, r.SessionID).Status == "busy" })
	if a := agentByID(t, cli, r.SessionID); a.Model != "openai/gpt-6" {
		t.Fatalf("a status change lost the model: %+v", a)
	}
	lc.mustCall("status", map[string]any{"mode": "background", "model": ""})
	eventually(t, "mode changed", func() bool {
		a := agentByID(t, cli, r.SessionID)
		return a.Mode == protocol.ModeBackground && a.Model == ""
	})

	// Bad input is refused.
	if _, err := lc.call("status", map[string]any{"mode": "sideways"}); err == nil {
		t.Fatal("a bad mode was accepted")
	}
	if _, err := lc.call("status", map[string]any{}); err == nil {
		t.Fatal("an empty status call was accepted")
	}
	if _, err := lc.call("status", map[string]any{"status": "napping"}); err == nil {
		t.Fatal("a bad status was accepted")
	}
	bad := dialLocal(t, n.sock)
	if _, err := bad.call("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "p2", "cwd": "/", "mode": "x"}); err == nil {
		t.Fatal("hello with a bad mode was accepted")
	}
}

// An older plugin sends neither mode nor model: both stay empty.
func TestPluginWithoutModeAndModel(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	_, id := helloPi(t, n, "old", "/w/old", false)
	a := agentByID(t, cliClient(t, n), id)
	if a.Mode != "" || a.Model != "" {
		t.Fatalf("an older plugin got mode %q model %q", a.Mode, a.Model)
	}
}

func queryFeed(t *testing.T, lc *localClient, afterID int64) ([]protocol.FeedEvent, string) {
	t.Helper()
	raw := lc.mustCall("events", map[string]any{"after_id": afterID, "limit": 100})
	var r struct {
		Events []protocol.FeedEvent `json:"events"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r.Events, string(raw)
}

// Any node can read the event feed. It names both ends of a message, never its
// body, and a cursor returns only newer events.
func TestEventFeed(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	na := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	nb := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	a, aid := helloPi(t, na, "a", "/w/a", true)
	_, bid := helloPi(t, nb, "b", "/w/b", true)

	const secret = "the launch code is 0451"
	m := sendResult(t, a.mustCall("send", map[string]any{"to": bid, "body": secret}))
	eventually(t, "delivered", func() bool { return messageState(t, a, m.ID) == protocol.StateDelivered })

	// A third node, with no session in the conversation, reads the feed.
	nc := startNode(t, g, nodeOpts{})
	cli := cliClient(t, nc)
	evs, raw := queryFeed(t, cli, 0)
	if strings.Contains(raw, secret) {
		t.Fatalf("the feed carries a message body: %s", raw)
	}
	kinds := map[string]protocol.FeedEvent{}
	for _, e := range evs {
		kinds[e.Kind] = e
	}
	for _, k := range []string{"session.new", "node.connected", "msg.stored", "msg.dispatched", "msg.delivered"} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("no %s event in %v", k, evs)
		}
	}
	st := kinds["msg.stored"]
	if st.From != aid || st.To != bid || st.Type != protocol.TypeRequest || st.MessageID != m.ID {
		t.Errorf("msg.stored does not name both ends: %+v", st)
	}
	if d := kinds["msg.delivered"]; d.From != aid || d.To != bid {
		t.Errorf("msg.delivered does not name both ends: %+v", d)
	}
	if kinds["node.connected"].Detail != "" {
		t.Errorf("the address of a machine is in the feed: %q", kinds["node.connected"].Detail)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].ID <= evs[i-1].ID {
			t.Fatalf("not oldest first: %v", evs)
		}
	}

	// The cursor returns only what is newer.
	last := evs[len(evs)-1].ID
	if more, _ := queryFeed(t, cli, last); len(more) != 0 {
		t.Fatalf("events after the last one: %v", more)
	}
	m2 := sendResult(t, a.mustCall("send", map[string]any{"to": bid, "body": "second"}))
	eventually(t, "second delivered", func() bool { return messageState(t, a, m2.ID) == protocol.StateDelivered })
	more, _ := queryFeed(t, cli, last)
	if len(more) == 0 {
		t.Fatal("no new events")
	}
	for _, e := range more {
		if e.ID <= last {
			t.Fatalf("event %d is not after %d", e.ID, last)
		}
		if e.MessageID != "" && e.MessageID != m2.ID {
			t.Fatalf("an old message in the new events: %+v", e)
		}
	}

	// The body stays private to the two nodes: the third node cannot fetch it.
	if _, err := cli.call("message_status", map[string]any{"message_id": m.ID}); err == nil {
		t.Fatal("a node outside the conversation read a message")
	}
}
