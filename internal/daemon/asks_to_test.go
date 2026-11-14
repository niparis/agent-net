package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// forgeNodes starts a gateway and a node with a team lead and one member, in a
// team that sends the questions of its members to its lead.
func forgeNodes(t *testing.T) (n *node, lead, member *localClient, leadID, memberID string, st *gateway.Store) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	n = startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "fake"}, cmdline: "pi"})
	store := g.gw.Store()
	if _, err := n.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	if _, err := store.AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "lead", Path: "/w/lead"}, {Name: "member", Path: "/w/member"}}}); err != nil {
		t.Fatal(err)
	}
	lead, leadID = helloPi(t, n, "lead", "/w/lead", true)
	member, memberID = helloPi(t, n, "member", "/w/member", true)
	owner, repo := protocol.GraphRef{Kind: "agent", ID: "lead"}, protocol.GraphRef{Kind: "repo", ID: "member"}
	for _, q := range []protocol.TeamWrite{
		{Action: "create", ID: "forge", Name: "forge", Owner: &owner},
		{Action: "membership", ID: "forge", Member: &repo},
		{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead},
	} {
		if err := store.WriteTeam(q, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	return n, lead, member, leadID, memberID, store
}

func ownerInbox(t *testing.T, n *node) []protocol.InboxItem {
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
	return listed.Items
}

// A member reads its team lead for the Claude hint hook. The lead itself has
// none, and a team that sends questions to the owner has none.
func TestTeamLeadReadForClaudeHint(t *testing.T) {
	t.Parallel()
	_, lead, member, leadID, _, st := forgeNodes(t)

	raw, err := member.call(protocol.MethodTeamLead, protocol.TeamLeadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var res protocol.TeamLeadResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Team != "forge" || res.Lead != "lead" || res.LeadSession != leadID {
		t.Fatalf("member lead: %+v, lead session %s", res, leadID)
	}

	raw, err = lead.call(protocol.MethodTeamLead, protocol.TeamLeadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	res = protocol.TeamLeadResult{}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Team != "" || res.LeadSession != "" {
		t.Fatalf("the lead got a lead: %+v", res)
	}

	if err := st.WriteTeam(protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToOwner}, "owner"); err != nil {
		t.Fatal(err)
	}
	raw, err = member.call(protocol.MethodTeamLead, protocol.TeamLeadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	res = protocol.TeamLeadResult{}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.LeadSession != "" {
		t.Fatalf("asks_to=owner still named a lead: %+v", res)
	}
}

// The owner's ask question path is unchanged by the read.
func TestTeamLeadReadChangesNothing(t *testing.T) {
	t.Parallel()
	n, _, member, _, memberID, _ := forgeNodes(t)
	if _, err := member.call(protocol.MethodTeamLead, protocol.TeamLeadRequest{}); err != nil {
		t.Fatal(err)
	}
	if w := waitingOf(t, n, memberID); w != nil {
		t.Fatalf("the read made the member wait: %+v", w)
	}
}

// A member of a team that asks its lead calls agent_owner_ask. The owner sees
// nothing, the member is told and carries on, the lead gets the question, and
// the lead's reply reaches the member.
func TestOwnerAskGoesToTheTeamLeadEndToEnd(t *testing.T) {
	t.Parallel()
	n, lead, member, leadID, memberID, _ := forgeNodes(t)

	raw, err := member.call("ask_question", map[string]any{"text": "Merge the forge rules?", "recommendation": "yes", "options": []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Redirected *protocol.AskRedirect `json:"redirected"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Redirected == nil {
		t.Fatalf("not redirected: %s %v", raw, err)
	}
	if res.Redirected.Team != "forge" || res.Redirected.Lead != "lead" || !strings.HasPrefix(res.Redirected.Note, "Your team (forge) sends questions to its lead, lead. Sent there;") {
		t.Fatalf("redirect: %+v", res.Redirected)
	}
	if w := waitingOf(t, n, memberID); w != nil {
		t.Fatalf("member waits for its user: %+v", w)
	}
	if items := ownerInbox(t, n); len(items) != 0 {
		t.Fatalf("owner inbox: %+v", items)
	}

	// The lead gets the question as a message and answers with a normal reply.
	got := lead.nextPush()
	if got.From != memberID || got.To != leadID || !strings.Contains(got.Body, "Merge the forge rules?") {
		t.Fatalf("lead got: %+v", got)
	}
	if _, err := lead.call("reply", map[string]any{"message_id": got.ID, "body": "yes, merge"}); err != nil {
		t.Fatal(err)
	}
	back := member.nextPush()
	if back.From != leadID || back.Body != "yes, merge" {
		t.Fatalf("member got: %+v", back)
	}

	// The lead's own question is not redirected: it waits for the owner.
	raw, err = lead.call("ask_question", map[string]any{"text": "Spend money?", "recommendation": "no"})
	if err != nil {
		t.Fatal(err)
	}
	var own struct {
		Redirected *protocol.AskRedirect `json:"redirected"`
		Item       protocol.InboxItem    `json:"item"`
	}
	if err := json.Unmarshal(raw, &own); err != nil || own.Redirected != nil || own.Item.ID == "" {
		t.Fatalf("lead's ask: %s %v", raw, err)
	}
	eventually(t, "lead waits for the owner", func() bool {
		w := waitingOf(t, n, leadID)
		return w != nil && w.Text == "Spend money?"
	})
}

// The explicit wait of a plugin (Pi, OpenCode) is redirected the same way.
func TestExplicitWaitGoesToTheTeamLead(t *testing.T) {
	t.Parallel()
	n, lead, member, leadID, memberID, _ := forgeNodes(t)
	raw, err := member.call("waiting", map[string]any{"explicit": true, "kind": "question", "text": "Which schema?"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "sends questions to its lead") {
		t.Fatalf("result: %s", raw)
	}
	got := lead.nextPush()
	if got.From != memberID || got.To != leadID || !strings.Contains(got.Body, "Which schema?") {
		t.Fatalf("lead got: %+v", got)
	}
	if w := waitingOf(t, n, memberID); w != nil {
		t.Fatalf("member waits for its user: %+v", w)
	}
}
