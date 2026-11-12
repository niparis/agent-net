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
	if err := validateCloneStart(p); err != nil {
		return protocol.StartedSession{}, err
	}
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
	if p.MCPServers != "" {
		if p.MCPServers != protocol.MCPApprove && p.MCPServers != protocol.MCPDisable {
			return protocol.StartedSession{}, fmt.Errorf("mcp_servers must be %q or %q", protocol.MCPApprove, protocol.MCPDisable)
		}
		if p.Harness != "claude" {
			return protocol.StartedSession{}, errors.New("mcp_servers applies to Claude Code sessions only")
		}
	}
	target, err := d.sessionStartMachine(ctx, p)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	if target != "" {
		// Older gateways ignore unknown start fields. Require the selection API
		// before allowing a relay to carry the requested identity.
		if p.AgentID != "" {
			if err := d.reserveStartAgent(ctx, p.Harness, "", p.AgentID); err != nil {
				return protocol.StartedSession{}, err
			}
		}
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

func validateCloneStart(p protocol.SessionStartRequest) error {
	if p.Remote == "" {
		return nil
	}
	if !p.CTOProposal || !p.Create || p.Path == "" {
		return errors.New("remote requires an approved project create")
	}
	return validateProjectRemote(p.Remote)
}

// sessionStartLocal starts the session on this machine.
func (d *Daemon) sessionStartLocal(ctx context.Context, p protocol.SessionStartRequest) (result protocol.StartedSession, startErr error) {
	if err := validateCloneStart(p); err != nil {
		return protocol.StartedSession{}, err
	}
	if p.AgentID != "" {
		if err := d.reserveStartAgent(ctx, p.Harness, "", p.AgentID); err != nil {
			return protocol.StartedSession{}, err
		}
	}
	bin, err := d.harnessBinary(p.Harness)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	var found *protocol.ProjectView
	// created says agent-net made the typed folder on this request.
	created := false
	if p.Path != "" {
		path, err := cleanOwnerPath(p.Path)
		if err != nil {
			return protocol.StartedSession{}, err
		}
		info, statErr := os.Stat(path)
		_, linkErr := os.Lstat(path)
		if p.CTOProposal && p.Create && linkErr == nil {
			return protocol.StartedSession{}, fmt.Errorf("%s already exists; choose another name or ask to register it", p.Path)
		}
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			if !p.Create {
				return protocol.StartedSession{ID: protocol.NewID("start"), Status: "needs_create",
					NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: p.Harness,
					Project: filepath.Base(path), Path: path, Model: p.Model, StartedAt: time.Now().Unix()}, nil
			}
			if p.Remote != "" {
				if err := d.cloneOwnerProject(ctx, p.Remote, path); err != nil {
					return protocol.StartedSession{}, err
				}
			} else if err := createOwnerFolder(ctx, path); err != nil {
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
	startRef := protocol.NewID("start")
	reserved, launched := false, false
	if !p.Merger {
		admission, err := d.repoStartReservation(ctx, protocol.RepoStartReservation{Owner: d.ownerSessionID(), Ref: startRef, Project: found.Name, Harness: p.Harness})
		if err != nil {
			return protocol.StartedSession{}, err
		}
		if admission.Status == "failed" {
			return protocol.StartedSession{ID: startRef, Status: "failed", Project: found.Name, Error: admission.Error, SuggestedChange: admission.SuggestedChange}, nil
		}
		reserved = admission.Reserved
	}
	defer func() {
		if reserved && (!launched || startErr != nil || result.Status == "failed") {
			d.releaseRepoStart(protocol.StartedSession{ID: startRef, Project: found.Name, Harness: p.Harness, LimitReserved: true})
		}
	}()
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
	// Claude Code refuses an untrusted folder in `claude --bg`, so the answer is
	// asked here for a project name and a typed path alike.
	if p.Harness == "claude" {
		needTrust, err := ownerClaudeTrust(sessionPath, created, p.Folder == protocol.FolderWorktree, p.TrustClaude)
		if err != nil {
			return protocol.StartedSession{}, err
		}
		if needTrust {
			// The gateway raises the owner's one-click trust ask, even when the
			// start runs on this machine and never crosses the gateway relay.
			go d.reportTrustNeed(context.Background(), sessionPath, found.Name, d.ownerSessionID())
			return protocol.StartedSession{ID: protocol.NewID("start"), Status: "needs_trust",
				NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: p.Harness,
				Project: found.Name, Path: sessionPath, Branch: branch, Model: p.Model,
				StartedAt: time.Now().Unix()}, nil
		}
	}
	// The choice for the .mcp.json servers is stored in the launch folder before
	// Claude Code starts, so its first run does not stop on the approval prompt.
	var mcpChoice *protocol.MCPChoiceResult
	if p.Harness == "claude" {
		// A new worktree is a new folder for Claude Code: copy the answers the
		// owner gave for the project's main checkout, then apply the choice.
		main := found.Path
		if sessionPath == found.Path {
			main = ""
		}
		var err error
		if mcpChoice, err = d.prepareClaudeMCP(main, sessionPath, p.MCPServers); err != nil {
			return protocol.StartedSession{}, err
		}
	}
	requester, _ := ctx.Value(startRequesterKey{}).(string)
	r := protocol.StartedSession{MCPChoice: mcpChoice, Requester: requester, AgentID: p.AgentID, Background: p.CTOProposal && p.Harness == "claude", ID: startRef, LimitReserved: reserved, Status: "starting", NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: p.Harness, Project: found.Name, Path: sessionPath, Branch: branch, Model: p.Model, StartedAt: time.Now().Unix()}
	defer func() {
		if startErr != nil || result.Status == "failed" {
			d.failStartAgent(r)
		}
	}()
	if p.Merger {
		r.Type = "merger"
	}
	if r.AgentID != "" && r.Harness != "codex" && r.Harness != "opencode" {
		if err := d.reserveStartAgent(ctx, r.Harness, "", r.AgentID, r.ID, r.Path, r.Project); err != nil {
			return protocol.StartedSession{}, err
		}
	}
	launched = true
	remoteOwner, _ := ctx.Value(proposalRemoteOwnerKey{}).(bool)
	notify := p.CTOProposal && !remoteOwner
	if p.Harness == "codex" {
		args := []string{bin}
		// Pin a running shared server. Without an explicit target Codex can
		// silently fall back to an embedded server, invisible to discovery.
		// When it is not running, let Codex's normal startup create it.
		if ca, ok := d.adapters["codex"].(*codexAdapter); ok && ca.serverPID() != 0 {
			args = append(args, "--remote", "unix://")
		}
		if r.AgentID != "" {
			var err error
			r, err = d.prepareOwnerCodex(ctx, r)
			if err != nil {
				return protocol.StartedSession{}, err
			}
			args = append(args, "resume", r.NativeID)
		}
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
		plan := d.viewPlan("start:"+r.ID, r.Project, p.HerdrSocket, p.HerdrPaneID, p.HerdrClient, *r.EnterPlan)
		r.EnterPlan = &plan
		if p.CTOProposal && !plan.OpenedHerdr {
			loc, _ := ctx.Value(proposalHerdrLocationKey{}).(proposalHerdrLocation)
			if remoteOwner || loc.approver == nil || len(plan.Argv) == 0 {
				r.Status = "failed"
				r.Error = "session could not start on " + d.hostName() + ": no herdr pane and no approving TUI available to confirm the start"
				if remoteOwner {
					r.Error = "remote fallback plan cannot be confirmed by the approving TUI"
				}
				if len(plan.Argv) == 0 && plan.Note != "" {
					r.Error += ": " + plan.Note
				}
			} else {
				r.Status = "handed_to_tui"
			}
		} else if notify {
			d.notifyApprovedSessionStart(ctx, r)
		}

		return r, nil
	}
	if p.Harness == "opencode" {
		return d.launchOwnerOpenCode(ctx, r, p.FirstMessage, p.HerdrSocket, p.HerdrPaneID, notify, remoteOwner)
	}
	if p.Harness == "pi" {
		return d.launchOwnerPi(r, p.FirstMessage, "", p.HerdrSocket, p.HerdrPaneID, notify)
	}
	name := r.Project
	if p.Merger {
		name += " merger"
	}
	args := []string{"--bg", "--name", name}
	// The MCP choice for the .mcp.json servers of this folder goes in the
	// launch settings too, so the start does not depend on the settings file.
	// Mouse tracking comes from the user's Claude settings, which the daemon
	// owns (decision 0128); a launch carries no copy of it.
	settings := ""
	if p.Merger {
		settings = mergerClaudeSettings(d.workerGoCacheDir())
	}
	if settings = claudeSettingsWithMCPAnswers(settings, r.Path); settings != "" {
		args = append(args, "--settings", settings)
	}
	if p.Model != "" {
		args = append(args, "--model", p.Model)
	}
	if p.FirstMessage != "" && p.AgentID == "" {
		args = append(args, "--", p.FirstMessage)
	}
	return d.launchOwnerClaude(r, bin, args, "", p.HerdrSocket, p.HerdrPaneID, notify, remoteOwner, p.CTOProposal, p.FirstMessage), nil
}

// relaySessionStart asks the gateway to start the session on another machine.
func (d *Daemon) relaySessionStart(ctx context.Context, p protocol.SessionStartRequest, target string) (protocol.StartedSession, error) {
	owner := d.ownerSessionID()
	if owner == "" {
		return protocol.StartedSession{}, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	socket, pane := p.HerdrSocket, p.HerdrPaneID
	p.HerdrSocket, p.HerdrPaneID = "", ""
	p.HerdrClient = !p.CTOProposal && !p.Merger && d.herdrBinary("") != ""
	frame := protocol.SessionStartRelay{Owner: owner, NodeID: target, SessionStartRequest: p}
	typ := protocol.FrameSessionStart
	if p.CTOProposal {
		typ = protocol.FrameSessionProposalStart
		if p.Remote != "" {
			typ = protocol.FrameSessionProposalClone
		}
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
	if p.CTOProposal && !p.Merger && !remoteOwner && res.Status == "starting" {
		d.watchApprovedSessionStart(ctx, res.ID)
	} else if p.CTOProposal && !p.Merger && !remoteOwner && res.Status == "started" {
		d.notifyApprovedSessionStart(ctx, res)
	}
	return res, nil
}

// An approved remote start must be opened even when no form is polling it.
func (d *Daemon) watchApprovedSessionStart(approval context.Context, id string) {
	loc, _ := approval.Value(proposalHerdrLocationKey{}).(proposalHerdrLocation)
	requester, _ := approval.Value(startRequesterKey{}).(string)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(context.WithValue(d.ctoCtx, proposalHerdrLocationKey{}, loc), d.workerSettings("").StartTimeout)
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
					// The machine that runs the start logs and records a failure.
					// This machine only tells the owner, as for a successful start.
					if err == nil && result.Status == "started" {
						d.notifyApprovedSessionStart(ctx, result)
					} else if err == nil && result.Status == "failed" {
						d.notifySessionStartFailed(result)
						d.notifyRequesterStartFailed(requester, result)
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
	if res.Background || res.Type == "merger" {
		res.EnterPlan = nil
		res.EnterNote = "Enter opens it."
		return res
	}
	if res.NodeID == "" || res.NodeID == d.cfg.NodeID {
		return res
	}
	res.Hostname = firstNonEmpty(res.Hostname, res.NodeID)
	if res.SessionID != "" && d.herdrBinary("") != "" && (res.EnterPlan == nil || !res.EnterPlan.OpenedHerdr) {
		if plan, err := d.enterPlan(ctx, protocol.EnterRequest{SessionID: res.SessionID, HerdrSocket: socket, HerdrPaneID: pane}); err == nil && (plan.OpenedHerdr || len(plan.Argv) > 0) {
			res.EnterPlan = &plan
			res.EnterNote = "Enter opens it."
			return res
		}
	}
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
		plan = d.viewPlan(key, res.Project, socket, pane, false, plan)
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
	if a.NativeID == "" || (a.Harness != "claude" && a.Harness != "pi" && a.Harness != "opencode" && a.Harness != "codex") {
		return protocol.StartedSession{}, errors.New("Cannot resume this conversation here. Open it in the harness directly.")
	}
	// The gateway records a session whose harness reports status exited as
	// offline: a Claude background session keeps its process and session file
	// after /exit, but nothing can be attached to or delivered to it. Resume is
	// the only way back, and a live session stays refused so its conversation
	// never starts twice.
	if a.Online {
		return protocol.StartedSession{}, errors.New("session is already online")
	}
	info, err := os.Stat(a.Cwd)
	if err != nil || !info.IsDir() {
		return protocol.StartedSession{}, fmt.Errorf("session folder is gone: %s", a.Cwd)
	}
	r := protocol.StartedSession{Resumes: a.SessionID, ID: protocol.NewID("start"), Status: "starting", NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: a.Harness, Project: firstNonEmpty(a.Profile.Name, a.Title, a.NativeID), Path: a.Cwd, NativeID: a.NativeID, StartedAt: time.Now().Unix()}
	if a.Harness == "opencode" {
		return d.resumeOwnerOpenCode(ctx, r, socket, pane)
	}
	if a.Harness == "codex" {
		return d.resumeOwnerCodex(ctx, r, socket, pane)
	}
	if a.Harness == "pi" {
		return d.launchOwnerPi(r, "", a.NativeID, socket, pane, false)
	}
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return protocol.StartedSession{}, err
	}
	return d.launchOwnerClaude(r, bin, d.claudeResumeArgs(a), a.NativeID, socket, pane, false, false, false), nil
}

// claudeResumeArgs is the argument list of the owner resume. The wake fallback
// uses the same list, so a wake and session_change resume open the same way.
func (d *Daemon) claudeResumeArgs(a protocol.AgentView) []string {
	args := []string{"--bg", "--resume", a.NativeID}
	settings := ""
	if a.Type == "merger" {
		settings = mergerClaudeSettings(d.workerGoCacheDir())
	}
	if settings = claudeSettingsWithMCPAnswers(settings, a.Cwd); settings != "" {
		args = append(args, "--settings", settings)
	}
	return args
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

// relayed says the owner approved the start from another machine. That machine
// has no herdr location for the owner's TUI, so this one must return the attach
// plan for the owner's machine to wrap and open.
func (d *Daemon) launchOwnerClaude(r protocol.StartedSession, bin string, args []string, resume, socket, pane string, notify, relayed, background bool, firstMessage ...string) protocol.StartedSession {
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
		cctx, cancel := context.WithTimeout(d.ctoCtx, d.workerSettings("").StartTimeout)
		defer cancel()
		d.claudeStartMu.Lock()
		sf, err := d.launchClaudeBackground(cctx, bin, r.Path, args, "", resume, nil, nil)
		if err == nil && r.Resumes != "" && sf.SessionID != "" && sf.SessionID != resume {
			// Claude gives a resumed background conversation a new native ID.
			// Keep the gateway record, and with it every worker, change and
			// message that belongs to the session. Discovery takes this lock,
			// so it cannot register the copy first.
			if rerr := d.rebindResumedSession(cctx, r.Resumes, sf.SessionID); rerr != nil {
				d.Logf("session resume: could not keep the session ID of %s: %v", r.Resumes, rerr)
			}
		}
		d.claudeStartMu.Unlock()
		if err == nil {
			r.NativeID, r.JobID = sf.SessionID, sf.JobID
			if r.LimitReserved {
				err = d.bindRepoStart(cctx, r)
			}
			if err == nil && r.AgentID != "" {
				err = d.reserveStartAgent(cctx, "claude", sf.SessionID, r.AgentID, r.ID, r.Path, r.Project)
			}
		}
		if err == nil {
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
		if err == nil {
			// A new conversation that could not keep the old ID hands its work
			// over to the record it registered.
			d.adoptResumedSession(cctx, r.Resumes, r.SessionID)
		}
		if err == nil {
			err = d.checkStartAgent(cctx, r)
		}
		if err == nil && r.Type == "merger" {
			_, err = d.call(cctx, "merger.set", map[string]any{"project": r.Project, "node_id": d.cfg.NodeID, "session_id": r.SessionID, "configure": true, "by": "owner"})
		}
		if err == nil && r.AgentID != "" && len(firstMessage) > 0 && firstMessage[0] != "" {
			_, err = d.send(cctx, protocol.MsgSend{Sender: d.ownerSessionID(), Recipient: r.SessionID, Type: protocol.TypeRequest, Body: firstMessage[0]})
		}
		if err != nil {
			r.Status = "failed"
			r.Error = err.Error()
			var blocked *claudeMCPApprovalError
			if errors.As(err, &blocked) {
				r.ErrorCode, r.JobID, r.JobAlive, r.MCPServers = protocol.StartErrorMCPApprovalNeeded, blocked.JobID, blocked.Alive, blocked.Servers
			}
			d.releaseRepoStart(r)
			d.failStartAgent(r)
		} else if r.Type == "merger" {
			r.Status = "started"
		} else {
			job := r.JobID
			if job == "" && len(r.NativeID) >= 8 {
				job = r.NativeID[:8]
			}
			// Claude Code ignores the mouse variable on the attach client; the
			// user's Claude settings carry it (decision 0128).
			plan := protocol.EnterPlan{Argv: []string{bin, "attach", job}, Dir: r.Path, Host: r.Hostname,
				Note: "Press ctrl+] to come back here; the session keeps running."}
			if job == "" {
				plan.Argv = nil
				plan.Note = "The session started but has no Claude background ID to attach."
			}
			opened := plan
			if !background {
				opened = d.viewPlan(r.SessionID, r.Project, socket, pane, false, plan)
			}
			if background {
				r.EnterNote = "Enter opens it."
			} else if opened.OpenedHerdr || len(opened.Argv) == 0 || relayed {
				r.EnterPlan = &opened
			} else {
				r.EnterNote = "Enter opens it."
				if d.herdrBinary("") == "" {
					r.EnterNote += " " + herdrInstallNote
				}
			}
			r.Status = "started"
		}
		// Report a failure before the failed state is visible, so a poller that
		// sees "failed" also finds the log line and the gateway event.
		if r.Status == "failed" {
			d.startFailed(r, notify)
		}
		d.mu.Lock()
		d.startedSessions[r.ID] = r
		d.mu.Unlock()
		if r.Status == "started" && notify {
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
	r, ok := d.startedSessions[id]
	d.mu.Unlock()
	if r.AgentID != "" || !ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status, err := d.startAgentStatus(ctx, id)
		if err == nil && status.Status == "failed" {
			r.ID, r.Status, r.Error, r.AgentID = id, "failed", status.Error, ""
			if ok {
				d.mu.Lock()
				d.startedSessions[id] = r
				d.mu.Unlock()
			}
			return r, nil
		}
	}
	if !ok {
		return r, errors.New("unknown session start")
	}
	return r, nil
}

// reserveStartAgent is reachable from owner starts and approved CTO proposals.
// Old gateways must refuse explicit selection instead of silently using a default.
func (d *Daemon) reserveStartAgent(ctx context.Context, harness, native, agent string, launch ...string) error {
	p := protocol.SessionAgentReservation{Owner: d.ownerSessionID(), Harness: harness, NativeID: native, AgentID: agent}
	if len(launch) >= 2 {
		p.Ref, p.Cwd = launch[0], launch[1]
		if len(launch) > 2 {
			p.Project = launch[2]
		}
	}
	_, err := d.call(ctx, "session.agent.reserve", p)
	return err
}

func (d *Daemon) startAgentStatus(ctx context.Context, ref string) (protocol.StartedSession, error) {
	raw, err := d.call(ctx, "session.agent.status", protocol.SessionAgentReservation{Owner: d.ownerSessionID(), Ref: ref})
	var status protocol.StartedSession
	if err == nil {
		err = json.Unmarshal(raw, &status)
	}
	return status, err
}

func (d *Daemon) checkStartAgent(ctx context.Context, r protocol.StartedSession) error {
	if r.AgentID == "" {
		return nil
	}
	status, err := d.startAgentStatus(ctx, r.ID)
	if err != nil {
		return err
	}
	if status.Status == "failed" {
		return errors.New(status.Error)
	}
	return nil
}

func (d *Daemon) failStartAgent(r protocol.StartedSession) {
	if r.AgentID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := d.call(ctx, "session.agent.reserve", protocol.SessionAgentReservation{Owner: d.ownerSessionID(), AgentID: r.AgentID, Harness: r.Harness, Ref: r.ID, Cwd: r.Path, Project: r.Project, NativeID: r.NativeID, Failed: true})
	if err != nil {
		d.Logf("Agent choice failure report for %s: %v", r.ID, err)
	}
}

// startFailed reports a start that was answered as starting and then failed in
// the background. It writes one daemon log line, tells the gateway, and, for a
// start the owner asked for (notify), shows the error in the owner's TUI, as a
// successful start is shown. Call it before r is stored as failed, so that a
// poller who sees "failed" also finds the log line and the gateway event.
func (d *Daemon) startFailed(r protocol.StartedSession, notify bool) {
	d.Logf("event=session.start_failed start=%s project=%s harness=%s error=%q", r.ID, r.Project, r.Harness, r.Error)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := d.call(ctx, "session.start.failed", protocol.SessionStartFailure{Owner: d.ownerSessionID(), StartID: r.ID,
		Project: r.Project, Harness: r.Harness, Error: r.Error, Requester: r.Requester, ErrorCode: r.ErrorCode, Hostname: r.Hostname})
	if err != nil {
		d.Logf("session start %s: the gateway did not record the failure: %v", r.ID, err)
	}
	if notify {
		d.notifySessionStartFailed(r)
	}
}

// startRequesterKey carries the session that asked for a start (the CTO) to
// the place where the start fails, which can be long after the call returned.
type startRequesterKey struct{}

// repoStartReservation returns a structured refusal so the caller can offer a
// settings change without parsing an error string.
func (d *Daemon) repoStartReservation(ctx context.Context, p protocol.RepoStartReservation) (protocol.RepoStartResult, error) {
	var out protocol.RepoStartResult
	raw, err := d.call(ctx, "session.repo.reserve", p)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
func (d *Daemon) bindRepoStart(ctx context.Context, r protocol.StartedSession) error {
	if !r.LimitReserved {
		return nil
	}
	_, err := d.repoStartReservation(ctx, protocol.RepoStartReservation{Owner: d.ownerSessionID(), Ref: r.ID, Project: r.Project, Harness: r.Harness, NativeID: r.NativeID})
	return err
}
func (d *Daemon) releaseRepoStart(r protocol.StartedSession) {
	if !r.LimitReserved {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := d.repoStartReservation(ctx, protocol.RepoStartReservation{Owner: d.ownerSessionID(), Ref: r.ID, Release: true})
	if err != nil {
		d.Logf("release Repo start reservation %s: %v", r.ID, err)
	}
}

// notifyRequesterStartFailed asks the gateway to send its own failure notice.
// The machine that ran a remote start already recorded the failure event.
func (d *Daemon) notifyRequesterStartFailed(requester string, r protocol.StartedSession) {
	if requester == "" || requester == d.ownerSessionID() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := d.call(ctx, "session.start.failed", protocol.SessionStartFailure{Owner: d.ownerSessionID(), StartID: r.ID, Project: r.Project, Harness: r.Harness, Error: r.Error, ErrorCode: r.ErrorCode, Hostname: r.Hostname, Requester: requester, NoticeOnly: true})
	if err != nil {
		d.Logf("session start %s: requester was not told of failure: %v", r.ID, err)
	}
}
