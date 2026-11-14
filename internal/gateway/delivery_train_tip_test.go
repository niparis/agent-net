package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// In train mode the merger runs CI once on the train tip and cites that run
// for every change. When a change's merged commit is the tested tip, the same
// run is its post-merge test: the gateway records the step, so the merger does
// not report a second CI that never ran.
func TestTrainTipMergeRecordsThePostMergeTest(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.ci_mode", Level: "repo", Scope: "demo", Value: settingValue(`"train"`)}}, "cto", "test train"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	pass := func(ref protocol.StepRef, commit string, fields map[string]string) protocol.Change {
		t.Helper()
		return r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commit, Fields: fields}).Change.Change
	}
	pass(stepAccept, commitA, nil)
	pass(protocol.StepRef{Step: protocol.StepTest}, commitA, map[string]string{
		protocol.FieldSuite:        protocol.SuiteFull,
		protocol.FieldRun:          "https://ci.example/run/42",
		protocol.FieldTestedCommit: commitMerged,
	})
	c := pass(protocol.StepRef{Step: protocol.StepMerge}, commitA, map[string]string{protocol.FieldMergedCommit: commitMerged})
	if c.State != protocol.ChangeMerged {
		t.Fatalf("after merge the change is %s", c.State)
	}
	var auto *protocol.Evidence
	for i, e := range r.get(id).Evidence {
		if e.Step == protocol.StepTest && e.AfterMerge && e.Commit == commitMerged {
			auto = &r.get(id).Evidence[i]
		}
	}
	if auto == nil {
		t.Fatal("the post-merge test was not recorded from the tested train tip")
	}
	if auto.Result != protocol.StepResultPass || auto.Fields[protocol.FieldSuite] != protocol.SuiteFull ||
		auto.Fields[protocol.FieldRun] != "https://ci.example/run/42" || auto.Fields[protocol.FieldIndependent] == "" {
		t.Fatalf("the recorded post-merge test: %+v", auto)
	}
	if !strings.Contains(auto.Note, "train tip") {
		t.Errorf("the note does not say where the result came from: %q", auto.Note)
	}
}

// A merge commit that the CI run did not test records nothing: the post-merge
// test step stays unchanged and waits for its own report.
func TestMergeOtherThanTheTestedTipRecordsNoPostMergeTest(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.ci_mode", Level: "repo", Scope: "demo", Value: settingValue(`"train"`)}}, "cto", "test train"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	pass := func(ref protocol.StepRef, commit string, fields map[string]string) protocol.Change {
		t.Helper()
		return r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commit, Fields: fields}).Change.Change
	}
	pass(stepAccept, commitA, nil)
	pass(protocol.StepRef{Step: protocol.StepTest}, commitA, map[string]string{
		protocol.FieldSuite:        protocol.SuiteFull,
		protocol.FieldRun:          "https://ci.example/run/42",
		protocol.FieldTestedCommit: commitB,
	})
	pass(protocol.StepRef{Step: protocol.StepMerge}, commitA, map[string]string{protocol.FieldMergedCommit: commitMerged})
	for _, e := range r.get(id).Evidence {
		if e.Step == protocol.StepTest && e.AfterMerge && e.Commit == commitMerged {
			t.Fatalf("a post-merge test was recorded for an untested commit: %+v", e)
		}
	}
}
