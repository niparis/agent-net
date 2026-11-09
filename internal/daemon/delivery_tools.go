package daemon

// The daemon side of the delivery tools agent_change_submit, agent_step_report
// and agent_changes_find (decision 0057, docs/design/delivery-pipeline.md).
// The daemon fills in what only it can know: the calling session, its project,
// the branch head from git and whether the work is committed, and the model.
// It checks that done_by is a worker the caller started, then sends the
// gateway frames. The caller is never taken from the arguments. A worker's
// complete hand-back submits its change too (origin worker): see
// recordHandbackChange.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// dispatchDelivery serves the delivery tool methods. handled is false for
// other methods.
func (lc *localConn) dispatchDelivery(ctx context.Context, client, method string, raw json.RawMessage) (res any, handled bool, err error) {
	switch method {
	case protocol.OpChangeSubmit, protocol.OpStepClaim, protocol.OpStepReport, protocol.OpChangesFind, protocol.OpAskSort:
	default:
		return nil, false, nil
	}
	sid, ls, err := lc.callerSession(client, method)
	if err != nil {
		return nil, true, err
	}
	if ls == nil {
		return nil, true, fmt.Errorf("%s needs an agent session", method)
	}
	d := lc.d
	d.mu.Lock()
	info := ls.info
	d.mu.Unlock()
	switch method {
	case protocol.OpChangeSubmit:
		if info.Worker != nil {
			return nil, true, errors.New("a worker does not submit changes: its complete hand-back does")
		}
		var a protocol.ChangeSubmitArgs
		if err := decodeDeliveryArgs(method, raw, &a); err != nil {
			return nil, true, err
		}
		res, err := d.changeSubmit(ctx, sid, info, a)
		return res, true, err
	case protocol.OpStepClaim:
		if info.Worker != nil {
			return nil, true, errors.New("a worker does not claim delivery steps")
		}
		var a protocol.StepClaimArgs
		if err := decodeDeliveryArgs(method, raw, &a); err != nil {
			return nil, true, err
		}
		q := protocol.StepClaimRequest{SessionID: sid, ChangeID: a.Change, StepRef: a.StepRef, Commit: a.Commit}
		if err := q.Validate(); err != nil {
			return nil, true, err
		}
		var out protocol.StepClaimResult
		err := d.deliveryCall(ctx, protocol.FrameStepClaim, q, &out)
		return out, true, err
	case protocol.OpAskSort:
		if info.Worker != nil {
			return nil, true, errors.New("a worker does not sort asks")
		}
		var a protocol.AskSortArgs
		if err := decodeDeliveryArgs(method, raw, &a); err != nil {
			return nil, true, err
		}
		var out protocol.AskSortResult
		err := d.deliveryCall(ctx, protocol.FrameAskSort, protocol.AskSortRequest{SessionID: sid, AskSortArgs: a}, &out)
		return out, true, err
	case protocol.OpStepReport:
		if info.Worker != nil {
			return nil, true, errors.New("a worker does not report delivery steps: its caller reports, with done_by naming the worker")
		}
		var a protocol.StepReportArgs
		if err := decodeDeliveryArgs(method, raw, &a); err != nil {
			return nil, true, err
		}
		res, err := d.stepReport(ctx, sid, info, a)
		return res, true, err
	default:
		var a protocol.ChangesFindArgs
		if err := decodeDeliveryArgs(method, raw, &a); err != nil {
			return nil, true, err
		}
		res, err := d.changesFind(ctx, sid, a)
		return res, true, err
	}
}

func decodeDeliveryArgs(method string, raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("bad %s params; use the tool's input schema: %v", method, err)
	}
	return nil
}

// errNoDeliveryGateway is the error of a gateway that does not know the
// delivery frames (an older one).
var errNoDeliveryGateway = errors.New("the gateway does not know the delivery pipeline yet; upgrade it with `agent-net upgrade` on the gateway machine")

// deliveryCall sends one delivery frame to the gateway. A gateway's refusal
// reaches the agent as it is; a gateway without the frames is named as too old
// (errNoDeliveryGateway).
func (d *Daemon) deliveryCall(ctx context.Context, typ string, params, out any) error {
	raw, err := d.call(ctx, typ, params)
	if err != nil {
		if strings.Contains(err.Error(), "unknown frame type") {
			return errNoDeliveryGateway
		}
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("bad %s answer from the gateway: %v", typ, err)
	}
	return nil
}

// deliveryContextProject asks the gateway for the session's resolved repo.
// Older gateways answer policy.get using the legacy project string.
func (d *Daemon) deliveryContextProject(ctx context.Context, sid string) (string, error) {
	raw, err := d.call(ctx, "policy.get", protocol.PolicyGet{SessionID: sid})
	if err != nil {
		return "", err
	}
	var got protocol.PolicyGot
	if err := json.Unmarshal(raw, &got); err != nil {
		return "", err
	}
	return got.Project, nil
}

// changeSubmit builds change.submit from the caller's session and git.
func (d *Daemon) changeSubmit(ctx context.Context, sid string, info protocol.SessionInfo, a protocol.ChangeSubmitArgs) (protocol.ChangeSubmitResult, error) {
	var out protocol.ChangeSubmitResult
	folder := info.Repo.Worktree
	if folder == "" {
		folder = info.Cwd
	}
	if info.Repo.Root == "" || !isGitRepo(ctx, folder) {
		return out, fmt.Errorf("your session folder %s is not in a git repository with a commit: submit from a session in the change's repository", folder)
	}
	project, err := d.deliveryContextProject(ctx, sid)
	if err != nil {
		return out, fmt.Errorf("cannot resolve session's repo context: %w", err)
	}
	if project != info.Repo.Project {
		return out, fmt.Errorf("session's repo context %q does not match its folder's project %q", project, info.Repo.Project)
	}
	if a.Project != "" && a.Project != project {
		return out, fmt.Errorf("project %q is not your session's project %q: submit from a session in %q", a.Project, project, a.Project)
	}
	branch := a.Branch
	if branch == "" {
		current, err := gitOut(ctx, folder, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil || current == "" || current == "HEAD" {
			return out, fmt.Errorf("your session folder %s is not on a branch: give branch", folder)
		}
		branch = current
	}
	head, err := gitBranchHead(ctx, folder, branch)
	if err != nil {
		return out, err
	}
	commit := head
	if a.Commit != "" {
		if !protocol.ValidCommit(a.Commit) {
			return out, errors.New("commit must be the full hash of the branch head (40 or 64 hexadecimal characters)")
		}
		if _, err := gitOut(ctx, folder, "cat-file", "-e", a.Commit+"^{commit}"); err != nil {
			return out, fmt.Errorf("commit %s is not in the repository at %s", a.Commit, folder)
		}
		if a.Commit != head {
			if _, err := gitOut(ctx, folder, "merge-base", "--is-ancestor", a.Commit, head); err != nil {
				return out, fmt.Errorf("commit %s is not on branch %q (its head is %s)", a.Commit, branch, head)
			}
		}
		commit = a.Commit
	}
	// Changes that are not committed belong to the head, not to an older
	// commit.
	uncommitted := false
	if commit == head {
		if uncommitted, err = branchUncommitted(ctx, folder, branch); err != nil {
			return out, err
		}
	}
	q := protocol.ChangeSubmitRequest{SessionID: sid, ChangeID: a.Change, Project: project, Branch: branch, TargetBranch: a.TargetBranch,
		HeadCommit: commit, Uncommitted: uncommitted, Title: a.Title, Fixes: a.Fixes, FixesSeverity: a.FixesSeverity,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: sid, Model: info.Model, ModelFamily: protocol.ModelFamily(info.Harness, info.Model)}}
	if err := q.Validate(); err != nil {
		return out, err
	}
	if q.TargetBranch != "" {
		if target, err := gitBranchHead(ctx, folder, q.TargetBranch); err == nil {
			if base, err := gitOut(ctx, folder, "merge-base", target, commit); err == nil && protocol.ValidCommit(base) {
				q.BaseCommit = base
			}
		}
	}
	// A continuation already knows the commit its work started from. Use it
	// when the submit names no target branch, so the diff stat and the
	// sensitive path are computed instead of "diff unavailable". The stored
	// base is kept only when it still holds the new head.
	if q.BaseCommit == "" && a.Change != "" {
		var view protocol.ChangeView
		if err := d.deliveryCall(ctx, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: a.Change}, &view); err == nil && view.Change.Project == q.Project {
			if base := view.Change.BaseCommit; protocol.ValidCommit(base) {
				if _, err := gitOut(ctx, folder, "merge-base", "--is-ancestor", base, commit); err == nil {
					q.BaseCommit = base
				}
			}
		}
	}
	if protocol.ValidCommit(q.BaseCommit) {
		if stat, err := gitOut(ctx, folder, "diff", "--stat", q.BaseCommit, commit); err == nil {
			q.DiffStat = deliveryDiffStat(stat)
		}
	}
	q.SensitivePath = deliverySensitivePath(ctx, folder, q.BaseCommit, commit)
	if err := d.deliveryCall(ctx, protocol.FrameChangeSubmit, q, &out); err != nil {
		return out, err
	}
	out.Change = fillChangeView(out.Change)
	return out, nil
}

// gitBranchHead is the full hash of the head of a local branch.
func gitBranchHead(ctx context.Context, dir, branch string) (string, error) {
	head, err := gitOut(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if err != nil || !protocol.ValidCommit(head) {
		return "", fmt.Errorf("branch %q is not a local branch of the repository at %s", branch, dir)
	}
	return head, nil
}

// branchUncommitted reports whether the worktree that has branch checked out
// has changes that are not committed, untracked files included. A branch that
// no worktree has checked out has none.
func branchUncommitted(ctx context.Context, dir, branch string) (bool, error) {
	list, err := gitOut(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	path := ""
	for _, line := range strings.Split(list, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if line == "branch refs/heads/"+branch && path != "" {
			status, err := gitOut(ctx, path, "status", "--porcelain")
			if err != nil {
				return false, err
			}
			return status != "", nil
		}
	}
	return false, nil
}

// stepReport sends step.report for the calling session, after checking that
// done_by is a worker it started.
func (d *Daemon) stepReport(ctx context.Context, sid string, info protocol.SessionInfo, a protocol.StepReportArgs) (protocol.StepReportResult, error) {
	var out protocol.StepReportResult
	doneBy := a.DoneBy
	if doneBy == sid {
		doneBy = ""
	}
	q := protocol.StepReportRequest{SessionID: sid, ChangeID: a.Change, StepRef: a.StepRef, Result: a.Result, Commit: a.Commit,
		Fields: a.Fields, DoneBy: doneBy, Note: a.Note}
	if err := q.Validate(); err != nil {
		return out, err
	}
	if doneBy == protocol.EvidenceByOwner {
		if a.Step != protocol.StepApprove {
			return out, errors.New("done_by owner is only for a delegated approval step")
		}
	} else if doneBy != "" {
		if err := d.checkStartedWorker(ctx, sid, doneBy); err != nil {
			return out, err
		}
	}
	if q.Step == protocol.StepMerge && q.Result == protocol.StepResultPass && q.Fields[protocol.FieldMergedCommit] != "" {
		var view protocol.ChangeView
		if err := d.deliveryCall(ctx, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: q.ChangeID}, &view); err != nil {
			return out, err
		}
		// The gateway checks this again in its write transaction. If a new
		// revision arrives during the git check, this report cannot count.
		if q.Commit != view.Change.HeadCommit {
			return out, fmt.Errorf("commit %s is not the head of change %s (now at %s)", q.Commit, q.ChangeID, view.Change.HeadCommit)
		}
		folder, err := d.deliveryMergeRepo(ctx, sid, info, view.Change.Project)
		if err != nil {
			return out, err
		}
		kind, err := deliveryMergeKind(ctx, folder, q.Commit, q.Fields[protocol.FieldMergedCommit])
		if err != nil {
			return out, err
		}
		contains := kind != ""
		q.MergeContainsHead = &contains
		delete(q.Fields, "merge_kind")
		if contains {
			q.Fields["merge_kind"] = kind
		}
	}
	// Fill an omitted release tag when the local repository has a tag that
	// contains the merged commit. An explicit tag remains the reporter's
	// evidence, and repositories without local tags can still report a release.
	if q.Step == protocol.StepRelease && q.AfterMerge && q.Result == protocol.StepResultPass && q.Fields[protocol.FieldTag] == "" {
		var view protocol.ChangeView
		if err := d.deliveryCall(ctx, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: q.ChangeID}, &view); err != nil {
			return out, err
		}
		if q.Commit == view.Change.MergedCommit && protocol.ValidCommit(q.Commit) {
			folder, err := d.deliveryMergeRepo(ctx, sid, info, view.Change.Project)
			if err != nil {
				return out, err
			}
			tags, err := gitOut(ctx, folder, "tag", "--contains", q.Commit, "--sort=-version:refname")
			if err == nil && tags != "" {
				if q.Fields == nil {
					q.Fields = make(map[string]string)
				}
				q.Fields[protocol.FieldTag] = strings.SplitN(tags, "\n", 2)[0]
			}
		}
	}
	if err := d.deliveryCall(ctx, protocol.FrameStepReport, q, &out); err != nil {
		return out, err
	}
	out.Change = fillChangeView(out.Change)
	return out, nil
}

// checkStartedWorker checks with the gateway that session is a worker that
// caller started, on any machine.
func (d *Daemon) checkStartedWorker(ctx context.Context, caller, session string) error {
	raw, err := d.call(ctx, "query.agent", protocol.QueryAgent{SessionID: session})
	if err != nil {
		return fmt.Errorf("done_by %s: %v", session, err)
	}
	var r struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("done_by %s: %v", session, err)
	}
	if r.Agent.Worker == nil || r.Agent.Worker.Owner != caller {
		return fmt.Errorf("done_by %s is not a worker you started: name a worker you started, or leave done_by out when you did the work", session)
	}
	return nil
}

// changesFind reads one change with change.get, or a page of changes with
// change.list. An empty sid is the owner's read (changes_owner_find): it has no
// session, so it has no "me".
func (d *Daemon) changesFind(ctx context.Context, sid string, a protocol.ChangesFindArgs) (protocol.ChangesFindResult, error) {
	out := protocol.ChangesFindResult{Items: []protocol.ChangeView{}}
	caller := ""
	switch a.Caller {
	case "", protocol.ChangesCallerAnyone:
	case protocol.ChangesCallerMe:
		if sid == "" {
			return out, errors.New("caller \"me\" needs an agent session: the owner reads every change, so leave caller out")
		}
		caller = sid
	default:
		return out, fmt.Errorf("caller must be %q or %q", protocol.ChangesCallerMe, protocol.ChangesCallerAnyone)
	}
	if a.Change != "" {
		var view protocol.ChangeView
		if err := d.deliveryCall(ctx, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: a.Change}, &view); err != nil {
			return out, err
		}
		out.Items, out.Total = []protocol.ChangeView{fillChangeView(view)}, 1
		return out, nil
	}
	var page protocol.ChangeListResult
	q := protocol.ChangeListRequest{Project: a.Project, State: a.State, Caller: caller, Limit: a.Limit, After: a.Cursor}
	if err := d.deliveryCall(ctx, protocol.FrameChangeList, q, &page); err != nil {
		return out, err
	}
	for _, v := range page.Changes {
		out.Items = append(out.Items, fillChangeView(v))
	}
	out.Total, out.NextCursor = len(out.Items), page.Next
	if page.Next != "" {
		out.Note = "total counts this page; pass next_cursor as cursor for more"
	}
	return out, nil
}

// ownerStepReport reports the owner's try of a released change from the TUI.
// It is the owner's own report: the owner is not the caller or the step's
// owner, so the gateway accepts it only for the after-merge verify step, on
// the merged commit.
func (d *Daemon) ownerStepReport(ctx context.Context, a protocol.ChangesOwnerReportArgs) (protocol.StepReportResult, error) {
	var out protocol.StepReportResult
	session := d.ownerSessionID()
	if session == "" {
		return out, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	var view protocol.ChangeView
	if err := d.deliveryCall(ctx, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: a.Change}, &view); err != nil {
		return out, err
	}
	c := view.Change
	if c.State != protocol.ChangeMerged {
		return out, fmt.Errorf("change %s is %s, not merged: only a released change can be tried", c.ID, c.State)
	}
	if !protocol.ValidCommit(c.MergedCommit) {
		return out, fmt.Errorf("change %s has no merged commit to check", c.ID)
	}
	q := protocol.StepReportRequest{SessionID: session, ChangeID: c.ID,
		StepRef: protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true},
		Result:  a.Result, Commit: c.MergedCommit, DoneBy: protocol.EvidenceByOwner, Note: a.Note}
	if err := q.Validate(); err != nil {
		return out, err
	}
	if err := d.deliveryCall(ctx, protocol.FrameStepReport, q, &out); err != nil {
		return out, err
	}
	out.Change = fillChangeView(out.Change)
	return out, nil
}

// fillChangeView makes the lists the tool schemas require non-nil.
func fillChangeView(v protocol.ChangeView) protocol.ChangeView {
	if v.Steps == nil {
		v.Steps = []protocol.StepStatus{}
	}
	return v
}

// deliveryRecordTimeout is how long a hand-back waits for the gateway to
// record its change. The hand-back goes on without the record after it.
const deliveryRecordTimeout = 10 * time.Second

// deliveryDiffStat bounds the summary and removes terminal controls before it
// is sent to the gateway. Keep truncation on a UTF-8 boundary.
func deliveryDiffStat(stat string) string {
	stat = strings.ToValidUTF8(stat, "?")
	stat = strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return ' '
		}
		return r
	}, stat)
	if len(stat) <= protocol.MaxDiffStatBytes {
		return stat
	}
	const tail = "\n... (truncated)"
	end := protocol.MaxDiffStatBytes - len(tail)
	for end > 0 && !utf8.RuneStart(stat[end]) {
		end--
	}
	return stat[:end] + tail
}

const handbackUncommittedPushProblem = "the branch was not pushed for the remote step owner because the worktree has uncommitted changes"

// recordHandbackChange submits the change of a worker's complete hand-back
// (origin worker), after commitAtHandback made the head hold the work. The
// caller is the worker's owner. A continued worker carries the change of the
// worker it continues (its new branch has no open change yet), so the change
// follows the work. A worker with no worktree has no branch of its own and
// records nothing.
//
// It never fails the hand-back: it returns the change ID, or the error for the
// notice's "delivery record:" line. A gateway that does not know change.submit
// gives neither.
func (d *Daemon) recordHandbackChange(ctx context.Context, w *worker) (changeID, problem string) {
	d.wmu.Lock()
	dir, branch, base, owner, sid := w.worktree, w.branch, w.base, w.owner, w.sessionID
	harness, model, title, kept := w.harness, w.model, w.title, w.changeID
	difficulty := first(w.difficultyUsed, w.difficultyAsked)
	project := w.project
	d.wmu.Unlock()
	if dir == "" || branch == "" || owner == "" {
		return "", ""
	}
	timeout := deliveryRecordTimeout
	if d.cfg.testDeliveryRecordTimeout > 0 {
		timeout = d.cfg.testDeliveryRecordTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	fail := func(err error) (string, string) {
		d.Logf("worker.delivery_record worker=%s session=%s branch=%s error=%q", w.id, sid, branch, err.Error())
		return "", strings.Join(strings.Fields(err.Error()), " ")
	}
	head, err := gitBranchHead(ctx, dir, branch)
	if err != nil {
		return fail(err)
	}
	uncommitted, err := branchUncommitted(ctx, dir, branch)
	if err != nil {
		return fail(err)
	}
	// The project's identity, as a session in the worktree has it (the
	// origin remote), so a submit from the caller names the same project.
	if p := d.repo.Lookup(dir).Project; p != "" {
		project = p
	}
	if !slices.Contains(protocol.DifficultyLevels, difficulty) {
		difficulty = ""
	}
	if !protocol.ValidCommit(base) {
		base = ""
	}
	var risk int
	if err := d.st.db.QueryRow(`SELECT risk FROM worker_delivery_risk WHERE worker_id=?`, w.id).Scan(&risk); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fail(fmt.Errorf("read delivery risk: %w", err))
	}
	q := protocol.ChangeSubmitRequest{SessionID: owner, ChangeID: kept, Project: project, Branch: branch, BaseCommit: base,
		HeadCommit: head, Uncommitted: uncommitted, Risk: risk, SensitivePath: deliverySensitivePath(ctx, dir, base, head), Title: strings.TrimSpace(strings.SplitN(title, "\n", 2)[0]), Difficulty: difficulty,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, WorkerID: w.id, SessionID: sid, Model: model}}
	if base != "" {
		if stat, err := gitOut(ctx, dir, "diff", "--stat", base); err == nil {
			q.DiffStat = deliveryDiffStat(stat)
		}
	}
	if model != "" {
		q.Origin.ModelFamily = protocol.ModelFamily(harness, model)
	}
	if err := q.Validate(); err != nil {
		return fail(err)
	}
	var pushProblem string
	var plan protocol.DeliveryNeedsPushResult
	if err := d.deliveryCall(ctx, protocol.FrameDeliveryNeedsPush, protocol.DeliveryNeedsPushRequest{Project: project, CallerSessionID: owner}, &plan); err != nil {
		if !errors.Is(err, errNoDeliveryGateway) {
			pushProblem = "could not check whether a remote step owner needs the branch"
		}
	} else if plan.NeedsPush {
		if uncommitted {
			pushProblem = handbackUncommittedPushProblem
		} else {
			pushCtx, stopPush := context.WithTimeout(ctx, timeout/2)
			_, pushErr := gitOut(pushCtx, dir, "push", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
			stopPush()
			if pushErr != nil {
				pushProblem = "could not push the branch to origin for the remote step owner"
			}
		}
	}
	var out protocol.ChangeSubmitResult
	err = d.deliveryCall(ctx, protocol.FrameChangeSubmit, q, &out)
	if err != nil && kept != "" && strings.Contains(err.Error(), "takes no new head") {
		// The kept change was merged or closed: this work is a new change.
		q.ChangeID = ""
		out = protocol.ChangeSubmitResult{}
		err = d.deliveryCall(ctx, protocol.FrameChangeSubmit, q, &out)
	}
	if errors.Is(err, errNoDeliveryGateway) {
		return "", pushProblem
	}
	if err != nil {
		_, problem := fail(err)
		if pushProblem != "" {
			problem = pushProblem + "; " + problem
		}
		return "", problem
	}
	id := out.Change.Change.ID
	d.wmu.Lock()
	w.changeID = id
	d.wmu.Unlock()
	d.Logf("worker.delivery_record worker=%s session=%s change=%s revision=%d head=%s", w.id, sid, id, out.Change.Change.Revision, head)
	return id, pushProblem
}

// Keep the start's risk across daemon restarts. Risk is the judge's Risk
// field (K in its six-score note), not Reasoning (R). A later assessment
// cannot erase a higher risk already known for this worker.
func (d *Daemon) recordDeliveryRisk(workerID string, a *protocol.WorkerAssessment) error {
	if a == nil {
		return nil
	}
	if a.Risk < 0 || a.Risk > 5 {
		return errors.New("judge risk must be 0 to 5")
	}
	_, err := d.st.db.Exec(`INSERT INTO worker_delivery_risk(worker_id,risk) VALUES(?,?)
 ON CONFLICT(worker_id) DO UPDATE SET risk=MAX(risk,excluded.risk)`, workerID, a.Risk)
	return err
}

// Diff names, not the printed stat: -z preserves spaces and unusual names,
// and --no-renames checks both the removed and added path of a rename.
// Missing git evidence requires review rather than silently skipping it.
func deliverySensitivePath(ctx context.Context, dir, base, head string) string {
	if !protocol.ValidCommit(base) || !protocol.ValidCommit(head) {
		return "diff unavailable"
	}
	names, err := gitOut(ctx, dir, "diff", "--no-renames", "--name-only", "-z", base, head, "--")
	if err != nil {
		return "diff unavailable"
	}
	paths := strings.Split(names, "\x00")
	for _, name := range paths {
		if name == "testdata/migrations.lock" || strings.HasSuffix(name, "/testdata/migrations.lock") {
			return "migrations"
		}
	}
	for _, name := range paths {
		lowerPath := strings.ToLower(name)
		file := path.Base(lowerPath)
		sensitive := name == "internal/gateway/store.go" || name == "install.sh" || strings.HasPrefix(name, ".github/workflows/") ||
			(path.Dir(name) == "internal/daemon" && strings.HasPrefix(file, "files") && strings.HasSuffix(file, ".go")) ||
			strings.Contains(lowerPath, "auth") || strings.Contains(lowerPath, "token") || strings.Contains(lowerPath, "security")
		if sensitive {
			if len(name) > 1024 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				return "sensitive file"
			}
			return name
		}
	}
	return ""
}

// A step owner may work in its own project. Use the target project's local
// registration in that case, never a path from another machine's change.
func (d *Daemon) deliveryMergeRepo(ctx context.Context, sid string, info protocol.SessionInfo, project string) (string, error) {
	contextProject, err := d.deliveryContextProject(ctx, sid)
	if err != nil {
		return "", fmt.Errorf("cannot resolve session's repo context: %w", err)
	}
	if contextProject == project && info.Repo.Project == project && info.Repo.Root != "" {
		return first(info.Repo.Worktree, info.Repo.Root), nil
	}
	raw, err := d.call(ctx, "query.projects", protocol.QueryProjects{})
	if err != nil {
		return "", fmt.Errorf("cannot find merge repository: %w", err)
	}
	var projects protocol.ProjectsResult
	if err := json.Unmarshal(raw, &projects); err != nil {
		return "", fmt.Errorf("cannot read merge repositories: %w", err)
	}
	folder := ""
	for _, p := range projects.Projects {
		if p.NodeID != d.cfg.NodeID || p.Name != project {
			continue
		}
		if folder != "" && folder != p.Path {
			return "", fmt.Errorf("more than one local repository for project %q: report the merge from a session in the target repository", project)
		}
		folder = p.Path
	}
	if folder == "" {
		return "", fmt.Errorf("no local repository for project %q: report the merge from a session in that repository or register its local path", project)
	}
	return folder, nil
}

// deliveryMergeKind verifies either ancestry or the exact tree of a squash.
func deliveryMergeKind(ctx context.Context, folder, head, merged string) (string, error) {
	kind, err := gitOut(ctx, folder, "cat-file", "-t", merged)
	if err != nil || kind != "commit" {
		return "", nil
	}
	if _, err = gitOut(ctx, folder, "merge-base", "--is-ancestor", head, merged); err == nil {
		return "merge", nil
	}
	parents, err := gitOut(ctx, folder, "rev-list", "--parents", "-n", "1", merged)
	parts := strings.Fields(parents)
	if err != nil || len(parts) != 2 {
		return "", nil
	}
	version, err := gitOut(ctx, folder, "--version")
	var major, minor int
	if err != nil || func() bool { _, e := fmt.Sscanf(version, "git version %d.%d", &major, &minor); return e != nil }() {
		return "", fmt.Errorf("cannot determine git version for squash verification: %s", version)
	}
	if major < 2 || (major == 2 && minor < 38) {
		return "", fmt.Errorf("squash verification requires git 2.38 or newer; found %s", version)
	}
	tree, err := gitOut(ctx, folder, "merge-tree", "--write-tree", parts[1], head)
	if err != nil {
		return "", nil
	}
	actual, err := gitOut(ctx, folder, "rev-parse", merged+"^{tree}")
	if err == nil && tree == actual {
		return "squash", nil
	}
	return "", nil
}

func deliveryMergeContainsHead(ctx context.Context, folder, head, merged string) bool {
	kind, _ := deliveryMergeKind(ctx, folder, head, merged)
	return kind != ""
}
