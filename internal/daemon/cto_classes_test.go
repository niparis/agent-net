package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/mcpserver"
)

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
	for _, tool := range listed.Tools {
		if _, err := ctoClass(tool.Name, json.RawMessage(`{}`)); err != nil {
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
		for _, field := range []string{"action", "operation"} {
			actions := schema.Properties[field].Enum
			if len(actions) != len(ctoActions[tool.Name][field]) {
				t.Errorf("%s %s: schema actions %v, classes %v", tool.Name, field, actions, ctoActions[tool.Name][field])
			}
			for _, action := range actions {
				args, _ := json.Marshal(map[string]string{field: action})
				if _, err := ctoClass(tool.Name, args); err != nil {
					t.Error(err)
				}
			}
		}
		if ctoToolClasses[tool.Name] == ctoProposal || tool.Name == "message" {
			if _, ok := schema.Properties["reason"]; !ok {
				t.Errorf("%s needs optional reason", tool.Name)
			}
		}

	}
	for name := range ctoOwnerMethods {
		if _, err := ctoClass(name, json.RawMessage(`{}`)); err != nil {
			t.Error(err)
		}
	}
	for name := range ctoLocalMethods {
		if _, err := ctoClass(name, json.RawMessage(`{}`)); err != nil {
			t.Error(err)
		}
	}
}

func TestCTOProposalRouting(t *testing.T) {
	for name, class := range ctoToolClasses {
		if class != ctoProposal {
			continue
		}
		t.Run(name, func(t *testing.T) {
			proposed, ran := 0, 0
			args := json.RawMessage(`{"reason":"owner should review"}`)
			if name == "notification_settings" {
				args = json.RawMessage(`{"disabled":true,"reason":"owner should review"}`)
			}
			if name == "cto_settings" {
				args = json.RawMessage(`{"title":"Review","reason":"owner should review"}`)
			}
			value, err := routeCTOTool(context.Background(), name, args, func(_ context.Context, tool, action string, raw json.RawMessage, reason string) (string, error) {
				proposed++
				if tool != name || reason != "owner should review" {
					t.Fatalf("stored %s %q", tool, reason)
				}
				return "p_1", nil
			}, func(context.Context, string, json.RawMessage) (any, error) { ran++; return nil, nil })
			if err != nil || proposed != 1 || ran != 0 || value != ProposalToolResult("p_1") {
				t.Fatalf("value=%v err=%v proposals=%d backend=%d", value, err, proposed, ran)
			}
		})
	}
	for _, name := range []string{"send", "reply", "learn", "ui_show", "ui_notice"} {
		ran := 0
		_, err := routeCTOTool(context.Background(), name, json.RawMessage(`{}`), func(context.Context, string, string, json.RawMessage, string) (string, error) {
			t.Fatal("talk proposed")
			return "", nil
		}, func(context.Context, string, json.RawMessage) (any, error) { ran++; return "ok", nil })
		if err != nil || ran != 1 {
			t.Fatalf("%s: ran=%d err=%v", name, ran, err)
		}
	}
}

func TestCTOUnknownActionFailsClosed(t *testing.T) {
	for _, name := range []string{"message", "held_answer", "outcome_overrule"} {
		if _, err := ctoClass(name, json.RawMessage(`{"action":"unknown"}`)); err == nil {
			t.Errorf("%s accepted unknown action", name)
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
		if err != nil || asOwner && (proposed != 1 || ran != 0) || !asOwner && (proposed != 0 || ran != 1) {
			t.Fatalf("owner=%t proposals=%d backend=%d err=%v", asOwner, proposed, ran, err)
		}
	}
}
