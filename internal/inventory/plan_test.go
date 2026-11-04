package inventory

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolPlanInventorySafety(t *testing.T) {
	now := time.Now().UTC()
	good := protocol.InventoryItem{Kind: "skill", Name: "good", State: "ok"}
	bad := protocol.InventoryItem{Kind: "skill", Name: "bad", State: "error", Location: "/skills/bad", Reason: "unreadable file"}
	partial := protocol.InventoryReport{State: "error", Reason: "one or more sources could not be scanned", ScannedAt: now, Items: []protocol.InventoryItem{good, bad}}
	if reason := PlanReportProblem(partial); reason != "" {
		t.Fatal(reason)
	}
	for _, tc := range []struct {
		name, source string
		blocked      bool
	}{
		{"good", "/skills/good", false}, {"bad", "", true}, {"other", "/skills/bad", true}, {"other", "/skills/bad/sub", true}, {"other", "/skills/bad-prefix", false},
	} {
		if got := PlanItemProblem(partial, "skill", tc.name, tc.source); (got != "") != tc.blocked {
			t.Errorf("%+v: %s", tc, got)
		}
	}
	for _, tc := range []struct {
		name   string
		report protocol.InventoryReport
	}{
		{"missing", protocol.InventoryReport{}},
		{"old", protocol.InventoryReport{State: "ok", ScannedAt: now.Add(-time.Hour)}},
		{"registry unavailable", protocol.InventoryReport{State: "error", Reason: "project registry unavailable", ScannedAt: now, Items: partial.Items}},
		{"truncated", protocol.InventoryReport{State: "error", Reason: "inventory exceeds report limit", ScannedAt: now, Items: partial.Items}},
		{"no successful source", protocol.InventoryReport{State: "error", Reason: partial.Reason, ScannedAt: now, Items: []protocol.InventoryItem{bad}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if PlanReportProblem(tc.report) == "" {
				t.Fatal("unsafe report accepted")
			}
		})
	}
	partial.Items[1].Name = ""
	if got := PlanItemProblem(partial, "skill", "good"); !strings.Contains(got, "unreadable") {
		t.Fatal("unknown root may hide the requested skill:", got)
	}
	partial.Items[1] = bad
	partial.Items[1].Aliases = []string{"/alias"}
	if got := PlanItemProblem(partial, "skill", "other", "/alias"); got == "" {
		t.Fatal("alias collision accepted")
	}
}
