package gateway

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// externalMergeGrace is how long the gateway waits after the last test or
// merge report on a change before it records a merge that only git shows.
// The merger reports its own merge right after its push, and that report
// should be the one on record.
const externalMergeGrace = 10 * time.Minute

// daemonEvidenceBy is the author of evidence the daemon writes from git.
const daemonEvidenceBy = "daemon"

// ReconcileChange accepts git observations only from the machine holding the
// change. It bypasses pipeline steps without inventing test or release passes.
// An external merge makes the change done and queues no steps: a merger that
// confirms the merge reopens its after-merge steps (see ReportStep). The
// transaction rejects observations made obsolete by a new revision/report.
func (s *Store) ReconcileChange(machine string, q protocol.ChangeReconcileRequest) (protocol.ChangeView, error) {
	if q.Kind != protocol.ReconcileExternal && q.Kind != protocol.ReconcileAbandoned && q.Kind != protocol.ReconcileNoWork {
		return protocol.ChangeView{}, reject("unknown reconciliation kind")
	}
	if !protocol.ValidCommit(q.HeadCommit) || (q.Kind == protocol.ReconcileExternal && !protocol.ValidCommit(q.MergedCommit)) {
		return protocol.ChangeView{}, reject("invalid reconciliation commit")
	}
	if q.Kind == protocol.ReconcileExternal && q.PRNumber == 0 {
		switch q.Proof {
		case "", protocol.MergeProofAncestor, protocol.MergeProofCherry, protocol.MergeProofSquash:
		default:
			return protocol.ChangeView{}, reject("unknown merge proof %q", q.Proof)
		}
	}
	// Read the pipeline before the transaction. A PR merge needs it to leave
	// the after-merge steps (verify among them) for the caller.
	pre, found, err := queryChange(s.db, `id = ?`, q.ChangeID)
	if err != nil {
		return protocol.ChangeView{}, err
	}
	var p protocol.Pipeline
	if found {
		p, err = s.deliveryPipeline(pre.Project)
		if err != nil {
			return protocol.ChangeView{}, err
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
	if q.Kind == protocol.ReconcileNoWork {
		return s.noWorkNotice(tx, c)
	}
	now := s.now().Unix()
	e := protocol.Evidence{ChangeID: c.ID, Revision: c.Revision, Commit: c.HeadCommit, By: daemonEvidenceBy, At: now, Fields: map[string]string{"reconcile_kind": q.Kind}}
	externalPR := false
	if q.Kind == protocol.ReconcileExternal {
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
		externalPR = q.PRNumber > 0
		c.State, c.MergedCommit = protocol.ChangeDone, q.MergedCommit
		e.Step, e.Result, e.Note = protocol.StepMerge, protocol.StepResultPass, externalMergeNote(q)
		e.Fields["merge_kind"], e.Fields[protocol.FieldMergedCommit] = "external", q.MergedCommit
		if q.Proof != "" {
			e.Fields["merge_proof"] = q.Proof
		}
		if externalPR {
			// A merged PR is the lead's review on GitHub. The change stays
			// merged so verify still runs for the caller; accept, and test
			// when every check passed, are recorded from that review.
			c.State = protocol.ChangeMerged
			e.Fields["merge_proof"] = "pr"
			e.Fields["pr_url"] = q.PRURL
		} else if q.Proof != "" {
			// No review was recorded for a git-only proof, and none is
			// invented: the record lists the steps agent-net did not run.
			e.Fields["skipped_steps"] = "accept,test"
		}
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
	if externalPR {
		evidence, err := changeEvidence(tx, c.ID)
		if err != nil {
			return protocol.ChangeView{}, err
		}
		if err := insertExternalPRSteps(tx, c, q, evidence, now); err != nil {
			return protocol.ChangeView{}, err
		}
		evidence, err = changeEvidence(tx, c.ID)
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
	if c.State == protocol.ChangeDone || c.State == protocol.ChangeStopped {
		if err := s.closeChangeAsks(tx, c.ID, protocol.AskReasonChangeDone); err != nil {
			return protocol.ChangeView{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE inbox_items SET status='dismissed', answered_at=? WHERE status='open' AND id IN (SELECT item_id FROM delivery_step_inbox WHERE change_id=?)`, s.now().UnixNano(), c.ID); err != nil {
		return protocol.ChangeView{}, err
	}
	if err := retireStepMessages(tx, c.ID); err != nil {
		return protocol.ChangeView{}, err
	}
	if externalPR && c.State == protocol.ChangeMerged {
		// The caller still runs the after-merge steps, verify last.
		if err := s.queueSteps(tx, c, p, now); err != nil {
			return protocol.ChangeView{}, err
		}
	}
	// Only an abandoned change is closed for the sessions that claimed its
	// steps. An external merge is not a close: the merger's report confirms it.
	var notices []protocol.Message
	if q.Kind == protocol.ReconcileExternal {
		// The caller owns the change's steps and may not know it moved.
		proof := "git shows it on the default branch"
		if q.PRNumber > 0 {
			proof = fmt.Sprintf("PR #%d", q.PRNumber)
		}
		notice, err := s.callerNoticeTx(tx, c, fmt.Sprintf("merged-outside-%s-r%d", c.ID, c.Revision),
			fmt.Sprintf("[agent-net merged outside]\nYour change %s was merged outside agent-net (%s); I recorded it.", changeLabel(c), proof))
		if err != nil {
			return protocol.ChangeView{}, err
		}
		notices = append(notices, notice)
	}
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

// externalMergeNote is the note on the merge step an external merge records.
// An empty proof is an older daemon and keeps the plain note.
func externalMergeNote(q protocol.ChangeReconcileRequest) string {
	const outside = "merged outside agent-net"
	switch {
	case q.PRNumber > 0:
		return fmt.Sprintf("%s: PR #%d (%s)", outside, q.PRNumber, q.PRURL)
	case q.Proof == protocol.MergeProofAncestor:
		return outside + ": head is an ancestor of the default branch; agent-net ran no accept or test step"
	case q.Proof == protocol.MergeProofCherry:
		return outside + ": every commit has a patch-equivalent on the default branch; agent-net ran no accept or test step"
	case q.Proof == protocol.MergeProofSquash:
		return outside + ": a squash commit on the default branch has the tree of this head; agent-net ran no accept or test step"
	}
	return outside
}

// noWorkNotice tells the caller of change c once per revision that it records
// no commits beyond its base. It changes nothing on the change: with no work
// recorded, nothing shows whether it was merged, so only the caller can close
// it or resubmit it. The message key makes a repeated observation a no-op.
func (s *Store) noWorkNotice(tx *sql.Tx, c protocol.Change) (protocol.ChangeView, error) {
	if c.HeadCommit != c.BaseCommit {
		return protocol.ChangeView{}, reject("change has commits beyond its base")
	}
	notice, err := s.callerNoticeTx(tx, c, fmt.Sprintf("no-work-%s-r%d", c.ID, c.Revision),
		fmt.Sprintf("[agent-net no work recorded]\nchange: %s\nproject: %s\nrevision: %d\nbranch: %s\n\n%s has no work recorded: its head equals its base. agent-net cannot tell whether it was merged. Close it (agent_step_report accept with result close) or resubmit it with the branch that holds the work.",
			c.ID, protocol.NeutralizeMarkers(c.Project), c.Revision, protocol.NeutralizeMarkers(c.Branch), changeLabel(c)))
	if err != nil {
		return protocol.ChangeView{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.ChangeView{}, err
	}
	if notice.ID != "" {
		s.logMessage(notice)
	}
	return s.GetChange(c.ID)
}

// changeLabel names a change for a message: its title and ID, or the ID.
func changeLabel(c protocol.Change) string {
	if title := protocol.NeutralizeMarkers(c.Title); title != "" {
		return title + " (" + c.ID + ")"
	}
	return c.ID
}

// callerNoticeTx stores a notification for the caller of change c in tx. The
// key makes a repeat a no-op: it returns an empty message then, and also when
// the change has no caller or the caller's session is gone.
func (s *Store) callerNoticeTx(tx *sql.Tx, c protocol.Change, key, body string) (protocol.Message, error) {
	if c.Caller == "" {
		return protocol.Message{}, nil
	}
	var ok int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=?)`, c.Caller).Scan(&ok); err != nil || ok == 0 {
		return protocol.Message{}, err
	}
	now := s.now().UTC()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: c.Caller, Type: protocol.TypeNotification, Body: body, IdempotencyKey: key,
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	res, err := tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?) ON CONFLICT(sender, idem_key) DO NOTHING`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State)
	if err != nil {
		return protocol.Message{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return protocol.Message{}, err
	}
	return m, nil
}

// externalPRNote is the evidence the lead's GitHub review leaves on accept and
// test. It names the PR so the record says where the review happened.
func externalPRNote(q protocol.ChangeReconcileRequest) string {
	return fmt.Sprintf("merged on GitHub by PR #%d (%s); the lead's review there", q.PRNumber, q.PRURL)
}

// insertExternalPRSteps records accept, and the before-merge test when every
// check passed, from the merged pull request. It never records a test pass
// without that evidence and never overwrites a pass already on this revision.
func insertExternalPRSteps(tx *sql.Tx, c protocol.Change, q protocol.ChangeReconcileRequest, evidence []protocol.Evidence, now int64) error {
	note := externalPRNote(q)
	for _, ref := range []protocol.StepRef{{Step: protocol.StepAccept}, {Step: protocol.StepTest}} {
		if ref.Step == protocol.StepTest && !q.PRChecksPassed {
			continue
		}
		if stepPassedOn(evidence, c, ref) {
			continue
		}
		e := protocol.Evidence{ChangeID: c.ID, Revision: c.Revision, StepRef: ref, Result: protocol.StepResultPass,
			Commit: c.HeadCommit, By: daemonEvidenceBy, At: now, Note: note}
		if err := insertEvidence(tx, e); err != nil {
			return err
		}
	}
	return nil
}

// stepPassedOn reports whether evidence already holds a pass for ref on the
// change's head and current revision.
func stepPassedOn(evidence []protocol.Evidence, c protocol.Change, ref protocol.StepRef) bool {
	for _, e := range evidence {
		if e.ChangeID == c.ID && e.Revision == c.Revision && e.StepRef == ref && e.Result == protocol.StepResultPass && e.Commit == c.HeadCommit {
			return true
		}
	}
	return false
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

// latestMergeReport returns the newest merge report on the current revision of c.
func latestMergeReport(evidence []protocol.Evidence, c protocol.Change) (protocol.Evidence, bool) {
	var last protocol.Evidence
	found := false
	for _, e := range evidence {
		if e.ChangeID == c.ID && e.Revision == c.Revision && e.StepRef == (protocol.StepRef{Step: protocol.StepMerge}) {
			last, found = e, true
		}
	}
	return last, found
}

// externalMergeRecorded reports whether the newest merge report on the current
// revision of change c is the daemon's git observation of c's merged commit.
func externalMergeRecorded(evidence []protocol.Evidence, c protocol.Change) bool {
	last, ok := latestMergeReport(evidence, c)
	return ok && last.Fields["reconcile_kind"] == "external" && last.Fields[protocol.FieldMergedCommit] == c.MergedCommit
}

// racedWithReport reports whether the external merge ext on the current
// revision of c was recorded within externalMergeGrace after a test or merge
// report by a session other than the daemon: the merger was still merging.
func racedWithReport(evidence []protocol.Evidence, c protocol.Change, ext protocol.Evidence) bool {
	for _, e := range evidence {
		if e.ChangeID != c.ID || e.Revision != c.Revision || e.By == daemonEvidenceBy {
			continue
		}
		if e.Step != protocol.StepTest && e.Step != protocol.StepMerge {
			continue
		}
		if e.At <= ext.At && time.Duration(ext.At-e.At)*time.Second < externalMergeGrace {
			return true
		}
	}
	return false
}

// repairExternalDone reopens a done change whose external merge raced a
// merger's report (see racedWithReport) and left an after-merge step open: the
// change goes back to merged and its steps are queued. Only the state and the
// step tasks change; evidence is kept. Every other done change stays as it is.
// It runs at start, before the step tasks are backfilled.
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
		ext, _ := latestMergeReport(evidence, c)
		if !racedWithReport(evidence, c, ext) {
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
