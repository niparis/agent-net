package daemon

// The daemon side of the delivery tools agent_change_submit, agent_step_report
// and agent_changes_find (decision 0057, docs/design/delivery-pipeline.md).
// The daemon fills in what only it can know: the calling session, its project,
// the branch head from git and whether the work is committed, and the model.
// It checks that done_by is a worker the caller started, then sends the
// gateway frames. The caller is never taken from the arguments.
//
// TODO(delivery): a worker's complete hand-back submits its change too
// (origin worker), from handbackToNote in worker_notice.go. It is not hooked
// in yet because another worker is changing that code; when it is, reuse
// gitBranchHead and branchUncommitted below.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// dispatchDelivery serves the delivery tool methods. handled is false for
// other methods.
func (lc *localConn) dispatchDelivery(ctx context.Context, client, method string, raw json.RawMessage) (res any, handled bool, err error) {
	switch method {
	case protocol.OpChangeSubmit, protocol.OpStepReport, protocol.OpChangesFind:
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
	case protocol.OpStepReport:
		if info.Worker != nil {
			return nil, true, errors.New("a worker does not report delivery steps: its caller reports, with done_by naming the worker")
		}
		var a protocol.StepReportArgs
		if err := decodeDeliveryArgs(method, raw, &a); err != nil {
			return nil, true, err
		}
		res, err := d.stepReport(ctx, sid, a)
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

// deliveryCall sends one delivery frame to the gateway. A gateway's refusal
// reaches the agent as it is; a gateway without the frames is named as too old.
func (d *Daemon) deliveryCall(ctx context.Context, typ string, params, out any) error {
	raw, err := d.call(ctx, typ, params)
	if err != nil {
		if strings.Contains(err.Error(), "unknown frame type") {
			return errors.New("the gateway does not know the delivery pipeline yet; upgrade it with `agent-net upgrade` on the gateway machine")
		}
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("bad %s answer from the gateway: %v", typ, err)
	}
	return nil
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
	project := info.Repo.Project
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
		HeadCommit: commit, Uncommitted: uncommitted, Title: a.Title,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: sid, Model: info.Model, ModelFamily: protocol.ModelFamily(info.Harness, info.Model)}}
	if err := q.Validate(); err != nil {
		return out, err
	}
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
func (d *Daemon) stepReport(ctx context.Context, sid string, a protocol.StepReportArgs) (protocol.StepReportResult, error) {
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
	if doneBy != "" {
		if err := d.checkStartedWorker(ctx, sid, doneBy); err != nil {
			return out, err
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
// change.list.
func (d *Daemon) changesFind(ctx context.Context, sid string, a protocol.ChangesFindArgs) (protocol.ChangesFindResult, error) {
	out := protocol.ChangesFindResult{Items: []protocol.ChangeView{}}
	caller := ""
	switch a.Caller {
	case "", protocol.ChangesCallerAnyone:
	case protocol.ChangesCallerMe:
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

// fillChangeView makes the lists the tool schemas require non-nil.
func fillChangeView(v protocol.ChangeView) protocol.ChangeView {
	if v.Steps == nil {
		v.Steps = []protocol.StepStatus{}
	}
	return v
}
