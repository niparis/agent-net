package daemon

import (
	"context"
	"encoding/json"
	"strings"
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

func TestCTOSessionStartRejectsPathOutsideHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := &Daemon{}
	for _, call := range []proposalCall{
		{Tool: "session_change", Action: "session_start", Args: json.RawMessage(`{"action":"session_start","path":"/outside/project","harness":"claude"}`)},
		{Tool: "project_change", Action: "create", Args: json.RawMessage(`{"action":"create","path":"/outside/project"}`)},
	} {
		if err := d.validateProposal(context.Background(), call); err == nil || !strings.Contains(err.Error(), "outside your home") {
			t.Errorf("outside path for %s: %v", call.Tool, err)
		}
	}
}
