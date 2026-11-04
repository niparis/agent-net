package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// A slash cannot occur in a graph node name, so it cannot be confused with a
// node assignment. The actual delegate nodes are stored separately.
const delegateTaskOwner = "/delegate"
const callerTaskOwner = "/caller"

type nodeTaskTarget struct {
	id, owner, session string
	revision           int
	ref                protocol.StepRef
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
		 OR (t.after_merge=0 AND c.state<>'open') OR (t.after_merge=1 AND c.state<>'merged')))`, change)
	return err
}

func dismissStepInbox(tx *sql.Tx, c protocol.Change, ref protocol.StepRef, at int64) error {
	_, err := tx.Exec(`UPDATE inbox_items SET status='dismissed',answer='',answered_at=? WHERE status='open' AND id IN (
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

// dispatchStepTasks writes one system notification per eligible session. The
// task itself remains in the gateway until a session reports it.
func (g *Gateway) dispatchStepTasks(ctx context.Context) {
	rows, err := g.store.db.Query(`SELECT t.change_id,t.revision,t.step,t.for_step,t.after_merge,COALESCE(NULLIF(s.repo_project,''),p.project,''),s.id
		FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
		JOIN sessions s ON s.online=1 AND s.worker='' LEFT JOIN profiles p ON p.session_id=s.id
		WHERE t.completed=0 AND t.claimed_by='' AND c.state IN ('open','merged')
		AND ((t.owner_node=? AND s.id=c.caller) OR COALESCE(NULLIF(s.repo_project,''),p.project,'')=t.owner_node OR
		 (t.owner_node=? AND EXISTS(SELECT 1 FROM delivery_step_delegates a WHERE a.change_id=t.change_id AND a.revision=t.revision
		 AND a.step=t.step AND a.for_step=t.for_step AND a.after_merge=t.after_merge AND a.node=COALESCE(NULLIF(s.repo_project,''),p.project,''))))
		AND s.harness NOT IN ('owner','bot','cto')
		AND NOT EXISTS (SELECT 1 FROM delivery_step_messages d JOIN messages m ON m.id=d.message_id
		WHERE d.change_id=t.change_id AND d.revision=t.revision AND d.step=t.step AND d.for_step=t.for_step
		AND d.after_merge=t.after_merge AND d.session_id=s.id AND m.state<>'expired')`, callerTaskOwner, delegateTaskOwner)
	if err != nil {
		g.logLine("error", "where", "step task targets", "err", err)
		return
	}
	var targets []nodeTaskTarget
	for rows.Next() {
		var target nodeTaskTarget
		var after int
		if err = rows.Scan(&target.id, &target.revision, &target.ref.Step, &target.ref.For, &after, &target.owner, &target.session); err != nil {
			break
		}
		target.ref.AfterMerge = after != 0
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
	for _, target := range targets {
		m, err := g.store.storeStepTaskMessage(target)
		if err != nil {
			g.logLine("error", "where", "step task message", "err", err)
			continue
		}
		if m.ID != "" {
			g.dispatch(ctx, m)
		}
	}
}

func (s *Store) storeStepTaskMessage(t nodeTaskTarget) (protocol.Message, error) {
	c, ok, err := queryChange(s.db, `id=?`, t.id)
	if err != nil || !ok {
		return protocol.Message{}, err
	}
	if c.Revision != t.revision || (t.ref.AfterMerge && c.State != protocol.ChangeMerged) || (!t.ref.AfterMerge && c.State != protocol.ChangeOpen) {
		return protocol.Message{}, nil
	}
	p, err := s.deliveryPipeline(c.Project)
	if err != nil {
		return protocol.Message{}, err
	}
	evidence, err := changeEvidence(s.db, c.ID)
	if err != nil {
		return protocol.Message{}, err
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return protocol.Message{}, err
	}
	commit := c.HeadCommit
	if t.ref.AfterMerge {
		commit = c.MergedCommit
	}
	var stat string
	err = s.db.QueryRow(`SELECT diff_stat FROM delivery_revision_stats WHERE change_id=? AND revision=?`, c.ID, t.revision).Scan(&stat)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return protocol.Message{}, err
	}
	if stat == "" {
		stat = "not available"
	}
	body := fmt.Sprintf("[agent-net step task]\nchange: %s\nproject: %s\nbranch: %s\nrevision: %d\ncommit: %s\nuncommitted: %t\nstep: %s\nowner: %s\ntitle: %s\ndiff stat:\n%s\nevidence: %s\nreport fields: %s\nClaim the task with agent_step_claim before starting, then report the result with agent_step_report from your session.",
		c.ID, protocol.NeutralizeMarkers(c.Project), protocol.NeutralizeMarkers(c.Branch), c.Revision, commit, c.Uncommitted, t.ref.String(), protocol.NeutralizeMarkers(t.owner),
		protocol.NeutralizeMarkers(c.Title), protocol.NeutralizeMarkers(stat), protocol.NeutralizeMarkers(string(encoded)), strings.Join(p.ReportFields(t.ref), ", "))
	now := s.now().UTC()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: t.session, Type: protocol.TypeNotification, Body: body, IdempotencyKey: protocol.NewID("step"),
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, err
	}
	defer tx.Rollback()
	// A notice can expire while its task remains unclaimed. Its association
	// must not prevent a fresh notice when the same session comes back.
	if _, err := tx.Exec(`DELETE FROM delivery_step_messages WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND session_id=?
		AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.id=delivery_step_messages.message_id AND m.state<>'expired')`,
		t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge), t.session); err != nil {
		return protocol.Message{}, err
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO delivery_step_messages(change_id,revision,step,for_step,after_merge,session_id,message_id)
		SELECT change_id,revision,step,for_step,after_merge,?,? FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=0 AND claimed_by=''
		AND EXISTS(SELECT 1 FROM delivery_changes c WHERE c.id=delivery_step_tasks.change_id AND c.revision=delivery_step_tasks.revision
		AND ((delivery_step_tasks.after_merge=0 AND c.state='open') OR (delivery_step_tasks.after_merge=1 AND c.state='merged')))`,
		t.session, m.ID, t.id, t.revision, t.ref.Step, t.ref.For, boolInt(t.ref.AfterMerge))
	if err != nil {
		return protocol.Message{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return protocol.Message{}, err
	}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		return protocol.Message{}, err
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
	shared := false
	for _, step := range p.Steps {
		if step.Owner.Kind != protocol.OwnerCaller && step.Owner.Kind != protocol.OwnerNone {
			shared = true
			break
		}
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
		} else if step.Owner.Kind != protocol.OwnerNode {
			continue
		}
		if ref.Step == protocol.StepApprove {
			if err := dismissStepInbox(tx, c, ref, s.now().UnixNano()); err != nil {
				return err
			}
			if err := discardCompletedStepTask(tx, c, ref); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO delivery_step_tasks
			(change_id,revision,step,for_step,after_merge,owner_node,created_at) VALUES(?,?,?,?,?,?,?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), ownerNode, now); err != nil {
			return err
		}
	}
	return nil
}

// discardCompletedStepTask is used only for a ready approval: a task marked
// complete when a stricter rule replaced it may need to be queued again if
// that rule is undone. Completed evidence itself is never removed.
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
	snap, err := settingsSnapshotTx(tx)
	if err != nil {
		return err
	}
	allowed := snap.Strings("delivery.delegated_approver_modes", settings.Where{})
	rows, err := tx.Query(`SELECT source,mode FROM graph_edges WHERE target=? AND kind='governs'`, c.Project)
	if err != nil {
		return err
	}
	var delegates []string
	for rows.Next() {
		var node, mode string
		if err := rows.Scan(&node, &mode); err != nil {
			rows.Close()
			return err
		}
		for _, v := range allowed {
			if v == mode {
				delegates = append(delegates, node)
				break
			}
		}
	}
	err = rows.Err()
	rows.Close()
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

func (s *Store) queueOwnerApproval(tx *sql.Tx, c protocol.Change, ref protocol.StepRef) error {
	var exists int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_inbox WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&exists); err != nil || exists != 0 {
		if err != nil {
			return err
		}
		// A rule can be re-enabled for this same revision after it was
		// relaxed. Restore an unanswered item rather than creating a duplicate.
		_, err = tx.Exec(`UPDATE inbox_items SET status='open',answered_at=0 WHERE status='dismissed' AND id IN
			(SELECT item_id FROM delivery_step_inbox WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge))
		return err
	}
	commit := c.HeadCommit
	if ref.AfterMerge {
		commit = c.MergedCommit
	}
	item, err := s.createInboxItem(tx, protocol.InboxItem{Kind: "approval", SessionID: c.Caller, Node: c.Project,
		Text: fmt.Sprintf("Approve %s for change %s, project %s, branch %s, commit %s", ref.String(), c.ID, c.Project, c.Branch, commit)})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO delivery_step_inbox(change_id,revision,step,for_step,after_merge,commit_hash,item_id) VALUES(?,?,?,?,?,?,?)`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), commit, item.ID)
	return err
}

// claimNodeTask awards a queued task to the first eligible claimant or
// reporter. found is false when no node task exists for this step.
func claimNodeTask(tx *sql.Tx, c protocol.Change, ref protocol.StepRef, session, machine string, complete bool) (found bool, err error) {
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
		return true, reject("%s was already reported by %s", ref, claimed)
	}
	var project, worker, harness, node string
	var online int
	err = tx.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),p.project,''),s.worker,s.online,s.harness,s.node_id
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, session).Scan(&project, &worker, &online, &harness, &node)
	if errors.Is(err, sql.ErrNoRows) {
		return true, reject("unknown session %q", session)
	}
	if err != nil {
		return true, err
	}
	allowedOwner := project == owner
	if owner == callerTaskOwner {
		allowedOwner = session == c.Caller
	}
	if owner == delegateTaskOwner {
		var allowed int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_delegates WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND node=?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), project).Scan(&allowed); err != nil {
			return true, err
		}
		allowedOwner = allowed != 0
	}
	if node != machine || !allowedOwner || worker != "" || online == 0 || harness == "owner" || harness == "bot" || harness == "cto" || (claimed != "" && claimed != session) {
		return true, reject("%s is owned by node %s", ref, owner)
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
	var project, worker, harness, node string
	var online int
	err = tx.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),p.project,''),s.worker,s.online,s.harness,s.node_id
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, session).Scan(&project, &worker, &online, &harness, &node)
	if err != nil {
		return true, err
	}
	allowed := session == c.Caller
	if !allowed {
		var delegate int
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_delegates WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND node=?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), project).Scan(&delegate)
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
