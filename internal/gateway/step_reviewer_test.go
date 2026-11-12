package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

var stepReview = protocol.StepRef{Step: protocol.StepReview}

// reviewerRig is a caller-agent rig plus a CTO session and a chief session,
// which are not of the caller's agent.
type reviewerRig struct {
	*callerAgentRig
	cto, chief, change string
}

func newReviewerRig(t *testing.T) *reviewerRig {
	t.Helper()
	r := &reviewerRig{callerAgentRig: newCallerAgentRig(t)}
	var err error
	if r.cto, err = r.s.RegisterCTOSession(r.a); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("chief", "owner"); err != nil {
		t.Fatal(err)
	}
	r.chief = r.sessionOfAgent("claude", "chief", "chief")
	// A hard change requires a review.
	q := r.workerSubmit("feature/reviewer", commitA)
	q.Difficulty = "hard"
	r.change = r.submit(r.a, q).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: r.change, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: r.change, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commitA,
		Fields: map[string]string{protocol.FieldSuite: protocol.SuiteFull}})
	return r
}

func (r *reviewerRig) name(session, to string, ref protocol.StepRef) (protocol.StepClaimResult, error) {
	r.t.Helper()
	return r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: session, ChangeID: r.change, StepRef: ref, Commit: commitA, Reviewer: to})
}

func (r *reviewerRig) review(session string) (protocol.StepReportResult, error) {
	r.t.Helper()
	return r.report(r.a, protocol.StepReportRequest{SessionID: session, ChangeID: r.change, StepRef: stepReview, Result: protocol.StepResultPass, Commit: commitA,
		Fields: map[string]string{protocol.FieldFindingsOpen: "0"}})
}

func TestCallerNamesTheCTOAsReviewerAndTheCTOReports(t *testing.T) {
	r := newReviewerRig(t)
	if _, err := r.review(r.cto); err == nil || !strings.Contains(err.Error(), "owned by the caller") {
		t.Fatalf("unnamed CTO report = %v, want owned by the caller", err)
	}
	if _, err := r.name(r.caller, r.cto, stepReview); err != nil {
		t.Fatalf("caller names the CTO: %v", err)
	}
	res, err := r.review(r.cto)
	if err != nil {
		t.Fatalf("named CTO report: %v", err)
	}
	if res.Evidence.By != r.cto || res.Evidence.ReportedBy != "" {
		t.Fatalf("evidence by %q reported_by %q, want by the CTO session %q", res.Evidence.By, res.Evidence.ReportedBy, r.cto)
	}
}

func TestCallerNamesTheChiefAsReviewerAndTheChiefReports(t *testing.T) {
	r := newReviewerRig(t)
	if _, err := r.name(r.caller, r.chief, stepReview); err != nil {
		t.Fatalf("caller names the chief: %v", err)
	}
	res, err := r.review(r.chief)
	if err != nil {
		t.Fatalf("named chief report: %v", err)
	}
	if res.Evidence.By != r.chief {
		t.Fatalf("evidence by %q, want the chief session %q", res.Evidence.By, r.chief)
	}
}

func TestUnnamedSessionCannotReportAfterAnotherWasNamed(t *testing.T) {
	r := newReviewerRig(t)
	if _, err := r.name(r.caller, r.cto, stepReview); err != nil {
		t.Fatal(err)
	}
	if _, err := r.review(r.chief); err == nil || !strings.Contains(err.Error(), "owned by the caller") {
		t.Fatalf("unnamed chief report = %v, want owned by the caller", err)
	}
}

func TestNamedReviewerCannotReportAnotherStep(t *testing.T) {
	r := newReviewerRig(t)
	if _, err := r.name(r.caller, r.cto, stepReview); err != nil {
		t.Fatal(err)
	}
	if _, err := r.accept(r.cto, r.change, commitA); err == nil || !strings.Contains(err.Error(), "owned by the caller") {
		t.Fatalf("named reviewer reported accept = %v, want owned by the caller", err)
	}
}

func TestCallerCannotNameItselfOrItsOwnAgentAsReviewer(t *testing.T) {
	r := newReviewerRig(t)
	for _, to := range []string{r.caller, r.second} {
		if _, err := r.name(r.caller, to, stepReview); err == nil || !strings.Contains(err.Error(), "reviewer") {
			t.Fatalf("naming %s = %v, want a refusal that names the reviewer", to, err)
		}
	}
}

func TestOnlyTheCallerNamesAReviewer(t *testing.T) {
	r := newReviewerRig(t)
	if _, err := r.name(r.chief, r.chief, stepReview); err == nil {
		t.Fatal("a session that is not the caller named a reviewer")
	}
}

func TestReviewerNamingEndsWithTheRevision(t *testing.T) {
	r := newReviewerRig(t)
	if _, err := r.name(r.caller, r.cto, stepReview); err != nil {
		t.Fatal(err)
	}
	q := r.workerSubmit("feature/reviewer", commitB)
	q.SessionID = r.caller
	r.submit(r.a, q)
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.cto, ChangeID: r.change, StepRef: stepReview, Result: protocol.StepResultPass, Commit: commitB,
		Fields: map[string]string{protocol.FieldFindingsOpen: "0"}}); err == nil || !strings.Contains(err.Error(), "owned by the caller") {
		t.Fatalf("report on a new revision = %v, want owned by the caller", err)
	}
}
