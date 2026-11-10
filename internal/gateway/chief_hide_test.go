package gateway

import (
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestChiefHideHistory(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	id := chiefAskKind(t, s, asker, "session_stall", "stalled")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: id, Result: protocol.AskSortClose, Reason: "handled"})
	var sortID int64
	if err := s.db.QueryRow(`SELECT id FROM ask_sorts WHERE ask_id=?`, id).Scan(&sortID); err != nil {
		t.Fatal(err)
	}
	args := protocol.AskSortArgs{Result: "hide", Mode: "preview", Kind: "session_stall", HideResult: "close", Before: c.now().Add(time.Hour).UTC().Format(time.RFC3339)}
	call := func(session string, a protocol.AskSortArgs) (protocol.AskSortResult, error) {
		out, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: session, AskSortArgs: a})
		return out, err
	}
	preview, err := call(chief, args)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.HiddenIDs, []int64{sortID}) || preview.VisibleBefore != 1 || preview.VisibleAfter != 0 {
		t.Fatalf("preview: %+v", preview)
	}
	list, err := s.ChiefSorts(time.Time{})
	if err != nil || len(list) != 1 {
		t.Fatalf("preview changed list: %v %v", list, err)
	}
	if _, err := call(asker, args); err == nil {
		t.Fatal("non-chief accepted")
	}
	args.Mode = "apply"
	args.Token = preview.Token
	applied, err := call(chief, args)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied.HiddenIDs, []int64{sortID}) || applied.Receipt == "" {
		t.Fatalf("apply: %+v", applied)
	}
	list, err = s.ChiefSorts(time.Time{})
	if err != nil || len(list) != 0 {
		t.Fatalf("list: %v %v", list, err)
	}
	if _, err := call(chief, args); err == nil {
		t.Fatal("stale token accepted")
	}
	stats, err := askSortStats(s.db, chief, 0, 0, 0, 0)
	if err != nil || stats.Counts["close"] != 1 || stats.Hidden != 1 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
	_, err = call(chief, protocol.AskSortArgs{Result: "hide", Mode: "undo", Receipt: applied.Receipt})
	if err != nil {
		t.Fatal(err)
	}
	list, err = s.ChiefSorts(time.Time{})
	if err != nil || len(list) != 1 {
		t.Fatalf("undo: %v %v", list, err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM inbox_items WHERE id=?`, id).Scan(&status); err != nil || status != "closed" {
		t.Fatalf("status: %q %v", status, err)
	}
}

func TestChiefHideRepeatsOnly(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	var ids []int64
	for _, baseline := range []string{"100", "100", "200", "200", "100"} {
		ask := chiefAskKind(t, s, asker, "session_stall", "stall")
		if _, err := s.db.Exec(`UPDATE inbox_items SET conversation_id=? WHERE id=?`, baseline, ask); err != nil {
			t.Fatal(err)
		}
		sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: ask, Result: protocol.AskSortClose, Reason: "handled"})
		var id int64
		if err := s.db.QueryRow(`SELECT id FROM ask_sorts WHERE ask_id=?`, ask).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	args := protocol.AskSortArgs{Result: "hide", Mode: "preview", Kind: "session_stall", HideResult: "close", Before: c.now().Add(time.Hour).UTC().Format(time.RFC3339), RepeatsOnly: true}
	got, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.HiddenIDs, []int64{ids[1], ids[3]}) {
		t.Fatalf("ids %v", got.HiddenIDs)
	}
}
