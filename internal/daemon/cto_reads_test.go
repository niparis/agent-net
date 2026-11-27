package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO's shared catalog reads are the ordinary agent's reads, run for the
// CTO's session (decision 0176): the same rows and the same opaque cursors.
// A plain offset is not a cursor any more.
func TestCTOCatalogReadsUseTheAgentReadContract(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	for _, p := range []protocol.WorkerProfile{
		{Name: "alpha", Harness: "codex", Model: "gpt-6.1-terra:high"},
		{Name: "beta", Harness: "codex", Model: "unscored-model"},
	} {
		p.AddedBy = w.ownerID()
		if err := g.gw.Store().AddWorkerProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	read := func(tool, args string) (map[string]any, error) {
		t.Helper()
		v, err := w.d.runCTOToolRaw(context.Background(), tool, json.RawMessage(args))
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out, nil
	}

	first, err := read("agent_worker_profiles_find", `{"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := first["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("first page: %v", first)
	}
	if row := items[0].(map[string]any); row["name"] != "alpha" || row["provider"] == nil {
		t.Fatalf("profile row is not the agent row: %v", row)
	}
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" || cursor == "1" {
		t.Fatalf("next_cursor %q is not an agent cursor", cursor)
	}
	second, err := read("agent_worker_profiles_find", `{"limit":1,"cursor":"`+cursor+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if items, _ := second["items"].([]any); len(items) != 1 || items[0].(map[string]any)["name"] != "beta" {
		t.Fatalf("second page: %v", second)
	}

	for _, tool := range []string{"agent_workers_find", "agent_messages_find", "agent_graph_read", "agent_mcp_servers_find", "agent_worker_profiles_find"} {
		if _, err := read(tool, `{"cursor":"1"}`); err == nil || !strings.Contains(err.Error(), "cursor") {
			t.Errorf("%s took an offset cursor: %v", tool, err)
		}
	}
}
