package daemon

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A queued automatic start keeps its nature across the database, so the daemon
// knows to ask the gate again when it leaves the queue (decision 0162).
func TestQueuedAutomaticStartSurvivesTheDatabase(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	q := queuedStart{ID: "q_auto", Owner: "s_1", Project: "p", Harness: "opencode", Model: "m", Task: "t", QueuedAt: time.Now(), Automatic: true}
	if err := st.addQueued(q); err != nil {
		t.Fatal(err)
	}
	rows, err := st.queue()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %+v %v", rows, err)
	}
	if !rows[0].Automatic {
		t.Fatal("the queued start lost its automatic mark")
	}
}

// The gate's answer replaces the queued model, and a refusal keeps the start
// from launching anything.
func TestApplyRecheck(t *testing.T) {
	a := &protocol.WorkerAssessment{SessionID: "a", PickerNote: "old"}
	e := queuedStart{ID: "q_1", Harness: "claude", Model: "opus", Why: "old why", Assessment: a}
	if refused := applyRecheck(&e, protocol.WorkerRecheckAnswer{Refused: true, Reason: "no model can run now"}); refused != "no model can run now" {
		t.Fatalf("refused = %q", refused)
	}
	answer := protocol.WorkerRecheckAnswer{Harness: "codex", Model: "gpt-6.1-terra", Why: "hard: terra", ModelNote: "picker note"}
	if refused := applyRecheck(&e, answer); refused != "" {
		t.Fatalf("unexpected refusal %q", refused)
	}
	if e.Harness != "codex" || e.Model != "gpt-6.1-terra" || e.Why != "hard: terra" {
		t.Fatalf("start after the recheck: %+v", e)
	}
	if e.Assessment == nil || e.Assessment.PickerNote != "picker note" || e.Assessment.SessionID != "a" {
		t.Fatalf("assessment after the recheck: %+v", e.Assessment)
	}
}
