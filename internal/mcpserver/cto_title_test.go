package mcpserver

import (
	"strings"
	"testing"
)

// The CTO changes the title on the owner's instruction, like the TUI form.
func TestCTOSettingsToolAcceptsTitle(t *testing.T) {
	def, ok := ctoTools["settings_change"]
	if !ok {
		t.Fatal("settings_change tool is missing")
	}
	field, ok := def.fields["title"]
	if !ok {
		t.Fatal("settings_change has no title field")
	}
	if field.typ != "string" || !strings.Contains(strings.ToLower(field.desc), "screen") {
		t.Fatalf("title field = %+v", field)
	}
}
