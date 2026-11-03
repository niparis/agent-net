package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// RecordWorkerAssessment stores an immutable result, idempotently per run.
// started_at is Unix nanoseconds, the judge's clock when it was asked, so it
// is usually a few seconds before the run's own Unix-second start.
func (s *Store) RecordWorkerAssessment(a protocol.WorkerAssessment) error {
	reasons, err := json.Marshal(a.Reasons)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO worker_assessments
 (run_id,session_id,started_at,assessed_at,reasoning,scope,coupling,uncertainty,verification,risk,d,label,bottleneck,confidence,rules_version,judge_harness,judge_model,reasons,picker_label,picker_note,error)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.RunID, a.SessionID, a.StartedAt, a.AssessedAt, a.Reasoning, a.Scope, a.Coupling, a.Uncertainty, a.Verification, a.Risk, a.D, a.Label, a.Bottleneck, a.Confidence, a.RulesVersion, a.JudgeHarness, a.JudgeModel, string(reasons), a.PickerLabel, a.PickerNote, a.Error)
	return err
}

// RecordWorkerStartAssessment stores the judge result chosen at a start,
// together with the picker's explanation for the model. The machine's daemon
// sends the same result later; the run id makes that write a no-op. The update
// below keeps the note when the machine's write landed first.
func (s *Store) RecordWorkerStartAssessment(a protocol.WorkerAssessment) error {
	if err := s.RecordWorkerAssessment(a); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE worker_assessments SET picker_note=? WHERE run_id=?`, a.PickerNote, a.RunID)
	return err
}

func (s *Store) WorkerAssessment(session string) (*protocol.WorkerAssessment, error) {
	var a protocol.WorkerAssessment
	var reasons string
	err := s.db.QueryRow(`SELECT run_id,session_id,started_at,assessed_at,reasoning,scope,coupling,uncertainty,verification,risk,d,label,bottleneck,confidence,rules_version,judge_harness,judge_model,reasons,picker_label,picker_note,error FROM worker_assessments WHERE session_id=? ORDER BY started_at DESC,rowid DESC LIMIT 1`, session).Scan(&a.RunID, &a.SessionID, &a.StartedAt, &a.AssessedAt, &a.Reasoning, &a.Scope, &a.Coupling, &a.Uncertainty, &a.Verification, &a.Risk, &a.D, &a.Label, &a.Bottleneck, &a.Confidence, &a.RulesVersion, &a.JudgeHarness, &a.JudgeModel, &reasons, &a.PickerLabel, &a.PickerNote, &a.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(reasons), &a.Reasons); err != nil {
		return nil, err
	}
	return &a, nil
}
