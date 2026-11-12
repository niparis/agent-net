package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Plan step (d): the front door closes the open Desk items that waited on the
// owner, once. The owner's words, typed to the agent-net lead on 2026-11-12:
// "note that i dont care if the inbox is flushed. Right now it's pretty bad,
// i only use it when forced."

// legacyAsk is an inbox row as a gateway before the front door wrote it.
type legacyAsk struct {
	id, kind, next, status, sourceRef string
}

func seedLegacyAsks(t *testing.T, s *Store, asks ...legacyAsk) {
	t.Helper()
	for i, a := range asks {
		if a.status == "" {
			a.status = "open"
		}
		if _, err := s.db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,source_ref,next_who)
			VALUES (?,?,'s_asker','demo',?,'','','',?,?,'[]',?,?)`,
			a.id, a.kind, "ask "+a.id, int64(i+1), a.status, a.sourceRef, a.next); err != nil {
			t.Fatal(err)
		}
	}
}

// rerunFrontDoorFlush rewinds the store to before the flush migration and
// migrates it again. fresh also clears the flush marker, as a gateway that
// never ran the flush has none.
func rerunFrontDoorFlush(t *testing.T, s *Store, fresh bool) {
	t.Helper()
	if fresh {
		if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key=?`, frontDoorFlushKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "Front door flush of open owner items")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
}

// flushRow reads what the flush keeps on an item.
func flushRow(t *testing.T, s *Store, id string) (status, answer string, answeredAt int64) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT status,answer,answered_at FROM inbox_items WHERE id=?`, id).Scan(&status, &answer, &answeredAt); err != nil {
		t.Fatal(err)
	}
	return
}

func TestFrontDoorFlushClosesOwnerItemsAndKeepsTheRows(t *testing.T) {
	s, c := newTestStore(t)
	seedLegacyAsks(t, s,
		legacyAsk{id: "i_blank", kind: protocol.AskKindQuestion, next: ""},
		legacyAsk{id: "i_owner", kind: protocol.AskKindCheck, next: protocol.AskNextOwner},
		legacyAsk{id: "i_sign", kind: protocol.AskKindSignIn, next: protocol.AskNextOwner, sourceRef: "sign-in:claude"},
		legacyAsk{id: "i_chief", kind: protocol.AskKindQuestion, next: protocol.AskNextChief},
		legacyAsk{id: "i_lead", kind: protocol.AskKindQuestion, next: "s_lead"},
		legacyAsk{id: "i_gate", kind: protocol.AskKindApproval, next: protocol.AskNextOwner},
		legacyAsk{id: "i_perm", kind: protocol.AskKindPermission, next: protocol.AskNextOwner, sourceRef: permissionAskPrefix + "p1"},
		legacyAsk{id: "i_old", kind: protocol.AskKindQuestion, status: "answered"},
		legacyAsk{id: "i_joined", kind: protocol.AskKindQuestion, status: protocol.ItemStatusJoined})
	for _, stmt := range []string{
		`UPDATE inbox_items SET dup_of='i_blank' WHERE id='i_joined'`,
		`UPDATE inbox_items SET answer='yes', answered_at=7 WHERE id='i_old'`,
		`INSERT INTO delivery_step_inbox(change_id,revision,step,for_step,after_merge,commit_hash,item_id) VALUES('ch_1',1,'approve','merge',0,'abc','i_gate')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	var before int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	rerunFrontDoorFlush(t, s, true)
	now := c.now().UnixNano()
	for _, id := range []string{"i_blank", "i_owner", "i_sign", "i_joined"} {
		if status, answer, at := flushRow(t, s, id); status != "closed" || answer != protocol.AskReasonFlushedByFrontDoor || at != now {
			t.Errorf("%s: status=%s answer=%q at=%d, want closed as %s at %d", id, status, answer, at, protocol.AskReasonFlushedByFrontDoor, now)
		}
	}
	// Items that wait on someone else stay. A delivery approval gates a merge
	// and a permission ask its proposal: closing them would strand the change
	// or the proposal with nothing to answer.
	for _, id := range []string{"i_chief", "i_lead", "i_gate", "i_perm"} {
		if status, answer, _ := flushRow(t, s, id); status != "open" || answer != "" {
			t.Errorf("%s: status=%s answer=%q, want it left open", id, status, answer)
		}
	}
	if status, answer, at := flushRow(t, s, "i_old"); status != "answered" || answer != "yes" || at != 7 {
		t.Errorf("answered item changed: %s %q %d", status, answer, at)
	}
	var after int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items`).Scan(&after); err != nil || after != before {
		t.Fatalf("items before %d, after %d (%v): the flush keeps every row", before, after, err)
	}
	var count string
	if err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, frontDoorFlushKey).Scan(&count); err != nil || count != "3" {
		t.Fatalf("flush marker = %q, %v; want the 3 closed items on the record", count, err)
	}
	log, err := s.DeskLog(5)
	if err != nil || len(log) == 0 || log[0].Text != "Front door: closed 3 open Desk items that waited on you (flushed_by_front_door)" {
		t.Fatalf("desk log: %+v, %v", log, err)
	}
}

// The flush runs once. An item opened after it stays open when the migrations
// run again.
func TestFrontDoorFlushRunsOnce(t *testing.T) {
	s, _ := newTestStore(t)
	rerunFrontDoorFlush(t, s, true)
	seedLegacyAsks(t, s, legacyAsk{id: "i_new", kind: protocol.AskKindQuestion, next: protocol.AskNextOwner})
	rerunFrontDoorFlush(t, s, false)
	if status, answer, _ := flushRow(t, s, "i_new"); status != "open" || answer != "" {
		t.Fatalf("item opened after the flush: status=%s answer=%q, want open", status, answer)
	}
}
