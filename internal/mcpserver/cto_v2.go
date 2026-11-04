package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

var ctoV2Tools = map[string]bool{
	"graph_change": true, "session_change": true, "worker_change": true,
	"cto_ui_show": true, "cto_placement_memory": true, "cto_proposals_find": true, "proposal_ask": true,
	"answer": true, "tools_change": true, "mcp_change": true,
	"settings_change": true, "project_change": true, "mcp_call": true, "cto_new": true,
}

var ctoActionRoutes = map[string]map[string]string{
	"answer":          {"held": "held_answer", "inbox": "inbox_answer", "outcome": "outcome_overrule"},
	"tools_change":    {"toggle": "tools_toggle", "new_projects": "tools_new_projects", "plan": "tools_plan", "apply": "tools_apply", "undo": "tools_undo", "import": "mcp_import", "native_remove": "mcp_native_remove"},
	"mcp_change":      {"add": "mcp_add", "remove": "mcp_remove", "test": "mcp_test", "enable": "mcp_enable"},
	"settings_change": {"values_write": "settings_values_write", "values_undo": "settings_values_undo", "worker_profile_add": "worker_profile_add", "worker_profile_remove": "worker_profile_remove", "worker_providers": "worker_providers_set", "protected_providers": "protected_providers_set", "score": "score_set", "integrations": "integrations_set", "notifications": "notification_settings", "cto": "cto_settings", "playbook": "playbook_set", "machine_name": "node_rename", "machine_remove": "machine_remove", "machine_rotate": "machine_rotate", "invite": "invite", "onboarding": "onboarding_restart"},
	"project_change":  {"add": "project_add", "remove": "project_remove", "display": "project_display_set", "create": "project_create"},
}

// ValidateCTOActionSchema validates the selected concrete action before it is
// sent to the daemon. Semantic and state validation remain daemon owned.
func ValidateCTOActionSchema(tool string, raw json.RawMessage) error {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return fmt.Errorf("arguments must be an object; example: %s", actionExample(tool))
	}
	selector := "action"
	if tool == "answer" {
		selector = "kind"
	}
	if tool == "settings_change" {
		selector = "area"
	}
	action, _ := args[selector].(string)
	if action == "" {
		return fmt.Errorf("%s is required; example: %s", selector, actionExample(tool))
	}
	concrete := ctoActionRoutes[tool][action]
	if concrete == "" {
		concrete = map[string]string{
			"graph_change":   map[string]string{"charter_set": "charter_set", "node_add": "node_add", "node_remove": "node_remove", "edge_set": "edge_set", "edge_remove": "edge_remove", "settings_set": "node_settings_set", "undo": "graph_undo"}[action],
			"session_change": map[string]string{"rename": "session_rename", "trust": "session_trust", "policy": "session_policy", "session_start": "session_start"}[action],
			"worker_change":  map[string]string{"start": "worker_start", "stop": "worker_stop"}[action],
		}[tool]
	}
	if concrete == "" {
		return fmt.Errorf("unknown %s action %q; example: %s", tool, action, actionExample(tool))
	}
	fields := ctoConcreteTools[concrete].fields
	allowed := map[string]bool{selector: true}
	for name := range fields {
		allowed[name] = true
	}
	if tool != "graph_change" {
		allowed["reason"] = true
	}
	for name := range args {
		if !allowed[name] {
			return fmt.Errorf("field %q is not valid for %s action %q; example: %s", name, tool, action, actionExample(tool))
		}
	}
	filtered := map[string]any{}
	for k, v := range args {
		if k != selector && k != "reason" {
			filtered[k] = v
		}
	}
	b, _ := json.Marshal(filtered)
	if err := CTOArguments(concrete, b); err != nil {
		return fmt.Errorf("invalid %s action %q: %w; example: %s", tool, action, err, actionExample(tool))
	}
	if err := validateCTOActionRequired(tool, action, args); err != nil {
		return err
	}
	if tool == "session_change" && action == "session_start" {
		if strings.TrimSpace(stringValue(args["project"])) == "" && strings.TrimSpace(stringValue(args["path"])) == "" {
			return fmt.Errorf("session_start needs a nonempty project or path; example: %s", actionExample(tool))
		}
	}
	if tool == "worker_change" && action == "start" {
		continuing := strings.TrimSpace(stringValue(args["continue"])) != ""
		if continuing && (strings.TrimSpace(stringValue(args["machine"])) != "" || strings.TrimSpace(stringValue(args["base"])) != "" || strings.TrimSpace(stringValue(args["isolation"])) != "") {
			return fmt.Errorf("worker continuation cannot set machine, base or isolation; example: %s", actionExample(tool))
		}
		if !continuing && (strings.TrimSpace(stringValue(args["project"])) == "" || strings.TrimSpace(stringValue(args["task"])) == "") {
			return fmt.Errorf("worker start needs project and task; example: %s", actionExample(tool))
		}
	}
	return nil
}

func stringValue(v any) string { s, _ := v.(string); return s }

func actionExample(tool string) string {
	switch tool {
	case "graph_change":
		return `{"action":"node_add","name":"example"}`
	case "session_change":
		return `{"action":"session_start","project":"example"}`
	case "worker_change":
		return `{"action":"start","project":"example","task":"Describe the work"}`
	case "answer":
		return `{"kind":"held","message_id":"message-id","action":"a"}`
	case "tools_change":
		return `{"action":"apply","plan_id":"plan-id"}`
	case "mcp_change":
		return `{"action":"add","server":{"name":"example","transport":"stdio","command":"/path/to/server"}}`
	case "settings_change":
		return `{"area":"notifications","disabled":true}`
	case "project_change":
		return `{"action":"create","path":"~/project"}`
	default:
		return `{"action":"show"}`
	}
}

func validateCTOActionRequired(tool, action string, args map[string]any) error {
	required := []string{}
	example := actionExampleFor(tool, action)
	switch tool {
	case "answer":
		switch action {
		case "held":
			required = []string{"message_id", "action"}
		case "inbox":
			required = []string{"id", "answer"}
		case "outcome":
			required = []string{"reply_id", "action"}
		}
	case "tools_change":
		switch action {
		case "toggle":
			required = []string{"item", "project", "on"}
		case "new_projects":
			required = []string{"item", "on"}
		case "apply":
			required = []string{"plan_id"}
		case "undo":
			required = []string{"item"}
		case "import":
			n := 0
			for _, k := range []string{"candidate", "plan_id"} {
				if nonempty(args[k]) {
					n++
				}
			}
			if n != 1 {
				return fmt.Errorf("import needs exactly one of candidate or plan_id; example: %s", example)
			}
		case "native_remove":
			n := 0
			for _, k := range []string{"candidate", "plan_id", "undo_plan_id"} {
				if nonempty(args[k]) {
					n++
				}
			}
			if n != 1 {
				return fmt.Errorf("native_remove needs exactly one selector; example: %s", example)
			}
		}
	case "mcp_change":
		switch action {
		case "add":
			required = []string{"server"}
		case "remove", "test":
			required = []string{"name"}
		case "enable":
			required = []string{"name", "enabled"}
		}
	case "settings_change":
		if action == "values_write" {
			required = []string{"changes"}
		}
		if action == "values_undo" {
			required = []string{"revision"}
		}
		if action == "notifications" {
			required = []string{"disabled"}
		}
		if action == "score" {
			required = []string{"model"}
		}
	case "project_change":
		if action == "create" {
			required = []string{"path"}
		}
	}
	for _, key := range required {
		if _, ok := args[key]; !ok || args[key] == nil {
			return fmt.Errorf("%s requires %s; example: %s", action, key, example)
		}
	}
	return nil
}

func actionExampleFor(tool, action string) string {
	switch tool {
	case "answer":
		switch action {
		case "inbox":
			return `{"kind":"inbox","id":"inbox-id","answer":"approve"}`
		case "outcome":
			return `{"kind":"outcome","reply_id":"message-id","action":"let_stand"}`
		}
	case "tools_change":
		switch action {
		case "apply":
			return `{"action":"apply","plan_id":"plan-id"}`
		case "undo":
			return `{"action":"undo","item":"item-id"}`
		case "import":
			return `{"action":"import","candidate":"candidate-id"}`
		case "native_remove":
			return `{"action":"native_remove","candidate":"candidate-id"}`
		}
	case "mcp_change":
		switch action {
		case "remove", "test":
			return `{"action":"` + action + `","name":"server-name"}`
		case "enable":
			return `{"action":"enable","name":"server-name","enabled":true}`
		}
	}
	return actionExample(tool)
}

func nonempty(v any) bool { s, ok := v.(string); return ok && strings.TrimSpace(s) != "" }

func addCTOV2Tools(s *mcp.Server, call Caller, addTool ctoToolAdder) {
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if !ctoV2Tools[entry.Name] {
			continue
		}
		var input any
		_ = json.Unmarshal(entry.InputSchema, &input)
		tool := &mcp.Tool{Name: entry.Name, Description: entry.Description, InputSchema: input}
		name := entry.Name
		addTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.Params.Arguments
			if name == "graph_change" || name == "session_change" || name == "worker_change" || name == "answer" || name == "tools_change" || name == "mcp_change" || name == "settings_change" || name == "project_change" {
				if err := ValidateCTOActionSchema(name, args); err != nil {
					return v2Error(err), nil
				}
			} else if name == "mcp_call" || name == "cto_new" {
				if err := validateCTOCatalogInput(entry.InputSchema, args); err != nil {
					return v2Error(fmt.Errorf("invalid arguments: %w; example: %s", err, `{"server":"example","tool":"list","arguments":{}}`)), nil
				}
			} else if name == "proposal_ask" {
				if err := validateCTOCatalogInput(entry.InputSchema, args); err != nil {
					return v2Error(fmt.Errorf("%w; example: %s", err, `{"id":"p_1"}`)), nil
				}
				var q map[string]any
				_ = json.Unmarshal(args, &q)
				if strings.TrimSpace(stringValue(q["id"])) == "" {
					return v2Error(fmt.Errorf("id is required; example: %s", `{"id":"p_1"}`)), nil
				}
			} else if name == "cto_ui_show" || name == "cto_placement_memory" {
				if err := validateCTOCatalogInput(entry.InputSchema, args); err != nil {
					return v2Error(err), nil
				}
			} else if name == "cto_proposals_find" {
				if err := validateCTOCatalogInput(entry.InputSchema, args); err != nil {
					return v2Error(err), nil
				}
				var q map[string]any
				if json.Unmarshal(args, &q) != nil {
					return v2Error(fmt.Errorf("arguments must be an object")), nil
				}
				// The daemon pages proposals by offset; the cursor is that offset.
				legacy := map[string]any{"id": q["id"], "limit": q["limit"]}
				if cursor := stringValue(q["cursor"]); cursor != "" {
					off, err := strconv.Atoi(cursor)
					if err != nil || off < 0 {
						return v2Error(fmt.Errorf("cursor must be a nonnegative offset; example: {\"cursor\":\"25\"}")), nil
					}
					legacy["offset"] = off
				}
				args, _ = json.Marshal(legacy)
			}
			raw, err := call(ctx, name, args)
			if err != nil {
				return v2Error(err), nil
			}
			var value any
			if json.Unmarshal(raw, &value) != nil {
				value = string(raw)
			}
			if name == "answer" || name == "tools_change" || name == "mcp_change" || name == "settings_change" || name == "project_change" || name == "mcp_call" || name == "proposal_ask" || name == "cto_new" || name == "graph_change" || name == "session_change" || name == "worker_change" {
				value = normalizeCTOProposalResult(value)
			}
			if name == "cto_ui_show" {
				shown := true
				if message, ok := value.(string); ok && message == "no TUI open" {
					shown = false
				}
				value = map[string]any{"shown": shown}
			}
			if name == "cto_proposals_find" {
				value = normalizeProposalOutput(value)
			}
			return catalogResult(name, value)
		})
	}
}

func normalizeCTOProposalResult(value any) any {
	if m, ok := value.(map[string]any); ok {
		if _, hasState := m["state"]; hasState {
			return m
		}
	}
	result := map[string]any{"state": "completed"}
	if s, ok := value.(string); ok {
		result["result"] = map[string]any{"summary": s, "data": s}
		if strings.HasPrefix(s, "proposal ") {
			fields := strings.Fields(s)
			if len(fields) > 1 {
				result["proposal_id"] = fields[1]
			}
			result["state"] = "proposed"
		}
	} else {
		result["result"] = map[string]any{"summary": "Action completed.", "data": value}
	}
	return result
}

func validateCTOCatalogInput(raw, args json.RawMessage) error {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return err
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return fmt.Errorf("arguments must be an object")
	}
	if err := resolved.Validate(value); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func v2Error(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

func normalizeProposalOutput(value any) any {
	m, _ := value.(map[string]any)
	if m == nil {
		return map[string]any{"items": []any{}, "total": 0}
	}
	if id, ok := m["id"].(string); ok {
		item := map[string]any{"id": id, "tool": m["tool"], "status": m["status"], "reason": m["reason"], "created": m["created_at"]}
		if action, ok := m["action"]; ok {
			item["action"] = action
		}
		return map[string]any{"items": []any{item}, "total": 1, "note": m["note"]}
	}
	if items, ok := m["items"].([]any); ok {
		for _, item := range items {
			row, _ := item.(map[string]any)
			if row != nil {
				if _, ok := row["created"]; !ok {
					row["created"] = ""
				}
			}
		}
		result := map[string]any{"items": items, "total": m["total"]}
		if next, ok := m["next"].(float64); ok {
			result["next_cursor"] = strconv.Itoa(int(next))
		}
		if note, ok := m["note"]; ok {
			result["note"] = note
		}
		return result
	}
	return map[string]any{"items": []any{}, "total": 0}
}
