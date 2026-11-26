package gateway

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

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
	return chiefAskFrom(t, s, from, kind, text, "")
}

// chiefAskFrom stores an ask the chief holds, as a gateway did before decision
// 0196, with the source of its sender: empty for the kinds agent-net raises
// itself, session for an agent ask.
func chiefAskFrom(t *testing.T, s *Store, from, kind, text, fromKind string) string {
	t.Helper()
	item, err := storeOldItem(t, s, protocol.InboxItem{Kind: kind, SessionID: from, Node: "demo", Text: text, NextWho: protocol.AskNextChief,
		FromKind: fromKind, Recommendation: "yes", Options: []string{"yes", "no"}, AboutKind: protocol.AskAboutProject, AboutID: "demo"})
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
	if err := replayMigrations(t, s); err != nil {
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

// A clear is recorded with its time and stands until a session is named chief
// or chief.model changes (decision 0171), so nothing that starts a chief from
// the list undoes the owner's clear.
func TestChiefClearStandsUntilANewChiefOrAModelChange(t *testing.T) {
	s, _, chief, _, _ := chiefRig(t)
	status := func() protocol.ChiefStatus {
		t.Helper()
		st, err := s.ChiefStatus()
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := status(); st.ClearedAt != 0 {
		t.Fatalf("a set chief reports a clear: %+v", st)
	}
	clear := func() {
		t.Helper()
		if err := s.SetChief(protocol.ChiefSetRequest{Clear: true}, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	clear()
	if st := status(); st.SessionID != "" || st.ClearedAt == 0 {
		t.Fatalf("after a clear: %+v, want no chief and a clear time", st)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if st := status(); st.ClearedAt != 0 {
		t.Fatalf("naming a chief did not end the clear: %+v", st)
	}
	clear()
	line := "codex · openai · gpt-6.1-sol · high"
	if _, err := s.WriteSettings([]SettingsChange{{Key: protocol.ChiefModelKey, Level: "network", Scope: "network", Value: json.RawMessage(strconv.Quote(line))}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if st := status(); st.ClearedAt != 0 {
		t.Fatalf("a chief.model change did not end the clear: %+v", st)
	}
}
