package toolcatalog

import (
	"strings"
	"testing"
)

// Every caller reads the worker start description. It names the one refusal
// left (overlap), the check mode, and the template as advice. The six format
// items are retired and must not be listed as a rule again (decision 0145).
func TestWorkerStartDescriptionNamesTheOverlapRefusalOnly(t *testing.T) {
	var found *Tool
	for i := range Tools {
		if Tools[i].Name == "agent_worker_start" {
			found = &Tools[i]
		}
	}
	if found == nil {
		t.Fatal("agent_worker_start missing")
	}
	for _, want := range []string{"overlap ok:", "docs/brief-template.md", "not a rule", `mode "check"`} {
		if !strings.Contains(found.Description, want) {
			t.Errorf("description does not contain %q: %s", want, found.Description)
		}
	}
	for _, gone := range []string{"Definition of done", "How you are graded", "Evidence (3+ lines)", "Files or areas", "Limits;"} {
		if strings.Contains(found.Description, gone) {
			t.Errorf("description still lists the retired format item %q: %s", gone, found.Description)
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
