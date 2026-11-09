package gateway

import (
	"fmt"
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

// A mark is stored in the owner judgment store as a correction (kind correction,
// area chief, ref the ask id). The chief's next batch carries it, so the chief
// learns from it. There is no second table for marks.
func TestAskMarkStoresCorrectionForTheChief(t *testing.T) {
	s, c, _, asker, _ := chiefRig(t)
	id := chiefAskKind(t, s, asker, "approval", "Deploy the snapshot fix?")
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: id, Mark: protocol.MarkDidntNeedMe, Why: "the deploy is routine"}, "owner"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	found, err := s.FindJudgments(protocol.JudgmentsFindRequest{Kind: protocol.StandingKindCorrection, Ref: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("judgments of kind correction for ask %s: %+v", id, found)
	}
	j := found[0]
	if j.Area != "chief" || j.Ref != id || j.Kind != protocol.StandingKindCorrection {
		t.Fatalf("judgment area %q ref %q kind %q", j.Area, j.Ref, j.Kind)
	}
	if !strings.Contains(j.Text, "did not need ask "+id) || !strings.Contains(j.Text, "the deploy is routine") {
		t.Fatalf("judgment text %q", j.Text)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM chief_corrections`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the old table got %d rows (%v)", n, err)
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

// The batch reads the newest five marks from the judgment store, newest first,
// even when they were all made in the same second.
func TestChiefBatchReadsNewestFiveCorrectionsFromJudgments(t *testing.T) {
	s, c, _, asker, _ := chiefRig(t)
	for i := 1; i <= 6; i++ {
		id := chiefAskKind(t, s, asker, "approval", fmt.Sprintf("Deploy number %d?", i))
		if err := s.MarkAsk(protocol.AskMarkRequest{Ask: id, Mark: protocol.MarkDidntNeedMe}, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	c.t = c.t.Add(time.Minute)
	chiefAsk(t, s, asker, "Merge PR 3?")
	body, sent := sendDueChief(t, s, c.now().Add(time.Minute))
	if !sent {
		t.Fatal("no batch")
	}
	at := func(n int) int { return strings.Index(body, fmt.Sprintf("Deploy number %d?", n)) }
	if at(1) >= 0 || at(6) < 0 || at(2) < 0 || !(at(6) < at(5) && at(5) < at(4) && at(4) < at(3) && at(3) < at(2)) {
		t.Fatalf("the batch does not carry marks 6 to 2 newest first: %q", body)
	}
}

// Marks of the old chief_corrections table move into the judgment store once.
// The table stays, and a replay does not copy a row twice.
func TestChiefCorrectionsMigrateToJudgments(t *testing.T) {
	s, _ := newTestStore(t)
	for i, text := range []string{"first mark", "second mark"} {
		if _, err := s.db.Exec(`INSERT INTO chief_corrections(at,mark,text,area,ref) VALUES(?,?,?,?,?)`,
			int64(1700000000+i)*1e9, protocol.MarkDidntNeedMe, text, "demo", fmt.Sprintf("i_%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	rewind := migrationVersion(t, "Owner marks move to the judgment store") - 1
	for i := 0; i < 2; i++ {
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>?`, rewind); err != nil {
			t.Fatal(err)
		}
		if err := s.migrate(); err != nil {
			t.Fatal(err)
		}
		found, err := s.FindJudgments(protocol.JudgmentsFindRequest{Kind: protocol.StandingKindCorrection, Area: "chief"})
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 2 || found[0].Text != "second mark" || found[1].Text != "first mark" || found[0].Ref != "i_1" || found[0].SavedAt != 1700000001 {
			t.Fatalf("round %d: %+v", i, found)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM chief_corrections`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("the old table has %d rows (%v)", n, err)
	}
}
