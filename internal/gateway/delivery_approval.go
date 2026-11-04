package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// answerDeliveryApproval records an inbox answer as evidence for the exact
// revision and commit that produced the item. Ordinary inbox items use the
// existing AnswerInbox path.
func (s *Store) answerDeliveryApproval(id, answer string, validate ...bool) (protocol.InboxItem, bool, error) {
	var project string
	err := s.db.QueryRow(`SELECT c.project FROM delivery_step_inbox d JOIN delivery_changes c ON c.id=d.change_id WHERE d.item_id=?`, id).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.InboxItem{}, false, nil
	}
	if err != nil {
		return protocol.InboxItem{}, true, err
	}
	p, err := s.deliveryPipeline(project)
	if err != nil {
		return protocol.InboxItem{}, true, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.InboxItem{}, true, err
	}
	defer tx.Rollback()
	item, err := scanInbox(tx.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, id))
	if err != nil {
		return item, true, err
	}
	if item.Status != "open" {
		return item, true, reject("inbox item already answered")
	}
	if strings.TrimSpace(answer) == "" || len(answer) > protocol.MaxBodyBytes {
		return item, true, reject("invalid answer")
	}
	item.AnsweredAt = s.now().UTC()
	if answer == "dismiss" || answer == "seen" {
		item.Status, item.Answer = "dismissed", ""
		if _, err := tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, item.Answer, item.AnsweredAt.UnixNano(), id); err != nil {
			return item, true, err
		}
		if len(validate) > 0 && validate[0] {
			return item, true, nil
		}
		return item, true, tx.Commit()
	}
	fields := map[string]string{}
	if strings.HasPrefix(answer, "approve ") {
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(answer, "approve "))), &fields); err != nil || fields == nil {
			return item, true, reject("delivery fields must be a JSON object of strings")
		}
		if _, supplied := fields[protocol.FieldIndependent]; supplied {
			return item, true, reject("independent is set by agent-net")
		}
	} else if answer != "approve" && answer != "refuse" {
		return item, true, reject("delivery approval needs approve, refuse or dismiss")
	}
	var changeID, commit string
	var revision, after int
	var ref protocol.StepRef
	err = tx.QueryRow(`SELECT change_id,revision,step,for_step,after_merge,commit_hash FROM delivery_step_inbox WHERE item_id=?`, id).
		Scan(&changeID, &revision, &ref.Step, &ref.For, &after, &commit)
	if err != nil {
		return item, true, err
	}
	ref.AfterMerge = after != 0
	c, ok, err := queryChange(tx, `id=?`, changeID)
	if err != nil {
		return item, true, err
	}
	if !ok || c.Revision != revision {
		return item, true, reject("the change has a different revision; dismiss this approval")
	}
	want := c.HeadCommit
	if ref.AfterMerge {
		want = c.MergedCommit
	}
	if want != commit {
		return item, true, reject("the change has a different commit; dismiss this approval")
	}
	var taskOwner, claimed string
	var completed int
	err = tx.QueryRow(`SELECT owner_node,claimed_by,completed FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&taskOwner, &claimed, &completed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return item, true, err
	}
	if err == nil && taskOwner == delegateTaskOwner {
		if completed != 0 {
			return item, true, reject("this approval was already reported by a delegate")
		}
		if _, err := tx.Exec(`UPDATE delivery_step_tasks SET claimed_by=?,completed=1
			WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
			protocol.EvidenceByOwner, c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)); err != nil {
			return item, true, err
		}
	}
	p.Steps = append([]protocol.PipelineStep(nil), p.Steps...)
	found := false
	for i := range p.Steps {
		if p.Steps[i].StepRef == ref {
			p.Steps[i].Owner = protocol.StepOwner{Kind: protocol.OwnerPerson}
			found = true
			break
		}
	}
	if !found {
		return item, true, reject("the approval step is no longer in the pipeline")
	}
	result, status := protocol.StepResultPass, "approved"
	if answer == "refuse" {
		result, status = protocol.StepResultFail, "refused"
	}
	fields[protocol.FieldIndependent] = protocol.IndependentFamily
	e := protocol.Evidence{ChangeID: c.ID, Revision: c.Revision, StepRef: ref, Result: result, Commit: commit,
		Fields: fields, By: protocol.EvidenceByOwner, At: item.AnsweredAt.Unix()}
	if err := p.Accepts(c, e); err != nil {
		return item, true, reject("%s", err.Error())
	}
	if err := insertEvidence(tx, e); err != nil {
		return item, true, err
	}
	if e.Step == protocol.StepMerge && e.Result == protocol.StepResultPass {
		c.State, c.MergedCommit, c.UpdatedAt = protocol.ChangeMerged, e.Fields[protocol.FieldMergedCommit], item.AnsweredAt.Unix()
		evidence, err := changeEvidence(tx, c.ID)
		if err != nil {
			return item, true, err
		}
		if afterMergeDone(p, c, evidence) {
			c.State = protocol.ChangeDone
		}
		if err := writeChange(tx, c, false); err != nil {
			return item, true, err
		}
	}
	item.Status, item.Answer = status, answer
	if _, err := tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, item.Answer, item.AnsweredAt.UnixNano(), id); err != nil {
		return item, true, err
	}
	if err := s.queueSteps(tx, c, p, item.AnsweredAt.Unix()); err != nil {
		return item, true, err
	}
	if err := retireStepMessages(tx, c.ID); err != nil {
		return item, true, err
	}
	var notice protocol.Message
	if answer == "refuse" || c.State == protocol.ChangeDone {
		if notice, err = s.stepResultNoticeTx(tx, c, e); err != nil {
			return item, true, err
		}
	}
	if len(validate) > 0 && validate[0] {
		return item, true, nil
	}
	if err := tx.Commit(); err != nil {
		return item, true, err
	}
	if notice.ID != "" {
		s.logMessage(notice)
	}
	return item, true, nil
}
