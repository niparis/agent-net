package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestVisualProofStoredOnChange(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("feature/proof", commitA)
	q.Origin.VisualEvidence = &protocol.VisualEvidence{UserVisible: "yes", VisualProof: []protocol.VisualProof{{File: "frame.txt", Caption: "open at 120x34"}}}
	out, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, q)
	if err != nil {
		t.Fatal(err)
	}
	got, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: out.Change.Change.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Change.Origin.VisualProof) != 1 || got.Change.Origin.VisualProof[0].Caption != "open at 120x34" {
		t.Fatalf("proof lost: %+v", got)
	}
}
func TestVisualProofWeeklyCount(t *testing.T) {
	r := newDeliveryRig(t)
	run := protocol.WorkerRun{NodeID: r.a, SessionID: "s_proof_test", Harness: "test", Model: "test-model", Outcome: protocol.OutcomeFailed, ProofUnavailable: "graphical runtime unavailable"}
	for i := 0; i < 2; i++ {
		if err := r.s.RecordWorkerRun(run); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.s.ScoresWeeklyAt(r.s.now())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, row := range rows {
		n += row.ProofUnavailable["test-model"]
	}
	if n != 1 {
		t.Fatalf("count = %d", n)
	}
}

func TestVisualBriefCheckWarnsOnly(t *testing.T) {
	a := newAgyGate(t)
	task := gatewayBrief("internal/tui/view.go")
	answer, err := a.g.workerStart(context.Background(), a.nc, protocol.WorkerStartFrame{Owner: a.owner, Level: "normal", WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: task, Harness: "agy", Model: "gemini-3.8-flash-low", Mode: protocol.WorkerStartModeCheck}})
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Checked || len(answer.BriefFailures) != 1 || !strings.HasPrefix(answer.BriefFailures[0], "WARNING:") {
		t.Fatalf("expected warning: %+v", answer)
	}
	failures, _, err := a.g.store.checkWorkerBrief(protocol.WorkerStartFrame{WorkerStartRequest: protocol.WorkerStartRequest{Task: task}}, "alpha", protocol.WorkerBriefRepo{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("visual warning entered refusal checks: %v %v", failures, err)
	}
}

// Version 180 was the last migration shipped before visual proof. Recreate
// that store without the proof table, then reopen it through the upgrade path.
func TestVisualProofMigrationUpgrade(t *testing.T) {
	const oldLastVersion = 187
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE worker_visual_proof; DELETE FROM schema_version WHERE version > ?`, oldLastVersion); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if got, err := s.SchemaVersion(); err != nil || got != oldLastVersion {
		s.Close()
		t.Fatalf("old version = %d, %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "test-node", SessionID: "test-session", Harness: "test", Model: "test-model", Outcome: protocol.OutcomeFailed, ProofUnavailable: "runtime unavailable"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_visual_proof WHERE unavailable='runtime unavailable'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("proof reports = %d, %v", count, err)
	}
}
