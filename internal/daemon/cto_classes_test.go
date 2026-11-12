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
		return json.RawMessage(`{"method":"apps_list","args":{}}`)
	}
	field := ctoActionField[name]
	if field == "" {
		return json.RawMessage(`{}`)
	}
	action := map[string]map[string]string{
		"message":         {"action": "send"},
		"graph_change":    {"action": "charter_set"},
		"session_change":  {"action": "rename"},
		"worker_change":   {"action": "start"},
		"answer":          {"kind": "held", "action": "a"},
		"tools_change":    {"action": "plan"},
		"mcp_change":      {"action": "test"},
		"settings_change": {"area": "onboarding"},
		"project_change":  {"action": "add"},
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
		if ctoToolClasses[tool.Name] == ctoProposal || tool.Name == "message" {
			if _, ok := schema.Properties["reason"]; !ok {
				t.Errorf("%s needs optional reason", tool.Name)
			}
		}
	}
}

func TestCTOProposalRouting(t *testing.T) {
	for name, class := range ctoToolClasses {
		if class != ctoProposal {
			continue
		}
		t.Run(name, func(t *testing.T) {
			args, ok := ctoProposalFixtures[name]
			if !ok {
				t.Fatal("missing proposal fixture")
			}
			wantConcrete, _, wantAction, err := ctoConcreteCall(name, json.RawMessage(args))
			if err != nil {
				t.Fatal(err)
			}
			proposed, ran := 0, 0
			value, err := routeCTOTool(context.Background(), name, json.RawMessage(args), func(_ context.Context, tool, action string, raw json.RawMessage, reason string) (string, error) {
				proposed++
				if tool != name || action != wantAction {
					t.Fatalf("stored %s %s", tool, action)
				}
				return "p_1", nil
			}, func(context.Context, string, json.RawMessage) (any, error) { ran++; return nil, nil })
			if err != nil || proposed != 1 || ran != 0 || value != ProposalToolResult("p_1") {
				t.Fatalf("value=%v err=%v proposals=%d backend=%d concrete=%s", value, err, proposed, ran, wantConcrete)
			}
		})
	}
	for _, name := range []string{"message", "cto_ui_show", "proposal_ask", "agent_message_send", "agent_message_reply", "agent_owner_ask", "agent_learn"} {
		ran := 0
		args := ctoClassFixture(name)
		_, err := routeCTOTool(context.Background(), name, args, func(context.Context, string, string, json.RawMessage, string) (string, error) {
			t.Fatal("talk proposed")
			return "", nil
		}, func(context.Context, string, json.RawMessage) (any, error) { ran++; return "ok", nil })
		if err != nil || ran != 1 {
			t.Fatalf("%s: ran=%d err=%v", name, ran, err)
		}
	}
}

func TestCTOMessageOwnerRouting(t *testing.T) {
	for _, asOwner := range []bool{false, true} {
		proposed, ran := 0, 0
		args, _ := json.Marshal(map[string]any{"action": "send", "to": "s_target", "body": "exact body", "as_owner": asOwner})
		_, err := routeCTOTool(context.Background(), "message", args, func(_ context.Context, tool, action string, stored json.RawMessage, reason string) (string, error) {
			proposed++
			if tool != "message" || action != "send" {
				t.Fatalf("%s %s", tool, action)
			}
			return "p_1", nil
		}, func(context.Context, string, json.RawMessage) (any, error) { ran++; return nil, nil })
		if err != nil || proposed != 0 || ran != 1 {
			t.Fatalf("owner=%t proposals=%d backend=%d err=%v", asOwner, proposed, ran, err)
		}
	}
}
