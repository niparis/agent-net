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
