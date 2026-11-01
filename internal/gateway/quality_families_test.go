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
