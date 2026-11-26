package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// sentToChief is one message an owner item sent to a session.
type sentToChief struct {
	from, typ, body, key string
}

// messagesTo returns every message stored for a session.
func messagesTo(t *testing.T, s *Store, to string) []sentToChief {
	t.Helper()
	rows, err := s.db.Query(`SELECT sender,type,body,idem_key FROM messages WHERE recipient=? ORDER BY created_at,rowid`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []sentToChief
	for rows.Next() {
		var m sentToChief
		if err := rows.Scan(&m.from, &m.typ, &m.body, &m.key); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func inboxCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// captureLog collects the store's log lines as "event k=v ...".
func captureLog(s *Store) *[]string {
	var lines []string
	s.logf = func(event string, kv ...any) { lines = append(lines, event+" "+fmt.Sprintln(kv...)) }
	return &lines
}

// An agent's ask for the owner is one request from the asker to the chief of
// staff, with the text, why, the recommendation and the asker. Nothing is
// stored for the owner (decision 0196).
func TestOwnerItemIsOneRequestToTheChief(t *testing.T) {
	s, _, chief, asker, _ := chiefRig(t)
	s.keepDeskItems = false
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: protocol.AskKindQuestion, SessionID: asker, Node: "demo", FromKind: protocol.AskFromSession,
		Text: "Can I drop the old table? Should I rename the new one?", WhyYou: "It deletes data.", Recommendation: "drop it", Options: []string{"drop", "keep"}})
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != ownerItemSentToChief || item.NextWho != chief || item.MessageID == "" {
		t.Fatalf("result: %+v", item)
	}
	if n := inboxCount(t, s); n != 0 {
		t.Fatalf("%d items stored for the owner", n)
	}
	got := messagesTo(t, s, chief)
	if len(got) != 1 {
		t.Fatalf("want one message to the chief, got %d: %+v", len(got), got)
	}
	m := got[0]
	if m.from != asker || m.typ != protocol.TypeRequest || !strings.HasPrefix(m.key, ownerItemKeyPrefix) {
		t.Fatalf("message: %+v", m)
	}
	for _, want := range []string{"bob", "Can I drop the old table? Should I rename the new one?", "Why only the owner: It deletes data.", "Recommendation: drop it", "drop / keep"} {
		if !strings.Contains(m.body, want) {
			t.Errorf("body lacks %q:\n%s", want, m.body)
		}
	}
}

// An item agent-net raises itself is only a log line: no item, no message.
func TestGatewayItemIsOnlyLogged(t *testing.T) {
	s, _, chief, _, _ := chiefRig(t)
	s.keepDeskItems = false
	logs := captureLog(s)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: protocol.AskKindCheck, SessionID: gatewayAsker, Node: "agent-net", FromKind: protocol.AskFromGateway,
		NextWho: protocol.AskNextOwner, SourceRef: "tree:x", Text: "Session tree rule R1: a session has two leads"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "" || item.Status != ownerItemLogged {
		t.Fatalf("result: %+v", item)
	}
	if n := inboxCount(t, s); n != 0 {
		t.Fatalf("%d items stored", n)
	}
	if got := messagesTo(t, s, chief); len(got) != 0 {
		t.Fatalf("messages to the chief: %+v", got)
	}
	if len(*logs) != 1 || !strings.HasPrefix((*logs)[0], "desk.dropped ") || !strings.Contains((*logs)[0], "kind check") {
		t.Fatalf("log: %q", *logs)
	}
}

// A permission proposal reaches the chief as a request that names the rules
// and the targets and says how the CTO applies it.
func TestPermissionProposalIsARequestToTheChief(t *testing.T) {
	s, node, session := permissionRig(t)
	s.keepDeskItems = false
	chief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "nc", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	res, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{
			Rules: []string{"Bash(gh pr merge *)", "Bash(gh pr close *)"}, Targets: []string{"forge"}, Reason: "the forge repos need PR merges",
		},
	}, node)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "pending" {
		t.Fatalf("proposal: %+v", res)
	}
	if n := inboxCount(t, s); n != 0 {
		t.Fatalf("%d items stored for the owner", n)
	}
	got := messagesTo(t, s, chief)
	if len(got) != 1 || got[0].from != session || got[0].typ != protocol.TypeRequest {
		t.Fatalf("messages to the chief: %+v", got)
	}
	for _, want := range []string{"Bash(gh pr merge *)", "Bash(gh pr close *)", "forge", "/tmp/forge", "the forge repos need PR merges",
		"forge-agent", "permission_apply id " + res.ID, "owner_words"} {
		if !strings.Contains(got[0].body, want) {
			t.Errorf("request does not name %q:\n%s", want, got[0].body)
		}
	}
}

// An older machine's ask to hold a start is refused with the one line, and no
// ask is stored: the machine passes the refusal to the caller.
func TestStartOverrideAskIsRefused(t *testing.T) {
	s, _ := newTestStore(t)
	s.keepDeskItems = false
	node, _, _ := s.CreateNode("test", "linux")
	agent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateStartOverrideAsk(node, protocol.StartOverrideAsk{Session: agent, StartID: "a_new", Text: "an agent wants model=m. Confirm, or let the picker choose."})
	if err == nil || err.Error() != protocol.OverrideNeedsOwner {
		t.Fatalf("err = %v", err)
	}
	if n := inboxCount(t, s); n != 0 {
		t.Fatalf("%d asks stored", n)
	}
}

// agent_owner_ask answers that the ask went to the chief, with the request it
// sent, so the asking session does not wait for its user.
func TestAgentOwnerAskSaysItWentToTheChief(t *testing.T) {
	s, _, g, chief, asker, node := questionRig(t)
	s.keepDeskItems = false
	res := nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": asker, "kind": "question", "text": "Which region for the new box?", "recommendation": "eu-west"})
	red, _ := res["redirected"].(map[string]any)
	if red["chief"] != true || red["lead_session"] != chief || red["message_id"] == "" {
		t.Fatalf("answer: %+v", res)
	}
	got := messagesTo(t, s, chief)
	if len(got) != 1 || got[0].from != asker || !strings.Contains(got[0].body, "Which region for the new box?") {
		t.Fatalf("messages to the chief: %+v", got)
	}
	if n := inboxCount(t, s); n != 0 {
		t.Fatalf("%d items stored", n)
	}
}
