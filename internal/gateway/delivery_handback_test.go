package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A hand-back check belongs to the revision that carried it, like its signals.
func TestChangeHandbackCheckStaysWithTheRevision(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("feat/check", commitA)
	q.HandbackCheck = &protocol.HandbackCheck{Status: protocol.HandbackCheckFailed, ExitCode: 3, DurationMS: 1200, OutputTail: "the check found a problem"}
	view := r.submit(r.a, q).Change
	check := view.HandbackCheck
	if check == nil || check.Status != protocol.HandbackCheckFailed || check.ExitCode != 3 || check.OutputTail == "" {
		t.Fatalf("revision 1 hand-back check: %+v", check)
	}
	if got := r.get(view.Change.ID).HandbackCheck; got == nil || got.Status != protocol.HandbackCheckFailed {
		t.Fatalf("stored hand-back check: %+v", got)
	}

	q.HeadCommit = commitB
	q.HandbackCheck = &protocol.HandbackCheck{Status: protocol.HandbackCheckPassed, DurationMS: 40}
	next := r.submit(r.a, q).Change
	if next.Change.Revision != 2 || next.HandbackCheck == nil || next.HandbackCheck.Status != protocol.HandbackCheckPassed {
		t.Fatalf("revision 2 hand-back check: %+v", next.HandbackCheck)
	}
}

// An older database gets the column without losing the revision it holds.
func TestChangeHandbackCheckMigrationUpgrade(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("feat/old-check", commitA)).Change.Change.ID
	if _, err := r.s.db.Exec(`ALTER TABLE delivery_revision_stats DROP COLUMN handback_check`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "change handback check column")); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, r.s); err != nil {
		t.Fatal(err)
	}
	view := r.get(id)
	if view.Change.HeadCommit != commitA || view.HandbackCheck != nil {
		t.Fatalf("upgrade changed existing work: %+v", view)
	}
}
