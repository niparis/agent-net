package gateway

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCalibrationReadsCallerAndDifficulty(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "caller",
		Name: "agent-net", Model: "claude-opus-5-5", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	// Five hard tasks with no rework: a strong caller would step down.
	for i := 0; i < 5; i++ {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_h" + string(rune('a'+i)),
			Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
			EndedAt: now.Add(-time.Duration(5-i) * time.Minute).Unix(),
			Caller:  caller, Difficulty: "hard"}); err != nil {
			t.Fatal(err)
		}
	}
	// Three older tasks with heavy rework: the oldest falls outside the
	// seven-task window, the two newer ones stay and cannot move the median.
	for i := 0; i < 3; i++ {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_old" + string(rune('a'+i)),
			Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
			EndedAt: now.Add(-time.Duration(20-i) * time.Minute).Unix(),
			Caller:  caller, Difficulty: "hard", Fix: 5}); err != nil {
			t.Fatal(err)
		}
	}
	// A run with no caller cannot be attributed and is left out.
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_nobody",
		Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete, EndedAt: now.Unix(), Fix: 9}); err != nil {
		t.Fatal(err)
	}

	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Callers) != 1 {
		t.Fatalf("callers: %+v", cal.Callers)
	}
	c := cal.Callers[0]
	if c.Caller != caller || c.Name != "agent-net" || c.Model != "claude-opus-5-5" || !c.Strong {
		t.Fatalf("caller: %+v", c)
	}
	if len(c.Levels) != 1 || c.Levels[0].Difficulty != "hard" {
		t.Fatalf("levels: %+v", c.Levels)
	}
	l := c.Levels[0]
	if l.Tasks != 7 || l.Median != 0 || l.Action != protocol.CalibrationLower || l.Offset != -1 {
		t.Fatalf("hard level: %+v", l)
	}
}

// A run recorded before part 2 can be attributed from its worker session
// while that session is still kept.
func TestCalibrationMigrationBackfillsCaller(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "caller", Name: "agent-net"})
	wid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "w",
		Worker: &protocol.WorkerInfo{ID: "w", Owner: caller, DifficultyAsked: "hard"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, ended_at)
		VALUES (?,?,?,?,?,?)`, node, wid, "opencode", "m", protocol.OutcomeComplete, s.now().Unix()); err != nil {
		t.Fatal(err)
	}
	backfill := 0
	for i, m := range migrations {
		if strings.Contains(m, "worker calibration columns") {
			backfill = i + 1
		}
	}
	if backfill == 0 {
		t.Fatal("the calibration migration is missing")
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version >= ?`, backfill); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Callers) != 1 || len(cal.Callers[0].Levels) != 1 {
		t.Fatalf("backfilled calibration: %+v", cal)
	}
	l := cal.Callers[0].Levels[0]
	if cal.Callers[0].Caller != caller || l.Difficulty != "hard" || l.Tasks != 1 || l.Action != protocol.CalibrationNotEnough {
		t.Fatalf("backfilled row: %+v %+v", cal.Callers[0], l)
	}
}

func TestWorkerSessionOwner(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	ownerID, _ := s.UpsertSession(node, owner("mac"))
	wid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "w",
		Worker: &protocol.WorkerInfo{ID: "w", Owner: ownerID}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkerSessionOwner(wid)
	if err != nil || got != ownerID {
		t.Fatalf("owner = %q, %v", got, err)
	}
	if got, err := s.WorkerSessionOwner("s_missing"); err != nil || got != "" {
		t.Fatalf("missing session = %q, %v", got, err)
	}
}

// Part 2 is read only: reading the calibration must not change the rotation
// state or the model a start would pick.
func TestCalibrationChangesNoChooserBehavior(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "caller",
		Name: "agent-net", Model: "claude-opus-5-5", Status: "idle"})
	for i := 0; i < 5; i++ {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_c" + string(rune('a'+i)),
			Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
			EndedAt: s.now().Add(-time.Duration(i+1) * time.Minute).Unix(), Caller: caller, Difficulty: "hard"}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.WorkerRotation()
	if err != nil {
		t.Fatal(err)
	}
	candidates := []protocol.WorkerModelCandidate{
		{Name: "a", Harness: "codex", Model: "a"},
		{Name: "b", Harness: "codex", Model: "b"},
	}
	scores := []protocol.ModelScore{{Match: "a", Score: 50}, {Match: "b", Score: 50}}
	first := protocol.WorkerRotation{}
	firstPick, err := protocol.ChooseWorkerModel(candidates, "normal", scores, nil, nil, nil, &first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Calibration(); err != nil {
		t.Fatal(err)
	}
	after, err := s.WorkerRotation()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("calibration changed the rotation: %+v -> %+v", before, after)
	}
	second := protocol.WorkerRotation{}
	secondPick, err := protocol.ChooseWorkerModel(candidates, "normal", scores, nil, nil, nil, &second)
	if err != nil {
		t.Fatal(err)
	}
	if firstPick.Model != secondPick.Model {
		t.Fatalf("calibration changed the pick: %s -> %s", firstPick.Model, secondPick.Model)
	}
}
