package gateway

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ReconcileChange accepts git observations only from the machine holding the
// change. It bypasses pipeline steps without inventing test or release passes.
// The transaction rejects observations made obsolete by a new revision/report.
func (s *Store) ReconcileChange(machine string, q protocol.ChangeReconcileRequest) (protocol.ChangeView, error) {
	if q.Kind != "external" && q.Kind != "abandoned" {
		return protocol.ChangeView{}, reject("unknown reconciliation kind")
	}
	if !protocol.ValidCommit(q.HeadCommit) || (q.Kind == "external" && !protocol.ValidCommit(q.MergedCommit)) {
		return protocol.ChangeView{}, reject("invalid reconciliation commit")
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
		c.State, c.MergedCommit = protocol.ChangeDone, q.MergedCommit
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
	if err := retireStepMessages(tx, c.ID); err != nil {
		return protocol.ChangeView{}, err
	}
	var notices []protocol.Message
	for _, session := range sessions {
		notice, err := s.stepClosedNoticeTx(tx, c, session)
		if err != nil {
			return protocol.ChangeView{}, err
		}
		notices = append(notices, notice)
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
