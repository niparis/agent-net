package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestCTOConnectedAppsOps(t *testing.T) {
	for _, name := range []string{
		"apps_list",
		"public_status",
		"app_grant",
		"app_ungrant",
		"app_rename",
		"app_revoke",
		"public_on",
		"public_off",
	} {
		if _, ok := CTOOpByName(name); ok {
			t.Errorf("%s should not be a cto_op; it belongs to mcp_change", name)
		}
	}
	// The typed tool mcp_change accepts the app actions.
	if err := ValidateCTOActionSchema("mcp_change", json.RawMessage(`{"action":"app_grant","id":"app_fixture","grant":"mcp server fixture"}`)); err != nil {
		t.Fatalf("mcp_change refused valid app_grant: %v", err)
	}
	if err := ValidateCTOActionSchema("mcp_change", json.RawMessage(`{"action":"apps_list"}`)); err != nil {
		t.Fatalf("mcp_change refused valid apps_list: %v", err)
	}
}
