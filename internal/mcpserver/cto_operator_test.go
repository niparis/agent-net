package mcpserver

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestCTOOperatorActionSchemas(t *testing.T) {
	for method := range protocol.CTOOperatorMethods() {
		op, ok := CTOOpByName(method)
		if !ok || (op.Method != method && op.Name != method) {
			t.Errorf("owner operation %s is not an op", method)
			continue
		}
		if op.Schema == nil {
			t.Errorf("%s has no schema", method)
		}
		if op.Example == "" {
			t.Errorf("%s has no example", method)
		}
	}
	cases := []struct {
		method, args string
		valid        bool
	}{
		{"charter_history", `{"node":"architect"}`, true},
		{"charter_history", `{"node":"architect","bogus":1}`, false},
		{"tools_export", `{"item":"item-id","version":"hash"}`, true},
		{"tools_export", `{"item":"item-id"}`, false},
		{"gateway_backup", `{"path":"/tmp/backup"}`, true},
		{"gateway_backup", `{}`, false},
		{"tools_versions", `{"item":"item-id","bogus":1}`, false},
		{"tools_import", `{"item":"item-id","location":{"node_id":"n_1","location":"/tmp/skill"}}`, true},
		{"tools_import", `{"item":"item-id"}`, false},
	}
	for _, tc := range cases {
		op, ok := CTOOpByName(tc.method)
		if !ok {
			t.Fatalf("missing op %s", tc.method)
		}
		err := ValidateCTOOpArgs(op, json.RawMessage(tc.args))
		if (err == nil) != tc.valid {
			t.Errorf("%s %s: err=%v, want valid=%v", tc.method, tc.args, err, tc.valid)
		}
	}
	if _, ok := CTOOpByName("apps.approve"); ok {
		t.Fatal("an operation outside the CTO allowlist is an op")
	}
}

func TestCTOOperatorCatalogParity(t *testing.T) {
	var fields struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if entry.Name == "settings_change" {
			if err := json.Unmarshal(entry.InputSchema, &fields); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, ok := fields.Properties["operator"]; ok {
		t.Fatal("settings_change still advertises operator")
	}
	if _, ok := fields.Properties["method"]; ok {
		t.Fatal("settings_change still advertises the operator method enum")
	}
	if _, ok := fields.Properties["arguments"]; ok {
		t.Fatal("settings_change still advertises operator arguments")
	}

	wanted := protocol.CTOOperatorMethods()
	seen := map[string]bool{}
	for _, op := range CTOOps() {
		name := op.Method
		if name == "" {
			if _, ok := wanted[op.Name]; !ok {
				continue // A merged op that is not an owner method.
			}
			name = op.Name
		}
		if _, ok := wanted[name]; !ok {
			t.Errorf("op advertises unknown owner operation %s", name)
		}
		seen[name] = true
	}
	for name := range wanted {
		if !seen[name] {
			t.Errorf("op list omits owner operation %s", name)
		}
	}
}

func TestCTOActionResultKeepsBackendStateFields(t *testing.T) {
	for _, backend := range []map[string]any{
		{"state": "open", "id": "ask_fixture", "question": "Approve Fixture?"},
		{"state": "ready", "plan_id": "plan_fixture", "targets": []any{}},
	} {
		result, ok := normalizeCTOProposalResult(backend).(map[string]any)
		if !ok {
			t.Fatal("missing action result")
		}
		body, ok := result["result"].(map[string]any)
		if !ok || !reflect.DeepEqual(body["data"], backend) {
			t.Fatalf("backend fields lost: %+v", result)
		}
	}
}

// A restart that already orphaned a session is repaired by the CTO with the
// owner operation session_replace, which is in the operator allowlist and the
// op tier. The typed session_change tool no longer lists it.
func TestCTOOperatorSessionReplaceListed(t *testing.T) {
	if _, ok := protocol.CTOOperatorMethods()["session_replace"]; !ok {
		t.Fatal("session_replace is not a CTO owner operation")
	}
	op, ok := CTOOpByName("session_replace")
	if !ok {
		t.Fatal("session_replace is not an op")
	}
	if op.Tool != "session_change" || op.Action != "replace" {
		t.Fatalf("session_replace routes to %s %s", op.Tool, op.Action)
	}
	if err := ValidateCTOOpArgs(op, json.RawMessage(`{"old_session":"s_old","new_session":"s_new"}`)); err != nil {
		t.Fatalf("the replace op was refused: %v", err)
	}
	if err := ValidateCTOOpArgs(op, json.RawMessage(`{"old_session":"s_old"}`)); err == nil {
		t.Fatal("the replace op accepted a missing new_session")
	}
	var input struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if entry.Name != "session_change" {
			continue
		}
		if err := json.Unmarshal(entry.InputSchema, &input); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range input.Properties["action"].Enum {
		if action == "replace" {
			t.Fatalf("session_change still lists the replace action: %v", input.Properties["action"].Enum)
		}
	}
}
