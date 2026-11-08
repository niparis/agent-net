package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// Defects that agent-net records by itself (decision 0103). The gateway knows
// each change's origin worker, so it does not wait for an agent to remember
// agent_worker_rate defect_found_later:
//
//   - a failed verify after merge: severity 3, or fields.severity of the report;
//   - a failed test before merge: severity 1, once per revision, never for a
//     failure the merger marks as a flake (fields.flake=yes);
//   - a fix of a shipped change (worker_start or agent_change_submit with
//     fixes): severity fixes_severity, default 2, once per fixed change.
//
// A failed accept or review is normal iteration and records nothing.

const (
	autoDefectVerify = "verify"
	autoDefectTest   = "test"
	autoDefectFix    = "fix"

	autoDefectVerifySeverity = 3
	autoDefectTestSeverity   = 1
	autoDefectFixSeverity    = 2

	// autoDefectManualWindow is how long a defect filed by hand hides an
	// automatic one for the same worker.
	autoDefectManualWindow = time.Hour
)

var autoDefectWhy = map[string]string{
	autoDefectVerify: "verify failed",
	autoDefectTest:   "test failed",
	autoDefectFix:    "fixed by a later change",
}

// autoDefect is one defect agent-net found in the delivery steps. Revision is
// 0 for a fix, which counts once per change.
type autoDefect struct {
	changeID string
	kind     string
	revision int
	worker   string
	severity int
	note     string
}

// addAutoDefect stores d in tx, unless the same failure is already recorded or
// the caller filed a defect for this worker by hand in the last hour. It
// writes the Desk log line in the same transaction. It reports whether it
// recorded a defect; the caller refreshes the quality data after the commit.
func (s *Store) addAutoDefect(tx *sql.Tx, d autoDefect) (bool, error) {
	worker := strings.TrimSpace(d.worker)
	if worker == "" || !protocol.ValidRateSeverity(d.severity) {
		return false, nil
	}
	var n int
	var err error
	if d.kind == autoDefectFix {
		err = tx.QueryRow(`SELECT COUNT(*) FROM worker_defect_auto WHERE change_id=?`, d.changeID).Scan(&n)
	} else {
		err = tx.QueryRow(`SELECT COUNT(*) FROM worker_defect_auto WHERE change_id=? AND kind=? AND revision=?`, d.changeID, d.kind, d.revision).Scan(&n)
	}
	if err != nil || n > 0 {
		return false, err
	}
	now := s.now()
	if err := tx.QueryRow(`SELECT COUNT(*) FROM worker_defects WHERE session_id=? AND at>=?
		AND id NOT IN (SELECT defect_id FROM worker_defect_auto)`, worker, now.Add(-autoDefectManualWindow).Unix()).Scan(&n); err != nil || n > 0 {
		return false, err
	}
	facts, ok, err := workerRatingFactsOn(tx, worker)
	if err != nil || !ok {
		return false, err
	}
	note := redact.Secrets(protocol.CleanRateNote(d.note))
	if note == "" {
		note = autoDefectWhy[d.kind]
	}
	res, err := tx.Exec(`INSERT INTO worker_defects
		(run_id, session_id, caller, rater_model, harness, model, effort, difficulty, severity, note, at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, facts.runID, worker, facts.caller, "", facts.harness,
		facts.model, facts.effort, facts.difficulty, d.severity, note, now.Unix())
	if err != nil {
		return false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO worker_defect_auto(defect_id, change_id, kind, revision, at) VALUES(?,?,?,?,?)`,
		id, d.changeID, d.kind, d.revision, now.Unix()); err != nil {
		return false, err
	}
	text := fmt.Sprintf("defect recorded: %s sev %d on %s (%s)", worker, d.severity, d.changeID, autoDefectWhy[d.kind])
	if err := s.deskLog(tx, "gateway", "change", d.changeID, text, note, nil); err != nil {
		return false, err
	}
	return true, nil
}

// autoDefectFromReport is the defect a step report makes, if any. c is the
// change as it stood when the report was accepted: its origin is the author of
// the revision the report is about.
func (s *Store) autoDefectFromReport(tx *sql.Tx, c protocol.Change, e protocol.Evidence) (bool, error) {
	if e.Result != protocol.StepResultFail || c.Origin.Kind != protocol.OriginWorker || c.Origin.SessionID == "" {
		return false, nil
	}
	d := autoDefect{changeID: c.ID, revision: e.Revision, worker: c.Origin.SessionID, note: e.Note}
	switch {
	case e.Step == protocol.StepVerify && e.AfterMerge:
		d.kind, d.severity = autoDefectVerify, autoDefectVerifySeverity
		if v, err := strconv.Atoi(e.Fields[protocol.FieldSeverity]); err == nil && protocol.ValidRateSeverity(v) {
			d.severity = v
		}
	case e.Step == protocol.StepTest && !e.AfterMerge:
		if e.Fields[protocol.FieldFlake] == protocol.FieldYes {
			return false, nil
		}
		d.kind, d.severity = autoDefectTest, autoDefectTestSeverity
	default:
		return false, nil
	}
	return s.addAutoDefect(tx, d)
}

// fixDefectTx records the defect for the worker of shipped change fixed. A
// change that is not merged yet is still being worked on: that is iteration,
// and it records nothing.
func (s *Store) fixDefectTx(tx *sql.Tx, fixed string, severity int) (bool, error) {
	c, ok, err := queryChange(tx, `id = ?`, fixed)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, reject("fixes: unknown change %q", fixed)
	}
	if c.State != protocol.ChangeMerged && c.State != protocol.ChangeDone {
		return false, nil
	}
	if severity == 0 {
		severity = autoDefectFixSeverity
	}
	return s.addAutoDefect(tx, autoDefect{changeID: c.ID, kind: autoDefectFix, worker: c.Origin.SessionID, severity: severity,
		note: "a later change fixes " + c.ID})
}

// RecordFixDefect records the defect of a fix: a new piece of work that says
// it fixes shipped change fixed. It reports whether it recorded one. A change
// that has a defect recorded already, or a hand-filed one for its worker in
// the last hour, gets none.
func (s *Store) RecordFixDefect(fixed string, severity int) (bool, error) {
	if err := protocol.ValidFixes(fixed, severity); err != nil {
		return false, reject("%s", err.Error())
	}
	if fixed == "" {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	recorded, err := s.fixDefectTx(tx, fixed, severity)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if recorded {
		if err := s.refreshQuality(); err != nil {
			return true, fmt.Errorf("refresh quality: %w", err)
		}
	}
	return recorded, nil
}

// refreshAfterAutoDefect refreshes the quality data and the picker table once
// a transaction that recorded an automatic defect has committed. A failure
// here is logged: the defect is stored, and the next write refreshes again.
func (s *Store) refreshAfterAutoDefect() {
	if err := s.refreshQuality(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logf("quality.refresh after an automatic defect: %v", err)
	}
}
