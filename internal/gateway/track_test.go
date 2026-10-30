package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestTrackRecordCountsAndMedian(t *testing.T) {
	s, _ := newTestStore(t)
	now := s.now()
	add := func(node, harness, model, outcome string, ago time.Duration, run time.Duration) {
		t.Helper()
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_" + outcome + harness,
			Harness: harness, Model: model, Outcome: outcome,
			StartedAt: now.Add(-ago).Add(-run).Unix(), EndedAt: now.Add(-ago).Unix(), RunSeconds: int64(run / time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	add("n1", "pi", "terra", protocol.OutcomeComplete, time.Hour, 180*time.Second)
	add("n1", "pi", "terra", protocol.OutcomePartial, 2*time.Hour, 60*time.Second)
	add("n1", "pi", "terra", protocol.OutcomePartial, 3*time.Hour, 60*time.Second)
	add("n1", "pi", "terra", protocol.OutcomePartial, 4*time.Hour, 60*time.Second)
	add("n1", "opencode", "other", protocol.OutcomeFailed, time.Hour, 30*time.Second)
	add("n2", "pi", "terra", protocol.OutcomeTimedOut, time.Hour, 10*time.Second)

	tr, ok, err := s.TrackRecord("n1", "pi", "terra", 30*24*time.Hour)
	if err != nil || !ok {
		t.Fatalf("track record: %v %v", ok, err)
	}
	if tr.Runs != 4 || tr.Complete != 1 || tr.Partial != 3 || tr.Failed != 0 || tr.TimedOut != 0 {
		t.Errorf("counts: %+v", tr)
	}
	if tr.MedianRunSeconds != 60 {
		t.Errorf("median: %d", tr.MedianRunSeconds)
	}
	if tr.Resumable {
		t.Error("Pi cannot resume")
	}
	all, err := s.TrackRecords("n1", 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Harness != "opencode" || all[1].Model != "terra" {
		t.Errorf("tracks: %+v", all)
	}
	other, ok, _ := s.TrackRecord("n2", "pi", "terra", 30*24*time.Hour)
	if !ok || other.TimedOut != 1 || other.Runs != 1 {
		t.Errorf("other node: %+v", other)
	}
}

// A stopped run is counted apart and never as a failure (a stop on request is
// not the model's fault).
func TestTrackRecordCountsStoppedApart(t *testing.T) {
	s, _ := newTestStore(t)
	now := s.now()
	add := func(outcome string, ago time.Duration) {
		t.Helper()
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "n1", SessionID: "s_" + outcome,
			Harness: "opencode", Model: "m1", Outcome: outcome,
			StartedAt: now.Add(-ago).Unix(), EndedAt: now.Add(-ago).Unix(), RunSeconds: 30}); err != nil {
			t.Fatal(err)
		}
	}
	add(protocol.OutcomeStopped, time.Hour)
	add(protocol.OutcomeComplete, 2*time.Hour)
	add(protocol.OutcomeFailed, 3*time.Hour)
	tr, ok, err := s.TrackRecord("n1", "opencode", "m1", 30*24*time.Hour)
	if err != nil || !ok {
		t.Fatalf("track record: %v %v", ok, err)
	}
	if tr.Runs != 3 || tr.Stopped != 1 || tr.Failed != 1 || tr.Complete != 1 {
		t.Errorf("counts: %+v", tr)
	}
	if !strings.Contains(tr.Line(), "1 stopped") {
		t.Errorf("line: %q", tr.Line())
	}
	subject, ok, err := s.TrackSubject(protocol.SubjectModel, "m1", 30*24*time.Hour)
	if err != nil || !ok || subject.Stopped != 1 || subject.Failed != 1 {
		t.Errorf("model subject: %+v %v %v", subject, ok, err)
	}
}

func TestTrackRecordKeepsThirtyDays(t *testing.T) {
	s, _ := newTestStore(t)
	now := s.now()
	old := protocol.WorkerRun{NodeID: "n1", SessionID: "s_old", Harness: "pi", Model: "terra",
		Outcome: protocol.OutcomeComplete, StartedAt: now.Add(-31 * 24 * time.Hour).Unix(), EndedAt: now.Add(-31 * 24 * time.Hour).Unix()}
	if err := s.RecordWorkerRun(old); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.TrackRecord("n1", "pi", "terra", 30*24*time.Hour); ok {
		t.Error("a run older than 30 days was counted")
	}
	res, err := s.Prune(Retention{WorkerRuns: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if res.WorkerRuns != 1 {
		t.Errorf("pruned %d runs, want 1", res.WorkerRuns)
	}
}

func TestPlaybookNotes(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.SetPlaybook("deepseek-v4.1-flash", "", "Ask for a checklist."); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlaybook("deepseek-v4.1-flash", "opencode", "Commit on the branch."); err != nil {
		t.Fatal(err)
	}
	n, ok, err := s.PlaybookFor("deepseek-v4.1-flash", "opencode")
	if err != nil || !ok || n.Note != "Commit on the branch." || n.Harness != "opencode" {
		t.Fatalf("exact harness: %+v %v %v", n, ok, err)
	}
	n, ok, _ = s.PlaybookFor("deepseek-v4.1-flash", "pi")
	if !ok || n.Note != "Ask for a checklist." || n.Harness != "" {
		t.Errorf("fallback to the model-wide note: %+v %v", n, ok)
	}
	if _, ok, _ := s.PlaybookFor("unknown", ""); ok {
		t.Error("an unknown model has a note")
	}
	// An empty note removes the entry. Removing the model-wide note leaves the
	// harness note; removing that leaves nothing.
	if err := s.SetPlaybook("deepseek-v4.1-flash", "", ""); err != nil {
		t.Fatal(err)
	}
	if n, ok, _ := s.PlaybookFor("deepseek-v4.1-flash", "opencode"); !ok || n.Note != "Commit on the branch." {
		t.Errorf("removing the model-wide note changed the harness note: %+v %v", n, ok)
	}
	if _, ok, _ := s.PlaybookFor("deepseek-v4.1-flash", "pi"); ok {
		t.Error("the model-wide note was not removed")
	}
	if err := s.SetPlaybook("deepseek-v4.1-flash", "opencode", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.PlaybookFor("deepseek-v4.1-flash", "opencode"); ok {
		t.Error("the harness note was not removed")
	}
	if err := s.SetPlaybook("m", "", strings.Repeat("x", protocol.PlaybookNoteMaxBytes+1)); err == nil {
		t.Error("an oversized note was accepted")
	}
}
