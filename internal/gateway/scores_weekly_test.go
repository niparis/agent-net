package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestScoresWeeklyEmptyWeeksShowZeros(t *testing.T) {
	s, _ := newTestStore(t)
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	rows, err := s.ScoresWeeklyAt(now)
	if err != nil {
		t.Fatalf("ScoresWeeklyAt failed: %v", err)
	}
	if len(rows) != 8 {
		t.Fatalf("want 8 weekly rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Runs != 0 || r.MeanScore != 0 || r.EarlyQuitRate != 0 || r.MeanFixRounds != 0 ||
			r.BriefDefects != 0 || r.Defects != 0 || r.MeanTokens != 0 || len(r.Releases) != 0 {
			t.Errorf("row %d (%s) not zero: %+v", i, r.Week, r)
		}
	}
}

func TestScoresWeeklyAggregateAcrossWeekBoundary(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("host", "linux")

	// Boundary between Sunday 2026-11-08 23:59:59 (W45) and Monday 2026-11-09 00:00:00 (W46).
	tSun := time.Date(2026, 11, 8, 23, 59, 50, 0, time.UTC)
	tMon := time.Date(2026, 11, 9, 0, 0, 10, 0, time.UTC)

	// Session 1 in W45 with 1 fix round, 1200 tokens.
	s1Worker := protocol.WorkerInfo{ID: "w1", Owner: "owner", Tokens: 1200}
	s1Raw, _ := json.Marshal(s1Worker)
	if _, err := s.db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, last_seen, worker) VALUES ('s1', ?, 'codex', 'w1', ?, ?)`,
		node, tSun.Unix(), string(s1Raw)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWorkerRun(protocol.WorkerRun{
		NodeID: node, SessionID: "s1", Harness: "codex", Model: "gpt-6.1-sol:low",
		Outcome: protocol.OutcomeComplete, Fix: 1, StartedAt: tSun.Unix() - 60, EndedAt: tSun.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	// Session 2 in W46 with early quit, 2400 tokens.
	s2Worker := protocol.WorkerInfo{ID: "w2", Owner: "owner", Tokens: 2400}
	s2Raw, _ := json.Marshal(s2Worker)
	if _, err := s.db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, last_seen, worker) VALUES ('s2', ?, 'codex', 'w2', ?, ?)`,
		node, tMon.Unix(), string(s2Raw)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWorkerRun(protocol.WorkerRun{
		NodeID: node, SessionID: "s2", Harness: "codex", Model: "gpt-6.1-sol:low",
		Outcome: protocol.OutcomeComplete, Evidence: &protocol.WorkerEvidence{EarlyQuit: true}, StartedAt: tMon.Unix() - 60, EndedAt: tMon.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	refTime := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	rows, err := s.ScoresWeeklyAt(refTime)
	if err != nil {
		t.Fatalf("ScoresWeeklyAt failed: %v", err)
	}
	if len(rows) != 8 {
		t.Fatalf("want 8 rows, got %d", len(rows))
	}

	// W45 is rows[6], W46 is rows[7].
	w45, w46 := rows[6], rows[7]
	if w45.Week != "2026-W45" {
		t.Errorf("w45 week = %q, want 2026-W45", w45.Week)
	}
	if w45.Runs != 1 {
		t.Errorf("w45 runs = %d, want 1", w45.Runs)
	}
	if w45.MeanScore != 0.75 { // 1.0 - 0.25*1 = 0.75
		t.Errorf("w45 mean_score = %v, want 0.75", w45.MeanScore)
	}
	if w45.MeanFixRounds != 1.0 {
		t.Errorf("w45 mean_fix_rounds = %v, want 1.0", w45.MeanFixRounds)
	}
	if w45.MeanTokens != 1200 {
		t.Errorf("w45 mean_tokens = %d, want 1200", w45.MeanTokens)
	}

	if w46.Week != "2026-W46" {
		t.Errorf("w46 week = %q, want 2026-W46", w46.Week)
	}
	if w46.Runs != 1 {
		t.Errorf("w46 runs = %d, want 1", w46.Runs)
	}
	if w46.MeanScore != 1.0 {
		t.Errorf("w46 mean_score = %v, want 1.0", w46.MeanScore)
	}
	if w46.EarlyQuitRate != 1.0 {
		t.Errorf("w46 early_quit_rate = %v, want 1.0", w46.EarlyQuitRate)
	}
	if w46.MeanTokens != 2400 {
		t.Errorf("w46 mean_tokens = %d, want 2400", w46.MeanTokens)
	}
}

func TestScoresWeeklyReleaseTagsLandInRightWeek(t *testing.T) {
	s, _ := newTestStore(t)

	tSun := time.Date(2026, 11, 8, 23, 59, 50, 0, time.UTC).Unix()
	tMon := time.Date(2026, 11, 9, 0, 0, 10, 0, time.UTC).Unix()

	// Release tag v0.5.21 in W45
	if _, err := s.db.Exec(`INSERT INTO delivery_evidence (change_id, revision, step, result, commit_hash, fields, by_session, at)
		VALUES ('c1', 1, 'release', 'pass', 'hash1', '{"tag":"v0.5.21"}', 's1', ?)`, tSun); err != nil {
		t.Fatal(err)
	}
	// Release tag v0.5.22 in W46
	if _, err := s.db.Exec(`INSERT INTO delivery_evidence (change_id, revision, step, result, commit_hash, fields, by_session, at)
		VALUES ('c2', 1, 'release', 'pass', 'hash2', '{"tag":"v0.5.22"}', 's2', ?)`, tMon); err != nil {
		t.Fatal(err)
	}

	// Brief defect in W46
	if _, err := s.db.Exec(`INSERT INTO worker_brief_defects (worker_session_id, caller, severity, at) VALUES ('s2', 'owner', 2, ?)`, tMon); err != nil {
		t.Fatal(err)
	}

	// Late defect in W46
	if _, err := s.db.Exec(`INSERT INTO worker_defects (session_id, severity, at, moved_at) VALUES ('s2', 1, ?, 0)`, tMon); err != nil {
		t.Fatal(err)
	}

	refTime := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	rows, err := s.ScoresWeeklyAt(refTime)
	if err != nil {
		t.Fatalf("ScoresWeeklyAt failed: %v", err)
	}
	if len(rows) != 8 {
		t.Fatalf("want 8 rows, got %d", len(rows))
	}

	w45, w46 := rows[6], rows[7]
	if len(w45.Releases) != 1 || w45.Releases[0] != "v0.5.21" {
		t.Errorf("w45 releases = %v, want [v0.5.21]", w45.Releases)
	}
	if len(w46.Releases) != 1 || w46.Releases[0] != "v0.5.22" {
		t.Errorf("w46 releases = %v, want [v0.5.22]", w46.Releases)
	}
	if w46.BriefDefects != 1 {
		t.Errorf("w46 brief_defects = %d, want 1", w46.BriefDefects)
	}
	if w46.Defects != 1 {
		t.Errorf("w46 defects = %d, want 1", w46.Defects)
	}
}
