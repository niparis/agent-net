package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryReconcile(t *testing.T) {
	for _, tc := range []struct {
		name, kind                       string
		old, report, wrongMachine, stale bool
		want                             string
	}{
		{name: "external", kind: "external", want: protocol.ChangeDone},
		{name: "abandoned", kind: "abandoned", old: true, want: protocol.ChangeStopped},
		{name: "young", kind: "abandoned", want: protocol.ChangeOpen},
		{name: "reported", kind: "abandoned", old: true, report: true, want: protocol.ChangeOpen},
		{name: "wrong machine", kind: "external", wrongMachine: true, want: protocol.ChangeOpen},
		{name: "stale revision", kind: "external", stale: true, want: protocol.ChangeOpen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newDeliveryRig(t)
			c := r.submit(r.a, r.workerSubmit("feat/test", commitA)).Change.Change
			if tc.old {
				if _, err := r.s.db.Exec(`UPDATE delivery_changes SET created_at=? WHERE id=?`, r.s.now().Add(-8*24*time.Hour).Unix(), c.ID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.report {
				r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
			}
			machine := r.a
			if tc.wrongMachine {
				machine = r.b
			}
			q := protocol.ChangeReconcileRequest{ChangeID: c.ID, Revision: c.Revision, HeadCommit: c.HeadCommit, Kind: tc.kind, MergedCommit: commitMerged}
			if tc.stale {
				q.Revision++
			}
			_, err := deliveryCall[protocol.ChangeView](r, machine, protocol.FrameChangeReconcile, q)
			if tc.want != protocol.ChangeOpen && err != nil {
				t.Fatal(err)
			}
			v, err := r.s.GetChange(c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if v.Change.State != tc.want {
				t.Fatalf("state %s, want %s", v.Change.State, tc.want)
			}
			if tc.want == protocol.ChangeDone && (len(v.Evidence) != 1 || v.Evidence[0].Fields["merge_kind"] != "external" || v.Change.MergedCommit != commitMerged) {
				t.Fatalf("missing external merge record: %+v", v)
			}
		})
	}
}

func TestDeliveryReconcileListKeepsTerminalEvidence(t *testing.T) {
	r := newDeliveryRig(t)
	c := r.submit(r.a, r.workerSubmit("feat/test", commitA)).Change.Change
	_, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeReconcile, protocol.ChangeReconcileRequest{ChangeID: c.ID, Revision: c.Revision, HeadCommit: c.HeadCommit, Kind: "external", MergedCommit: commitMerged})
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.s.ListChanges(protocol.ChangeListRequest{State: protocol.ChangeDone})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Changes) != 1 || len(page.Changes[0].Evidence) != 1 || page.Changes[0].Evidence[0].Note != "merged outside agent-net" {
		t.Fatalf("missing list status: %+v", page)
	}
	var pending int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND completed=0`, c.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("%d pending tasks after external merge", pending)
	}
}

func TestDeliveryReconcileHeadEqualsBaseStaysOpen(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("feat/empty", commitA)
	q.BaseCommit = commitA
	c := r.submit(r.a, q).Change.Change
	_, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeReconcile, protocol.ChangeReconcileRequest{ChangeID: c.ID, Revision: c.Revision, HeadCommit: c.HeadCommit, Kind: "external", MergedCommit: commitMerged})
	if err == nil {
		t.Fatal("external merge accepted for head equal to base")
	}
	v, err := r.s.GetChange(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Change.State != protocol.ChangeOpen || len(v.Evidence) != 0 {
		t.Fatalf("empty change was reconciled: %+v", v)
	}
}

// passedToMerge records the caller's accept and the merger's test on commitA,
// so the merger's merge report is the next thing the change needs.
func passedToMerge(t *testing.T, r *deliveryRig, c protocol.Change) {
	t.Helper()
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commitA,
		Fields: map[string]string{protocol.FieldSuite: protocol.SuiteFull}})
}

// mergeReport is the merger's merge pass: it fast-forwarded main to commitMerged.
func mergeReport(c protocol.Change, session string) protocol.StepReportRequest {
	return protocol.StepReportRequest{SessionID: session, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA,
		Fields: map[string]string{protocol.FieldMergedCommit: commitMerged}}
}

// externalReconcile is the daemon's observation that main now holds c's head.
func externalReconcile(c protocol.Change) protocol.ChangeReconcileRequest {
	return protocol.ChangeReconcileRequest{ChangeID: c.ID, Revision: c.Revision, HeadCommit: c.HeadCommit, Kind: "external", MergedCommit: commitMerged}
}

func stepClosedNotices(t *testing.T, r *deliveryRig, session string) int {
	t.Helper()
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM messages WHERE recipient=? AND body LIKE '%step closed%'`, session).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func latestMergeEvidence(t *testing.T, r *deliveryRig, change string) protocol.Evidence {
	t.Helper()
	evidence, err := changeEvidence(r.s.db, change)
	if err != nil {
		t.Fatal(err)
	}
	var out *protocol.Evidence
	for i := range evidence {
		if evidence[i].StepRef == (protocol.StepRef{Step: protocol.StepMerge}) {
			out = &evidence[i]
		}
	}
	if out == nil {
		t.Fatal("no merge evidence")
	}
	return *out
}

// recordOldExternalMerge writes the external merge the daemon recorded before
// the grace window existed: merge evidence at time at, the change done, and no
// after-merge tasks.
func recordOldExternalMerge(t *testing.T, r *deliveryRig, c protocol.Change, at int64) {
	t.Helper()
	tx, err := r.s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	e := protocol.Evidence{ChangeID: c.ID, Revision: c.Revision, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: c.HeadCommit,
		By: "daemon", At: at, Note: "merged outside agent-net",
		Fields: map[string]string{"reconcile_kind": "external", "merge_kind": "external", protocol.FieldMergedCommit: commitMerged}}
	if err := insertEvidence(tx, e); err != nil {
		t.Fatal(err)
	}
	c.State, c.MergedCommit = protocol.ChangeDone, commitMerged
	if err := writeChange(tx, c, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM delivery_step_tasks WHERE change_id=? AND after_merge=1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// The race seen live on 2026-11-08 (change ch_d176): the merger passed its
// test and fast-forwarded main, and the daemon's git check saw the merge
// seconds before the merger's own report. The daemon must not record the merge
// yet, and the merger's report must then be accepted.
func TestDeliveryReconcileWaitsForMergerReport(t *testing.T) {
	r := setupMergerRig(t)
	c := r.submit(r.a, r.workerSubmit("feature/race", commitA)).Change.Change
	passedToMerge(t, r, c)

	if _, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeReconcile, externalReconcile(c)); err == nil {
		t.Fatal("external merge recorded seconds after the merger's test report")
	}
	if v := r.get(c.ID); v.Change.State != protocol.ChangeOpen || len(v.Evidence) != 2 {
		t.Fatalf("change moved during the grace window: %+v", v)
	}

	res := r.mustReport(r.a, mergeReport(c, r.sonnet))
	if res.Change.Change.State != protocol.ChangeMerged || res.Change.Change.MergedCommit != commitMerged {
		t.Fatalf("merger's merge report left the change %+v", res.Change.Change)
	}
}

// After the grace window the daemon records the merge from git. The change is
// done and queues no steps. The merger still reports the merge: its report for
// the same merged commit is accepted, it becomes the latest merge report in
// place of the git observation, and the merger is not told the change was closed.
func TestDeliveryMergerConfirmsExternalMerge(t *testing.T) {
	r := setupMergerRig(t)
	c := r.submit(r.a, r.workerSubmit("feature/confirm", commitA)).Change.Change
	passedToMerge(t, r, c)
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Commit: commitA}); err != nil {
		t.Fatalf("claim merge: %v", err)
	}
	r.clk.t = r.clk.t.Add(11 * time.Minute)
	if _, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeReconcile, externalReconcile(c)); err != nil {
		t.Fatalf("external merge after the grace window: %v", err)
	}
	if got := r.get(c.ID).Change.State; got != protocol.ChangeDone {
		t.Fatalf("state %s after a plain external merge, want done", got)
	}
	if n := stepClosedNotices(t, r, r.sonnet); n != 0 {
		t.Fatalf("merger got %d step closed notices for an external merge", n)
	}

	res := r.mustReport(r.a, mergeReport(c, r.sonnet))
	if res.Change.Change.State != protocol.ChangeMerged {
		t.Fatalf("state %s after the merger's confirmation, want merged", res.Change.Change.State)
	}
	if latest := latestMergeEvidence(t, r, c.ID); latest.By != r.sonnet || latest.Fields["reconcile_kind"] != "" || latest.Fields[protocol.FieldMergedCommit] != commitMerged {
		t.Fatalf("latest merge evidence %+v, want the merger's report", latest)
	}
}

// A plain external merge is done and queues no steps. When the merger confirms
// that merge, the change moves to merged with its after-merge steps open, and it
// is done only once verify has passed.
func TestDeliveryMergerConfirmDoneOnlyAfterVerify(t *testing.T) {
	r := setupMergerRig(t)
	c := r.submit(r.a, r.workerSubmit("feature/after", commitA)).Change.Change
	passedToMerge(t, r, c)
	r.clk.t = r.clk.t.Add(11 * time.Minute)
	if _, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeReconcile, externalReconcile(c)); err != nil {
		t.Fatal(err)
	}
	if got := r.get(c.ID).Change.State; got != protocol.ChangeDone {
		t.Fatalf("state %s after a plain external merge, want done", got)
	}
	r.mustReport(r.a, mergeReport(c, r.sonnet))
	if got := r.get(c.ID).Change.State; got != protocol.ChangeMerged {
		t.Fatalf("state %s after the merger's confirmation, want merged", got)
	}

	// The merger owns the test step, the caller owns release and verify.
	for _, ref := range []protocol.StepRef{ciTestRef, releaseRef, verifyRef} {
		session := r.caller
		fields := map[string]string{}
		if ref == ciTestRef {
			session = r.sonnet
			fields[protocol.FieldSuite] = protocol.SuiteFull
		}
		if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: session, ChangeID: c.ID, StepRef: ref, Commit: commitMerged}); err != nil {
			t.Fatalf("claim %s: %v", ref, err)
		}
		r.mustReport(r.a, protocol.StepReportRequest{SessionID: session, ChangeID: c.ID, StepRef: ref, Result: protocol.StepResultPass, Commit: commitMerged, Fields: fields})
		want := protocol.ChangeMerged
		if ref == verifyRef {
			want = protocol.ChangeDone
		}
		if got := r.get(c.ID).Change.State; got != want {
			t.Fatalf("state %s after %s passed, want %s", got, ref, want)
		}
	}
}

// Before the grace window, the daemon recorded an external merge seconds after
// a merger's test report (change ch_d176 on 2026-11-08). Starting the gateway
// reopens that change: merged, with its after-merge steps queued. A plain
// external merge, with no test or merge report near it, stays done and queues
// no steps. Evidence is kept in both cases.
func TestDeliveryRepairExternalDone(t *testing.T) {
	r := setupMergerRig(t)
	raced := r.submit(r.a, r.workerSubmit("feature/raced", commitA)).Change.Change
	passedToMerge(t, r, raced)
	recordOldExternalMerge(t, r, raced, r.clk.now().Add(7*time.Second).Unix())
	before := r.get(raced.ID).Evidence

	plain := r.submit(r.a, r.workerSubmit("feature/plain", commitB)).Change.Change
	r.clk.t = r.clk.t.Add(time.Hour)
	if _, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeReconcile, protocol.ChangeReconcileRequest{ChangeID: plain.ID, Revision: plain.Revision, HeadCommit: plain.HeadCommit, Kind: "external", MergedCommit: commitMerged}); err != nil {
		t.Fatal(err)
	}
	if got := r.get(plain.ID).Change.State; got != protocol.ChangeDone {
		t.Fatalf("plain external merge is %s, want done", got)
	}

	if err := r.s.repairExternalDone(); err != nil {
		t.Fatal(err)
	}
	v := r.get(raced.ID)
	if v.Change.State != protocol.ChangeMerged {
		t.Fatalf("raced change is %s after repair, want merged", v.Change.State)
	}
	if len(v.Evidence) != len(before) {
		t.Fatalf("repair changed evidence: %d reports, had %d", len(v.Evidence), len(before))
	}
	var queued int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND after_merge=1 AND completed=0`, raced.ID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued == 0 {
		t.Fatal("repair queued no after-merge step for the raced change")
	}

	if got := r.get(plain.ID).Change.State; got != protocol.ChangeDone {
		t.Fatalf("plain external merge moved to %s on repair, want done", got)
	}
	var plainTasks int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND after_merge=1`, plain.ID).Scan(&plainTasks); err != nil {
		t.Fatal(err)
	}
	if plainTasks != 0 {
		t.Fatalf("plain external merge has %d after-merge tasks, want none", plainTasks)
	}
}
