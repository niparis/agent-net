package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Desk item shape of decision 0150, plan step (a).

// deskRig is a chief rig with a CTO session on the chief's machine.
func deskRig(t *testing.T) (s *Store, c *clock, chief, asker, cto, node string) {
	t.Helper()
	s, c, chief, asker, node = chiefRig(t)
	var err error
	cto, _, err = s.upsertSession(s.db, node, protocol.SessionInfo{Harness: "cto", NativeID: "cto", Name: "CTO"}, true)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, chief, asker, cto, node
}

func TestJudgmentRecordByChiefAndCTOOnly(t *testing.T) {
	s, _, chief, asker, cto, node := deskRig(t)
	args := protocol.JudgmentRecordArgs{Text: "Always merge docs-only changes in demo.", Area: "repo:demo", Action: "merge docs"}
	o, err := s.RecordJudgment(node, protocol.JudgmentRecordRequest{SessionID: chief, JudgmentRecordArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != protocol.StandingKindRule || o.Area != "repo:demo" || o.Action != "merge docs" {
		t.Fatalf("recorded judgment = %+v", o)
	}
	once, err := s.RecordJudgment(node, protocol.JudgmentRecordRequest{SessionID: cto, JudgmentRecordArgs: protocol.JudgmentRecordArgs{Text: "Skip the Mac build today."}})
	if err != nil {
		t.Fatal(err)
	}
	if once.Kind != protocol.StandingKindAnswer {
		t.Fatalf("unscoped record kind %q, want answer", once.Kind)
	}
	var askerNode string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, asker).Scan(&askerNode); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordJudgment(askerNode, protocol.JudgmentRecordRequest{SessionID: asker, JudgmentRecordArgs: args}); err == nil {
		t.Fatal("a session that is neither the chief nor the CTO recorded an owner judgment")
	}
}

// Only ids are folded out of the duplicate key: numbers stay.
func TestSameTextKeepsNumbersApart(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	a, err := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: asker, Node: "demo", Text: "Merge 100 files?", Options: []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: asker, Node: "demo", Text: "Merge 900 files?"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("two asks that differ in a number were joined as duplicates")
	}
}
