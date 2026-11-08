package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestCTOConnectedAppsSchema(t *testing.T) {
	for _, raw := range []string{
		`{"action":"apps_list"}`, `{"action":"public_status"}`,
		`{"action":"app_grant","id":"app_fixture","grant":"mcp server fixture"}`,
		`{"action":"app_ungrant","id":"app_fixture","grant":"mcp server fixture"}`,
		`{"action":"app_rename","id":"app_fixture","name":"After"}`,
		`{"action":"app_revoke","id":"app_fixture"}`,
		`{"action":"public_on"}`, `{"action":"public_off"}`,
	} {
		if err := ValidateCTOActionSchema("mcp_change", json.RawMessage(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"action":"app_grant","id":"app_fixture"}`, `{"action":"app_ungrant","grant":"mcp server fixture"}`,
		`{"action":"app_rename","id":"app_fixture"}`, `{"action":"app_revoke"}`,
		`{"action":"public_on","id":"app_fixture"}`, `{"action":"apps_list","code":"1234"}`,
	} {
		if err := ValidateCTOActionSchema("mcp_change", json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
