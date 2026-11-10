package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The chief undoes its own send while the target has not answered, so the ask
// is back with the chief and open. The undo is a row of the sort history with
// sorter chief, and one Desk log line records it.
func TestChiefUndoReturnsItsOwnSendToTheChief(t *testing.T) {
	old := readPoolMax
	readPoolMax = 0
	defer func() { readPoolMax = old }()
	s, _, chief, asker, node := chiefRig(t)
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "ncu", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	id := chiefAskKind(t, s, asker, "feature_idea", "Who owns the cache?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: id, Result: protocol.AskSortSend, To: other, Reason: "carol runs the cache"})

	res, msg, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortUndo}})
	if err != nil {
		t.Fatalf("chief undo: %v", err)
	}
	if msg.ID != "" {
		t.Fatalf("the undo sent a message %q", msg.ID)
	}
	if res.Result != protocol.AskSortUndo || res.Next != protocol.AskNextChief || res.State != protocol.AskStateOpen {
		t.Fatalf("undo result %+v", res)
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("after undo: next %q, want chief", next)
	}
	if status, answer, _ := askRow(t, s, `id=?`, id); status != "open" || answer != "" {
		t.Fatalf("after undo: status %q answer %q", status, answer)
	}
	hist, err := s.AskSortHistory(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].Result != protocol.AskSortSend || hist[1].Result != protocol.AskSortUndo || hist[1].Sorter != chief {
		t.Fatalf("history %+v", hist)
	}
	if !deskLogHas(t, s, "Undone chief sort of ask "+id) {
		t.Fatal("the chief undo has no log line")
	}
}

// An undo after the target answered is refused: the sort is no longer the last
// word on the ask.
func TestChiefUndoRefusesAnAskTheTargetAnswered(t *testing.T) {
	old := readPoolMax
	readPoolMax = 0
	defer func() { readPoolMax = old }()
	s, _, chief, asker, node := chiefRig(t)
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "ncu", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	id := chiefAskKind(t, s, asker, "feature_idea", "Who owns the cache?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: id, Result: protocol.AskSortSend, To: other, Reason: "carol runs the cache"})
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='answered', answer='carol owns it', answered_at=? WHERE id=?`, time.Now().UnixNano(), id); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortUndo}})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("undo of an ask the target answered: %v", err)
	}
	if next := askNext(t, s, id); next != other {
		t.Fatalf("the refused undo moved the ask to %q", next)
	}
}

// A sort of another sorter is not the chief's to undo.
func TestChiefUndoRefusesAnotherSortersSort(t *testing.T) {
	old := readPoolMax
	readPoolMax = 0
	defer func() { readPoolMax = old }()
	s, _, chief, asker, node := chiefRig(t)
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "ncu", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	id := chiefAsk(t, s, asker, "Who owns the cache?")
	if _, err := s.db.Exec(`INSERT INTO ask_sorts(ask_id,at,sorter,result,to_id,reason,prev_next,prev_status)
		VALUES(?,?,?,?,?,?,?,?)`, id, time.Now().UnixNano(), "s_someone_else", protocol.AskSortSend, other, "theirs", protocol.AskNextChief, "open"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE inbox_items SET next_who=? WHERE id=?`, other, id); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortUndo}})
	if err == nil || !strings.Contains(err.Error(), "not sorted by you") {
		t.Fatalf("undo of another sorter's sort: %v", err)
	}
	if next := askNext(t, s, id); next != other {
		t.Fatalf("the refused undo moved the ask to %q", next)
	}
	if hist, err := s.AskSortHistory(id); err != nil || len(hist) != 1 {
		t.Fatalf("the refused undo wrote history %+v (%v)", hist, err)
	}
}

// The owner's and the CTO's undo keep their behaviour: the ask goes back to the
// owner, not to the chief, and the undo row names its actor.
func TestOwnerAndCTOUndoStillRestoreTheOwner(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	ownerAsk := chiefAsk(t, s, asker, "Is the thread answered?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: ownerAsk, Result: protocol.AskSortClose, Reason: "answered"})
	res, err := s.UndoAsk(ownerAsk, "owner")
	if err != nil {
		t.Fatalf("owner undo: %v", err)
	}
	if res.Next != protocol.AskNextOwner || res.State != protocol.AskStateOpen {
		t.Fatalf("owner undo result %+v", res)
	}
	if next := askNext(t, s, ownerAsk); next != protocol.AskNextOwner {
		t.Fatalf("owner undo left next %q", next)
	}

	ctoAsk := chiefAskKind(t, s, asker, "approval", "Deploy the fix?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: ctoAsk, Result: protocol.AskSortClose, Reason: "answered"})
	res, err = s.UndoAsk(ctoAsk, "cto")
	if err != nil {
		t.Fatalf("CTO undo: %v", err)
	}
	if res.Next != protocol.AskNextOwner || res.State != protocol.AskStateOpen {
		t.Fatalf("CTO undo result %+v", res)
	}
	if next := askNext(t, s, ctoAsk); next != protocol.AskNextOwner {
		t.Fatalf("CTO undo left next %q", next)
	}
	if hist, err := s.AskSortHistory(ctoAsk); err != nil || len(hist) != 2 || hist[1].Sorter != "cto" {
		t.Fatalf("CTO undo history %+v (%v)", hist, err)
	}
}
