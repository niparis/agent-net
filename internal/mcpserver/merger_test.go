package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestMergerCTOActionsAdvertised(t *testing.T) {
	for _, e := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if e.Name != "session_change" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(e.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"merger_start", "merger_stop"} {
			found := false
			for _, action := range schema.Properties["action"].Enum {
				if action == want {
					found = true
				}
			}
			if !found {
				t.Errorf("missing advertised action %s", want)
			}
		}
	}
	for _, raw := range []string{`{"action":"merger_start","project":"garden"}`, `{"action":"merger_stop","project":"garden"}`} {
		if err := ValidateCTOActionSchema("session_change", json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateCTOActionSchema("session_change", json.RawMessage(`{"action":"merger_start"}`)); err == nil {
		t.Fatal("merger without project")
	}
}
