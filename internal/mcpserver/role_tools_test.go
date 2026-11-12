package mcpserver

// Tests for tool redesign task 4a: each server lists exactly the catalog tools
// of its caller's role, no catalog tool carries an output schema in tools/list,
// and a structured result that breaks its catalog output schema is refused.

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func catalogNames(role toolcatalog.Role) []string {
	var names []string
	for _, tool := range toolcatalog.ForRole(role) {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func listedCatalogNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, tool := range toolcatalog.Tools {
		known[tool.Name] = true
	}
	var names []string
	for _, tool := range list.Tools {
		if known[tool.Name] {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	return names
}

func connectAgent(t *testing.T) *mcp.ClientSession {
	t.Helper()
	t.Setenv("AGENT_NET_WORKER", "")
	return connect(t, okCaller)
}

func TestCatalogToolsListedForRole(t *testing.T) {
	cases := []struct {
		name   string
		role   toolcatalog.Role
		client func(*testing.T) *mcp.ClientSession
	}{
		{"agent", toolcatalog.RoleAgent, connectAgent},
		{"worker", toolcatalog.RoleWorker, connectWorker},
		{"CTO", toolcatalog.RoleCTO, ctoTestSession},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := listedCatalogNames(t, tc.client(t))
			want := catalogNames(tc.role)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s catalog tools = %v, want %v", tc.name, got, want)
			}
		})
	}
}

func TestCatalogToolsHaveNoOutputSchema(t *testing.T) {
	cases := []struct {
		name   string
		client func(*testing.T) *mcp.ClientSession
	}{
		{"agent", connectAgent},
		{"worker", connectWorker},
		{"CTO", ctoTestSession},
	}
	catalog := map[string]bool{}
	for _, tool := range toolcatalog.Tools {
		catalog[tool.Name] = true
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list, err := tc.client(t).ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range list.Tools {
				if catalog[tool.Name] && tool.OutputSchema != nil {
					t.Errorf("%s lists an output schema", tool.Name)
				}
			}
		})
	}
}

func TestWorkerHandbackTool(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "1")
	var method string
	var input protocol.WorkerHandbackRequest
	cs := connectServer(t, func(_ context.Context, m string, p any) (json.RawMessage, error) {
		method = m
		b, _ := json.Marshal(p)
		_ = json.Unmarshal(b, &input)
		return json.RawMessage(`{"sent":true,"status":"done","complete":true,"queued":false}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_handback",
		Arguments: map[string]any{"report": "done", "status": "done", "complete": true, "files": []string{"/work/result.txt"}}})
	if err != nil || res.IsError {
		t.Fatalf("agent_handback: %v %+v", err, res)
	}
	if method != "worker_handback" {
		t.Fatalf("agent_handback called %q", method)
	}
	if len(input.Files) != 1 || input.Files[0] != "/work/result.txt" {
		t.Fatalf("files lost: %+v", input)
	}
	if res.StructuredContent == nil {
		t.Fatal("agent_handback has no structured result")
	}
}

// A hand-back answered before its check ends carries the daemon's note, so the
// worker knows the notice follows and does not hand back again.
func TestWorkerHandbackToolKeepsCheckNote(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "1")
	cs := connectServer(t, func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"status":"done","complete":true,"checking":true,"note":"already handed back; check running"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_handback",
		Arguments: map[string]any{"report": "done", "status": "done", "complete": true}})
	if err != nil || res.IsError {
		t.Fatalf("agent_handback: %v %+v", err, res)
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok || out["note"] != "already handed back; check running" || out["delivered"] != false {
		t.Fatalf("structured result: %#v", res.StructuredContent)
	}
}

func TestCatalogOutputValidationRefusesBadResult(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "")
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == "sessions_find" {
			// items must be an array of sessions, not a string.
			return json.RawMessage(`{"items":"not a list","total":0}`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_sessions_find", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("a structured result that breaks the output schema was accepted: %+v", res)
	}
}
