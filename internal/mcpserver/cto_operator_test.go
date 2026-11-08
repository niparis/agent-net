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
		raw, _ := json.Marshal(map[string]any{"area": "operator", "method": method, "arguments": map[string]any{}})
		if err := ValidateCTOActionSchema("settings_change", raw); err != nil {
			t.Errorf("%s: %v", method, err)
		}
	}
	for _, raw := range []string{
		`{"area":"operator","method":"apps.approve","arguments":{}}`,
		`{"area":"operator","method":"session_trust","arguments":{}}`,
		`{"area":"operator","method":"hidden_set"}`,
		`{"area":"operator","method":"hidden_set","arguments":[]}`,
		`{"area":"operator","method":"hidden_set","arguments":{},"owner":"forged"}`,
	} {
		if err := ValidateCTOActionSchema("settings_change", json.RawMessage(raw)); err == nil {
			t.Errorf("accepted invalid operator request %s", raw)
		}
	}
}

func TestCTOOperatorCatalogParity(t *testing.T) {
	var fields struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if entry.Name == "settings_change" {
			if err := json.Unmarshal(entry.InputSchema, &fields); err != nil {
				t.Fatal(err)
			}
		}
	}
	wanted := protocol.CTOOperatorMethods()
	for _, name := range fields.Properties["method"].Enum {
		if _, ok := wanted[name]; !ok {
			t.Errorf("catalog advertises unknown operation %s", name)
		}
		delete(wanted, name)
	}
	for name := range wanted {
		t.Errorf("catalog omits owner operation %s", name)
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
