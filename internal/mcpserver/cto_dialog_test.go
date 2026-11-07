package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCTODialogResultTypes(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ordinary := false
	srv := NewCTO(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if ordinary {
			return json.RawMessage(`{"kind":"result","value":"ordinary"}`), nil
		}
		if method == "cto_dialog_answer" {
			return json.RawMessage(`"done"`), nil
		}
		return json.RawMessage(`{"kind":"proposal","proposal_id":"p_1","message":"Stored proposal","text":"waiting"}`), nil
	}, "test")
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if method == "tools/call" && err == nil {
				raw, e := json.Marshal(res)
				if e != nil {
					t.Error(e)
				}
				t.Logf("wire result: %s", raw)
				var v map[string]json.RawMessage
				if e := json.Unmarshal(raw, &v); e != nil {
					t.Error(e)
				}
				want := `"complete"`
				if len(v["inputRequests"]) > 0 {
					want = `"input_required"`
				}
				if string(v["resultType"]) != want {
					t.Errorf("resultType=%s want %s", v["resultType"], want)
				}
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
	}}).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "graph_change", Arguments: map[string]any{"action": "node_add", "name": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "Approved") {
		t.Fatal(result)
	}
	ordinary = true
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "graph_change", Arguments: map[string]any{"action": "node_add", "name": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "ordinary") {
		t.Fatal(result)
	}
}

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

func TestCTOProposalAskDialog(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		t.Run("approve_"+version, func(t *testing.T) {
			ctx := context.Background()
			st, ct := mcp.NewInMemoryTransports()
			askedID := ""
			answered := false
			storedMessage := "Approve this proposal?\nTool: mcp_change\nAction: add\nArguments:\n  name: demo\nReason: needed"
			srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
				if method == "cto_dialog_answer" {
					answered = true
					answer := params.(map[string]any)
					if answer["id"] != "p_15" || answer["approve"] != true {
						t.Errorf("wrong answer: %v", answer)
					}
					return json.RawMessage(`"p_15 approved and applied"`), nil
				}
				if method != "proposal_ask" {
					t.Fatalf("unexpected method %q", method)
				}
				raw, _ := params.(json.RawMessage)
				var q struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(raw, &q); err != nil {
					t.Fatal(err)
				}
				askedID = q.ID
				b, _ := json.Marshal(map[string]any{
					"kind":        "proposal",
					"proposal_id": "p_15",
					"message":     storedMessage,
					"text":        "proposal p_15 is in the owner's Inbox (tab 7 of the TUI); the owner decides there. You will get a note with the outcome.",
				})
				return b, nil
			}, "test")
			ss, err := srv.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			elicited := ""
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{
				ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					elicited = req.Params.Message
					return &mcp.ElicitResult{Action: "accept"}, nil
				},
			}).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "proposal_ask", Arguments: map[string]any{"id": "p_15"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || !answered || askedID != "p_15" || elicited != storedMessage {
				t.Fatalf("result=%+v answered=%v askedID=%q elicited=%q", result, answered, askedID, elicited)
			}
			text := result.Content[0].(*mcp.TextContent).Text
			if !strings.Contains(text, "Approved by the owner in the dialog.") || strings.Contains(text, "p_15 approved and applied") {
				t.Fatalf("unexpected dialog text: %q", text)
			}
		})
	}

	t.Run("closed_or_invalid_id", func(t *testing.T) {
		ctx := context.Background()
		st, ct := mcp.NewInMemoryTransports()
		elicited := false
		daemonCalls := 0
		srv := NewCTO(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			daemonCalls++
			return nil, errors.New("proposal p_15 is applied, not open")
		}, "test")
		ss, err := srv.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{
			ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				elicited = true
				return &mcp.ElicitResult{Action: "accept"}, nil
			},
		}).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()
		for _, bad := range []map[string]any{{}, {"id": "   "}} {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "proposal_ask", Arguments: bad})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError || daemonCalls != 0 || elicited {
				t.Fatalf("bad args %v: res=%+v calls=%d elicited=%v", bad, res, daemonCalls, elicited)
			}
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "proposal_ask", Arguments: map[string]any{"id": "p_15"}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || elicited || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "not open") {
			t.Fatalf("closed proposal: res=%+v elicited=%v", res, elicited)
		}
	})
}

func TestCTOProjectCreateUsesChatConfirmation(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	asked, answered := false, false
	srv := NewCTO(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == "cto_dialog_answer" {
			answered = true
			return json.RawMessage(`"ready"`), nil
		}
		return json.RawMessage(`{"kind":"proposal","proposal_id":"p_1","message":"garden -> machine, ~/code/garden, Claude Opus, background. OK?","text":"waiting for the owner's yes in CTO chat"}`), nil
	}, "test")
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		if request.Params.Message != "garden -> machine, ~/code/garden, Claude Opus, background. OK?" {
			t.Errorf("confirmation: %q", request.Params.Message)
		}
		asked = true
		return &mcp.ElicitResult{Action: "accept"}, nil
	}}).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "project_change", Arguments: map[string]any{"action": "create", "path": "~/code/garden", "harness": "claude"}})
	if err != nil || result.IsError || !asked || !answered {
		t.Fatalf("result=%v elicited=%v err=%v", result, asked, err)
	}
}
