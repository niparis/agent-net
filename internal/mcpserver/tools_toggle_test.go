package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestToolToggleCTOSchema(t *testing.T) {
	for _, raw := range []string{`{"action":"toggle","item":"skill","project":"demo","on":false}`, `{"action":"new_projects","item":"skill","on":true}`} {
		if err := ValidateCTOActionSchema("tools_change", json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"action":"toggle","item":"skill","project":"demo"}`, `{"action":"toggle","item":"skill","on":true}`, `{"action":"new_projects","item":"skill","on":"yes"}`} {
		if err := ValidateCTOActionSchema("tools_change", json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
