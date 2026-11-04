package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

type nodeTaskTarget struct {
	id, owner, session string
	revision           int
	ref                protocol.StepRef
}

// dispatchStepTasks writes one system notification per eligible session. The
// task itself remains in the gateway until a session reports it.
func (g *Gateway) dispatchStepTasks(ctx context.Context) {
	rows, err := g.store.db.Query(`SELECT t.change_id,t.revision,t.step,t.for_step,t.after_merge,t.owner_node,s.id
		FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
		JOIN sessions s ON s.online=1 AND s.worker='' LEFT JOIN profiles p ON p.session_id=s.id
		WHERE t.completed=0 AND t.claimed_by='' AND c.state IN ('open','merged')
		AND COALESCE(NULLIF(s.repo_project,''),p.project,'')=t.owner_node
		AND s.harness NOT IN ('owner','bot','cto')
		AND NOT EXISTS (SELECT 1 FROM delivery_step_messages d WHERE d.change_id=t.change_id AND d.revision=t.revision
		AND d.step=t.step AND d.for_step=t.for_step AND d.after_merge=t.after_merge AND d.session_id=s.id)`)
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
	body := fmt.Sprintf("[agent-net step task]\nchange: %s\nproject: %s\nbranch: %s\nrevision: %d\ncommit: %s\nstep: %s\nowner: %s\ntitle: %s\nevidence: %s\nreport fields: %s\nReport the result with agent_step_report from your session.",
		c.ID, protocol.NeutralizeMarkers(c.Project), protocol.NeutralizeMarkers(c.Branch), c.Revision, commit, t.ref.String(), protocol.NeutralizeMarkers(t.owner),
		protocol.NeutralizeMarkers(c.Title), protocol.NeutralizeMarkers(string(encoded)), strings.Join(p.ReportFields(t.ref), ", "))
	now := s.now().UTC()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: t.session, Type: protocol.TypeNotification, Body: body, IdempotencyKey: protocol.NewID("step"),
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR IGNORE INTO delivery_step_messages(change_id,revision,step,for_step,after_merge,session_id,message_id)
		SELECT change_id,revision,step,for_step,after_merge,?,? FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=0 AND claimed_by=''`,
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

// queueNodeSteps is called in the same transaction as a submit or report.
func queueNodeSteps(tx *sql.Tx, c protocol.Change, p protocol.Pipeline, now int64) error {
	evidence, err := changeEvidence(tx, c.ID)
	if err != nil {
		return err
	}
	for _, ref := range p.NextSteps(c, evidence) {
		step, ok := p.Find(ref)
		if !ok || step.Owner.Kind != protocol.OwnerNode {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO delivery_step_tasks
			(change_id,revision,step,for_step,after_merge,owner_node,created_at) VALUES(?,?,?,?,?,?,?)`,
			c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge), step.Owner.Node, now); err != nil {
			return err
		}
	}
	return nil
}

// claimNodeStep awards a queued task to the first eligible reporting session.
// A session's graph identity comes from its repo or announced project.
func claimNodeStep(tx *sql.Tx, c protocol.Change, p protocol.Pipeline, q protocol.StepReportRequest) error {
	var owner, claimed string
	var completed int
	err := tx.QueryRow(`SELECT owner_node,claimed_by,completed FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		c.ID, c.Revision, q.Step, q.For, boolInt(q.AfterMerge)).Scan(&owner, &claimed, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		step, ok := p.Find(q.StepRef)
		if ok && step.Owner.Kind == protocol.OwnerNode {
			return reject("%s has not reached its node owner", q.StepRef)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if completed != 0 {
		return reject("%s was already reported by %s", q.StepRef, claimed)
	}
	var project, worker, harness string
	var online int
	err = tx.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),p.project,''),s.worker,s.online,s.harness
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, q.SessionID).Scan(&project, &worker, &online, &harness)
	if err != nil {
		return err
	}
	if project != owner || worker != "" || online == 0 || harness == "owner" || harness == "bot" || harness == "cto" || (claimed != "" && claimed != q.SessionID) {
		return reject("%s is owned by node %s", q.StepRef, owner)
	}
	_, err = tx.Exec(`UPDATE delivery_step_tasks SET claimed_by=?,completed=1
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`,
		q.SessionID, c.ID, c.Revision, q.Step, q.For, boolInt(q.AfterMerge))
	return err
}
