package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCTODialog(t *testing.T) {
	for _, tc := range []struct {
		action, want    string
		support, answer bool
	}{
		{"accept", "Approved by the owner in the dialog.", true, true},
		{"decline", "Refused by the owner in the dialog.", true, true},
		{"cancel", "waiting", true, false},
		{"", "waiting", false, false},
	} {
		t.Run(tc.action+tc.want, func(t *testing.T) {
			ctx := context.Background()
			st, ct := mcp.NewInMemoryTransports()
			answered := false
			srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
				if method == "cto_dialog_answer" {
					answered = true
					answer := params.(map[string]any)
					if answer["id"] != "p_1" || answer["approve"] != (tc.action == "accept") {
						t.Errorf("wrong answer: %v", answer)
					}
					return json.RawMessage(`"done"`), nil
				}
				return json.RawMessage(`{"kind":"proposal","proposal_id":"p_1","message":"Stored proposal","text":"waiting"}`), nil
			}, "test")
			ss, err := srv.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			opts := &mcp.ClientOptions{}
			if tc.support {
				opts.ElicitationHandler = func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					if req.Params.Message != "Stored proposal" {
						t.Errorf("message: %s", req.Params.Message)
					}
					return &mcp.ElicitResult{Action: tc.action}, nil
				}
			}
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, opts).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "graph_change", Arguments: map[string]any{"action": "node_add", "name": "x"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Content) != 1 || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, tc.want) || answered != tc.answer {
				t.Fatalf("result=%+v answered=%v", result.Content[0].(*mcp.TextContent).Text, answered)
			}
		})
	}
}

func TestCTODialogIgnoresToolResultThatLooksLikeProposal(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	called := false
	srv := NewCTO(func(_ context.Context, _ string, _ any) (json.RawMessage, error) {
		return json.RawMessage(`{"kind":"result","value":{"proposal_id":"p_1","message":"misleading","text":"waiting"}}`), nil
	}, "test")
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		called = true
		return &mcp.ElicitResult{Action: "accept"}, nil
	}}).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "graph_change", Arguments: map[string]any{"action": "node_add", "name": "x"}})
	if err != nil || result.IsError || called {
		t.Fatalf("result %v, error %v, elicited %v", result, err, called)
	}
	if strings.Contains(result.Content[0].(*mcp.TextContent).Text, `"kind"`) || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "misleading") {
		t.Fatalf("ordinary result lost: %+v", result.Content)
	}
}
