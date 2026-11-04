package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMergeDaemonAncestryProof(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		version  string
		refusal  string
		proof    *bool
		accepted bool
	}{
		{name: "older daemon", version: "0.4.129", accepted: true},
		{name: "first proof daemon", version: "0.4.130", refusal: "merge_contains_head"},
		{name: "newer daemon", version: "v0.4.131", refusal: "merge_contains_head"},
		{name: "newer minor", version: "0.5.0", refusal: "merge_contains_head"},
		{name: "dev daemon", version: "dev", refusal: "merge_contains_head"},
		{name: "unknown old version", version: "", accepted: true},
		{name: "contains head", version: "0.4.130", proof: &yes, accepted: true},
		{name: "does not contain head", version: "0.4.130", proof: &no, refusal: "exists and contains change head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newDeliveryRig(t)
			if err := r.s.SetNodeHello(r.a, "ha", tc.version); err != nil {
				t.Fatal(err)
			}
			c := r.submit(r.a, r.workerSubmit("feat/merge", commitA)).Change.Change
			for _, step := range []string{protocol.StepAccept, protocol.StepTest} {
				fields := map[string]string{}
				if step == protocol.StepTest {
					fields[protocol.FieldSuite] = protocol.SuiteFull
				}
				r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: step}, Result: protocol.StepResultPass, Commit: commitA, Fields: fields})
			}
			_, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldMergedCommit: commitMerged}, MergeContainsHead: tc.proof})
			if tc.accepted {
				if err != nil {
					t.Fatal(err)
				}
				if got := r.get(c.ID).Change; got.State != protocol.ChangeMerged || got.MergedCommit != commitMerged {
					t.Fatalf("merge not stored: %+v", got)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("unclear ancestry refusal: %v", err)
				}
				got := r.get(c.ID)
				if got.Change.State != protocol.ChangeOpen || len(got.Evidence) != 2 {
					t.Fatalf("refused merge changed state/evidence: %+v", got)
				}
			}
		})
	}
}

func TestMergePositiveProofStillNeedsSteps(t *testing.T) {
	r := newDeliveryRig(t)
	c := r.submit(r.a, r.workerSubmit("feat/merge", commitA)).Change.Change
	yes := true
	_, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldMergedCommit: commitMerged}, MergeContainsHead: &yes})
	if err == nil || !strings.Contains(err.Error(), "merge is not ready") {
		t.Fatalf("proof bypassed prerequisites: %v", err)
	}
}
