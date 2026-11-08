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
