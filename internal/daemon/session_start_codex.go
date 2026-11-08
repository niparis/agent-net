package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Create only the owner's new thread, then relinquish the temporary subscription.
// The long-lived discovery client remains read-only. No turn starts here: the
// owner's CLI resumes this exact ID and handles its normal permission prompts.
func (d *Daemon) prepareOwnerCodex(ctx context.Context, r protocol.StartedSession) (protocol.StartedSession, error) {
	return d.prepareOwnerCodexThread(ctx, r, false)
}

func (d *Daemon) prepareOwnerCodexThread(ctx context.Context, r protocol.StartedSession, resume bool) (protocol.StartedSession, error) {
	ctx, cancel := context.WithTimeout(ctx, d.workerSettings("").StartTimeout)
	defer cancel()
	rpc, err := dialCodexSocket(ctx, codexSocketPath(d.cfg.CodexHome), d.helloVersion(), nil)
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

func (d *Daemon) resumeOwnerCodex(ctx context.Context, r protocol.StartedSession, socket, pane string) (protocol.StartedSession, error) {
	bin, err := d.harnessBinary("codex")
	if err != nil {
		return r, err
	}
	r, err = d.prepareOwnerCodexThread(ctx, r, true)
	if err != nil {
		return r, err
	}
	plan := protocol.EnterPlan{Argv: []string{bin, "resume", r.NativeID}, Dir: r.Path, Host: r.Hostname, Note: "Press ctrl+] to come back here; Codex keeps running."}
	if home, def := d.cfg.CodexHome, defaultCodexHome(); home != "" && home != def {
		plan.Env = map[string]string{"CODEX_HOME": home}
	}
	opened := d.openForCaller(r.SessionID, r.Project, socket, pane, plan)
	r.EnterPlan = &opened
	r.Status = "started"
	return r, nil
}
