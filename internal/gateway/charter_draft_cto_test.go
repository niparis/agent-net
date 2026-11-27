package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func draftIDOf(t *testing.T, s *Store, level, attr string) int64 {
	t.Helper()
	drafts, err := s.PendingCharterDrafts()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drafts {
		if d.Level == level && d.Attr == attr {
			return d.ID
		}
	}
	t.Fatalf("no pending %s %s draft in %+v", level, attr, drafts)
	return 0
}

func TestCharterDraftReadByID(t *testing.T) {
	s := teamStore(t)
	sevenFieldDrafts(t, s, draftSession(t, s, "demo"))
	id := draftIDOf(t, s, "repo", "refuses")
	d, err := s.CharterDraftByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != id || d.Level != "repo" || d.Attr != "refuses" || d.Value != "live trading" || d.Source != protocol.CharterDraftSource || d.DraftedBy == "" || d.Status != "pending" {
		t.Fatalf("row: %+v", d)
	}
	if _, err := s.CharterDraftByID(id + 1000); err == nil {
		t.Fatal("read an unknown draft")
	}
}

func TestCtoAcceptsADraftAndUndoesIt(t *testing.T) {
	s := teamStore(t)
	sevenFieldDrafts(t, s, draftSession(t, s, "demo"))
	id := draftIDOf(t, s, "repo", "purpose")
	row, err := s.DecideCharterDraftRow(id, "accept", "cto")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "accepted" || row.DecidedBy != "cto" || row.DecidedAt == 0 || row.HistoryID == 0 {
		t.Fatalf("stored row: %+v", row)
	}
	if got, _ := draftOwnValue(s.db, "repo", "demo", "purpose"); got != "Demo service" {
		t.Fatalf("charter not written: %q", got)
	}
	if err := s.GraphUndo(row.HistoryID, "cto"); err != nil {
		t.Fatal(err)
	}
	if got, _ := draftOwnValue(s.db, "repo", "demo", "purpose"); got != "" {
		t.Fatalf("undo left %q", got)
	}
}

func TestCtoRejectsADraftWithoutWriting(t *testing.T) {
	s := teamStore(t)
	sevenFieldDrafts(t, s, draftSession(t, s, "demo"))
	id := draftIDOf(t, s, "agent", "refuses")
	row, err := s.DecideCharterDraftRow(id, "reject", "cto")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "rejected" || row.DecidedBy != "cto" || row.DecidedAt == 0 || row.HistoryID != 0 {
		t.Fatalf("stored row: %+v", row)
	}
	if got, _ := draftOwnValue(s.db, "agent", "demo", "refuses"); got != "" {
		t.Fatalf("reject wrote %q", got)
	}
	if _, err := s.DecideCharterDraftRow(id, "accept", "cto"); err == nil {
		t.Fatal("decided a draft twice")
	}
}

// The CTO reads the pending drafts and one draft by id, and decides one, over
// the owner frames the daemon calls.
func TestCharterDraftFramesReadAndDecide(t *testing.T) {
	r := newDeliveryRig(t)
	owner, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "owner", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.s.db.Exec(`INSERT INTO charter_drafts(level,scope,attr,value,source,source_hash,drafted_by,created_at) VALUES('repo','demo','refuses','live trading','AGENTS.md','h','s_agent',?)`, r.clk.now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	var list []protocol.CharterDraft
	if err := json.Unmarshal(handleBytes(t, r.g, r.a, "charter.drafts", map[string]any{"owner": owner}), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Level != "repo" || list[0].Scope != "demo" || list[0].Attr != "refuses" || list[0].Value != "live trading" || list[0].Source != "AGENTS.md" || list[0].DraftedBy != "s_agent" {
		t.Fatalf("list: %+v", list)
	}
	var one protocol.CharterDraft
	if err := json.Unmarshal(handleBytes(t, r.g, r.a, "charter.drafts", map[string]any{"owner": owner, "draft": id}), &one); err != nil || one.ID != id {
		t.Fatalf("one: %+v %v", one, err)
	}
	var decided protocol.CharterDraft
	if err := json.Unmarshal(handleBytes(t, r.g, r.a, "charter.draft_answer", map[string]any{"owner": owner, "draft": id, "action": "reject", "actor": "cto"}), &decided); err != nil {
		t.Fatal(err)
	}
	if decided.Status != "rejected" || decided.DecidedBy != "cto" {
		t.Fatalf("decided: %+v", decided)
	}
	nc := &nodeConn{id: r.a}
	if _, err := r.g.handle(context.Background(), nc, "charter.drafts", mustJSON(t, map[string]any{"owner": "s_not_owner"})); err == nil {
		t.Fatal("charter.drafts accepted a caller that is not the owner")
	}
}
