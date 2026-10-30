package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The operator interface on the daemon (decision 0007). The methods and their
// types are in protocol/operator.go. The data methods are served in local.go;
// this file has the owner pseudo-session and the actions.

// addOwnerSession registers the owner pseudo-session: the person at this
// machine, as a sender and a recipient. It is created here and nowhere else.
func (d *Daemon) addOwnerSession() {
	info := protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID,
		Name: protocol.OwnerName(d.cfg.Hostname), Status: "idle", Delivery: "owner", Mode: protocol.ModeForeground}
	d.sessions[key(info.Harness, info.NativeID)] = &localSession{info: info}
}

// ownerSessionID returns the gateway ID of the owner session, or "".
func (d *Daemon) ownerSessionID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ls := d.sessions[key(protocol.OwnerHarness, protocol.OwnerNativeID)]; ls != nil {
		return ls.sessionID
	}
	return ""
}

// launchFlag is what the daemon tells the gateway about workers.
func (d *Daemon) launchFlag() *bool {
	v := !d.cfg.NoLaunch
	return &v
}

// callerSession returns the session an action is made for: the owner session
// for the command line, else the bound session.
//
// The command line acts as the owner, so it goes through operatorGate, the one
// rule for owner-only methods (sessiontext.go): CLI only, and never from inside
// an agent session or a worker.
func (lc *localConn) callerSession(client, method string) (id string, ls *localSession, err error) {
	if client == "cli" {
		if err := lc.operatorGate(method); err != nil {
			return "", nil, err
		}
		id = lc.d.ownerSessionID()
		if id == "" {
			return "", nil, errors.New("the owner session is not registered yet (is the gateway reachable?)")
		}
		return id, nil, nil
	}
	lc.mu.Lock()
	ls = lc.ls
	lc.mu.Unlock()
	if ls == nil {
		return "", nil, errors.New("this method needs a bound session")
	}
	id, err = lc.boundID()
	return id, ls, err
}

// tooOldGateway makes the error of a gateway without worker frames clear.
func tooOldGateway(err error) error {
	if err != nil && strings.Contains(err.Error(), "unknown frame type") {
		return errors.New("the gateway is too old for workers; upgrade it with `agent-net upgrade` on the gateway machine")
	}
	return err
}

// dispatchActions serves the operator actions. handled is false for other
// methods.
func (lc *localConn) dispatchActions(ctx context.Context, client, method string, raw json.RawMessage) (res any, handled bool, err error) {
	d := lc.d
	switch method {
	case protocol.OpSend:
		// The owner as a sender. The sender is the owner session of this
		// machine, never a parameter.
		var p protocol.OwnerSendRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad owner_send params")
		}
		if client != "cli" {
			return nil, true, errors.New("only the owner can use owner_send")
		}
		sid, _, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		to, err := d.resolveTo(ctx, p.To)
		if err != nil {
			return nil, true, err
		}
		data, err := d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: to, Type: protocol.TypeRequest,
			Body: p.Body, IdempotencyKey: p.IdempotencyKey})
		return data, true, err

	case protocol.OpLearn:
		// A finding comes from an agent session, never from the command line.
		var p protocol.LearnRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad agent_learn params")
		}
		if client == "cli" {
			return nil, true, errors.New("agent_learn is for agent sessions, not the command line")
		}
		sid, _, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "learn.add", protocol.LearnFrame{LearnRequest: p, Reporter: sid}); err != nil {
			return nil, true, tooOldGateway(err)
		}
		var out protocol.LearnResult
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, true, err
		}
		return out, true, nil

	case protocol.OpWorkerStart:
		var p protocol.WorkerStartRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad worker_start params")
		}
		sid, ls, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		// A worker cannot start workers. The check is on the session's worker
		// flag, which the daemon set, not on anything the model says.
		if ls != nil {
			d.mu.Lock()
			isWorker := ls.info.Worker != nil
			d.mu.Unlock()
			if isWorker {
				d.Logf("worker.refused owner=%s project=%s harness=%s reason=%q", sid, p.Project, p.Harness, "a worker cannot start workers")
				return nil, true, errors.New("a worker cannot start workers")
			}
		}
		// No project given: use the caller's own (W17). The command line has no
		// project of its own, so it must name one.
		callerProject, callerPath := false, ""
		if strings.TrimSpace(p.Project) == "" && strings.TrimSpace(p.Continue) == "" {
			if ls != nil {
				d.mu.Lock()
				p.Project, callerPath = ls.info.Repo.Project, ls.info.Repo.Root
				d.mu.Unlock()
				callerProject = p.Project != ""
			}
			if p.Project == "" {
				return nil, true, errors.New("no project: name one (agent_project_list), or start the worker from a session that runs in a project folder")
			}
		}
		out, err := d.workerStartCall(ctx, sid, p, callerProject, callerPath)
		return out, true, err

	case protocol.OpWorkerHandback:
		// Only a worker's own session hands back, never the command line.
		var p protocol.WorkerHandbackRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad worker_handback params")
		}
		if client == "cli" {
			return nil, true, errors.New("worker_handback is for a worker's own session")
		}
		sid, ls, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		d.mu.Lock()
		isWorker := ls != nil && ls.info.Worker != nil
		d.mu.Unlock()
		if !isWorker {
			return nil, true, errors.New("agent_handback is for workers started through agent-net; you are not one: answer with agent_reply")
		}
		out, err := d.handback(ctx, sid, p)
		return out, true, err

	case protocol.OpWorkerStop:
		var p protocol.WorkerStopRequest
		if json.Unmarshal(raw, &p) != nil || p.Session == "" {
			return nil, true, errors.New("worker_stop needs session")
		}
		sid, _, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		cctx := withCallWait(ctx, 30e9)
		if _, err := d.call(cctx, "worker.stop", protocol.WorkerStopFrame{Session: p.Session, Requester: sid}); err != nil {
			return nil, true, tooOldGateway(err)
		}
		return protocol.WorkerStopResult{Session: p.Session, Stopped: true}, true, nil

	case protocol.OpWorkers:
		var q protocol.WorkersQuery
		if len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &q) != nil {
				return nil, true, errors.New("bad workers params")
			}
		}
		out, err := d.workersView(ctx, q)
		return out, true, err
	}
	return nil, false, nil
}

// workerStartCall asks the gateway to start a worker, then sends the task as
// a message from the caller.
func (d *Daemon) workerStartCall(ctx context.Context, caller string, p protocol.WorkerStartRequest, callerProject bool, callerPath string) (*protocol.WorkerStartResult, error) {
	p.Project, p.Harness, p.Task = strings.TrimSpace(p.Project), strings.TrimSpace(p.Harness), strings.TrimSpace(p.Task)
	p.Continue = strings.TrimSpace(p.Continue)
	if p.Continue != "" {
		if p.Machine != "" || p.Base != "" || p.Isolation != "" {
			return nil, errors.New("a continued worker keeps the machine, the branch and the isolation of the old one: leave machine, base and isolation empty")
		}
	} else if p.Project == "" || p.Harness == "" || p.Task == "" {
		return nil, errors.New("worker_start needs project, harness and task")
	}
	if !protocol.ValidIsolation(p.Isolation) {
		return nil, fmt.Errorf("isolation can be %q, %q or empty, not %q", protocol.IsolationWorktree, protocol.IsolationNone, p.Isolation)
	}
	if len(protocol.WorkerTaskBody(p.Task, "/x", "b")) > protocol.MaxBodyBytes {
		return nil, fmt.Errorf("the task is too long: the message limit is %d bytes", protocol.MaxBodyBytes)
	}
	cctx := withCallWait(ctx, d.cfg.Workers.filled().StartTimeout+40e9)
	data, err := d.call(cctx, "worker.start", protocol.WorkerStartFrame{WorkerStartRequest: p, Owner: caller, CallerProject: callerProject, CallerPath: callerPath})
	if err != nil {
		return nil, tooOldGateway(err)
	}
	var ans protocol.WorkerStartAnswer
	if err := json.Unmarshal(data, &ans); err != nil || (ans.SessionID == "" && !ans.Queued) {
		return nil, errors.New("the gateway answered without a worker session")
	}
	if ans.Queued {
		// No room on the machine: the start waits there, and its owner's
		// machine sends the task when it begins (decision 0018).
		return &protocol.WorkerStartResult{Status: "queued", QueueID: ans.QueueID, Position: ans.Position, QueueReason: ans.Reason,
			NodeID: ans.NodeID, Hostname: ans.Hostname, Project: ans.Project, Path: ans.Path, Harness: ans.Harness,
			Model: ans.Model, ModelNote: ans.ModelNote, MachineNote: ans.MachineNote,
			Permissions: ans.Permissions, Track: ans.Track, Playbooks: ans.Playbooks}, nil
	}
	out := &protocol.WorkerStartResult{Status: "started", MachineNote: ans.MachineNote, SessionID: ans.SessionID, NodeID: ans.NodeID, Hostname: ans.Hostname,
		Project: ans.Project, Path: ans.Path, Harness: ans.Harness, Model: ans.Model, ModelNote: ans.ModelNote, Worktree: ans.Worktree, Branch: ans.Branch,
		Permissions: ans.Permissions, Track: ans.Track, Playbooks: ans.Playbooks}
	// The task is a normal message from the caller, with the fixed preamble.
	task := p.Task
	if ans.Task != "" {
		task = ans.Task // a continued worker gets the old first task
	}
	raw, err := d.send(ctx, protocol.MsgSend{Sender: caller, Recipient: ans.SessionID, Type: protocol.TypeRequest,
		Body: protocol.WorkerTaskBodyWithPermissions(ans.Harness, task, ans.Worktree, ans.Branch, ans.ProjectRules, ans.Permissions)})
	if err != nil {
		return out, fmt.Errorf("worker %s started, but sending the task failed: %w", ans.SessionID, err)
	}
	var sent protocol.MessageSent
	if json.Unmarshal(raw, &sent) == nil {
		out.Task, out.Queued = sent.Message, sent.Queued
	}
	return out, nil
}

// workersView lists the workers on the network with their owners. The optional
// query keeps the workers of one local Claude session, and adds the workers
// that ended in the last two minutes.
func (d *Daemon) workersView(ctx context.Context, q protocol.WorkersQuery) (*protocol.WorkersResult, error) {
	data, err := d.call(ctx, "query.agents", protocol.QueryAgents{IncludeOwners: true})
	if err != nil {
		return nil, err
	}
	var r protocol.AgentsResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	ws := protocol.WorkersOf(r.Agents)
	for i := range ws {
		ws[i].Task = d.workerTask(ws[i].SessionID)
	}
	owner := ""
	if q.OwnerClaude != "" {
		d.mu.Lock()
		if ls := d.sessions[key("claude", q.OwnerClaude)]; ls != nil {
			owner = ls.sessionID
		}
		d.mu.Unlock()
		kept := ws[:0]
		for _, v := range ws {
			if owner != "" && v.Owner == owner {
				kept = append(kept, v)
			}
		}
		ws = kept
	}
	if q.Recent {
		index := map[string]int{}
		for i, v := range ws {
			index[v.SessionID] = i
		}
		for _, v := range d.recentWorkers(time.Now()) {
			if q.OwnerClaude != "" && v.Owner != owner {
				continue
			}
			// The gateway may still list the session for a moment, without the
			// end it reached. The recent entry has it.
			if i, ok := index[v.SessionID]; ok {
				ws[i] = v
				continue
			}
			ws = append(ws, v)
		}
	}
	return &protocol.WorkersResult{Workers: ws}, nil
}

// onWorkerFrame serves "worker.launch" and "worker.kill" from the gateway.
func (d *Daemon) onWorkerFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var data any
	var err error
	switch h.Type {
	case "worker.launch":
		var p protocol.WorkerLaunch
		if err = json.Unmarshal(raw, &p); err == nil {
			data, err = d.startWorker(ctx, p)
		}
	case "worker.kill":
		var p protocol.WorkerKill
		if err = json.Unmarshal(raw, &p); err == nil {
			err = d.stopWorkerSession(p)
		}
	case "worker.task":
		var p protocol.WorkerQueuedStart
		if err = json.Unmarshal(raw, &p); err == nil {
			err = d.sendQueuedTask(ctx, p)
		}
	case "worker.continue":
		var p protocol.WorkerContinue
		if err = json.Unmarshal(raw, &p); err == nil {
			data, err = d.continueWorker(ctx, p)
		}
	case "worker.resume":
		var p protocol.WorkerResume
		if err = json.Unmarshal(raw, &p); err == nil {
			err = d.resumeWorkerSession(ctx, p.Session, p.Requester)
		}
	}
	if res, eerr := protocol.EncodeResult(h.ID, data, err); eerr == nil {
		l.write(ctx, res)
	}
}
