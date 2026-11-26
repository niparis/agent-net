package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// storeOldItem stores an owner item the way a gateway did before decision 0196,
// when items still waited on the Desk. A gateway no longer makes one, but the
// answer, sort and close paths still serve the ones it stored.
func storeOldItem(t *testing.T, s *Store, item protocol.InboxItem) (protocol.InboxItem, error) {
	t.Helper()
	if item.NextWho == "" {
		item.NextWho = protocol.AskNextOwner
	}
	item.ID, item.Status, item.CreatedAt, item.Category = protocol.NewID("i"), "open", s.now().UTC(), itemCategory(item)
	options, err := json.Marshal(item.Options)
	if err != nil {
		return item, err
	}
	_, err = s.db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,
		from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,route_fixed,category) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?)`,
		item.ID, item.Kind, item.SessionID, item.Node, item.Text, item.Recommendation, item.MessageID, item.ConversationID, item.CreatedAt.UnixNano(), item.Status,
		string(options), item.AskerName, item.ChangeTitle, item.FromKind, item.SourceRef, item.AboutKind, item.AboutID, item.NextWho, item.WhyYou, item.Detail, item.Category)
	return item, err
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
		"forge-agent", "permission_apply id " + res.ID, "permission_reject id " + res.ID, "owner_words"} {
		if !strings.Contains(got[0].body, want) {
			t.Errorf("request does not name %q:\n%s", want, got[0].body)
		}
	}
}

// A message an 'ask the owner' rule holds reaches the chief as a request that
// names the held message and tells how the CTO releases it.
func TestHeldMessageRequestNamesHeldAnswer(t *testing.T) {
	s, _, a, b := heldRig(t)
	node, _, _ := s.CreateNode("chief-box", "linux")
	chief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	m, _, err := send(s, a, b, "k1", "")
	if err != nil || m.State != protocol.StateHeld {
		t.Fatalf("message %+v %v", m, err)
	}
	got := messagesTo(t, s, chief)
	if len(got) != 1 {
		t.Fatalf("want one request to the chief, got %+v", got)
	}
	want := "If the owner agrees, the CTO answers it with held_answer, message_id " + m.ID + ", action a to deliver or r to refuse."
	if !strings.HasSuffix(got[0].body, want) {
		t.Fatalf("request does not end with the held_answer line:\n%s", got[0].body)
	}
}

// An older machine's ask to hold a start is refused with the one line, and no
// ask is stored: the machine passes the refusal to the caller.
func TestStartOverrideAskIsRefused(t *testing.T) {
	s, _ := newTestStore(t)
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

// reconcileTimes runs the minute reconcile n times.
func reconcileTimes(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.reconcileAsks(); err != nil {
			t.Fatal(err)
		}
	}
}

// A held message reaches the chief once. The minute reconcile finds no stored
// ask for it, and a repeat tick must not send it again.
func TestHeldMessageReachesTheChiefOnce(t *testing.T) {
	s, _, a, b := heldRig(t)
	node, _, _ := s.CreateNode("chief-box", "linux")
	chief, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief"})
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "k1", ""); err != nil {
		t.Fatal(err)
	}
	reconcileTimes(t, s, 3)
	if got := messagesTo(t, s, chief); len(got) != 1 {
		t.Fatalf("want one request to the chief, got %d", len(got))
	}
}

// A session that waits past the debounce reaches the chief once for each
// thing it waits on, however often the reconcile runs.
func TestWaitingSessionReachesTheChiefOncePerWait(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	chief, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief"})
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	info := protocol.SessionInfo{Harness: "claude", NativeID: "w", Name: "waiter", Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Which database?"}}
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	c.advance(waitingAskDebounce)
	reconcileTimes(t, s, 3)
	if got := messagesTo(t, s, chief); len(got) != 1 {
		t.Fatalf("one wait: want one request to the chief, got %d", len(got))
	}
	info.Waiting = &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Which region?"}
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	c.advance(waitingAskDebounce)
	reconcileTimes(t, s, 3)
	if got := messagesTo(t, s, chief); len(got) != 2 {
		t.Fatalf("a new wait: want two requests to the chief, got %d", len(got))
	}
}

// A ready learned suggestion is no item for the owner, so the minute reconcile
// writes no Desk log line for it.
func TestLearnedSuggestionMakesNoOwnerItem(t *testing.T) {
	s, _ := newTestStore(t)
	logs := captureLog(s)
	now := s.now().UnixNano()
	if _, err := s.db.Exec(`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES('demo','accepts','','add','billing','[]','pending','major',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	reconcileTimes(t, s, 2)
	for _, l := range *logs {
		if strings.HasPrefix(l, "desk.") {
			t.Fatalf("reconcile made an owner item: %s", l)
		}
	}
}

// The chief's import of a turn's question is one request to the chief, not a
// stored ask (decision 0196). A second import of the same turn sends nothing
// and names the first.
func TestAskIntakeImportIsOneRequestToTheChief(t *testing.T) {
	s, _, chief, asker, _ := chiefRig(t)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}, logger: log.New(io.Discard, "", 0)}
	q := protocol.AskIntakeRequest{SessionID: chief, AskIntakeArgs: protocol.AskIntakeArgs{Action: protocol.AskIntakeImport, Session: asker,
		Turn: protocol.AskIntakeSourceRefPrefix + asker + ":t1", Text: "Rotate the stale vault backup now?", Options: []string{"rotate", "later"}}}
	first, err := g.askIntakeImport(context.Background(), q)
	if err != nil || first.Ask == nil || first.Ask.State != protocol.AskIntakeCreated || first.Ask.ID == "" {
		t.Fatalf("first import = %+v, %v", first, err)
	}
	second, err := g.askIntakeImport(context.Background(), q)
	if err != nil || second.Ask == nil || second.Ask.State != protocol.AskIntakeAlreadyImported || second.Ask.ID != first.Ask.ID {
		t.Fatalf("second import = %+v, %v; first %+v", second, err, first)
	}
	if got := messagesTo(t, s, chief); len(got) != 1 {
		t.Fatalf("want one request to the chief, got %d", len(got))
	}
}
