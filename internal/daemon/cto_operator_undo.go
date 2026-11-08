package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

type ownerUndoCall struct {
	Method    string          `json:"method"`
	Arguments json.RawMessage `json:"arguments"`
}
type ownerOperationCall func(string, json.RawMessage) (any, error)

func undoCall(method string, args any) ownerUndoCall {
	raw, _ := json.Marshal(args)
	return ownerUndoCall{method, raw}
}

func (lc *localConn) operatorUndo(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct {
		Operations []ownerUndoCall `json:"operations"`
	}
	if json.Unmarshal(raw, &p) != nil || len(p.Operations) == 0 {
		return nil, fmt.Errorf("operator_undo needs operations")
	}
	allowed := map[string]bool{"onboarding_restart": true, "onboarding_set": true, "graph_undo": true, "settings_undo": true, "session_rename": true, "merger_set": true, "merger_restore": true, "sessions_archive": true, "apps.public.restore": true, "session_resume": true, "session_stop": true, "session_agent_set": true, "tools_undo": true, "mcp_coverage_undo": true, "hidden_set": true, "timezone_set": true, "quality_set": true, "difficulty_set": true, "worker_floors_set": true, "worker_floors_reset": true, "session_pin": true, "tools_accept": true}
	// Validate the whole plan before applying its first operation.
	for _, op := range p.Operations {
		if !allowed[op.Method] {
			return nil, fmt.Errorf("unsupported undo operation %q", op.Method)
		}
	}
	for _, op := range p.Operations {
		if _, handled, err := lc.dispatchOwner(ctx, op.Method, op.Arguments); err != nil {
			return nil, err
		} else if !handled {
			return nil, fmt.Errorf("unknown undo operation %s", op.Method)
		}
	}
	return map[string]bool{"undone": true}, nil
}

func (d *Daemon) operatorUndoBefore(ctx context.Context, method string, args json.RawMessage, call ownerOperationCall) ([]ownerUndoCall, error) {
	getter := map[string]string{"hidden_set": "hidden_get", "timezone_set": "timezone_get", "quality_set": "quality_get", "difficulty_set": "difficulty_get"}[method]
	if getter != "" {
		before, err := call(getter, json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		return []ownerUndoCall{undoCall(method, before)}, nil
	}
	switch method {
	case "worker_floors_set", "worker_floors_reset":
		before, err := call("worker_floors_get", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(before)
		if err != nil {
			return nil, err
		}
		var old protocol.WorkerFloors
		if err := json.Unmarshal(data, &old); err != nil {
			return nil, err
		}
		if !old.Configured {
			return []ownerUndoCall{undoCall("worker_floors_reset", map[string]any{})}, nil
		}
		return []ownerUndoCall{undoCall("worker_floors_set", old)}, nil

	case "session_pin", "session_agent_set":
		var q struct {
			Session string `json:"session_id"`
		}
		if json.Unmarshal(args, &q) != nil {
			return nil, fmt.Errorf("bad session operation")
		}
		before, err := d.agentView(ctx, q.Session)
		if err != nil {
			return nil, err
		}
		if method == "session_pin" {
			return []ownerUndoCall{undoCall(method, map[string]any{"session_id": q.Session, "pinned": before.PinnedAt != 0})}, nil
		}
		if before.AgentID != "" {
			return []ownerUndoCall{undoCall(method, map[string]any{"session_id": q.Session, "agent": before.AgentID})}, nil
		}
	case "session_stop":
		return []ownerUndoCall{{"session_resume", args}}, nil
	case "session_resume":
		return []ownerUndoCall{{"session_stop", args}}, nil
	case "tools_accept":
		var q protocol.ToolAccept
		if err := json.Unmarshal(args, &q); err != nil {
			return nil, err
		}
		before, err := call("tools_accepted", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(before)
		if err != nil {
			return nil, err
		}
		var keys []string
		if err := json.Unmarshal(data, &keys); err != nil {
			return nil, err
		}
		q.Accepted = false
		for _, key := range keys {
			if key == q.Key {
				q.Accepted = true
			}
		}
		return []ownerUndoCall{undoCall(method, q)}, nil
	case "apps.public.url", "apps.public.restore":
		before, err := call("apps.public.status", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		return []ownerUndoCall{undoCall("apps.public.restore", before)}, nil
	case "onboarding_set":
		before, err := call("onboarding_get", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(before)
		if err != nil {
			return nil, err
		}
		var old protocol.Onboarding
		if err := json.Unmarshal(data, &old); err != nil {
			return nil, err
		}
		plan := []ownerUndoCall{undoCall("onboarding_restart", map[string]any{})}
		var steps []string
		for step := range old.Steps {
			steps = append(steps, step)
		}
		sort.Strings(steps)
		for _, step := range steps {
			plan = append(plan, undoCall("onboarding_set", protocol.OnboardingUpdate{Step: step, State: old.Steps[step]}))
		}
		plan = append(plan, undoCall("onboarding_set", protocol.OnboardingUpdate{Disabled: &old.Disabled}))
		return plan, nil
	case "merger_set", "merger_restore":
		before, err := call("merger_get", args)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(before)
		if err != nil {
			return nil, err
		}
		var old []protocol.MergerSetRequest
		if err := json.Unmarshal(data, &old); err != nil {
			return nil, err
		}
		var q protocol.MergerSetRequest
		if err := json.Unmarshal(args, &q); err != nil {
			return nil, err
		}
		plan := []ownerUndoCall{undoCall("merger_restore", map[string]any{"project": q.Project, "assignments": old})}
		if q.Configure && q.SessionID != "" {
			view, err := d.agentView(ctx, q.SessionID)
			if err != nil {
				return nil, err
			}
			plan = append(plan, undoCall("session_rename", protocol.SessionRenameRequest{SessionID: q.SessionID, Name: view.Profile.Name}))
		}
		return plan, nil
	}
	return nil, nil
}
