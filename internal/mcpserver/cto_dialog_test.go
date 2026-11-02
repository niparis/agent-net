package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCTODialog(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		for _, tc := range []struct {
			action, want    string
			support, answer bool
		}{
			{"accept", "Approved by the owner in the dialog.", true, true},
			{"decline", "Refused by the owner in the dialog.", true, true},
			{"cancel", "waiting", true, false},
			{"", "waiting", false, false},
		} {
			t.Run(version+tc.action+tc.want, func(t *testing.T) {
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
				cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, opts).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: version})
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

func TestCTODialogRetryTokens(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			st, ct := mcp.NewInMemoryTransports()
			calls, decisions := 0, 0
			token := ""
			srv := NewCTO(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
				if method == "cto_dialog_answer" {
					decisions++
					return json.RawMessage(`"done"`), nil
				}
				calls++
				return json.RawMessage(`{"kind":"proposal","proposal_id":"p_1","message":"Stored proposal","text":"waiting"}`), nil
			}, "test")
			srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
				return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
					res, err := next(ctx, method, req)
					if result, ok := res.(*mcp.CallToolResult); ok && result.RequestState != "" {
						token = result.RequestState
					}
					return res, err
				}
			})
			ss, err := srv.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				return &mcp.ElicitResult{Action: "accept"}, nil
			}}).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			args := map[string]any{"action": "node_add", "name": "x"}
			_, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "graph_change", Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || decisions != 1 {
				t.Fatalf("initial calls %d decisions %d", calls, decisions)
			}
			if version == "2026-07-28" && (token == "" || strings.Contains(token, "p_1")) {
				t.Fatalf("bad token %q", token)
			}
			for _, state := range []string{"forged", token} {
				if state == "" {
					continue
				}
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "graph_change", Arguments: args, RequestState: state, InputResponses: mcp.InputResponseMap{"approval": &mcp.ElicitResult{Action: "accept"}}})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "Inbox") || calls != 1 || decisions != 1 {
					t.Fatalf("retry %q: %+v calls %d decisions %d", state, result, calls, decisions)
				}
			}
		})
	}
}
