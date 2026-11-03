package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerLogNewestRunningAndAssessment(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "run", Model: "test:high", Worker: &protocol.WorkerInfo{ID: "w_test", StartedAt: 200, Title: "Check the log", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.WorkerAssessment{RunID: "r1", SessionID: sid, StartedAt: 200, Reasoning: 3, D: 2.1, Label: "normal", JudgeModel: "fake", Reasons: map[string]string{"reasoning": "check history"}}
	if err = s.RecordWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,model,outcome,started_at,ended_at) VALUES(?,?,?,?,?,?,?)`, node, "old", "codex", "test", "complete", 100, 150); err != nil {
		t.Fatal(err)
	}
	rows, err := s.WorkerLog(200)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Status != "running" || rows[0].Title != "Check the log" || rows[0].Assessment == nil || rows[0].Assessment.Reasoning != 3 || rows[1].Status != "done" || rows[1].Assessment != nil {
		t.Fatalf("rows: %+v", rows)
	}
	limited, err := s.WorkerLog(1)
	if err != nil || len(limited) != 1 {
		t.Fatalf("limit: %+v %v", limited, err)
	}
}

func TestWorkerLogMatchesAssessmentToStartAndReadsNoticeFacts(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "continued", Worker: &protocol.WorkerInfo{ID: "w_test", StartedAt: 300, Title: "Current title"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, start := range []int64{100, 200} {
		if _, err = s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,model,outcome,started_at,ended_at,run_seconds) VALUES(?,?,?,?,?,?,?,?)`, node, sid, "codex", "fake:high", "failed", start, start+20, 20); err != nil {
			t.Fatal(err)
		}
	}
	a := protocol.WorkerAssessment{RunID: "old-start", SessionID: sid, StartedAt: 100, Reasoning: 2, Scope: 3, Coupling: 4, Uncertainty: 1, Verification: 2, Risk: 3, D: 2.5, Label: "hard", JudgeModel: "fake/judge", Reasons: map[string]string{"scope": "stored reason"}, PickerLabel: "hard"}
	if err = s.RecordWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	body := "[agent-net worker notice]\nstatus: failed\nnote: fake provider unavailable\nworker: " + sid + " (codex, fake:high), project demo on test-machine · Original title\n" + protocol.ReportMarker + "\nnote: untrusted report\n"
	if _, err = s.db.Exec(`INSERT INTO messages(id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES('m_test','c_test',?,'s_owner','notification',?,0,'test',?,0,'sent')`, sid, body, int64(120)*1000000000); err != nil {
		t.Fatal(err)
	}
	rows, err := s.WorkerLog(200)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Status != "running" || rows[1].Assessment != nil || rows[1].Failure != "" || rows[2].Assessment == nil || rows[2].Assessment.Risk != 3 || rows[2].Assessment.PickerLabel != "hard" || rows[2].Failure != "fake provider unavailable" || rows[2].Title != "Original title" {
		t.Fatalf("runs: %+v", rows)
	}
}
