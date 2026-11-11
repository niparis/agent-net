package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

var (
	releaseRef = protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}
	verifyRef  = protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}
	ciTestRef  = protocol.StepRef{Step: protocol.StepTest, AfterMerge: true}
)

// pairEvidence is a pass by a session that is not the caller, so the setup
// does not look like recent caller activity to the coalescing rule.
func pairEvidence(c protocol.Change, ref protocol.StepRef, commit string, fields map[string]string, at int64, by string) protocol.Evidence {
	return protocol.Evidence{ChangeID: c.ID, Revision: c.Revision, StepRef: ref, Result: protocol.StepResultPass,
		Commit: commit, Fields: fields, By: by, At: at}
}

// mergedChange records the evidence of a merged change, with the CI test on
// the merged commit already passed when ci is true, and queues its ready
// after-merge steps. Earlier step tasks and messages are cleared so the test
// sees only what the merged state produces.
func mergedChange(t *testing.T, r *deliveryRig, branch string, ci bool) protocol.Change {
	t.Helper()
	c := r.submit(r.a, r.workerSubmit(branch, commitA)).Change.Change
	p, err := r.s.deliveryPipeline(c.Project)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := r.s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{sql: `DELETE FROM delivery_step_messages`},
		{sql: `DELETE FROM delivery_step_tasks WHERE change_id=?`, args: []any{c.ID}},
		{sql: `DELETE FROM messages`},
	} {
		if _, err = tx.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	evidence := []protocol.Evidence{
		pairEvidence(c, stepAccept, commitA, nil, 1, r.worker),
		pairEvidence(c, protocol.StepRef{Step: protocol.StepTest}, commitA, map[string]string{protocol.FieldSuite: protocol.SuiteChanged}, 2, r.worker),
		pairEvidence(c, protocol.StepRef{Step: protocol.StepMerge}, commitA, map[string]string{protocol.FieldMergedCommit: commitMerged}, 3, r.worker),
	}
	if ci {
		evidence = append(evidence, pairEvidence(c, ciTestRef, commitMerged, map[string]string{protocol.FieldSuite: protocol.SuiteFull}, 4, r.worker))
	}
	for _, e := range evidence {
		if err := insertEvidence(tx, e); err != nil {
			t.Fatal(err)
		}
	}
	c.State, c.MergedCommit = protocol.ChangeMerged, commitMerged
	if err := writeChange(tx, c, false); err != nil {
		t.Fatal(err)
	}
	if err := r.s.queueSteps(tx, c, p, r.s.now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return c
}

// callerNeedsSharedPipeline keeps the caller's own steps routable: a step task
// goes to the caller only when another owner exists in the pipeline.
func callerNeedsSharedPipeline(t *testing.T, r *deliveryRig) {
	t.Helper()
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: protocol.StepReview, Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}
}

func callerMessages(t *testing.T, r *deliveryRig, change string) []string {
	t.Helper()
	rows, err := r.s.db.Query(`SELECT DISTINCT m.body FROM messages m JOIN delivery_step_messages d ON d.message_id=m.id
		WHERE d.session_id=? AND d.change_id=? ORDER BY m.id`, r.caller, change)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		out = append(out, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStepPairReleaseVerifyOneMessage(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/pair-release", true)

	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)

	bodies := callerMessages(t, r, c.ID)
	if len(bodies) != 1 {
		t.Fatalf("caller got %d task messages, want 1: %v", len(bodies), bodies)
	}
	body := bodies[0]
	if !strings.Contains(body, "steps: release, verify (after merge)") {
		t.Fatalf("pair line missing from:\n%s", body)
	}
	if !strings.Contains(body, "Claim and report each step in order: release, then verify.") {
		t.Fatalf("order guidance missing from:\n%s", body)
	}
	var tasks int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_tasks WHERE change_id=? AND revision=? AND owner_node=?`,
		c.ID, c.Revision, callerTaskOwner).Scan(&tasks); err != nil || tasks != 2 {
		t.Fatalf("caller tasks=%d want=2 err=%v", tasks, err)
	}
}

func TestStepPairReleaseVerifyClaimAndReport(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/pair-report", true)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)

	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: releaseRef, Commit: commitMerged}); err != nil {
		t.Fatalf("claim release: %v", err)
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: releaseRef,
		Result: protocol.StepResultPass, Commit: commitMerged})

	if got := len(callerMessages(t, r, c.ID)); got != 1 {
		t.Fatalf("after release the caller has %d messages, want the one pair message", got)
	}

	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: verifyRef, Commit: commitMerged}); err != nil {
		t.Fatalf("claim verify: %v", err)
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: verifyRef,
		Result: protocol.StepResultPass, Commit: commitMerged})

	if got := r.get(c.ID).Change.State; got != protocol.ChangeDone {
		t.Fatalf("state after verify = %s, want done", got)
	}
	var reported int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_evidence WHERE change_id=? AND revision=? AND after_merge=1 AND result='pass'`,
		c.ID, c.Revision).Scan(&reported); err != nil || reported != 3 {
		t.Fatalf("after-merge evidence=%d want=3 err=%v", reported, err)
	}
}

func TestStepPairLaterStepBeforeEarlierRefused(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/pair-order", true)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)

	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: verifyRef, Commit: commitMerged}); err == nil {
		t.Fatal("claimed verify before release passed")
	} else if !strings.Contains(err.Error(), "not ready") || !strings.Contains(err.Error(), "release") {
		t.Fatalf("claim refusal does not name release: %v", err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: verifyRef,
		Result: protocol.StepResultPass, Commit: commitMerged}); err == nil {
		t.Fatal("reported verify before release passed")
	} else if !strings.Contains(err.Error(), "not ready") || !strings.Contains(err.Error(), "release") {
		t.Fatalf("report refusal does not name release: %v", err)
	}

	// Release is still claimable, and its pass opens verify.
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: releaseRef,
		Result: protocol.StepResultPass, Commit: commitMerged})
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: verifyRef, Commit: commitMerged}); err != nil {
		t.Fatalf("claim verify after release: %v", err)
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: verifyRef,
		Result: protocol.StepResultPass, Commit: commitMerged})
	if got := r.get(c.ID).Change.State; got != protocol.ChangeDone {
		t.Fatalf("state after verify = %s, want done", got)
	}
}

func TestStepPairSplitOwnersSeparateTasks(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: protocol.StepMerge, Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}
	mergedBy, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "claude", NativeID: "pair-reviewer", Repo: protocol.RepoInfo{Project: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	c := r.submit(r.a, r.workerSubmit("feature/pair-split", commitA)).Change.Change

	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept,
		Result: protocol.StepResultPass, Commit: commitA})
	var testOwner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='test' AND after_merge=0`, c.ID).Scan(&testOwner); err != nil {
		t.Fatalf("test task is missing: %v", err)
	}
	if testOwner != callerTaskOwner {
		t.Fatalf("test owner = %q, want %q", testOwner, callerTaskOwner)
	}

	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepTest},
		Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}})
	var pairLine int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE body LIKE '%steps: test, merge%'`).Scan(&pairLine); err != nil || pairLine != 0 {
		t.Fatalf("test and merge were paired across owners: %d %v", pairLine, err)
	}
	var mergeOwner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='merge' AND after_merge=0`, c.ID).Scan(&mergeOwner); err != nil {
		t.Fatalf("merge task is not queued: %v", err)
	}
	if mergeOwner != "reviewer" {
		t.Fatalf("merge owner = %q, want reviewer", mergeOwner)
	}

	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(mergedBy)

	var mergerBody string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE id IN (SELECT message_id FROM delivery_step_messages WHERE session_id=?) ORDER BY id DESC LIMIT 1`, mergedBy).Scan(&mergerBody); err != nil {
		t.Fatalf("merge owner got no task message: %v", err)
	}
	if !strings.Contains(mergerBody, "step: merge") {
		t.Fatalf("merge owner message does not name merge:\n%s", mergerBody)
	}
	for _, body := range callerMessages(t, r, c.ID) {
		if strings.Contains(body, "steps: test, merge") || strings.Contains(body, "| step: merge |") {
			t.Fatalf("caller message mentions merge across owners:\n%s", body)
		}
	}
}

func TestStepPairAcceptReviewOneMessage(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	q := r.workerSubmit("feature/pair-accept-review", commitA)
	q.Difficulty = "hard"
	c := r.submit(r.a, q).Change.Change

	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	bodies := callerMessages(t, r, c.ID)
	if len(bodies) != 1 {
		t.Fatalf("caller got %d messages, want 1: %v", len(bodies), bodies)
	}
	if !strings.Contains(bodies[0], "step: accept") || !strings.Contains(bodies[0], "step: review") {
		t.Fatalf("one message does not name both accept and review:\n%s", bodies[0])
	}
}

// A merged change whose CI test has not passed yet pairs the CI test with the
// step that waits only for it; verify is queued when release passes.
func TestStepPairAfterMergePairsOnlyNextStep(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/pair-run", false)

	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	bodies := callerMessages(t, r, c.ID)
	if len(bodies) != 1 {
		t.Fatalf("caller got %d messages, want 1: %v", len(bodies), bodies)
	}
	if !strings.Contains(bodies[0], "step: test after merge") {
		t.Fatalf("pair line missing from:\n%s", bodies[0])
	}
	if strings.Contains(bodies[0], "then release") {
		t.Fatalf("order guidance missing from:\n%s", bodies[0])
	}
	var verifyTasks int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_tasks WHERE change_id=? AND step IN ('release','verify') AND completed=0`, c.ID).Scan(&verifyTasks); err != nil || verifyTasks != 0 {
		t.Fatalf("verify task rows=%d want=0 err=%v", verifyTasks, err)
	}
}

func TestStepTaskReleaseRequiresPassingTest(t *testing.T) {
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/release-readiness", false)
	assertTasks := func(want int) {
		t.Helper()
		var got int
		if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_tasks WHERE change_id=? AND step='release' AND completed=0`, c.ID).Scan(&got); err != nil || got != want {
			t.Fatalf("active release tasks=%d want=%d err=%v", got, want, err)
		}
	}
	assertTasks(0)
	// Reproduce a stale task left by an older gateway before recording failure.
	if _, err := r.s.db.Exec(`INSERT INTO delivery_step_tasks (change_id,revision,step,for_step,after_merge,owner_node,created_at) VALUES(?,?,'release','',1,?,1)`, c.ID, c.Revision, callerTaskOwner); err != nil {
		t.Fatal(err)
	}
	record := func(result string, at int64) {
		t.Helper()
		tx, err := r.s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		e := pairEvidence(c, ciTestRef, commitMerged, map[string]string{protocol.FieldSuite: protocol.SuiteFull}, at, r.worker)
		e.Result = result
		if err := insertEvidence(tx, e); err != nil {
			t.Fatal(err)
		}
		p, err := r.s.deliveryPipeline(c.Project)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.s.queueSteps(tx, c, p, r.s.now().Unix()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	record(protocol.StepResultFail, 4)
	assertTasks(0)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	for _, body := range callerMessages(t, r, c.ID) {
		if strings.Contains(body, "step: release") || strings.Contains(body, "test, release") {
			t.Fatalf("unready release reminder: %s", body)
		}
	}
	record(protocol.StepResultPass, 5)
	assertTasks(1)
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
	found := false
	for _, body := range callerMessages(t, r, c.ID) {
		if strings.Contains(body, "step: release") || strings.Contains(body, "steps: release") {
			found = true
		}
	}
	if !found {
		t.Fatal("ready release was not dispatched")
	}
}
