package mcpserver

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoOperatorRequests maps an operator-backed concrete CTO tool to the request
// struct its arguments become. The gateway decode is strict, so every field a
// tool advertises must exist in that struct.
var ctoOperatorRequests = map[string]func() any{
	"charter_set":             func() any { return &protocol.CharterSetRequest{} },
	"lead_set":                func() any { return &protocol.LeadSetRequest{} },
	"node_add":                func() any { return &protocol.GraphNodeRequest{} },
	"node_remove":             func() any { return &protocol.GraphNodeRequest{} },
	"edge_set":                func() any { return &protocol.Edge{} },
	"edge_remove":             func() any { return &protocol.Edge{} },
	"graph_undo":              func() any { return &protocol.GraphUndoRequest{} },
	"node_settings_set":       func() any { return &protocol.NodeSettingsSetRequest{} },
	"repo_create":             func() any { return &protocol.GraphLifecycleRequest{} },
	"repo_archive":            func() any { return &protocol.GraphLifecycleRequest{} },
	"agent_create":            func() any { return &protocol.GraphLifecycleRequest{} },
	"agent_tombstone":         func() any { return &protocol.GraphLifecycleRequest{} },
	"node_rename":             func() any { return &protocol.NodeRenameRequest{} },
	"session_rename":          func() any { return &protocol.SessionRenameRequest{} },
	"folder_mcp":              func() any { return &protocol.FolderMCPRequest{} },
	"session_policy":          func() any { return &protocol.SessionPolicyRequest{} },
	"process_stop":            func() any { return &protocol.ProcessStopRequest{} },
	"worker_start":            func() any { return &protocol.WorkerStartRequest{} },
	"worker_stop":             func() any { return &protocol.WorkerStopRequest{} },
	"held_answer":             func() any { return &protocol.HeldAnswer{} },
	"outcome_overrule":        func() any { return &protocol.OutcomeResolution{} },
	"settings_values_write":   func() any { return &protocol.SettingsWrite{} },
	"settings_values_undo":    func() any { return &protocol.SettingsUndo{} },
	"score_set":               func() any { return &protocol.ScoreSetRequest{} },
	"playbook_set":            func() any { return &protocol.PlaybookSetRequest{} },
	"protected_providers_set": func() any { return &protocol.ProtectedProviders{} },
	"worker_providers_set":    func() any { return &protocol.WorkerProviders{} },
	"worker_profile_add":      func() any { return &protocol.WorkerProfileRequest{} },
	"worker_profile_remove":   func() any { return &protocol.WorkerProfileRequest{} },
	"mcp_add":                 func() any { return &protocol.MCPAdd{} },
	"mcp_remove":              func() any { return &protocol.MCPName{} },
	"mcp_test":                func() any { return &protocol.MCPName{} },
	"mcp_enable":              func() any { return &protocol.MCPSetEnabled{} },
	"mcp_import":              func() any { return &protocol.MCPImportRequest{} },
	"mcp_native_remove":       func() any { return &protocol.MCPNativeRemoveRequest{} },
	"tools_toggle":            func() any { return &protocol.ToolToggleRequest{} },
	"tools_new_projects":      func() any { return &protocol.ToolNewProjectsRequest{} },
	"project_display_set":     func() any { return &protocol.ProjectDisplayRequest{} },
	"session_stop":            func() any { return &protocol.SessionResumeRequest{} },
	"session_resume":          func() any { return &protocol.SessionResumeRequest{} },
	"session_restart":         func() any { return &protocol.SessionResumeRequest{} },
}

// ctoOperatorTranslatedTools are operator-backed tools whose schema fields are
// not the request struct's fields: the daemon rewrites them first (a team name
// into a TeamWrite ID, trust into trust_claude, and so on). Their own routing
// test covers the rewrite.
var ctoOperatorTranslatedTools = map[string]string{
	"team_create":           "resolved into TeamWrite",
	"team_rename":           "resolved into TeamWrite",
	"team_purpose":          "resolved into TeamWrite",
	"team_charter":          "resolved into TeamWrite",
	"team_archive":          "resolved into TeamWrite",
	"team_member_add":       "resolved into TeamWrite",
	"team_member_remove":    "resolved into TeamWrite",
	"team_settings_set":     "resolved into TeamWrite",
	"session_start":         "trust becomes trust_claude",
	"session_trust":         "a project or path trust goes through folder_trust",
	"project_add":           "paths, dir, scan and depth are daemon project arguments",
	"project_remove":        "node_id becomes on",
	"project_create":        "starts a session through the project route",
	"sessions_archive":      "preview, restore and filters are the operator request",
	"machine_remove":        "handled by the machine action",
	"machine_rotate":        "handled by the machine action",
	"merger_start":          "starts a merger session",
	"merger_stop":           "stops a merger session",
	"owner_operation":       "arguments are checked per method by the operator decode",
	"tools_plan":            "plans a tool assignment",
	"tools_apply":           "applies a tool plan",
	"tools_undo":            "undoes a tool assignment",
	"integrations_set":      "owner settings transport",
	"notification_settings": "owner preference transport",
	"cto_settings":          "owner settings transport",
	"apps.ask":              "app consent transport",
	"apps.grant":            "app consent transport",
	"apps.ungrant":          "app consent transport",
	"apps.rename":           "app consent transport",
	"apps.revoke":           "app consent transport",
	"invite":                "prints an install line",
	"onboarding_restart":    "resets onboarding",
	"cto_send":              "queues a CTO instruction",
	"cto_new":               "starts a CTO conversation",
}

// TestCTOOperatorToolSchemaFieldsDecode walks every field the CTO tools
// advertise for operator-backed calls and checks it decodes into the request
// struct. A field the struct lacks would be dropped without a word.
func TestCTOOperatorToolSchemaFieldsDecode(t *testing.T) {
	for name, request := range ctoOperatorRequests {
		spec, ok := ctoConcreteTools[name]
		if !ok {
			t.Errorf("mapped tool %s is not a concrete CTO tool", name)
			continue
		}
		fields := map[string]any{}
		for field, def := range spec.fields {
			fields[field] = schemaPlaceholder(def)
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(request()); err != nil {
			t.Errorf("%s sends %s: %v", name, raw, err)
		}
	}
	for name := range ctoOperatorTranslatedTools {
		if _, ok := ctoConcreteTools[name]; !ok {
			if _, ok := ctoTools[name]; !ok {
				t.Errorf("translated tool %s is not a CTO tool", name)
			}
		}
	}
}

// schemaPlaceholder gives a field a value of its own type, so a struct's
// custom unmarshalers see the shape the tool advertises.
func schemaPlaceholder(f ctoField) any {
	switch f.typ {
	case "boolean":
		return false
	case "integer", "number":
		return 0
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	case "graph_ref":
		return map[string]any{"kind": "agent", "id": "fixture"}
	case "graph_endpoint":
		return "fixture"
	default:
		return ""
	}
}
