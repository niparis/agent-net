package gateway

import (
	"context"
	"errors"
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
		detail := strings.TrimPrefix(pairs("reason", p.Reason, "harness", p.Harness, "project", p.Project), " ")
		g.store.Event("worker.stopped", nc.id, p.SessionID, "", detail)
		g.logLine("worker.stopped", "node", nc.id, "host", g.store.hostname(nc.id), "session", p.SessionID,
			"worker", p.WorkerID, "reason", p.Reason, "harness", p.Harness, "project", p.Project)
		return nil, nil
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

func (g *Gateway) workerStart(ctx context.Context, nc *nodeConn, p protocol.WorkerStartFrame) (protocol.WorkerStartAnswer, error) {
	var none protocol.WorkerStartAnswer
	p.Project, p.Harness = strings.TrimSpace(p.Project), strings.TrimSpace(p.Harness)
	p.Model, p.Machine = protocol.CleanModel(p.Model), strings.TrimSpace(p.Machine)
	if p.Project == "" {
		return none, reject("a worker needs a project")
	}
	if !protocol.WorkerHarnessSupported(p.Harness) {
		return none, reject("workers can run these harnesses: %s (not %q)", strings.Join(protocol.WorkerHarnesses, ", "), p.Harness)
	}
	if strings.TrimSpace(p.Task) == "" {
		return none, reject("a worker needs a task")
	}
	if !protocol.ValidIsolation(p.Isolation) {
		return none, reject("isolation can be %q or empty, not %q", protocol.IsolationWorktree, p.Isolation)
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

	// Which machine and folder.
	projects, err := g.store.Projects(p.Project)
	if err != nil {
		return none, err
	}
	var cands []protocol.ProjectView
	for _, v := range projects {
		if !strings.EqualFold(v.Name, p.Project) {
			continue
		}
		if p.Machine != "" && !strings.EqualFold(v.Hostname, p.Machine) && v.NodeID != p.Machine {
			continue
		}
		cands = append(cands, v)
	}
	if len(cands) == 0 {
		if p.Machine != "" {
			return none, reject("no project named %q on machine %q (see `agent-net projects`)", p.Project, p.Machine)
		}
		return none, reject("no project named %q is known: register it with `agent-net projects add <path>` (see `agent-net projects`)", p.Project)
	}
	nodes := map[string]bool{}
	for _, v := range cands {
		nodes[v.NodeID] = true
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
		return none, reject("%d folders named %q on %s (%s): register the one to use and unregister the others", len(ps), p.Project, pick.Hostname, strings.Join(ps, ", "))
	}

	// The machine must be there, accept workers, and have the harness and model.
	info, err := g.NodesInfo()
	if err != nil {
		return none, err
	}
	var node *protocol.NodeView
	for i := range info.Nodes {
		if info.Nodes[i].NodeID == pick.NodeID {
			node = &info.Nodes[i]
		}
	}
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
	if p.Model != "" && len(hi.Models) > 0 {
		found := false
		for _, m := range hi.Models {
			found = found || m == p.Model
		}
		if !found {
			return none, reject("model %q is not in the %s model list of %s (%d models; see `agent-net nodes --wide`)", p.Model, p.Harness, node.Hostname, len(hi.Models))
		}
	}

	g.mu.Lock()
	target := g.conns[pick.NodeID]
	g.mu.Unlock()
	if target == nil {
		return none, reject("machine %s is offline", pick.Hostname)
	}
	cctx, cancel := context.WithTimeout(ctx, launchWait)
	defer cancel()
	raw, err := target.call(cctx, "worker.launch", protocol.WorkerLaunch{Project: pick.Name, Path: pick.Path,
		Harness: p.Harness, Model: p.Model, Owner: p.Owner, Isolation: p.Isolation})
	if err != nil {
		// The target daemon's refusal (its limit, its checks) is a rule, not a fault.
		if errors.Is(err, errNodeGone) || errors.Is(err, context.DeadlineExceeded) {
			return none, reject("machine %s did not start the worker: %v", pick.Hostname, err)
		}
		return none, reject("machine %s refused the worker: %v", pick.Hostname, err)
	}
	launched, err := decode[protocol.WorkerLaunched](raw)
	if err != nil || launched.SessionID == "" {
		return none, reject("machine %s answered without a session", pick.Hostname)
	}
	g.store.Event("worker.started", pick.NodeID, launched.SessionID, "",
		strings.TrimPrefix(pairs("project", pick.Name, "harness", p.Harness, "model", p.Model, "owner", p.Owner,
			"isolation", p.Isolation, "branch", launched.Branch), " "))
	g.logLine("worker.started", "node", pick.NodeID, "host", pick.Hostname, "session", launched.SessionID,
		"worker", launched.WorkerID, "project", pick.Name, "path", pick.Path, "harness", p.Harness, "model", p.Model,
		"owner", p.Owner, "from_node", nc.id, "isolation", p.Isolation, "worktree", launched.Worktree, "branch", launched.Branch)
	return protocol.WorkerStartAnswer{SessionID: launched.SessionID, NodeID: pick.NodeID, Hostname: pick.Hostname,
		Project: pick.Name, Path: pick.Path, Harness: p.Harness, Model: p.Model,
		Worktree: launched.Worktree, Branch: launched.Branch}, nil
}

func (g *Gateway) workerStop(ctx context.Context, nc *nodeConn, p protocol.WorkerStopFrame) (map[string]any, error) {
	req, ok, err := g.store.Agent(p.Requester)
	if err != nil {
		return nil, err
	}
	if !ok || req.NodeID != nc.id {
		return nil, reject("requester session %q does not belong to this node", p.Requester)
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
	g.mu.Lock()
	target := g.conns[w.NodeID]
	g.mu.Unlock()
	if target == nil {
		return nil, reject("machine %s is offline", w.Hostname)
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := target.call(cctx, "worker.kill", protocol.WorkerKill{Session: p.Session}); err != nil {
		return nil, reject("machine %s did not stop the worker: %v", w.Hostname, err)
	}
	return map[string]any{"stopped": true}, nil
}
