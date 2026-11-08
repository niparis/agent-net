package gateway

// The delivery pipeline (decision 0057, docs/design/delivery-pipeline.md):
// the gateway stores changes, revisions and evidence, and routes ready steps
// assigned to graph nodes. A merge pass is refused until its required
// evidence has passed.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Page sizes of change.list.
const (
	changeListDefault = 25
	changeListMax     = 100
)

const changeColumns = `id, project, machine, branch, target_branch, base_commit, head_commit, uncommitted, revision, origin, caller, title, difficulty, state, merged_commit, created_at, updated_at, risk, sensitive_path`

// deliveryQuerier is a *sql.DB or a *sql.Tx.
type deliveryQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// handleDelivery answers the delivery pipeline frames from a daemon.
func (g *Gateway) handleDelivery(ctx context.Context, nc *nodeConn, typ string, raw []byte) (any, bool, error) {
	var (
		data any
		err  error
	)
	switch typ {
	case protocol.FrameChangeReconcile:
		q, derr := decode[protocol.ChangeReconcileRequest](raw)
		if derr != nil {
			return nil, true, reject("bad %s", typ)
		}
		data, err = g.store.ReconcileChange(nc.id, q)
	case protocol.FrameChangeSubmit:
		q, derr := decode[protocol.ChangeSubmitRequest](raw)
		if derr != nil {
			return nil, true, reject("bad %s", typ)
		}
		data, err = g.store.SubmitChange(nc.id, q)
	case protocol.FrameStepReport:
		q, derr := decode[protocol.StepReportRequest](raw)
		if derr != nil {
			return nil, true, reject("bad %s", typ)
		}
		data, err = g.store.ReportStep(nc.id, q)
	case protocol.FrameStepClaim:
		q, derr := decode[protocol.StepClaimRequest](raw)
		if derr != nil {
			return nil, true, reject("bad %s", typ)
		}
		data, err = g.store.ClaimStep(nc.id, q)
	case protocol.FrameChangeGet:
		q, derr := decode[protocol.ChangeGetRequest](raw)
		if derr != nil {
			return nil, true, reject("bad %s", typ)
		}
		data, err = g.store.GetChange(q.ChangeID)
	case protocol.FrameChangeList:
		q, derr := decode[protocol.ChangeListRequest](raw)
		if derr != nil {
			return nil, true, reject("bad %s", typ)
		}
		data, err = g.store.ListChanges(q)
	case protocol.FrameDeliveryNeedsPush:
		q, derr := decode[protocol.DeliveryNeedsPushRequest](raw)
		if derr != nil || q.Project == "" || len(q.Project) > protocol.ProjectMaxFieldLen {
			return nil, true, reject("bad %s", typ)
		}
		var needed bool
		needed, err = g.store.DeliveryNeedsPush(q.Project, nc.id, q.CallerSessionID)
		data = protocol.DeliveryNeedsPushResult{NeedsPush: needed}
	default:
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if typ == protocol.FrameStepReport {
		q, derr := decode[protocol.StepReportRequest](raw)
		if derr == nil && q.SessionID != "" {
			g.recordSessionReport(q.SessionID)
		}
	}
	if typ == protocol.FrameChangeSubmit || typ == protocol.FrameStepReport {
		g.dispatchStepTasks(ctx)
	}
	if typ == protocol.FrameStepReport {
		result := data.(protocol.StepReportResult)
		if node, err := g.store.SessionNode(result.Change.Change.Caller); err == nil && node != "" {
			g.dispatchPending(ctx, node, false)
		}
	}
	return data, true, nil
}

// DeliveryNeedsPush reports whether an assigned step owner is known on a
// different machine. It exposes no machine or session identities.
func (s *Store) DeliveryNeedsPush(project, machine, caller string) (bool, error) {
	p, err := s.deliveryPipeline(project)
	if err != nil {
		return false, err
	}
	if caller != "" {
		session, ok, err := s.deliverySession(caller)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, reject("unknown caller session %q", caller)
		}
		if session.node != machine {
			return true, nil
		}
	}
	seen := map[string]bool{}
	for _, step := range p.Steps {
		if step.Owner.Kind != protocol.OwnerNode || seen[step.Owner.Node] {
			continue
		}
		seen[step.Owner.Node] = true
		var found int
		err := s.db.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
			WHERE `+sessionRepoSQL+`=? AND s.node_id<>? AND s.worker=''
			AND s.harness NOT IN ('owner','bot','cto')
			UNION ALL SELECT 1 FROM projects WHERE name=? AND node_id<>? AND graph_excluded<>1)`,
			step.Owner.Node, machine, step.Owner.Node, machine).Scan(&found)
		if err != nil {
			return false, err
		}
		if found != 0 {
			return true, nil
		}
	}
	return false, nil
}

// deliveryPipeline resolves the ordered delivery settings and overlays graph
// assignments. A project with no graph node gets the network values.
func (s *Store) deliveryPipeline(project string) (protocol.Pipeline, error) {
	c, err := s.resolveRepoContext(project)
	if err != nil {
		return protocol.Pipeline{}, err
	}
	p, err := protocol.PipelineFromSettings(c.Settings, c.Repo)
	if err != nil {
		return protocol.Pipeline{}, err
	}
	return p, p.Validate()
}

// deliveryEdgeStep maps an assignment edge to the step it owns. deploys is
// the existing deploy owner edge. The same owner handles both test phases.
func deliveryEdgeStep(kind string) string {
	return protocol.DeliveryEdgeStep(kind)
}

// deliverySession is what the delivery frames need of a session: its
// machine, harness and model, and the session that started it when it is a
// worker.
type deliverySession struct {
	node, harness, model, workerOwner string
}

func (s *Store) deliverySession(id string) (deliverySession, bool, error) {
	var d deliverySession
	var worker string
	err := s.db.QueryRow(`SELECT s.node_id, s.harness, s.model, s.worker FROM sessions s JOIN nodes n ON n.id = s.node_id WHERE s.id = ?`, id).Scan(&d.node, &d.harness, &d.model, &worker)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	if worker != "" {
		var w protocol.WorkerInfo
		if json.Unmarshal([]byte(worker), &w) == nil {
			d.workerOwner = w.Owner
		}
	}
	return d, true, nil
}

// family is the model maker of the session, for independence.
func (d deliverySession) family() string {
	if d.model == "" {
		return ""
	}
	return protocol.ModelFamily(d.harness, d.model)
}

// checkSubmitter checks that machine may submit q: the session is on this
// machine, or q is the hand-back of a worker on this machine that the session
// started (the caller may be on another machine; the branch is here). A
// session submits only its own work.
func (s *Store) checkSubmitter(machine string, q protocol.ChangeSubmitRequest) error {
	if q.Origin.Kind == protocol.OriginSession && q.Origin.SessionID != q.SessionID {
		return reject("a session submits its own work: origin session_id must be %s", q.SessionID)
	}
	who, ok, err := s.deliverySession(q.SessionID)
	if err != nil {
		return err
	}
	if ok && who.node == machine {
		return nil
	}
	if q.Origin.Kind == protocol.OriginWorker && q.Origin.SessionID != "" {
		w, ok, err := s.deliverySession(q.Origin.SessionID)
		if err != nil {
			return err
		}
		if ok && w.node == machine && w.workerOwner == q.SessionID {
			return nil
		}
	}
	return reject("session %q is not on this machine", q.SessionID)
}

// fillOrigin adds the author's model from its session record when the submit
// did not name it, and the model maker.
func (s *Store) fillOrigin(o *protocol.ChangeOrigin) error {
	harness := ""
	if o.SessionID != "" {
		d, ok, err := s.deliverySession(o.SessionID)
		if err != nil {
			return err
		}
		if ok {
			harness = d.harness
			if o.Model == "" {
				o.Model = d.model
			}
		}
	}
	if o.ModelFamily == "" && o.Model != "" {
		o.ModelFamily = protocol.ModelFamily(harness, o.Model)
	}
	return nil
}

// SubmitChange stores change.submit from machine. Without a change ID, the
// open change of the project and branch takes the new head, or a new change
// starts. With one, that change follows the work to its branch. A new head
// commit makes a new revision, whose author is the submit's origin. Fields the
// submit gives replace stored ones; the caller and the base commit stay.
func (s *Store) SubmitChange(machine string, q protocol.ChangeSubmitRequest) (protocol.ChangeSubmitResult, error) {
	if err := q.Validate(); err != nil {
		return protocol.ChangeSubmitResult{}, reject("%s", err.Error())
	}
	if err := s.checkSubmitter(machine, q); err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	if err := s.fillOrigin(&q.Origin); err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	p, err := s.deliveryPipeline(q.Project)
	if err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	now := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	defer tx.Rollback()
	var c protocol.Change
	var found bool
	if q.ChangeID != "" {
		if c, found, err = queryChange(tx, `id = ?`, q.ChangeID); err != nil {
			return protocol.ChangeSubmitResult{}, err
		}
		if !found {
			return protocol.ChangeSubmitResult{}, reject("unknown change %q", q.ChangeID)
		}
		if c.Project != q.Project {
			return protocol.ChangeSubmitResult{}, reject("change %s is for project %s, not %s", c.ID, c.Project, q.Project)
		}
	} else if c, found, err = queryChange(tx, `project = ? AND branch = ? AND state = 'open'`, q.Project, q.Branch); err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	bumped := false
	if !found {
		c = protocol.Change{ID: protocol.NewID("ch"), Project: q.Project, Machine: machine, Branch: q.Branch, TargetBranch: q.TargetBranch,
			BaseCommit: q.BaseCommit, HeadCommit: q.HeadCommit, Uncommitted: q.Uncommitted, Revision: 1, Origin: q.Origin, Caller: q.SessionID,
			Title: q.Title, Difficulty: q.Difficulty, Risk: q.Risk, SensitivePath: q.SensitivePath, State: protocol.ChangeOpen, CreatedAt: now}
		if err := c.Validate(); err != nil {
			return protocol.ChangeSubmitResult{}, reject("%s", err.Error())
		}
		if err := writeChange(tx, c, true); err != nil {
			return protocol.ChangeSubmitResult{}, err
		}
		if err := insertRevision(tx, c); err != nil {
			return protocol.ChangeSubmitResult{}, err
		}
	} else {
		next, b, err := c.WithHead(q.Branch, q.HeadCommit, q.Uncommitted, now)
		if err != nil {
			return protocol.ChangeSubmitResult{}, reject("%s", err.Error())
		}
		bumped = b
		if next.Branch != c.Branch {
			other, taken, err := queryChange(tx, `project = ? AND branch = ? AND state = 'open' AND id <> ?`, c.Project, next.Branch, c.ID)
			if err != nil {
				return protocol.ChangeSubmitResult{}, err
			}
			if taken {
				return protocol.ChangeSubmitResult{}, reject("branch %s of project %s already holds open change %s", next.Branch, c.Project, other.ID)
			}
		}
		next.Machine = machine
		if bumped {
			next.Origin = q.Origin
		}
		if q.TargetBranch != "" {
			next.TargetBranch = q.TargetBranch
		}
		if next.BaseCommit == "" {
			next.BaseCommit = q.BaseCommit
		}
		if q.Title != "" {
			next.Title = q.Title
		}
		if q.Difficulty != "" {
			next.Difficulty = q.Difficulty
		}

		// A missing field from an older daemon or a later plain submit must
		// not erase a review requirement already recorded on this change.
		next.Risk = max(next.Risk, q.Risk)
		if q.SensitivePath != "" {
			next.SensitivePath = q.SensitivePath
		}
		if next != c {
			next.UpdatedAt = now
			if err := next.Validate(); err != nil {
				return protocol.ChangeSubmitResult{}, reject("%s", err.Error())
			}
			if err := writeChange(tx, next, false); err != nil {
				return protocol.ChangeSubmitResult{}, err
			}
			if bumped {
				if err := insertRevision(tx, next); err != nil {
					return protocol.ChangeSubmitResult{}, err
				}
			}
		}
		c = next
	}
	if q.DiffStat != "" {
		if _, err := tx.Exec(`INSERT INTO delivery_revision_stats(change_id,revision,diff_stat) VALUES(?,?,?)
			ON CONFLICT(change_id,revision) DO UPDATE SET diff_stat=excluded.diff_stat`, c.ID, c.Revision, q.DiffStat); err != nil {
			return protocol.ChangeSubmitResult{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE inbox_items SET status='dismissed', answered_at=? WHERE status='open' AND id IN (SELECT item_id FROM delivery_step_inbox WHERE change_id=? AND revision<>?)`, s.now().UnixNano(), c.ID, c.Revision); err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	if _, err := s.closeAsks(tx, `about_kind='change' AND about_id=? AND kind='step' AND source_ref NOT LIKE ?`,
		[]any{c.ID, "step:" + c.ID + ":" + strconv.Itoa(c.Revision) + ":%"}, protocol.AskReasonResolved); err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	if err := s.queueSteps(tx, c, p, now); err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	if err := retireStepMessages(tx, c.ID); err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.ChangeSubmitResult{}, err
	}
	view, err := s.changeView(c, p, false)
	return protocol.ChangeSubmitResult{Change: view, Bumped: bumped}, err
}

// ReportStep stores step.report from machine. The reporting session must be
// on machine; done_by must be a worker it started or the person directly
// approving a delegated step. The gateway fills the
// revision, who did the work with its model, and the independent field, then
// checks the evidence with Pipeline.Accepts. A merge pass marks the change
// merged, and done once every step after merge has passed; a close stops it.
// Stored evidence never changes.
func (s *Store) ReportStep(machine string, q protocol.StepReportRequest) (protocol.StepReportResult, error) {
	if err := q.Validate(); err != nil {
		return protocol.StepReportResult{}, reject("%s", err.Error())
	}
	reporter, ok, err := s.deliverySession(q.SessionID)
	if err != nil {
		return protocol.StepReportResult{}, err
	}
	if !ok || reporter.node != machine {
		return protocol.StepReportResult{}, reject("session %q is not on this machine", q.SessionID)
	}
	by, doer, reportedBy := q.SessionID, reporter, ""
	if q.Result == protocol.StepResultClose && q.DoneBy != "" {
		return protocol.StepReportResult{}, reject("a close is reported by the session itself: leave done_by out")
	}
	directOwner := q.DoneBy == protocol.EvidenceByOwner
	if directOwner {
		by, doer, reportedBy = protocol.EvidenceByOwner, deliverySession{}, q.SessionID
	} else if q.DoneBy != "" && q.DoneBy != q.SessionID {
		w, ok, err := s.deliverySession(q.DoneBy)
		if err != nil {
			return protocol.StepReportResult{}, err
		}
		if !ok || w.workerOwner != q.SessionID {
			return protocol.StepReportResult{}, reject("done_by %q is not a worker that session %s started", q.DoneBy, q.SessionID)
		}
		by, doer, reportedBy = q.DoneBy, w, q.SessionID
	}
	c, ok, err := queryChange(s.db, `id = ?`, q.ChangeID)
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
	now := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.StepReportResult{}, err
	}
	defer tx.Rollback()
	// Read the change again in the transaction: a submit may have moved its
	// head since.
	if c, _, err = queryChange(tx, `id = ?`, q.ChangeID); err != nil {
		return protocol.StepReportResult{}, err
	}
	if p, err = taskOwners(tx, c, p); err != nil {
		return protocol.StepReportResult{}, err
	}
	// The caller of a change (or the owner) may close it at any step, even when
	// the step is owned by another node and is not ready yet. The report is
	// still checked against the pipeline, revision and commit by Accepts, but
	// the caller does not own the step and does not claim it.
	closeAnyStep := q.Result == protocol.StepResultClose && (q.SessionID == c.Caller || reporter.harness == "owner")
	if closeAnyStep {
		by, doer, reportedBy = q.SessionID, reporter, ""
	}
	// A released change's verify step is an owner check: the owner tries it
	// from the TUI and reports pass or fail as themselves, without being the
	// caller or the step's owner.
	ownerVerify := directOwner && reporter.harness == "owner" && c.State == protocol.ChangeMerged && q.StepRef == (protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true})
	if !closeAnyStep {
		if err := checkApprovalTaskPolicy(tx, c, p, q.StepRef); err != nil {
			return protocol.StepReportResult{}, err
		}
		if step, ok := p.Find(q.StepRef); ok {
			if directOwner && !ownerVerify && (step.Owner.Kind != protocol.OwnerDelegate || step.Step != protocol.StepApprove) {
				return protocol.StepReportResult{}, reject("direct owner approval is only for a delegated approval step or an after-merge verify")
			}
			switch step.Owner.Kind {
			case protocol.OwnerCaller:
				if q.SessionID != c.Caller && !ownerVerify {
					return protocol.StepReportResult{}, reject("%s is owned by the caller", q.StepRef)
				}
			case protocol.OwnerPerson:
				return protocol.StepReportResult{}, reject("%s is owned by the person: answer its Inbox approval", q.StepRef)
			case protocol.OwnerGitHub:
				return protocol.StepReportResult{}, reject("%s is owned by GitHub", q.StepRef)
			case protocol.OwnerDelegate:
				if q.DoneBy != "" && q.DoneBy != q.SessionID && !directOwner {
					return protocol.StepReportResult{}, reject("a delegate reports its own approval")
				}
			}
		} else if directOwner {
			return protocol.StepReportResult{}, reject("the approval step is not in this pipeline")
		}
	}
	fields := map[string]string{}
	for k, v := range q.Fields {
		fields[k] = v
	}
	family := doer.family()
	fields[protocol.FieldIndependent] = protocol.IndependenceOf(c.Origin, by, family)
	e := protocol.Evidence{ChangeID: c.ID, Revision: c.Revision, StepRef: q.StepRef, Result: q.Result, Commit: q.Commit, Fields: fields,
		By: by, ReportedBy: reportedBy, ByModel: doer.model, ByModelFamily: family, Note: q.Note, At: now}
	if err := p.Accepts(c, e); err != nil {
		return protocol.StepReportResult{}, reject("%s", err.Error())
	}
	if q.Step == protocol.StepMerge && q.Result == protocol.StepResultPass {
		if q.MergeContainsHead == nil {
			var version string
			if err := tx.QueryRow(`SELECT version FROM nodes WHERE id=?`, machine).Scan(&version); err != nil {
				return protocol.StepReportResult{}, err
			}
			if deliveryMergeProofRequired(version) {
				return protocol.StepReportResult{}, reject("merge_contains_head proof is required from daemon %s (since %s)", version, deliveryMergeProofMinVersion)
			}
		} else if !*q.MergeContainsHead {
			return protocol.StepReportResult{}, reject("merge refused: daemon could not verify merged_commit %s exists and contains change head %s", q.Fields[protocol.FieldMergedCommit], c.HeadCommit)
		}
	}
	if err := checkMergeReady(tx, p, c, e); err != nil {
		return protocol.StepReportResult{}, err
	}
	var found bool
	if !closeAnyStep {
		queued, err := stepTaskQueued(tx, c, q.StepRef)
		if err != nil {
			return protocol.StepReportResult{}, err
		}
		if queued {
			if err := checkStepReady(tx, c, p, q.StepRef); err != nil {
				return protocol.StepReportResult{}, err
			}
		}
		switch {
		case ownerVerify:
			// The owner check is reported on its own, not claimed as a task.
			found = false
		case directOwner:
			found, err = claimDirectOwnerApproval(tx, c, q.StepRef, q.SessionID, machine)
		default:
			found, err = claimNodeTask(tx, c, q.StepRef, q.SessionID, machine, true)
		}
		if err != nil {
			return protocol.StepReportResult{}, err
		}
		if step, ok := p.Find(q.StepRef); !found && ok && (step.Owner.Kind == protocol.OwnerNode || step.Owner.Kind == protocol.OwnerDelegate || step.Owner.Kind == protocol.OwnerMerger) {
			return protocol.StepReportResult{}, reject("%s has not reached its owner", q.StepRef)
		}
	}
	if err := insertEvidence(tx, e); err != nil {
		return protocol.StepReportResult{}, err
	}
	if err := s.syncStepAsk(tx, c, e); err != nil {
		return protocol.StepReportResult{}, err
	}
	if found && q.Step == protocol.StepApprove {
		if err := dismissStepInbox(tx, c, q.StepRef, s.now().UnixNano()); err != nil {
			return protocol.StepReportResult{}, err
		}
	}
	next := c
	switch {
	case e.Result == protocol.StepResultClose:
		next.State = protocol.ChangeStopped
	case e.Step == protocol.StepMerge && e.Result == protocol.StepResultPass:
		next.State, next.MergedCommit = protocol.ChangeMerged, e.Fields[protocol.FieldMergedCommit]
	}
	if next.State == protocol.ChangeMerged {
		evidence, err := changeEvidence(tx, c.ID)
		if err != nil {
			return protocol.StepReportResult{}, err
		}
		if afterMergeDone(p, next, evidence) {
			next.State = protocol.ChangeDone
		}
	}
	if next != c {
		next.UpdatedAt = now
		if err := writeChange(tx, next, false); err != nil {
			return protocol.StepReportResult{}, err
		}
	}
	var closedSessions []string
	if next.State == protocol.ChangeDone {
		if err := s.closeChangeAsks(tx, c.ID, protocol.AskReasonChangeDone); err != nil {
			return protocol.StepReportResult{}, err
		}
	}
	if next.State == protocol.ChangeStopped {
		if err := s.closeChangeAsks(tx, c.ID, protocol.AskReasonChangeDone); err != nil {
			return protocol.StepReportResult{}, err
		}
		if _, err := tx.Exec(`UPDATE inbox_items SET status='dismissed', answered_at=? WHERE status='open' AND id IN (SELECT item_id FROM delivery_step_inbox WHERE change_id=?)`, s.now().UnixNano(), c.ID); err != nil {
			return protocol.StepReportResult{}, err
		}
		if closedSessions, err = closeStepTasks(tx, c); err != nil {
			return protocol.StepReportResult{}, err
		}
	}
	if err := s.queueSteps(tx, next, p, now); err != nil {
		return protocol.StepReportResult{}, err
	}
	if err := retireStepMessages(tx, c.ID); err != nil {
		return protocol.StepReportResult{}, err
	}
	var notices []protocol.Message
	if found && (e.Result != protocol.StepResultPass || next.State == protocol.ChangeDone) {
		notice, err := s.stepResultNoticeTx(tx, next, e)
		if err != nil {
			return protocol.StepReportResult{}, err
		}
		notices = append(notices, notice)
	}
	for _, session := range closedSessions {
		notice, err := s.stepClosedNoticeTx(tx, next, session)
		if err != nil {
			return protocol.StepReportResult{}, err
		}
		notices = append(notices, notice)
	}
	if err := tx.Commit(); err != nil {
		return protocol.StepReportResult{}, err
	}
	for _, notice := range notices {
		if notice.ID != "" {
			s.logMessage(notice)
		}
	}
	view, err := s.changeView(next, p, false)
	return protocol.StepReportResult{Evidence: e, Change: view}, err
}

// afterMergeDone reports whether every step after merge passed or is left
// out on merged change c.
func afterMergeDone(p protocol.Pipeline, c protocol.Change, evidence []protocol.Evidence) bool {
	for _, st := range p.Status(c, evidence) {
		if st.AfterMerge && st.State != protocol.StepStatePassed && st.State != protocol.StepStateSkipped {
			return false
		}
	}
	return true
}

// GetChange answers change.get: one change with its pipeline, revisions and
// evidence of every revision.
func (s *Store) GetChange(id string) (protocol.ChangeView, error) {
	c, ok, err := queryChange(s.db, `id = ?`, id)
	if err != nil {
		return protocol.ChangeView{}, err
	}
	if !ok {
		return protocol.ChangeView{}, reject("unknown change %q", id)
	}
	p, err := s.deliveryPipeline(c.Project)
	if err != nil {
		return protocol.ChangeView{}, err
	}
	return s.changeView(c, p, true)
}

// ListChanges answers change.list, newest change first.
func (s *Store) ListChanges(q protocol.ChangeListRequest) (protocol.ChangeListResult, error) {
	where, args := "1=1", []any{}
	switch q.State {
	case "", protocol.ChangeListActive:
		where += ` AND state IN ('open', 'merged')`
	case protocol.ChangeListAny:
	case protocol.ChangeOpen, protocol.ChangeMerged, protocol.ChangeDone, protocol.ChangeStopped:
		where += ` AND state = ?`
		args = append(args, q.State)
	default:
		return protocol.ChangeListResult{}, reject("state must be active, open, merged, done, stopped or any, not %q", q.State)
	}
	if q.Project != "" {
		where += ` AND project = ?`
		args = append(args, q.Project)
	}
	if q.Branch != "" {
		where += ` AND (branch = ? OR EXISTS (SELECT 1 FROM delivery_revisions r WHERE r.change_id=delivery_changes.id AND r.branch=?))`
		args = append(args, q.Branch, q.Branch)
	}
	if q.Caller != "" {
		where += ` AND caller = ?`
		args = append(args, q.Caller)
	}
	if q.After != "" {
		var seq int64
		err := s.db.QueryRow(`SELECT seq FROM delivery_changes WHERE id = ?`, q.After).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.ChangeListResult{}, reject("unknown change %q in after", q.After)
		}
		if err != nil {
			return protocol.ChangeListResult{}, err
		}
		where += ` AND seq < ?`
		args = append(args, seq)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = changeListDefault
	}
	if limit > changeListMax {
		limit = changeListMax
	}
	rows, err := s.db.Query(`SELECT `+changeColumns+` FROM delivery_changes WHERE `+where+` ORDER BY seq DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return protocol.ChangeListResult{}, err
	}
	var changes []protocol.Change
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			rows.Close()
			return protocol.ChangeListResult{}, err
		}
		changes = append(changes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return protocol.ChangeListResult{}, err
	}
	out := protocol.ChangeListResult{Changes: []protocol.ChangeView{}, Branch: q.Branch}
	if len(changes) > limit {
		changes = changes[:limit]
		out.Next = changes[limit-1].ID
	}
	pipelines := map[string]protocol.Pipeline{}
	for _, c := range changes {
		p, ok := pipelines[c.Project]
		if !ok {
			if p, err = s.deliveryPipeline(c.Project); err != nil {
				return protocol.ChangeListResult{}, err
			}
			pipelines[c.Project] = p
		}
		v, err := s.changeView(c, p, false)
		if err != nil {
			return protocol.ChangeListResult{}, err
		}
		out.Changes = append(out.Changes, v)
	}
	return out, nil
}

// changeView is change c with where its steps stand and the steps that may
// start now. full adds the pipeline, the revisions and all evidence.
func (s *Store) changeView(c protocol.Change, p protocol.Pipeline, full bool) (protocol.ChangeView, error) {
	p, err := taskOwners(s.db, c, p)
	if err != nil {
		return protocol.ChangeView{}, err
	}
	evidence, err := changeEvidence(s.db, c.ID)
	if err != nil {
		return protocol.ChangeView{}, err
	}
	v := protocol.ChangeView{Change: c, Steps: p.Status(c, evidence), Next: p.NextSteps(c, evidence)}
	startedSteps := map[protocol.StepRef]bool{}
	for i := range v.Steps {
		st := &v.Steps[i]
		// Tasks queued under the previous rule remain ready across an upgrade.
		if c.State == protocol.ChangeOpen && st.Step == protocol.StepTest && !st.AfterMerge && st.State == protocol.StepStateWaiting {
			var exists int
			if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=0 AND completed=0)`, c.ID, c.Revision, st.Step, st.For).Scan(&exists); err != nil {
				return protocol.ChangeView{}, err
			}
			if exists != 0 {
				st.State, st.Note = protocol.StepStateReady, ""
				v.Next = append(v.Next, st.StepRef)
			}
		}
		if st.State != protocol.StepStateReady {
			continue
		}
		if st.Owner.Kind == protocol.OwnerGitHub {
			st.State, st.Note = protocol.StepStateWaiting, "waiting for GitHub (integration not built)"
			startedSteps[st.StepRef] = true
			continue
		}
		if st.Owner.Kind == protocol.OwnerDelegate && st.Step == protocol.StepApprove {
			var claimed string
			err := s.db.QueryRow(`SELECT claimed_by FROM delivery_step_tasks WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=0`,
				c.ID, c.Revision, st.Step, st.For, boolInt(st.AfterMerge)).Scan(&claimed)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return protocol.ChangeView{}, err
			}
			if claimed != "" {
				st.State = protocol.StepStateRunning
				st.Note = deliveryStatusNote(st.Note, "claimed by "+claimed)
				startedSteps[st.StepRef] = true
				continue
			}
			var online int
			err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_delegates d JOIN sessions s ON s.online=1 AND s.worker=''
				LEFT JOIN profiles p ON p.session_id=s.id WHERE d.change_id=? AND d.revision=? AND d.step=? AND d.for_step=? AND d.after_merge=?
				AND (d.node=`+sessionRepoSQL+` OR d.node='agent:'||COALESCE(s.agent_id,'')) AND s.harness NOT IN ('owner','bot','cto'))`,
				c.ID, c.Revision, st.Step, st.For, boolInt(st.AfterMerge)).Scan(&online)
			if err != nil {
				return protocol.ChangeView{}, err
			}
			if online != 0 {
				st.Note = "waiting for a delegate or the owner"
				continue
			}
		}
		if st.Owner.Kind == protocol.OwnerPerson || (st.Owner.Kind == protocol.OwnerDelegate && st.Step == protocol.StepApprove) {
			var open int
			err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_step_inbox d JOIN inbox_items i ON i.id=d.item_id
				WHERE d.change_id=? AND d.revision=? AND d.step=? AND d.for_step=? AND d.after_merge=? AND i.status='open')`,
				c.ID, c.Revision, st.Step, st.For, boolInt(st.AfterMerge)).Scan(&open)
			if err != nil {
				return protocol.ChangeView{}, err
			}
			if open != 0 {
				st.State, st.Note = protocol.StepStateNeedsOwner, "waiting for owner approval"
				startedSteps[st.StepRef] = true
			}
			continue
		}
		if st.Owner.Kind != protocol.OwnerNode && st.Owner.Kind != protocol.OwnerCaller && st.Owner.Kind != protocol.OwnerMerger {
			continue
		}
		var claimed string
		err := s.db.QueryRow(`SELECT claimed_by FROM delivery_step_tasks
			WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=0`,
			c.ID, c.Revision, st.Step, st.For, boolInt(st.AfterMerge)).Scan(&claimed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return protocol.ChangeView{}, err
		}
		if errors.Is(err, sql.ErrNoRows) && st.Owner.Kind == protocol.OwnerCaller {
			continue
		}
		if st.Owner.Kind == protocol.OwnerMerger {
			var online int
			if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_mergers pm JOIN sessions s ON s.id=pm.session_id WHERE pm.project=? AND pm.node_id=? AND s.online=1)`, c.Project, c.Machine).Scan(&online); err != nil {
				return protocol.ChangeView{}, err
			}
			if online == 0 {
				st.State = protocol.StepStateWaiting
				st.Note = deliveryStatusNote(st.Note, "waiting for merger (offline)")
				startedSteps[st.StepRef] = true
				continue
			}
		}
		if claimed != "" {
			st.State = protocol.StepStateRunning
			st.Note = deliveryStatusNote(st.Note, "claimed by "+claimed)
			startedSteps[st.StepRef] = true
			continue
		}
		var online int
		if st.Owner.Kind == protocol.OwnerMerger {
			err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_mergers pm JOIN sessions s ON s.id=pm.session_id WHERE pm.project=? AND pm.node_id=? AND s.online=1)`, c.Project, c.Machine).Scan(&online)
		} else if st.Owner.Kind == protocol.OwnerCaller {
			err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND online=1 AND worker='')`, c.Caller).Scan(&online)
		} else {
			err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
				WHERE s.online=1 AND s.worker='' AND s.harness NOT IN ('owner','bot','cto')
				AND `+sessionRepoSQL+`=?)`, st.Owner.Node).Scan(&online)
		}
		if err != nil {
			return protocol.ChangeView{}, err
		}
		if online == 0 {
			owner := st.Owner.Node
			if st.Owner.Kind == protocol.OwnerMerger {
				owner = "merger"
			}
			if st.Owner.Kind == protocol.OwnerCaller {
				owner = "caller"
			}
			st.Note = deliveryStatusNote(st.Note, "waiting for "+owner+" (offline)")
		}
	}
	if len(startedSteps) > 0 {
		next := v.Next[:0]
		for _, ref := range v.Next {
			if !startedSteps[ref] {
				next = append(next, ref)
			}
		}
		v.Next = next
	}
	if !full {
		// Keep the terminal observation even if its step was removed from
		// the current pipeline or configured to be skipped.
		for _, e := range evidence {
			if e.Fields["reconcile_kind"] != "" {
				v.Evidence = append(v.Evidence, e)
			}
		}
		return v, nil
	}
	v.Pipeline, v.Evidence = &p, evidence
	rows, err := s.db.Query(`SELECT change_id, number, branch, commit_hash, uncommitted, at FROM delivery_revisions WHERE change_id = ? ORDER BY number`, c.ID)
	if err != nil {
		return protocol.ChangeView{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var r protocol.Revision
		var uncommitted int
		if err := rows.Scan(&r.ChangeID, &r.Number, &r.Branch, &r.Commit, &uncommitted, &r.At); err != nil {
			return protocol.ChangeView{}, err
		}
		r.Uncommitted = uncommitted != 0
		v.Revisions = append(v.Revisions, r)
	}
	return v, rows.Err()
}

func queryChange(q deliveryQuerier, where string, args ...any) (protocol.Change, bool, error) {
	c, err := scanChange(q.QueryRow(`SELECT `+changeColumns+` FROM delivery_changes WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

func scanChange(row interface{ Scan(...any) error }) (protocol.Change, error) {
	var c protocol.Change
	var origin string
	var uncommitted int
	if err := row.Scan(&c.ID, &c.Project, &c.Machine, &c.Branch, &c.TargetBranch, &c.BaseCommit, &c.HeadCommit, &uncommitted, &c.Revision,
		&origin, &c.Caller, &c.Title, &c.Difficulty, &c.State, &c.MergedCommit, &c.CreatedAt, &c.UpdatedAt, &c.Risk, &c.SensitivePath); err != nil {
		return c, err
	}
	c.Uncommitted = uncommitted != 0
	return c, json.Unmarshal([]byte(origin), &c.Origin)
}

// writeChange inserts change c, or updates the stored row with its ID.
func writeChange(tx *sql.Tx, c protocol.Change, insert bool) error {
	origin, err := json.Marshal(c.Origin)
	if err != nil {
		return err
	}
	if insert {
		_, err = tx.Exec(`INSERT INTO delivery_changes(`+changeColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.ID, c.Project, c.Machine, c.Branch, c.TargetBranch, c.BaseCommit, c.HeadCommit, boolInt(c.Uncommitted), c.Revision,
			string(origin), c.Caller, c.Title, c.Difficulty, c.State, c.MergedCommit, c.CreatedAt, c.UpdatedAt, c.Risk, c.SensitivePath)
		return err
	}
	_, err = tx.Exec(`UPDATE delivery_changes SET machine=?, branch=?, target_branch=?, base_commit=?, head_commit=?, uncommitted=?, revision=?,
		origin=?, title=?, difficulty=?, state=?, merged_commit=?, updated_at=?, risk=?, sensitive_path=? WHERE id=?`,
		c.Machine, c.Branch, c.TargetBranch, c.BaseCommit, c.HeadCommit, boolInt(c.Uncommitted), c.Revision,
		string(origin), c.Title, c.Difficulty, c.State, c.MergedCommit, c.UpdatedAt, c.Risk, c.SensitivePath, c.ID)
	return err
}

// insertRevision records change c's current head as its revision.
func insertRevision(tx *sql.Tx, c protocol.Change) error {
	at := c.UpdatedAt
	if at == 0 {
		at = c.CreatedAt
	}
	_, err := tx.Exec(`INSERT INTO delivery_revisions(change_id, number, branch, commit_hash, uncommitted, at) VALUES(?,?,?,?,?,?)`,
		c.ID, c.Revision, c.Branch, c.HeadCommit, boolInt(c.Uncommitted), at)
	return err
}

func insertEvidence(tx *sql.Tx, e protocol.Evidence) error {
	fields, err := json.Marshal(e.Fields)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO delivery_evidence(change_id, revision, step, for_step, after_merge, result, commit_hash, fields, by_session, reported_by, by_model, by_model_family, note, at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ChangeID, e.Revision, e.Step, e.For, boolInt(e.AfterMerge), e.Result, e.Commit, string(fields), e.By, e.ReportedBy, e.ByModel, e.ByModelFamily, e.Note, e.At)
	return err
}

// changeEvidence is every piece of evidence of change id, oldest first, so
// that among results with the same time the later report counts.
func changeEvidence(q deliveryQuerier, id string) ([]protocol.Evidence, error) {
	rows, err := q.Query(`SELECT change_id, revision, step, for_step, after_merge, result, commit_hash, fields, by_session, reported_by, by_model, by_model_family, note, at
		FROM delivery_evidence WHERE change_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Evidence
	for rows.Next() {
		var e protocol.Evidence
		var after int
		var fields string
		if err := rows.Scan(&e.ChangeID, &e.Revision, &e.Step, &e.For, &after, &e.Result, &e.Commit, &fields, &e.By, &e.ReportedBy, &e.ByModel, &e.ByModelFamily, &e.Note, &e.At); err != nil {
			return nil, err
		}
		e.AfterMerge = after != 0
		if err := json.Unmarshal([]byte(fields), &e.Fields); err != nil {
			return nil, err
		}
		if len(e.Fields) == 0 {
			e.Fields = nil
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// checkMergeReady applies the same merge gate to reports and owner approvals.
func checkMergeReady(tx *sql.Tx, p protocol.Pipeline, c protocol.Change, e protocol.Evidence) error {
	if e.Step != protocol.StepMerge || e.Result != protocol.StepResultPass {
		return nil
	}
	evidence, err := changeEvidence(tx, c.ID)
	if err != nil {
		return err
	}
	if ready, missing := p.Ready(e.StepRef, c, evidence); !ready {
		return reject("merge is not ready: %s", strings.Join(missing, "; "))
	}
	return nil
}

// Older upgrade tests replay later migrations. Add each column only once,
// leaving existing changes with unknown risk and no sensitive-path reason.
func migrateDeliveryReviewRisk(tx *sql.Tx) error {
	for _, col := range []struct{ name, spec string }{
		{"risk", "risk INTEGER NOT NULL DEFAULT 0"},
		{"sensitive_path", "sensitive_path TEXT NOT NULL DEFAULT ''"},
	} {
		var has int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delivery_changes') WHERE name=?`, col.name).Scan(&has); err != nil {
			return err
		}
		if has == 0 {
			if _, err := tx.Exec(`ALTER TABLE delivery_changes ADD COLUMN ` + col.spec); err != nil {
				return err
			}
		}
	}
	return nil
}

// Keep the reason review is required when the gateway adds who it waits for.
func deliveryStatusNote(existing, status string) string {
	if strings.HasPrefix(existing, "required: ") {
		return existing + "; " + status
	}
	return status
}

const deliveryMergeProofMinVersion = "0.4.131"

// Older daemons omit proof. Use the authenticated machine's hello version,
// not a field supplied in the report. Dev builds support current checks.
func deliveryMergeProofRequired(version string) bool {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "dev" {
		return true
	}
	have, ok := parseReleaseVersion(version)
	if !ok {
		return false
	}
	want, _ := parseReleaseVersion(deliveryMergeProofMinVersion)
	for i := range want {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return true
}
