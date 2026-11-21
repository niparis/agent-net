package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Decision 0175: the pinned lead session of an Agent may set the status of
// reports. Any other session is refused and writes nothing.
func TestFrictionMarkByLeadSessionOnly(t *testing.T) {
	s := teamStore(t)
	leads, _ := graphSessions(t, s, "demo", "other")
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id=?`, leads["demo"], "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=NULL WHERE id='other'`); err != nil {
		t.Fatal(err)
	}
	reportFriction(t, s, "demo", "s_1", protocol.FrictionCI, "the suite flaked")
	all, err := s.FrictionFind(protocol.FrictionQuery{})
	if err != nil || len(all.Entries) != 1 {
		t.Fatalf("stored report: %+v %v", all, err)
	}
	id := all.Entries[0].ID
	mark := protocol.FrictionMarkRequest{IDs: []int64{id}, Status: protocol.FrictionTriaged}
	if _, err := s.MarkFrictionAsLead(leads["other"], mark); err == nil || !strings.Contains(err.Error(), "only a lead session") {
		t.Fatalf("a session that is not a lead: %v", err)
	}
	if got, err := s.FrictionFind(protocol.FrictionQuery{ID: id}); err != nil || got.Entries[0].Status != protocol.FrictionNew {
		t.Fatalf("a refused mark wrote: %+v %v", got.Entries, err)
	}
	res, err := s.MarkFrictionAsLead(leads["demo"], mark)
	if err != nil || res.Marked != 1 {
		t.Fatalf("lead mark: %+v %v", res, err)
	}
	got, err := s.FrictionFind(protocol.FrictionQuery{ID: id})
	if err != nil || len(got.Entries) != 1 {
		t.Fatalf("report after the lead mark: %+v %v", got, err)
	}
	if got.Entries[0].Status != protocol.FrictionTriaged || got.Entries[0].ChangedBy != leads["demo"] {
		t.Fatalf("the lead's mark: %+v", got.Entries[0])
	}
}

// Decision 0175: a worker never sets the status of a report, even when a pin
// names its session as the lead of an Agent.
func TestFrictionMarkRefusesWorkerSession(t *testing.T) {
	s := teamStore(t)
	leads, _ := graphSessions(t, s, "demo")
	if _, err := s.db.Exec(`UPDATE sessions SET worker='a_worker' WHERE id=?`, leads["demo"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='demo'`, leads["demo"]); err != nil {
		t.Fatal(err)
	}
	reportFriction(t, s, "demo", "s_1", protocol.FrictionCI, "the suite flaked")
	all, err := s.FrictionFind(protocol.FrictionQuery{})
	if err != nil || len(all.Entries) != 1 {
		t.Fatalf("stored report: %+v %v", all, err)
	}
	mark := protocol.FrictionMarkRequest{IDs: []int64{all.Entries[0].ID}, Status: protocol.FrictionRead}
	if _, err := s.MarkFrictionAsLead(leads["demo"], mark); err == nil || !strings.Contains(err.Error(), "only a lead session") {
		t.Fatalf("a worker session marked a report: %v", err)
	}
}
