package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// chiefRig is a store with a live chief (a), an asker (b) and a node. The chief
// is registered by the owner.
func chiefRig(t *testing.T) (s *Store, c *clock, chief, asker, node string) {
	t.Helper()
	s, c = newTestStore(t)
	chief, asker = twoSessions(t, s)
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, chief).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id IN (?,?)`, chief, asker); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	return s, c, chief, asker, node
}

// chiefAsk opens a question. A session holds one open question at a time, so
// tests that open several asks from one session give them other kinds.
func chiefAsk(t *testing.T, s *Store, from, text string) string {
	t.Helper()
	return chiefAskKind(t, s, from, "question", text)
}

func chiefAskKind(t *testing.T, s *Store, from, kind, text string) string {
	t.Helper()
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: kind, SessionID: from, Node: "demo", Text: text,
		Recommendation: "yes", Options: []string{"yes", "no"}, AboutKind: protocol.AskAboutProject, AboutID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	return item.ID
}

func askNext(t *testing.T, s *Store, id string) string {
	t.Helper()
	var next string
	if err := s.db.QueryRow(`SELECT next_who FROM inbox_items WHERE id=?`, id).Scan(&next); err != nil {
		t.Fatal(err)
	}
	return next
}

// chiefMessages returns the bodies of the agent-net messages sent to a session.
func chiefMessages(t *testing.T, s *Store, to string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT body FROM messages WHERE recipient=? AND sender='agent-net' ORDER BY created_at`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		out = append(out, body)
	}
	return out
}

// sendDueChief does what the gateway's batch tick does: it stores the due
// batch, if there is one, and returns its message body.
func sendDueChief(t *testing.T, s *Store, now time.Time) (string, bool) {
	t.Helper()
	b, err := s.chiefBatchDue(now)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		return "", false
	}
	if _, err := s.storeChiefBatch(*b, now); err != nil {
		t.Fatal(err)
	}
	return b.body, true
}

// A gateway that starts with a chief already set adopts the open asks that
// were never offered to it, so a restart does not strand them.
func TestGatewayStartAdoptsAsksForExistingChief(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return base }
	s.tempFolders = []string{}
	chief, asker := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id IN (?,?)`, chief, asker); err != nil {
		t.Fatal(err)
	}
	id := chiefAsk(t, s, asker, "Opened with no chief?")
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("before the chief existed, next = %q, want owner", next)
	}
	// The setting was already stored when the gateway stopped, so SetChief
	// does not run this time. Only the start reconcile can adopt the ask.
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)`, chiefSessionKey, chief, base.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("after a start with a chief set, next = %q, want chief", next)
	}
	var chiefAt int64
	if err := s.db.QueryRow(`SELECT chief_at FROM inbox_items WHERE id=?`, id).Scan(&chiefAt); err != nil {
		t.Fatal(err)
	}
	if chiefAt == 0 {
		t.Fatal("the adopted ask has no chief_at, so its fallback clock is wrong")
	}
}

// An ask whose creator routed it to the owner on purpose stays with the owner.
// The Tailscale pairing notice is one: only the owner may revoke a device, so
// the chief must not adopt it or close it.
func TestChiefDoesNotAdoptOwnerOnlyAsk(t *testing.T) {
	s, _ := newTestStore(t)
	chief, asker := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id IN (?,?)`, chief, asker); err != nil {
		t.Fatal(err)
	}
	notice, err := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: asker, Node: "apps",
		FromKind: protocol.AskFromApps, NextWho: protocol.AskNextOwner, AskerName: "Tailscale",
		SourceRef: "tailscale-pair:test", Text: "Paired X by Tailscale sign-in",
		Recommendation: "Information only. Revoke this device in Connected apps if this was not you."})
	if err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, notice.ID); next != protocol.AskNextOwner {
		t.Fatalf("before a chief, next = %q, want owner", next)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, notice.ID); next != protocol.AskNextOwner {
		t.Fatalf("after chief_set, next = %q, want owner", next)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, notice.ID); next != protocol.AskNextOwner {
		t.Fatalf("after reconcile, next = %q, want owner", next)
	}
}

// An ask stored before route_fixed existed, such as an open Tailscale
// pairing notice, must have route_fixed backfilled so chief adoption does
// not adopt it when the gateway upgrades.
func TestChiefMigrationPreservesOwnerRoutedAsk(t *testing.T) {
	s, _ := newTestStore(t)
	chief, asker := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id IN (?,?)`, chief, asker); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE inbox_items DROP COLUMN route_fixed`); err != nil {
		t.Fatal(err)
	}
	version := migrationVersion(t, "chief ask adoption time")
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
	noticeID := "i_tailscale_pair"
	if _, err := s.db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,from_kind,source_ref,about_kind,about_id,next_who,why_you,detail)
		VALUES (?,'check',?,'apps','Paired phone by Tailscale sign-in','Information only. Revoke this device in Connected apps if this was not you.','','',?,'open','[]','Tailscale','',?,'tailscale-pair:node-123','','','owner','','')`,
		noticeID, asker, s.now().UnixNano(), protocol.AskFromApps); err != nil {
		t.Fatal(err)
	}
	leadAskID := "i_lead_ask"
	if _, err := s.db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,from_kind,source_ref,about_kind,about_id,next_who,why_you,detail)
		VALUES (?,'question',?,'demo','Question for lead?','','m1','c1',?,'open','[]','lead asker','','','lead_ask:m1','project','demo','s_lead','','')`,
		leadAskID, asker, s.now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	regularID := "i_regular"
	if _, err := s.db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,from_kind,source_ref,about_kind,about_id,next_who,why_you,detail)
		VALUES (?,'question',?,'demo','Regular question?','','m2','c2',?,'open','[]','alice','','','','','','owner','','')`,
		regularID, asker, s.now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	for id, wantRouteFixed := range map[string]int{noticeID: 1, leadAskID: 1, regularID: 0} {
		var routeFixed int
		if err := s.db.QueryRow(`SELECT route_fixed FROM inbox_items WHERE id=?`, id).Scan(&routeFixed); err != nil {
			t.Fatal(err)
		}
		if routeFixed != wantRouteFixed {
			t.Fatalf("%s route_fixed = %d, want %d", id, routeFixed, wantRouteFixed)
		}
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, noticeID); next != protocol.AskNextOwner {
		t.Fatalf("after chief_set, notice next = %q, want %q", next, protocol.AskNextOwner)
	}
	if next := askNext(t, s, regularID); next != protocol.AskNextChief {
		t.Fatalf("after chief_set, regular next = %q, want %q", next, protocol.AskNextChief)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, noticeID); next != protocol.AskNextOwner {
		t.Fatalf("after reconcile, notice next = %q, want %q", next, protocol.AskNextOwner)
	}
}

// A chief set after asks are already open takes them over: the chief can sort
// them, and the ones it has not sorted leave in a batch. An answered ask stays
// with the owner.
func TestChiefSetAdoptsAsksOpenBeforeIt(t *testing.T) {
	s, c := newTestStore(t)
	chief, asker := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id IN (?,?)`, chief, asker); err != nil {
		t.Fatal(err)
	}
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, chief).Scan(&node); err != nil {
		t.Fatal(err)
	}
	first := chiefAsk(t, s, asker, "Was this open before the chief?")
	second := chiefAskKind(t, s, asker, "feature_idea", "And this one?")
	answered := chiefAskKind(t, s, asker, "approval", "The owner already answered this")
	if _, err := s.AnswerInbox(answered, "yes"); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, first); next != protocol.AskNextOwner {
		t.Fatalf("before a chief, next = %q, want owner", next)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	// The live failure: an ask opened before chief_set was not waiting for the
	// chief, so this sort was refused.
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: first, Result: protocol.AskSortClose, Reason: "answered in the thread"}}); err != nil {
		t.Fatalf("sort an ask opened before chief_set: %v", err)
	}
	sorts, err := s.ChiefSorts(c.now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(sorts) != 1 || sorts[0].Ask != first {
		t.Fatalf("chief sorts = %+v, want the sort of %s", sorts, first)
	}
	if next := askNext(t, s, second); next != protocol.AskNextChief {
		t.Fatalf("an ask opened before chief_set is next %q, want chief", next)
	}
	if next := askNext(t, s, answered); next != protocol.AskNextOwner {
		t.Fatalf("the answered ask is next %q, want owner", next)
	}
	c.t = c.t.Add(31 * time.Second)
	body, sent := sendDueChief(t, s, c.now())
	if !sent {
		t.Fatal("no batch went to the chief for asks opened before it")
	}
	if !strings.Contains(body, second) || strings.Contains(body, first) {
		t.Fatalf("batch body = %q, want %s and not the sorted %s", body, second, first)
	}
}

// agent_ask_sort with result list returns the asks that wait for the chief,
// with their fields and age. A sort still needs an ask and a reason.
func TestAskSortListsWaitingAsks(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	closed := chiefAsk(t, s, asker, "Already sorted?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: closed, Result: protocol.AskSortClose, Reason: "done"})
	waiting := chiefAskKind(t, s, asker, "feature_idea", "Still waiting?")
	onOwner := chiefAskKind(t, s, asker, "approval", "With the owner?")
	if _, err := s.db.Exec(`UPDATE inbox_items SET next_who=? WHERE id=?`, protocol.AskNextOwner, onOwner); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(90 * time.Minute)
	res, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Result: protocol.AskSortList}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != protocol.AskSortList || len(res.Asks) != 1 {
		t.Fatalf("list = %+v, want one waiting ask", res)
	}
	got := res.Asks[0]
	if got.Ask != waiting || got.Kind != "feature_idea" || got.From != "bob" || got.Text != "Still waiting?" || got.Recommendation != "yes" || got.Age != "1h30m" {
		t.Fatalf("listed ask = %+v", got)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: asker, AskSortArgs: protocol.AskSortArgs{Result: protocol.AskSortList}}); err == nil {
		t.Fatal("a non-chief listed the asks")
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Result: protocol.AskSortClose}}); err == nil {
		t.Fatal("a sort without an ask and a reason was accepted")
	}
}

// A new ask with a live chief goes to the chief, and the chief gets one task
// message for it after the quiet window. The chief's own asks go to the owner.
func TestChiefRoutesAskAndSendsOneTask(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Merge PR 3?")
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q, want chief", next)
	}
	if _, sent := sendDueChief(t, s, c.now()); sent {
		t.Fatal("a batch went out before the quiet window")
	}
	c.t = c.t.Add(6 * time.Second)
	body, sent := sendDueChief(t, s, c.now())
	if !sent {
		t.Fatal("no batch after the quiet window")
	}
	if !strings.Contains(body, id) || !strings.Contains(body, "Merge PR 3?") {
		t.Fatalf("batch body misses the ask: %q", body)
	}
	if msgs := chiefMessages(t, s, chief); len(msgs) != 1 {
		t.Fatalf("messages to the chief = %d, want 1", len(msgs))
	}
	if _, sent := sendDueChief(t, s, c.now().Add(time.Minute)); sent {
		t.Fatal("the same ask was sent twice")
	}

	own := chiefAsk(t, s, chief, "Should I restart the gateway?")
	if next := askNext(t, s, own); next != protocol.AskNextOwner {
		t.Fatalf("the chief's own ask went to %q, want owner", next)
	}
}

// Asks that arrive in a burst leave as one message.
func TestChiefBurstIsOneMessage(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	ids := []string{chiefAskKind(t, s, asker, "question", "one")}
	c.t = c.t.Add(time.Second)
	ids = append(ids, chiefAskKind(t, s, asker, "approval", "two"))
	c.t = c.t.Add(time.Second)
	ids = append(ids, chiefAskKind(t, s, asker, "feature_idea", "three"))
	c.t = c.t.Add(3 * time.Second)
	if _, sent := sendDueChief(t, s, c.now()); sent {
		t.Fatal("the burst went out before its quiet window ended")
	}
	c.t = c.t.Add(3 * time.Second)
	body, sent := sendDueChief(t, s, c.now())
	if !sent {
		t.Fatal("the burst did not go out")
	}
	for _, id := range ids {
		if !strings.Contains(body, id) {
			t.Fatalf("batch misses %s: %q", id, body)
		}
	}
	if msgs := chiefMessages(t, s, chief); len(msgs) != 1 {
		t.Fatalf("messages to the chief = %d, want 1", len(msgs))
	}
}

// With no live chief that can be woken, an ask stays with the owner. An
// offline chief whose harness can be woken gets the ask instead
// (TestAskMadeWhileWakeableChiefIsDownWaitsForMissedWakes), so this chief
// runs Pi, which cannot be woken.
func TestNoLiveChiefKeepsAskWithOwner(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET online=0, harness='pi' WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	id := chiefAsk(t, s, asker, "Ship it?")
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q with the chief offline, want owner", next)
	}
	c.t = c.t.Add(time.Minute)
	if _, sent := sendDueChief(t, s, c.now()); sent {
		t.Fatal("a batch went to an offline chief")
	}
}

// When the chief goes offline, its waiting asks go to the owner after the missed
// wakes of the safety net (decision 0150), not at once.
func TestChiefOfflineReturnsWaitingAsksToOwner(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q right after the chief went offline, want chief", next)
	}
	c.t = c.t.Add(2 * protocol.ChiefWakeInterval)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q with the chief offline for two wakes, want owner", next)
	}
	entries, _ := s.DeskLog(10)
	if len(entries) == 0 || !strings.Contains(entries[0].Text, "chief of staff is offline") {
		t.Fatalf("desk log = %+v, want the offline line", entries)
	}
}

// Only the owner or the CTO may name the chief, and only a live agent session.
func TestSetChiefNeedsOwnerOrCTO(t *testing.T) {
	s, _, chief, asker, _ := chiefRig(t)
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: asker}, "agent"); err == nil {
		t.Fatal("an agent named the chief")
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: asker}, "cto"); err != nil {
		t.Fatalf("the CTO could not name the chief: %v", err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: "s_missing"}, "owner"); err == nil {
		t.Fatal("an unknown session was named the chief")
	}
}

// The model is a setting: the default is the chief's model, and a change shows
// in the chief's status.
func TestChiefModelSettingIsRead(t *testing.T) {
	s, _, chief, _, _ := chiefRig(t)
	st, err := s.ChiefStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Model != protocol.DefaultChiefModel || st.SessionID != chief || !st.Online {
		t.Fatalf("status = %+v, want the default model and the live chief", st)
	}
	if _, err := s.WriteSettings([]SettingsChange{{Key: protocol.ChiefModelKey, Level: "network", Scope: "network",
		Value: json.RawMessage(`"claude · anthropic · claude-sonnet-5-5 · high"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	st, err = s.ChiefStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Model != "claude · anthropic · claude-sonnet-5-5 · high" {
		t.Fatalf("model = %q after the change", st.Model)
	}
}

// The four sorts: close, merge, send and owner. Each one writes a history row
// that keeps the ask as it was, and one log line.
func TestChiefSortResults(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	closeID := chiefAsk(t, s, asker, "Is the thread answered?")
	mergeID := chiefAsk(t, s, other, "Merge PR 4?")
	mergeInto := chiefAskKind(t, s, asker, "approval", "Merge PR 4 today?")
	sendID := chiefAskKind(t, s, asker, "feature_idea", "Who owns the cache?")
	ownerID := chiefAskKind(t, s, other, "approval", "Pay for the new model?")

	sort := func(args protocol.AskSortArgs) protocol.AskSortResult {
		t.Helper()
		res, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: args})
		if err != nil {
			t.Fatalf("sort %+v: %v", args, err)
		}
		return res
	}

	res := sort(protocol.AskSortArgs{Ask: closeID, Result: protocol.AskSortClose, Reason: "answered in the thread"})
	if res.State != protocol.AskClosed {
		t.Fatalf("close: state = %q", res.State)
	}
	if status, answer, _ := askRow(t, s, `id=?`, closeID); status != "closed" || answer != "chief: answered in the thread" {
		t.Fatalf("close: status %q answer %q", status, answer)
	}

	sort(protocol.AskSortArgs{Ask: mergeID, Result: protocol.AskSortMerge, Into: mergeInto, Reason: "same question"})
	if status, answer, _ := askRow(t, s, `id=?`, mergeID); status != "closed" || answer != "chief: same question" {
		t.Fatalf("merge: source status %q answer %q", status, answer)
	}
	var detail string
	if err := s.db.QueryRow(`SELECT detail FROM inbox_items WHERE id=?`, mergeInto).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "merged: "+mergeID) {
		t.Fatalf("merge: target detail %q has no merged note", detail)
	}

	sort(protocol.AskSortArgs{Ask: sendID, Result: protocol.AskSortSend, To: other, Reason: "carol runs the cache"})
	if next := askNext(t, s, sendID); next != other {
		t.Fatalf("send: next = %q, want %q", next, other)
	}
	msgs := chiefMessages(t, s, other)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "chief of staff sent you ask "+sendID) {
		t.Fatalf("send: messages to the session = %q", msgs)
	}

	res = sort(protocol.AskSortArgs{Ask: ownerID, Result: protocol.AskSortOwner, Priority: 1, Reason: "needs a decision", WhyYou: "only you can approve the spend"})
	if res.Next != protocol.AskNextOwner || askNext(t, s, ownerID) != protocol.AskNextOwner {
		t.Fatalf("owner: next = %q", res.Next)
	}
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	var got protocol.Ask
	for _, a := range asks {
		if a.ID == ownerID {
			got = a
		}
	}
	if got.Priority != 1 || got.WhyYou != "only you can approve the spend" {
		t.Fatalf("owner: ask priority %d why_you %q", got.Priority, got.WhyYou)
	}

	hist, err := s.AskSortHistory(closeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Result != protocol.AskSortClose || hist[0].PrevNext != protocol.AskNextChief || hist[0].PrevStatus != "open" || hist[0].Sorter != chief {
		t.Fatalf("history of the closed ask = %+v", hist)
	}
	entries, _ := s.DeskLog(20)
	if len(entries) < 4 {
		t.Fatalf("desk log has %d lines, want one per sort", len(entries))
	}
	for _, e := range entries {
		if e.Source != "chief" {
			t.Fatalf("log line %+v is not from the chief", e)
		}
	}
}

// An ask about a session never goes back to that session: the send would
// return the ask to the session it is about, a self-loop. Every other send
// keeps the path it had.
func TestChiefSendToTheAskAboutSessionIsRefused(t *testing.T) {
	// With the read pool off every read runs on the one writer connection, the
	// one the sort's transaction holds. The name lookup inside the sort must
	// still finish, or the gateway hangs.
	for _, pool := range []string{"read pool on", "read pool off"} {
		t.Run(pool, func(t *testing.T) {
			if pool == "read pool off" {
				old := readPoolMax
				readPoolMax = 0
				defer func() { readPoolMax = old }()
			}
			testChiefSendToTheAskAboutSessionIsRefused(t)
		})
	}
}

func testChiefSendToTheAskAboutSessionIsRefused(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	id := chiefAsk(t, s, asker, "Is the scheduled run done?")
	_, _, err = s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortSend, To: asker, Reason: "bob is blocked on it"}})
	want := "this ask is about bob; sending it back to them makes a loop. Close it, send it to someone else, or send it to the owner."
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("send to the ask's own session = %v, want %q", err, want)
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("the refused send moved the ask to %q", next)
	}
	if msgs := chiefMessages(t, s, asker); len(msgs) != 0 {
		t.Fatalf("the refused send sent %d messages", len(msgs))
	}

	res, msg, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortSend, To: other, Reason: "carol runs the schedule"}})
	if err != nil {
		t.Fatalf("send to another session: %v", err)
	}
	if msg.To != other || res.Next != other || askNext(t, s, id) != other {
		t.Fatalf("send to another session: message to %q, next %q, ask next %q", msg.To, res.Next, askNext(t, s, id))
	}
}

// Only the live chief may sort, and only an ask that waits for the chief.
func TestChiefSortRefusesNonChief(t *testing.T) {
	s, _, chief, asker, node := chiefRig(t)
	id := chiefAsk(t, s, asker, "Is it done?")
	args := protocol.AskSortArgs{Ask: id, Result: protocol.AskSortClose, Reason: "done"}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: asker, AskSortArgs: args}); err == nil || !strings.Contains(err.Error(), "only the chief of staff") {
		t.Fatalf("a non-chief sorted: %v", err)
	}
	if status, _, _ := askRow(t, s, `id=?`, id); status != "open" {
		t.Fatalf("refused sort closed the ask: %s", status)
	}
	owner := chiefAskKind(t, s, asker, "approval", "Owner only?")
	if _, err := s.db.Exec(`UPDATE inbox_items SET next_who='owner' WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{Ask: owner, Result: protocol.AskSortClose, Reason: "x"}}); err == nil {
		t.Fatal("the chief sorted an ask that was not waiting for it")
	}
}

// chiefModelRig gives the chief's node a claude model list, so a model change
// has an inventory to resolve against.
func chiefModelRig(t *testing.T) (s *Store, chief string) {
	t.Helper()
	s, _, chief, _, node := chiefRig(t)
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{
		{Name: "claude", Models: []string{"claude-opus-5-5", "claude-sonnet-5-5"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE nodes SET seen_at = 1 WHERE id = ?`, node); err != nil {
		t.Fatal(err)
	}
	return s, chief
}

// A model-only chief_set writes chief.model and leaves the chief session where
// it is. This is the call that used to clear the chief.
func TestChiefSetModelKeepsSession(t *testing.T) {
	s, chief := chiefModelRig(t)
	if err := s.SetChief(protocol.ChiefSetRequest{Harness: "claude", Model: "claude-opus-5-5", Effort: "medium"}, "cto"); err != nil {
		t.Fatal(err)
	}
	st, err := s.ChiefStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.SessionID != chief {
		t.Fatalf("chief session = %q after a model change, want %q", st.SessionID, chief)
	}
	if want := "claude · anthropic · claude-opus-5-5 · medium"; st.Model != want {
		t.Fatalf("model = %q, want %q", st.Model, want)
	}
}

// A loose model name resolves like a worker start, and one field alone keeps
// the rest of the line.
func TestChiefSetModelResolvesAndMerges(t *testing.T) {
	s, _ := chiefModelRig(t)
	if err := s.SetChief(protocol.ChiefSetRequest{Harness: "claude", Model: "opus", Effort: "high"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{Effort: "low"}, "owner"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.ChiefStatus()
	if want := "claude · anthropic · claude-opus-5-5 · low"; st.Model != want {
		t.Fatalf("model = %q, want %q", st.Model, want)
	}
}

// A model no machine lists is refused, and nothing changes.
func TestChiefSetUnknownModelRefused(t *testing.T) {
	s, chief := chiefModelRig(t)
	err := s.SetChief(protocol.ChiefSetRequest{Harness: "claude", Model: "claude-nonesuch-9", Effort: "medium"}, "owner")
	if err == nil || !strings.Contains(err.Error(), "claude-nonesuch-9") {
		t.Fatalf("err = %v, want a refusal naming the model", err)
	}
	st, _ := s.ChiefStatus()
	if st.Model != protocol.DefaultChiefModel || st.SessionID != chief {
		t.Fatalf("status = %+v after a refused model, want no change", st)
	}
}

// A harness no machine reports a model list for cannot be checked, so it is
// refused too.
func TestChiefSetModelWithoutInventoryRefused(t *testing.T) {
	s, _ := chiefModelRig(t)
	if err := s.SetChief(protocol.ChiefSetRequest{Harness: "codex", Model: "gpt-6.1-sol"}, "owner"); err == nil {
		t.Fatal("a model of a harness with no model list was accepted")
	}
}

// The model change needs the owner or the CTO, like naming the chief.
func TestChiefSetModelNeedsOwnerOrCTO(t *testing.T) {
	s, _ := chiefModelRig(t)
	if err := s.SetChief(protocol.ChiefSetRequest{Harness: "claude", Model: "claude-opus-5-5"}, "agent"); err == nil {
		t.Fatal("an agent changed the chief model")
	}
}

// An empty chief_set is refused and leaves the chief in place; only an explicit
// clear unassigns it.
func TestChiefSetEmptyRefusedClearClears(t *testing.T) {
	s, chief := chiefModelRig(t)
	if err := s.SetChief(protocol.ChiefSetRequest{}, "owner"); err == nil {
		t.Fatal("an empty chief_set was accepted")
	}
	if id, _ := chiefSession(s.db); id != chief {
		t.Fatalf("chief = %q after an empty call, want %q", id, chief)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{Clear: true, SessionID: chief}, "owner"); err == nil {
		t.Fatal("clear with a session_id was accepted")
	}
	if err := s.SetChief(protocol.ChiefSetRequest{Clear: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	if id, _ := chiefSession(s.db); id != "" {
		t.Fatalf("chief = %q after clear, want none", id)
	}
}

// The operator frame refuses an argument chief_set does not have, by name.
func TestChiefSetFrameRefusesUnknownArgument(t *testing.T) {
	if _, err := decodeOperator[protocol.ChiefSetRequest]([]byte(`{"foo":1}`)); err == nil || !strings.Contains(err.Error(), "foo") {
		t.Fatalf("err = %v, want the unknown argument foo named", err)
	}
}

// The chief's model setting is an ordered list. Models replaces it whole, and
// the status names the first entry, which is the chief's current choice.
func TestChiefSetReplacesTheOrderedList(t *testing.T) {
	s, _ := chiefModelRig(t)
	models := []protocol.RoleModel{
		{Harness: "claude", Provider: "anthropic", Model: "claude-opus-5-5"},
		{Harness: "claude", Provider: "anthropic", Model: "claude-sonnet-5-5", Effort: "high"},
	}
	if err := s.SetChief(protocol.ChiefSetRequest{Models: models}, "owner"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snap.String(protocol.ChiefModelKey, settings.Where{}), protocol.FormatRoleModels(models); got != want {
		t.Fatalf("chief.model = %q, want %q", got, want)
	}
	st, err := s.ChiefStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Model != models[0].Display() {
		t.Fatalf("status model = %q, want the first entry %q", st.Model, models[0].Display())
	}
}
