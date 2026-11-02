package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
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

// The cards list the most used models first, so each row counts the tasks
// that ended in the last 7 days next to the 30-day total.
func TestQualityFamilyCountsTasksThisWeek(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	now := s.now()
	for i, ended := range []time.Time{now, now.Add(-2 * 24 * time.Hour), now.Add(-10 * 24 * time.Hour)} {
		run := protocol.WorkerRun{NodeID: node, SessionID: fmt.Sprintf("s%d", i), Caller: "caller", Harness: "codex", Model: "gpt-6.1-sol", Difficulty: "normal", Outcome: protocol.OutcomeComplete, EndedAt: ended.Unix()}
		if err := s.RecordWorkerRun(run); err != nil {
			t.Fatal(err)
		}
	}
	levels, _, _, err := s.qualityFamilyTables()
	if err != nil {
		t.Fatal(err)
	}
	f := qualityFamily(t, qualityLevel(t, levels, "normal").Families, "sol")
	if f.Tasks != 3 || f.TasksWeek != 2 {
		t.Fatalf("tasks %d, this week %d; want 3 and 2", f.Tasks, f.TasksWeek)
	}
}

// Quality details list each model with its effort: terra high and terra max
// are different choices for the picker, so they are different rows.
func TestQualityModelsSplitByEffort(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	now := s.now().Unix()
	for i, model := range []string{"gpt-6.1-terra:high", "gpt-6.1-terra:high", "gpt-6.1-terra:max", "opencode-go/deepseek-v4.1-flash"} {
		run := protocol.WorkerRun{NodeID: node, SessionID: fmt.Sprintf("s%d", i), Caller: "caller", Harness: "codex", Model: model, Difficulty: "hard", Outcome: protocol.OutcomeComplete, EndedAt: now}
		if err := s.RecordWorkerRun(run); err != nil {
			t.Fatal(err)
		}
	}
	levels, _, _, err := s.qualityFamilyTables()
	if err != nil {
		t.Fatal(err)
	}
	hard := qualityLevel(t, levels, "hard")
	got := map[string]int{}
	for _, m := range hard.Models {
		got[m.Family] = m.Tasks
	}
	if got["terra high"] != 2 || got["terra max"] != 1 || got["deepseek"] != 1 || len(got) != 3 {
		t.Fatalf("models: %v", got)
	}
	if f := qualityFamily(t, hard.Families, "terra"); f.Tasks != 3 {
		t.Fatalf("the terra family has %d tasks, want 3", f.Tasks)
	}
}
