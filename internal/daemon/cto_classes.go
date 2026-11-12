package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

type ctoToolClass string

const (
	ctoRead     ctoToolClass = "read"
	ctoTalk     ctoToolClass = "talk"
	ctoMinor    ctoToolClass = "minor"
	ctoProposal ctoToolClass = "proposal"
)

// Every CTO entry point is listed here. Unknown tools and actions fail closed.
// Reads must have an ordinary-agent source or be the CTO's own timeline.
// message is a legacy stored call for an as-owner send or reply.
var ctoToolClasses = func() map[string]ctoToolClass {
	out := map[string]ctoToolClass{}
	for class, names := range map[ctoToolClass]string{
		ctoRead:  "cto_proposals_find cto_intake_find cto_ops_list agent_sessions_find agent_session_get agent_machines_find agent_machine_get agent_machine_log agent_projects_find agent_workers_find agent_messages_find agent_message_get agent_graph_read agent_settings_read agent_mcp_servers_find agent_worker_profiles_find agent_owner_judgments_find projects_layout",
		ctoTalk:  "message cto_placement_memory cto_standing_orders cto_ui_show proposal_ask agent_message_send agent_message_reply agent_message_withdraw agent_owner_ask agent_owner_judgment_record agent_learn agent_permission_propose",
		ctoMinor: "graph_change session_change worker_change answer tools_change mcp_change mcp_call settings_change project_change cto_new sessions_archive judgment_add",
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
		"undo": "graph_undo", "repo_create": "repo_create", "repo_archive": "repo_archive", "agent_create": "agent_create", "agent_tombstone": "agent_tombstone",
		"lead_set": "lead_set", "team_create": "team_create", "team_rename": "team_rename", "team_purpose": "team_purpose", "team_charter": "team_charter", "team_archive": "team_archive", "team_member_add": "team_member_add", "team_member_remove": "team_member_remove",
	},
	"session_change": {"rename": "session_rename", "replace": "session_replace", "trust": "session_trust", "mcp_servers": "folder_mcp", "policy": "session_policy", "session_start": "session_start", "merger_start": "merger_start", "merger_stop": "merger_stop", "stop": "session_stop", "resume": "session_resume", "restart": "session_restart"},
	"worker_change":  {"start": "worker_start", "stop": "worker_stop"},
	"answer":         {"held": "held_answer", "inbox": "inbox_answer", "outcome": "outcome_overrule"},
	"tools_change": {
		"toggle": "tools_toggle", "new_projects": "tools_new_projects", "plan": "tools_plan", "apply": "tools_apply", "undo": "tools_undo",
		"import": "mcp_import", "native_remove": "mcp_native_remove",
	},
	"mcp_change": {"add": "mcp_add", "remove": "mcp_remove", "test": "mcp_test", "enable": "mcp_enable", "app_ask": "apps.ask", "apps_list": "apps.list", "app_grant": "apps.grant", "app_ungrant": "apps.ungrant", "app_rename": "apps.rename", "app_revoke": "apps.revoke", "public_status": "apps.public.status", "public_on": "apps.public.on", "public_off": "apps.public.off"},
	"settings_change": {
		"values_write": "settings_values_write", "values_undo": "settings_values_undo", "operator": "owner_operation",
		"worker_profile_add": "worker_profile_add", "worker_profile_remove": "worker_profile_remove",
		"worker_providers": "worker_providers_set", "protected_providers": "protected_providers_set",
		"score": "score_set", "integrations": "integrations_set", "notifications": "notification_settings",
		"cto": "cto_settings", "playbook": "playbook_set", "machine_name": "node_rename", "machine_remove": "machine_remove", "machine_rotate": "machine_rotate",
		"invite": "invite", "onboarding": "onboarding_restart",
		"process_stop": "process_stop",
	},
	"project_change": {"add": "project_add", "remove": "project_remove", "display": "project_display_set", "create": "project_create"},
}

// Each routed mutation has an explicit authority class. Adding an action to
// the tool schema without classifying it here fails closed.
var ctoActionClasses = map[string]map[string]ctoToolClass{
	"graph_change": {
		"charter_set": ctoMinor, "node_add": ctoMinor, "node_remove": ctoProposal,
		"edge_set": ctoMinor, "edge_remove": ctoMinor, "settings_set": ctoMinor,
		"undo": ctoMinor, "repo_create": ctoMinor, "repo_archive": ctoProposal,
		"agent_create": ctoMinor, "agent_tombstone": ctoProposal, "lead_set": ctoMinor,
		"team_create": ctoMinor, "team_rename": ctoMinor, "team_purpose": ctoMinor, "team_charter": ctoMinor, "team_archive": ctoMinor,
		"team_member_add": ctoMinor, "team_member_remove": ctoMinor,
	},
	"session_change":  {"rename": ctoMinor, "replace": ctoMinor, "trust": ctoMinor, "mcp_servers": ctoMinor, "policy": ctoMinor, "session_start": ctoMinor, "merger_start": ctoMinor, "merger_stop": ctoMinor, "stop": ctoMinor, "resume": ctoMinor, "restart": ctoMinor},
	"worker_change":   {"start": ctoMinor, "stop": ctoMinor},
	"answer":          {"held": ctoMinor, "inbox": ctoMinor, "outcome": ctoMinor},
	"tools_change":    {"toggle": ctoMinor, "new_projects": ctoMinor, "plan": ctoMinor, "apply": ctoMinor, "undo": ctoMinor, "import": ctoMinor, "native_remove": ctoMinor},
	"mcp_change":      {"add": ctoMinor, "remove": ctoMinor, "test": ctoMinor, "enable": ctoMinor, "app_ask": ctoMinor, "apps_list": ctoRead, "app_grant": ctoMinor, "app_ungrant": ctoMinor, "app_rename": ctoMinor, "app_revoke": ctoMinor, "public_status": ctoRead, "public_on": ctoMinor, "public_off": ctoMinor},
	"settings_change": {"operator": ctoMinor, "values_write": ctoMinor, "values_undo": ctoMinor, "worker_profile_add": ctoMinor, "worker_profile_remove": ctoMinor, "worker_providers": ctoMinor, "protected_providers": ctoMinor, "score": ctoMinor, "integrations": ctoMinor, "notifications": ctoMinor, "cto": ctoMinor, "playbook": ctoMinor, "machine_name": ctoMinor, "machine_remove": ctoProposal, "machine_rotate": ctoProposal, "invite": ctoMinor, "onboarding": ctoMinor, "process_stop": ctoMinor},
	"project_change":  {"add": ctoMinor, "remove": ctoMinor, "display": ctoMinor, "create": ctoMinor},
}

var ctoActions = map[string]map[string][]string{
	"message":         {"action": {"send", "reply"}},
	"graph_change":    {"action": {"charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "settings_set", "undo", "repo_create", "repo_archive", "agent_create", "agent_tombstone", "team_create", "team_rename", "team_purpose", "team_charter", "team_archive", "team_member_add", "team_member_remove", "lead_set"}},
	"session_change":  {"action": {"rename", "replace", "trust", "mcp_servers", "policy", "session_start", "merger_start", "merger_stop", "stop", "resume", "restart"}},
	"worker_change":   {"action": {"start", "stop"}},
	"answer":          {"kind": {"held", "inbox", "outcome"}, "action": {"a", "A", "r", "R", "let_stand", "overrule"}},
	"tools_change":    {"action": {"toggle", "new_projects", "plan", "apply", "undo", "import", "native_remove"}, "operation": {"repo_remove"}},
	"mcp_change":      {"action": {"add", "remove", "test", "enable", "app_ask", "apps_list", "app_grant", "app_ungrant", "app_rename", "app_revoke", "public_status", "public_on", "public_off"}},
	"settings_change": {"area": {"operator", "values_write", "values_undo", "worker_profile_add", "worker_profile_remove", "worker_providers", "protected_providers", "score", "integrations", "notifications", "cto", "playbook", "machine_name", "machine_remove", "machine_rotate", "invite", "onboarding", "process_stop"}},
	"project_change":  {"action": {"add", "remove", "display", "create"}},
}

func ctoClass(name string, raw json.RawMessage) (ctoToolClass, error) {
	if name == "cto_op" {
		_, tool, args, err := ctoOpCall(raw)
		if err != nil {
			return "", err
		}
		return ctoClass(tool, args)
	}
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
	}
	if _, routed := ctoChangeActions[name]; routed {
		if _, classified := ctoActionClasses[name]; !classified {
			return "", fmt.Errorf("unclassified CTO actions for %q", name)
		}
	}
	if actions, ok := ctoActionClasses[name]; ok {
		var action string
		_ = json.Unmarshal(fields[field], &action)
		class, ok = actions[action]
		if !ok {
			return "", fmt.Errorf("unclassified %s %s %q", name, field, action)
		}
	}
	if name == "settings_change" && string(fields["area"]) == `"operator"` {
		var method string
		if json.Unmarshal(fields["method"], &method) != nil {
			return "", errors.New("operator needs method")
		}
		read, ok := protocol.CTOOperatorMethods()[method]
		if !ok {
			return "", fmt.Errorf("unclassified owner operation %q", method)
		}
		if read {
			class = ctoRead
		} else {
			class = ctoMinor
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
	if concrete == "node_settings_set" {
		var ref struct {
			Kind string `json:"kind"`
		}
		if value, ok := fields["ref"]; ok && json.Unmarshal(value, &ref) == nil && ref.Kind == "team" {
			concrete = "team_settings_set"
		}
	}
	if name == "session_change" && action == "trust" && fields["session_id"] == nil && (fields["project"] != nil || fields["path"] != nil) {
		// A folder, not a session: Claude Code trust for a project or path.
		concrete = "folder_trust"
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
		if concrete != "protected_providers_set" && concrete != "settings_values_write" {
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

// isCTOGraphTool identifies the merged graph entry point.
func isCTOGraphTool(name string) bool {
	return name == "graph_change"
}
