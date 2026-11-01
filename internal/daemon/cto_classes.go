package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/mcpserver"
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
		ctoRead:     "overview sessions session proposals feed graph machines workers tools settings projects messages cto_proposals_find agent_sessions_find agent_session_get agent_machines_find agent_machine_get agent_projects_find agent_workers_find agent_messages_find agent_message_get agent_graph_read agent_mcp_servers_find agent_worker_profiles_find",
		ctoTalk:     "message ask_owner learn ui cto_ui_show agent_message_send agent_message_reply agent_owner_ask agent_learn",
		ctoProposal: "graph_change session_change worker_change answer tools_change mcp_change mcp_call settings_change project_change cto_new",
	} {
		for _, name := range strings.Fields(names) {
			out[name] = class
		}
	}
	return out
}()

// ctoActionField names the routing field of each merged action tool.
var ctoActionField = map[string]string{
	"message":         "action",
	"ui":              "action",
	"graph_change":    "action",
	"session_change":  "action",
	"worker_change":   "action",
	"answer":          "kind",
	"tools_change":    "action",
	"mcp_change":      "action",
	"settings_change": "area",
	"project_change":  "action",
}

// ctoChangeActions maps a merged action to the concrete daemon call that runs
// it. The concrete calls keep their own schemas, authority checks and audit.
var ctoChangeActions = map[string]map[string]string{
	"graph_change": {
		"charter_set": "charter_set", "node_add": "node_add", "node_remove": "node_remove",
		"edge_set": "edge_set", "edge_remove": "edge_remove", "settings_set": "node_settings_set",
		"undo": "graph_undo",
	},
	"session_change": {"rename": "session_rename", "trust": "session_trust", "policy": "session_policy", "session_start": "session_start"},
	"worker_change":  {"start": "worker_start", "stop": "worker_stop"},
	"answer":         {"held": "held_answer", "inbox": "inbox_answer", "outcome": "outcome_overrule"},
	"tools_change": {
		"plan": "tools_plan", "apply": "tools_apply", "undo": "tools_undo",
		"import": "mcp_import", "native_remove": "mcp_native_remove",
	},
	"mcp_change": {"add": "mcp_add", "remove": "mcp_remove", "test": "mcp_test", "enable": "mcp_enable"},
	"settings_change": {
		"worker_profile_add": "worker_profile_add", "worker_profile_remove": "worker_profile_remove",
		"worker_providers": "worker_providers_set", "protected_providers": "protected_providers_set",
		"score": "score_set", "integrations": "integrations_set", "notifications": "notification_settings",
		"cto": "cto_settings", "playbook": "playbook_set", "machine_name": "node_rename",
		"invite": "invite", "onboarding": "onboarding_restart",
	},
	"project_change": {"add": "project_add", "remove": "project_remove", "display": "project_display_set", "create": "project_create"},
}

var ctoActions = map[string]map[string][]string{
	"message":         {"action": {"send", "reply"}},
	"ui":              {"action": {"show", "notice"}},
	"graph_change":    {"action": {"charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "settings_set", "undo"}},
	"session_change":  {"action": {"rename", "trust", "policy", "session_start"}},
	"worker_change":   {"action": {"start", "stop"}},
	"answer":          {"kind": {"held", "inbox", "outcome"}, "action": {"a", "A", "r", "R", "let_stand", "overrule"}},
	"tools_change":    {"action": {"plan", "apply", "undo", "import", "native_remove"}, "operation": {"repo_remove"}},
	"mcp_change":      {"action": {"add", "remove", "test", "enable"}},
	"settings_change": {"area": {"worker_profile_add", "worker_profile_remove", "worker_providers", "protected_providers", "score", "integrations", "notifications", "cto", "playbook", "machine_name", "invite", "onboarding"}},
	"project_change":  {"action": {"add", "remove", "display", "create"}},
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

	field := ctoActionField[name]
	if field != "" {
		value, exists := fields[field]
		if !exists {
			return "", fmt.Errorf("%s needs %s", name, field)
		}
		var action string
		if json.Unmarshal(value, &action) != nil {
			return "", errors.New("bad CTO action")
		}
		actions, ok := ctoActions[name][field]
		if !ok {
			return "", fmt.Errorf("unknown %s %s %q", name, field, action)
		}
		found := false
		for _, a := range actions {
			found = found || a == action
		}
		if !found {
			return "", fmt.Errorf("unknown %s %s %q", name, field, action)
		}
	}
	for f, actions := range ctoActions[name] {
		if f == field {
			continue
		}
		value, exists := fields[f]
		if !exists {
			continue
		}
		var action string
		if json.Unmarshal(value, &action) != nil {
			return "", errors.New("bad CTO action")
		}
		found := false
		for _, a := range actions {
			found = found || a == action
		}
		if !found {
			return "", fmt.Errorf("unknown %s %s %q", name, f, action)
		}
	}
	if name == "answer" {
		var kind, action string
		_ = json.Unmarshal(fields["kind"], &kind)
		_ = json.Unmarshal(fields["action"], &action)
		switch kind {
		case "held":
			if action != "a" && action != "A" && action != "r" && action != "R" {
				return "", fmt.Errorf("unknown answer held action %q", action)
			}
		case "outcome":
			if action != "let_stand" && action != "overrule" {
				return "", fmt.Errorf("unknown answer outcome action %q", action)
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

// ctoConcreteCall turns a merged action call into the concrete daemon call.
func ctoConcreteCall(name string, raw json.RawMessage) (string, json.RawMessage, string, error) {
	field := ctoActionField[name]
	if field == "" {
		if _, ok := ctoChangeActions[name]; ok {
			return "", nil, "", fmt.Errorf("unknown CTO tool %q", name)
		}
		if _, ok := ctoToolClasses[name]; ok || mcpserver.IsCTOConcreteTool(name) {
			return name, raw, "", nil
		}
		return "", nil, "", fmt.Errorf("unknown CTO tool %q", name)
	}
	var fields map[string]json.RawMessage
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &fields) != nil {
		return "", nil, "", errors.New("bad CTO tool arguments")
	}
	var action string
	if err := json.Unmarshal(fields[field], &action); err != nil || action == "" {
		return "", nil, "", fmt.Errorf("%s needs %s", name, field)
	}
	actions, ok := ctoChangeActions[name]
	if !ok {
		return name, raw, action, nil
	}
	concrete, ok := actions[action]
	if !ok {
		return "", nil, "", fmt.Errorf("unknown %s %q", field, action)
	}
	delete(fields, field)
	args, err := json.Marshal(fields)
	if err != nil {
		return "", nil, "", err
	}
	return concrete, args, action, nil
}

func routeCTOTool(ctx context.Context, name string, raw json.RawMessage, propose func(context.Context, string, string, json.RawMessage, string) (string, error), run func(context.Context, string, json.RawMessage) (any, error)) (any, error) {
	class, err := ctoClass(name, raw)
	if err != nil {
		return nil, err
	}
	if class != ctoProposal {
		return run(ctx, name, raw)
	}
	concrete, _, action, err := ctoConcreteCall(name, raw)
	if err != nil {
		return nil, err
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
		if concrete != "protected_providers_set" {
			delete(fields, "reason")
		}
	}
	args, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	id, err := propose(ctx, name, action, args, reason)
	if err != nil {
		return nil, err
	}
	return ProposalToolResult(id), nil
}

// Graph calls default to proposals; the daemon policy may apply a minor
// change or use an owner window before falling back to the proposal route.
func isCTOGraphTool(name string) bool {
	return name == "graph_change"
}
