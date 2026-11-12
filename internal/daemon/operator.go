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
	info := protocol.SessionInfo{Type: "owner", Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID,
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
			Body: p.Body, IdempotencyKey: p.IdempotencyKey, ConversationID: p.ConversationID})
		return data, true, err

	case protocol.OpWorkerProfiles, protocol.OpWorkerProfileAdd, protocol.OpWorkerProfileRemove:
		var p protocol.WorkerProfileRequest
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad worker profile request")
		}
		sid, ls, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		if ls != nil {
			d.mu.Lock()
			worker := ls.info.Worker != nil
			d.mu.Unlock()
			if worker {
				return nil, true, errors.New("workers cannot manage worker profiles")
			}
		}
		action := map[string]string{protocol.OpWorkerProfiles: "worker.profile.list", protocol.OpWorkerProfileAdd: "worker.profile.add", protocol.OpWorkerProfileRemove: "worker.profile.remove"}[method]
		data, err := d.call(ctx, action, protocol.WorkerProfileFrame{WorkerProfileRequest: p, Requester: sid})
		if err != nil {
			return nil, true, tooOldGateway(err)
		}
		return data, true, nil
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

	case protocol.OpPermissionPropose:
		// A proposal comes from a session, never from the command line. It
		// writes no file; the owner answers one owner-only ask.
		var p protocol.PermissionProposeRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad agent_permission_propose params")
		}
		if client == "cli" {
			return nil, true, errors.New("agent_permission_propose is for agent sessions, not the command line")
		}
		sid, _, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		out, err := d.proposePermission(ctx, sid, p)
		return out, true, err

	case protocol.OpCharterDraft:
		// A draft comes from an agent session, never from the command line. The
		// daemon adds the hash of the AGENTS.md it read (decision 0088).
		var p protocol.CharterDraftRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad agent_charter_draft params")
		}
		if client == "cli" {
			return nil, true, errors.New("agent_charter_draft is for agent sessions, not the command line")
		}
		sid, ls, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "charter.draft", protocol.CharterDraftFrame{CharterDraftRequest: p, SessionID: sid, SourceHash: d.agentsHashOf(ls)}); err != nil {
			return nil, true, tooOldGateway(err)
		}
		var out protocol.CharterDraftResult
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, true, err
		}
		return out, true, nil

	case protocol.OpRate:
		// A rating comes from an agent session, never the command line.
		var p protocol.RateRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad agent_worker_rate params")
		}
		if client == "cli" {
			return nil, true, errors.New("agent_worker_rate is for agent sessions, not the command line")
		}
		sid, _, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "rate.add", protocol.RateFrame{RateRequest: p, Rater: sid}); err != nil {
			return nil, true, tooOldGateway(err)
		}
		var out protocol.RateResult
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, true, err
		}
		return out, true, nil

	case protocol.OpWorkerDefect:
		// The owner's own path for a defect found later. It never carries a
		// score: the caller's agent_worker_rate remains the agent-side rating and
		// defect tool.
		if client != "cli" {
			return nil, true, errors.New("only the owner can report a defect found later")
		}
		var p protocol.DefectRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad worker_defect params")
		}
		sid, _, err := lc.callerSession(client, method)
		if err != nil {
			return nil, true, err
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "rate.add", protocol.RateFrame{RateRequest: protocol.RateRequest{
			Worker: p.Worker, Kind: protocol.RateKindDefectLater, Severity: p.Severity, Note: p.Note}, Rater: sid}); err != nil {
			return nil, true, tooOldGateway(err)
		}
		var out protocol.RateResult
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
				return nil, true, errors.New("no project: name one (agent_projects_find), or start the worker from a session that runs in a project folder")
			}
		}
		if client != "cli" {
			// An agent's harness gives up on a slow tool call: it may get an
			// "accepted" answer. The owner's command line and TUI wait.
			ctx = context.WithValue(ctx, answerEarlyKey{}, true)
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
			return nil, true, errors.New("agent_handback is for workers started through agent-net; you are not one: answer with agent_message_reply")
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
func checkedWorkerStart(p protocol.WorkerStartRequest) (protocol.WorkerStartRequest, error) {
	p.Project, p.Harness, p.Task = strings.TrimSpace(p.Project), strings.TrimSpace(p.Harness), strings.TrimSpace(p.Task)
	p.Continue, p.Title = strings.TrimSpace(p.Continue), strings.TrimSpace(p.Title)
	if err := protocol.ValidWorkerTitle(p.Title); err != nil {
		return p, err
	}
	if p.Continue != "" {
		if p.Machine != "" || p.Base != "" || p.Isolation != "" {
			return p, errors.New("a continued worker keeps the machine, the branch and the isolation of the old one: leave machine, base and isolation empty")
		}
	} else if p.Project == "" || p.Task == "" {
		return p, errors.New("worker_start needs project, harness and task")
	}
	if !protocol.ValidIsolation(p.Isolation) {
		return p, fmt.Errorf("isolation can be %q, %q or empty, not %q", protocol.IsolationWorktree, protocol.IsolationNone, p.Isolation)
	}
	if err := claudeStartChoiceValid(p.MCPServers); err != nil {
		return p, err
	}
	if p.MCPServers != "" {
		if p.Continue != "" {
			return p, errors.New("mcp_servers cannot be used with continue: a continued worker copies the choice of the main checkout; set it with session_change action mcp_servers")
		}
		if p.Harness != "" && p.Harness != "claude" {
			return p, errors.New("mcp_servers applies to Claude Code workers only")
		}
	}
	if len(protocol.WorkerTaskBody(p.Task, "/x", "b")) > protocol.MaxBodyBytes {
		return p, fmt.Errorf("the task is too long: the message limit is %d bytes", protocol.MaxBodyBytes)
	}

	return p, nil
}

func (d *Daemon) workerStartCall(ctx context.Context, caller string, p protocol.WorkerStartRequest, callerProject bool, callerPath string, fromCTO ...bool) (*protocol.WorkerStartResult, error) {
	cto := len(fromCTO) > 0 && fromCTO[0]
	ph := phasesOf(ctx)
	var err error
	end := ph.begin("validate")
	p, err = checkedWorkerStart(p)
	end()
	if err != nil {
		return nil, err
	}
	// An agent cannot override the picker without a human: the owner's words
	// or his answer on the Desk (worker_override.go). The owner's command line
	// and TUI and the CTO are not agents here.
	agent, _ := ctx.Value(answerEarlyKey{}).(bool)
	selection, hold, reason, err := d.checkOverride(caller, &p, agent && !cto)
	if err != nil {
		return nil, err
	}
	frame := protocol.WorkerStartFrame{WorkerStartRequest: p, Owner: caller, CallerProject: callerProject, CallerPath: callerPath, FromCTO: cto}
	if p.Continue == "" {
		// Everything that needs no judge is checked first, in a moment: the
		// project, the machine, the named model and its provider's allowance.
		end := ph.begin("precheck")
		err := d.precheckWorkerStart(ctx, frame, cto)
		end()
		if err != nil {
			return nil, err
		}
	}
	if hold {
		return d.holdStart(ctx, caller, p, reason, callerProject, callerPath)
	}
	run := func(ctx context.Context) (*protocol.WorkerStartResult, error) {
		return d.runWorkerStartCall(ctx, caller, p, frame, callerPath, cto, ph)
	}
	var res *protocol.WorkerStartResult
	if !agent || cto || p.Continue != "" {
		res, err = run(ctx)
	} else {
		res, err = d.startWithinBudget(ctx, caller, p, ph, run)
	}
	if res != nil && selection != "" {
		res.SelectionPath = selection
	}
	return res, err
}

// runWorkerStartCall judges the brief, asks the gateway to start the worker,
// then sends the task as a message from the caller.
func (d *Daemon) runWorkerStartCall(ctx context.Context, caller string, p protocol.WorkerStartRequest, frame protocol.WorkerStartFrame, callerPath string, cto bool, ph *callPhases) (*protocol.WorkerStartResult, error) {
	var err error
	if p.Continue == "" {
		// The judge runs here, before the gateway picks a machine or a model.
		// Its label is the level. The caller's difficulty is ignored, and a
		// failed judge never blocks the start.
		end := ph.begin("judge")
		frame.Assessment = d.assessWorkerStart(ctx, d.judgeDir(callerPath, p.Project), p.Task)
		end()
		frame.Level = protocol.WorkerLevel(frame.Assessment)
		// An older gateway still reads Difficulty; give it the judge's label.
		frame.Difficulty = frame.Level
	}
	cctx := withCallWait(ctx, d.workerSettings("").StartTimeout+40e9)
	var data json.RawMessage
	end := ph.begin("gateway")
	if cto {
		data, err = d.ctoAttributedCall(cctx, "worker.start", frame)
	} else {
		data, err = d.call(cctx, "worker.start", frame)
	}
	end()
	if err != nil {
		return nil, tooOldGateway(err)
	}
	var ans protocol.WorkerStartAnswer
	if err := json.Unmarshal(data, &ans); err != nil || (ans.SessionID == "" && !ans.Queued) {
		return nil, errors.New("the gateway answered without a worker session")
	}
	ph.note("gateway", ans.Phases)
	selected := protocol.ParseModelSelection(ans.Harness, ans.Provider, ans.Model, ans.Effort)
	ans.Provider, ans.Effort = selected.Provider, selected.Effort
	if ans.Provider == "" {
		ans.Provider = protocol.ModelVendor(ans.Harness, selected.Model)
	}
	if ans.Queued {
		// No room on the machine: the start waits there, and its owner's
		// machine sends the task when it begins (decision 0018).
		return &protocol.WorkerStartResult{Status: "queued", QueueID: ans.QueueID, Position: ans.Position, QueueReason: ans.Reason,
			NodeID: ans.NodeID, Hostname: ans.Hostname, Project: ans.Project, Path: ans.Path, Harness: ans.Harness,
			Model: ans.Model, Provider: ans.Provider, Effort: ans.Effort, ModelNote: ans.ModelNote, Title: ans.Title, MachineNote: ans.MachineNote,
			Permissions: ans.Permissions, Track: ans.Track, Playbooks: ans.Playbooks, OwnerLine: protocol.WorkerOwnerLine(frame.Assessment, protocol.ParseModelSelection(ans.Harness, "", ans.Model, "").Display()),
			Hint: ans.Hint}, nil
	}
	out := &protocol.WorkerStartResult{Status: "started", MachineNote: ans.MachineNote, SessionID: ans.SessionID, NodeID: ans.NodeID, Hostname: ans.Hostname,
		Project: ans.Project, Path: ans.Path, Harness: ans.Harness, Model: ans.Model, Provider: ans.Provider, Effort: ans.Effort, ModelNote: ans.ModelNote, Title: ans.Title, Worktree: ans.Worktree, Branch: ans.Branch,
		Permissions: ans.Permissions, OSSandbox: ans.OSSandbox, Track: ans.Track, Playbooks: ans.Playbooks, OwnerLine: protocol.WorkerOwnerLine(frame.Assessment, protocol.ParseModelSelection(ans.Harness, "", ans.Model, "").Display()),
		Hint: ans.Hint}
	if ans.Starting {
		out.Status = "starting"
		return out, nil
	}
	// The task is a normal message from the caller, with the fixed preamble.
	task := p.Task
	if ans.Task != "" {
		task = ans.Task // a continued worker gets the old first task
	}
	charter := d.charterBrief(ctx, ans.Project)
	end = ph.begin("task")
	raw, err := d.send(ctx, protocol.MsgSend{Sender: caller, Recipient: ans.SessionID, Type: protocol.TypeRequest, WorkerTask: true, WorkerTaskRef: ans.StartRef,
		Body: protocol.WorkerTaskBodyOS(ans.Harness, task, ans.Worktree, ans.Branch, ans.ProjectRules, charter, ans.Permissions, ans.OSSandbox, d.SettingsSnapshot(), ans.Project)})
	end()
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
	owner := ""
	if q.OwnerClaude != "" {
		d.mu.Lock()
		if ls := d.sessions[key("claude", q.OwnerClaude)]; ls != nil {
			owner = ls.sessionID
		}
		d.mu.Unlock()
	}
	// A Claude session's panel asks every few seconds, so the gateway reads
	// only that owner's workers, not every agent on the network. A session
	// the daemon does not know owns none, so the gateway is not asked at all.
	var r protocol.AgentsResult
	if q.OwnerClaude == "" || owner != "" {
		var data json.RawMessage
		var err error
		if owner != "" {
			// A Claude session's panel polls every few seconds: the answer
			// stays until the gateway pushes a change of that owner's rows.
			data, err = d.ownerAgents(ctx, owner)
		} else {
			data, err = d.call(ctx, "query.agents", protocol.QueryAgents{IncludeOwners: true})
		}
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
	}
	ws := protocol.WorkersOf(r.Agents)
	for i := range ws {
		ws[i].Task = d.workerTask(ws[i].SessionID)
	}
	if q.OwnerClaude != "" {
		kept := ws[:0]
		for _, v := range ws {
			if owner != "" && v.Owner == owner {
				kept = append(kept, v)
			}
		}
		ws = kept
	}
	if q.Recent {
		assessments := make(map[string]*protocol.WorkerAssessment)
		for _, a := range r.Agents {
			if a.Worker != nil {
				assessments[a.SessionID] = a.Worker.Assessment
			}
		}
		index := map[string]int{}
		for i, v := range ws {
			index[v.SessionID] = i
		}
		for _, v := range d.recentWorkers(time.Now()) {
			v.Assessment = assessments[v.SessionID]
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
	case "cto.worker_capabilities":
		data = map[string]bool{"attribution": true, "read_only_enforced": true}
	case "worker.launch":
		var p protocol.WorkerLaunch
		if err = json.Unmarshal(raw, &p); err == nil {
			data, err = d.startWorker(ctx, p)
		}
	case "worker.validate_stop":
		var p protocol.WorkerKill
		if err = json.Unmarshal(raw, &p); err == nil {
			err = d.stopWorkerSession(p, true)
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
			data, err = d.resumeWorkerSession(ctx, p.Session, p.Requester)
		}
	}
	if res, eerr := protocol.EncodeResult(h.ID, data, err); eerr == nil {
		l.write(ctx, res)
	}
}
