package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// holderRig is a chief rig with one more session, carol on the chief's machine,
// and one ask from bob that the chief sent to carol.
func holderRig(t *testing.T) (s *Store, c *clock, chief, carol, ask, node string) {
	t.Helper()
	s, c, chief, asker, node := chiefRig(t)
	carol, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	ask = chiefAsk(t, s, asker, "Is release 42 out?")
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: ask, Result: protocol.AskSortSend, To: carol, Reason: "carol runs releases"}}); err != nil {
		t.Fatal(err)
	}
	return s, c, chief, carol, ask, node
}

// setOffline marks a session offline from now on, as the gateway does when its
// machine reports it gone.
func setOffline(t *testing.T, s *Store, id string, at time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE sessions SET online=0, last_seen=? WHERE id=?`, at.UnixNano(), id); err != nil {
		t.Fatal(err)
	}
}

func backNote(t *testing.T, s *Store, id string) string {
	t.Helper()
	var note string
	if err := s.db.QueryRow(`SELECT back_note FROM inbox_items WHERE id=?`, id).Scan(&note); err != nil {
		t.Fatal(err)
	}
	return note
}

// A routed ask whose holder stayed offline for the stuck-work limit goes back
// to the chief, with a note the chief reads in its batch and one Desk log line.
func TestRoutedAskWithOfflineHolderGoesBackToChief(t *testing.T) {
	s, c, chief, carol, ask, _ := holderRig(t)
	setOffline(t, s, carol, c.t)
	c.t = c.t.Add(testStuckLimit - time.Minute)
	if _, err := s.chiefStuckWork(c.t, testStuckLimit); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, ask); next != carol {
		t.Fatalf("next = %q before the limit, want carol", next)
	}
	c.t = c.t.Add(2 * time.Minute)
	if _, err := s.chiefStuckWork(c.t, testStuckLimit); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, ask); next != protocol.AskNextChief {
		t.Fatalf("next = %q past the limit, want chief", next)
	}
	const want = "back from carol: offline for 12 h"
	if note := backNote(t, s, ask); note != want {
		t.Fatalf("back note = %q, want %q", note, want)
	}
	entries, _ := s.DeskLog(10)
	if len(entries) == 0 || !strings.Contains(entries[0].Text, want) || !strings.Contains(entries[0].Text, ask) {
		t.Fatalf("desk log = %+v, want one line with %q", entries, want)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET harness_activity=? WHERE id=?`, c.t.UnixNano(), chief); err != nil {
		t.Fatal(err)
	}
	body, ok := sendDueChief(t, s, c.t)
	if !ok || !strings.Contains(body, "Ask "+ask) || !strings.Contains(body, "Why it is back: "+want) {
		t.Fatalf("chief batch = %q, want the ask and its note", body)
	}
}

// A holder that was replaced hands its routed ask to the session that replaced
// it, with the same note, once it has been offline for the limit.
func TestRoutedAskWithReplacedHolderGoesToReplacement(t *testing.T) {
	s, c, _, carol, ask, node := holderRig(t)
	dave, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nd", Name: "dave"})
	if err != nil {
		t.Fatal(err)
	}
	setOffline(t, s, carol, c.t)
	if _, err := s.db.Exec(`UPDATE sessions SET replaced_by=? WHERE id=?`, dave, carol); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(testStuckLimit + time.Minute)
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, last_seen=? WHERE id=?`, c.t.UnixNano(), dave); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.chiefStuckWork(c.t, testStuckLimit)
	if err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, ask); next != dave {
		t.Fatalf("next = %q, want the replacement dave", next)
	}
	const want = "back from carol: offline for 12 h"
	if note := backNote(t, s, ask); note != want {
		t.Fatalf("back note = %q, want %q", note, want)
	}
	var toDave []protocol.Message
	for _, m := range msgs {
		if m.To == dave {
			toDave = append(toDave, m)
		}
	}
	if len(toDave) != 1 || !strings.Contains(toDave[0].Body, ask) || !strings.Contains(toDave[0].Body, want) {
		t.Fatalf("messages to dave = %+v, want one with the ask and %q", toDave, want)
	}
	entries, _ := s.DeskLog(10)
	if len(entries) == 0 || !strings.Contains(entries[0].Text, "dave") || !strings.Contains(entries[0].Text, want) {
		t.Fatalf("desk log = %+v, want the move to dave", entries)
	}
}

// The chief may sort again an ask it routed once the holder is offline; while
// the holder is online the ask stays with it.
func TestChiefReSortsAskWhenHolderIsOffline(t *testing.T) {
	s, c, chief, carol, ask, node := holderRig(t)
	dave, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nd", Name: "dave"})
	if err != nil {
		t.Fatal(err)
	}
	resend := protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: ask, Result: protocol.AskSortSend, To: dave, Reason: "carol is gone, dave took releases"}}
	if _, _, err := s.SortAsk(node, resend); err == nil {
		t.Fatal("the chief moved an ask its online holder still has")
	}
	setOffline(t, s, carol, c.t)
	res, m, err := s.SortAsk(node, resend)
	if err != nil {
		t.Fatalf("re-sort with the holder offline: %v", err)
	}
	if res.Next != dave || askNext(t, s, ask) != dave || m.To != dave {
		t.Fatalf("re-sort: result next %q, ask next %q, message to %q; want dave", res.Next, askNext(t, s, ask), m.To)
	}
}

// Either side of a routed ask may close it (decision 0150): the chief that
// routed it, and the session that holds it. Each close keeps who and why.
func TestRoutedAskClosedByChiefOrHolder(t *testing.T) {
	t.Run("chief", func(t *testing.T) {
		s, _, chief, _, ask, node := holderRig(t)
		if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
			Ask: ask, Result: protocol.AskSortClose, Reason: "carol shipped it"}}); err != nil {
			t.Fatalf("the chief closing an ask it routed: %v", err)
		}
		if status, answer, _ := askRow(t, s, `id=?`, ask); status != "closed" || answer != "chief: carol shipped it" {
			t.Fatalf("status %q answer %q", status, answer)
		}
	})
	t.Run("holder", func(t *testing.T) {
		s, _, chief, carol, ask, node := holderRig(t)
		var asker string
		if err := s.db.QueryRow(`SELECT session_id FROM inbox_items WHERE id=?`, ask).Scan(&asker); err != nil {
			t.Fatal(err)
		}
		var askerNode string
		if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, asker).Scan(&askerNode); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.SortAsk(askerNode, protocol.AskSortRequest{SessionID: asker, AskSortArgs: protocol.AskSortArgs{
			Ask: ask, Result: protocol.AskSortClose, Reason: "mine"}}); err == nil {
			t.Fatal("a session that does not hold the ask closed it")
		}
		if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: carol, AskSortArgs: protocol.AskSortArgs{
			Ask: ask, Result: protocol.AskSortSend, To: chief, Reason: "not mine"}}); err == nil {
			t.Fatal("the holder sent the ask on; only close is the holder's")
		}
		res, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: carol, AskSortArgs: protocol.AskSortArgs{
			Ask: ask, Result: protocol.AskSortClose, Reason: "released in 42f8a7a"}})
		if err != nil {
			t.Fatalf("the holder closing its ask: %v", err)
		}
		if res.State != protocol.AskClosed {
			t.Fatalf("state = %q, want closed", res.State)
		}
		if status, answer, _ := askRow(t, s, `id=?`, ask); status != "closed" || answer != "carol: released in 42f8a7a" {
			t.Fatalf("status %q answer %q", status, answer)
		}
		hist, err := s.AskSortHistory(ask)
		if err != nil {
			t.Fatal(err)
		}
		last := hist[len(hist)-1]
		if last.Sorter != carol || last.Result != protocol.AskSortClose || last.PrevNext != carol || last.Reason != "released in 42f8a7a" {
			t.Fatalf("last sort = %+v, want carol's close", last)
		}
		entries, _ := s.DeskLog(10)
		if len(entries) == 0 || !strings.Contains(entries[0].Text, "carol closed ask") {
			t.Fatalf("desk log = %+v, want carol's close", entries)
		}
	})
}
