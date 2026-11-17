package daemon

import (
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// pickRowsFixture is a pick with a cell row that won and a level row with a
// pace reading, so both shapes cross the daemon's database.
func pickRowsFixture() []protocol.PickCandidate {
	room, margin := -12.5, 1.25
	return []protocol.PickCandidate{
		{Name: "fast", Harness: "opencode", Provider: "pa", Model: "fast", Effort: "low", Rule: protocol.PickRuleCell,
			BucketLo: 2.33, BucketHi: 2.67, Cell: protocol.PickerCell{Score: .8, Lo: .7, Hi: .9, N: 12},
			Allowed: true, Qualified: true, Best: true, Chosen: true, Winner: true,
			CostKnown: true, CostRecorded: true, CostPerRun: 1, PClean: .9, RescueCost: 2, ExpectedCost: 1.2,
			PaceRoom: &room, PaceFactor: .53, AdjustedCost: 2.26, Probability: 1, Margin: &margin},
		{Name: "slow", Harness: "opencode", Model: "slow", Rule: protocol.PickRuleLevel, BucketLo: 2.33, BucketHi: 2.67,
			Fallback: "fallback to the level rule (none allowed)", PaceFactor: 1, Weight: 2},
	}
}

// The judge result a start queues keeps its pick rows across a restart, so the
// queued start can store them when it runs.
func TestWorkerAssessmentOutboxKeepsPickRows(t *testing.T) {
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.WorkerAssessment{RunID: "k_picks", SessionID: "s_picks", PickRows: pickRowsFixture(), PickerNote: "bucket 2.33-2.67: fast"}
	if err := st.enqueueWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	st.close()
	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	rows, err := st.pendingWorkerAssessments()
	if err != nil || len(rows) != 1 {
		t.Fatalf("pending: %+v, %v", rows, err)
	}
	if !reflect.DeepEqual(rows[0].PickRows, a.PickRows) {
		t.Fatalf("pick rows lost in the outbox:\n got %+v\nwant %+v", rows[0].PickRows, a.PickRows)
	}
}

// A queued start keeps the assessment that carries its pick rows, until the
// start is dequeued.
func TestQueuedStartKeepsPickRows(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	q := queuedStart{ID: "q_picks", Owner: "s_1", Project: "p", Harness: "opencode", Model: "fast", Task: "t",
		Assessment: &protocol.WorkerAssessment{RunID: "k_queued", PickRows: pickRowsFixture(), PickerNote: "bucket 2.33-2.67: fast"},
		QueuedAt:   time.Now()}
	if err := st.addQueued(q); err != nil {
		t.Fatal(err)
	}
	rows, err := st.queue()
	if err != nil || len(rows) != 1 || rows[0].Assessment == nil {
		t.Fatalf("queue: %+v, %v", rows, err)
	}
	if !reflect.DeepEqual(rows[0].Assessment.PickRows, q.Assessment.PickRows) {
		t.Fatalf("pick rows lost in the queue:\n got %+v\nwant %+v", rows[0].Assessment.PickRows, q.Assessment.PickRows)
	}
}
