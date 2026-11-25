package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// RecordWorkerAssessment stores an immutable result, idempotently per run. The
// pick rows the result carries, and what the pick read of the quota windows,
// are stored in the same transaction.
// started_at is Unix nanoseconds, the judge's clock when it was asked, so it
// is usually a few seconds before the run's own Unix-second start.
func (s *Store) RecordWorkerAssessment(a protocol.WorkerAssessment) error {
	return s.recordWorkerAssessment(a, false)
}

// RecordWorkerStartAssessment stores the judge result chosen at a start,
// together with the picker's explanation for the model and the pick's rows. The
// machine's daemon sends the same result later; the run id makes that write a
// no-op. The update keeps the note when the machine's write landed first.
func (s *Store) RecordWorkerStartAssessment(a protocol.WorkerAssessment) error {
	return s.recordWorkerAssessment(a, true)
}

// recordWorkerAssessment writes one judge result and its pick rows in one
// transaction. startNote also sets the picker note of an existing row.
func (s *Store) recordWorkerAssessment(a protocol.WorkerAssessment, startNote bool) error {
	reasons, err := json.Marshal(a.Reasons)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR IGNORE INTO worker_assessments
 (run_id,session_id,started_at,assessed_at,reasoning,scope,coupling,uncertainty,verification,risk,d,label,bottleneck,confidence,rules_version,judge_harness,judge_model,reasons,picker_label,picker_note,error)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.RunID, a.SessionID, a.StartedAt, a.AssessedAt, a.Reasoning, a.Scope, a.Coupling, a.Uncertainty, a.Verification, a.Risk, a.D, a.Label, a.Bottleneck, a.Confidence, a.RulesVersion, a.JudgeHarness, a.JudgeModel, string(reasons), a.PickerLabel, a.PickerNote, a.Error)
	if err != nil {
		return err
	}
	if startNote {
		if _, err := tx.Exec(`UPDATE worker_assessments SET picker_note=? WHERE run_id=?`, a.PickerNote, a.RunID); err != nil {
			return err
		}
	}
	if err := insertPickRows(tx, a.RunID, a.PickRows); err != nil {
		return err
	}
	if err := insertPickQuota(tx, a.RunID, a.PickRows, a.PickQuota); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// A judge result that arrives after its run (the daemon sends it again when
	// the gateway was unreachable) gives that task its D.
	if n, _ := res.RowsAffected(); n > 0 && a.D > 0 {
		s.refreshPickerLogged()
	}
	return nil
}

// pickRowColumns are the columns of pick_candidates, in the order insertPickRows
// writes them.
const pickRowColumns = `run_id,pos,name,harness,provider,model,effort,rule,bucket_lo,bucket_hi,fallback,` +
	`cell_score,cell_low,cell_high,cell_n,is_allowed,is_qualified,is_prior,is_best,is_chosen,is_winner,left_out_reason,` +
	`cost_known,cost_recorded,price_unknown,cost_per_run,p_clean,rescue_cost,expected_cost,` +
	`pace_room,pace_factor,adjusted_cost,weight,probability,skipped_reason,explore,` +
	`explore_credit_before,explore_credit_after,margin`

// insertPickRows stores the rows of one pick, one per candidate in roster
// order. The run id and the position make a row unique, so a repeat write of
// the same pick is a no-op. A level pick reads no cell and no cost, so those
// columns are NULL for it.
func insertPickRows(tx *sql.Tx, runID string, rows []protocol.PickCandidate) error {
	if len(rows) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", strings.Count(pickRowColumns, ",")+1), ",")
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO pick_candidates (` + pickRowColumns + `) VALUES (` + placeholders + `)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for pos, r := range rows {
		level := r.Rule == protocol.PickRuleLevel
		// nullIf is NULL for a level pick, and the value otherwise.
		nullIf := func(v any) any {
			if level {
				return nil
			}
			return v
		}
		expected, adjusted := r.ExpectedCost, r.AdjustedCost
		// SQLite stores infinity as a real, preserving an unbounded result without a schema change.
		if r.CostUnbounded {
			expected, adjusted = math.Inf(1), math.Inf(1)
		}
		if _, err := stmt.Exec(runID, pos, r.Name, r.Harness, r.Provider, r.Model, r.Effort, r.Rule, r.BucketLo, r.BucketHi, r.Fallback,
			nullIf(r.Cell.Score), nullIf(r.Cell.Lo), nullIf(r.Cell.Hi), nullIf(r.Cell.N),
			boolInt(r.Allowed), boolInt(r.Qualified), boolInt(r.Prior), boolInt(r.Best), boolInt(r.Chosen), boolInt(r.Winner), r.LeftOutReason,
			nullIf(boolInt(r.CostKnown)), nullIf(boolInt(r.CostRecorded)), nullIf(boolInt(r.PriceUnknown)),
			nullIf(r.CostPerRun), nullIf(r.PClean), nullIf(r.RescueCost), nullIf(expected),
			optionalFloat(r.PaceRoom), r.PaceFactor, nullIf(adjusted), r.Weight, r.Probability, r.SkippedReason, boolInt(r.Explore),
			optionalFloat(r.ExploreCreditBefore), optionalFloat(r.ExploreCreditAfter), optionalFloat(r.Margin)); err != nil {
			return err
		}
	}
	return nil
}

// optionalFloat is the value of a float the picker may not have read, or NULL.
func optionalFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
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
