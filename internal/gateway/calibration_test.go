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
	// The row is about caller attribution, so its counters count as known: a
	// run from before the counters shipped is covered by
	// TestCalibrationLeavesOutUnknownCounters.
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, ended_at, counters_known)
		VALUES (?,?,?,?,?,?,1)`, node, wid, "opencode", "m", protocol.OutcomeComplete, s.now().Unix()); err != nil {
		t.Fatal(err)
	}
	// Replay the calibration migration and every migration appended after it.
	// New migrations always go at the end, so the calibration one is no longer
	// necessarily the last.
	calibration := len(migrations)
	for i, m := range migrations {
		if strings.Contains(m, "worker calibration columns") {
			calibration = i + 1
			break
		}
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version >= ?`, calibration); err != nil {
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

// The calibration table is built on every nodes call, which every open TUI
// makes once a second. It must look at the callers in the window only, not at
// every session the gateway ever saw (v0.4.49 scored all of them and kept the
// gateway busy).
func TestCalibrationLooksUpOnlyCallersInTheWindow(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "caller",
		Name: "agent-net", Model: "claude-opus-5-5", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "other" + string(rune('a'+i)),
			Name: "other", Model: "claude-opus-5-5", Status: "idle"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_run", Harness: "opencode", Model: "m",
		Outcome: protocol.OutcomeComplete, EndedAt: s.now().Unix(), Caller: caller, Difficulty: "normal"}); err != nil {
		t.Fatal(err)
	}
	scores, err := s.scores()
	if err != nil {
		t.Fatal(err)
	}
	names, models, strong, err := s.callerFacts(scores, []string{caller})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || len(models) != 1 || names[caller] != "agent-net" || !strong[caller] {
		t.Fatalf("callerFacts read %d sessions (names %v, strong %v), want only the caller", len(names), names, strong)
	}
	if names, _, _, err := s.callerFacts(scores, nil); err != nil || len(names) != 0 {
		t.Fatalf("no callers: got %d names, err %v", len(names), err)
	}
}

// A task is one worker session (one agent_start), not one row per turn. The
// original run and the follow-up runs of the same session make one sample,
// with the rework of all of them. Follow-up turns never count as new tasks.
func TestCalibrationCountsOneTaskPerWorkerSession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "caller",
		Name: "agent-net", Model: "claude-opus-5-5", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	for i := 0; i < 5; i++ {
		sid := "s_task" + string(rune('a'+i))
		// The original run needed no rework.
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: sid,
			Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
			EndedAt: now.Add(-time.Duration(10-i) * time.Minute).Unix(),
			Caller:  caller, Difficulty: "hard"}); err != nil {
			t.Fatal(err)
		}
		// The caller sent one message after a complete hand-back: the follow-up
		// turn carries the fix. Its caller and difficulty may be empty now.
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: sid,
			Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
			EndedAt: now.Add(-time.Duration(10-i)*time.Minute + 30*time.Second).Unix(),
			Fix:     1}); err != nil {
			t.Fatal(err)
		}
	}

	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Callers) != 1 || len(cal.Callers[0].Levels) != 1 {
		t.Fatalf("callers: %+v", cal.Callers)
	}
	l := cal.Callers[0].Levels[0]
	if l.Difficulty != "hard" {
		t.Errorf("difficulty from the first row: %q", l.Difficulty)
	}
	if l.Tasks != 5 || l.Median != 1 {
		t.Fatalf("one sample per session, each with rework 1: %+v", l)
	}
	if l.Action != protocol.CalibrationKeep || l.Offset != 0 {
		t.Errorf("action for median 1: %+v", l)
	}
}

// Runs recorded before the counters shipped read 0 rework, which is not the
// same as no rework. A session with an unknown run stays out of the medians.
func TestCalibrationLeavesOutUnknownCounters(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "caller",
		Name: "agent-net", Model: "claude-opus-5-5", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	for i := 0; i < 5; i++ {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_known" + string(rune('a'+i)),
			Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
			EndedAt: now.Add(-time.Duration(10-i) * time.Minute).Unix(),
			Caller:  caller, Difficulty: "hard"}); err != nil {
			t.Fatal(err)
		}
	}
	// A row from before the counters existed: the column keeps its default 0.
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, started_at, ended_at, caller, difficulty)
		VALUES (?,?,?,?,?,?,?,?,?)`, node, "s_old", "opencode", "m", protocol.OutcomeComplete,
		now.Add(-20*time.Minute).Unix(), now.Add(-15*time.Minute).Unix(), caller, "hard"); err != nil {
		t.Fatal(err)
	}
	// A later turn of that same session is recorded now, but the session's
	// rework is still unknown.
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_old",
		Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
		EndedAt: now.Add(-14 * time.Minute).Unix(), Caller: caller, Difficulty: "hard", Fix: 3}); err != nil {
		t.Fatal(err)
	}

	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Callers) != 1 || len(cal.Callers[0].Levels) != 1 {
		t.Fatalf("callers: %+v", cal.Callers)
	}
	l := cal.Callers[0].Levels[0]
	if l.Tasks != 5 || l.Median != 0 {
		t.Fatalf("the unknown session must not count: %+v", l)
	}
}

// A task stopped by the owner or the caller is no verdict on the model. It is
// not a clean task either: it stays out of the samples.
func TestCalibrationLeavesOutStoppedTasks(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "caller",
		Name: "agent-net", Model: "claude-opus-5-5", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	for i := 0; i < 5; i++ {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_done" + string(rune('a'+i)),
			Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
			EndedAt: now.Add(-time.Duration(10-i) * time.Minute).Unix(),
			Caller:  caller, Difficulty: "hard"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_stop" + string(rune('a'+i)),
			Harness: "opencode", Model: "m", Outcome: protocol.OutcomeStopped,
			EndedAt: now.Add(-time.Duration(3-i) * time.Minute).Unix(),
			Caller:  caller, Difficulty: "hard", Fix: 5}); err != nil {
			t.Fatal(err)
		}
	}

	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Callers) != 1 || len(cal.Callers[0].Levels) != 1 {
		t.Fatalf("callers: %+v", cal.Callers)
	}
	if l := cal.Callers[0].Levels[0]; l.Tasks != 5 || l.Median != 0 {
		t.Fatalf("stopped tasks must not count: %+v", l)
	}
}

// A stopped run adds no rework to its session either: the task keeps the
// counters of its complete, partial and failed runs.
func TestCalibrationStoppedRunAddsNoRework(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "caller",
		Name: "agent-net", Model: "claude-opus-5-5", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_mixed",
		Harness: "opencode", Model: "m", Outcome: protocol.OutcomeComplete,
		EndedAt: now.Add(-2 * time.Minute).Unix(), Caller: caller, Difficulty: "hard"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_mixed",
		Harness: "opencode", Model: "m", Outcome: protocol.OutcomeStopped,
		EndedAt: now.Add(-time.Minute).Unix(), Caller: caller, Difficulty: "hard", Fix: 5}); err != nil {
		t.Fatal(err)
	}

	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Callers) != 1 || len(cal.Callers[0].Levels) != 1 {
		t.Fatalf("callers: %+v", cal.Callers)
	}
	if l := cal.Callers[0].Levels[0]; l.Tasks != 1 || l.Median != 0 {
		t.Fatalf("the stopped run added rework: %+v", l)
	}
}
