package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func stepAboutPrefix(changeID string, revision int, ref protocol.StepRef) string {
	return fmt.Sprintf("%s:%d:%s:%s:%d:", changeID, revision, ref.Step, ref.For, boolInt(ref.AfterMerge))
}

func stepAboutID(changeID string, revision int, ref protocol.StepRef, commit string) string {
	return stepAboutPrefix(changeID, revision, ref) + commit
}

func parseStepAboutID(aboutID string) (changeID string, revision int, ref protocol.StepRef, commit string, ok bool) {
	parts := strings.SplitN(aboutID, ":", 6)
	if len(parts) != 6 || parts[0] == "" {
		return "", 0, ref, "", false
	}
	rev, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, ref, "", false
	}
	return parts[0], rev, protocol.StepRef{Step: parts[2], For: parts[3], AfterMerge: parts[4] != "" && parts[4] != "0"}, parts[5], true
}

// answerDeliveryApproval records an inbox answer as evidence for the exact
// revision and commit that produced the item. Ordinary inbox items use the
// existing AnswerInbox path.
func (s *Store) answerDeliveryApproval(id, answer string, validate ...bool) (protocol.InboxItem, bool, error) {
	var aboutKind, aboutID string
	err := s.db.QueryRow(`SELECT about_kind,about_id FROM inbox_items WHERE id=?`, id).Scan(&aboutKind, &aboutID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && aboutKind != protocol.AskAboutStep) {
		return protocol.InboxItem{}, false, nil
	}
	if err != nil {
		return protocol.InboxItem{}, true, err
	}
	changeID, revision, ref, commit, ok := parseStepAboutID(aboutID)
	if !ok {
		return protocol.InboxItem{}, false, nil
	}
	var project string
	err = s.db.QueryRow(`SELECT project FROM delivery_changes WHERE id=?`, changeID).Scan(&project)
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
		item.Status, item.Answer = "dismissed", "dismissed_by_owner"
		if _, err := tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, item.Answer, item.AnsweredAt.UnixNano(), id); err != nil {
			return item, true, err
		}
		if err := s.recordOwnerAnswer(tx, item, answer); err != nil {
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
	var taskOwner string
	err = tx.QueryRow(`SELECT owner_node FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&taskOwner)
	switch {
	case err == nil && taskOwner == delegateTaskOwner:
		if err := endStepTasks(tx, `t.change_id=? AND t.revision=? AND t.step=? AND t.for_step=? AND t.after_merge=?`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)); err != nil {
			return item, true, err
		}
	case errors.Is(err, sql.ErrNoRows):
		// A delegate that reported the step already ended its task.
		var reported bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_evidence WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&reported); err != nil {
			return item, true, err
		} else if reported {
			return item, true, reject("this approval was already reported by a delegate")
		}
	case err != nil:
		return item, true, err
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
	if err := checkMergeReady(tx, p, c, e); err != nil {
		return item, true, err
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
	if err := s.recordOwnerAnswer(tx, item, answer); err != nil {
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
