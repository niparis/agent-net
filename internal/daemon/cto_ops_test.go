package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOOpsHavePurposeSchemaExampleAndGate(t *testing.T) {
	gates := map[string]bool{"direct": true, "owner_words": true, "irreversible": true}
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
		if class == ctoOwnerOnly && gate != "owner_words" {
			t.Errorf("%s owner-only op has gate %s", op.Name, gate)
		}
	}
	for name, want := range map[string]string{
		"gateway_backup":  "direct",
		"charter_history": "direct",
		"inbox_list":      "direct",
		"step_report":     "direct",
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
	for _, name := range []string{"gateway_backup", "charter_history", "inbox_list", "step_report", "chief_set", "permission_apply"} {
		if !seen[name] {
			t.Errorf("op list misses %s", name)
		}
	}
	filtered := ctoOpsCatalog("backup")
	if len(filtered) != 1 || len(filtered[0].Ops) != 1 || filtered[0].Ops[0].Name != "gateway_backup" {
		t.Fatalf("filter backup = %+v", filtered)
	}
}

func TestCTOOpResolvesToTheSamePathAsTheTypedTool(t *testing.T) {
	// step_report runs agent_step_report.
	op, tool, args, err := ctoOpCall(json.RawMessage(`{"method":"step_report","args":{"change":"ch_1","step":"review","result":"pass","commit":"0123456789012345678901234567890123456789"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if op.Name != "step_report" || tool != "agent_step_report" {
		t.Fatalf("step_report route %s %s", op.Name, tool)
	}
	var stepArgs map[string]any
	if err := json.Unmarshal(args, &stepArgs); err != nil {
		t.Fatal(err)
	}
	if stepArgs["change"] != "ch_1" {
		t.Fatalf("step args %s", args)
	}

	// Owner ops run settings_change area operator.
	op, tool, args, err = ctoOpCall(json.RawMessage(`{"method":"gateway_backup","args":{"path":"/tmp/bak"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if op.Name != "gateway_backup" || tool != "settings_change" {
		t.Fatalf("owner op route %s %s", op.Name, tool)
	}
	var ownerArgs map[string]any
	if err := json.Unmarshal(args, &ownerArgs); err != nil {
		t.Fatal(err)
	}
	if ownerArgs["area"] != "operator" || ownerArgs["method"] != "gateway_backup" {
		t.Fatalf("owner args %s", args)
	}
}

func TestCTOOpIncompleteIrreversibleRunsNothing(t *testing.T) {
	for _, raw := range []string{
		`{"method":"permission_apply","args":{"rules":["Bash(make:*)"],"targets":["demo"]}}`,
		`{"method":"bar_edit","args":{"add":["Rule."]}}`,
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
		`{"method":"chief_set","args":{"bogus":true}}`,
		`{"method":"bar_edit","args":{"project":"demo","bogus":true}}`,
	} {
		if _, _, _, err := ctoOpCall(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), "bogus") {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestCTOOpsListPinsNoAliases(t *testing.T) {
	aliases := []string{
		"apps_list", "app_grant", "app_ungrant", "app_rename", "app_revoke",
		"public_status", "public_on", "public_off", "machine_remove",
		"machine_rotate", "process_stop", "session_replace", "node_remove",
		"repo_archive", "agent_tombstone",
	}
	for _, alias := range aliases {
		if _, ok := mcpserver.CTOOpByName(alias); ok {
			t.Errorf("cto_ops still includes moved alias %s", alias)
		}
	}
	ops := mcpserver.CTOOps()
	if len(ops) != 99 {
		t.Fatalf("expected 99 ops, got %d", len(ops))
	}
}
