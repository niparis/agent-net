package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type ctoToolClass string

const (
	ctoRead     ctoToolClass = "read"
	ctoTalk     ctoToolClass = "talk"
	ctoProposal ctoToolClass = "proposal"
)

// Every CTO entry point is listed here. Unknown tools and actions fail closed.
// Reads must have an ordinary-agent source or be the CTO's own timeline.
var ctoToolClasses = func() map[string]ctoToolClass {
	out := map[string]ctoToolClass{}
	for class, names := range map[ctoToolClass]string{
		ctoRead:     "charter_guide agents nodes projects get messages message_status mcp_list worker_profile_list cto_events graph",
		ctoTalk:     "send reply learn ui_show ui_notice message",
		ctoProposal: "project_display_set charter_set node_add node_remove edge_set edge_remove graph_undo worker_profile_add worker_profile_remove worker_providers_set protected_providers_set score_set worker_start project_add project_remove mcp_call session_trust session_policy node_rename session_rename integrations_set worker_stop held_answer inbox_answer outcome_overrule node_settings_set playbook_set mcp_add mcp_remove mcp_test mcp_enable cto_send cto_new tools_plan tools_apply tools_undo mcp_import mcp_native_remove invite onboarding_restart notification_settings cto_settings",
	} {
		for _, name := range strings.Fields(names) {
			out[name] = class
		}
	}
	return out
}()

var ctoActions = map[string]map[string][]string{
	"message":          {"action": {"send", "reply"}},
	"held_answer":      {"action": {"a", "A", "r", "R"}},
	"outcome_overrule": {"action": {"let_stand", "overrule"}},
	"tools_plan":       {"operation": {"repo_remove"}},
}

func ctoClass(name string, raw json.RawMessage) (ctoToolClass, error) {
	class, ok := ctoToolClasses[name]
	if !ok {
		return "", fmt.Errorf("unclassified CTO tool %q", name)
	}
	var fields map[string]json.RawMessage
	if len(raw) > 0 && json.Unmarshal(raw, &fields) != nil {
		return "", errors.New("bad CTO tool arguments")
	}

	for field, actions := range ctoActions[name] {
		if value, exists := fields[field]; exists {
			var action string
			if json.Unmarshal(value, &action) != nil {
				return "", errors.New("bad CTO action")
			}
			found := false
			for _, a := range actions {
				found = found || a == action
			}
			if !found {
				return "", fmt.Errorf("unknown %s %s %q", name, field, action)
			}
		}
	}

	if name == "message" {
		var asOwner bool
		if value, ok := fields["as_owner"]; ok && json.Unmarshal(value, &asOwner) != nil {
			return "", errors.New("as_owner must be boolean")
		}
		if asOwner {
			return ctoProposal, nil
		}
	}
	return class, nil
}

func routeCTOTool(ctx context.Context, name string, raw json.RawMessage, propose func(context.Context, string, string, json.RawMessage, string) (string, error), run func(context.Context, string, json.RawMessage) (any, error)) (any, error) {
	class, err := ctoClass(name, raw)
	if err != nil {
		return nil, err
	}
	if class != ctoProposal {
		return run(ctx, name, raw)
	}
	fields := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
	}
	var reason string
	if value, ok := fields["reason"]; ok {
		if err := json.Unmarshal(value, &reason); err != nil {
			return nil, errors.New("reason must be a string")
		}
		if name != "protected_providers_set" {
			delete(fields, "reason")
		}
	}
	args, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	var action string
	if value, ok := fields["action"]; ok {
		_ = json.Unmarshal(value, &action)
	}
	id, err := propose(ctx, name, action, args, reason)
	if err != nil {
		return nil, err
	}
	return ProposalToolResult(id), nil
}
