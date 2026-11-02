package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// workerListWith is the gateway's list as agent_workers_find reads it.
func workerListWith(worker protocol.AgentView) json.RawMessage {
	raw, _ := json.Marshal(protocol.AgentsResult{Agents: []protocol.AgentView{worker}})
	return raw
}

func ratedWorker() protocol.AgentView {
	return protocol.AgentView{
		SessionID: "s_w1", Hostname: "research", Harness: "opencode", Model: "deepseek-v4.1-flash",
		Online: true, Profile: protocol.Profile{Name: "worker-one"},
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: "s_owner", Project: "shop", Task: "fix the bug",
			StartedAt: 1700000000, DifficultyUsed: "hard"},
	}
}

// agent_worker_rate returns the worker record that agent_workers_find would
// show, not just its session ID.
func TestAgentWorkerRateReturnsTheFilledWorker(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		switch method {
		case "agent_rate":
			return json.RawMessage(`{"accepted":true,"worker":"s_w1","score":4,"model":"deepseek-v4.1-flash","difficulty":"hard"}`), nil
		case "list":
			return workerListWith(ratedWorker()), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_rate",
		Arguments: map[string]any{"worker": "s_w1", "kind": "rating", "score": 4, "note": "good"}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_rate: %v %+v", err, res)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out["recorded"] != true || out["kind"] != "rating" || out["score"] != float64(4) {
		t.Fatalf("rate result: %+v", out)
	}
	worker, _ := out["worker"].(map[string]any)
	if worker["id"] != "s_w1" || worker["name"] != "worker-one" || worker["project"] != "shop" ||
		worker["task"] != "fix the bug" || worker["model"] != "deepseek-v4.1-flash" ||
		worker["difficulty"] != "hard" || worker["state"] != "running" || worker["run_time"] == "" {
		t.Fatalf("worker record: %+v", worker)
	}
	caller, _ := worker["caller"].(map[string]any)
	if caller["id"] != "s_owner" || caller["name"] != "s_owner" {
		t.Fatalf("caller: %+v", caller)
	}
}

// agent_worker_rate takes kind defect_found_later: no score, one note, and the
// answer says a defect was recorded.
func TestAgentWorkerRateDefectFoundLater(t *testing.T) {
	var saw map[string]any
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "agent_rate":
			raw, _ := json.Marshal(params)
			json.Unmarshal(raw, &saw)
			return json.RawMessage(`{"accepted":true,"worker":"s_w1","kind":"defect_found_later","severity":3,"model":"deepseek-v4.1-flash","difficulty":"hard"}`), nil
		case "list":
			return workerListWith(ratedWorker()), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_rate",
		Arguments: map[string]any{"worker": "s_w1", "kind": "defect_found_later", "severity": 3, "note": "old path left in place"}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_rate defect: %v %+v", err, res)
	}
	if saw["kind"] != "defect_found_later" || saw["note"] != "old path left in place" || saw["severity"] != float64(3) {
		t.Fatalf("defect request: %+v", saw)
	}
	if _, ok := saw["score"]; ok {
		t.Fatalf("a defect report sent a score: %+v", saw)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out["recorded"] != true || out["kind"] != "defect_found_later" || out["severity"] != float64(3) {
		t.Fatalf("defect result: %+v", out)
	}
}
