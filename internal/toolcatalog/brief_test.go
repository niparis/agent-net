package toolcatalog

import (
	"strings"
	"testing"
)

// Every caller reads the worker start description. It names the structured
// fields and the one refusal left (overlap). The six format
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
	for _, want := range []string{
		"structured brief", "problem and done required", "evidence", "hypothesis", "scope",
		"extra notes", "overlap ok:",
		"For a feature with several tasks, list the tasks first, then group them into sessions",
		"continue one session (continue) instead of starting new workers",
	} {
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
	for _, field := range []string{"problem", "done", "evidence", "hypothesis", "scope", "task"} {
		if props[field] == nil {
			t.Errorf("input has no %s field", field)
		}
	}
	if _, ok := props["mode"]; ok {
		t.Fatal("input still has the removed check mode")
	}
}

// The start result declares its brief flags, so the caller sees them, and the
// description says they never block a start.
func TestWorkerStartDeclaresBriefFlagsAsAdvisory(t *testing.T) {
	var found *Tool
	for i := range Tools {
		if Tools[i].Name == "agent_worker_start" {
			found = &Tools[i]
		}
	}
	if found == nil {
		t.Fatal("agent_worker_start missing")
	}
	if !strings.Contains(found.Description, "brief_flags never block") {
		t.Errorf("description does not say the brief flags never block: %s", found.Description)
	}
	output := decodeObject(t, found.Name, found.OutputSchema)
	props, _ := output["properties"].(map[string]any)
	flags, ok := props["brief_flags"].(map[string]any)
	if !ok {
		t.Fatal("output has no brief_flags")
	}
	items, _ := flags["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	for _, field := range []string{"check", "line", "reason"} {
		if itemProps[field] == nil {
			t.Errorf("brief_flags has no %s field", field)
		}
	}
}
