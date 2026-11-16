package toolcatalog

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/niparis/agent-net/internal/protocol"
)

// The skill archive read answer is part of the skill_archive output. Every
// field of the read result must be in the schema, or the CTO never sees it.
func TestSkillArchiveReadResultMatchesOutputSchema(t *testing.T) {
	tool, ok := toolNamed("skill_archive")
	if !ok {
		t.Fatal("catalog has no skill_archive")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
		t.Fatalf("output schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("output schema resolve: %v", err)
	}
	value := reflect.New(reflect.TypeOf(protocol.SkillArchiveReadResult{})).Elem()
	fillAll(t, value)
	raw, err := json.Marshal(value.Interface())
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(instance); err != nil {
		t.Fatalf("read result does not match the skill_archive output schema: %v\nvalue: %s", err, raw)
	}
}
