package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

var testSignals = []protocol.QualitySignal{
	{Code: protocol.SignalRepeatedLine, Strength: protocol.SignalStrong, Evidence: "repeated line ×3: p.Ack || p.Type == protocol.TypeAck in server.go, workers.go"},
	{Code: protocol.SignalHeavyTests, Strength: protocol.SignalWeak, Evidence: "heavy tests: 300 test lines added for 12 code lines"},
}

// Signals belong to the revision that carried them, and a new revision shows
// its own set, not the one before.
func TestChangeQualitySignalsStayWithTheRevision(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("feat/signals", commitA)
	q.Signals = testSignals
	c := r.submit(r.a, q).Change.Change
	view := r.get(c.ID)
	if len(view.Signals) != 2 || view.Signals[0].Code != protocol.SignalRepeatedLine || view.Signals[1].Strength != protocol.SignalWeak {
		t.Fatalf("signals on revision 1: %+v", view.Signals)
	}
	q.HeadCommit = commitB
	q.Signals = []protocol.QualitySignal{{Code: protocol.SignalTestTiming, Strength: protocol.SignalStrong, Evidence: "test timing ×1: time.Sleep(5 * time.Millisecond) in a_test.go"}}
	if got := r.submit(r.a, q).Change; got.Change.Revision != 2 || len(got.Signals) != 1 || got.Signals[0].Code != protocol.SignalTestTiming {
		t.Fatalf("signals on revision 2: %+v", got.Signals)
	}
}

// The accept task carries the signals in the same one-line-per-signal form
// the Desk shows.
func TestDeliveryTaskShowsQualitySignals(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	q := r.workerSubmit("feature/signals", commitA)
	q.Signals = testSignals
	if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, q); err != nil {
		t.Fatal(err)
	}
	msgs, err := r.s.Messages(r.b, protocol.QueryMessages{SessionID: sid})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("task messages: %+v, %v", msgs, err)
	}
	for _, want := range []string{"quality signals:", "strong: repeated line ×3:", "weak: heavy tests:"} {
		if !strings.Contains(msgs[0].Body, want) {
			t.Fatalf("task body lacks %q:\n%s", want, msgs[0].Body)
		}
	}
	for _, line := range strings.Split(msgs[0].Body, "\n") {
		if strings.Count(line, "repeated line ×3") > 1 {
			t.Fatalf("signal repeated on one line: %q", line)
		}
	}
}

// An older database gets the column without losing the revision row it holds.
func TestChangeQualitySignalsMigrationUpgrade(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("feat/old", commitA)).Change.Change.ID
	if _, err := r.s.db.Exec(`ALTER TABLE delivery_revision_stats DROP COLUMN signals`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "change quality signals column")); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, r.s); err != nil {
		t.Fatal(err)
	}
	view := r.get(id)
	if view.Change.HeadCommit != commitA || len(view.Signals) != 0 {
		t.Fatalf("upgrade changed existing work: %+v", view)
	}
}
