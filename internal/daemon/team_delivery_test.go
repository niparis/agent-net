package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestTeamRecipientResolution(t *testing.T) {
	active := true
	ref := func(kind, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: kind, ID: id} }
	graph := protocol.Graph{Nodes: []protocol.Node{
		{Name: "Finance", Ref: ref("team", "finance"), Active: &active},
		{Name: "Alice", Ref: ref("agent", "alice")}, {Name: "Bob", Ref: ref("agent", "bob")},
	}, Edges: []protocol.Edge{
		{Kind: "member_of", FromRef: ref("agent", "alice"), ToRef: ref("team", "finance")},
		{Kind: "member_of", FromRef: ref("agent", "bob"), ToRef: ref("team", "finance")},
	}}
	sessions := []protocol.AgentView{{AgentID: "alice", SessionID: "online", Online: true}, {AgentID: "bob", SessionID: "offline", Online: false}, {AgentID: "outsider", SessionID: "outside", Online: true}}
	ids, missed, err := teamRecipients(graph, "Finance", sessions)
	if err != nil || !ids["online"] || ids["offline"] || ids["outside"] || len(ids) != 1 || len(missed) != 1 || missed[0].ID != "bob" {
		t.Fatalf("recipients=%v missed=%+v err=%v", ids, missed, err)
	}
	if _, _, err = teamRecipients(graph, "missing", sessions); err == nil || !strings.Contains(err.Error(), "unknown team") {
		t.Fatalf("unknown team: %v", err)
	}
}

func TestTeamRepoLinkedAndUnlinked(t *testing.T) {
	active := true
	ref := func(kind, id string) *protocol.GraphRef { return &protocol.GraphRef{Kind: kind, ID: id} }
	graph := protocol.Graph{Nodes: []protocol.Node{
		{Name: "Team", Ref: ref("team", "t"), Active: &active},
		{Name: "Linked", Ref: ref("repo", "linked"), Owner: ref("agent", "a")},
		{Name: "Unlinked", Ref: ref("repo", "unlinked")},
	}, Edges: []protocol.Edge{
		{Kind: "member_of", FromRef: ref("repo", "linked"), ToRef: ref("team", "t")},
		{Kind: "member_of", FromRef: ref("repo", "unlinked"), ToRef: ref("team", "t")},
	}}
	ids, missed, err := teamRecipients(graph, "t", []protocol.AgentView{{AgentID: "a", SessionID: "s", Online: true}, {AgentID: "b", SessionID: "outside", Online: true}})
	if err != nil || len(ids) != 1 || !ids["s"] || len(missed) != 1 || missed[0].ID != "unlinked" || missed[0].Reason != "repo has no linked agent" {
		t.Fatalf("ids=%v missed=%+v err=%v", ids, missed, err)
	}
}

func TestTeamGroupSendExcludesOutsider(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	cli := cliClient(t, n)
	eventually(t, "owner session", func() bool { return n.d.ownerSessionID() != "" })
	_, err := g.gw.Store().AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "alice", Path: "/work/alice"}, {Name: "bob", Path: "/work/bob"}, {Name: "outside", Path: "/work/outside"}}})
	if err != nil {
		t.Fatal(err)
	}
	fa.setSessions(fakeSession("a", "alice"), fakeSession("o", "outside"))
	eventually(t, "sessions known", func() bool { return fa.host.SessionID("fake", "a") != "" && fa.host.SessionID("fake", "o") != "" })
	if _, err = n.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	cli.mustCall("team_write", map[string]any{"action": "create", "team_id": "finance", "name": "Finance"})
	for _, id := range []string{"alice", "bob"} {
		cli.mustCall("team_write", map[string]any{"action": "membership", "team_id": "finance", "member": map[string]any{"kind": "agent", "id": id}})
	}
	cto, ctoID := helloPi(t, n, "cto", "/w/cto", true)
	if _, err = n.d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id`, ctoID); err != nil {
		t.Fatal(err)
	}
	raw, err := cto.call("send", map[string]any{"to_set": map[string]any{"team": "Finance"}, "body": "team order"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sent   []groupReceipt     `json:"sent"`
		Missed []missedTeamMember `json:"missed"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sent) != 1 || got.Sent[0].To.ID != fa.host.SessionID("fake", "a") || len(got.Missed) != 1 || got.Missed[0].ID != "bob" {
		t.Fatalf("result: %s", raw)
	}
	eventually(t, "team delivery", func() bool { return len(fa.delivered()) == 1 })
	if m := fa.delivered()[0]; m.To != fa.host.SessionID("fake", "a") {
		t.Fatalf("outside delivery: %+v", m)
	}
	if _, err = cto.call("send", map[string]any{"to_set": map[string]any{"team": "unknown"}, "body": "order"}); err == nil || !strings.Contains(err.Error(), "unknown team") {
		t.Fatalf("unknown team: %v", err)
	}
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	ownerValue, err := n.d.runCTOToolRaw(context.Background(), "agent_message_send", json.RawMessage(`{"to_set":{"team":"finance"},"body":"owner order","as_owner":true}`))
	ownerRaw, _ := json.Marshal(ownerValue)
	if err != nil {
		t.Fatal(err)
	}
	var ownerResult struct {
		Sent []groupReceipt `json:"sent"`
	}
	if err := json.Unmarshal(ownerRaw, &ownerResult); err != nil || len(ownerResult.Sent) != 1 || strings.HasPrefix(ownerResult.Sent[0].MessageID, "p_") {
		t.Fatalf("owner proposal: %s %v", ownerRaw, err)
	}
	secondValue, err := n.d.runCTOToolRaw(context.Background(), "agent_message_send", json.RawMessage(`{"to_set":{"team":"finance"},"body":"approved order","as_owner":true}`))
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, _ := json.Marshal(secondValue)
	var secondResult struct {
		Sent []groupReceipt `json:"sent"`
	}
	if err := json.Unmarshal(secondRaw, &secondResult); err != nil || len(secondResult.Sent) != 1 {
		t.Fatalf("second proposal: %s %v", secondRaw, err)
	}
	eventually(t, "owner team deliveries", func() bool { return len(fa.delivered()) == 3 })
	for _, receipt := range []groupReceipt{ownerResult.Sent[0], secondResult.Sent[0]} {
		m, ok, err := g.gw.Store().Message(receipt.MessageID)
		if err != nil || !ok || !m.FromCTO || !m.OwnerApproved {
			t.Fatalf("owner message %+v %v", m, err)
		}
	}
	cli.mustCall("team_write", map[string]any{"action": "membership", "expect_team": "finance", "member": map[string]any{"kind": "agent", "id": "alice"}})
	third, err := n.d.runCTOToolRaw(context.Background(), "agent_message_send", json.RawMessage(`{"to_set":{"team":"finance"},"body":"later","as_owner":true}`))
	b, _ := json.Marshal(third)
	var thirdResult struct {
		Sent []groupReceipt `json:"sent"`
	}
	_ = json.Unmarshal(b, &thirdResult)
	if err != nil || len(thirdResult.Sent) != 0 {
		t.Fatalf("removed member: %s %v", b, err)
	}

}
