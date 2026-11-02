package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerAssessmentStoreAndMigrationReplay(t *testing.T) {
	s, _ := newTestStore(t)
	a := protocol.WorkerAssessment{RunID: "run1", SessionID: "session", AssessedAt: 123, Reasoning: 2, Scope: 1, Coupling: 1, Uncertainty: 1, Verification: 1, Risk: 1, D: 1.2, Label: "trivial", Bottleneck: "risk", Confidence: "high", RulesVersion: "2026-11-02.1", JudgeHarness: "opencode", JudgeModel: "fake/model", Reasons: map[string]string{"reasoning": "reason", "scope": "scope", "coupling": "coupling", "uncertainty": "uncertain", "verification": "tests", "risk": "risk"}, PickerLabel: "normal"}
	if err := s.RecordWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkerAssessment(a.SessionID)
	if err != nil || !reflect.DeepEqual(got, &a) {
		t.Fatalf("got %+v, %v", got, err)
	}
	a.RunID = "run2"
	a.Error = "judge failed"
	a.AssessedAt++
	if err := s.RecordWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_assessments`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows: %d, %v", n, err)
	}
	if _, err := s.db.Exec(migrations[len(migrations)-1]); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	got, err = s.WorkerAssessment(a.SessionID)
	if err != nil || !reflect.DeepEqual(got, &a) {
		t.Fatalf("latest: %+v, %v", got, err)
	}
}

func TestWorkerAssessmentRequiresWorkerNode(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("host1", "linux")
	other, _, _ := s.CreateNode("host2", "linux")
	wid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Worker: &protocol.WorkerInfo{ID: "w_test", Owner: "s_caller"}})
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	a := protocol.WorkerAssessment{RunID: "k_1", SessionID: wid}
	raw, _ := json.Marshal(a)
	if _, err := g.handleWorker(context.Background(), &nodeConn{id: other}, "worker.assessment", raw); err == nil {
		t.Fatal("another node stored an assessment")
	}
	a.SessionID = ordinary
	raw, _ = json.Marshal(a)
	if _, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.assessment", raw); err == nil {
		t.Fatal("ordinary session accepted")
	}
	a.SessionID = wid
	raw, _ = json.Marshal(a)
	if _, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.assessment", raw); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerAssessmentNoticeIsNotHandback(t *testing.T) {
	body := protocol.WorkerAssessmentNotice("s_test", protocol.WorkerAssessment{Label: "trivial"})
	if _, _, _, ok := storedReworkNotice(body); ok {
		t.Fatal("difficulty notice counted as a hand-back")
	}
	s, _ := newTestStore(t)
	var lifecycle bool
	if err := s.db.QueryRow(`SELECT `+workerNoticeLifecycleSQL("m")+` FROM (SELECT ? AS body) m`, body).Scan(&lifecycle); err != nil || !lifecycle {
		t.Fatalf("lifecycle: %v %v", lifecycle, err)
	}
}
