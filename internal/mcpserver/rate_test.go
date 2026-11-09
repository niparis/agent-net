package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
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

// agent_worker_rate takes kind move_to_brief_defect: it sends the defect id and
// returns the moved row and the new brief defect row.
func TestAgentWorkerRateMoveToBriefDefect(t *testing.T) {
	var saw map[string]any
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "agent_rate":
			raw, _ := json.Marshal(params)
			json.Unmarshal(raw, &saw)
			return json.RawMessage(`{"accepted":true,"worker":"s_w1","kind":"move_to_brief_defect","defect":12,"brief_defect":13,"model":"deepseek-v4.1-flash","difficulty":"hard"}`), nil
		case "list":
			return workerListWith(ratedWorker()), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_rate",
		Arguments: map[string]any{"worker": "s_w1", "kind": "move_to_brief_defect", "defect": 12}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_rate move: %v %+v", err, res)
	}
	if saw["kind"] != "move_to_brief_defect" || saw["defect"] != float64(12) {
		t.Fatalf("move request: %+v", saw)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out["recorded"] != true || out["kind"] != "move_to_brief_defect" || out["defect"] != float64(12) || out["brief_defect"] != float64(13) {
		t.Fatalf("move result: %+v", out)
	}
}

// A refused move reaches the caller with the gateway's reason, not just
// recorded false.
func TestAgentWorkerRateRefusalCarriesTheReason(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		switch method {
		case "agent_rate":
			return json.RawMessage(`{"accepted":false,"worker":"s_w1","kind":"move_to_brief_defect","reason":"only the session that filed this defect may move it"}`), nil
		case "list":
			return workerListWith(ratedWorker()), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_rate",
		Arguments: map[string]any{"worker": "s_w1", "kind": "move_to_brief_defect", "defect": 12}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_rate move: %v %+v", err, res)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out["recorded"] != false || !strings.Contains(out["reason"].(string), "only the session that filed") {
		t.Fatalf("refusal result: %+v", out)
	}
}

// A move without a defect id reaches the gateway, which picks the only match.
func TestAgentWorkerRateMoveWithoutDefectID(t *testing.T) {
	var saw map[string]any
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "agent_rate":
			raw, _ := json.Marshal(params)
			json.Unmarshal(raw, &saw)
			return json.RawMessage(`{"accepted":true,"worker":"s_w1","kind":"move_to_brief_defect","defect":12,"brief_defect":13}`), nil
		case "list":
			return workerListWith(ratedWorker()), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_rate",
		Arguments: map[string]any{"worker": "s_w1", "kind": "move_to_brief_defect"}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_rate move: %v %+v", err, res)
	}
	if saw["kind"] != "move_to_brief_defect" {
		t.Fatalf("move request: %+v", saw)
	}
	if _, ok := saw["defect"]; ok {
		t.Fatalf("an omitted defect id was sent: %+v", saw)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out["recorded"] != true || out["defect"] != float64(12) || out["brief_defect"] != float64(13) {
		t.Fatalf("move result: %+v", out)
	}
}

// agent_worker_rate takes kind brief_defect: a severity and a note, charged to
// the briefing session, and the answer says it was recorded.
func TestAgentWorkerRateBriefDefect(t *testing.T) {
	var saw map[string]any
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "agent_rate":
			raw, _ := json.Marshal(params)
			json.Unmarshal(raw, &saw)
			return json.RawMessage(`{"accepted":true,"worker":"s_w1","kind":"brief_defect","severity":2,"model":"deepseek-v4.1-flash","difficulty":"hard"}`), nil
		case "list":
			return workerListWith(ratedWorker()), nil
		}
		return json.RawMessage(`{}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_worker_rate",
		Arguments: map[string]any{"worker": "s_w1", "kind": "brief_defect", "severity": 2, "note": "the brief named the wrong branch"}})
	if err != nil || res.IsError {
		t.Fatalf("agent_worker_rate brief defect: %v %+v", err, res)
	}
	if saw["kind"] != "brief_defect" || saw["note"] != "the brief named the wrong branch" || saw["severity"] != float64(2) {
		t.Fatalf("brief defect request: %+v", saw)
	}
	if _, ok := saw["score"]; ok {
		t.Fatalf("a brief defect sent a score: %+v", saw)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out["recorded"] != true || out["kind"] != "brief_defect" || out["severity"] != float64(2) {
		t.Fatalf("brief defect result: %+v", out)
	}
}
