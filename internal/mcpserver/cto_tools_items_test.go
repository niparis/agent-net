package mcpserver

import (
	"encoding/json"
	"testing"
)

// The CTO reads the compact list through cto_op, so its schema must name the
// compact fields, or the CTO cannot know they exist.
func TestCTOOpToolsItemsSchemaListsCompactFields(t *testing.T) {
	op, ok := CTOOpByName("tools_items")
	if !ok {
		t.Fatal("tools_items is not a cto_op")
	}
	props, _ := op.Schema["properties"].(map[string]any)
	for _, field := range []string{"compact", "limit", "cursor", "name", "kind", "offset"} {
		if _, ok := props[field]; !ok {
			t.Errorf("tools_items schema has no %q field", field)
		}
	}
	cases := []struct {
		args  string
		valid bool
	}{
		{`{"compact":true,"limit":50,"cursor":"50","name":"wiki"}`, true},
		{`{"compact":true,"bogus":1}`, false},
	}
	for _, tc := range cases {
		err := ValidateCTOOpArgs(op, json.RawMessage(tc.args))
		if (err == nil) != tc.valid {
			t.Errorf("tools_items %s: err=%v, want valid=%v", tc.args, err, tc.valid)
		}
	}
}
