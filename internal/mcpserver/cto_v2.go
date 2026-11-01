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
	"cto_ui_show": true, "cto_proposals_find": true,
}

// ValidateCTOActionSchema validates the selected concrete action before it is
// sent to the daemon. Semantic and state validation remain daemon owned.
func ValidateCTOActionSchema(tool string, raw json.RawMessage) error {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return fmt.Errorf("arguments must be an object; example: %s", actionExample(tool))
	}
	action, _ := args["action"].(string)
	if action == "" {
		return fmt.Errorf("action is required; example: %s", actionExample(tool))
	}
	concrete := map[string]string{
		"graph_change":   map[string]string{"charter_set": "charter_set", "node_add": "node_add", "node_remove": "node_remove", "edge_set": "edge_set", "edge_remove": "edge_remove", "settings_set": "node_settings_set", "undo": "graph_undo"}[action],
		"session_change": map[string]string{"rename": "session_rename", "trust": "session_trust", "policy": "session_policy", "session_start": "session_start"}[action],
		"worker_change":  map[string]string{"start": "worker_start", "stop": "worker_stop"}[action],
	}[tool]
	if concrete == "" {
		return fmt.Errorf("unknown %s action %q; example: %s", tool, action, actionExample(tool))
	}
	fields := ctoConcreteTools[concrete].fields
	allowed := map[string]bool{"action": true}
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
		if k != "action" && k != "reason" {
			filtered[k] = v
		}
	}
	b, _ := json.Marshal(filtered)
	if err := CTOArguments(concrete, b); err != nil {
		return fmt.Errorf("invalid %s action %q: %w; example: %s", tool, action, err, actionExample(tool))
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
		return `{"action":"start","project":"example","task":"Describe the work","touches":["docs"]}`
	default:
		return `{"action":"show"}`
	}
}

func addCTOV2Tools(s *mcp.Server, call Caller) {
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if !ctoV2Tools[entry.Name] {
			continue
		}
		var input, output any
		_ = json.Unmarshal(entry.InputSchema, &input)
		_ = json.Unmarshal(entry.OutputSchema, &output)
		tool := &mcp.Tool{Name: entry.Name, Description: entry.Description, InputSchema: input, OutputSchema: output}
		name := entry.Name
		s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.Params.Arguments
			method := name
			if name == "graph_change" || name == "session_change" || name == "worker_change" {
				if err := ValidateCTOActionSchema(name, args); err != nil {
					return v2Error(err), nil
				}
			} else if name == "cto_ui_show" {
				if err := validateCTOCatalogInput(entry.InputSchema, args); err != nil {
					return v2Error(err), nil
				}
				var q map[string]any
				if json.Unmarshal(args, &q) != nil {
					return v2Error(fmt.Errorf("arguments must be an object")), nil
				}
				if notice := stringValue(q["notice"]); notice != "" {
					method = "ui_notice"
					args, _ = json.Marshal(map[string]any{"text": notice})
				} else {
					method = "ui_show"
				}
			} else if name == "cto_proposals_find" {
				if err := validateCTOCatalogInput(entry.InputSchema, args); err != nil {
					return v2Error(err), nil
				}
				var q map[string]any
				if json.Unmarshal(args, &q) != nil {
					return v2Error(fmt.Errorf("arguments must be an object")), nil
				}
				legacy := map[string]any{"id": q["id"], "limit": q["limit"]}
				if cursor := stringValue(q["cursor"]); cursor != "" {
					off, err := strconv.Atoi(cursor)
					if err != nil || off < 0 {
						return v2Error(fmt.Errorf("cursor must be a nonnegative offset; example: {\"cursor\":\"25\"}")), nil
					}
					legacy["offset"] = off
				}
				args, _ = json.Marshal(legacy)
				method = "proposals"
			}
			raw, err := call(ctx, method, args)
			if err != nil {
				return v2Error(err), nil
			}
			var value any
			if json.Unmarshal(raw, &value) != nil {
				value = string(raw)
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
			encoded, _ := json.Marshal(value)
			return &mcp.CallToolResult{StructuredContent: value, Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
		})
	}
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
