package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The redesigned session reads carry the same public charter fields the old
// agent_list and agent_get did, so removing those aliases loses no charter.
func TestAgentSessionsCarryCharter(t *testing.T) {
	row := `{"id":"s_test","name":"s_test","kind":"agent","machine":"research","project":"housing",` +
		`"harness":"claude","model":"standard","online":true,"status":"idle","task":"work",` +
		`"last_seen":"2026-01-01T12:00:00Z","task_started":null,"purpose":"fallback",` +
		`"charter":{"purpose":"fallback","purpose_fallback":true,"accepts":["bugs"],"refuses":["special"],"feature_requests":"ask"}}`
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		switch method {
		case "sessions_find":
			return json.RawMessage(`{"items":[` + row + `],"total":1}`), nil
		case "session_get":
			return json.RawMessage(`{"session":` + row + `}`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"agent_sessions_find", map[string]any{"detail": "full"}},
		{"agent_session_get", map[string]any{"session": "s_test"}},
	} {
		r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: c.name, Arguments: c.args})
		if err != nil || r.IsError {
			t.Fatalf("%s: %v %+v", c.name, err, r)
		}
		raw, _ := json.Marshal(r)
		for _, want := range []string{"fallback", "purpose_fallback", "bugs", "special", "feature_requests"} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("%s missing %s: %s", c.name, want, raw)
			}
		}
	}
}

// agent_projects_find keeps the display name and source the old project list
// carried, so an agent can still tell a registered project from one merely seen.
func TestAgentProjectsFindCarriesDisplayNameAndSource(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == "projects_find" {
			return json.RawMessage(`{"items":[{"name":"housing","display_name":"Housing","machine":"research",` +
				`"folder":"/work/housing","sessions_online":1,"sessions_total":2,"source":"both","remote":"https://example.com/r.git"}],"total":1}`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_projects_find", Arguments: map[string]any{"name": "housing"}})
	if err != nil || res.IsError {
		t.Fatalf("agent_projects_find: %v %+v", err, res)
	}
	raw, _ := json.Marshal(res)
	for _, want := range []string{"Housing", "both", "https://example.com/r.git"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("agent_projects_find missing %s: %s", want, raw)
		}
	}
}
