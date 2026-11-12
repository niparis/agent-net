package gateway

import (
	"database/sql"

	"github.com/niparis/agent-net/internal/protocol"
)

// Recorded causes of failure and rework (decision 0141). Every write is from a
// signal agent-net already has: the stop reason and provider error text of a
// run, a failed test or accept step, or the caller's own brief defect. An
// unknown cause stays "unknown" and never blocks anything.

// causeOrUnknown is the stored value of a cause. A run that ends without a
// known signal stores "unknown", so a later count does not have to guess
// between a missing value and an unknown one.
func causeOrUnknown(c string) string {
	if c == "" {
		return protocol.CauseUnknown
	}
	return c
}

// setWorkerRunCauseTx fills the cause of a session's latest run when it has no
// specific one yet. A known cause is never replaced: the first signal that
// explains the rework keeps it.
func setWorkerRunCauseTx(tx *sql.Tx, session, cause string) error {
	if session == "" || cause == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE worker_runs SET cause=? WHERE id=(
		SELECT id FROM worker_runs WHERE session_id=? ORDER BY id DESC LIMIT 1)
		AND (cause='' OR cause='unknown')`, cause, session)
	return err
}

// setWorkerRunCause is setWorkerRunCauseTx outside a transaction.
func (s *Store) setWorkerRunCause(session, cause string) error {
	if session == "" || cause == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_runs SET cause=? WHERE id=(
		SELECT id FROM worker_runs WHERE session_id=? ORDER BY id DESC LIMIT 1)
		AND (cause='' OR cause='unknown')`, cause, session)
	return err
}

// noteReworkCauseTx records what made a change's origin worker need rework: a
// failed test step is a test failure in the merger's suite, a failed accept
// step is a finding by the merger. Other steps and passes record nothing.
func (s *Store) noteReworkCauseTx(tx *sql.Tx, c protocol.Change, e protocol.Evidence) error {
	if e.Result != protocol.StepResultFail || c.Origin.Kind != protocol.OriginWorker || c.Origin.SessionID == "" {
		return nil
	}
	cause := ""
	switch e.Step {
	case protocol.StepTest:
		cause = protocol.CauseTestFailure
	case protocol.StepAccept:
		cause = protocol.CauseAcceptFinding
	}
	return setWorkerRunCauseTx(tx, c.Origin.SessionID, cause)
}

// applyWorkerCauses puts each worker's latest recorded cause on its row, so
// agent_workers_find detail full can show why the last run failed or needed
// rework. It reads one query for all workers in the list.
func (s *Store) applyWorkerCauses(agents []protocol.AgentView) error {
	var sessions []any
	var marks string
	for i := range agents {
		if agents[i].Worker == nil {
			continue
		}
		sessions = append(sessions, agents[i].SessionID)
	}
	if len(sessions) == 0 {
		return nil
	}
	marks = "?"
	for i := 1; i < len(sessions); i++ {
		marks += ",?"
	}
	rows, err := s.db.Query(`SELECT session_id, cause FROM (
		SELECT session_id, cause, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY id DESC) AS rn
		FROM worker_runs WHERE session_id IN (`+marks+`)) WHERE rn=1`, sessions...)
	if err != nil {
		return err
	}
	defer rows.Close()
	causes := map[string]string{}
	for rows.Next() {
		var session, cause string
		if err := rows.Scan(&session, &cause); err != nil {
			return err
		}
		causes[session] = cause
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range agents {
		if agents[i].Worker == nil {
			continue
		}
		if cause := causes[agents[i].SessionID]; cause != "" {
			agents[i].Worker.Cause = cause
		}
	}
	return nil
}
