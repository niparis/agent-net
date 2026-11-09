package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/niparis/agent-net/internal/protocol"
)

// The tool forwards a status read and a delivery to the daemon op, and returns
// the schedule receipt.
func TestOwnerBriefToolReadsStatusAndDelivers(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, call{method, p})
		out := protocol.OwnerBriefResult{Status: protocol.BriefStatusActive, BriefTime: "09:00", TimeZone: "Asia/Kolkata",
			NextRunLocal: "2026-11-09 09:00", NextRunUTC: "2026-11-09T03:30:00Z"}
		if p["body"] != "" {
			out.ItemID, out.Date, out.Status = "i_brief", "2026-11-09", "delivered"
		}
		return json.Marshal(out)
	})
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_owner_brief", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("status: %v %+v", err, res)
	}
	if len(calls) != 1 || calls[0].method != protocol.OpOwnerBrief {
		t.Fatalf("status called %v", calls)
	}
	status, _ := res.StructuredContent.(map[string]any)
	if status["status"] != protocol.BriefStatusActive || status["next_run_local"] != "2026-11-09 09:00" {
		t.Fatalf("status output = %+v", status)
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_owner_brief", Arguments: map[string]any{
		"body": "Handled two fixes.\nOne decision waits.", "date": "2026-11-09"}})
	if err != nil || res.IsError {
		t.Fatalf("delivery: %v %+v", err, res)
	}
	if len(calls) != 2 || calls[1].params["body"] != "Handled two fixes.\nOne decision waits." || calls[1].params["date"] != "2026-11-09" {
		t.Fatalf("delivery called %v", calls)
	}
	receipt, _ := res.StructuredContent.(map[string]any)
	if receipt["item_id"] != "i_brief" || receipt["date"] != "2026-11-09" {
		t.Fatalf("delivery output = %+v", receipt)
	}
}
