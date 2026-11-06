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
	ctx, cancel := context.WithTimeout(ctx, d.workerSettings("").StartTimeout)
	defer cancel()
	rpc, err := dialCodexSocket(ctx, codexSocketPath(d.cfg.CodexHome), d.helloVersion(), nil)
	if err != nil {
		return r, err
	}
	defer rpc.close()
	if err = d.reserveStartAgent(ctx, "codex", "", r.AgentID, r.ID, r.Path, r.Project); err != nil {
		return r, err
	}
	params := map[string]any{"cwd": r.Path}
	if r.Model != "" {
		model, effort := splitCodexModel(r.Model)
		params["model"] = model
		if effort != "" {
			params["config"] = map[string]string{"model_reasoning_effort": effort}
		}
	}
	raw, err := rpc.call(ctx, "thread/start", params)
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
	if err = d.reserveStartAgent(ctx, "codex", r.NativeID, r.AgentID, r.ID, r.Path, r.Project); err != nil {
		return r, err
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
