package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// fieldsRig is a change with accept passed and the merger (Sonnet) owning
// test, which merge requires with suite=*.
func fieldsRig(t *testing.T) (*deliveryRig, string) {
	t.Helper()
	r := newDeliveryRig(t)
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/fields", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	return r, id
}

func (r *deliveryRig) testReport(session, id, result string, fields map[string]string) protocol.StepReportRequest {
	return protocol.StepReportRequest{SessionID: session, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: result, Commit: commitA, Fields: fields}
}

func (r *deliveryRig) mergeReady(id string) (bool, string) {
	r.t.Helper()
	for _, st := range r.get(id).Steps {
		if st.Step == protocol.StepMerge {
			return st.State == protocol.StepStateReady, strings.Join(st.Missing, "; ")
		}
	}
	r.t.Fatal("no merge step")
	return false, ""
}

// A pass that a later step requires fields of is refused up front, naming the
// field, so the reporter fixes it at once and not at the merge claim.
func TestStepPassWithoutRequiredFieldIsRefused(t *testing.T) {
	r, id := fieldsRig(t)
	_, err := r.report(r.a, r.testReport(r.sonnet, id, protocol.StepResultPass, nil))
	if err == nil || !strings.Contains(err.Error(), "test pass needs fields.suite, for example suite=full") {
		t.Fatalf("pass without suite: %v", err)
	}
	if n := len(r.get(id).Evidence); n != 1 {
		t.Fatalf("evidence %d, want only the accept pass", n)
	}
	// Only a pass needs the fields.
	r.mustReport(r.a, r.testReport(r.sonnet, id, protocol.StepResultFail, nil))
}

// A pass stored before the check existed, without suite, is fixed by the same
// session reporting it again on the same commit.
func TestSameSessionReportsAStoredPassAgainWithTheMissingField(t *testing.T) {
	r, id := fieldsRig(t)
	if _, err := r.s.db.Exec(`INSERT INTO delivery_evidence(change_id,revision,step,for_step,after_merge,result,commit_hash,fields,by_session,reported_by,by_model,by_model_family,note,at)
		VALUES(?,1,'test','',0,'pass',?,'{"independent":"none"}',?,'','','','',1)`, id, commitA, r.sonnet); err != nil {
		t.Fatal(err)
	}
	if res, err := r.s.db.Exec(`UPDATE delivery_step_tasks SET claimed_by=?,completed=1 WHERE change_id=? AND step='test' AND after_merge=0`, r.sonnet, id); err != nil {
		t.Fatal(err)
	} else if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("test task rows %d, want 1", n)
	}
	if ready, why := r.mergeReady(id); ready || !strings.Contains(why, "suite is not set") {
		t.Fatalf("merge ready=%v missing %q before the fix", ready, why)
	}
	// Another session still cannot overwrite the step.
	if _, err := r.report(r.a, r.testReport(r.caller, id, protocol.StepResultPass, map[string]string{protocol.FieldSuite: protocol.SuiteFull})); err == nil || !strings.Contains(err.Error(), "already reported by "+r.sonnet) {
		t.Fatalf("another session's second report: %v", err)
	}
	r.mustReport(r.a, r.testReport(r.sonnet, id, protocol.StepResultPass, map[string]string{protocol.FieldSuite: protocol.SuiteFull}))
	if ready, why := r.mergeReady(id); !ready {
		t.Fatalf("merge not ready after the second report: %s", why)
	}
}

// Once merge has passed, the evidence it relied on stays as it was.
func TestSameSessionCannotReportAgainAfterMergePassed(t *testing.T) {
	r, id := fieldsRig(t)
	r.mustReport(r.a, r.testReport(r.sonnet, id, protocol.StepResultPass, map[string]string{protocol.FieldSuite: protocol.SuiteChanged}))
	yes := true
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA,
		Fields: map[string]string{protocol.FieldMergedCommit: commitMerged}, MergeContainsHead: &yes})
	if _, err := r.report(r.a, r.testReport(r.sonnet, id, protocol.StepResultPass, map[string]string{protocol.FieldSuite: protocol.SuiteFull})); err == nil {
		t.Fatal("a test on the old head was reported again after merge")
	}
}

// A session that is not the change's caller (the merger, say) cannot submit a
// new head for it; the refusal says who can.
func TestSubmitByNonCallerSaysToAskTheCaller(t *testing.T) {
	r, id := fieldsRig(t)
	q := r.workerSubmit("feature/fields", commitB)
	q.SessionID, q.ChangeID = r.sonnet, id
	_, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, q)
	if err == nil || !strings.Contains(err.Error(), "belongs to caller "+r.caller) || !strings.Contains(err.Error(), "ask the caller to submit it") {
		t.Fatalf("submit by a non-caller: %v", err)
	}
}
