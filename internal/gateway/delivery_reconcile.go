package gateway

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// externalMergeGrace is how long the gateway waits after the last test or
// merge report on a change before it records a merge that only git shows.
// The merger reports its own merge right after its push, and that report
// should be the one on record.
const externalMergeGrace = 10 * time.Minute

// ReconcileChange accepts git observations only from the machine holding the
// change. It bypasses pipeline steps without inventing test or release passes.
// An external merge leaves the change merged, and done only once its
// after-merge steps have passed or are left out. The transaction rejects
// observations made obsolete by a new revision/report.
func (s *Store) ReconcileChange(machine string, q protocol.ChangeReconcileRequest) (protocol.ChangeView, error) {
	if q.Kind != "external" && q.Kind != "abandoned" {
		return protocol.ChangeView{}, reject("unknown reconciliation kind")
	}
	if !protocol.ValidCommit(q.HeadCommit) || (q.Kind == "external" && !protocol.ValidCommit(q.MergedCommit)) {
		return protocol.ChangeView{}, reject("invalid reconciliation commit")
	}
	// Settings are read before the transaction: the store's only connection
	// cannot serve a read while the transaction holds it.
	var p protocol.Pipeline
	if q.Kind == "external" {
		pre, ok, err := queryChange(s.db, `id = ?`, q.ChangeID)
		if err != nil {
			return protocol.ChangeView{}, err
		}
		if ok {
			if p, err = s.deliveryPipeline(pre.Project); err != nil {
				return protocol.ChangeView{}, err
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.ChangeView{}, err
	}
	defer tx.Rollback()
	c, ok, err := queryChange(tx, `id = ?`, q.ChangeID)
	if err != nil {
		return protocol.ChangeView{}, err
	}
	if !ok || c.Machine != machine || c.State != protocol.ChangeOpen || c.Revision != q.Revision || c.HeadCommit != q.HeadCommit || c.Uncommitted {
		return protocol.ChangeView{}, reject("change no longer matches reconciliation")
	}
	now := s.now().Unix()
	e := protocol.Evidence{ChangeID: c.ID, Revision: c.Revision, Commit: c.HeadCommit, By: "daemon", At: now, Fields: map[string]string{"reconcile_kind": q.Kind}}
	if q.Kind == "external" {
		if c.HeadCommit == c.BaseCommit {
			return protocol.ChangeView{}, reject("change has no commits beyond its base")
		}
		evidence, err := changeEvidence(tx, c.ID)
		if err != nil {
			return protocol.ChangeView{}, err
		}
		if last, ok := lastMergeActivity(evidence, c); ok && s.now().Sub(time.Unix(last, 0)) < externalMergeGrace {
			return protocol.ChangeView{}, reject("%s: the merger may still report this merge; reconcile waits %s after the last test or merge report", c.ID, externalMergeGrace)
		}
		c.State, c.MergedCommit = protocol.ChangeMerged, q.MergedCommit
		e.Step, e.Result, e.Note = protocol.StepMerge, protocol.StepResultPass, "merged outside agent-net"
		e.Fields["merge_kind"], e.Fields[protocol.FieldMergedCommit] = "external", q.MergedCommit
	} else {
		if s.now().Sub(time.Unix(c.CreatedAt, 0)) <= 7*24*time.Hour {
			return protocol.ChangeView{}, reject("change is not older than seven days")
		}
		evidence, err := changeEvidence(tx, c.ID)
		if err != nil {
			return protocol.ChangeView{}, err
		}
		if len(evidence) > 0 {
			return protocol.ChangeView{}, reject("change already has step reports")
		}
		c.State = protocol.ChangeStopped
		e.Step, e.Result, e.Note = protocol.StepAccept, protocol.StepResultClose, "abandoned: branch gone"
	}
	c.UpdatedAt = now
	if err := insertEvidence(tx, e); err != nil {
		return protocol.ChangeView{}, err
	}
	if q.Kind == "external" {
		evidence, err := changeEvidence(tx, c.ID)
		if err != nil {
			return protocol.ChangeView{}, err
		}
		if afterMergeDone(p, c, evidence) {
			c.State = protocol.ChangeDone
		}
	}
	if err := writeChange(tx, c, false); err != nil {
		return protocol.ChangeView{}, err
	}
	sessions, err := closeStepTasks(tx, c)
	if err != nil {
		return protocol.ChangeView{}, err
	}
	if _, err := tx.Exec(`UPDATE inbox_items SET status='dismissed', answered_at=? WHERE status='open' AND id IN (SELECT item_id FROM delivery_step_inbox WHERE change_id=?)`, s.now().UnixNano(), c.ID); err != nil {
		return protocol.ChangeView{}, err
	}
	if q.Kind == "external" {
		// The after-merge steps start now, as they do after a merger's report.
		if err := s.queueSteps(tx, c, p, now); err != nil {
			return protocol.ChangeView{}, err
		}
	}
	if err := retireStepMessages(tx, c.ID); err != nil {
		return protocol.ChangeView{}, err
	}
	// Only an abandoned change is closed for the sessions that claimed its
	// steps. An external merge is not a close: the merger's report confirms it.
	var notices []protocol.Message
	if q.Kind == "abandoned" {
		for _, session := range sessions {
			notice, err := s.stepClosedNoticeTx(tx, c, session)
			if err != nil {
				return protocol.ChangeView{}, err
			}
			notices = append(notices, notice)
		}
	}
	if err := tx.Commit(); err != nil {
		return protocol.ChangeView{}, err
	}
	for _, notice := range notices {
		if notice.ID != "" {
			s.logMessage(notice)
		}
	}
	return s.GetChange(c.ID)
}

// lastMergeActivity is the time of the newest test or merge report on the
// current revision of change c. A report of any result counts.
func lastMergeActivity(evidence []protocol.Evidence, c protocol.Change) (int64, bool) {
	var last int64
	found := false
	for _, e := range evidence {
		if e.ChangeID != c.ID || e.Revision != c.Revision {
			continue
		}
		if e.Step != protocol.StepTest && e.Step != protocol.StepMerge {
			continue
		}
		if !found || e.At > last {
			last, found = e.At, true
		}
	}
	return last, found
}

// externalMergeRecorded reports whether the newest merge report on the current
// revision of change c is the daemon's git observation of c's merged commit.
func externalMergeRecorded(evidence []protocol.Evidence, c protocol.Change) bool {
	var last *protocol.Evidence
	for i := range evidence {
		e := &evidence[i]
		if e.ChangeID == c.ID && e.Revision == c.Revision && e.StepRef == (protocol.StepRef{Step: protocol.StepMerge}) {
			last = e
		}
	}
	return last != nil && last.Fields["reconcile_kind"] == "external" && last.Fields[protocol.FieldMergedCommit] == c.MergedCommit
}

// repairExternalDone moves a change back to merged when an external merge
// marked it done while an after-merge step was still open. Only the state and
// the step tasks change; evidence is kept. It runs at start, before the step
// tasks are backfilled.
func (s *Store) repairExternalDone() error {
	rows, err := s.db.Query(`SELECT `+changeColumns+` FROM delivery_changes WHERE state=?`, protocol.ChangeDone)
	if err != nil {
		return err
	}
	var done []protocol.Change
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			rows.Close()
			return err
		}
		done = append(done, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range done {
		evidence, err := changeEvidence(s.db, c.ID)
		if err != nil {
			return err
		}
		if !externalMergeRecorded(evidence, c) {
			continue
		}
		p, err := s.deliveryPipeline(c.Project)
		if err != nil {
			return err
		}
		c.State = protocol.ChangeMerged
		if afterMergeDone(p, c, evidence) {
			continue
		}
		if err := s.reopenMerged(c, p); err != nil {
			return err
		}
	}
	return nil
}

// reopenMerged writes change c as merged and queues the after-merge steps
// that are ready on it.
func (s *Store) reopenMerged(c protocol.Change, p protocol.Pipeline) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	c.UpdatedAt = now
	if err := writeChange(tx, c, false); err != nil {
		return err
	}
	if p, err = taskOwners(tx, c, p); err != nil {
		return err
	}
	if err := s.queueSteps(tx, c, p, now); err != nil {
		return err
	}
	if err := retireStepMessages(tx, c.ID); err != nil {
		return err
	}
	return tx.Commit()
}
