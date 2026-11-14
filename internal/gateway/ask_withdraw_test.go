package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// agentAsk creates an ask the way agent_owner_ask does: the repeat rule
// applies to these.
func agentAsk(t *testing.T, s *Store, from, kind, text string) string {
	t.Helper()
	return chiefAskFrom(t, s, from, kind, text, protocol.AskFromSession)
}

// deskAsk opens one ask and leaves it on the owner's Desk: the chief has sent
// it to the owner, so it is no longer waiting for the chief.
func deskAsk(t *testing.T, s *Store, from, kind, text string) string {
	t.Helper()
	id := agentAsk(t, s, from, kind, text)
	if _, err := s.db.Exec(`UPDATE inbox_items SET next_who='owner', owner='chief', state=? WHERE id=?`, protocol.DeskReady, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The homelab case: an asker takes back its own approval after the merge head
// moved. The item leaves the Desk at once and the log keeps the reason.
func TestAskerWithdrawsItsDeskAskThroughTheAskText(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	res := nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": asker, "kind": "approval", "text": "Approve merge #935 at 2143e3af"})
	item, _ := res["item"].(map[string]any)
	id, _ := item["id"].(string)
	if id == "" {
		t.Fatalf("the ask was not created: %+v", res)
	}
	if _, err := s.db.Exec(`UPDATE inbox_items SET next_who='owner', owner='chief', state=? WHERE id=?`, protocol.DeskReady, id); err != nil {
		t.Fatal(err)
	}

	res = nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": asker, "kind": "question",
		"text": "Withdraw " + id + ": the head moved and a newer approval replaces it"})
	if res["withdrawn"] != true {
		t.Fatalf("the withdrawal was not taken as a withdrawal: %+v", res)
	}
	status, answer, _ := askRow(t, s, `id=?`, id)
	if status != "closed" || !strings.Contains(answer, "the head moved") {
		t.Fatalf("withdrawn ask is %q with answer %q", status, answer)
	}
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if a.ID == id {
			t.Fatalf("the withdrawn ask is still on the Desk: %+v", a)
		}
	}
	var made int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE text LIKE 'Withdraw %'`).Scan(&made); err != nil {
		t.Fatal(err)
	}
	if made != 0 {
		t.Fatalf("the withdrawal text made %d new asks", made)
	}
	hist, err := s.AskSortHistory(id)
	if err != nil || len(hist) != 1 || hist[0].Result != protocol.AskSortWithdraw || hist[0].Sorter != asker {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	entries, err := s.DeskLog(3)
	if err != nil || len(entries) == 0 {
		t.Fatalf("log = %+v, %v", entries, err)
	}
	if !strings.Contains(entries[0].Text, "withdrew ask") || !strings.Contains(entries[0].Detail, "the head moved") {
		t.Fatalf("the log does not keep the withdrawal and its reason: %+v", entries[0])
	}
}

// A withdrawal text from a session that neither asked nor relayed the ask is
// refused with a clear reason, and it still makes no ask of its own.
func TestWithdrawalFromAnotherSessionIsRefusedAndMakesNoAsk(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	id := deskAsk(t, s, asker, "approval", "Approve merge #935 at 2143e3af")
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"session_id": other, "kind": "question",
		"text": "Withdraw " + id + ": I think it is stale"})
	_, err = g.handle(context.Background(), &nodeConn{id: node}, "inbox.create", raw)
	if err == nil || !strings.Contains(err.Error(), "only the asker or a session that relayed") {
		t.Fatalf("refusal = %v", err)
	}
	if status, _, _ := askRow(t, s, `id=?`, id); status != "open" {
		t.Fatalf("the refusal closed the ask: %q", status)
	}
	var made int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE text LIKE 'Withdraw %'`).Scan(&made); err != nil {
		t.Fatal(err)
	}
	if made != 0 {
		t.Fatalf("the refused withdrawal made %d new asks", made)
	}
}

// The asker takes its own ask back with agent_ask_sort, the same tool the
// chief sorts with.
func TestAskerWithdrawsItsDeskAskWithAskSort(t *testing.T) {
	s, _, _, _, asker, askerNode := questionRig(t)
	id := deskAsk(t, s, asker, "approval", "Approve merge #935 at 2143e3af")
	if _, _, err := s.SortAsk(askerNode, protocol.AskSortRequest{SessionID: asker, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortWithdraw, Reason: "the head moved"}}); err != nil {
		t.Fatalf("asker withdraw: %v", err)
	}
	if status, answer, _ := askRow(t, s, `id=?`, id); status != "closed" || !strings.Contains(answer, "the head moved") {
		t.Fatalf("withdrawn ask is %q with answer %q", status, answer)
	}
}

// The homelab case, second half: the chief withdraws the stale approval when a
// newer ask from the same asker replaces it, and names the replacement.
func TestChiefWithdrawsAReplacedDeskAsk(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	stale := deskAsk(t, s, asker, "approval", "Approve merge #935 at 2143e3af")
	c.advance(time.Minute)
	replacement := agentAsk(t, s, asker, "approval", "Approve merge #935 at 9999abcd")
	if _, err := s.db.Exec(`UPDATE inbox_items SET next_who='owner', owner='chief', state=? WHERE id=?`, protocol.DeskReady, replacement); err != nil {
		t.Fatal(err)
	}
	// A new head is a new ask, not a repeat of the stale one.
	asks, err := s.ListAsks()
	if err != nil || len(asks) != 2 {
		t.Fatalf("asks after the replacement = %+v, %v", asks, err)
	}

	// Without a replacement the chief still cannot close an ask that is on the
	// owner's Desk.
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: stale, Result: protocol.AskSortWithdraw, Reason: "the head moved"}}); err == nil {
		t.Fatal("the chief withdrew an owner-bound ask without a replacement")
	}

	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: stale, Result: protocol.AskSortWithdraw, ReplacedBy: replacement,
		Reason: "the head moved"}}); err != nil {
		t.Fatalf("chief withdrew the replaced ask: %v", err)
	}
	if status, answer, _ := askRow(t, s, `id=?`, stale); status != "closed" || !strings.Contains(answer, "the head moved") {
		t.Fatalf("stale ask is %q with answer %q", status, answer)
	}
	if status, _, _ := askRow(t, s, `id=?`, replacement); status != "open" {
		t.Fatalf("the replacement is %q", status)
	}
	entries, _ := s.DeskLog(2)
	if len(entries) == 0 || !strings.Contains(entries[0].Text, "withdrew ask") || !strings.Contains(entries[0].Detail, "the head moved") {
		t.Fatalf("the log does not keep the withdrawal and its reason: %+v", entries)
	}
}

// relaySession makes a session in project on node, the way a relay of the
// asker's question runs in the same project.
func relaySession(t *testing.T, s *Store, node, project, name string) string {
	t.Helper()
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: name, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=? WHERE id=?`, project, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The finget-ci case: one question and three relayed repeats. Every relay gets
// the first ask's ID back, and the Desk keeps one item.
func TestRelayedRepeatsJoinTheOneAsk(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project='finget-ci' WHERE id=?`, asker); err != nil {
		t.Fatal(err)
	}
	const question = "Should the finget-ci upload run against production?"
	first := nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": asker, "kind": "question", "text": question})
	firstItem, _ := first["item"].(map[string]any)
	id, _ := firstItem["id"].(string)
	if id == "" {
		t.Fatalf("the first ask was not created: %+v", first)
	}
	for _, name := range []string{"tenebrae relay 1", "tenebrae relay 2", "tenebrae relay 3"} {
		relay := relaySession(t, s, node, "finget-ci", name)
		res := nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": relay, "kind": "question", "text": question})
		item, _ := res["item"].(map[string]any)
		if got, _ := item["id"].(string); got != id {
			t.Fatalf("%s got ask %q, want the existing ask %q", name, got, id)
		}
	}
	asks, err := s.ListAsks()
	if err != nil || len(asks) != 1 || asks[0].ID != id {
		t.Fatalf("the repeats made more than one ask: %+v, %v", asks, err)
	}
	var joined int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE dup_of=? AND status=?`, id, protocol.ItemStatusJoined).Scan(&joined); err != nil {
		t.Fatal(err)
	}
	if joined != 3 {
		t.Fatalf("%d relayed repeats joined the ask, want 3", joined)
	}
	var detail string
	if err := s.db.QueryRow(`SELECT detail FROM inbox_items WHERE id=?`, id).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if strings.Count(detail, "Repeated by") != 3 {
		t.Fatalf("the repeats are not noted on the ask: %q", detail)
	}
}

// A session that relayed an ask may take it back too.
func TestRelayWithdrawsTheAskItJoined(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project='finget-ci' WHERE id=?`, asker); err != nil {
		t.Fatal(err)
	}
	const question = "Should the finget-ci upload run against production?"
	first := nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": asker, "kind": "question", "text": question})
	firstItem, _ := first["item"].(map[string]any)
	id, _ := firstItem["id"].(string)
	relay := relaySession(t, s, node, "finget-ci", "tenebrae relay")
	nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": relay, "kind": "question", "text": question})

	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: relay, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortWithdraw, Reason: "tenebrae answered it in its own chat"}}); err != nil {
		t.Fatalf("relay withdraw: %v", err)
	}
	if status, _, _ := askRow(t, s, `id=?`, id); status != "closed" {
		t.Fatalf("relay withdrawal left the ask %q", status)
	}
}

// A session stall is an event agent-net raises itself, not an agent ask. A
// stall raised right after one closed is a new event, not a repeat.
func TestEventAskAfterCloseIsANewAsk(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	first := chiefAskKind(t, s, asker, "session_stall", "stall")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: first, Result: protocol.AskSortClose, Reason: "handled"})
	c.advance(time.Minute)
	second := chiefAskKind(t, s, asker, "session_stall", "stall")
	if second == first {
		t.Fatalf("the closed stall caught the new event: %s", second)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: second, Result: protocol.AskSortClose, Reason: "handled"}}); err != nil {
		t.Fatalf("the new stall cannot be sorted: %v", err)
	}
}

// An event ask of the same asker with the same text while one is open still
// joins it, as it did before the repeat rule.
func TestEventAskWhileOpenStillJoins(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	first := chiefAskKind(t, s, asker, "session_stall", "stall")
	second := chiefAskKind(t, s, asker, "session_stall", "stall")
	if second != first {
		t.Fatalf("the open event ask was not refreshed: %s, want %s", second, first)
	}
}

// An ask that is not a clear repeat still opens as today.
func TestANewQuestionIsNotTreatedAsARepeat(t *testing.T) {
	s, _, g, _, asker, node := questionRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project='finget-ci' WHERE id=?`, asker); err != nil {
		t.Fatal(err)
	}
	first := nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": asker, "kind": "question", "text": "Should the finget-ci upload run against production?"})
	firstItem, _ := first["item"].(map[string]any)
	firstID, _ := firstItem["id"].(string)

	relay := relaySession(t, s, node, "finget-ci", "another agent")
	res := nodeCall(t, g, node, "inbox.create", map[string]any{"session_id": relay, "kind": "question", "text": "Should the finget-ci upload run against staging?"})
	item, _ := res["item"].(map[string]any)
	if got, _ := item["id"].(string); got == "" || got == firstID {
		t.Fatalf("the new question got ask %q, want a new ask", got)
	}
	asks, err := s.ListAsks()
	if err != nil || len(asks) != 2 {
		t.Fatalf("asks = %+v, %v", asks, err)
	}
}
