package toolcatalog

import (
	"reflect"
	"testing"
)

func TestSettingsReadSchemaOffersNamedTarget(t *testing.T) {
	tool, ok := toolNamed("agent_settings_read")
	if !ok {
		t.Fatal("agent_settings_read is not in the catalog")
	}
	obj := decodeObject(t, "agent_settings_read", tool.InputSchema)
	props := obj["properties"].(map[string]any)
	if _, ok := props["repo"]; !ok {
		t.Fatal("legacy repo field was removed")
	}
	target, ok := props["target"].(map[string]any)
	if !ok {
		t.Fatalf("target field missing: %v", props)
	}
	targetProps := target["properties"].(map[string]any)
	level := targetProps["level"].(map[string]any)
	if !reflect.DeepEqual(level["enum"], []any{"network", "repo", "agent"}) {
		t.Fatalf("target levels: %v", level)
	}
	if _, ok := targetProps["name"]; !ok {
		t.Fatal("target name missing")
	}
	if required := target["required"]; !reflect.DeepEqual(required, []any{"level"}) {
		t.Fatalf("target required: %v", required)
	}
}
