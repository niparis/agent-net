package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// A slash cannot occur in a graph node name, so it cannot be confused with a
// node assignment. The actual delegate nodes are stored separately.
const delegateTaskOwner = "/delegate"
const callerTaskOwner = "/caller"
const mergerTaskOwner = "/merger"

type nodeTaskTarget struct {
	id, owner, session string
	revision           int
	ref                protocol.StepRef
	urgent             bool
	createdAt          int64
	isReminder         bool
	wasNotified        bool // Includes expired notices; a pause must not reclassify these as first notices.
	pullForward        bool
	noticeDue          int64
}

func taskDisplayOwner(owner string) string {
	switch owner {
	case callerTaskOwner:
		return "caller"
	case mergerTaskOwner:
		return "merger"
	case delegateTaskOwner:
		return "delegate"
	default:
		return owner
	}
}

func formatAge(d time.Duration) string {
	d = d.Round(time.Second)
	if d <= 0 {
		return "0s"
	}
	if d >= time.Hour {
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		s := int((d % time.Minute) / time.Second)
		if m == 0 && s == 0 {
			return fmt.Sprintf("%dh", h)
		}
		if s == 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if d >= time.Minute {
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

// backfillDeliveryTasks makes changes recorded before routing was installed
// eligible for the same durable queue as newly submitted changes. Existing
// task rows retain their captured owners and claims across restarts.
func (s *Store) backfillDeliveryTasks() error {
	rows, err := s.db.Query(`SELECT ` + changeColumns + ` FROM delivery_changes WHERE state IN ('open','merged') ORDER BY seq`)
	if err != nil {
		return err
	}
	var changes []protocol.Change
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	pipelines := map[string]protocol.Pipeline{}
	for _, c := range changes {
		p, ok := pipelines[c.Project]
		if !ok {
			p, err = s.deliveryPipeline(c.Project)
			if err != nil {
				return err
			}
			pipelines[c.Project] = p
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		p, err = taskOwners(tx, c, p)
		if err == nil {
			err = s.queueSteps(tx, c, p, s.now().Unix())
		}
		if err == nil {
			err = retireStepMessages(tx, c.ID)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// retireStepMessages removes undelivered notices for obsolete revisions,
// finished tasks, or sessions that lost a claim. Already delivered notices
// remain in the conversation; a later claim or report still checks the task.
func retireStepMessages(tx *sql.Tx, change string) error {
	_, err := tx.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched') AND id IN (
		SELECT d.message_id FROM delivery_step_messages d
		JOIN delivery_step_tasks t ON t.change_id=d.change_id AND t.revision=d.revision AND t.step=d.step
		 AND t.for_step=d.for_step AND t.after_merge=d.after_merge
		JOIN delivery_changes c ON c.id=t.change_id WHERE d.change_id=? AND
		 (t.revision<>c.revision OR t.completed=1 OR (t.claimed_by<>'' AND t.claimed_by<>d.session_id)
		 OR (t.after_merge=0 AND c.state<>'open') OR (t.after_merge=1 AND c.state<>'merged'))
		 AND NOT EXISTS (
			SELECT 1 FROM delivery_step_messages d2
			JOIN delivery_step_tasks t2 ON t2.change_id=d2.change_id AND t2.revision=d2.revision AND t2.step=d2.step
			 AND t2.for_step=d2.for_step AND t2.after_merge=d2.after_merge
			JOIN delivery_changes c2 ON c2.id=t2.change_id
			WHERE d2.message_id=d.message_id
			 AND t2.revision=c2.revision AND t2.completed=0
			 AND (t2.claimed_by='' OR t2.claimed_by=d2.session_id)
			 AND ((t2.after_merge=0 AND c2.state='open') OR (t2.after_merge=1 AND c2.state='merged'))))`, change)
	return err
}

// retireObsoleteStepTasks ends step tasks that can no longer be reported:
// every task of a done or stopped change, and a before-merge task once its
// change has merged. State changes retire these themselves; this is the
// safety net for a state written before the tasks existed, for a task that was
// claimed (queueSteps leaves claims alone), and for an old record repaired
// later. The undelivered notices those tasks carried expire with them.
func (s *Store) retireObsoleteStepTasks() error {
	rows, err := s.db.Query(`SELECT c.id,c.state FROM delivery_changes c
		WHERE (c.state IN ('done','stopped') AND EXISTS (
			SELECT 1 FROM delivery_step_tasks t WHERE t.change_id=c.id AND t.completed=0))
		OR (c.state='merged' AND EXISTS (
			SELECT 1 FROM delivery_step_tasks t WHERE t.change_id=c.id AND t.completed=0 AND t.after_merge=0))`)
	if err != nil {
		return err
	}
	type obsoleteChange struct{ id, state string }
	var changes []obsoleteChange
	for rows.Next() {
		var c obsoleteChange
		if err := rows.Scan(&c.id, &c.state); err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// A stuck notice is a plain message, not linked through
	// delivery_step_messages, so it needs its own retirement even when its
	// task row is already gone.
	var stuck int
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE sender='agent-net'
		AND state IN ('stored','dispatched') AND idem_key LIKE 'delivery:stuck:%')`).Scan(&stuck); err != nil {
		return err
	}
	if len(changes) == 0 && stuck == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range changes {
		if _, err := tx.Exec(`UPDATE delivery_step_tasks SET completed=1 WHERE change_id=? AND completed=0
			AND ((after_merge=0 AND ?<>'open') OR (after_merge=1 AND ?<>'merged'))`, c.id, c.state, c.state); err != nil {
			return err
		}
		if err := retireStepMessages(tx, c.id); err != nil {
			return err
		}
	}
	if stuck != 0 {
		// The change id follows the prefix "delivery:stuck:", and the last
		// field of the key is the after-merge flag.
		const id = `substr(idem_key,16,instr(substr(idem_key,16),':')-1)`
		if _, err := tx.Exec(`UPDATE messages SET state='expired' WHERE sender='agent-net'
			AND state IN ('stored','dispatched') AND idem_key LIKE 'delivery:stuck:%'
			AND (EXISTS (SELECT 1 FROM delivery_changes c WHERE c.id=` + id + ` AND c.state IN ('done','stopped'))
			 OR (substr(idem_key,-1)='0' AND EXISTS (SELECT 1 FROM delivery_changes c WHERE c.id=` + id + ` AND c.state='merged')))`); err != nil {
			return err
		}
	}
	if _, err := retireUnlinkedStepMessages(tx); err != nil {
		return err
	}
	if _, err := retireEmptyNudgeBatches(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type sqlExecer interface {
	Exec(string, ...any) (sql.Result, error)
}

// retireUnlinkedStepMessages expires a step notice that no task association
// points at any more. Creating a reminder moves the association to the new
// message, so the earlier notice is stale even when its task is still open.
func retireUnlinkedStepMessages(ex sqlExecer) (int64, error) {
	res, err := ex.Exec(`UPDATE messages SET state='expired'
 WHERE sender='agent-net' AND state IN ('stored','dispatched') AND idem_key GLOB 'step_*'
 AND NOT EXISTS (
   SELECT 1 FROM delivery_step_messages d
   JOIN delivery_step_tasks t ON t.change_id=d.change_id AND t.revision=d.revision AND t.step=d.step
    AND t.for_step=d.for_step AND t.after_merge=d.after_merge
   JOIN delivery_changes c ON c.id=t.change_id
   WHERE d.message_id=messages.id AND t.revision=c.revision AND t.completed=0
    AND (t.claimed_by='' OR t.claimed_by=d.session_id)
    AND ((t.after_merge=0 AND c.state='open') OR (t.after_merge=1 AND c.state='merged')))`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// retireEmptyNudgeBatches expires a nudge carrier whose messages have all
// expired. The carrier is the text a session sees, so retiring only the
// originals would still deliver a reminder that no longer applies.
func retireEmptyNudgeBatches(ex sqlExecer) (int64, error) {
	res, err := ex.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched')
		AND idem_key LIKE 'nudge:%'
		AND NOT EXISTS (SELECT 1 FROM messages o WHERE o.batch_id=messages.id AND o.state<>'expired')`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// closeStepTasks ends every pending step task of c and returns the sessions
// that had claimed one, so each can be told to drop the change. A closed
// change keeps its evidence; only the open tasks end.
func closeStepTasks(tx *sql.Tx, c protocol.Change) ([]string, error) {
	rows, err := tx.Query(`SELECT DISTINCT claimed_by FROM delivery_step_tasks
		WHERE change_id=? AND completed=0 AND claimed_by<>'' ORDER BY claimed_by`, c.ID)
	if err != nil {
		return nil, err
	}
	var sessions []string
	for rows.Next() {
		var session string
		if err := rows.Scan(&session); err != nil {
			rows.Close()
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := tx.Exec(`UPDATE delivery_step_tasks SET completed=1 WHERE change_id=? AND completed=0`, c.ID); err != nil {
		return nil, err
	}
	return sessions, nil
}

// stepClosedNoticeTx tells a session that had claimed a step that the caller
// closed change c, so it drops the change from its queue instead of reporting.
func (s *Store) stepClosedNoticeTx(tx *sql.Tx, c protocol.Change, session string) (protocol.Message, error) {
	if session == "" || session == c.Caller {
		return protocol.Message{}, nil
	}
	var ok int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=?)`, session).Scan(&ok); err != nil || ok == 0 {
		return protocol.Message{}, err
	}
	title := protocol.NeutralizeMarkers(c.Title)
	if title == "" {
		title = c.ID
	} else {
		title += " (" + c.ID + ")"
	}
	text := fmt.Sprintf("[agent-net step closed]\nchange: %s\nproject: %s\nrevision: %d\n\n%s was closed by its caller without merge. Drop it from your queue; do not report a step for it.",
		c.ID, protocol.NeutralizeMarkers(c.Project), c.Revision, title)
	now := s.now().UTC()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: session, Type: protocol.TypeNotification, Body: text, IdempotencyKey: protocol.NewID("step-closed"),
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	_, err := tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State)
	return m, err
}

func dismissStepInbox(tx *sql.Tx, c protocol.Change, ref protocol.StepRef, at int64) error {
	_, err := tx.Exec(`UPDATE inbox_items SET status='dismissed',answer='rule_disabled',answered_at=? WHERE (status='open' OR (status='dismissed' AND answer='')) AND id IN (
		SELECT item_id FROM delivery_step_inbox WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`,
		at, c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge))
	return err
}

// stepMessageCurrent prevents a task selected for delivery before a claim or
// revision change from being sent afterwards.
func (s *Store) stepMessageCurrent(id string) (bool, error) {
	var current int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_messages d
		JOIN delivery_step_tasks t ON t.change_id=d.change_id AND t.revision=d.revision AND t.step=d.step
		 AND t.for_step=d.for_step AND t.after_merge=d.after_merge
		JOIN delivery_changes c ON c.id=t.change_id JOIN messages m ON m.id=d.message_id
		WHERE d.message_id=? AND t.revision=c.revision AND t.completed=0
		 AND (t.claimed_by='' OR t.claimed_by=d.session_id)
		 AND ((t.after_merge=0 AND c.state='open') OR (t.after_merge=1 AND c.state='merged'))
		 AND m.state IN ('stored','dispatched','failed'))`, id).Scan(&current)
	return current != 0, err
}

// stepResultNoticeTx tells the caller when a node-owned step needs attention
// or finishes the change. It is committed together with the evidence.
func (s *Store) stepResultNoticeTx(tx *sql.Tx, c protocol.Change, e protocol.Evidence) (protocol.Message, error) {
	if c.Caller == "" || c.Caller == e.By || c.Caller == e.ReportedBy {
		return protocol.Message{}, nil
	}
	var ok int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=?)`, c.Caller).Scan(&ok); err != nil || ok == 0 {
		return protocol.Message{}, err
	}
	text := fmt.Sprintf("[agent-net step result]\nchange: %s\nproject: %s\nrevision: %d\ncommit: %s\nstep: %s\nresult: %s\nreported by: %s",
		c.ID, protocol.NeutralizeMarkers(c.Project), c.Revision, e.Commit, e.StepRef.String(), e.Result, protocol.NeutralizeMarkers(e.By))
	if e.Note != "" {
		text += "\nfindings: " + protocol.NeutralizeMarkers(e.Note)
	}
	if len(e.Fields) > 0 {
		fields, err := json.Marshal(e.Fields)
		if err != nil {
			return protocol.Message{}, err
		}
		text += "\nfields: " + protocol.NeutralizeMarkers(string(fields))
	}
	if c.State == protocol.ChangeDone {
		text += "\nThe change has completed its delivery steps."
	} else if c.State == protocol.ChangeStopped {
		text += "\nThe change was closed without merge."
	} else {
		text += "\nReview the result and decide what happens next."
	}
	now := s.now().UTC()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: c.Caller, Type: protocol.TypeNotification, Body: text, IdempotencyKey: protocol.NewID("step-result"),
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	_, err := tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State)
	return m, err
}

// taskOwners preserves the owner captured when an unfinished node step became
// ready. A later assignment applies when a different step starts.
func taskOwners(q deliveryQuerier, c protocol.Change, p protocol.Pipeline) (protocol.Pipeline, error) {
	rows, err := q.Query(`SELECT step,for_step,after_merge,owner_node FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND completed=0`, c.ID, c.Revision)
	if err != nil {
		return protocol.Pipeline{}, err
	}
	defer rows.Close()
	p.Steps = append([]protocol.PipelineStep(nil), p.Steps...)
	for rows.Next() {
		var ref protocol.StepRef
		var owner string
		var after int
		if err := rows.Scan(&ref.Step, &ref.For, &after, &owner); err != nil {
			return protocol.Pipeline{}, err
		}
		ref.AfterMerge = after != 0
		for i := range p.Steps {
			if p.Steps[i].StepRef == ref {
				if ref.Step == protocol.StepApprove && (p.Steps[i].Owner.Kind == protocol.OwnerPerson ||
					(p.Steps[i].Owner.Kind == protocol.OwnerDelegate && owner != delegateTaskOwner)) {
					// A stricter approval rule cannot be bypassed by a task
					// that was queued under the previous rule.
					break
				}
				if owner == callerTaskOwner {
					p.Steps[i].Owner = protocol.StepOwner{Kind: protocol.OwnerCaller}
				} else if owner == mergerTaskOwner {
					p.Steps[i].Owner = protocol.StepOwner{Kind: protocol.OwnerMerger}
				} else if owner == delegateTaskOwner {
					p.Steps[i].Owner = protocol.StepOwner{Kind: protocol.OwnerDelegate}
				} else {
					p.Steps[i].Owner = protocol.StepOwner{Kind: protocol.OwnerNode, Node: owner}
				}
				break
			}
		}
	}
	return p, rows.Err()
}

func checkApprovalTaskPolicy(tx *sql.Tx, c protocol.Change, p protocol.Pipeline, ref protocol.StepRef) error {
	if ref.Step != protocol.StepApprove {
		return nil
	}
	step, ok := p.Find(ref)
	if !ok {
		return reject("approval step is not in the pipeline")
	}
	// Re-read the rule inside the report transaction: settings may have
	// tightened after the pipeline was resolved outside the transaction.
	snap, err := settingsSnapshotTx(tx)
	if err != nil {
		return err
	}
	repo := c.Project
	if _, err := graphKind(tx, repo); err != nil {
		var rejection Rejection
		if !errors.As(err, &rejection) {
			return err
		}
		repo = ""
	}
	key := "delivery.merge_authority"
	if ref.For == protocol.StepDeploy {
		key = "delivery.deploy_rule"
	}
	rule, err := deliveryValue(snap, key, repo)
	if err != nil {
		return err
	}
	written := (ref.For == protocol.StepMerge && rule == protocol.MergeAsk) || (ref.For == protocol.StepDeploy && rule == protocol.DeployOwner)
	delegated := (ref.For == protocol.StepMerge && rule == protocol.MergeDelegated) || (ref.For == protocol.StepDeploy && rule == protocol.DeployAsk)
	if written || step.Owner.Kind == protocol.OwnerPerson {
		return reject("%s requires the owner's written approval", ref)
	}
	if delegated || step.Owner.Kind == protocol.OwnerDelegate {
		var owner string
		err := tx.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if owner != delegateTaskOwner {
			return reject("%s requires a delegate or the owner", ref)
		}
	}
	return nil
}

const defaultStepTaskQuietWindow = 10 * time.Second
const defaultStepTaskRecentWindow = 30 * time.Second
const defaultStepTaskBatchCap = 25

type stepTaskBatch struct {
	session string
	targets []nodeTaskTarget
	timer   *time.Timer
	// timerGen names the quiet-window timer that is armed now. A timer that
	// had already fired when a newer task re-armed it still runs its callback;
	// the callback sees an old generation and does nothing, so a task that
	// just joined the batch waits a full quiet window.
	timerGen uint64
}

func (g *Gateway) quietWindow() time.Duration {
	if g.stepTaskQuietWindow > 0 {
		return g.stepTaskQuietWindow
	}
	if g.opts.StepTaskQuietWindow > 0 {
		return g.opts.StepTaskQuietWindow
	}
	return defaultStepTaskQuietWindow
}

func (g *Gateway) recentWindow() time.Duration {
	if g.stepTaskRecentWindow > 0 {
		return g.stepTaskRecentWindow
	}
	if g.opts.StepTaskRecentWindow > 0 {
		return g.opts.StepTaskRecentWindow
	}
	return defaultStepTaskRecentWindow
}

func (g *Gateway) batchCap() int {
	if g.stepTaskBatchCap > 0 {
		return g.stepTaskBatchCap
	}
	if g.opts.StepTaskBatchCap > 0 {
		return g.opts.StepTaskBatchCap
	}
	return defaultStepTaskBatchCap
}

func (g *Gateway) recordSessionReport(sessionID string) {
	if sessionID == "" {
		return
	}
	g.lastReportMu.Lock()
	if g.lastReportBySession == nil {
		g.lastReportBySession = make(map[string]time.Time)
	}
	g.lastReportBySession[sessionID] = time.Now()
	g.lastReportMu.Unlock()
}

func targetInBatch(list []nodeTaskTarget, t nodeTaskTarget) bool {
	for _, x := range list {
		if x.id == t.id && x.revision == t.revision && x.ref == t.ref && x.session == t.session {
			return true
		}
	}
	return false
}

// sessionHasRecentStepMessage checks if the session has a step-task message created within recentWindow.
func (s *Store) sessionHasRecentStepMessage(session string, recentWindow time.Duration) (bool, error) {
	cutoff := s.now().Add(-recentWindow).UnixNano()
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages m
		JOIN delivery_step_messages d ON d.message_id=m.id
		WHERE m.recipient=? AND m.sender='agent-net' AND m.state<>'expired'
		AND m.created_at>=?)`, session, cutoff).Scan(&exists)
	return exists != 0, err
}

// stepReminderCutoff returns the oldest message time that still counts as a
// delivered task notice. Zero disables reminders, so any recorded notice
// suppresses a new one.
func (s *Store) stepReminderCutoff() int64 {
	interval := 2 * time.Hour
	if snap, err := s.SettingsSnapshot(); err == nil {
		interval = snap.Duration("delivery.step_reminder", settings.Where{})
	}
	if interval <= 0 {
		return 0
	}
	return s.now().Add(-interval).UnixNano()
}

// verifyReminderAges are measured from the time the step became ready.
var verifyReminderAges = [...]time.Duration{30 * time.Minute, 2 * time.Hour, 8 * time.Hour, 24 * time.Hour}

func verifyReminderDue(ready, last time.Time) time.Time {
	for _, age := range verifyReminderAges {
		if due := ready.Add(age); due.After(last) {
			return due
		}
	}
	age := last.Sub(ready)
	return ready.Add((age/(24*time.Hour) + 1) * 24 * time.Hour)
}

// stepMessageRecorded reports whether the step already has a live task notice
// for this session that is newer than the reminder cutoff.
func (s *Store) stepMessageRecorded(t nodeTaskTarget, cutoff int64) (bool, error) {
	return s.stepMessageRecordedAt(t, cutoff, s.now())
}

func (s *Store) stepMessageRecordedAt(t nodeTaskTarget, cutoff int64, at time.Time) (bool, error) {
	active, _, err := s.stepWaiting(t)
	if err != nil || active {
		return active, err
	}

	if t.ref.AfterMerge && t.ref.Step == protocol.StepVerify && cutoff != 0 {
		var ready, last int64
		var claimed string
		err := s.db.QueryRow(`SELECT created_at,MAX(last_notice_at,last_notice_due_at),claimed_by FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=1`, t.id, t.revision, t.ref.Step, t.ref.For).Scan(&ready, &last, &claimed)
		if err != nil {
			return false, err
		}
		if claimed == "" && last != 0 {
			return at.Before(verifyReminderDue(time.Unix(ready, 0), time.Unix(0, last))), nil
		}
	}
	// Waiting retires undelivered notices. Keep their durable send time so
	// ending the wait cannot bring an ordinary reminder forward or bypass 0s.
	if !t.ref.AfterMerge || t.ref.Step != protocol.StepVerify || cutoff == 0 {
		var last int64
		err := s.db.QueryRow(`SELECT last_notice_at FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND waiting_reason<>''`, t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge)).Scan(&last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if last != 0 && (cutoff == 0 || last >= cutoff) {
			return true, nil
		}
	}
	var recorded int
	err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id
		WHERE d.change_id=? AND d.revision=? AND d.step=? AND d.for_step=? AND d.after_merge=? AND d.session_id=?
		AND m.state<>'expired' AND (?=0 OR m.created_at>=?))`,
		t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge), t.session, cutoff, cutoff).Scan(&recorded)
	return recorded != 0, err
}

func (s *Store) clearTaskUrgent(t nodeTaskTarget) error {
	_, err := s.db.Exec(`UPDATE delivery_step_tasks SET urgent=0 WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge))
	return err
}

// flushStepTasks sends the session's waiting batch now.
func (g *Gateway) flushStepTasks(session string) {
	g.flushStepTasksArmed(session, 0)
}

// flushStepTasksArmed is flushStepTasks for a quiet-window timer. It sends
// the batch only while that timer is the one armed on it (gen 0 sends
// always). A timer that fired just before a new task re-armed the batch is
// stale: sending then would cut the burst in two.
func (g *Gateway) flushStepTasksArmed(session string, gen uint64) {
	g.flushStepTasksArmedContext(context.Background(), session, gen)
}

func (g *Gateway) flushStepTasksArmedContext(ctx context.Context, session string, gen uint64) {
	g.stepBatchesMu.Lock()
	if g.stepBatches == nil {
		g.stepBatchesMu.Unlock()
		return
	}
	b, ok := g.stepBatches[session]
	if ok && gen != 0 && b.timerGen != gen {
		g.stepBatchesMu.Unlock()
		return
	}
	if !ok || len(b.targets) == 0 {
		delete(g.stepBatches, session)
		g.stepBatchesMu.Unlock()
		return
	}
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	toSend := b.targets
	delete(g.stepBatches, session)
	g.stepBatchesMu.Unlock()

	if g.onStepBatchFlushed != nil {
		g.onStepBatchFlushed(session)
	}
	g.sendStepTaskBatch(ctx, toSend)
}

func (g *Gateway) flushStepTaskBatches() {
	g.stepBatchesMu.Lock()
	if g.stepBatches == nil {
		g.stepBatchesMu.Unlock()
		return
	}
	var sessions []string
	for s := range g.stepBatches {
		sessions = append(sessions, s)
	}
	g.stepBatchesMu.Unlock()

	for _, s := range sessions {
		g.flushStepTasks(s)
	}
}

func (g *Gateway) sendStepTaskBatch(ctx context.Context, targets []nodeTaskTarget) {
	if len(targets) == 0 {
		return
	}
	cap := g.batchCap()
	for len(targets) > 0 {
		n := len(targets)
		if n > cap {
			n = cap
		}
		chunk := targets[:n]
		targets = targets[n:]
		m, err := g.store.storeStepTaskMessagesInternal(chunk, true)
		if err != nil {
			g.logLine("error", "where", "step task message", "err", err)
			continue
		}
		if m.ID != "" {
			g.dispatchNudge(ctx, m)
			g.logStepSent(chunk)
		}
	}
}

// logStepSent says in the Desk log that the gateway sent step work by itself.
func (g *Gateway) logStepSent(targets []nodeTaskTarget) {
	if len(targets) == 0 {
		return
	}
	aboutKind, aboutID, text := "", "", fmt.Sprintf("Sent %d delivery steps to a session", len(targets))
	if len(targets) == 1 {
		aboutKind, aboutID = protocol.AskAboutChange, targets[0].id
		text = fmt.Sprintf("Sent step %s of change %s to a session", targets[0].ref.String(), targets[0].id)
	}
	if err := g.store.deskLog(g.store.db, "gateway", aboutKind, aboutID, text, "", nil); err != nil {
		g.logLine("error", "where", "desk log", "err", err)
	}
}

// dispatchStepTasks writes system notifications per eligible session.
// Tasks are coalesced into batches per recipient when the session has recent activity
// or reported the step before it.
func (g *Gateway) dispatchStepTasks(ctx context.Context) {
	g.nudgeMu.Lock()
	defer g.nudgeMu.Unlock()
	batch := &nudgeCollection{}
	if seed, ok := ctx.Value(nudgeSeedKey{}).(protocol.Message); ok {
		batch.messages = append(batch.messages, seed)
	}
	ctx = context.WithValue(ctx, nudgeCollectionKey{}, batch)
	defer g.flushNudges(ctx, batch)

	g.stepDispatchRuns.Add(1)
	defer g.slowLoop("dispatchStepTasks", time.Now())
	// End tasks the change's state has already made unreportable before
	// choosing what to hand out; their notices must not become reminders.
	if err := g.store.retireObsoleteStepTasks(); err != nil {
		g.logLine("error", "where", "retire obsolete step tasks", "err", err)
	}
	snap, err := g.store.SettingsSnapshot()
	stuckLimit := 12 * time.Hour
	if err == nil {
		stuckLimit = snap.Duration("delivery.step_stuck", settings.Where{})
	}
	reminderCutoff := g.store.stepReminderCutoff()

	g.wakeOfflineStepOwners(ctx, reminderCutoff)
	candidateCutoff := reminderCutoff
	if candidateCutoff != 0 {
		candidateCutoff += int64(nudgeLookahead)
	}
	rows, err := g.store.db.Query(`SELECT t.change_id,t.revision,t.step,t.for_step,t.after_merge,t.owner_node,s.id,t.urgent,t.created_at,
		EXISTS(SELECT 1 FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id
			WHERE d.change_id=t.change_id AND d.revision=t.revision AND d.step=t.step AND d.for_step=t.for_step
			AND d.after_merge=t.after_merge AND d.session_id=s.id AND m.state<>'expired'),
        EXISTS(SELECT 1 FROM delivery_step_messages d WHERE d.change_id=t.change_id AND d.revision=t.revision
        AND d.step=t.step AND d.for_step=t.for_step AND d.after_merge=t.after_merge AND d.session_id=s.id)
		FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
		JOIN sessions s ON s.online=1 AND s.worker='' LEFT JOIN profiles p ON p.session_id=s.id
		WHERE `+g.store.stepNotWaitingAt()+` AND t.completed=0 AND t.claimed_by='' AND c.state IN ('open','merged')
		AND ((t.owner_node='/merger' AND EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.project=c.project AND pm.node_id=c.machine AND pm.session_id=s.id)) OR (t.owner_node=? AND s.id=c.caller) OR `+sessionRepoSQL+`=t.owner_node OR
		 (t.owner_node=? AND EXISTS(SELECT 1 FROM delivery_step_delegates a WHERE a.change_id=t.change_id AND a.revision=t.revision
		 AND a.step=t.step AND a.for_step=t.for_step AND a.after_merge=t.after_merge AND (a.node=`+sessionRepoSQL+` OR a.node='agent:'||COALESCE(s.agent_id,'')))))
		AND s.harness NOT IN ('owner','bot','cto')
        AND ((t.step='verify' AND t.after_merge=1 AND ?<>0) OR NOT EXISTS (SELECT 1 FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id
        WHERE d.change_id=t.change_id AND d.revision=t.revision AND d.step=t.step AND d.for_step=t.for_step
        AND d.after_merge=t.after_merge AND d.session_id=s.id AND m.state<>'expired' AND (?=0 OR m.created_at>=?)))`, callerTaskOwner, delegateTaskOwner, candidateCutoff, candidateCutoff, candidateCutoff)
	if err != nil {
		g.logLine("error", "where", "step task targets", "err", err)
		return
	}
	var targets []nodeTaskTarget
	for rows.Next() {
		var target nodeTaskTarget
		var after int
		var urgentInt int
		var isReminderInt int
		if err = rows.Scan(&target.id, &target.revision, &target.ref.Step, &target.ref.For, &after, &target.owner, &target.session, &urgentInt, &target.createdAt, &isReminderInt, &target.wasNotified); err != nil {
			break
		}
		target.ref.AfterMerge = after != 0
		target.urgent = urgentInt != 0
		target.isReminder = isReminderInt != 0
		targets = append(targets, target)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		g.logLine("error", "where", "step task targets", "err", err)
		return
	}

	dueSessions, err := g.store.dueNudgeSessions(stuckLimit)
	if err != nil {
		g.logLine("error", "where", "nudge due sessions", "err", err)
		return
	}
	forceFlush := map[string]bool{}
	for session := range dueSessions {
		forceFlush[session] = true
	}
	for _, m := range batch.messages {
		forceFlush[m.To] = true
		dueSessions[m.To] = true
	}
	candidates := map[string][]nodeTaskTarget{}
	horizon := g.store.now().Add(nudgeLookahead)
	futureCutoff := reminderCutoff
	if futureCutoff != 0 {
		futureCutoff += int64(nudgeLookahead)
	}
	pausedReminder := g.store.nudgeLookup(g.store.db, "step_reminder")
	for _, target := range targets {
		if target.isReminder || target.wasNotified {
			if paused, err := pausedReminder(target.session); err != nil {
				g.logLine("error", "where", "step reminder pause", "err", err)
				continue
			} else if paused {
				continue
			}
		}
		recorded, err := g.store.stepMessageRecorded(target, reminderCutoff)
		if err != nil {
			continue
		}
		if recorded {
			future, err := g.store.stepMessageRecordedAt(target, futureCutoff, horizon)
			if err != nil || future {
				continue
			}
			target.pullForward = true
		} else {
			dueSessions[target.session] = true
			if target.isReminder {
				forceFlush[target.session] = true
			}
		}
		if target.ref.AfterMerge && target.ref.Step == protocol.StepVerify {
			var last int64
			if err := g.store.db.QueryRow(`SELECT MAX(last_notice_at,last_notice_due_at) FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=1`, target.id, target.revision, target.ref.Step, target.ref.For).Scan(&last); err != nil {
				continue
			}
			if last != 0 {
				target.noticeDue = verifyReminderDue(time.Unix(target.createdAt, 0), time.Unix(0, last)).UnixNano()
			}
		}
		candidates[target.session] = append(candidates[target.session], target)
	}
	groups := map[string][]nodeTaskTarget{}
	for session, ts := range candidates {
		if dueSessions[session] {
			groups[session] = ts
		}
	}
	ctx = context.WithValue(ctx, nudgeDueKey{}, dueSessions)
	g.checkStuckStepTasks(ctx, stuckLimit)
	reminders, err := g.store.remindPendingBefore(horizon, dueSessions)
	if err != nil {
		g.logLine("error", "where", "pending reminders", "err", err)
	}
	for _, m := range reminders {
		g.dispatchNudge(ctx, m)
	}

	recentBySession := map[string]bool{}
	for session := range groups {
		recent, _ := g.store.sessionHasRecentStepMessage(session, g.recentWindow())
		recentBySession[session] = recent
	}

	cap := g.batchCap()
	quiet := g.quietWindow()

	var urgentTargets []nodeTaskTarget
	var immediateSingle []nodeTaskTarget
	var immediateBatches [][]nodeTaskTarget
	var timerSchedules []string

	g.stepBatchesMu.Lock()
	if g.stepBatches == nil {
		g.stepBatches = make(map[string]*stepTaskBatch)
	}

	for session, group := range groups {
		var batchable []nodeTaskTarget
		for _, t := range group {
			if t.urgent {
				urgentTargets = append(urgentTargets, t)
			} else {
				batchable = append(batchable, t)
			}
		}

		if len(batchable) == 0 {
			continue
		}

		recent := recentBySession[session]
		b, hasBatch := g.stepBatches[session]

		if !recent && (!hasBatch || len(b.targets) == 0) {
			if len(batchable) == 1 {
				immediateSingle = append(immediateSingle, batchable[0])
			} else {
				immediateBatches = append(immediateBatches, batchable)
			}
			continue
		}

		if !hasBatch {
			b = &stepTaskBatch{session: session}
			g.stepBatches[session] = b
		}
		for _, t := range batchable {
			if !targetInBatch(b.targets, t) {
				b.targets = append(b.targets, t)
			}
		}
		for len(b.targets) >= cap {
			chunk := b.targets[:cap]
			b.targets = b.targets[cap:]
			immediateBatches = append(immediateBatches, chunk)
		}
		if len(b.targets) == 0 {
			if b.timer != nil {
				b.timer.Stop()
				b.timer = nil
			}
			delete(g.stepBatches, session)
		} else {
			timerSchedules = append(timerSchedules, session)
		}
	}

	for _, session := range timerSchedules {
		b := g.stepBatches[session]
		if b != nil {
			sess := session
			if b.timer != nil {
				b.timer.Stop()
			}
			g.stepBatchGen++
			gen := g.stepBatchGen
			b.timerGen = gen
			b.timer = time.AfterFunc(quiet, func() {
				g.flushStepTasksArmed(sess, gen)
			})
		}
	}
	g.stepBatchesMu.Unlock()

	for _, t := range urgentTargets {
		m, err := g.store.storeStepTaskMessagesInternal([]nodeTaskTarget{t}, false)
		if err != nil {
			g.logLine("error", "where", "step task message", "err", err)
			continue
		}
		_ = g.store.clearTaskUrgent(t)
		if m.ID != "" {
			g.dispatchNudge(ctx, m)
			g.logStepSent([]nodeTaskTarget{t})
		}
	}
	for _, t := range immediateSingle {
		m, err := g.store.storeStepTaskMessagesInternal([]nodeTaskTarget{t}, false)
		if err != nil {
			g.logLine("error", "where", "step task message", "err", err)
			continue
		}
		if m.ID != "" {
			g.dispatchNudge(ctx, m)
			g.logStepSent([]nodeTaskTarget{t})
		}
	}
	for _, batch := range immediateBatches {
		g.sendStepTaskBatch(ctx, batch)
	}
	for session := range forceFlush {
		// A burst that already has a notice out stays in its quiet window.
		// Flushing it here would cut the same burst into two step-task
		// messages just because an unrelated nudge is due.
		if recentBySession[session] {
			continue
		}
		g.flushStepTasksArmedContext(ctx, session, 0)
	}
}

// checkStuckStepTasks sends a notice to the caller session of any change whose
// ready delivery step has remained unclaimed past the stuck limit.
func (g *Gateway) checkStuckStepTasks(ctx context.Context, stuckLimit time.Duration) {
	if stuckLimit <= 0 {
		return
	}
	now := g.store.now()
	cutoff := now.Add(-stuckLimit).Unix()
	dueSessions, _ := ctx.Value(nudgeDueKey{}).(map[string]bool)
	if dueSessions != nil {
		cutoff += int64(nudgeLookahead / time.Second)
	}
	rows, err := g.store.db.Query(`SELECT t.change_id, t.revision, t.step, t.for_step, t.after_merge, t.owner_node, t.created_at, c.caller, c.title
		FROM delivery_step_tasks t
		JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
		WHERE `+g.store.stepNotWaitingAt()+` AND t.completed=0 AND t.claimed_by='' AND (t.step<>'verify' OR t.after_merge=0 OR t.stuck_notified=0)
		AND ((t.after_merge=0 AND c.state='open') OR (t.after_merge=1 AND c.state='merged'))
		AND c.caller <> ''
		AND t.created_at <= ?
		AND EXISTS (SELECT 1 FROM sessions WHERE id=c.caller)
		AND NOT EXISTS (
			SELECT 1 FROM messages WHERE sender='agent-net' AND idem_key='delivery:stuck:' || t.change_id || ':' || t.revision || ':' || t.step || ':' || t.for_step || ':' || t.after_merge
		)`, cutoff)
	if err != nil {
		g.logLine("error", "where", "stuck step tasks query", "err", err)
		return
	}
	type stuckItem struct {
		changeID    string
		revision    int
		ref         protocol.StepRef
		owner       string
		createdAt   int64
		caller      string
		changeTitle string
	}
	var items []stuckItem
	for rows.Next() {
		var it stuckItem
		var after int
		if err := rows.Scan(&it.changeID, &it.revision, &it.ref.Step, &it.ref.For, &after, &it.owner, &it.createdAt, &it.caller, &it.changeTitle); err != nil {
			break
		}
		it.ref.AfterMerge = after != 0
		items = append(items, it)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		g.logLine("error", "where", "stuck step tasks scan", "err", err)
		return
	}

	for _, it := range items {
		if paused, err := g.store.nudgePaused(g.store.db, "stuck_step", it.caller); err != nil {
			g.logLine("error", "where", "stuck step pause", "err", err)
			continue
		} else if paused {
			continue
		}
		if dueSessions != nil && !dueSessions[it.caller] {
			continue
		}
		idemKey := fmt.Sprintf("delivery:stuck:%s:%d:%s:%s:%d", it.changeID, it.revision, it.ref.Step, it.ref.For, boolInt(it.ref.AfterMerge))
		age := now.Sub(time.Unix(it.createdAt, 0))
		ageStr := formatAge(age)
		displayOwner := taskDisplayOwner(it.owner)
		title := protocol.NeutralizeMarkers(it.changeTitle)
		prefix := title + " (" + it.changeID + "): "
		if title == "" {
			prefix = it.changeID + ": "
		}
		body := fmt.Sprintf("%s%s has waited %s for %s; nobody claimed it.",
			prefix, it.ref.String(), ageStr, displayOwner)

		m := protocol.Message{
			ID:             protocol.NewID("m"),
			ConversationID: protocol.NewID("c"),
			From:           "agent-net",
			FromName:       "agent-net",
			FromHarness:    "agent-net",
			To:             it.caller,
			Type:           protocol.TypeNotification,
			Body:           body,
			IdempotencyKey: idemKey,
			CreatedAt:      now.UTC(),
			ExpiresAt:      now.Add(protocol.DefaultTTL).UTC(),
			State:          protocol.StateStored,
		}

		tx, err := g.store.db.Begin()
		if err != nil {
			g.logLine("error", "where", "stuck step notice begin", "err", err)
			continue
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
			VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
			m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State)
		if err != nil {
			tx.Rollback()
			g.logLine("error", "where", "stuck step notice insert", "err", err)
			continue
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			tx.Rollback()
			continue
		}
		if err := recordNudgeFire(tx, "stuck_step", m.To, m.ID, m.CreatedAt); err != nil {
			tx.Rollback()
			continue
		}
		if _, err := tx.Exec(`UPDATE delivery_step_tasks SET stuck_notified=1 WHERE step='verify' AND after_merge=1 AND change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`, it.changeID, it.revision, it.ref.Step, it.ref.For, boolInt(it.ref.AfterMerge)); err != nil {
			tx.Rollback()
			continue
		}
		if err := tx.Commit(); err != nil {
			g.logLine("error", "where", "stuck step notice commit", "err", err)
			continue
		}
		g.store.logMessage(m)
		g.dispatchNudge(ctx, m)
	}
}

// wakeOfflineStepOwners asks only the recorded merger or the change's caller
// to resume. Project and delegate owners can have many old sessions, so their
// tasks wait for an online session. No notice is created until discovery brings
// a session online, preserving batching across its accumulated ready tasks.
func (g *Gateway) wakeOfflineStepOwners(ctx context.Context, reminderCutoff int64) {
	rows, err := g.store.db.Query(`SELECT DISTINCT s.id,
  EXISTS(SELECT 1 FROM delivery_step_messages d
   WHERE d.change_id=t.change_id AND d.revision=t.revision AND d.step=t.step AND d.for_step=t.for_step
   AND d.after_merge=t.after_merge AND d.session_id=s.id)
  FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
  JOIN sessions s ON s.online=0 AND s.harness='claude' AND s.worker=''
  WHERE `+g.store.stepNotWaitingAt()+` AND t.completed=0 AND t.claimed_by=''
  AND ((t.after_merge=0 AND c.state='open') OR (t.after_merge=1 AND c.state='merged'))
  AND ((t.owner_node=? AND s.id=c.caller) OR (t.owner_node=? AND EXISTS
   (SELECT 1 FROM project_mergers pm WHERE pm.project=c.project AND pm.node_id=c.machine AND pm.session_id=s.id)))
  AND NOT EXISTS (SELECT 1 FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id
   WHERE d.change_id=t.change_id AND d.revision=t.revision AND d.step=t.step AND d.for_step=t.for_step
   AND d.after_merge=t.after_merge AND d.session_id=s.id AND m.state<>'expired' AND (?=0 OR m.created_at>=?))`, callerTaskOwner, mergerTaskOwner, reminderCutoff, reminderCutoff)
	if err != nil {
		g.logLine("error", "where", "offline step owners", "err", err)
		return
	}
	type wakeTarget struct {
		id       string
		reminder bool
	}
	var ids []wakeTarget
	for rows.Next() {
		var target wakeTarget
		if err = rows.Scan(&target.id, &target.reminder); err != nil {
			break
		}
		ids = append(ids, target)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		g.logLine("error", "where", "offline step owners", "err", err)
		return
	}
	seen := map[string]bool{}
	pausedReminder := g.store.nudgeLookup(g.store.db, "step_reminder")
	for _, target := range ids {
		if seen[target.id] {
			continue
		}
		if target.reminder {
			if paused, err := pausedReminder(target.id); err != nil || paused {
				continue
			}
		}
		seen[target.id] = true
		a, ok, err := g.store.Agent(target.id)
		if err != nil {
			g.logLine("error", "where", "offline step owner", "err", err)
			continue
		}
		if ok {
			g.wakeSession(ctx, a)
		}
	}
}

// stepRun returns the steps to hand out in one task: ref, which has just
// become ready, and at most the next step that may follow it in the same task.
// That step must have the same owner, run in the same phase, and wait only for
// ref. Steps left out of this change's pipeline do not break the pairing, but
// the pair never crosses the merge.
func stepRun(p protocol.Pipeline, c protocol.Change, evidence []protocol.Evidence, ref protocol.StepRef) []protocol.StepRef {
	status := p.Status(c, evidence)
	start := -1
	for i := range p.Steps {
		if p.Steps[i].StepRef == ref {
			start = i
			break
		}
	}
	if start < 0 {
		return []protocol.StepRef{ref}
	}
	run := []protocol.StepRef{ref}
	hypothetical := append([]protocol.Evidence(nil), evidence...)
	previous := p.Steps[start]
	for i := start + 1; i < len(p.Steps); i++ {
		next := p.Steps[i]
		if status[i].State == protocol.StepStateSkipped {
			continue
		}
		if next.AfterMerge != p.Steps[start].AfterMerge || next.Owner != previous.Owner || status[i].State != protocol.StepStateWaiting {
			break
		}
		var requires []protocol.Requirement
		for _, r := range next.Requires {
			if r.StepRef == previous.StepRef {
				requires = append(requires, r)
			}
		}
		if len(requires) == 0 {
			break
		}
		hypothetical = append(hypothetical, hypotheticalPass(c, previous, requires))
		if ok, _ := p.Ready(next.StepRef, c, hypothetical); !ok {
			break
		}
		run = append(run, next.StepRef)
		break
	}
	return run
}

// hypotheticalPass is the pass that previous would report, carrying every
// field the next step requires of it. Ready then says whether previous is the
// only thing still missing.
func hypotheticalPass(c protocol.Change, previous protocol.PipelineStep, requires []protocol.Requirement) protocol.Evidence {
	commit := c.HeadCommit
	if previous.AfterMerge {
		commit = c.MergedCommit
	}
	e := protocol.Evidence{ChangeID: c.ID, Revision: c.Revision, StepRef: previous.StepRef,
		Result: protocol.StepResultPass, Commit: commit, Fields: map[string]string{}}
	for _, r := range requires {
		for k, v := range r.Fields {
			if v == protocol.FieldAny {
				v = "set"
			}
			e.Fields[k] = v
		}
	}
	if previous.Independent != "" {
		e.Fields[protocol.FieldIndependent] = previous.Independent
	}
	if len(e.Fields) == 0 {
		e.Fields = nil
	}
	return e
}

func stepRefName(r protocol.StepRef) string {
	name := r.Step
	if r.For != "" {
		name += " for " + r.For
	}
	return name
}

func stepRunNames(run []protocol.StepRef) []string {
	names := make([]string, 0, len(run))
	for _, r := range run {
		names = append(names, stepRefName(r))
	}
	return names
}

// stepReadyNote says when the run's first step became ready. Only that step is
// ready; the rest are the then steps of the same task.
func stepReadyNote(readySince time.Time, run []protocol.StepRef) string {
	note := "ready since " + readySince.UTC().Format(time.RFC3339) + ", not claimed"
	if len(run) > 1 {
		note += ", then " + strings.Join(stepRunNames(run[1:]), ", then ") + " (not ready yet)"
	}
	return note
}

// stepThenNote names a step that still waits on another step of its run.
func stepThenNote(run []protocol.StepRef) string {
	return "then " + strings.Join(stepRunNames(run), ", then ") + " (not ready yet)"
}

// stepTaskDeliverable reports whether a task selected before a batch was
// flushed is still waiting for this session: current revision, unclaimed,
// unfinished, not waiting, and in the phase the task belongs to.
func (s *Store) stepTaskDeliverable(t nodeTaskTarget) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_tasks t
		JOIN delivery_changes c ON c.id=t.change_id
		WHERE t.change_id=? AND t.revision=? AND t.step=? AND t.for_step=? AND t.after_merge=?
		AND t.revision=c.revision AND t.completed=0 AND t.claimed_by='' AND t.owner_node=?
		AND `+s.stepNotWaitingAt()+`
		AND ((t.after_merge=0 AND c.state='open') OR (t.after_merge=1 AND c.state='merged')))`,
		t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge), t.owner).Scan(&n)
	return n != 0, err
}

func (s *Store) storeStepTaskMessage(t nodeTaskTarget) (protocol.Message, error) {
	return s.storeStepTaskMessages([]nodeTaskTarget{t})
}

func (s *Store) storeStepTaskMessages(targets []nodeTaskTarget) (protocol.Message, error) {
	return s.storeStepTaskMessagesInternal(targets, false)
}

// stepSignalBlock is the step task's quality-signal block, one line per
// signal, or "" when the revision has none. It shows the same lines as the
// TUI: strength and evidence, never a reason to block.
func (s *Store) stepSignalBlock(id string, revision int) (string, error) {
	signals, err := changeSignals(s.db, id, revision)
	if err != nil || len(signals) == 0 {
		return "", err
	}
	return "quality signals:\n" + strings.Join(protocol.SignalLines(signals), "\n"), nil
}

func (s *Store) storeStepTaskMessagesInternal(targets []nodeTaskTarget, forceBatch bool) (protocol.Message, error) {
	type notice struct {
		targets    []nodeTaskTarget
		body, line string
		order      string
		isReminder bool
	}
	type details struct {
		change   protocol.Change
		pipeline protocol.Pipeline
		evidence []protocol.Evidence
	}
	targetID := func(t nodeTaskTarget) string {
		return fmt.Sprintf("%s|%d|%s|%s|%t|%s", t.id, t.revision, t.ref.Step, t.ref.For, t.ref.AfterMerge, t.session)
	}
	loaded := map[string]*details{}
	pipelines := map[string]protocol.Pipeline{}
	stepIndex := func(p protocol.Pipeline, ref protocol.StepRef) int {
		for i := range p.Steps {
			if p.Steps[i].StepRef == ref {
				return i
			}
		}
		return len(p.Steps)
	}
	queue := append([]nodeTaskTarget(nil), targets...)
	for _, t := range queue {
		if _, ok := loaded[t.id]; ok {
			continue
		}
		c, found, err := queryChange(s.db, `id=?`, t.id)
		if err != nil {
			return protocol.Message{}, err
		}
		if !found {
			loaded[t.id] = nil
			continue
		}
		p, ok := pipelines[c.Project]
		if !ok {
			p, err = s.deliveryPipeline(c.Project)
			if err != nil {
				return protocol.Message{}, err
			}
			pipelines[c.Project] = p
		}
		evidence, err := changeEvidence(s.db, c.ID)
		if err != nil {
			return protocol.Message{}, err
		}
		loaded[t.id] = &details{change: c, pipeline: p, evidence: evidence}
	}
	// A dispatch pass can select a task while another pass is flushing the
	// same batch, before that message and its rows are committed. Re-check at
	// write time so a step already recorded is not sent again.
	cutoff := s.stepReminderCutoff()
	kept := queue[:0]
	pausedReminder := s.nudgeLookup(s.db, "step_reminder")
	for _, t := range queue {
		recorded, err := s.stepMessageRecorded(t, cutoff)
		if err != nil {
			return protocol.Message{}, err
		}
		if recorded && t.pullForward {
			futureCutoff := cutoff
			if futureCutoff != 0 {
				futureCutoff += int64(nudgeLookahead)
			}
			recorded, err = s.stepMessageRecordedAt(t, futureCutoff, s.now().Add(nudgeLookahead))
			if err != nil {
				return protocol.Message{}, err
			}
		}
		if recorded {
			continue
		}
		// A claim or a report can land while a batch waits in its quiet window.
		// The task is no longer this session's to hand out then.
		deliverable, err := s.stepTaskDeliverable(t)
		if err != nil {
			return protocol.Message{}, err
		}
		if !deliverable {
			continue
		}
		if !t.isReminder {
			var prior bool
			if err := s.db.QueryRow(`SELECT COALESCE(MAX(m.state<>'expired'),0),COUNT(d.message_id)>0
                FROM delivery_step_messages d LEFT JOIN messages m ON m.id=d.message_id
				WHERE d.change_id=? AND d.revision=? AND d.step=? AND d.for_step=? AND d.after_merge=? AND d.session_id=?`,
				t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge), t.session).Scan(&prior, &t.wasNotified); err != nil {
				return protocol.Message{}, err
			}
			t.isReminder = prior
		}
		if t.isReminder || t.wasNotified {
			if paused, err := pausedReminder(t.session); err != nil {
				return protocol.Message{}, err
			} else if paused {
				continue
			}
		}
		kept = append(kept, t)
	}
	queue = kept
	// Pipeline order keeps a pair together whatever order the dispatch query
	// returned the tasks in.
	sort.SliceStable(queue, func(a, b int) bool {
		ta, tb := queue[a], queue[b]
		if ta.id != tb.id {
			return ta.id < tb.id
		}
		da, db := loaded[ta.id], loaded[tb.id]
		if da == nil || db == nil {
			return false
		}
		return stepIndex(da.pipeline, ta.ref) < stepIndex(db.pipeline, tb.ref)
	})
	var notices []notice
	seen := map[string]bool{}
	isBatch := forceBatch || len(targets) > 1
	for i, t := range queue {
		if seen[targetID(t)] {
			continue
		}
		seen[targetID(t)] = true
		d := loaded[t.id]
		if d == nil {
			continue
		}
		c := d.change
		if c.Revision != t.revision || (t.ref.AfterMerge && c.State != protocol.ChangeMerged) || (!t.ref.AfterMerge && c.State != protocol.ChangeOpen) {
			continue
		}
		// A task whose pair partner was claimed or reported before the batch
		// flushed is only the next step of a run. It is not ready on its own,
		// so a reminder must not announce it as ready.
		firstReady, _ := d.pipeline.Ready(t.ref, c, d.evidence)
		if t.createdAt == 0 {
			_ = s.db.QueryRow(`SELECT created_at FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
				t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge)).Scan(&t.createdAt)
		}
		commit := c.HeadCommit
		if t.ref.AfterMerge {
			commit = c.MergedCommit
		}
		// Hand out the next step after this one in the same task when it waits
		// only for this step and the same session owns it.
		group := []nodeTaskTarget{t}
		run := []protocol.StepRef{t.ref}
		for _, ref := range stepRun(d.pipeline, c, d.evidence, t.ref)[1:] {
			paired := -1
			for j := i + 1; j < len(queue); j++ {
				candidate := queue[j]
				if seen[targetID(candidate)] || candidate.id != t.id || candidate.revision != t.revision ||
					candidate.session != t.session || candidate.ref != ref {
					continue
				}
				paired = j
				break
			}
			if paired < 0 {
				break
			}
			seen[targetID(queue[paired])] = true
			group = append(group, queue[paired])
			run = append(run, ref)
		}
		displayOwner := taskDisplayOwner(t.owner)
		readyNote := stepReadyNote(time.Unix(t.createdAt, 0), run)
		if !firstReady {
			readyNote = stepThenNote(run)
		}
		if len(group) == 1 {
			// A reminder for a step that still waits on another step has
			// nothing to act on. Keep handing it out as a first notice only.
			if t.isReminder && !firstReady {
				continue
			}
			line := fmt.Sprintf("change: %s | step: %s | after_merge: %t | commit: %s | title: %s", c.ID, t.ref.String(), t.ref.AfterMerge, commit, protocol.NeutralizeMarkers(c.Title))
			if t.isReminder {
				line += " | " + readyNote
			}
			stLine := s.changeStruggleLine(c, []protocol.StepRef{t.ref})
			var body string
			if !isBatch {
				encoded, err := json.Marshal(d.evidence)
				if err != nil {
					return protocol.Message{}, err
				}
				var stat string
				err = s.db.QueryRow(`SELECT diff_stat FROM delivery_revision_stats WHERE change_id=? AND revision=?`, c.ID, t.revision).Scan(&stat)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return protocol.Message{}, err
				}
				if stat == "" {
					stat = "not available"
				}
				signalText, err := s.stepSignalBlock(c.ID, t.revision)
				if err != nil {
					return protocol.Message{}, err
				}
				if signalText != "" {
					stat += "\n" + signalText
				}
				header := "[agent-net step task]"
				reminderNote := ""
				if t.isReminder {
					header = "[agent-net step task reminder]"
					reminderNote = "\n" + readyNote
				}
				body = fmt.Sprintf("%s%s\nchange: %s\nproject: %s\nbranch: %s\nrevision: %d\ncommit: %s\nuncommitted: %t\nstep: %s\nowner: %s\ntitle: %s\ndiff stat:\n%s\nevidence: %s\nreport fields: %s\nClaim the task with agent_step_claim before starting, then report the result with agent_step_report from your session.",
					header, reminderNote, c.ID, protocol.NeutralizeMarkers(c.Project), protocol.NeutralizeMarkers(c.Branch), c.Revision, commit, c.Uncommitted, t.ref.String()+"\n"+s.stepContract(c.Project, t.ref.Step), protocol.NeutralizeMarkers(displayOwner),
					protocol.NeutralizeMarkers(c.Title), protocol.NeutralizeMarkers(stat), protocol.NeutralizeMarkers(string(encoded)), strings.Join(d.pipeline.ReportFields(t.ref), ", "))
			}
			waitNote := s.wasWaiting(group)
			body += waitNote
			line += waitNote
			if stLine != "" {
				body += "\n" + stLine
				line += " | " + stLine
			}
			notices = append(notices, notice{targets: group, body: body, line: line + "\n" + s.stepContract(c.Project, t.ref.Step), isReminder: t.isReminder})
			continue
		}
		isReminder, allReminder := false, true
		for _, gt := range group {
			if gt.isReminder {
				isReminder = true
			} else {
				allReminder = false
			}
		}
		// A group of reminders that starts with a step still waiting on another
		// step has nothing to act on. A first notice in the group keeps today's
		// hand-out behaviour.
		if allReminder && !firstReady {
			continue
		}
		names := stepRunNames(run)
		steps := strings.Join(names, ", ")
		if t.ref.AfterMerge {
			steps += " (after merge)"
		}
		encoded, err := json.Marshal(d.evidence)
		if err != nil {
			return protocol.Message{}, err
		}
		var stat string
		err = s.db.QueryRow(`SELECT diff_stat FROM delivery_revision_stats WHERE change_id=? AND revision=?`, c.ID, t.revision).Scan(&stat)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return protocol.Message{}, err
		}
		if stat == "" {
			stat = "not available"
		}
		signalText, err := s.stepSignalBlock(c.ID, t.revision)
		if err != nil {
			return protocol.Message{}, err
		}
		if signalText != "" {
			stat += "\n" + signalText
		}
		var fields []string
		for _, ref := range run {
			want := d.pipeline.ReportFields(ref)
			text := "(none)"
			if len(want) > 0 {
				text = strings.Join(want, ", ")
			}
			fields = append(fields, stepRefName(ref)+": "+text)
		}
		order := strings.Join(names, ", then ")
		line := fmt.Sprintf("change: %s | steps: %s | after_merge: %t | commit: %s | title: %s", c.ID, steps, t.ref.AfterMerge, commit, protocol.NeutralizeMarkers(c.Title))
		if isReminder {
			line += " | " + readyNote
		}
		header := "[agent-net step tasks]"
		reminderNote := ""
		if isReminder {
			header = "[agent-net step task reminder]"
			reminderNote = "\n" + readyNote
		}
		body := fmt.Sprintf("%s%s\nchange: %s\nproject: %s\nbranch: %s\nrevision: %d\ncommit: %s\nuncommitted: %t\nsteps: %s\nowner: %s\ntitle: %s\ndiff stat:\n%s\nevidence: %s\nreport fields:\n%s\nClaim and report each step in order: %s.",
			header, reminderNote, c.ID, protocol.NeutralizeMarkers(c.Project), protocol.NeutralizeMarkers(c.Branch), c.Revision, commit, c.Uncommitted, steps+"\n"+s.stepContracts(c.Project, run), protocol.NeutralizeMarkers(displayOwner),
			protocol.NeutralizeMarkers(c.Title), protocol.NeutralizeMarkers(stat), protocol.NeutralizeMarkers(string(encoded)), strings.Join(fields, "\n"), order)
		waitNote := s.wasWaiting(group)
		body += waitNote
		line += waitNote
		if stLine := s.changeStruggleLine(c, run); stLine != "" {
			body += "\n" + stLine
			line += " | " + stLine
		}
		notices = append(notices, notice{targets: group, body: body, line: line + "\n" + s.stepContracts(c.Project, run), order: order, isReminder: isReminder})
	}
	if len(notices) == 0 {
		return protocol.Message{}, nil
	}
	t := notices[0].targets[0]
	now := s.now().UTC()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: t.session, Type: protocol.TypeNotification, IdempotencyKey: protocol.NewID("step"),
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, err
	}
	defer tx.Rollback()
	var included []notice
	for _, item := range notices {
		added := false
		for _, t := range item.targets {
			// A notice can expire while its task remains unclaimed. Its association
			// must not prevent a fresh notice when the same session comes back.
			if _, err := tx.Exec(`DELETE FROM delivery_step_messages WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND session_id=?
			AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.id=delivery_step_messages.message_id AND m.state<>'expired')`,
				t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge), t.session); err != nil {
				return protocol.Message{}, err
			}
			res, err := tx.Exec(`INSERT INTO delivery_step_messages(change_id,revision,step,for_step,after_merge,session_id,message_id)
			SELECT change_id,revision,step,for_step,after_merge,?,? FROM delivery_step_tasks
			WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=0 AND claimed_by='' AND owner_node=?
			AND EXISTS(SELECT 1 FROM delivery_changes c WHERE c.id=delivery_step_tasks.change_id AND c.revision=delivery_step_tasks.revision
			AND ((delivery_step_tasks.after_merge=0 AND c.state='open') OR (delivery_step_tasks.after_merge=1 AND c.state='merged')))
			ON CONFLICT(change_id,revision,step,for_step,after_merge,session_id) DO UPDATE SET message_id=excluded.message_id`,
				t.session, m.ID, t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge), t.owner)
			if err != nil {
				return protocol.Message{}, err
			}
			if n, err := res.RowsAffected(); err != nil {
				return protocol.Message{}, err
			} else if n > 0 {
				added = true
				if _, err := tx.Exec(`UPDATE delivery_step_tasks SET last_notice_at=?,last_notice_due_at=MAX(last_notice_due_at,?) WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`, s.now().UnixNano(), t.noticeDue, t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge)); err != nil {
					return protocol.Message{}, err
				}
			}
		}
		if added {
			included = append(included, item)
		}
	}
	if len(included) == 0 {
		return protocol.Message{}, nil
	}
	if len(included) == 1 && included[0].body != "" {
		m.Body = included[0].body
	} else {
		hasReminder := false
		for _, item := range included {
			if item.isReminder {
				hasReminder = true
				break
			}
		}
		header := "[agent-net step tasks]"
		if hasReminder {
			header = "[agent-net step task reminders]"
		}
		lines := []string{header}
		for _, item := range included {
			lines = append(lines, item.line)
		}
		for _, item := range included {
			if item.order == "" {
				continue
			}
			prefix := ""
			if len(included) > 1 {
				prefix = "Change " + item.targets[0].id + ": "
			}
			lines = append(lines, prefix+"Claim and report each step in order: "+item.order+".")
		}
		lines = append(lines, "Claim each task with agent_step_claim before starting, then report the result with agent_step_report from your session. Use agent_changes_find for change details.")
		m.Body = strings.Join(lines, "\n")
	}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		return protocol.Message{}, err
	}
	if strings.HasPrefix(m.Body, "[agent-net step task reminder") {
		if err = recordNudgeFire(tx, "step_reminder", m.To, m.ID, m.CreatedAt); err != nil {
			return protocol.Message{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return protocol.Message{}, err
	}
	s.logMessage(m)
	return m, nil
}

// queueSteps is called in the same transaction as a submit or report.
func (s *Store) queueSteps(tx *sql.Tx, c protocol.Change, p protocol.Pipeline, now int64) error {
	evidence, err := changeEvidence(tx, c.ID)
	if err != nil {
		return err
	}
	// A step whose phase has ended can no longer be reported: a before-merge
	// step once the change merged, and any step once it is done or stopped.
	// Complete it even when it was claimed, so no reminder or notice outlives
	// the phase it belonged to.
	if _, err := tx.Exec(`UPDATE delivery_step_tasks SET completed=1 WHERE change_id=? AND revision=? AND completed=0
		AND ((after_merge=0 AND ?<>'open') OR (after_merge=1 AND ?<>'merged'))`,
		c.ID, c.Revision, c.State, c.State); err != nil {
		return err
	}
	for _, step := range p.Steps {
		if step.Step == protocol.StepApprove && step.Owner.Kind == protocol.OwnerNone {
			if err := dismissStepInbox(tx, c, step.StepRef, s.now().UnixNano()); err != nil {
				return err
			}
		}
	}
	shared := false
	for _, step := range p.Steps {
		if step.Owner.Kind != protocol.OwnerCaller && step.Owner.Kind != protocol.OwnerNone {
			shared = true
			break
		}
	}
	// Retire unclaimed work outside the ready steps and their paired partners. Claims stay
	// with their current owner; retired tasks can be queued again when ready.
	ready := p.NextSteps(c, evidence)
	args := []any{c.ID, c.Revision}
	query := `UPDATE delivery_step_tasks SET completed=1 WHERE change_id=? AND revision=? AND claimed_by='' AND completed=0`
	keep := append([]protocol.StepRef(nil), ready...)
	for _, ref := range ready {
		keep = append(keep, stepRun(p, c, evidence, ref)[1:]...)
	}
	for _, ref := range keep {
		query += ` AND NOT (step=? AND for_step=? AND after_merge=?)`
		args = append(args, ref.Step, ref.For, boolInt(ref.AfterMerge))
	}
	if _, err := tx.Exec(query, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched') AND id IN (SELECT m.message_id FROM delivery_step_messages m JOIN delivery_step_tasks t ON t.change_id=m.change_id AND t.revision=m.revision AND t.step=m.step AND t.for_step=m.for_step AND t.after_merge=m.after_merge WHERE t.change_id=? AND t.revision=? AND t.completed=1 AND t.claimed_by='')`, c.ID, c.Revision); err != nil {
		return err
	}
	for _, ref := range p.NextSteps(c, evidence) {
		step, ok := p.Find(ref)
		if !ok {
			continue
		}
		if step.Owner.Kind == protocol.OwnerPerson || (step.Owner.Kind == protocol.OwnerDelegate && ref.Step == protocol.StepApprove) {
			if step.Owner.Kind == protocol.OwnerPerson {
				if _, err := tx.Exec(`UPDATE delivery_step_tasks SET completed=1 WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=0`,
					c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)); err != nil {
					return err
				}
			}
			if err := s.queueOwnerApproval(tx, c, ref); err != nil {
				return err
			}
			if step.Owner.Kind == protocol.OwnerDelegate {
				if err := s.queueDelegateTask(tx, c, ref, now); err != nil {
					return err
				}
			}
			continue
		}
		ownerNode := step.Owner.Node
		if step.Owner.Kind == protocol.OwnerCaller && shared {
			ownerNode = callerTaskOwner
		} else if step.Owner.Kind == protocol.OwnerMerger {
			ownerNode = mergerTaskOwner
		} else if step.Owner.Kind != protocol.OwnerNode {
			continue
		}
		var retired int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=1 AND claimed_by='')`, c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&retired); err != nil {
			return err
		}
		if retired != 0 {
			if err := discardCompletedStepTask(tx, c, ref); err != nil {
				return err
			}
		}
		if ref.Step == protocol.StepApprove {
			if err := dismissStepInbox(tx, c, ref, s.now().UnixNano()); err != nil {
				return err
			}
			if err := discardCompletedStepTask(tx, c, ref); err != nil {
				return err
			}
		}
		result, err := tx.Exec(`INSERT OR IGNORE INTO delivery_step_tasks
			(change_id,revision,step,for_step,after_merge,owner_node,created_at) VALUES(?,?,?,?,?,?,?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), ownerNode, now)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 0 {
			// The step was already handed out; a later step that becomes ready
			// now is its own task, not another partner for an old notice.
			continue
		}
		// Hand the next step out with this one when it needs only this step.
		// Each step keeps its own task row, evidence and order.
		for _, next := range stepRun(p, c, evidence, ref)[1:] {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO delivery_step_tasks
				(change_id,revision,step,for_step,after_merge,owner_node,created_at) VALUES(?,?,?,?,?,?,?)`,
				c.ID, c.Revision, next.Step, next.For, boolInt(next.AfterMerge), ownerNode, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// discardCompletedStepTask removes a retired task so a newly ready step can
// be queued again. Completed evidence itself is never removed.
func discardCompletedStepTask(tx *sql.Tx, c protocol.Change, ref protocol.StepRef) error {
	args := []any{c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)}
	where := `change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`
	if _, err := tx.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched') AND id IN
		(SELECT m.message_id FROM delivery_step_messages m JOIN delivery_step_tasks t ON t.change_id=m.change_id AND t.revision=m.revision
		AND t.step=m.step AND t.for_step=m.for_step AND t.after_merge=m.after_merge
		WHERE m.change_id=? AND m.revision=? AND m.step=? AND m.for_step=? AND m.after_merge=? AND t.completed=1)`, args...); err != nil {
		return err
	}
	for _, table := range []string{"delivery_step_messages", "delivery_step_delegates"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE `+where+` AND EXISTS (SELECT 1 FROM delivery_step_tasks t WHERE
			t.change_id=`+table+`.change_id AND t.revision=`+table+`.revision AND t.step=`+table+`.step
			AND t.for_step=`+table+`.for_step AND t.after_merge=`+table+`.after_merge AND t.completed=1)`, args...); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`DELETE FROM delivery_step_tasks WHERE `+where+` AND completed=1`, args...)
	return err
}

func (s *Store) queueDelegateTask(tx *sql.Tx, c protocol.Change, ref protocol.StepRef, now int64) error {
	if err := discardCompletedStepTask(tx, c, ref); err != nil {
		return err
	}
	// A previously queued node approval is no longer sufficient for a
	// delegated rule. Retire its notices before replacing it with delegates.
	var previous string
	err := tx.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous != "" && previous != delegateTaskOwner {
		if _, err := tx.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched') AND id IN
			(SELECT message_id FROM delivery_step_messages WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)); err != nil {
			return err
		}
		for _, table := range []string{"delivery_step_messages", "delivery_step_tasks"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
				c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)); err != nil {
				return err
			}
		}
	}
	result, err := tx.Exec(`INSERT OR IGNORE INTO delivery_step_tasks(change_id,revision,step,for_step,after_merge,owner_node,created_at)
		VALUES(?,?,?,?,?,?,?)`, c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), delegateTaskOwner, now)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted == 0 {
		return err
	}
	delegates, err := delegateAgents(tx, c)
	if err != nil {
		return err
	}
	for _, node := range delegates {
		if _, err := tx.Exec(`INSERT INTO delivery_step_delegates(change_id,revision,step,for_step,after_merge,node) VALUES(?,?,?,?,?,?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), node); err != nil {
			return err
		}
	}
	return nil
}

// delegateAgents lists the agents above the change's repo in the tree: the
// repo lead and the lead of the repo's team. Each holds merge unless the owner
// holds it back, and each is written as "agent:<id>". The caller is never its
// own approver (decision 0089).
func delegateAgents(tx *sql.Tx, c protocol.Change) ([]string, error) {
	var caller string
	if err := tx.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=?`, c.Caller).Scan(&caller); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var out []string
	add := func(agent string) {
		if agent == "" || agent == caller {
			return
		}
		for _, have := range out {
			if have == "agent:"+agent {
				return
			}
		}
		out = append(out, "agent:"+agent)
	}
	var repoID, repoName string
	var lead, pinned sql.NullString
	err := tx.QueryRow(repoLeadQuery(`id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`), c.Project).Scan(&repoID, &repoName, &lead, &pinned)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && lead.Valid {
		add(lead.String)
	}
	_, _, _, _, owner, _, err := repoTeam(tx, c.Project)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && owner.Valid {
		add(owner.String)
	}
	return out, nil
}

func (s *Store) queueOwnerApproval(tx *sql.Tx, c protocol.Change, ref protocol.StepRef) error {
	var exists int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_inbox WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&exists); err != nil || exists != 0 {
		if err != nil {
			return err
		}
		// A rule can be re-enabled for this same revision after it was
		// relaxed. Restore an unanswered item rather than creating a duplicate.
		_, err = tx.Exec(`UPDATE inbox_items SET status='open',answered_at=0 WHERE status='dismissed' AND answer='rule_disabled' AND id IN
			(SELECT item_id FROM delivery_step_inbox WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge))
		return err
	}
	commit := c.HeadCommit
	if ref.AfterMerge {
		commit = c.MergedCommit
	}
	item, err := s.createInboxItem(tx, protocol.InboxItem{Kind: "approval", SessionID: c.Caller, Node: c.Project, ChangeTitle: c.Title,
		Text: fmt.Sprintf("Approve %s for change %s, project %s, branch %s, commit %s", strings.TrimPrefix(ref.String(), "approve for "), c.ID, c.Project, c.Branch, commit)})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO delivery_step_inbox(change_id,revision,step,for_step,after_merge,commit_hash,item_id) VALUES(?,?,?,?,?,?,?)`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), commit, item.ID)
	return err
}

// changeStruggleLine is the struggle line the caller's accept task carries.
// The hand-back carries the counts in the change origin; a run row is the
// fallback for a change recorded before this field existed. It is empty for
// every other step, for a clean run and for a harness with no trace.
func (s *Store) changeStruggleLine(c protocol.Change, refs []protocol.StepRef) string {
	accept := false
	for _, ref := range refs {
		if ref.Step == protocol.StepAccept {
			accept = true
			break
		}
	}
	if !accept {
		return ""
	}
	return protocol.StruggleLine(s.changeStruggle(c))
}

// changeStruggle returns the run's sign count for a change, or nil.
func (s *Store) changeStruggle(c protocol.Change) *protocol.Struggle {
	if c.Origin.Struggle != nil {
		return c.Origin.Struggle
	}
	if c.Origin.SessionID == "" {
		return nil
	}
	var st protocol.Struggle
	var top string
	var capped int
	err := s.db.QueryRow(`SELECT struggle_failed, struggle_repeats, struggle_retries, struggle_exploration, struggle_env, struggle_top, struggle_trace, struggle_capped
		FROM worker_runs WHERE session_id=? ORDER BY id DESC LIMIT 1`, c.Origin.SessionID).Scan(
		&st.Failed, &st.Repeats, &st.Retries, &st.Exploration, &st.EnvErrors, &top, &st.Trace, &capped)
	if err != nil {
		return nil
	}
	if top != "" {
		st.Top = strings.Split(top, "; ")
	}
	st.Capped = capped != 0
	return &st
}

// stepTaskQueued reports whether a task row already exists for ref on this
// revision. A queued task can be claimed or reported even when the step is not
// ready yet, for example the second step of a pair task.
func stepTaskQueued(q deliveryQuerier, c protocol.Change, ref protocol.StepRef) (bool, error) {
	var exists int
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&exists)
	return exists != 0, err
}

// checkStepReady refuses a queued step whose pipeline requirements have not
// passed. The refusal names the requirement that is missing.
func checkStepReady(tx *sql.Tx, c protocol.Change, p protocol.Pipeline, ref protocol.StepRef) error {
	evidence, err := changeEvidence(tx, c.ID)
	if err != nil {
		return err
	}
	if ready, missing := p.Ready(ref, c, evidence); !ready {
		return reject("%s is not ready: %s", ref, strings.Join(missing, "; "))
	}
	return nil
}

// claimNodeTask awards a queued task to the first eligible claimant or
// reporter. found is false when no node task exists for this step.
func claimNodeTask(tx *sql.Tx, c protocol.Change, ref protocol.StepRef, session, machine string, complete bool) (found bool, err error) {
	return checkNodeTask(tx, c, ref, session, machine, complete, true)
}

func checkNodeTask(tx *sql.Tx, c protocol.Change, ref protocol.StepRef, session, machine string, complete, claim bool) (found bool, err error) {
	var owner, claimed string
	var completed int
	err = tx.QueryRow(`SELECT owner_node,claimed_by,completed FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&owner, &claimed, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if completed != 0 {
		var result string
		err := tx.QueryRow(`SELECT result FROM delivery_evidence WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? ORDER BY seq DESC LIMIT 1`, c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&result)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return true, err
		}
		if err == nil && result == string(protocol.StepResultNeedsOwner) && claimed == session {
			completed = 0
		}
	}
	if completed != 0 {
		return true, reject("%s was already reported by %s", ref, claimed)
	}
	var project, worker, harness, node, agent string
	var online int
	err = tx.QueryRow(`SELECT `+sessionRepoSQL+`,s.worker,s.online,s.harness,s.node_id,COALESCE(s.agent_id,'')
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, session).Scan(&project, &worker, &online, &harness, &node, &agent)
	if errors.Is(err, sql.ErrNoRows) {
		return true, reject("unknown session %q", session)
	}
	if err != nil {
		return true, err
	}
	allowedOwner := project == owner
	if owner == mergerTaskOwner {
		var n int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_mergers WHERE project=? AND node_id=? AND session_id=?)`, c.Project, c.Machine, session).Scan(&n); err != nil {
			return true, err
		}
		allowedOwner = n != 0
	}
	if owner == callerTaskOwner {
		allowedOwner = session == c.Caller
	}
	if owner == delegateTaskOwner {
		var allowed int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_delegates WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND (node=? OR node=?))`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), project, "agent:"+agent).Scan(&allowed); err != nil {
			return true, err
		}
		allowedOwner = allowed != 0
	}
	if node != machine || !allowedOwner || worker != "" || online == 0 || harness == "owner" || harness == "bot" || harness == "cto" || (claimed != "" && claimed != session) {
		return true, reject("%s is owned by node %s", ref, owner)
	}
	if !claim {
		return true, nil
	}
	_, err = tx.Exec(`UPDATE delivery_step_tasks SET claimed_by=?,completed=?
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		session, boolInt(complete), c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge))
	return true, err
}

// claimDirectOwnerApproval records an owner approval reported in the caller's
// own session, or in a session of an allowed delegate. It is never used for
// an owner-only written approval.
func claimDirectOwnerApproval(tx *sql.Tx, c protocol.Change, ref protocol.StepRef, session, machine string) (bool, error) {
	var owner, claimed string
	var completed int
	err := tx.QueryRow(`SELECT owner_node,claimed_by,completed FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&owner, &claimed, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if owner != delegateTaskOwner || completed != 0 {
		return true, reject("%s is not an open delegated approval", ref)
	}
	var project, worker, harness, node, agent string
	var online int
	err = tx.QueryRow(`SELECT `+sessionRepoSQL+`,s.worker,s.online,s.harness,s.node_id,COALESCE(s.agent_id,'')
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, session).Scan(&project, &worker, &online, &harness, &node, &agent)
	if err != nil {
		return true, err
	}
	allowed := session == c.Caller
	if !allowed {
		var delegate int
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_delegates WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND (node=? OR node=?))`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), project, "agent:"+agent).Scan(&delegate)
		if err != nil {
			return true, err
		}
		allowed = delegate != 0
	}
	if !allowed || node != machine || worker != "" || online == 0 || harness == "owner" || harness == "bot" || harness == "cto" {
		return true, reject("session %s cannot report this delegated owner approval", session)
	}
	_, err = tx.Exec(`UPDATE delivery_step_tasks SET claimed_by=?,completed=1 WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		protocol.EvidenceByOwner, c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge))
	return true, err
}

// ClaimStep reserves a ready node task without recording evidence. The
// gateway checks the reporting machine and current commit in the transaction.
func (s *Store) ClaimStep(machine string, q protocol.StepClaimRequest) (protocol.StepClaimResult, error) {
	if err := q.Validate(); err != nil {
		return protocol.StepClaimResult{}, reject("%s", err.Error())
	}
	initial, ok, err := queryChange(s.db, `id=?`, q.ChangeID)
	if err != nil {
		return protocol.StepClaimResult{}, err
	}
	if !ok {
		return protocol.StepClaimResult{}, reject("unknown change %q", q.ChangeID)
	}
	p, err := s.deliveryPipeline(initial.Project)
	if err != nil {
		return protocol.StepClaimResult{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.StepClaimResult{}, err
	}
	defer tx.Rollback()
	c, ok, err := queryChange(tx, `id=?`, q.ChangeID)
	if err != nil {
		return protocol.StepClaimResult{}, err
	}
	if !ok {
		return protocol.StepClaimResult{}, reject("unknown change %q", q.ChangeID)
	}
	want := c.HeadCommit
	if q.AfterMerge {
		want = c.MergedCommit
	}
	if (q.AfterMerge && c.State != protocol.ChangeMerged) || (!q.AfterMerge && c.State != protocol.ChangeOpen) || q.Commit != want {
		return protocol.StepClaimResult{}, reject("%s is not open on the requested commit", q.StepRef)
	}
	if err := checkApprovalTaskPolicy(tx, c, p, q.StepRef); err != nil {
		return protocol.StepClaimResult{}, err
	}
	queued, err := stepTaskQueued(tx, c, q.StepRef)
	if err != nil {
		return protocol.StepClaimResult{}, err
	}
	if queued {
		if err := checkStepReady(tx, c, p, q.StepRef); err != nil {
			return protocol.StepClaimResult{}, err
		}
	}
	found, err := claimNodeTask(tx, c, q.StepRef, q.SessionID, machine, false)
	if err != nil {
		return protocol.StepClaimResult{}, err
	}
	if !found {
		return protocol.StepClaimResult{}, reject("no ready node task for %s", q.StepRef)
	}
	if err := retireStepMessages(tx, c.ID); err != nil {
		return protocol.StepClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.StepClaimResult{}, err
	}
	return protocol.StepClaimResult{Claimed: true, SessionID: q.SessionID}, nil
}

func (s *Store) stepContract(project, step string) string {
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return "contract: unavailable"
	}
	return "contract:\n  " + strings.ReplaceAll(protocol.NeutralizeMarkers(snap.String("delivery.contract."+step, settings.Where{Repo: project})), "\n", "\n  ")
}
func (s *Store) stepContracts(project string, refs []protocol.StepRef) string {
	var lines []string
	for _, ref := range refs {
		lines = append(lines, "step: "+ref.String()+"\n"+s.stepContract(project, ref.Step))
	}
	return strings.Join(lines, "\n")
}
