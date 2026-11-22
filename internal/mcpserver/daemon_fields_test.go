package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// daemonFieldSamples are inputs that get a handler past its own checks. Every
// other string field gets a marker of its own name.
var daemonFieldSamples = map[string]map[string]any{
	"agent_message_send":  {"to": "s01", "body": "BODY"},
	"agent_message_reply": {"message": "MSG", "body": "BODY", "outcome": "accept"},
	"agent_file_send":     {"to": "TO", "paths": []any{"/tmp/a"}, "note": "NOTE"},
	"agent_owner_ask":     {"kind": "question", "text": "TEXT", "recommendation": "REC"},
	"agent_worker_rate":   {"worker": "WORKER", "kind": "rating", "score": 4, "note": "NOTE"},
	"agent_worker_start":  {"problem": "PROBLEM", "done": []any{"DONE"}},
	"agent_step_claim":    {"change": "CHANGE", "step": "test", "commit": sampleCommit},
	"agent_step_report":   {"change": "CHANGE", "step": "test", "result": "pass", "commit": sampleCommit},
	"agent_note_write":    {"name": "NAME", "description": "DESC", "body": "BODY"},
	"agent_messages_find": {"since": "2026-01-01T12:00:00Z", "conversation": "CONV", "with": "WITH", "id": "ID"},
	"agent_sessions_find": {"kind": []any{"agent"}, "text": "TEXT", "project": "PROJ", "id": "ID"},
	"agent_machines_find": {"id": "ID"},
}

const sampleCommit = "0123456789012345678901234567890123456789"

// daemonFieldsInfo answers the "info" call that the read tools make to learn
// the calling session.
const daemonFieldsInfo = `{"connected":true,"sessions":[{"type":"claude","session_id":"s_me","harness":"claude","native_id":"nat_me","name":"me","repo":{"project":"p"}}]}`

// pluginOtherMethod lists the tools whose native plugin calls another daemon
// method than the MCP server does. Their fields are not compared.
var pluginOtherMethod = map[string]string{}

// TestDaemonFieldsMatchTheMCPServer sends every agent catalog tool through the
// MCP server with marker values. It checks where each marker reaches the daemon
// and compares that with the daemon table in internal/toolcatalog. A renamed
// field that the table does not list fails, and so does a table entry that the
// MCP server does not send.
func TestDaemonFieldsMatchTheMCPServer(t *testing.T) {
	plugin, err := toolcatalog.PluginTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range plugin {
		if !hasRole(tool.Roles, toolcatalog.RoleAgent) {
			continue
		}
		t.Run(tool.Name, func(t *testing.T) {
			if why, ok := pluginOtherMethod[tool.Name]; ok {
				t.Skip(why)
			}
			checkDaemonFields(t, tool)
		})
	}
}

func hasRole(roles []toolcatalog.Role, want toolcatalog.Role) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func checkDaemonFields(t *testing.T, tool toolcatalog.PluginTool) {
	t.Helper()
	input, markers := daemonFieldInput(t, tool)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "nat_me")
	var sent []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		sent = append(sent, call{method, m})
		if method == "info" {
			return json.RawMessage(daemonFieldsInfo), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool.Name, Arguments: input})
	var got map[string]any
	found := false
	for _, c := range sent {
		if c.method == tool.Method {
			got, found = c.params, true
		}
	}
	if !found {
		t.Fatalf("the MCP server did not call daemon method %s for %s with %v (err %v, result %+v)", tool.Method, tool.Name, input, err, res)
	}
	for field, marker := range markers {
		key := keyOfMarker(got, marker)
		want := toolcatalog.DaemonField(tool.Name, field)
		if key == want || key == "" && want == field {
			continue
		}
		t.Errorf("%s: the field %s reaches the daemon as %q, but the daemon table says %q", tool.Name, field, key, want)
	}
}

// daemonFieldInput builds an input for a tool. Each string field gets a marker
// of its own, and the markers map each field to its marker.
func daemonFieldInput(t *testing.T, tool toolcatalog.PluginTool) (map[string]any, map[string]string) {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Type    string `json:"type"`
			Enum    []any  `json:"enum"`
			Pattern string `json:"pattern"`
			Items   *struct {
				Type string `json:"type"`
			} `json:"items"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{}
	for _, name := range schema.Required {
		required[name] = true
	}
	input := map[string]any{}
	markers := map[string]string{}
	for field, value := range daemonFieldSamples[tool.Name] {
		input[field] = value
		if marker := markerOf(value); marker != "" {
			markers[field] = marker
		}
	}
	for name, prop := range schema.Properties {
		// A cursor is opaque, and continue excludes task in worker_start.
		if _, ok := input[name]; ok || name == "cursor" || name == "continue" {
			continue
		}
		if target := toolcatalog.DaemonField(tool.Name, "id"); target != "" && target != "id" && name == target {
			if _, ok := input["id"]; ok {
				continue
			}
		}
		switch {
		case len(prop.Enum) > 0:
			if required[name] {
				input[name] = prop.Enum[0]
			}
		case prop.Pattern != "":
			continue
		case prop.Type == "string":
			input[name] = "S_" + name
			markers[name] = "S_" + name
		case prop.Type == "array" && prop.Items != nil && prop.Items.Type == "string":
			if required[name] {
				input[name] = []any{"S_" + name}
				markers[name] = "S_" + name
			}
		case prop.Type == "boolean" && required[name]:
			input[name] = false
		case (prop.Type == "integer" || prop.Type == "number") && required[name]:
			input[name] = 1
		case prop.Type == "array" && required[name]:
			input[name] = []any{}
		case prop.Type == "object" && required[name]:
			input[name] = map[string]any{}
		}
	}
	return input, markers
}

// markerOf returns the marker of a sample value: the string itself, or the
// first item of a string array.
func markerOf(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// keyOfMarker returns the key under which a marker reached the daemon. A
// string array with the marker as its only item counts too. It returns "" when
// the marker did not reach the daemon.
func keyOfMarker(params map[string]any, marker string) string {
	for key, value := range params {
		switch v := value.(type) {
		case string:
			if v == marker && strings.TrimSpace(v) != "" {
				return key
			}
		case []any:
			if len(v) == 1 && v[0] == marker {
				return key
			}
		}
	}
	return ""
}
