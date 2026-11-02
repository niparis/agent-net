package gateway

import (
	"context"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestTouchedAreaCountsEachTask(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	now := s.now().Unix()
	for _, run := range []protocol.WorkerRun{
		{NodeID: node, Harness: "opencode", SessionID: "root", Caller: "caller", Outcome: protocol.OutcomeComplete, EndedAt: now, Touches: []string{"ui", "tests"}, Fix: 1},
		{NodeID: node, Harness: "opencode", SessionID: "next", TaskID: "root", Caller: "caller", Outcome: protocol.OutcomeComplete, EndedAt: now, Touches: []string{"tests"}},
	} {
		if err := s.RecordWorkerRun(run); err != nil {
			t.Fatal(err)
		}
	}
	_, areas, agents, err := s.qualityFamilyTables()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range [][]protocol.QualityFamily{areas, agents[0].Areas} {
		for _, name := range []string{"ui", "tests"} {
			row := qualityFamily(t, table, name)
			if row.Tasks != 1 || row.Headache != 10 {
				t.Errorf("%s: %+v", name, row)
			}
		}
	}
}

func TestWorkerStartRequiresTouches(t *testing.T) {
	g := &Gateway{}
	_, err := g.workerStart(context.Background(), nil, protocol.WorkerStartFrame{WorkerStartRequest: protocol.WorkerStartRequest{Project: "p", Task: "t"}})
	if err == nil || !strings.Contains(err.Error(), protocol.WorkerTouchesError) {
		t.Fatalf("missing actionable refusal: %v", err)
	}
}

func TestQualityFamilyHelpWorstAndRating(t *testing.T) {
	counts := map[string]*familyCount{}
	addFamilyTask(counts, "sol", familyTask{fix: 2, cont: 1, worstSeverity: 4, ratings: []float64{3, 5}})
	addFamilyTask(counts, "sol", familyTask{})
	row := familyRows(counts)[0]
	if row.NeededHelp != 0.5 || row.WorstSeverity != 4 || row.AvgRating == nil || *row.AvgRating != 4 {
		t.Fatalf("help, severity and rating = %v, %v, %v", row.NeededHelp, row.WorstSeverity, row.AvgRating)
	}
}

func TestQualityFamilySnapshotMetrics(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	now := s.now().Unix()
	for _, run := range []protocol.WorkerRun{{NodeID: node, SessionID: "a", Caller: "caller", Harness: "codex", Model: "gpt-6.1-sol", Difficulty: "normal", Outcome: protocol.OutcomeComplete, EndedAt: now, Fix: 2, Continue: 1}, {NodeID: node, SessionID: "b", Caller: "caller", Harness: "codex", Model: "gpt-6.1-sol", Difficulty: "normal", Outcome: protocol.OutcomeComplete, EndedAt: now}} {
		if err := s.RecordWorkerRun(run); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{`INSERT INTO worker_defects (session_id,severity,at) VALUES ('a',2,?)`, `INSERT INTO worker_defects (session_id,severity,at) VALUES ('a',4,?)`, `INSERT INTO worker_ratings (session_id,score,at) VALUES ('a',5,?)`} {
		if _, err := s.db.Exec(q, now); err != nil {
			t.Fatal(err)
		}
	}
	levels, _, agents, err := s.qualityFamilyTables()
	if err != nil {
		t.Fatal(err)
	}
	for _, ls := range [][]protocol.QualityLevel{levels, agents[0].Levels} {
		f := qualityFamily(t, qualityLevel(t, ls, "normal").Families, "sol")
		if f.NeededHelp != .5 || f.WorstSeverity != 4 || f.AvgRating == nil || *f.AvgRating != 5 {
			t.Fatalf("metrics: %+v", f)
		}
	}
}
