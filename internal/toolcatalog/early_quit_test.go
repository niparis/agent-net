package toolcatalog

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestMachineTrackEarlyQuitSchema(t *testing.T) {
	tool, ok := toolNamed("agent_machine_get")
	if !ok {
		t.Fatal("missing tool")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var output any
	if err := json.Unmarshal([]byte(`{"machine":{"id":"test","name":"test","online":true,"version":"test","harnesses":[],"sessions_online":0,"workers_running":0},"track_record":{"items":[{"harness":"codex","model":"test","runs":1,"complete":0,"partial":1,"failed":0,"early_quits":1}],"total":1}}`), &output); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(output); err != nil {
		t.Fatal(err)
	}
}
