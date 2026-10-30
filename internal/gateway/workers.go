package gateway

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Workers (decision 0002). The gateway never runs a process. It checks a
// request, finds the machine that holds the project, and asks that machine's
// daemon to start the worker. Every refusal is an event and a log line.

// launchWait is how long the gateway waits for a machine to start a worker.
const launchWait = 75 * time.Second

func (g *Gateway) serveWorker(ctx context.Context, nc *nodeConn, h protocol.Header, raw []byte) {
	data, err := g.handleWorker(ctx, nc, h.Type, raw)
	if err != nil {
		var rj Rejection
		if !errors.As(err, &rj) {
			g.logLine("error", "node", nc.id, "where", h.Type, "err", err)
		}
	}
	reply, eerr := protocol.EncodeResult(h.ID, data, err)
	if eerr != nil {
		g.logLine("error", "where", "encode result", "err", eerr)
		return
	}
	nc.write(ctx, reply)
}

func (g *Gateway) handleWorker(ctx context.Context, nc *nodeConn, typ string, raw []byte) (any, error) {
	switch typ {
	case "worker.start":
		p, err := decode[protocol.WorkerStartFrame](raw)
		if err != nil {
			return nil, reject("bad worker.start")
		}
		out, err := g.workerStart(ctx, nc, p)
		var rj Rejection
		if errors.As(err, &rj) {
			g.workerRefused(nc, p.Owner, p, string(rj))
		}
		return out, err

	case "worker.stop":
		p, err := decode[protocol.WorkerStopFrame](raw)
		if err != nil {
			return nil, reject("bad worker.stop")
		}
		return g.workerStop(ctx, nc, p)

	case "worker.stopped":
		p, err := decode[protocol.WorkerStopped](raw)
		if err != nil {
			return nil, reject("bad worker.stopped")
		}
		owner, err := g.store.SessionNode(p.SessionID)
		if err != nil {
			return nil, err
		}
		if owner != nc.id {
			return nil, reject("session %q does not belong to this node", p.SessionID)
		}
		if p.Reason == protocol.StopModelFails && p.Model != "" {
			if err := g.store.RecordModelFailure(nc.id, p.Harness, p.Model); err != nil {
				return nil, err
			}
		}
		// The run feeds the model track record (decision 0022). An older
		// machine sends no start, model or hand-back status: the session row
		// has what it stored, and a hand-back without a status counts complete.
		started, storedHarness, storedModel, _, err := g.store.WorkerSessionFacts(p.SessionID)
		if err != nil {
			return nil, err
		}
		if p.Harness == "" {
			p.Harness = storedHarness
		}
		if p.Model == "" {
			p.Model = storedModel
		}
		if p.StartedAt == 0 {
			p.StartedAt = started
		}
		end := g.store.now().Unix()
		runSeconds := int64(0)
		if p.StartedAt > 0 && end > p.StartedAt {
			runSeconds = end - p.StartedAt
		}
		run := protocol.WorkerRun{NodeID: nc.id, SessionID: p.SessionID, Harness: p.Harness, Model: p.Model,
			Outcome: protocol.WorkerOutcome(p.Reason, p.Status, p.Complete), StartedAt: p.StartedAt, EndedAt: end,
			RunSeconds: runSeconds}
		if err := g.store.RecordWorkerRun(run); err != nil {
			return nil, err
		}
		// The worker has ended: it leaves the worker list, and its record stays
		// (offline) only for a follow-up that starts it again.
		if err := g.store.SetSessionOffline(nc.id, p.SessionID); err != nil {
			return nil, err
		}
		detail := strings.TrimPrefix(pairs("reason", p.Reason, "harness", p.Harness, "project", p.Project, "model", p.Model), " ")
		g.store.Event("worker.stopped", nc.id, p.SessionID, "", detail)
		g.logLine("worker.stopped", "node", nc.id, "host", g.store.hostname(nc.id), "session", p.SessionID,
			"worker", p.WorkerID, "reason", p.Reason, "harness", p.Harness, "project", p.Project)
		return nil, nil

	case "worker.not_started":
		p, err := decode[protocol.WorkerNotStarted](raw)
		if err != nil {
			return nil, reject("bad worker.not_started")
		}
		if len(p.Reason) > 300 {
			p.Reason = p.Reason[:300]
		}
		g.store.Event("worker.not_started", nc.id, p.Owner, "",
			strings.TrimPrefix(pairs("project", p.Project, "harness", p.Harness, "reason", p.Reason), " "))
		g.logLine("worker.not_started", "node", nc.id, "host", g.store.hostname(nc.id), "owner", p.Owner,
			"project", p.Project, "harness", p.Harness, "reason", p.Reason)
		return nil, nil

	case "worker.queued_start":
		p, err := decode[protocol.WorkerQueuedStart](raw)
		if err != nil {
			return nil, reject("bad worker.queued_start")
		}
		return nil, g.relayQueuedTask(ctx, nc, p)

	case "worker.cleanup":
		p, err := decode[protocol.WorkerCleanup](raw)
		if err != nil || p.MaxAgeSeconds <= 0 {
			return nil, reject("bad worker.cleanup")
		}
		n, err := g.store.CleanWorkers(nc.id, time.Duration(p.MaxAgeSeconds)*time.Second, p.Running)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			g.logLine("worker.cleaned", "node", nc.id, "host", g.store.hostname(nc.id), "workers", n)
		}
		if p.WorktreesDir != "" {
			ghosts, err := g.store.CleanWorktreeSessions(nc.id, p.WorktreesDir)
			if err != nil {
				return nil, err
			}
			if ghosts > 0 {
				g.logLine("worker.ghosts.cleaned", "node", nc.id, "host", g.store.hostname(nc.id), "sessions", ghosts)
			}
		}
		return map[string]any{"cleaned": n}, nil
	}
	return nil, reject("unknown frame type %q", typ)
}

// workerRefused records a refused start as an event and a log line.
func (g *Gateway) workerRefused(nc *nodeConn, owner string, p protocol.WorkerStartFrame, reason string) {
	g.store.Event("worker.refused", nc.id, owner, "",
		strings.TrimPrefix(pairs("project", p.Project, "harness", p.Harness, "model", p.Model, "reason", reason), " "))
	g.logLine("worker.refused", "node", nc.id, "host", g.store.hostname(nc.id), "owner", owner,
		"project", p.Project, "harness", p.Harness, "model", p.Model, "reason", reason)
}

// workerCandidates finds the project rows a start request can mean.
// An absolute path matches the main tree or a listed worktree of a known
// project. The caller's own folder (CallerPath) comes first, on the caller's
// machine. Else the name rule applies, and on each machine a registered
// folder wins over seen-only folders of the same name.
func (g *Gateway) workerCandidates(callerNode string, p protocol.WorkerStartFrame) ([]protocol.ProjectView, error) {
	onMachine := func(v protocol.ProjectView) bool {
		return p.Machine == "" || strings.EqualFold(v.Hostname, p.Machine) || v.NodeID == p.Machine
	}
	byPath := func(path, node string) ([]protocol.ProjectView, error) {
		all, err := g.store.Projects("")
		if err != nil {
			return nil, err
		}
		path = filepath.Clean(path)
		var out []protocol.ProjectView
		for _, v := range all {
			if (node != "" && v.NodeID != node) || !onMachine(v) {
				continue
			}
			hit := v.Path == path
			for _, w := range v.Worktrees {
				hit = hit || w == path
			}
			if hit {
				out = append(out, v)
			}
		}
		return out, nil
	}
	if filepath.IsAbs(p.Project) {
		return byPath(p.Project, "")
	}
	if p.CallerProject && p.CallerPath != "" {
		out, err := byPath(p.CallerPath, callerNode)
		if err != nil || len(out) > 0 {
			return out, err
		}
	}
	projects, err := g.store.Projects(p.Project)
	if err != nil {
		return nil, err
	}
	registered := map[string]bool{}
	for _, v := range projects {
		if strings.EqualFold(v.Name, p.Project) && v.Source != protocol.SourceSeen {
			registered[v.NodeID] = true
		}
	}
	var cands []protocol.ProjectView
	for _, v := range projects {
		if !strings.EqualFold(v.Name, p.Project) || !onMachine(v) {
			continue
		}
		if v.Source == protocol.SourceSeen && registered[v.NodeID] {
			continue
		}
		cands = append(cands, v)
	}
	return cands, nil
}

func (g *Gateway) workerStart(ctx context.Context, nc *nodeConn, p protocol.WorkerStartFrame) (protocol.WorkerStartAnswer, error) {
	var none protocol.WorkerStartAnswer
	p.Project, p.Harness = strings.TrimSpace(p.Project), strings.TrimSpace(p.Harness)
	p.Model, p.Machine = protocol.CleanModel(p.Model), strings.TrimSpace(p.Machine)
	p.Continue = strings.TrimSpace(p.Continue)
	if p.Continue == "" && p.Project == "" {
		return none, reject("a worker needs a project")
	}
	if !protocol.WorkerHarnessSupported(p.Harness) && !(p.Continue != "" && p.Harness == "") {
		return none, reject("workers can run these harnesses: %s (not %q)", strings.Join(protocol.WorkerHarnesses, ", "), p.Harness)
	}
	if p.Continue == "" && strings.TrimSpace(p.Task) == "" {
		return none, reject("a worker needs a task")
	}
	if !protocol.ValidIsolation(p.Isolation) {
		return none, reject("isolation can be %q, %q or empty, not %q", protocol.IsolationWorktree, protocol.IsolationNone, p.Isolation)
	}
	// The caller must be a session of the sending node, and not a worker.
	owner, ok, err := g.store.Agent(p.Owner)
	if err != nil {
		return none, err
	}
	if !ok || owner.NodeID != nc.id {
		return none, reject("owner session %q does not belong to this node", p.Owner)
	}
	if owner.Worker != nil {
		return none, reject("a worker cannot start workers")
	}

	if p.Continue != "" {
		return g.workerContinue(ctx, nc, p)
	}

	// Which machine and folder.
	cands, err := g.workerCandidates(nc.id, p)
	if err != nil {
		return none, err
	}
	if len(cands) == 0 {
		switch {
		case filepath.IsAbs(p.Project) && p.Machine != "":
			return none, reject("no known project at the path %q on machine %q (see `agent-net projects`)", p.Project, p.Machine)
		case filepath.IsAbs(p.Project):
			return none, reject("no known project at the path %q: register it with `agent-net projects add <path>` (see `agent-net projects`)", p.Project)
		case p.Machine != "":
			return none, reject("no project named %q on machine %q (see `agent-net projects`)", p.Project, p.Machine)
		}
		return none, reject("no project named %q is known: register it with `agent-net projects add <path>` (see `agent-net projects`)", p.Project)
	}
	info, err := g.NodesInfo()
	if err != nil {
		return none, err
	}
	nodeByID := func(id string) *protocol.NodeView {
		for i := range info.Nodes {
			if info.Nodes[i].NodeID == id {
				return &info.Nodes[i]
			}
		}
		return nil
	}
	nodes := map[string]bool{}
	for _, v := range cands {
		nodes[v.NodeID] = true
	}
	if len(nodes) > 1 && p.CallerProject {
		// The project came from the caller's own session: use the machine
		// that holds it and has the harness. The caller's own machine wins.
		hasHarness := func(id string) bool {
			n := nodeByID(id)
			if n == nil || !n.Online || n.Launch == nil || !*n.Launch {
				return false
			}
			for _, h := range n.Harnesses {
				if h.Name == p.Harness {
					return true
				}
			}
			return false
		}
		var keep []protocol.ProjectView
		for _, v := range cands {
			if hasHarness(v.NodeID) {
				keep = append(keep, v)
			}
		}
		own := keep[:0:0]
		for _, v := range keep {
			if v.NodeID == nc.id {
				own = append(own, v)
			}
		}
		if len(own) > 0 {
			keep = own
		}
		if len(keep) > 0 {
			cands = keep
			nodes = map[string]bool{}
			for _, v := range cands {
				nodes[v.NodeID] = true
			}
		}
	}
	var machineNote string
	if len(nodes) > 1 {
		// Several machines can run it: the one with the most room wins, when
		// every one of them reports its room (decision 0018).
		if keep, note := g.mostHeadroom(cands, nodeByID, p.Harness); len(keep) > 0 {
			cands, machineNote = keep, note
			nodes = map[string]bool{}
			for _, v := range cands {
				nodes[v.NodeID] = true
			}
		}
	}
	if len(nodes) > 1 {
		var hosts []string
		for _, v := range cands {
			hosts = append(hosts, v.Hostname)
		}
		sort.Strings(hosts)
		return none, reject("project %q is on several machines (%s): say which one with a machine", p.Project, strings.Join(hosts, ", "))
	}
	// One machine. Prefer the registered folder when seen folders also exist.
	pick := cands[0]
	paths := map[string]bool{}
	for _, v := range cands {
		paths[v.Path] = true
		if v.Source != protocol.SourceSeen && pick.Source == protocol.SourceSeen {
			pick = v
		}
	}
	if len(paths) > 1 {
		var ps []string
		for pth := range paths {
			ps = append(ps, pth)
		}
		sort.Strings(ps)
		return none, reject("%d folders named %q on %s (%s): pass one of these paths as project, or register one and unregister the others", len(ps), pick.Name, pick.Hostname, strings.Join(ps, ", "))
	}

	// The machine must be there, accept workers, and have the harness and model.
	node := nodeByID(pick.NodeID)
	if node == nil || !node.Online {
		return none, reject("machine %s is offline", pick.Hostname)
	}
	switch {
	case node.Launch == nil:
		return none, reject("machine %s runs an older agent-net that cannot start workers: upgrade it", node.Hostname)
	case !*node.Launch:
		return none, reject("machine %s does not accept workers (it was set up with --no-launch)", node.Hostname)
	case node.Harnesses == nil:
		return none, reject("machine %s has not reported its harnesses yet", node.Hostname)
	}
	var hi *protocol.HarnessInfo
	for i := range node.Harnesses {
		if node.Harnesses[i].Name == p.Harness {
			hi = &node.Harnesses[i]
		}
	}
	if hi == nil {
		return none, reject("%s is not installed on %s", p.Harness, node.Hostname)
	}
	// A loose model name is resolved against the machine's own list (W17).
	// Without a list (claude, or a harness that cannot list), it passes through.
	var modelNote string
	if p.Model != "" && len(hi.Models) > 0 {
		usage, err := g.store.ModelUsage(pick.NodeID, p.Harness, time.Duration(protocol.ModelUsageDays)*24*time.Hour)
		if err != nil {
			return none, err
		}
		asked := p.Model
		failedAt, err := g.store.FailedModels(pick.NodeID, p.Harness)
		if err != nil {
			return none, err
		}
		res, err := protocol.ResolveModelFailed(asked, hi.Models, usage, hi.ModelProviders, failedSet(failedAt))
		if err != nil {
			return none, reject("model on %s: %v", node.Hostname, err)
		}
		resolved := res.Model
		if resolved != asked || len(res.Demoted) > 0 {
			modelNote = asked + " -> " + resolved
		}
		if len(res.Demoted) > 0 {
			var parts []string
			for _, m := range res.Demoted {
				parts = append(parts, fmt.Sprintf("%s failed a worker at %s", m, failedAt[m].Format("15:04 on 2006-01-02")))
			}
			sort.Strings(parts)
			if failedAt[resolved].IsZero() {
				modelNote += " (" + strings.Join(parts, "; ") + ", so it ranks last for " + fmt.Sprint(protocol.ModelFailureHours) + " hours)"
			} else {
				modelNote += " (" + strings.Join(parts, "; ") + ": no other model matched)"
			}
		}
		p.Model = resolved
	}
	track, books, err := g.choiceNotes(pick.NodeID, pick.Hostname, pick.Name, p.Harness, p.Model)
	if err != nil {
		return none, err
	}

	g.mu.Lock()
	target := g.conns[pick.NodeID]
	g.mu.Unlock()
	if target == nil {
		return none, reject("machine %s is offline", pick.Hostname)
	}
	// The owner sets the permission level per project (decision 0020).
	perms, err := g.store.WorkerPermissions(pick.NodeID, pick.Name)
	if err != nil {
		return none, err
	}
	cctx, cancel := context.WithTimeout(ctx, launchWait)
	defer cancel()
	raw, err := target.call(cctx, "worker.launch", protocol.WorkerLaunch{Project: pick.Name, Path: pick.Path,
		Harness: p.Harness, Model: p.Model, Owner: p.Owner, Task: p.Task, Isolation: p.Isolation, Base: p.Base, Permissions: perms})
	if err != nil {
		// The target daemon's refusal (its limit, its checks) is a rule, not a fault.
		if errors.Is(err, errNodeGone) || errors.Is(err, context.DeadlineExceeded) {
			return none, reject("machine %s did not start the worker: %v", pick.Hostname, err)
		}
		return none, reject("machine %s refused the worker: %v", pick.Hostname, err)
	}
	launched, err := decode[protocol.WorkerLaunched](raw)
	if err == nil && launched.Queued && launched.QueueID != "" {
		g.store.Event("worker.queued", pick.NodeID, p.Owner, "",
			strings.TrimPrefix(pairs("project", pick.Name, "harness", p.Harness, "model", p.Model, "owner", p.Owner,
				"queue", launched.QueueID, "position", fmt.Sprint(launched.Position)), " "))
		g.logLine("worker.queued", "node", pick.NodeID, "host", pick.Hostname, "queue", launched.QueueID,
			"project", pick.Name, "harness", p.Harness, "model", p.Model, "owner", p.Owner, "position", launched.Position,
			"reason", launched.Reason)
		return protocol.WorkerStartAnswer{NodeID: pick.NodeID, Hostname: pick.Hostname, Project: pick.Name, Path: pick.Path,
			Harness: p.Harness, Model: p.Model, ModelNote: modelNote, Queued: true, QueueID: launched.QueueID,
			Position: launched.Position, Reason: launched.Reason, MachineNote: machineNote, Permissions: perms,
			Track: track, Playbooks: books}, nil
	}
	if err != nil || launched.SessionID == "" {
		return none, reject("machine %s answered without a session", pick.Hostname)
	}
	g.store.Event("worker.started", pick.NodeID, launched.SessionID, "",
		strings.TrimPrefix(pairs("project", pick.Name, "harness", p.Harness, "model", p.Model, "owner", p.Owner,
			"isolation", p.Isolation, "branch", launched.Branch), " "))
	g.logLine("worker.started", "node", pick.NodeID, "host", pick.Hostname, "session", launched.SessionID,
		"worker", launched.WorkerID, "project", pick.Name, "path", pick.Path, "harness", p.Harness, "model", p.Model,
		"owner", p.Owner, "from_node", nc.id, "isolation", p.Isolation, "permissions", perms, "worktree", launched.Worktree, "branch", launched.Branch)
	return protocol.WorkerStartAnswer{SessionID: launched.SessionID, NodeID: pick.NodeID, Hostname: pick.Hostname,
		Project: pick.Name, Path: pick.Path, Harness: p.Harness, Model: p.Model, ModelNote: modelNote,
		Worktree: launched.Worktree, Branch: launched.Branch, ProjectRules: launched.ProjectRules, MachineNote: machineNote, Permissions: perms,
		Track: track, Playbooks: books}, nil
}

// workerContinue starts a new worker from an ended one. The gateway knows the
// node of each worker session, so it asks the machine that ran the old worker.
// The machine holds the old task and branch and checks the owner again.
func (g *Gateway) workerContinue(ctx context.Context, nc *nodeConn, p protocol.WorkerStartFrame) (protocol.WorkerStartAnswer, error) {
	var none protocol.WorkerStartAnswer
	if p.Machine != "" || p.Base != "" || p.Isolation != "" {
		return none, reject("a continued worker keeps the machine, the branch and the isolation of the old one")
	}
	old, ok, err := g.store.Agent(p.Continue)
	if err != nil {
		return none, err
	}
	if !ok || old.Worker == nil {
		// Not a session ID: try the worker ID that the machine gave it.
		all, err := g.store.Agents(protocol.QueryAgents{IncludeOwners: true})
		if err != nil {
			return none, err
		}
		ok = false
		for _, a := range all {
			if a.Worker != nil && a.Worker.ID == p.Continue && a.Worker.Owner == p.Owner {
				old, ok = a, true
				break
			}
		}
	}
	if !ok || old.Worker == nil {
		return none, reject("no worker with the session or worker ID %q is known (records of ended workers are dropped after a while)", p.Continue)
	}
	if old.Worker.Owner != p.Owner {
		return none, reject("worker %s ended, and only the session that started it can continue it", old.SessionID)
	}
	if old.Online {
		return none, reject("worker %s is still running: wait for it to end, or stop it first", old.SessionID)
	}
	if p.Project != "" && old.Worker.Project != "" && !strings.EqualFold(p.Project, old.Worker.Project) {
		return none, reject("worker %s ran in project %q, not %q: a continued worker keeps its project", old.SessionID, old.Worker.Project, p.Project)
	}
	p.Harness = strings.TrimSpace(p.Harness)
	if p.Harness != "" && !protocol.WorkerHarnessSupported(p.Harness) {
		return none, reject("workers can run these harnesses: %s (not %q)", strings.Join(protocol.WorkerHarnesses, ", "), p.Harness)
	}
	g.mu.Lock()
	target := g.conns[old.NodeID]
	g.mu.Unlock()
	if target == nil {
		return none, reject("machine %s is offline, so worker %s cannot be continued", old.Hostname, old.SessionID)
	}
	cctx, cancel := context.WithTimeout(ctx, launchWait)
	defer cancel()
	raw, err := target.call(cctx, "worker.continue", protocol.WorkerContinue{Session: old.SessionID, Requester: p.Owner,
		Harness: p.Harness, Model: p.Model, Task: strings.TrimSpace(p.Task)})
	if err != nil {
		if errors.Is(err, errNodeGone) || errors.Is(err, context.DeadlineExceeded) {
			return none, reject("machine %s did not start the worker: %v", old.Hostname, err)
		}
		return none, reject("machine %s refused the worker: %v", old.Hostname, err)
	}
	c, err := decode[protocol.WorkerContinued](raw)
	if err == nil {
		if track, books, nerr := g.choiceNotes(old.NodeID, old.Hostname, c.Project, c.Harness, c.Model); nerr == nil {
			c.Track, c.Playbooks = track, books
		}
	}
	if err == nil && c.Queued && c.QueueID != "" {
		g.store.Event("worker.queued", old.NodeID, p.Owner, "",
			strings.TrimPrefix(pairs("project", c.Project, "harness", c.Harness, "model", c.Model, "owner", p.Owner,
				"queue", c.QueueID, "position", fmt.Sprint(c.Position), "continues", old.SessionID), " "))
		g.logLine("worker.queued", "node", old.NodeID, "host", old.Hostname, "queue", c.QueueID,
			"project", c.Project, "harness", c.Harness, "model", c.Model, "owner", p.Owner, "position", c.Position,
			"reason", c.Reason, "continues", old.SessionID)
		return protocol.WorkerStartAnswer{NodeID: old.NodeID, Hostname: old.Hostname, Project: c.Project, Path: c.Path,
			Harness: c.Harness, Model: c.Model, Queued: true, QueueID: c.QueueID, Position: c.Position, Reason: c.Reason,
			Permissions: old.Worker.Permissions, Track: c.Track, Playbooks: c.Playbooks}, nil
	}
	if err != nil || c.SessionID == "" {
		return none, reject("machine %s answered without a session", old.Hostname)
	}
	g.store.Event("worker.started", old.NodeID, c.SessionID, "",
		strings.TrimPrefix(pairs("project", c.Project, "harness", c.Harness, "model", c.Model, "owner", p.Owner,
			"continues", old.SessionID, "branch", c.Branch), " "))
	g.logLine("worker.started", "node", old.NodeID, "host", old.Hostname, "session", c.SessionID,
		"worker", c.WorkerID, "project", c.Project, "path", c.Path, "harness", c.Harness, "model", c.Model,
		"owner", p.Owner, "from_node", nc.id, "continues", old.SessionID, "worktree", c.Worktree, "branch", c.Branch)
	return protocol.WorkerStartAnswer{SessionID: c.SessionID, NodeID: old.NodeID, Hostname: old.Hostname,
		Project: c.Project, Path: c.Path, Harness: c.Harness, Model: c.Model,
		Worktree: c.Worktree, Branch: c.Branch, ProjectRules: c.ProjectRules, Task: c.Task,
		Permissions: old.Worker.Permissions, Track: c.Track, Playbooks: c.Playbooks}, nil
}

// mostHeadroom picks, among the machines that hold the project, the one with
// the most room for a worker. It returns nothing unless at least two machines
// can run the harness now and every one of them reported its room recently.
// The note says why the machine won.
func (g *Gateway) mostHeadroom(cands []protocol.ProjectView, nodeByID func(string) *protocol.NodeView, harness string) ([]protocol.ProjectView, string) {
	have := g.usage.all()
	now := g.store.now()
	type scored struct {
		view protocol.ProjectView
		load protocol.WorkerLoad
	}
	var ok []scored
	seen := map[string]bool{}
	for _, v := range cands {
		if seen[v.NodeID] {
			continue
		}
		n := nodeByID(v.NodeID)
		if n == nil || !n.Online || n.Launch == nil || !*n.Launch {
			continue
		}
		has := false
		for _, h := range n.Harnesses {
			if h.Name == harness {
				has = true
			}
		}
		if !has {
			continue
		}
		seen[v.NodeID] = true
		e, fresh := have[v.NodeID]
		if !fresh || now.Sub(e.at) > protocol.UsageStaleSeconds*time.Second || e.rep.Workers == nil || e.rep.Workers.Headroom < -0.999 {
			return nil, "" // one machine cannot say: keep the old rule
		}
		ok = append(ok, scored{v, *e.rep.Workers})
	}
	if len(ok) < 2 {
		return nil, ""
	}
	sort.SliceStable(ok, func(i, j int) bool {
		a, b := ok[i].load, ok[j].load
		if (a.Room == protocol.RoomOK) != (b.Room == protocol.RoomOK) {
			return a.Room == protocol.RoomOK
		}
		return a.Headroom > b.Headroom
	})
	best := ok[0]
	var others []string
	for _, o := range ok[1:] {
		others = append(others, fmt.Sprintf("%s %s, %d queued", o.view.Hostname, roomWord(o.load), o.load.Queued))
	}
	note := fmt.Sprintf("picked %s: most room for a worker (%s, %d running, %d queued; %s)",
		best.view.Hostname, roomWord(best.load), best.load.Running, best.load.Queued, strings.Join(others, "; "))
	return []protocol.ProjectView{best.view}, note
}

func roomWord(l protocol.WorkerLoad) string {
	if l.Room == protocol.RoomOK {
		return fmt.Sprintf("room ok, %.0f%% free", max(l.Headroom, 0)*100)
	}
	return "room: " + l.Room
}

// relayQueuedTask passes a queued start that has become a worker to the
// machine of its owner, which sends the task. The sending machine must hold
// the worker, and the worker must name that owner.
func (g *Gateway) relayQueuedTask(ctx context.Context, nc *nodeConn, p protocol.WorkerQueuedStart) error {
	w, ok, err := g.store.Agent(p.SessionID)
	if err != nil {
		return err
	}
	if !ok || w.NodeID != nc.id || w.Worker == nil || w.Worker.Owner != p.Owner {
		return reject("session %q is not a worker of this node with that owner", p.SessionID)
	}
	ownerNode, err := g.store.SessionNode(p.Owner)
	if err != nil {
		return err
	}
	g.mu.Lock()
	target := g.conns[ownerNode]
	g.mu.Unlock()
	if target == nil {
		return reject("the machine of the owner is offline")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := target.call(cctx, "worker.task", p); err != nil {
		return reject("the owner's machine did not send the task: %v", err)
	}
	return nil
}

func (g *Gateway) workerStop(ctx context.Context, nc *nodeConn, p protocol.WorkerStopFrame) (map[string]any, error) {
	req, ok, err := g.store.Agent(p.Requester)
	if err != nil {
		return nil, err
	}
	if !ok || req.NodeID != nc.id {
		return nil, reject("requester session %q does not belong to this node", p.Requester)
	}
	if protocol.IsQueueID(p.Session) {
		return g.cancelQueuedStart(ctx, req, p)
	}
	w, ok, err := g.store.Agent(p.Session)
	if err != nil {
		return nil, err
	}
	if !ok || w.Worker == nil {
		return nil, reject("session %q is not a worker", p.Session)
	}
	// The owner at a keyboard may stop any worker. An agent may stop its own.
	if !protocol.IsOwner(req.Harness) && req.SessionID != w.Worker.Owner {
		return nil, reject("only the worker's owner or the person at the command line can stop it")
	}
	if !w.Online {
		return nil, reject("worker %s has already ended", p.Session)
	}
	g.mu.Lock()
	target := g.conns[w.NodeID]
	g.mu.Unlock()
	if target == nil {
		return nil, reject("machine %s is offline", w.Hostname)
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := target.call(cctx, "worker.kill", protocol.WorkerKill{Session: p.Session, Requester: p.Requester, AsOwner: protocol.IsOwner(req.Harness)}); err != nil {
		// The machine says it does not run this worker. Drop it from the list;
		// its record and the machine's own cleanup decide about a resume.
		if strings.Contains(err.Error(), "no worker with session") {
			g.store.SetSessionOffline(w.NodeID, p.Session)
		}
		return nil, reject("machine %s did not stop the worker: %v", w.Hostname, err)
	}
	return map[string]any{"stopped": true}, nil
}

// cancelQueuedStart asks the machines that accept workers to cancel a queued
// start. The gateway does not know which one holds it: the one that does
// answers, and it checks that the requester is the owner of the start.
func (g *Gateway) cancelQueuedStart(ctx context.Context, req protocol.AgentView, p protocol.WorkerStopFrame) (map[string]any, error) {
	g.mu.Lock()
	var targets []*nodeConn
	for _, c := range g.conns {
		targets = append(targets, c)
	}
	g.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var refusal error
	for _, t := range targets {
		_, err := t.call(cctx, "worker.kill", protocol.WorkerKill{Session: p.Session, Requester: p.Requester, AsOwner: protocol.IsOwner(req.Harness)})
		if err == nil {
			g.store.Event("worker.queue_cancelled", t.id, p.Requester, "", "queue="+p.Session)
			g.logLine("worker.queue_cancelled", "node", t.id, "queue", p.Session, "by", p.Requester)
			return map[string]any{"stopped": true}, nil
		}
		if !strings.Contains(err.Error(), "no worker with session") {
			refusal = err
		}
	}
	if refusal != nil {
		return nil, reject("%v", refusal)
	}
	return nil, reject("no queued start has the ID %q (it may have started: see `agent-net workers`)", p.Session)
}

// resumeEndedWorker starts again the worker whose session this is, when its
// owner sends it a message. An ended worker is offline and still flagged as a
// worker; its record names the owner. Anyone else gets a clear refusal, as for
// an offline session. The machine decides whether its harness can resume.
func (g *Gateway) resumeEndedWorker(ctx context.Context, nc *nodeConn, p protocol.MsgSend) error {
	a, ok, err := g.store.Agent(p.Recipient)
	if err != nil || !ok || a.Worker == nil || a.Online {
		return err
	}
	if a.Worker.Owner != p.Sender {
		return reject("worker %s has ended, and only the session that started it can start it again", p.Recipient)
	}
	g.mu.Lock()
	target := g.conns[a.NodeID]
	g.mu.Unlock()
	if target == nil {
		return reject("machine %s is offline, so worker %s cannot be started again", a.Hostname, p.Recipient)
	}
	cctx, cancel := context.WithTimeout(ctx, launchWait)
	defer cancel()
	if _, err := target.call(cctx, "worker.resume", protocol.WorkerResume{Session: p.Recipient, Requester: p.Sender}); err != nil {
		// The machine does not have a record it can resume. Do not keep showing
		// it as a worker that could start again.
		g.store.SetSessionOffline(a.NodeID, p.Recipient)
		g.logLine("worker.resume_failed", "node", a.NodeID, "host", a.Hostname, "session", p.Recipient, "reason", err)
		return reject("worker %s cannot be started again: %v", p.Recipient, err)
	}
	g.logLine("worker.resumed", "node", a.NodeID, "host", a.Hostname, "session", p.Recipient, "owner", p.Sender)
	return nil
}
