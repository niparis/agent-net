package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/mcpserver"
)

func ctoClassFixture(name string) json.RawMessage {
	if name == "cto_op" {
		return json.RawMessage(`{"method":"inbox_list","args":{}}`)
	}
	field := ctoActionField[name]
	if field == "" {
		return json.RawMessage(`{}`)
	}
	action := map[string]map[string]string{
		"graph_change":    {"action": "charter_set"},
		"session_change":  {"action": "rename"},
		"worker_change":   {"action": "start"},
		"answer":          {"kind": "held", "action": "a"},
		"mcp_change":      {"action": "test"},
		"settings_change": {"area": "onboarding"},
		"project_change":  {"action": "add"},
		"skill_archive":   {"action": "list"},
		"skills":          {"action": "effective"},
	}[name]
	fields := map[string]any{}
	for k, v := range action {
		fields[k] = v
	}
	b, _ := json.Marshal(fields)
	return b
}

func TestCTOClassTableComplete(t *testing.T) {
	ctx := context.Background()
	a, b := mcp.NewInMemoryTransports()
	ss, err := mcpserver.NewCTO(nil, "test").Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Actions that moved to the op tier stay in the class table for cto_op,
	// but they are no longer advertised on the typed tool.
	moved := map[string]map[string]bool{"settings_change": {"operator": true}}
	for _, op := range mcpserver.CTOOps() {
		if op.Tool == "" {
			continue
		}
		if moved[op.Tool] == nil {
			moved[op.Tool] = map[string]bool{}
		}
		moved[op.Tool][op.Action] = true
	}
	for _, tool := range listed.Tools {
		fixture := ctoClassFixture(tool.Name)
		if _, err := ctoClass(tool.Name, fixture); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
		}
		schemaRaw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schemaRaw, &schema); err != nil {
			t.Fatal(err)
		}
		for field, actions := range ctoActions[tool.Name] {
			var kept []string
			for _, action := range actions {
				if !moved[tool.Name][action] {
					kept = append(kept, action)
				}
			}
			got := schema.Properties[field].Enum
			if len(got) != len(kept) {
				t.Errorf("%s %s: schema actions %v, classes %v", tool.Name, field, got, kept)
			}
		}
	}
}

// Talk tools run at once: none of them is an owner-only action.
func TestCTOTalkRunsAtOnce(t *testing.T) {
	for _, name := range []string{"cto_ui_show", "agent_message_send", "agent_message_reply"} {
		ran := 0
		_, err := routeCTOTool(context.Background(), name, ctoClassFixture(name), func(context.Context, string, json.RawMessage) (any, error) { ran++; return "ok", nil })
		if err != nil || ran != 1 {
			t.Fatalf("%s: ran=%d err=%v", name, ran, err)
		}
	}
}
