package gateway

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// All automatic notice paths use the same predicate. The store clock is bound
// as a SQL literal by stepNotWaitingAt, keeping tests and dispatch consistent.
const stepNotWaitingSQL = `(t.waiting_at=0 OR
 EXISTS(SELECT 1 FROM delivery_evidence e WHERE e.change_id=t.change_id AND e.at>t.waiting_at) OR
 (CASE WHEN t.waiting_until<>0 THEN t.waiting_until ELSE t.waiting_at END)+86400<=WAIT_NOW)`

func (s *Store) stepNotWaitingAt() string {
	return strings.ReplaceAll(stepNotWaitingSQL, "WAIT_NOW", strconv.FormatInt(s.now().Unix(), 10))
}

func (s *Store) stepWaiting(t nodeTaskTarget) (bool, string, error) {
	var reason string
	var at, until int64
	var active int
	err := s.db.QueryRow(`SELECT waiting_reason,waiting_at,waiting_until,NOT (`+s.stepNotWaitingAt()+`) FROM delivery_step_tasks t WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=0`, t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge)).Scan(&reason, &at, &until, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	note := "waiting on " + reason
	if until != 0 {
		note += " (until " + time.Unix(until, 0).UTC().Format(time.RFC3339) + ")"
	}
	return active != 0, note, nil
}

func (s *Store) wasWaiting(targets []nodeTaskTarget) string {
	var lines []string
	for _, t := range targets {
		var reason string
		if err := s.db.QueryRow(`SELECT waiting_reason FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`, t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge)).Scan(&reason); err == nil && reason != "" {
			lines = append(lines, "was waiting on: "+protocol.NeutralizeMarkers(reason))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(lines, "\n")
}

// Waiting checks claim eligibility without taking the claim or writing evidence.
func (s *Store) reportStepWaiting(machine string, q protocol.StepReportRequest) (protocol.StepReportResult, error) {
	c, ok, err := queryChange(s.db, `id=?`, q.ChangeID)
	if err != nil {
		return protocol.StepReportResult{}, err
	}
	if !ok {
		return protocol.StepReportResult{}, reject("unknown change %q", q.ChangeID)
	}
	p, err := s.deliveryPipeline(c.Project)
	if err != nil {
		return protocol.StepReportResult{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.StepReportResult{}, err
	}
	defer tx.Rollback()
	c, _, err = queryChange(tx, `id=?`, q.ChangeID)
	if err != nil {
		return protocol.StepReportResult{}, err
	}
	want := c.HeadCommit
	if q.AfterMerge {
		want = c.MergedCommit
	}
	if (q.AfterMerge && c.State != protocol.ChangeMerged) || (!q.AfterMerge && c.State != protocol.ChangeOpen) || q.Commit != want {
		return protocol.StepReportResult{}, reject("%s is not open on the requested commit", q.StepRef)
	}
	if err := checkApprovalTaskPolicy(tx, c, p, q.StepRef); err != nil {
		return protocol.StepReportResult{}, err
	}
	if err := checkStepReady(tx, c, p, q.StepRef); err != nil {
		return protocol.StepReportResult{}, err
	}
	found, err := checkNodeTask(tx, c, q.StepRef, q.SessionID, machine, false, false)
	if err != nil {
		return protocol.StepReportResult{}, err
	}
	if !found {
		return protocol.StepReportResult{}, reject("no ready node task for %s", q.StepRef)
	}
	var until int64
	if q.Until != "" {
		parsed, _ := time.Parse(time.RFC3339, q.Until)
		until = parsed.Unix()
	}
	if _, err := tx.Exec(`UPDATE delivery_step_tasks SET waiting_reason=?,waiting_at=?,waiting_until=? WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`, strings.TrimSpace(q.Note), s.now().Unix(), until, c.ID, c.Revision, q.Step, q.For, boolInt(q.AfterMerge)); err != nil {
		return protocol.StepReportResult{}, err
	}
	// Retire a queued notice too: a report may arrive during the batch window.
	if _, err := tx.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched') AND id IN (SELECT message_id FROM delivery_step_messages WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`, c.ID, c.Revision, q.Step, q.For, boolInt(q.AfterMerge)); err != nil {
		return protocol.StepReportResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.StepReportResult{}, err
	}
	v, err := s.changeView(c, p, false)
	return protocol.StepReportResult{Change: v}, err
}
