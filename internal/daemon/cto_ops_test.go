package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOOpsHavePurposeSchemaExampleAndGate(t *testing.T) {
	gates := map[string]bool{"direct": true, "proposal": true, "irreversible": true}
	seen := map[string]bool{}
	for _, op := range mcpserver.CTOOps() {
		if seen[op.Name] {
			t.Errorf("%s is listed twice", op.Name)
		}
		seen[op.Name] = true
		if strings.TrimSpace(op.Purpose) == "" {
			t.Errorf("%s has no purpose", op.Name)
		}
		if op.Example == "" {
			t.Errorf("%s has no example", op.Name)
		}
		if op.Schema == nil {
			t.Errorf("%s has no schema", op.Name)
		}
		gate := ctoOpGate(op)
		if !gates[gate] {
			t.Errorf("%s has gate %q", op.Name, gate)
			continue
		}
		if op.Method != "" {
			if _, ok := protocol.CTOOperatorMethods()[op.Method]; !ok {
				t.Errorf("%s names unlisted owner operation %s", op.Name, op.Method)
			}
			if gate != "direct" {
				t.Errorf("%s owner operation has gate %s", op.Name, gate)
			}
			continue
		}
		if op.Action == "" {
			// A typed tool, such as step_report, has one class of its own.
			if _, ok := ctoToolClasses[op.Tool]; !ok {
				t.Errorf("%s runs unclassified tool %s", op.Name, op.Tool)
			}
			continue
		}
		class, ok := ctoActionClasses[op.Tool][op.Action]
		if !ok {
			t.Errorf("%s action %s.%s has no class", op.Name, op.Tool, op.Action)
			continue
		}
		if class == ctoProposal && gate != "proposal" {
			t.Errorf("%s proposal has gate %s", op.Name, gate)
		}
	}
	for name, want := range map[string]string{
		"apps_list":       "direct",
		"process_stop":    "irreversible",
		"app_revoke":      "irreversible",
		"node_remove":     "proposal",
		"machine_remove":  "proposal",
		"session_replace": "direct",
	} {
		op, ok := mcpserver.CTOOpByName(name)
		if !ok {
			t.Fatalf("missing op %s", name)
		}
		if got := ctoOpGate(op); got != want {
			t.Errorf("%s gate = %s, want %s", name, got, want)
		}
	}
}

func TestCTOOpsListGroupsAndFilter(t *testing.T) {
	groups := ctoOpsCatalog("")
	if len(groups) != len(ctoOpsGroupNames()) {
		t.Fatalf("groups = %+v", groups)
	}
	seen := map[string]bool{}
	for _, group := range groups {
		if len(group.Ops) == 0 {
			t.Errorf("empty group %s", group.Name)
		}
		for _, op := range group.Ops {
			seen[op.Name] = true
		}
	}
	for _, name := range []string{"apps_list", "process_stop", "node_remove", "mcp_import", "gateway_backup", "charter_history", "inbox_list", "session_replace", "step_report"} {
		if !seen[name] {
			t.Errorf("op list misses %s", name)
		}
	}
	filtered := ctoOpsCatalog("process")
	if len(filtered) != 1 || len(filtered[0].Ops) != 1 || filtered[0].Ops[0].Name != "process_stop" {
		t.Fatalf("filter process = %+v", filtered)
	}
}

func TestCTOOpResolvesToTheSamePathAsTheTypedTool(t *testing.T) {
	// Direct: apps_list runs mcp_change action apps_list.
	op, tool, args, err := ctoOpCall(json.RawMessage(`{"method":"apps_list","args":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if op.Name != "apps_list" || tool != "mcp_change" {
		t.Fatalf("direct route %s %s", op.Name, tool)
	}
	var direct map[string]any
	if err := json.Unmarshal(args, &direct); err != nil {
		t.Fatal(err)
	}
	if direct["action"] != "apps_list" {
		t.Fatalf("direct args %s", args)
	}
	concrete, _, action, err := ctoConcreteCall(tool, args)
	if err != nil || concrete != "apps.list" || action != "apps_list" {
		t.Fatalf("direct concrete %s %s: %v", concrete, action, err)
	}

	// Proposal: node_remove runs graph_change action node_remove and reaches
	// the same concrete call the typed tool used.
	op, tool, args, err = ctoOpCall(json.RawMessage(`{"method":"node_remove","args":{"name":"old-node","reason":"retired"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if op.Name != "node_remove" || tool != "graph_change" {
		t.Fatalf("proposal route %s %s", op.Name, tool)
	}
	class, err := ctoClass(tool, args)
	if err != nil || class != ctoProposal {
		t.Fatalf("proposal class %s: %v", class, err)
	}
	concrete, concreteArgs, action, err := ctoConcreteCall(tool, args)
	if err != nil || concrete != "node_remove" || action != "node_remove" {
		t.Fatalf("proposal concrete %s %s: %v", concrete, action, err)
	}
	var proposed map[string]any
	if err := json.Unmarshal(concreteArgs, &proposed); err != nil {
		t.Fatal(err)
	}
	if proposed["name"] != "old-node" || proposed["reason"] != "retired" {
		t.Fatalf("proposal args %s", concreteArgs)
	}
}

func TestCTOOpIncompleteIrreversibleRunsNothing(t *testing.T) {
	for _, raw := range []string{
		`{"method":"process_stop","args":{"node_id":"n_1"}}`,
		`{"method":"process_stop","args":{"node_id":"n_1","pids":[]}}`,
		`{"method":"app_revoke","args":{}}`,
		`{"method":"node_remove","args":{}}`,
	} {
		if _, _, _, err := ctoOpCall(json.RawMessage(raw)); err == nil {
			t.Errorf("%s ran", raw)
			continue
		} else if !strings.Contains(err.Error(), "Schema:") || !strings.Contains(err.Error(), "Example:") {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestCTOOpUnknownArgumentIsRefused(t *testing.T) {
	for _, raw := range []string{
		`{"method":"apps_list","args":{"bogus":true}}`,
		`{"method":"process_stop","args":{"node_id":"n_1","pids":[1],"bogus":true}}`,
	} {
		if _, _, _, err := ctoOpCall(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), "bogus") {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
