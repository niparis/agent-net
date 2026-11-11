package toolcatalog

import (
	"strings"
	"testing"
)

// Every caller reads the worker start description, so the six brief items and
// the template pointer must be there, and mode check must be in the input.
func TestWorkerStartDescriptionHasTheBriefFormat(t *testing.T) {
	var found *Tool
	for i := range Tools {
		if Tools[i].Name == "agent_worker_start" {
			found = &Tools[i]
		}
	}
	if found == nil {
		t.Fatal("agent_worker_start missing")
	}
	for _, want := range []string{
		"Goal", "Files or areas", "Limits", "Definition of done", "How you are graded",
		"Evidence", "Overlap", "overlap ok:", "docs/brief-template.md", `mode "check"`,
	} {
		if !strings.Contains(found.Description, want) {
			t.Errorf("description does not contain %q: %s", want, found.Description)
		}
	}
	input := decodeObject(t, found.Name, found.InputSchema)
	props := input["properties"].(map[string]any)
	mode, ok := props["mode"].(map[string]any)
	if !ok {
		t.Fatal("input has no mode")
	}
	values, _ := mode["enum"].([]any)
	if len(values) != 1 || values[0] != "check" {
		t.Fatalf("mode enum = %v", mode["enum"])
	}
}
