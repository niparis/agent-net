package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A lead hands a request to a member. The member gets the original sender, its
// reply goes to the asker, and the lead gets a copy.
func TestHandoffEndToEnd(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"), fakeSession("nb", "bob"))
	eventually(t, "recipients known", func() bool {
		return fa.host.SessionID("fake", "na") != "" && fa.host.SessionID("fake", "nb") != ""
	})
	aliceID, bobID := fa.host.SessionID("fake", "na"), fa.host.SessionID("fake", "nb")
	lead, leadID := helloPi(t, n, "lead", "/w/lead", true)
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: leadID,
		Type: protocol.TypeRequest, Body: "please ingest the file"}); err != nil {
		t.Fatal(err)
	}
	request := lead.nextPush()

	raw, err := lead.call("handoff", map[string]any{"message_id": request.ID, "to_session": "bob", "note": "this is yours"})
	if err != nil {
		t.Fatal(err)
	}
	var receipt groupReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.MessageID == "" || receipt.To.ID != bobID || receipt.To.Name != "bob" {
		t.Fatalf("handoff receipt %s: %v", raw, err)
	}
	eventually(t, "bob got the hand-off", func() bool { return len(fa.delivered()) == 1 })
	got := fa.delivered()[0]
	if got.To != bobID || got.From != aliceID || got.ConversationID != request.ConversationID ||
		!strings.Contains(got.Body, "this is yours") || !strings.Contains(got.Body, "please ingest the file") {
		t.Fatalf("hand-off as delivered: %+v", got)
	}

	// Bob answers. The reply goes to alice, and the lead gets a copy.
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: bobID, Recipient: aliceID,
		Type: protocol.TypeReply, ParentID: got.ID, Body: "ingested"}); err != nil {
		t.Fatal(err)
	}
	copyMsg := lead.nextPush()
	if copyMsg.From != bobID || copyMsg.To != leadID || copyMsg.Type != protocol.TypeNotification || !strings.Contains(copyMsg.Body, "ingested") {
		t.Fatalf("lead copy: %+v", copyMsg)
	}
	eventually(t, "alice got the reply", func() bool {
		for _, m := range fa.delivered() {
			if m.To == aliceID && m.From == bobID && m.Body == "ingested" {
				return true
			}
		}
		return false
	})
}

func TestHandoffRefusals(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice known", func() bool { return fa.host.SessionID("fake", "na") != "" })
	lead, leadID := helloPi(t, n, "lead", "/w/lead", true)
	aliceID := fa.host.SessionID("fake", "na")
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: leadID, Type: protocol.TypeRequest, Body: "q"}); err != nil {
		t.Fatal(err)
	}
	request := lead.nextPush()
	for name, args := range map[string]map[string]any{
		"no message": {"to_session": "alice", "note": "x"},
		"no session": {"message_id": request.ID, "note": "x"},
		"unknown":    {"message_id": request.ID, "to_session": "nobody", "note": "x"},
	} {
		if _, err := lead.call("handoff", args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A lead's order, sent through the daemon, reaches the member with agent-net's
// order note. The same flag from the member to its lead is dropped and the
// sender is told.
func TestOrderFlagEndToEnd(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "fake"}, cmdline: "pi"})
	st := g.gw.Store()
	if _, err := n.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	if _, err := st.AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "lead", Path: "/w/lead"}, {Name: "member", Path: "/w/member"}}}); err != nil {
		t.Fatal(err)
	}
	lead, leadID := helloPi(t, n, "lead", "/w/lead", true)
	member, memberID := helloPi(t, n, "member", "/w/member", true)
	owner, repo := protocol.GraphRef{Kind: "agent", ID: "lead"}, protocol.GraphRef{Kind: "repo", ID: "member"}
	if err := st.WriteTeam(protocol.TeamWrite{Action: "create", ID: "forge", Name: "forge", Owner: &owner}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTeam(protocol.TeamWrite{Action: "membership", ID: "forge", Member: &repo}, "owner"); err != nil {
		t.Fatal(err)
	}

	if _, err := lead.call("send", map[string]any{"to": memberID, "body": "freeze the schema", "order": true}); err != nil {
		t.Fatal(err)
	}
	got := member.nextPush()
	if got.From != leadID || got.Body != "freeze the schema" || !got.IsOrder ||
		!strings.Contains(got.AuthorityNote, "order from your lead lead (team forge, mode charter first)") {
		t.Fatalf("order as delivered: %+v", got)
	}

	// A member cannot give its lead an order.
	if _, err := member.call("send", map[string]any{"to": leadID, "body": "do it now", "order": true}); err != nil {
		t.Fatal(err)
	}
	back := lead.nextPush()
	if back.From != memberID || back.IsOrder || back.AuthorityNote != "" {
		t.Fatalf("a member's order flag was kept: %+v", back)
	}
}
