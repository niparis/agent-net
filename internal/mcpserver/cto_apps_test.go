package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestCTOConnectedAppsOps(t *testing.T) {
	for _, tc := range []struct {
		method, args string
	}{
		{"apps_list", `{}`},
		{"public_status", `{}`},
		{"app_grant", `{"id":"app_fixture","grant":"mcp server fixture"}`},
		{"app_ungrant", `{"id":"app_fixture","grant":"mcp server fixture"}`},
		{"app_rename", `{"id":"app_fixture","name":"After"}`},
		{"app_revoke", `{"id":"app_fixture"}`},
		{"public_on", `{}`},
		{"public_off", `{}`},
	} {
		op, ok := CTOOpByName(tc.method)
		if !ok {
			t.Errorf("missing op %s", tc.method)
			continue
		}
		if err := ValidateCTOOpArgs(op, json.RawMessage(tc.args)); err != nil {
			t.Errorf("%s %s: %v", tc.method, tc.args, err)
		}
	}
	for _, tc := range []struct {
		method, args string
	}{
		{"app_grant", `{"id":"app_fixture"}`},
		{"app_ungrant", `{"grant":"mcp server fixture"}`},
		{"app_rename", `{"id":"app_fixture"}`},
		{"app_revoke", `{}`},
		{"public_on", `{"id":"app_fixture"}`},
		{"apps_list", `{"code":"1234"}`},
	} {
		op, ok := CTOOpByName(tc.method)
		if !ok {
			continue
		}
		if err := ValidateCTOOpArgs(op, json.RawMessage(tc.args)); err == nil {
			t.Errorf("accepted %s %s", tc.method, tc.args)
		}
	}
	// The typed tool no longer advertises or accepts the app actions.
	if err := ValidateCTOActionSchema("mcp_change", json.RawMessage(`{"action":"app_grant","id":"app_fixture","grant":"mcp server fixture"}`)); err == nil {
		t.Fatal("mcp_change still accepts app_grant")
	}
}
