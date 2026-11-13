package gateway

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
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

// The chief sends a draft ask to the CTO. The message carries each draft's
// id, level, scope, attribute and text, so the CTO needs no other read.
func TestChiefSendOfADraftAskCarriesTheDraftFields(t *testing.T) {
	old := readPoolMax
	readPoolMax = 0
	defer func() { readPoolMax = old }()
	s := teamStore(t)
	asker := draftSession(t, s, "demo")
	sevenFieldDrafts(t, s, asker)
	chief := draftSession(t, s, "other")
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, chief).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id IN (?,?)`, chief, asker); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	cto, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "ncto", Name: "cto-stand-in"})
	if err != nil {
		t.Fatal(err)
	}
	asks := draftAsks(t, s)
	if len(asks) != 1 {
		t.Fatalf("asks: %+v", asks)
	}
	_, msg, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: asks[0].ID, Result: protocol.AskSortSend, To: cto, Reason: "charters are yours"}})
	if err != nil {
		t.Fatal(err)
	}
	id := draftIDOf(t, s, "repo", "purpose")
	for _, want := range []string{
		"draft " + strconv.FormatInt(id, 10) + ", level repo, scope demo, purpose = Demo service",
		"level agent, scope demo, accepts = bug fixes",
		"cto_proposals_find", "learned_answer",
	} {
		if !strings.Contains(msg.Body, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg.Body)
		}
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
	if err := json.Unmarshal(handleBytes(t, r.g, r.a, "learned.answer", map[string]any{"owner": owner, "suggestion_id": -id, "action": "reject", "actor": "cto"}), &decided); err != nil {
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
