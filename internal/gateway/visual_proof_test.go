package gateway

import (
	"context"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
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
	failures, err := a.g.store.checkWorkerBrief(protocol.WorkerStartFrame{WorkerStartRequest: protocol.WorkerStartRequest{Task: task}}, "alpha")
	if err != nil || len(failures) != 0 {
		t.Fatalf("visual warning entered refusal checks: %v %v", failures, err)
	}
}
