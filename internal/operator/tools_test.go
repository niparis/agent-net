package operator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolsMethodsPageAndCarryTheirParameters(t *testing.T) {
	d, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		switch method {
		case "hello":
			return map[string]any{}, ""
		case "tools_items":
			var p struct {
				Offset int `json:"offset"`
			}
			json.Unmarshal(params, &p)
			if p.Offset == 0 {
				return protocol.ToolsPage{Items: []protocol.ToolItem{{ID: "a", Name: "a"}}, Next: 1}, ""
			}
			return protocol.ToolsPage{Items: []protocol.ToolItem{{ID: "b", Name: "b"}}}, ""
		case "tools_accepted":
			return []string{"drift:x"}, ""
		case "tools_accept", "tools_import":
			return map[string]any{}, ""
		case "tools_plan":
			return protocol.ToolPlan{ID: "plan_1", Item: "a"}, ""
		case "tools_apply":
			return protocol.ToolApplyResult{Targets: []protocol.ToolTargetOutcome{{Machine: "m", State: "pending"}}}, ""
		}
		return nil, "unknown method"
	})
	c := New(path)
	defer c.Close()
	ctx := context.Background()
	items, err := c.ToolsItems(ctx)
	if err != nil || len(items) != 2 || items[1].ID != "b" {
		t.Fatalf("items %+v %v", items, err)
	}
	keys, err := c.ToolsAccepted(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("accepted %v %v", keys, err)
	}
	if err = c.ToolsAccept(ctx, "drift:x", true); err != nil {
		t.Fatal(err)
	}
	var acc protocol.ToolAccept
	json.Unmarshal(d.params["tools_accept"], &acc)
	if acc.Key != "drift:x" || !acc.Accepted {
		t.Errorf("accept params %+v", acc)
	}
	plan, err := c.ToolsPlan(ctx, protocol.ToolPlanRequest{Item: "a", Target: protocol.ToolAssignment{Scope: "global"}})
	if err != nil || plan.ID != "plan_1" {
		t.Fatalf("plan %+v %v", plan, err)
	}
	res, err := c.ToolsApply(ctx, plan.ID)
	if err != nil || len(res.Targets) != 1 || res.Targets[0].State != "pending" {
		t.Fatalf("apply %+v %v", res, err)
	}
	var pid protocol.ToolPlanID
	json.Unmarshal(d.params["tools_apply"], &pid)
	if pid.ID != "plan_1" {
		t.Errorf("apply params %+v", pid)
	}
	if err = c.ToolsImport(ctx, protocol.ToolImport{Item: "a", Location: protocol.ToolImportLocation{NodeID: "n", Location: "/x"}}); err != nil {
		t.Fatal(err)
	}
}
