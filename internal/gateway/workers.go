package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

// workerNotStarted handles a machine's report that a start never became a
// worker. The report records an event for the TUI feed. It makes a notice to
// the owner only when it names a start the gateway handed to this machine
// (Ref), and then the owner, project and harness come from the gateway's
// record, never from the report. A report without a Ref comes from an older
// machine, which tells its owner itself.
func (g *Gateway) workerNotStarted(ctx context.Context, nc *nodeConn, p protocol.WorkerNotStarted) error {
	full := p
	if len(p.Reason) > 300 {
		p.Reason = p.Reason[:300]
	}
	if p.Ref == "" {
		g.store.Event("worker.not_started", nc.id, p.Owner, "",
			strings.TrimPrefix(pairs("project", p.Project, "harness", p.Harness, "reason", p.Reason), " "))
		g.logLine("worker.not_started", "node", nc.id, "host", g.store.hostname(nc.id), "owner", p.Owner,
			"project", p.Project, "harness", p.Harness, "reason", p.Reason)
		_ = g.store.abandonLegacyStart(nc.id, p.Owner, p.Project)
		return nil
	}
	rec, n, noticed, first, err := g.store.declareNotStarted(nc.id, p.Ref, g.store.hostname(nc.id), full.Reason)
	if err != nil {
		var rj Rejection
		if errors.As(err, &rj) {
			return err
		}
		// A storage error is the gateway's fault, not a refusal: the machine
		// may report again, and the start is still open.
		g.logLine("error", "where", "worker.not_started", "err", err)
		return protocol.RetryableError{Err: err}
	}
	if rec.Ref == "" {
		g.logLine("msg.refused", "node", nc.id, "where", "worker.not_started", "ref", p.Ref, "reason", "no such start on this node")
		return reject("no start of this machine has the reference %q", p.Ref)
	}
	if !first {
		return nil
	}
	g.store.Event("worker.not_started", nc.id, rec.Owner, "",
		strings.TrimPrefix(pairs("project", rec.Project, "harness", rec.Harness, "reason", p.Reason), " "))
	g.logLine("worker.not_started", "node", nc.id, "host", g.store.hostname(nc.id), "owner", rec.Owner,
		"project", rec.Project, "harness", rec.Harness, "reason", p.Reason)
	if noticed {
		g.dispatch(ctx, n)
	}
	return nil
}

func (g *Gateway) handleWorker(ctx context.Context, nc *nodeConn, typ string, raw []byte) (any, error) {
	switch typ {
	case "worker.judge.pick":
		p, err := decode[protocol.DifficultyJudgePick](raw)
		if err != nil {
			return nil, reject("bad judge pick request")
		}
		return g.pickDifficultyJudge(nc.id, p)
	case "worker.profile.list", "worker.profile.add", "worker.profile.remove":
		p, err := decode[protocol.WorkerProfileFrame](raw)
		if err != nil {
			return nil, reject("bad worker profile request")
		}
		return g.workerProfileAction(nc.id, typ, p)
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

	case "worker.assessment":
		p, err := decode[protocol.WorkerAssessment](raw)
		if err != nil || p.RunID == "" || p.SessionID == "" {
			return nil, reject("bad worker.assessment")
		}
		a, ok, err := g.store.Agent(p.SessionID)
		if err != nil {
			return nil, err
		}
		if !ok || a.NodeID != nc.id || a.Worker == nil {
			return nil, reject("session %q is not a worker of this node", p.SessionID)
		}
		if err := g.store.RecordWorkerAssessment(p); err != nil {
			return nil, err
		}
		return nil, nil

	case "worker.run_state":
		p, err := decode[protocol.WorkerRunState](raw)
		if err != nil {
			return nil, err
		}
		if p.RunGeneration <= 0 {
			return nil, reject("worker run needs a positive generation")
		}
		if p.Ended {
			return nil, g.store.SetWorkerSessionEnded(nc.id, p.Session, p.RunGeneration)
		}
		return nil, g.store.SetWorkerRunActive(nc.id, p.Session, p.RunGeneration)

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
		if p.RunGeneration != 0 {
			var current int
			if err := g.store.db.QueryRow(`SELECT worker_run_generation FROM sessions WHERE id=? AND node_id=?`, p.SessionID, nc.id).Scan(&current); err != nil {
				return nil, err
			}
			if current != p.RunGeneration {
				g.logLine("worker.stale_end", "node", nc.id, "session", p.SessionID, "generation", p.RunGeneration, "current", current)
				return nil, nil
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
		if p.Reason == protocol.StopModelFails && p.Model != "" {
			var err error
			if protocol.ModelAuthFailure(p.Failure) {
				err = g.store.RecordModelAuthFailure(nc.id, p.Harness, p.Model)
			} else if protocol.WorkerOutcomeDetail(p.Reason, p.Status, p.Complete, p.Failure) != protocol.OutcomeInfra {
				err = g.store.RecordModelFailure(nc.id, p.Harness, p.Model, p.Failure)
			}
			if err != nil {
				return nil, err
			}
		}
		// A run that reports its provider account out of quota blocks that
		// account on this machine until the reset the provider named, so the
		// next start does not fail the same way minutes later.
		if provider, wait, ok := protocol.AccountQuotaBlock(p.Harness, p.Failure); ok {
			until := g.store.now().Add(wait)
			if err := g.store.RecordQuotaBlock(nc.id, provider, until, p.Failure); err != nil {
				return nil, err
			}
			g.logLine("quota.blocked", "node", nc.id, "provider", provider, "until", until.UTC().Format(time.RFC3339), "session", p.SessionID)
		}
		if p.StartedAt == 0 {
			p.StartedAt = started
		}
		end := g.store.now().Unix()
		runSeconds := int64(0)
		if p.StartedAt > 0 && end > p.StartedAt {
			runSeconds = end - p.StartedAt
		}
		caller, err := g.store.WorkerSessionOwner(p.SessionID)
		if err != nil {
			return nil, err
		}
		difficulty := p.DifficultyAsked
		if difficulty == "" {
			difficulty = p.DifficultyUsed
		}
		if difficulty == "" {
			difficulty = "normal"
		}
		run := protocol.WorkerRun{RunGeneration: p.RunGeneration, NodeID: nc.id, SessionID: p.SessionID, Harness: p.Harness, Model: p.Model,
			Complete: p.Complete, Evidence: p.Evidence, Outcome: protocol.WorkerOutcomeDetail(p.Reason, p.Status, p.Complete, p.Failure), StartedAt: p.StartedAt, EndedAt: end,
			RunSeconds: runSeconds, Continue: p.ContinueRun, Fix: p.FixRun, AutoContinue: p.AutoContinueRun, Caller: caller, CallerSessionID: caller, Difficulty: difficulty, DifficultyUsed: p.DifficultyUsed, Touches: p.Touches, TaskID: p.TaskID, Midrun: p.MidrunRun}
		if err := g.store.RecordWorkerRun(run); err != nil {
			return nil, err
		}
		if err := g.store.SetSessionWorkerRework(nc.id, p.SessionID, p.Continue, p.Fix, p.DifficultyAsked, p.DifficultyUsed, p.AutoContinue); err != nil {
			return nil, err
		}
		if p.Status == protocol.StatusDone && p.Model != "" && (p.Evidence == nil || !p.Evidence.EarlyQuit) {
			if err := g.store.RecordModelSuccess(nc.id, p.Harness, p.Model); err != nil {
				return nil, err
			}
		}
		// The worker has ended: it leaves the worker list, and its record stays
		// (offline) only for a follow-up that starts it again.
		if err := g.store.SetWorkerSessionEnded(nc.id, p.SessionID, p.RunGeneration); err != nil {
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
		return nil, g.workerNotStarted(ctx, nc, p)

	case "worker.queued_start":
		p, err := decode[protocol.WorkerQueuedStart](raw)
		if err != nil {
			return nil, reject("bad worker.queued_start")
		}
		// A reference must belong to this machine, this owner and this worker.
		alreadyClosed, err := g.store.verifyStart(nc.id, p.Ref, p.Owner, p.SessionID)
		if err != nil {
			return nil, err
		}
		if alreadyClosed {
			return nil, nil
		}
		if p.Ref != "" {
			won, err := g.store.claimStart(nc.id, p.Ref)
			if err != nil {
				return nil, err
			}
			if !won {
				return nil, reject("start %s is not open", p.Ref)
			}
			claimed := true
			defer func() {
				if claimed {
					_ = g.store.unclaimStart(nc.id, p.Ref)
				}
			}()
			if err := g.relayQueuedTask(ctx, nc, p); err != nil {
				return nil, err
			}
			claimed = false
		} else {
			if err := g.relayQueuedTask(ctx, nc, p); err != nil {
				return nil, err
			}
		}
		// The task was sent: the start is closed, and no one can say it never began.
		if p.Ref != "" {
			if err := g.store.closeStart(nc.id, p.Ref, p.SessionID); err != nil {
				g.logLine("error", "where", "close start", "err", err)
			}
		} else {
			if _, err := g.store.closeLegacyStart(nc.id, p.Owner, p.Project, p.SessionID); err != nil {
				g.logLine("error", "where", "close legacy start", "err", err)
			}
		}
		g.store.Event("worker.started", nc.id, p.SessionID, "", strings.TrimPrefix(pairs("project", p.Project, "harness", p.Harness, "owner", p.Owner, "branch", p.Branch), " "))
		return nil, nil

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
				// A deleted merger session frees its tasks; tell the caller now.
				g.dispatchStepTasks(ctx)
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

func (g *Gateway) workerStart(ctx context.Context, nc *nodeConn, p protocol.WorkerStartFrame, validate ...bool) (protocol.WorkerStartAnswer, error) {
	var none protocol.WorkerStartAnswer
	p.Project, p.Harness = strings.TrimSpace(p.Project), strings.TrimSpace(p.Harness)
	p.Model, p.Machine = protocol.CleanModel(p.Model), strings.TrimSpace(p.Machine)
	selection := protocol.ParseModelSelection(p.Harness, p.Provider, p.Model, p.Effort)
	p.Provider, p.Effort = selection.Provider, selection.Effort
	if selection.Model != "" && selection.Model != "auto" {
		p.Model = selection.LegacyModel()
		if p.Harness == "agy" {
			p.Model = selection.Model
		}
	}
	p.Continue, p.Title = strings.TrimSpace(p.Continue), strings.TrimSpace(p.Title)
	if err := protocol.ValidWorkerTitle(p.Title); err != nil {
		return none, reject("%v", err)
	}
	// Difficulty and touches are still accepted from older callers, but the
	// level comes from the judge. Validate the fields so a malformed old
	// request fails the same way, and ignore their values.
	if p.Difficulty != "" {
		if _, err := protocol.DifficultyFit(0, p.Difficulty, nil); err != nil {
			return none, reject("%v", err)
		}
	}
	for _, touch := range p.Touches {
		switch touch {
		case "db", "hot-path", "ui", "docs", "tests", "config", "other":
		default:
			return none, reject("unknown touches value %q", touch)
		}
	}
	level := protocol.WorkerLevel(p.Assessment)
	if p.Assessment == nil && p.Level != "" {
		if _, err := protocol.DifficultyFit(0, p.Level, nil); err != nil {
			return none, reject("%v", err)
		}
		level = p.Level
	}
	difficultyAsked, difficultyUsed := level, level
	if p.Continue == "" && p.Project == "" {
		return none, reject("a worker needs a project")
	}
	if p.Harness != "" && !protocol.WorkerHarnessSupported(p.Harness) {
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
	if p.FromCTO && (!ok || owner.Harness != protocol.OwnerHarness) {
		return none, reject("CTO worker start requires an owner session")
	}
	if !ok || owner.NodeID != nc.id {
		return none, reject("owner session %q does not belong to this node", p.Owner)
	}
	if owner.Worker != nil {
		return none, reject("a worker cannot start workers")
	}

	if p.Continue != "" {
		return g.workerContinue(ctx, nc, p, validate...)
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
	protected, err := g.store.protectedProviders()
	if err != nil {
		return none, err
	}
	floors, err := g.store.workerFloors()
	if err != nil {
		return none, err
	}
	automatic := p.Model == "" || strings.EqualFold(p.Model, "auto")
	var automaticNote, harnessNote string
	if automatic {
		difficulty := difficultyUsed
		providers, err := g.store.workerProviders()
		if err != nil {
			return none, err
		}
		quotas := g.nodeQuotas(pick.NodeID)
		scores, err := g.store.scores()
		if err != nil {
			return none, err
		}
		minimums, err := g.store.difficultyMinimums()
		if err != nil {
			return none, err
		}
		agents, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true})
		if err != nil {
			return none, err
		}
		running := map[string]int{}
		for _, a := range agents {
			if a.NodeID == pick.NodeID && a.Worker != nil {
				running[protocol.QuotaProviderForStart(a.Harness, a.Model)]++
			}
		}
		profiles, err := g.store.WorkerProfiles()
		if err != nil {
			return none, err
		}
		if len(profiles) == 0 {
			return none, reject("no registered workers qualify for %s; register a profile for %s, or pass model explicitly", difficulty, difficulty)
		} else {
			candidates, skippedProfiles, resolutionNotes, err := g.workerModelCandidates(node, profiles, providers, quotas, p.Harness, p.RequireReadOnly, protected)
			if err != nil {
				return none, err
			}
			var allowedCandidates []protocol.WorkerModelCandidate
			for _, c := range candidates {
				under, floorSentence, noReading := protocol.CheckWorkerFloor(quotas, floors, c.Harness, c.Model)
				if under {
					name := c.Name
					if name == "" {
						name = c.Harness + " " + c.Model
					}
					skippedProfiles = append(skippedProfiles, fmt.Sprintf("skipped %s: %s", name, floorSentence))
					continue
				}
				if noReading {
					key := c.Harness + "\x00" + c.Model
					if existing, ok := resolutionNotes[key]; ok && existing != "" {
						resolutionNotes[key] = existing + "; no quota reading, floor not applied"
					} else {
						resolutionNotes[key] = "no quota reading, floor not applied"
					}
				}
				allowedCandidates = append(allowedCandidates, c)
			}
			candidates = allowedCandidates
			if len(candidates) == 0 {
				reason := fmt.Sprintf("no registered workers are available on an allowed worker provider on %s", node.Hostname)
				if len(skippedProfiles) > 0 {
					reason += "; " + strings.Join(skippedProfiles, "; ")
				}
				return none, reject("%s", reason)
			}
			candidates, familyNote, err := filterWorkerFamilies(candidates, p.NotFamily)
			if err != nil {
				return none, reject("%v", err)
			}
			choice, protectionNote, err := g.chooseAutomaticWorker(candidates, difficulty, scores, minimums, quotas, running, protected, !(len(validate) > 0 && validate[0]))
			if err != nil {
				if familyNote != "" {
					return none, reject("%v; %s", err, familyNote)
				}
				return none, reject("%v", err)
			}
			p.Harness, p.Model = choice.Harness, choice.Model
			automaticNote = "(" + workerChoiceNote(difficulty, choice, candidates, scores, minimums)
			if note := resolutionNotes[p.Harness+"\x00"+p.Model]; note != "" {
				automaticNote += "; " + note
			}
			if familyNote != "" {
				automaticNote += "; " + familyNote
			}
			if protectionNote != "" {
				automaticNote += "; " + protectionNote
			}
			if len(skippedProfiles) > 0 {
				automaticNote += "; " + strings.Join(skippedProfiles, "; ")
			}
			automaticNote += ")"
		}
	} else if p.Harness == "" {
		if p.Model == "" {
			p.Harness = "opencode"
		} else {
			harness, err := g.chooseWorkerHarness(node, p.Model, p.RequireReadOnly)
			if err != nil {
				return none, err
			}
			p.Harness = harness
			state := "sign-in unknown"
			for _, h := range node.Harnesses {
				if h.Name == harness && h.SignedIn == "yes" {
					state = "signed in"
				}
			}
			harnessNote = fmt.Sprintf("selected %s on %s (%s)", harness, node.Hostname, state)
		}
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
	if p.RequireReadOnly && (!automatic || !protocol.WorkerReadOnlySupported(p.Harness) || p.Model == "" || isProtected(p.Harness, p.Model, protected)) {
		return none, reject("no safe automatic read-only worker is available")
	}
	lead := workerDifficultyLead(p.Assessment, difficultyUsed)

	quotas := g.nodeQuotas(pick.NodeID)
	var modelNote string
	if automatic {
		modelNote = lead + " -> " + p.Harness + " " + p.Model + " " + automaticNote
	} else {
		var resolveNote string
		p.Model, resolveNote, err = g.resolveWorkerModel(node, hi, p.Harness, p.Model, "", p.Effort)
		if err != nil {
			return none, err
		}
		modelNote = appendModelNote(appendModelNote(lead, harnessNote), resolveNote)
	}
	if !automatic && len(p.NotFamily) > 0 {
		if _, _, err := filterWorkerFamilies([]protocol.WorkerModelCandidate{{Harness: p.Harness, Model: p.Model}}, p.NotFamily); err != nil {
			return none, reject("%v", err)
		}
	}
	selected := protocol.ParseModelSelection(p.Harness, "", p.Model, p.Effort)
	p.Provider, p.Effort, p.Model = selected.Provider, selected.Effort, selected.LegacyModel()
	if p.Provider == "" {
		p.Provider = protocol.ModelVendor(p.Harness, selected.Model)
	}
	if p.Harness == "agy" {
		if _, buildErr := selected.Arguments(hi.Models); buildErr != nil {
			return none, reject("%v", buildErr)
		}
	}

	if !automatic {
		scores, err := g.store.scores()
		if err != nil {
			return none, err
		}
		minimums, err := g.store.difficultyMinimums()
		if err != nil {
			return none, err
		}
		_, _, eligible, scored := protocol.WorkerCandidateFit(protocol.WorkerModelCandidate{Model: p.Model, DefaultEffort: hi.ModelDefaultEfforts[p.Model]}, difficultyUsed, scores, minimums)
		if scored && !eligible {
			if modelNote != "" {
				modelNote += "; "
			}
			modelNote += "explicit model overrides the max-or-one-level-below eligibility rule"
		}
	}
	if p.Model != "" && protocol.PayPerUseProvider(p.Harness, p.Model, quotas) {
		if modelNote != "" {
			modelNote += "; "
		}
		modelNote += fmt.Sprintf("uses pay-per-use provider (%s)", protocol.ProfileProvider(p.Harness, p.Model))
	}
	if !automatic && isProtected(p.Harness, p.Model, protected) {
		if modelNote != "" {
			modelNote += "; "
		}
		modelNote += fmt.Sprintf("uses protected provider (%s), saved for your orchestrator", protocol.ProfileProvider(p.Harness, p.Model))
	}
	if !automatic {
		if under, floorSentence, _ := protocol.CheckWorkerFloor(quotas, floors, p.Harness, p.Model); under {
			isOwner := protocol.IsOwner(owner.Harness) && !p.FromCTO
			if !isOwner {
				return none, reject("%s", floorSentence)
			}
		}
	}
	// A provider with no allowance left refuses the start now, with the reset
	// time, instead of failing at the first model call (decision 0022).
	if reason := protocol.QuotaRefusal(quotas, p.Harness, p.Model); reason != "" {
		return none, reject("machine %s: %s", node.Hostname, reason)
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
	// A queued start must retain CTO attribution on the target machine.
	if p.FromCTO || p.RequireReadOnly {
		capCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		data, err := target.call(capCtx, "cto.worker_capabilities", nil)
		cancel()
		var caps struct {
			Attribution bool `json:"attribution"`
			ReadOnly    bool `json:"read_only_enforced"`
		}
		if err != nil || json.Unmarshal(data, &caps) != nil || (p.FromCTO && !caps.Attribution) || (p.RequireReadOnly && !caps.ReadOnly) {
			return none, reject("machine %s cannot preserve CTO attribution; upgrade its daemon", pick.Hostname)
		}
	}
	// The owner sets the permission level per project (decision 0020).
	perms, err := g.store.WorkerPermissions(pick.NodeID, pick.Name)
	if err != nil {
		return none, err
	}
	if p.RequireReadOnly {
		if perms == protocol.WorkersNoNetwork {
			return none, reject("read-only phrasing would widen this project's no-network policy")
		}
		perms = protocol.WorkersReadOnly
	}
	if len(validate) > 0 && validate[0] {
		return none, nil
	}
	cctx, cancel := context.WithTimeout(ctx, launchWait)
	defer cancel()
	// The gateway tracks the start before the machine begins it, so a report
	// that arrives at once finds it.
	startRef := protocol.NewID("q")
	if err := g.store.trackWorkerStart(startRef, pick.NodeID, p.Owner, pick.Name, p.Harness, perms); err != nil {
		return none, err
	}
	raw, err := target.call(cctx, "worker.launch", protocol.WorkerLaunch{StartRef: startRef, Project: pick.Name, Path: pick.Path,
		Harness: p.Harness, Model: p.Model, Provider: p.Provider, Effort: p.Effort, Owner: p.Owner, OwnerName: g.store.displayName(p.Owner), Task: p.Task, Title: p.Title, Isolation: p.Isolation, Base: p.Base, Permissions: perms, FromCTO: p.FromCTO, RequireReadOnly: p.RequireReadOnly,
		DifficultyAsked: difficultyAsked, DifficultyUsed: difficultyUsed, Touches: p.Touches, Assessment: p.Assessment, AutoContinue: p.AutoContinue})
	if err != nil {
		// The target daemon's refusal (its limit, its checks) is a rule, not a fault.
		if errors.Is(err, errNodeGone) || errors.Is(err, context.DeadlineExceeded) {
			_ = g.store.markStartUnknown(pick.NodeID, startRef)
			return none, reject("machine %s did not start the worker: %v", pick.Hostname, err)
		}
		g.abandon(pick.NodeID, startRef)
		return none, reject("machine %s refused the worker: %v", pick.Hostname, err)
	}
	launched, err := decode[protocol.WorkerLaunched](raw)
	if err == nil && launched.SessionID != "" {
		if serr := g.store.startSession(pick.NodeID, startRef, launched.SessionID); serr != nil {
			g.logLine("error", "where", "start session", "err", serr)
		}
	}
	if err == nil && launched.SessionID != "" && !launched.Starting && !launched.Queued {
		// A worker that is running when the machine answers has started.
		if cerr := g.store.closeStart(pick.NodeID, startRef, launched.SessionID); cerr != nil {
			g.logLine("error", "where", "close start", "err", cerr)
		}
	}
	if err != nil || (launched.SessionID == "" && !launched.Queued) {
		// Nothing began: the caller is told, and the start ends here.
		g.abandon(pick.NodeID, startRef)
	}
	if err == nil && launched.Queued && launched.QueueID != "" {
		if launched.QueueID != startRef {
			_ = g.store.recordLegacyQueueID(pick.NodeID, startRef, launched.QueueID)
		}
		g.store.Event("worker.queued", pick.NodeID, p.Owner, "",
			strings.TrimPrefix(pairs("project", pick.Name, "harness", p.Harness, "model", p.Model, "owner", p.Owner,
				"queue", launched.QueueID, "position", fmt.Sprint(launched.Position)), " "))
		g.logLine("worker.queued", "node", pick.NodeID, "host", pick.Hostname, "queue", launched.QueueID,
			"project", pick.Name, "harness", p.Harness, "model", p.Model, "owner", p.Owner, "position", launched.Position,
			"reason", launched.Reason)
		return protocol.WorkerStartAnswer{NodeID: pick.NodeID, Hostname: pick.Hostname, Project: pick.Name, Path: pick.Path,
			Harness: p.Harness, Model: p.Model, Provider: p.Provider, Effort: p.Effort, ModelNote: modelNote, Title: p.Title, Queued: true, QueueID: launched.QueueID,
			Position: launched.Position, Reason: launched.Reason, MachineNote: machineNote, Permissions: perms,
			Track: track, Playbooks: books}, nil
	}
	if err != nil || launched.SessionID == "" {
		return none, reject("machine %s answered without a session", pick.Hostname)
	}
	g.recordWorkerPickerNote(p.Assessment, launched.SessionID, modelNote)
	event := "worker.started"
	if launched.Starting {
		event = "worker.starting"
	}
	g.store.Event(event, pick.NodeID, launched.SessionID, "",
		strings.TrimPrefix(pairs("project", pick.Name, "harness", p.Harness, "model", p.Model, "owner", p.Owner,
			"isolation", p.Isolation, "branch", launched.Branch), " "))
	g.logLine(event, "node", pick.NodeID, "host", pick.Hostname, "session", launched.SessionID,
		"worker", launched.WorkerID, "project", pick.Name, "path", pick.Path, "harness", p.Harness, "model", p.Model,
		"owner", p.Owner, "from_node", nc.id, "isolation", p.Isolation, "permissions", perms, "worktree", launched.Worktree, "branch", launched.Branch)
	return protocol.WorkerStartAnswer{StartRef: startRef, Starting: launched.Starting, SessionID: launched.SessionID, NodeID: pick.NodeID, Hostname: pick.Hostname,
		Project: pick.Name, Path: pick.Path, Harness: p.Harness, Model: p.Model, Provider: p.Provider, Effort: p.Effort, ModelNote: modelNote, Title: p.Title,
		Worktree: launched.Worktree, Branch: launched.Branch, ProjectRules: launched.ProjectRules, MachineNote: machineNote, Permissions: perms,
		OSSandbox: launched.OSSandbox, Track: track, Playbooks: books}, nil
}

// recordWorkerPickerNote stores the start's judge result and the picker's
// explanation for the model under the new session. The machine's daemon sends
// the same result later; the run id makes that write a no-op, and the store
// keeps the note when the machine's write lands first.
func (g *Gateway) recordWorkerPickerNote(a *protocol.WorkerAssessment, sessionID, note string) {
	if a == nil || a.RunID == "" || sessionID == "" {
		return
	}
	copied := *a
	copied.SessionID = sessionID
	copied.PickerNote = note
	if err := g.store.RecordWorkerStartAssessment(copied); err != nil {
		g.logLine("error", "where", "worker picker note", "session", sessionID, "err", err)
	}
}

// resolveWorkerModel applies the machine's model inventory and ranking to an explicit name.
func (g *Gateway) resolveWorkerModel(node *protocol.NodeView, hi *protocol.HarnessInfo, harness, model, modelNote string, effortOverride ...string) (string, string, error) {
	profiles, err := g.store.WorkerProfiles()
	if err != nil {
		return "", "", err
	}
	for _, profile := range profiles {
		override := ""
		if len(effortOverride) > 0 {
			override = effortOverride[0]
		}
		askedSelection := protocol.ParseModelSelection(harness, "", model, override)
		if !strings.EqualFold(profile.Name, askedSelection.Model) {
			continue
		}
		if profile.Harness != harness {
			return "", "", reject("worker profile %q requires %s, not %s", profile.Name, profile.Harness, harness)
		}
		effort := askedSelection.Effort
		if effort == "" {
			effort = profile.Effort
		}
		model = protocol.ParseModelSelection(profile.Harness, "", profile.Model, effort).LegacyModel()
		modelNote = appendModelNote(modelNote, fmt.Sprintf("worker profile %q: %s %s", profile.Name, harness, model))
		break
	}
	if harness == "agy" {
		override := ""
		if len(effortOverride) > 0 {
			override = effortOverride[0]
		}
		selected := protocol.ParseModelSelection(harness, "", model, override)
		if selected.Effort != "" {
			var bases []string
			seen := map[string]bool{}
			for _, id := range hi.Models {
				base := protocol.ParseModelSelection(harness, "", id, "").Model
				if !seen[base] {
					bases = append(bases, base)
					seen[base] = true
				}
			}
			resolved, resolveErr := protocol.ResolveModel(selected.Model, bases, nil, nil)
			if resolveErr != nil {
				return "", "", reject("agy model: %v", resolveErr)
			}
			selected.Model = resolved
			if _, buildErr := selected.Arguments(hi.Models); buildErr != nil {
				return "", "", reject("%v", buildErr)
			}
			result := selected.LegacyModel()
			if result != model {
				modelNote = appendModelNote(modelNote, model+" -> "+result)
			}
			return result, modelNote, nil
		}
	}
	quotas := g.nodeQuotas(node.NodeID)
	if model != "" && len(hi.Models) > 0 {
		usage, err := g.store.ModelUsage(node.NodeID, harness, time.Duration(protocol.ModelUsageDays)*24*time.Hour)
		if err != nil {
			return "", "", err
		}
		asked := model
		profileModel, profileEffort := protocol.SplitModelLevel(asked)
		for _, profile := range profiles {
			if profile.Harness != harness || profile.Model != profileModel {
				continue
			}
			resolved, ambiguous := protocol.ResolveWorkerProfileModel(profile.Selection().ModelID(), hi.Models)
			if len(ambiguous) > 0 {
				return "", "", reject("worker profile %q model %s is ambiguous on %s (%s)", profile.Name, profile.Model, node.Hostname, strings.Join(ambiguous, ", "))
			}
			if resolved != "" {
				if resolved != profile.Model {
					modelNote = appendModelNote(modelNote, fmt.Sprintf("%s: %s resolved to %s on %s", profile.Name, profile.Model, resolved, node.Hostname))
				}
				asked = resolved
				if profileEffort != "" {
					asked += ":" + profileEffort
				}
			}
			break
		}
		failedAt, err := g.store.FailedModels(node.NodeID, harness)
		if err != nil {
			return "", "", err
		}
		succeededAt, err := g.store.SucceededModels(node.NodeID, harness)
		if err != nil {
			return "", "", err
		}
		res, err := protocol.ResolveWorkerModelRankedQuota(harness, asked, hi.Models, usage, hi.ModelProviders, failedSet(failedAt), failedSet(succeededAt), quotas)
		if err != nil {
			var nm *protocol.NoModelMatchError
			var pu *protocol.PayPerUseModelError
			switch {
			case errors.As(err, &pu):
				return "", "", reject("%s model %v on %s", harness, err, node.Hostname)
			case errors.As(err, &nm):
				msg := fmt.Sprintf("no %s model matches %q on %s", harness, asked, node.Hostname)
				if len(nm.Closest) > 0 {
					msg += "; closest: " + strings.Join(nm.Closest, ", ")
				}
				return "", "", reject("%s", msg)
			}
			return "", "", reject("model on %s: %v", node.Hostname, err)
		}
		resolved := res.Model
		if resolved != asked || len(res.Demoted) > 0 {
			modelNote = appendModelNote(modelNote, asked+" -> "+resolved)
		}
		if res.QuotaNote != "" {
			if modelNote == "" {
				modelNote = asked + " (" + res.QuotaNote + ")"
			} else {
				modelNote += " (" + res.QuotaNote + ")"
			}
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
		model = resolved
	}
	return model, modelNote, nil
}

// chooseWorkerHarness picks the harness for an explicit model when the caller
// named none. It resolves the model against every harness that reported a list
// and prefers a subscription-backed harness over a pay-per-use one (decision
// 0026, amendment). A bare name that only matches a pay-per-use provider is
// refused with the full id to use.
func (g *Gateway) chooseWorkerHarness(node *protocol.NodeView, model string, requireReadOnly bool) (string, error) {
	type candidate struct {
		harness      string
		signedIn     bool
		subscription bool
		exact        bool
		order        int
	}
	profiles, err := g.store.WorkerProfiles()
	if err != nil {
		return "", err
	}
	profileHarness := ""
	for _, p := range profiles {
		name, _ := protocol.SplitModelLevel(model)
		if strings.EqualFold(p.Name, name) {
			profileHarness = p.Harness
			break
		}
	}
	quotas := g.nodeQuotas(node.NodeID)
	var cands []candidate
	var payErr, opencodeErr, firstErr error
	for i := range node.Harnesses {
		h := &node.Harnesses[i]
		if !protocol.WorkerHarnessSupported(h.Name) || len(h.Models) == 0 || h.SignedIn == "no" || profileHarness != "" && profileHarness != h.Name {
			continue
		}
		if requireReadOnly && !protocol.WorkerReadOnlySupported(h.Name) {
			continue
		}
		resolved, _, err := g.resolveWorkerModel(node, h, h.Name, model, "")
		if err != nil {
			var pe *protocol.PayPerUseModelError
			if errors.As(err, &pe) && payErr == nil {
				payErr = err
			}
			if h.Name == "opencode" {
				opencodeErr = err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		blocked, err := g.store.ModelAuthBlocked(node.NodeID, h.Name, resolved)
		if err != nil {
			return "", err
		}
		if blocked {
			continue
		}
		cands = append(cands, candidate{
			harness:      h.Name,
			signedIn:     h.SignedIn == "yes",
			subscription: protocol.SubscriptionProvider(h.Name, resolved, quotas),
			exact:        protocol.ModelIDWrittenInFull(model, resolved),
			order:        i,
		})
	}
	if len(cands) == 0 {
		switch {
		case payErr != nil:
			return "", payErr
		case opencodeErr != nil:
			return "", opencodeErr
		case firstErr != nil:
			return "", firstErr
		}
		return "", reject("no harness on %s lists %q", node.Hostname, model)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].signedIn != cands[j].signedIn {
			return cands[i].signedIn
		}
		if cands[i].subscription != cands[j].subscription {
			return cands[i].subscription
		}
		if cands[i].exact != cands[j].exact {
			return cands[i].exact
		}
		return cands[i].order < cands[j].order
	})
	return cands[0].harness, nil
}

// workerContinue starts a new worker from an ended one. The gateway knows the
// node of each worker session, so it asks the machine that ran the old worker.
// The machine holds the old task and branch and checks the owner again.
func (g *Gateway) workerContinue(ctx context.Context, nc *nodeConn, p protocol.WorkerStartFrame, validate ...bool) (protocol.WorkerStartAnswer, error) {
	var none protocol.WorkerStartAnswer
	if len(p.NotFamily) > 0 {
		return none, reject("not_family cannot be used with continue; start a new worker for a different family")
	}
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
	// Whether the worker still runs is the machine's answer, not this store's:
	// a hand-back notice tells the owner that a message starts the worker
	// again, and the notice can reach the owner before the machine has
	// reported the end. The machine either waits for an end under way or
	// refuses with "still running". Its record is also the newer one.
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
	if p.Effort != "" || p.Provider != "" {
		harness := p.Harness
		if harness == "" {
			harness = old.Harness
		}
		model := p.Model
		if model == "" {
			model = old.Model
		}
		p.Model = protocol.ParseModelSelection(harness, p.Provider, model, p.Effort).LegacyModel()
	}
	if p.Model != "" {
		harness := p.Harness
		if harness == "" {
			harness = old.Harness
		}
		info, err := g.NodesInfo()
		if err != nil {
			return none, err
		}
		for i := range info.Nodes {
			node := &info.Nodes[i]
			if node.NodeID != old.NodeID {
				continue
			}
			for j := range node.Harnesses {
				hi := &node.Harnesses[j]
				if hi.Name != harness {
					continue
				}
				p.Model, _, err = g.resolveWorkerModel(node, hi, harness, p.Model, "")
				if err != nil {
					return none, err
				}
				break
			}
			break
		}
	}
	if len(validate) > 0 && validate[0] {
		return none, nil
	}
	cctx, cancel := context.WithTimeout(ctx, launchWait)
	defer cancel()
	taskID, err := g.store.workerTaskID(old.SessionID)
	if err != nil {
		return none, err
	}
	startRef := protocol.NewID("q")
	if err := g.store.trackWorkerStart(startRef, old.NodeID, p.Owner, old.Worker.Project, p.Harness, old.Worker.Permissions); err != nil {
		return none, err
	}
	raw, err := target.call(cctx, "worker.continue", protocol.WorkerContinue{StartRef: startRef, Session: old.SessionID, Requester: p.Owner, FromCTO: p.FromCTO, TaskID: taskID,
		Harness: p.Harness, Model: p.Model, Task: strings.TrimSpace(p.Task), Title: p.Title, Touches: p.Touches,
		Assessment: old.Worker.Assessment})
	if err != nil {
		if errors.Is(err, errNodeGone) || errors.Is(err, context.DeadlineExceeded) {
			_ = g.store.markStartUnknown(old.NodeID, startRef)
			return none, reject("machine %s did not start the worker: %v", old.Hostname, err)
		}
		g.abandon(old.NodeID, startRef)
		return none, reject("machine %s refused the worker: %v", old.Hostname, err)
	}
	c, err := decode[protocol.WorkerContinued](raw)
	if err == nil && c.SessionID != "" {
		if serr := g.store.startSession(old.NodeID, startRef, c.SessionID); serr != nil {
			g.logLine("error", "where", "start session", "err", serr)
		}
	}
	if err == nil && c.SessionID != "" && !c.Starting && !c.Queued {
		if cerr := g.store.closeStart(old.NodeID, startRef, c.SessionID); cerr != nil {
			g.logLine("error", "where", "close start", "err", cerr)
		}
	}
	if err != nil || (c.SessionID == "" && !c.Queued) {
		g.abandon(old.NodeID, startRef)
	}
	var modelNote string
	if err == nil {
		if track, books, nerr := g.choiceNotes(old.NodeID, old.Hostname, c.Project, c.Harness, c.Model); nerr == nil {
			c.Track, c.Playbooks = track, books
		}
		modelNote = workerDifficultyLead(c.Assessment, protocol.WorkerLevel(c.Assessment))
		if c.Model != "" && protocol.PayPerUseProvider(c.Harness, c.Model, g.nodeQuotas(old.NodeID)) {
			modelNote = appendModelNote(modelNote, fmt.Sprintf("uses pay-per-use provider (%s)", protocol.ProfileProvider(c.Harness, c.Model)))
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
			Harness: c.Harness, Model: c.Model, ModelNote: modelNote, Title: c.Title, Queued: true, QueueID: c.QueueID, Position: c.Position, Reason: c.Reason,
			Permissions: old.Worker.Permissions, Track: c.Track, Playbooks: c.Playbooks}, nil
	}
	if err != nil || c.SessionID == "" {
		return none, reject("machine %s answered without a session", old.Hostname)
	}
	g.recordWorkerPickerNote(c.Assessment, c.SessionID, modelNote)
	event := "worker.started"
	if c.Starting {
		event = "worker.starting"
	}
	g.store.Event(event, old.NodeID, c.SessionID, "",
		strings.TrimPrefix(pairs("project", c.Project, "harness", c.Harness, "model", c.Model, "owner", p.Owner,
			"continues", old.SessionID, "branch", c.Branch), " "))
	g.logLine(event, "node", old.NodeID, "host", old.Hostname, "session", c.SessionID,
		"worker", c.WorkerID, "project", c.Project, "path", c.Path, "harness", c.Harness, "model", c.Model,
		"owner", p.Owner, "from_node", nc.id, "continues", old.SessionID, "worktree", c.Worktree, "branch", c.Branch)
	return protocol.WorkerStartAnswer{StartRef: startRef, Starting: c.Starting, SessionID: c.SessionID, NodeID: old.NodeID, Hostname: old.Hostname,
		Project: c.Project, Path: c.Path, Harness: c.Harness, Model: c.Model, ModelNote: modelNote, Title: c.Title,
		Worktree: c.Worktree, Branch: c.Branch, ProjectRules: c.ProjectRules, Task: c.Task,
		Permissions: old.Worker.Permissions, OSSandbox: c.OSSandbox, Track: c.Track, Playbooks: c.Playbooks}, nil
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

// defaultRelayWait is how long the gateway waits for the owner's machine to
// send a task. It matches the 2 minutes the starting machine waits for the whole
// call, so a slow owner does not stop a running worker.
const defaultRelayWait = 2 * time.Minute

// relayDeadline is the relay wait, overridden in tests.
func (g *Gateway) relayDeadline() time.Duration {
	if g.relayWait > 0 {
		return g.relayWait
	}
	return defaultRelayWait
}

// abandon ends a start that will never be a worker and logs a storage error.
func (g *Gateway) abandon(nodeID, ref string) {
	if err := g.store.abandonStart(nodeID, ref); err != nil {
		g.logLine("error", "where", "abandon start", "err", err)
	}
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
	// Older worker daemons did not carry a start reference. Give this relay
	// its own tracked reference before asking the owner's daemon to send it.
	syntheticRef := p.Ref == ""
	if syntheticRef {
		p.Ref = protocol.NewID("q")
	}
	if err := g.store.trackWorkerStart(p.Ref, nc.id, p.Owner, p.Project, p.Harness, p.Permissions); err != nil {
		return err
	}
	if syntheticRef {
		// The old host does not know this reference, so it cannot close it on
		// failure. Abandon it unless the relay closes it successfully.
		defer g.abandon(nc.id, p.Ref)
	}
	if err := g.store.startSession(nc.id, p.Ref, p.SessionID); err != nil {
		return err
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
	cctx, cancel := context.WithTimeout(ctx, g.relayDeadline())
	defer cancel()
	if _, err := target.call(cctx, "worker.task", p); err != nil {
		return reject("the owner's machine did not send the task: %v", err)
	}
	if syntheticRef {
		return g.store.closeStart(nc.id, p.Ref, p.SessionID)
	}
	return nil
}

func (g *Gateway) workerStop(ctx context.Context, nc *nodeConn, p protocol.WorkerStopFrame, validate ...bool) (map[string]any, error) {
	req, ok, err := g.store.Agent(p.Requester)
	if err != nil {
		return nil, err
	}
	if !ok || req.NodeID != nc.id {
		return nil, reject("requester session %q does not belong to this node", p.Requester)
	}
	if protocol.IsQueueID(p.Session) {
		return g.cancelQueuedStart(ctx, req, p, validate...)
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
	if len(validate) > 0 && validate[0] {
		return nil, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := target.call(cctx, "worker.kill", protocol.WorkerKill{Session: p.Session, Requester: p.Requester, AsOwner: protocol.IsOwner(req.Harness)}); err != nil {
		// The machine says it does not run this worker. Drop it from the list;
		// its record and the machine's own cleanup decide about a resume.
		if strings.Contains(err.Error(), "no worker with session") {
			g.store.SetWorkerSessionEnded(w.NodeID, p.Session)
		}
		return nil, reject("machine %s did not stop the worker: %v", w.Hostname, err)
	}
	return map[string]any{"stopped": true}, nil
}

// cancelQueuedStart asks the machines that accept workers to cancel a queued
// start. The gateway does not know which one holds it: the one that does
// answers, and it checks that the requester is the owner of the start.
func (g *Gateway) cancelQueuedStart(ctx context.Context, req protocol.AgentView, p protocol.WorkerStopFrame, validate ...bool) (map[string]any, error) {
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
		typ := "worker.kill"
		if len(validate) > 0 && validate[0] {
			typ = "worker.validate_stop"
		}
		_, err := t.call(cctx, typ, protocol.WorkerKill{Session: p.Session, Requester: p.Requester, AsOwner: protocol.IsOwner(req.Harness)})
		if err == nil {
			if len(validate) > 0 && validate[0] {
				return nil, nil
			}
			g.abandon(t.id, p.Session)
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
	if err != nil || !ok || a.Worker == nil {
		return err
	}
	if a.Online && a.Worker.Owner != p.Sender {
		return nil
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
	// Keep the activation for older daemons, which cannot announce a run.
	// New daemons fence the old run before registering the resumed harness.
	if err := g.store.SetWorkerRunActive(a.NodeID, p.Recipient); err != nil {
		return err
	}
	if _, err := target.call(cctx, "worker.resume", protocol.WorkerResume{Session: p.Recipient, Requester: p.Sender}); err != nil {
		g.store.SetWorkerSessionEnded(a.NodeID, p.Recipient, a.Worker.RunGeneration)
		// The machine does not have a record it can resume. Do not keep showing
		// it as a worker that could start again.
		g.logLine("worker.resume_failed", "node", a.NodeID, "host", a.Hostname, "session", p.Recipient, "reason", err)
		return reject("worker %s cannot be started again: %v", p.Recipient, err)
	}
	g.logLine("worker.resumed", "node", a.NodeID, "host", a.Hostname, "session", p.Recipient, "owner", p.Sender)
	return nil
}

// notifyResumeRefusal reports a failed restart even when the original send
// was queued while its sender was disconnected from the gateway.
func (g *Gateway) notifyResumeRefusal(ctx context.Context, p protocol.MsgSend, reason error) {
	a, ok, err := g.store.Agent(p.Recipient)
	if err != nil || !ok || a.Worker == nil || a.Worker.Owner != p.Sender {
		return
	}
	body := fmt.Sprintf("[agent-net] Your message could not be delivered because %v. Retry the message or start a new worker.\n\nMessage: ", reason)
	remaining := protocol.MaxBodyBytes - len(body)
	if remaining < 0 {
		return
	}
	if len(p.Body) <= remaining {
		body += p.Body
	} else {
		body += strings.ToValidUTF8(p.Body[:remaining], "")
	}
	key := fmt.Sprintf("resume-refusal-%x", sha256.Sum256([]byte(p.Sender+"\x00"+p.IdempotencyKey)))
	m, created, err := g.store.StoreMessage(protocol.MsgSend{Sender: p.Recipient, Recipient: p.Sender, Type: protocol.TypeNotification, Body: body, IdempotencyKey: key})
	if err != nil {
		g.logLine("worker.resume_notice_failed", "session", p.Recipient, "reason", err)
		return
	}
	if created {
		g.dispatch(ctx, m)
	}
}

// workerDifficultyLead begins every start note with the judge result that set
// the level. A failed judge says why the start used normal.
func workerDifficultyLead(a *protocol.WorkerAssessment, level string) string {
	if a != nil && a.Error != "" {
		return "difficulty: no assessment (" + a.Error + "); started at " + level
	}
	if a != nil && a.Label != "" && protocol.WorkerLevel(a) == a.Label {
		return "difficulty " + a.DifficultyLine()
	}
	if a != nil && a.Label != "" {
		return "difficulty: no assessment (the judge confidence is low); started at " + level
	}
	return "difficulty: no assessment (no judge result); started at " + level
}

func appendModelNote(note, more string) string {
	switch {
	case more == "":
		return note
	case note == "":
		return more
	}
	return note + "; " + more
}

// workerChoiceNote leaves the parenthesis open for resolution and protection notes.
func workerChoiceNote(level string, choice protocol.WorkerModelChoice, candidates []protocol.WorkerModelCandidate, scores []protocol.ModelScore, minimums map[string]float64) string {
	note := fmt.Sprintf("%g, good enough", choice.Score)
	if choice.QuotaNote != "" {
		note += "; " + choice.QuotaNote
	} else {
		note += "; no quota pace reading"
	}
	if choice.Weight > 0 {
		note += fmt.Sprintf("; rotation weight %.2f", choice.Weight)
	}
	if choice.ShareNote != "" {
		note += "; " + choice.ShareNote
	}
	for _, c := range candidates {
		n := c.Track.Complete + c.Track.Partial + c.Track.Failed
		if c.Model != choice.Model && n >= 5 && 2*c.Track.Complete < n {
			note += "; " + c.Model + " has a poor track record"
			break
		}
	}
	for _, c := range candidates {
		score, fit, eligible, ok := protocol.WorkerCandidateFit(c, level, scores, minimums)
		if ok && eligible && score > choice.Score {
			name := c.Name
			if name == "" {
				name = c.Model
			}
			note += "; " + name + " kept for harder work"
		}
		if !ok || eligible || fit != "good" && fit != "excellent" {
			continue
		}
		name := c.Name
		if name == "" {
			name = c.Harness + " " + c.Model
		}
		note += "; " + name + " excluded: its max is " + protocol.WorkerMaximumLevel(score, minimums) + " (automatic picks may be at most one level below their max)"
	}
	return note
}

// workerModelCandidates builds the roster shared by starts and read-only previews.
func (g *Gateway) workerModelCandidates(node *protocol.NodeView, profiles []protocol.WorkerProfile, providers protocol.WorkerProviders, quotas *protocol.Quotas, harness string, readOnly bool, protected protocol.ProtectedProviders) ([]protocol.WorkerModelCandidate, []string, map[string]string, error) {
	tracks, err := g.store.TrackRecords(node.NodeID, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	earlyLevels, err := g.store.recentEarlyQuitLevels(node.NodeID)
	if err != nil {
		return nil, nil, nil, err
	}
	trackByModel := make(map[string]protocol.TrackRecord, len(tracks))
	for _, track := range tracks {
		trackByModel[track.Harness+"\x00"+track.Model] = track
	}
	var candidates []protocol.WorkerModelCandidate
	var skippedProfiles []string
	resolutionNotes := map[string]string{}
	for _, h := range node.Harnesses {
		if h.SignedIn == "no" {
			skippedProfiles = append(skippedProfiles, fmt.Sprintf("%s skipped on %s: not signed in", h.Name, node.Hostname))
			continue
		}
		if !protocol.WorkerHarnessSupported(h.Name) || harness != "" && harness != h.Name {
			continue
		}
		if readOnly && !protocol.WorkerReadOnlySupported(h.Name) {
			continue
		}
		failures, err := g.store.RecentWorkerFailures(node.NodeID, h.Name)
		if err != nil {
			return nil, nil, nil, err
		}
		successes, err := g.store.SucceededModels(node.NodeID, h.Name)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, profile := range profiles {
			if profile.Harness != h.Name {
				continue
			}
			resolved, ambiguous := protocol.ResolveWorkerProfileModel(profile.Selection().ModelID(), h.Models)
			if resolved == "" {
				message := fmt.Sprintf("stale profile %q skipped: %s model %s is not listed by %s", profile.Name, h.Name, profile.Model, node.Hostname)
				if len(ambiguous) > 0 {
					message = fmt.Sprintf("stale profile %q skipped: %s model %s is ambiguous on %s (%s)", profile.Name, h.Name, profile.Model, node.Hostname, strings.Join(ambiguous, ", "))
				} else if closest := protocol.ClosestModels(profile.Model, h.Models, 3); len(closest) > 0 {
					message += "; closest: " + strings.Join(closest, ", ")
				}
				skippedProfiles = append(skippedProfiles, message)
				continue
			}
			blocked, err := g.store.ModelAuthBlocked(node.NodeID, h.Name, resolved)
			if err != nil {
				return nil, nil, nil, err
			}
			if blocked {
				skippedProfiles = append(skippedProfiles, fmt.Sprintf("%s skipped on %s: provider authentication failed", profile.Name, node.Hostname))
				continue
			}
			if !protocol.WorkerProviderAllowed(h.Name, resolved, providers.Providers, quotas) {
				continue
			}
			if readOnly && isProtected(h.Name, resolved, protected) {
				continue
			}
			model := resolved
			model = protocol.ParseModelSelection(profile.Harness, "", model, profile.Effort).LegacyModel()
			candidates = append(candidates, protocol.WorkerModelCandidate{Name: profile.Name, Harness: h.Name, Model: model, DefaultEffort: h.ModelDefaultEfforts[resolved], EarlyQuitLevels: earlyLevels[h.Name+"\x00"+model], Failed: failures[model], Succeeded: !successes[model].IsZero(), Track: trackByModel[h.Name+"\x00"+model]})
			if resolved != profile.Model {
				resolutionNotes[h.Name+"\x00"+model] = fmt.Sprintf("%s: %s resolved to %s on %s", profile.Name, profile.Model, resolved, node.Hostname)
			}
		}
	}

	return candidates, skippedProfiles, resolutionNotes, nil
}

// chooseAutomaticWorker applies the same weights and rotation for a start or a preview.
func (g *Gateway) chooseAutomaticWorker(candidates []protocol.WorkerModelCandidate, difficulty string, scores []protocol.ModelScore, minimums map[string]float64, quotas *protocol.Quotas, running map[string]int, protected protocol.ProtectedProviders, persist bool) (protocol.WorkerModelChoice, string, error) {
	if err := g.store.applyQualityWeights(candidates, difficulty); err != nil {
		return protocol.WorkerModelChoice{}, "", err
	}
	g.rotationMu.Lock()
	defer g.rotationMu.Unlock()
	rotation, err := g.store.WorkerRotation()
	if err != nil {
		return protocol.WorkerModelChoice{}, "", err
	}
	var healthy []protocol.WorkerModelCandidate
	var cooling []string
	for _, c := range candidates {
		h, err := g.store.ModelHealth(c.Harness, c.Model)
		if err != nil {
			return protocol.WorkerModelChoice{}, "", err
		}
		if h.Cooling {
			cooling = append(cooling, fmt.Sprintf("skipped %s %s: cooling down after %d infrastructure failures (until %s UTC)", c.Harness, c.Model, h.Failures, h.Until.UTC().Format("15:04")))
		} else {
			healthy = append(healthy, c)
		}
	}
	var choice protocol.WorkerModelChoice
	var note string
	if len(healthy) > 0 && len(cooling) > 0 {
		choice, note, err = chooseProtectedWorker(healthy, difficulty, scores, minimums, quotas, running, &rotation, protected)
	} else if len(cooling) > 0 {
		err = fmt.Errorf("no healthy candidates")
	}
	if len(cooling) == 0 || err != nil {
		choice, note, err = chooseProtectedWorker(candidates, difficulty, scores, minimums, quotas, running, &rotation, protected)
		if err == nil && len(cooling) > 0 {
			note = appendModelNote(note, "used model cooling down because no healthy alternative is adequate")
		}
	} else {
		note = appendModelNote(note, strings.Join(cooling, "; "))
	}
	if err == nil && persist {
		err = g.store.saveWorkerRotation(rotation)
	}
	return choice, note, err
}

// filterWorkerFamilies removes excluded makers before scoring and rotation.
func filterWorkerFamilies(candidates []protocol.WorkerModelCandidate, excluded []string) ([]protocol.WorkerModelCandidate, string, error) {
	if len(excluded) == 0 {
		return candidates, "", nil
	}
	deny := map[string]bool{}
	for _, family := range excluded {
		if f := strings.ToLower(strings.TrimSpace(family)); f != "" {
			deny[f] = true
		}
	}
	var kept []protocol.WorkerModelCandidate
	var skipped []string
	families := map[string]bool{}
	for _, c := range candidates {
		family := protocol.ModelFamily(c.Harness, c.Model)
		if family == "" {
			family = "unknown"
		}
		families[family] = true
		if deny[family] {
			skipped = append(skipped, c.Harness+" "+c.Model+" ("+family+")")
		} else {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 && len(candidates) > 0 {
		var denied, available []string
		for f := range deny {
			denied = append(denied, f)
		}
		for f := range families {
			available = append(available, f)
		}
		sort.Strings(denied)
		sort.Strings(available)
		return nil, "", fmt.Errorf("no models remain after excluding families %s; available families: %s", strings.Join(denied, ", "), strings.Join(available, ", "))
	}
	if len(skipped) == 0 {
		return kept, "", nil
	}
	return kept, "skipped for family rule: " + strings.Join(skipped, ", "), nil
}
