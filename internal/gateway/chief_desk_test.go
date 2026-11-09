package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// sortAsk is one sort of the live chief, for the Desk tests.
func sortAsk(t *testing.T, s *Store, chief, node string, args protocol.AskSortArgs) {
	t.Helper()
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: args}); err != nil {
		t.Fatalf("sort %+v: %v", args, err)
	}
}

// deskLogHas reports whether one log line contains the text.
func deskLogHas(t *testing.T, s *Store, text string) bool {
	t.Helper()
	entries, err := s.DeskLog(50)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}

// The owner's undo puts a closed ask back. The ask is open and with the owner
// again: the chief's batch was sent once and is not sent again, so the chief
// is not given the ask a second time. The sort history keeps both rows.
func TestAskUndoRestoresPriorState(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	id := chiefAsk(t, s, asker, "Is the thread answered?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: id, Result: protocol.AskSortClose, Reason: "answered in the thread"})

	res, err := s.UndoAsk(id, "owner")
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if res.Result != protocol.AskSortUndo || res.Next != protocol.AskNextOwner || res.State != protocol.AskStateOpen {
		t.Fatalf("undo result %+v", res)
	}
	if status, answer, _ := askRow(t, s, `id=?`, id); status != "open" || answer != "" {
		t.Fatalf("after undo: status %q answer %q", status, answer)
	}
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("after undo: next %q, want owner", next)
	}
	hist, err := s.AskSortHistory(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[1].Result != protocol.AskSortUndo || hist[1].Sorter != "owner" {
		t.Fatalf("history %+v", hist)
	}
	if !deskLogHas(t, s, "Undone chief sort of ask "+id) {
		t.Fatal("the undo has no log line")
	}
}

// An ask that changed since the chief's sort is not undone: the session answered
// it, so the sort is no longer the last word. A second undo is refused too.
func TestAskUndoRefusesChangedAsk(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	sent := chiefAskKind(t, s, asker, "feature_idea", "Who owns the cache?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: sent, Result: protocol.AskSortSend, To: other, Reason: "carol runs the cache"})
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='answered', answer='carol owns it', answered_at=? WHERE id=?`, time.Now().UnixNano(), sent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UndoAsk(sent, "owner"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("undo of a changed ask: %v", err)
	}

	closed := chiefAsk(t, s, asker, "Is the thread answered?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: closed, Result: protocol.AskSortClose, Reason: "answered"})
	if _, err := s.UndoAsk(closed, "owner"); err != nil {
		t.Fatalf("first undo: %v", err)
	}
	if _, err := s.UndoAsk(closed, "owner"); err == nil {
		t.Fatal("a second undo of the same sort was accepted")
	}
}

// Only the owner or the CTO may undo or mark. An agent session may not.
func TestAskUndoAndMarkNeedOwnerOrCTO(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	id := chiefAsk(t, s, asker, "Is the thread answered?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: id, Result: protocol.AskSortClose, Reason: "answered"})
	if _, err := s.UndoAsk(id, asker); err == nil {
		t.Fatal("an agent session undid a sort")
	}
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: id, Mark: protocol.MarkDidntNeedMe}, asker); err == nil {
		t.Fatal("an agent session marked an ask")
	}
	if _, err := s.UndoAsk(id, "cto"); err != nil {
		t.Fatalf("the CTO could not undo: %v", err)
	}
}

// A mark is stored as a correction with the ask's project as area and the ask id
// as ref. The chief's next batch carries it, so the chief learns from it.
func TestAskMarkStoresCorrectionForTheChief(t *testing.T) {
	s, c, _, asker, _ := chiefRig(t)
	id := chiefAskKind(t, s, asker, "approval", "Deploy the snapshot fix?")
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: id, Mark: protocol.MarkDidntNeedMe, Why: "the deploy is routine"}, "owner"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	var area, ref, text, mark string
	if err := s.db.QueryRow(`SELECT area,ref,text,mark FROM chief_corrections`).Scan(&area, &ref, &text, &mark); err != nil {
		t.Fatal(err)
	}
	if area != "demo" || ref != id || mark != protocol.MarkDidntNeedMe {
		t.Fatalf("correction area %q ref %q mark %q", area, ref, mark)
	}
	if !strings.Contains(text, "did not need ask "+id) || !strings.Contains(text, "the deploy is routine") {
		t.Fatalf("correction text %q", text)
	}
	if !deskLogHas(t, s, "Owner marked ask "+id) {
		t.Fatal("the mark has no log line")
	}

	c.t = c.t.Add(time.Minute)
	chiefAsk(t, s, asker, "Merge PR 3?")
	body, sent := sendDueChief(t, s, c.now().Add(time.Minute))
	if !sent || !strings.Contains(body, "Owner corrections") || !strings.Contains(body, "did not need ask "+id) {
		t.Fatalf("the batch does not carry the mark: %q", body)
	}
}

func TestAskMarkRefusesUnknownMark(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Merge PR 3?")
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: id, Mark: "bogus"}, "owner"); err == nil {
		t.Fatal("an unknown mark was stored")
	}
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: "i_missing", Mark: protocol.MarkShouldHaveAsked}, "owner"); err == nil {
		t.Fatal("a mark on an unknown ask was stored")
	}
}

// The Handled for you list is the chief's sorts of the last day, newest first.
// An undone sort is marked as undone, and the undo row is not a row of its own.
func TestChiefSortsListsTheLastDay(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	closeID := chiefAsk(t, s, asker, "Is the thread answered?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: closeID, Result: protocol.AskSortClose, Reason: "answered in the thread"})
	sendID := chiefAskKind(t, s, asker, "feature_idea", "Who owns the cache?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: sendID, Result: protocol.AskSortSend, To: other, Reason: "carol runs the cache"})
	if _, err := s.UndoAsk(sendID, "owner"); err != nil {
		t.Fatal(err)
	}

	list, err := s.ChiefSorts(c.now().Add(-protocol.ChiefSortsWindow))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("sorts %+v, want 2", list)
	}
	if list[0].Ask != sendID || list[0].Result != protocol.AskSortSend || !list[0].Undone || list[0].Question != "Who owns the cache?" {
		t.Fatalf("newest sort %+v", list[0])
	}
	if list[1].Ask != closeID || list[1].Result != protocol.AskSortClose || list[1].Undone {
		t.Fatalf("older sort %+v", list[1])
	}
	if late, _ := s.ChiefSorts(c.now().Add(time.Minute)); len(late) != 0 {
		t.Fatalf("sorts after the window: %+v", late)
	}
}
