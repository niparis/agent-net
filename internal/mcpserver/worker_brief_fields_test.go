package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every structured brief field reaches the daemon unchanged.
func TestWorkerStartForwardsStructuredBrief(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		return json.RawMessage(`{"status":"checked"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_start", Arguments: map[string]any{
		"project":    "shop",
		"problem":    "the login fails after a restart",
		"evidence":   []string{"test A fails", "the log shows a stale cache"},
		"done":       []string{"the login test passes", "no new warnings"},
		"hypothesis": "the cache is stale",
		"scope":      "do not touch the writer",
		"mode":       "check",
	}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_start: %v %+v", err, res)
	}
	if len(calls) == 0 || calls[0].method != "worker_start" {
		t.Fatalf("calls %#v", calls)
	}
	p := calls[0].params
	if p["problem"] != "the login fails after a restart" || p["hypothesis"] != "the cache is stale" || p["scope"] != "do not touch the writer" {
		t.Fatalf("text fields %#v", p)
	}
	if done, _ := p["done"].([]any); len(done) != 2 || done[0] != "the login test passes" {
		t.Fatalf("done %#v", p["done"])
	}
	if evidence, _ := p["evidence"].([]any); len(evidence) != 2 || evidence[1] != "the log shows a stale cache" {
		t.Fatalf("evidence %#v", p["evidence"])
	}
}

// The brief flags of a start reach the caller. The output schema would drop an
// undeclared field, so this also checks the catalog declares them.
func TestWorkerStartReturnsBriefFlags(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		return json.RawMessage(`{"status":"checked","brief_flags":[{"check":"reference","line":"Use the fixture as in the brief above.","reason":"points at text that is not in this brief."}]}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_start", Arguments: map[string]any{
		"project": "shop", "problem": "the login fails after a restart", "done": []string{"the login test passes"},
		"task": "Use the fixture as in the brief above.", "mode": "check",
	}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_start: %v %+v", err, res)
	}
	var out struct {
		Brief      string `json:"brief"`
		BriefFlags []struct {
			Check  string `json:"check"`
			Line   string `json:"line"`
			Reason string `json:"reason"`
		} `json:"brief_flags"`
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("structured content: %v", err)
	}
	if out.Brief != "ok" || len(out.BriefFlags) != 1 || out.BriefFlags[0].Check != "reference" || out.BriefFlags[0].Line != "Use the fixture as in the brief above." {
		t.Fatalf("output = %+v", out)
	}
}
