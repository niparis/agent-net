package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type groupReceipt struct {
	MessageID string `json:"message_id"`
	To        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"to"`
	State string `json:"state"`
}

type groupResult struct {
	Sent []groupReceipt `json:"sent"`
}

func groupReceipts(t *testing.T, raw json.RawMessage) groupResult {
	t.Helper()
	var out groupResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("group result %s: %v", raw, err)
	}
	return out
}

// A normal agent may not send to a group even when it bypasses the MCP server.
func TestGroupSendRefusedForNormalAgent(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"), fakeSession("nb", "bob"))
	eventually(t, "recipients known", func() bool {
		return fa.host.SessionID("fake", "na") != "" && fa.host.SessionID("fake", "nb") != ""
	})
	pi, _ := helloPi(t, n, "sender", "/w/sender", true)

	_, err := pi.call("send", map[string]any{"to_set": map[string]any{"harness": "fake"}, "body": "hello group"})
	if err == nil || err.Error() != "group messages are for the CTO and advisor nodes only; send to one session with to" {
		t.Fatalf("group send from a normal agent: %v", err)
	}
	if len(fa.delivered()) != 0 {
		t.Fatalf("refused group send delivered %d messages", len(fa.delivered()))
	}
}

// Exactly one of to and to_set is allowed.
func TestSendRefusesToAndToSetTogether(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, _ := helloPi(t, n, "sender", "/w/sender", true)
	for _, args := range []map[string]any{
		{"to": "s_x", "to_set": map[string]any{"harness": "fake"}, "body": "both"},
		{"body": "neither"},
	} {
		if _, err := pi.call("send", args); err == nil || !strings.Contains(err.Error(), "exactly one of to and to_set") {
			t.Fatalf("send %v: %v", args, err)
		}
	}
}

// A CTO session sends a group message to every matching online session once.
func TestCTOGroupSendReachesEveryRecipientOnce(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"), fakeSession("nb", "bob"))
	eventually(t, "recipients known", func() bool {
		return fa.host.SessionID("fake", "na") != "" && fa.host.SessionID("fake", "nb") != ""
	})
	want := map[string]bool{fa.host.SessionID("fake", "na"): true, fa.host.SessionID("fake", "nb"): true}

	cto, ctoID := helloPi(t, n, "cto", "/w/cto", true)
	if _, err := n.d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id`, ctoID); err != nil {
		t.Fatal(err)
	}
	raw, err := cto.call("send", map[string]any{"to_set": map[string]any{"harness": "fake"}, "body": "from the CTO"})
	if err != nil {
		t.Fatalf("CTO group send: %v", err)
	}
	got := groupReceipts(t, raw)
	if len(got.Sent) != 2 {
		t.Fatalf("receipts: %+v", got.Sent)
	}
	seen := map[string]bool{}
	for _, r := range got.Sent {
		if !want[r.To.ID] {
			t.Fatalf("unexpected recipient %+v", r)
		}
		if r.MessageID == "" || r.State == "" || r.To.Name == "" {
			t.Fatalf("incomplete receipt %+v", r)
		}
		if seen[r.To.ID] {
			t.Fatalf("recipient %s appears twice", r.To.ID)
		}
		seen[r.To.ID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("missed a recipient: %+v", got.Sent)
	}
	eventually(t, "both recipients got the message", func() bool { return len(fa.delivered()) == 2 })
	for _, m := range fa.delivered() {
		if m.Body != "from the CTO" || !want[m.To] {
			t.Fatalf("delivery: %+v", m)
		}
	}
}

// An advisor node is the source of a governs edge in mode advisor.
func TestAdvisorGroupSendReachesEveryRecipientOnce(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"), fakeSession("nb", "bob"))
	eventually(t, "recipients known", func() bool {
		return fa.host.SessionID("fake", "na") != "" && fa.host.SessionID("fake", "nb") != ""
	})
	st := g.gw.Store()
	if _, err := st.AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: "/w/shop"}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEdge(protocol.Edge{From: "architect", To: "shop", Kind: "governs", Mode: "advisor"}, "owner"); err != nil {
		t.Fatal(err)
	}

	advisor, _ := helloPi(t, n, "advisor", "/w/architect", true)
	raw, err := advisor.call("send", map[string]any{"to_set": map[string]any{"harness": "fake"}, "body": "advice"})
	if err != nil {
		t.Fatalf("advisor group send: %v", err)
	}
	got := groupReceipts(t, raw)
	if len(got.Sent) != 2 {
		t.Fatalf("receipts: %+v", got.Sent)
	}
	eventually(t, "both recipients got the message", func() bool { return len(fa.delivered()) == 2 })
}

// as_owner is CTO only.
func TestAsOwnerRefusedForNormalAgent(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "recipient known", func() bool { return fa.host.SessionID("fake", "na") != "" })
	pi, _ := helloPi(t, n, "sender", "/w/sender", true)
	_, err := pi.call("send", map[string]any{"to": fa.host.SessionID("fake", "na"), "body": "as owner", "as_owner": true})
	if err == nil || !strings.Contains(err.Error(), "as_owner") {
		t.Fatalf("as_owner from a normal agent: %v", err)
	}
}

// as_owner from the CTO becomes an owner proposal.
func TestCTOAsOwnerBecomesAProposal(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "recipient known", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	cto, ctoID := helloPi(t, n, "cto", "/w/cto", true)
	if _, err := n.d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id`, ctoID); err != nil {
		t.Fatal(err)
	}
	raw, err := cto.call("send", map[string]any{"to": aliceID, "body": "owner words", "as_owner": true})
	if err != nil {
		t.Fatalf("as_owner from the CTO: %v", err)
	}
	got := groupReceipts(t, raw)
	if len(got.Sent) != 1 || !strings.HasPrefix(got.Sent[0].MessageID, "p_") || got.Sent[0].State != "proposed" {
		t.Fatalf("proposal receipt: %s", raw)
	}
	var count int
	if err := n.d.st.db.QueryRow(`SELECT COUNT(*) FROM cto_proposals WHERE status='open'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("open proposals: %d %v", count, err)
	}
}

// One send returns one receipt with the recipient's id and name.
func TestSendReturnsOneReceipt(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "recipient known", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	pi, _ := helloPi(t, n, "sender", "/w/sender", true)

	raw, err := pi.call("send", map[string]any{"to": aliceID, "body": "one", "receipt": true})
	if err != nil {
		t.Fatal(err)
	}
	got := groupReceipts(t, raw)
	if len(got.Sent) != 1 || got.Sent[0].To.ID != aliceID || got.Sent[0].To.Name != "alice" || got.Sent[0].MessageID == "" {
		t.Fatalf("receipt: %s", raw)
	}
	eventually(t, "message delivered", func() bool { return len(fa.delivered()) == 1 })
}

// A reply returns one receipt for the peer, and keeps the outcome rules.
func TestReplyReturnsOneReceiptAndKeepsOutcomeRules(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "recipient known", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	pi, piID := helloPi(t, n, "sender", "/w/sender", true)
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: piID,
		Type: protocol.TypeRequest, Body: "please review"}); err != nil {
		t.Fatal(err)
	}
	request := pi.nextPush()

	raw, err := pi.call("reply", map[string]any{"message_id": request.ID, "body": "done", "outcome": "accept", "receipt": true})
	if err != nil {
		t.Fatal(err)
	}
	var receipt groupReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.MessageID == "" || receipt.To.ID != aliceID || receipt.To.Name != "alice" {
		t.Fatalf("reply receipt %s: %v", raw, err)
	}
	for _, args := range []map[string]any{
		{"message_id": request.ID, "body": "no", "outcome": "reject"},
		{"message_id": request.ID, "body": "elsewhere", "outcome": "redirect", "reason": "not me"},
		{"message_id": request.ID, "body": "ask", "outcome": "escalate", "reason": "unclear"},
	} {
		if _, err := pi.call("reply", args); err == nil {
			t.Fatalf("reply %v accepted", args)
		}
	}
}
