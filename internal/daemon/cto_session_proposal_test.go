package daemon

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCTOSessionStartIsProposal(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"session_change", `{"action":"session_start","project":"garden","harness":"claude"}`},
		{"project_change", `{"action":"create","path":"/outside/project"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposed, ran := 0, 0
			_, err := routeCTOTool(context.Background(), tc.name, json.RawMessage(tc.args), func(context.Context, string, string, json.RawMessage, string) (string, error) {
				proposed++
				return "p_1", nil
			}, func(context.Context, string, json.RawMessage) (any, error) { ran++; return nil, nil })
			if err != nil || proposed != 1 || ran != 0 {
				t.Fatalf("proposal=%d runs=%d err=%v", proposed, ran, err)
			}
		})
	}
}
