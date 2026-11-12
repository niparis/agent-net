package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Create only the owner's new thread, then relinquish the temporary subscription.
// The long-lived discovery client remains read-only. No turn starts here: the
// owner's CLI resumes this exact ID and handles its normal permission prompts.
func (d *Daemon) prepareOwnerCodex(ctx context.Context, r protocol.StartedSession) (protocol.StartedSession, error) {
	return d.prepareOwnerCodexThread(ctx, r, false)
}

// dialOwnerCodex connects to the shared app server. Nothing starts it after a
// reboot, or on a machine where the owner has not run Codex yet, so a missing
// control socket starts the server with Codex's own command, then dials again.
func (d *Daemon) dialOwnerCodex(ctx context.Context) (*codexRPC, error) {
	sock := codexSocketPath(d.cfg.CodexHome)
	rpc, err := dialCodexSocket(ctx, sock, d.helloVersion(), nil)
	if !codexServerDown(err) {
		return rpc, err
	}
	if err := d.startCodexAppServer(ctx); err != nil {
		return nil, fmt.Errorf("cannot start the Codex app server: %w", err)
	}
	return dialCodexSocket(ctx, sock, d.helloVersion(), nil)
}

func (d *Daemon) prepareOwnerCodexThread(ctx context.Context, r protocol.StartedSession, resume bool) (protocol.StartedSession, error) {
	ctx, cancel := context.WithTimeout(ctx, d.workerSettings("").StartTimeout)
	defer cancel()
	rpc, err := d.dialOwnerCodex(ctx)
	if err != nil {
		return r, err
	}
	defer rpc.close()
	if r.AgentID != "" {
		if err = d.reserveStartAgent(ctx, "codex", "", r.AgentID, r.ID, r.Path, r.Project); err != nil {
			return r, err
		}
	}
	params := map[string]any{"cwd": r.Path}
	// The app server takes no text per turn. A thread that agent-net starts or
	// resumes gets the charter, with the owner's global rules, as developer
	// instructions, so the rules reach it even when it does not read the MCP
	// server instructions.
	if text := d.codexThreadInstructions(ctx, r.Project); text != "" {
		params["developerInstructions"] = text
	}
	if r.Model != "" {
		model, effort := splitCodexModel(r.Model)
		params["model"] = model
		if effort != "" {
			params["config"] = map[string]string{"model_reasoning_effort": effort}
		}
	}
	method := "thread/start"
	if resume {
		method = "thread/resume"
		params["threadId"] = r.NativeID
	}
	raw, err := rpc.call(ctx, method, params)
	if err != nil {
		return r, err
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Thread.ID == "" {
		return r, errors.New("Codex answered without a thread ID")
	}
	r.NativeID = out.Thread.ID
	if err := d.bindRepoStart(ctx, r); err != nil {
		return r, err
	}
	if r.AgentID != "" {
		if err = d.reserveStartAgent(ctx, "codex", r.NativeID, r.AgentID, r.ID, r.Path, r.Project); err != nil {
			return r, err
		}
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		r.SessionID = d.SessionID("codex", r.NativeID)
		if r.SessionID != "" {
			return r, d.checkStartAgent(ctx, r)
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-ticker.C:
		}
	}
}

// codexThreadInstructions is the charter text for a Codex thread of a project.
func (d *Daemon) codexThreadInstructions(ctx context.Context, project string) string {
	return d.orGlobalCharter(ctx, d.charterContext(ctx, project))
}

// launchOwnerCodex starts an owner Codex session on the shared app server and
// registers it. Nothing here needs a terminal: the thread lives in the app
// server, and a viewer (`codex resume --remote`) is the optional view step.
// The first message goes in through the daemon, as for a Claude session.
func (d *Daemon) launchOwnerCodex(ctx context.Context, r protocol.StartedSession, firstMessage string, view startView, notify bool) (protocol.StartedSession, error) {
	bin, err := d.harnessBinary("codex")
	if err != nil {
		return r, err
	}
	r, err = d.prepareOwnerCodex(ctx, r)
	if err != nil {
		return r, err
	}
	if firstMessage != "" {
		if _, err := d.send(ctx, protocol.MsgSend{Sender: d.ownerSessionID(), Recipient: r.SessionID, Type: protocol.TypeRequest, Body: firstMessage}); err != nil {
			return r, err
		}
	}
	r.Status = "started"
	d.showStart(&r, d.codexViewPlan(bin, r), view)
	if notify {
		d.notifySessionStart(r)
	}
	return r, nil
}

// codexViewPlan is the command that shows a thread of the shared app server.
func (d *Daemon) codexViewPlan(bin string, r protocol.StartedSession) protocol.EnterPlan {
	plan := protocol.EnterPlan{Argv: []string{bin, "resume", "--remote", "unix://", r.NativeID}, Dir: r.Path, Host: r.Hostname, Note: "Press ctrl+] to come back here; Codex keeps running."}
	if home, def := d.cfg.CodexHome, defaultCodexHome(); home != "" && home != def {
		plan.Env = map[string]string{"CODEX_HOME": home}
	}
	return plan
}

func (d *Daemon) resumeOwnerCodex(ctx context.Context, r protocol.StartedSession, socket, pane string) (protocol.StartedSession, error) {
	bin, err := d.harnessBinary("codex")
	if err != nil {
		return r, err
	}
	r, err = d.prepareOwnerCodexThread(ctx, r, true)
	if err != nil {
		return r, err
	}
	// A Codex thread the app server could not find is a new conversation: the
	// old session's work moves to it.
	d.adoptResumedSession(ctx, r.Resumes, r.SessionID)
	opened := d.viewPlan(r.SessionID, r.Project, false, d.codexViewPlan(bin, r))
	r.EnterPlan = &opened
	r.Status = "started"
	return r, nil
}
