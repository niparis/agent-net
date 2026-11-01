package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// sessionStart starts an owner session on this machine, or relays the request
// to the machine that holds the project. See docs/design/start-sessions.md,
// build plan part 8.
func (d *Daemon) sessionStart(ctx context.Context, p protocol.SessionStartRequest) (protocol.StartedSession, error) {
	p.Project = strings.TrimSpace(p.Project)
	p.Path = strings.TrimSpace(p.Path)
	p.NodeID = strings.TrimSpace(p.NodeID)
	if p.Project == "" && p.Path == "" {
		return protocol.StartedSession{}, errors.New("session_start needs project or path")
	}
	if p.Harness == "" {
		p.Harness = "claude"
	}
	if p.Harness != "claude" && p.Harness != "opencode" && p.Harness != "codex" && p.Harness != "pi" {
		return protocol.StartedSession{}, errors.New("session_start supports only Claude, OpenCode, Codex and Pi sessions")
	}
	if len(p.FirstMessage) > protocol.MaxBodyBytes {
		return protocol.StartedSession{}, errors.New("first_message is too long")
	}
	target, err := d.sessionStartMachine(ctx, p)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	if target != "" {
		return d.relaySessionStart(ctx, p, target)
	}
	return d.sessionStartLocal(ctx, p)
}

// agentView reads one session from the gateway.
func (d *Daemon) agentView(ctx context.Context, id string) (protocol.AgentView, error) {
	raw, err := d.call(ctx, "query.agent", protocol.QueryAgent{SessionID: id})
	if err != nil {
		return protocol.AgentView{}, err
	}
	var result struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.AgentView{}, err
	}
	return result.Agent, nil
}

// sessionStartMachine picks the machine that runs the start. Empty means this
// machine. An explicit NodeID is trusted; the target checks the project.
func (d *Daemon) sessionStartMachine(ctx context.Context, p protocol.SessionStartRequest) (string, error) {
	if p.NodeID != "" && p.NodeID != d.cfg.NodeID && !strings.EqualFold(p.NodeID, d.cfg.Hostname) {
		return p.NodeID, nil
	}
	if p.Path != "" {
		// A typed path belongs to this machine unless the request names
		// another one with NodeID.
		return "", nil
	}
	raw, err := d.call(ctx, "query.projects", protocol.QueryProjects{})
	if err != nil {
		return "", err
	}
	var projects protocol.ProjectsResult
	if err := json.Unmarshal(raw, &projects); err != nil {
		return "", err
	}
	var local bool
	remote := map[string]bool{}
	for _, project := range projects.Projects {
		hit := project.Name == p.Project || project.Path == p.Project
		if !hit {
			continue
		}
		if project.NodeID == d.cfg.NodeID {
			local = true
			continue
		}
		remote[project.NodeID] = true
	}
	if local || p.NodeID != "" {
		return "", nil
	}
	switch len(remote) {
	case 0:
		return "", errors.New("project is not known on this machine or another one")
	case 1:
		for node := range remote {
			return node, nil
		}
	}
	return "", errors.New("project is on more than one machine: use name@machine")
}

// sessionStartLocal starts the session on this machine.
func (d *Daemon) sessionStartLocal(ctx context.Context, p protocol.SessionStartRequest) (protocol.StartedSession, error) {
	bin, err := d.harnessBinary(p.Harness)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	var found *protocol.ProjectView
	// typed is the checked folder of a path start, empty for a project start.
	// created says agent-net made that folder on this request.
	typed, created := "", false
	if p.Path != "" {
		path, err := cleanOwnerPath(p.Path)
		if err != nil {
			return protocol.StartedSession{}, err
		}
		info, statErr := os.Stat(path)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			if !p.Create {
				return protocol.StartedSession{ID: protocol.NewID("start"), Status: "needs_create",
					NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: p.Harness,
					Project: filepath.Base(path), Path: path, Model: p.Model, StartedAt: time.Now().Unix()}, nil
			}
			if err := createOwnerFolder(ctx, path); err != nil {
				return protocol.StartedSession{}, err
			}
			created = true
		case statErr != nil:
			return protocol.StartedSession{}, statErr
		case !info.IsDir():
			return protocol.StartedSession{}, errors.New("the path is not a folder")
		}
		projectPath, projectName, err := d.ownerPathProject(ctx, path, p.Harness, created)
		if err != nil {
			return protocol.StartedSession{}, err
		}
		typed = projectPath
		found = &protocol.ProjectView{Name: projectName, Path: projectPath}
	} else {
		raw, err := d.call(ctx, "query.projects", protocol.QueryProjects{})
		if err != nil {
			return protocol.StartedSession{}, err
		}
		var projects protocol.ProjectsResult
		if err = json.Unmarshal(raw, &projects); err != nil {
			return protocol.StartedSession{}, err
		}
		for _, project := range projects.Projects {
			if project.NodeID == d.cfg.NodeID && (project.Name == p.Project || project.Path == p.Project) {
				if found != nil {
					return protocol.StartedSession{}, errors.New("project is ambiguous: use its local path")
				}
				copy := project
				found = &copy
			}
		}
		if found == nil {
			return protocol.StartedSession{}, errors.New("project is not known on this machine")
		}
	}
	info, err := os.Stat(found.Path)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	if !info.IsDir() {
		return protocol.StartedSession{}, errors.New("project path is not a folder")
	}
	sessionPath, branch := found.Path, ""
	if p.Folder == protocol.FolderWorktree {
		if p.CTOProposal {
			if err := checkOwnerHome(ownerWorktreeRoot(found.Path)); err != nil {
				return protocol.StartedSession{}, err
			}
		}
		dir, name, _, err := d.createOwnerWorktreeChecked(ctx, found.Path, found.Name, p.Branch, p.CTOProposal)
		if err != nil {
			return protocol.StartedSession{}, err
		}
		sessionPath, branch = dir, name
	} else {
		branch, _ = gitOut(ctx, found.Path, "branch", "--show-current")
	}
	if typed != "" && p.Harness == "claude" {
		needTrust, err := ownerClaudeTrust(sessionPath, created, p.Folder == protocol.FolderWorktree, p.TrustClaude)
		if err != nil {
			return protocol.StartedSession{}, err
		}
		if needTrust {
			return protocol.StartedSession{ID: protocol.NewID("start"), Status: "needs_trust",
				NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: p.Harness,
				Project: found.Name, Path: sessionPath, Branch: branch, Model: p.Model,
				StartedAt: time.Now().Unix()}, nil
		}
	}
	r := protocol.StartedSession{ID: protocol.NewID("start"), Status: "starting", NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: p.Harness, Project: found.Name, Path: sessionPath, Branch: branch, Model: p.Model, StartedAt: time.Now().Unix()}
	remoteOwner, _ := ctx.Value(proposalRemoteOwnerKey{}).(bool)
	notify := p.CTOProposal && !remoteOwner
	if p.Harness == "codex" {
		args := []string{bin}
		if p.Model != "" {
			args = append(args, "-m", p.Model)
		}
		if p.FirstMessage != "" {
			args = append(args, p.FirstMessage)
		}
		r.Status = "started"
		r.EnterPlan = &protocol.EnterPlan{Argv: args, Dir: r.Path, Host: d.hostName(), Note: "Press ctrl+] to come back here; Codex keeps running."}
		if home, def := d.cfg.CodexHome, defaultCodexHome(); home != "" && home != def {
			r.EnterPlan.Env = map[string]string{"CODEX_HOME": home}
		}
		plan := d.openInHerdr("start:"+r.ID, r.Project, p.HerdrSocket, p.HerdrPaneID, *r.EnterPlan)
		r.EnterPlan = &plan
		if notify {
			d.notifySessionStart(r)
		}
		return r, nil
	}
	if p.Harness == "opencode" {
		return d.launchOwnerOpenCode(ctx, r, p.FirstMessage, p.HerdrSocket, p.HerdrPaneID, notify)
	}
	if p.Harness == "pi" {
		return d.launchOwnerPi(r, p.FirstMessage, "", p.HerdrSocket, p.HerdrPaneID, notify)
	}
	args := []string{"--bg", "--name", r.Project}
	if p.Model != "" {
		args = append(args, "--model", p.Model)
	}
	if p.FirstMessage != "" {
		args = append(args, "--", p.FirstMessage)
	}
	return d.launchOwnerClaude(r, bin, args, "", p.HerdrSocket, p.HerdrPaneID, notify), nil
}

// relaySessionStart asks the gateway to start the session on another machine.
func (d *Daemon) relaySessionStart(ctx context.Context, p protocol.SessionStartRequest, target string) (protocol.StartedSession, error) {
	owner := d.ownerSessionID()
	if owner == "" {
		return protocol.StartedSession{}, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	socket, pane := p.HerdrSocket, p.HerdrPaneID
	p.HerdrSocket, p.HerdrPaneID = "", ""
	frame := protocol.SessionStartRelay{Owner: owner, NodeID: target, SessionStartRequest: p}
	typ := protocol.FrameSessionStart
	if p.CTOProposal {
		typ = protocol.FrameSessionProposalStart
	}
	data, err := d.call(ctx, typ, frame)
	if err != nil {
		return protocol.StartedSession{}, tooOldGateway(err)
	}
	var res protocol.StartedSession
	if err := json.Unmarshal(data, &res); err != nil {
		return protocol.StartedSession{}, err
	}
	d.rememberRelayedStart(res.ID, firstNonEmpty(res.NodeID, target), socket, pane)
	res = d.remoteStartResult(ctx, res, socket, pane)
	remoteOwner, _ := ctx.Value(proposalRemoteOwnerKey{}).(bool)
	if p.CTOProposal && !remoteOwner && res.Status == "starting" {
		d.watchApprovedSessionStart(res.ID)
	} else if p.CTOProposal && !remoteOwner && res.Status == "started" {
		d.notifySessionStart(res)
	}
	return res, nil
}

// An approved remote start must be opened even when no form is polling it.
func (d *Daemon) watchApprovedSessionStart(id string) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(d.ctoCtx, d.cfg.Workers.filled().StartTimeout)
		defer cancel()
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				result, err := d.sessionStartGet(ctx, id)
				if err != nil || result.Status != "starting" {
					if err == nil && result.Status == "started" {
						d.notifySessionStart(result)
					}
					return
				}
			}
		}
	}()
}

// remoteStartResult fills the fields that only the owner's machine can know:
// the other machine's host name, the ssh note, and a remote Codex plan.
func (d *Daemon) remoteStartResult(ctx context.Context, res protocol.StartedSession, socket, pane string) protocol.StartedSession {
	if res.NodeID == "" || res.NodeID == d.cfg.NodeID {
		return res
	}
	res.Hostname = firstNonEmpty(res.Hostname, res.NodeID)
	if res.EnterPlan != nil {
		remote := *res.EnterPlan
		if remote.OpenedHerdr && herdrIDRe.MatchString(remote.HerdrPane) {
			remote.Argv = []string{"herdr", "agent", "attach", remote.HerdrPane}
			remote.OpenedHerdr = false
		}
		plan := d.wrapRemoteEnter(res.Hostname, remote)
		key := "start:" + res.ID
		if res.SessionID != "" {
			key = "remote:" + res.SessionID
		}
		plan = d.openInHerdr(key, res.Project, socket, pane, plan)
		res.EnterPlan = &plan
	} else if res.Status == "started" && res.SessionID != "" && d.herdrBinary("") != "" {
		if plan, err := d.enterPlan(ctx, protocol.EnterRequest{SessionID: res.SessionID, HerdrSocket: socket, HerdrPaneID: pane}); err == nil && plan.OpenedHerdr {
			res.EnterPlan = &plan
		}
	}
	res.EnterNote = d.remoteEnterNote(res.Hostname)
	if d.herdrBinary("") == "" {
		res.EnterNote += " " + herdrInstallNote
	}
	return res
}

func (d *Daemon) rememberRelayedStart(id, node, socket, pane string) {
	if id == "" || node == "" {
		return
	}
	d.mu.Lock()
	if d.startedRelays == nil {
		d.startedRelays = make(map[string]string)
	}
	d.startedRelays[id] = node
	if d.startedRelaySockets == nil {
		d.startedRelaySockets = make(map[string]string)
	}
	d.startedRelaySockets[id] = socket
	if d.startedRelayPanes == nil {
		d.startedRelayPanes = make(map[string]string)
	}
	d.startedRelayPanes[id] = pane
	d.mu.Unlock()
}

func (d *Daemon) sessionResume(ctx context.Context, p protocol.SessionResumeRequest) (protocol.StartedSession, error) {
	a, err := d.agentView(ctx, p.SessionID)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	if a.NodeID != d.cfg.NodeID {
		return d.relaySessionResume(ctx, a, p.SessionID, p.HerdrSocket, p.HerdrPaneID)
	}
	return d.resumeAgent(ctx, a, p.HerdrSocket, p.HerdrPaneID)
}

// sessionResumeLocal resumes an offline session that must be on this machine.
// It is the gateway's path, so it never bounces the request again.
func (d *Daemon) sessionResumeLocal(ctx context.Context, sessionID string) (protocol.StartedSession, error) {
	a, err := d.agentView(ctx, sessionID)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	if a.NodeID != d.cfg.NodeID {
		return protocol.StartedSession{}, errors.New("session is not on this machine")
	}
	return d.resumeAgent(ctx, a, "", "")
}

func (d *Daemon) resumeAgent(ctx context.Context, a protocol.AgentView, socket, pane string) (protocol.StartedSession, error) {
	if a.NativeID == "" || (a.Harness != "claude" && a.Harness != "pi") {
		return protocol.StartedSession{}, errors.New("resume works for Claude and Pi for now")
	}
	if a.Online {
		return protocol.StartedSession{}, errors.New("session is already online")
	}
	info, err := os.Stat(a.Cwd)
	if err != nil || !info.IsDir() {
		return protocol.StartedSession{}, fmt.Errorf("session folder is gone: %s", a.Cwd)
	}
	r := protocol.StartedSession{ID: protocol.NewID("start"), Status: "starting", NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: a.Harness, Project: firstNonEmpty(a.Profile.Name, a.Title, a.NativeID), Path: a.Cwd, NativeID: a.NativeID, StartedAt: time.Now().Unix()}
	if a.Harness == "pi" {
		return d.launchOwnerPi(r, "", a.NativeID, socket, pane, false)
	}
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return protocol.StartedSession{}, err
	}
	return d.launchOwnerClaude(r, bin, []string{"--bg", "--resume", a.NativeID}, a.NativeID, socket, pane, false), nil
}

func (d *Daemon) relaySessionResume(ctx context.Context, a protocol.AgentView, sessionID, socket, pane string) (protocol.StartedSession, error) {
	owner := d.ownerSessionID()
	if owner == "" {
		return protocol.StartedSession{}, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	frame := protocol.SessionRelay{Owner: owner, NodeID: a.NodeID, SessionID: sessionID}
	data, err := d.call(ctx, protocol.FrameSessionResume, frame)
	if err != nil {
		return protocol.StartedSession{}, tooOldGateway(err)
	}
	var res protocol.StartedSession
	if err := json.Unmarshal(data, &res); err != nil {
		return protocol.StartedSession{}, err
	}
	d.rememberRelayedStart(res.ID, firstNonEmpty(res.NodeID, a.NodeID), socket, pane)
	return d.remoteStartResult(ctx, res, socket, pane), nil
}

func (d *Daemon) launchOwnerClaude(r protocol.StartedSession, bin string, args []string, resume, socket, pane string, notify bool) protocol.StartedSession {
	d.mu.Lock()
	if d.startedSessions == nil {
		d.startedSessions = make(map[string]protocol.StartedSession)
	}
	d.startedSessions[r.ID] = r
	d.mu.Unlock()
	// The launch belongs to the daemon, not the requesting socket. Cancelling a
	// request or closing the TUI must not stop an owner's background session.
	d.wg.Add(1)
	go func(r protocol.StartedSession) {
		defer d.wg.Done()
		cctx, cancel := context.WithTimeout(d.ctoCtx, d.cfg.Workers.filled().StartTimeout)
		defer cancel()
		d.claudeStartMu.Lock()
		sf, err := d.launchClaudeBackground(cctx, bin, r.Path, args, "", resume, nil)
		d.claudeStartMu.Unlock()
		if err == nil {
			r.NativeID, r.JobID = sf.SessionID, sf.JobID
			d.mu.Lock()
			d.startedSessions[r.ID] = r
			d.mu.Unlock()
			ticker := time.NewTicker(25 * time.Millisecond)
			defer ticker.Stop()
			for {
				d.mu.Lock()
				if ls := d.sessions[key("claude", sf.SessionID)]; ls != nil {
					r.SessionID = ls.sessionID
				}
				d.mu.Unlock()
				if r.SessionID != "" {
					break
				}
				select {
				case <-cctx.Done():
					err = fmt.Errorf("the daemon did not register the Claude session: %w", cctx.Err())
				case <-ticker.C:
				}
				if err != nil {
					break
				}
			}
		}
		if err != nil {
			r.Status = "failed"
			r.Error = err.Error()
		} else {
			job := r.JobID
			if job == "" && len(r.NativeID) >= 8 {
				job = r.NativeID[:8]
			}
			plan := protocol.EnterPlan{Argv: []string{bin, "attach", job}, Dir: r.Path, Host: r.Hostname,
				Env: map[string]string{"CLAUDE_CODE_DISABLE_MOUSE": "1"}, Note: "Press ctrl+] to come back here; the session keeps running."}
			if job == "" {
				plan.Argv = nil
				plan.Note = "The session started but has no Claude background ID to attach."
			}
			opened := d.openInHerdr(r.SessionID, r.Project, socket, pane, plan)
			if opened.OpenedHerdr || len(opened.Argv) == 0 {
				r.EnterPlan = &opened
			} else {
				r.EnterNote = "Enter opens it. " + herdrInstallNote
			}
			r.Status = "started"
		}
		d.mu.Lock()
		d.startedSessions[r.ID] = r
		d.mu.Unlock()
		if notify && r.Status == "started" {
			d.notifySessionStart(r)
		}
	}(r)
	return r
}

// sessionStartGet answers for a local start or asks the machine that holds a
// relayed one.
func (d *Daemon) sessionStartGet(ctx context.Context, id string) (protocol.StartedSession, error) {
	d.mu.Lock()
	r, ok := d.startedSessions[id]
	node := d.startedRelays[id]
	socket := d.startedRelaySockets[id]
	pane := d.startedRelayPanes[id]
	d.mu.Unlock()
	if ok {
		return r, nil
	}
	if node == "" {
		return r, errors.New("unknown session start")
	}
	owner := d.ownerSessionID()
	if owner == "" {
		return r, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	data, err := d.call(ctx, protocol.FrameSessionGet, protocol.SessionRelay{Owner: owner, NodeID: node, StartID: id})
	if err != nil {
		return r, tooOldGateway(err)
	}
	var remote protocol.StartedSession
	if err := json.Unmarshal(data, &remote); err != nil {
		return r, err
	}
	if remote.NodeID == "" {
		remote.NodeID = node
	}
	return d.remoteStartResult(ctx, remote, socket, pane), nil
}

// sessionStartGetLocal answers the gateway for a start on this machine.
func (d *Daemon) sessionStartGetLocal(id string) (protocol.StartedSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.startedSessions[id]
	if !ok {
		return r, errors.New("unknown session start")
	}
	return r, nil
}
