package mcpserver

import (
	"strings"
	"testing"
)

// The CTO changes the title on the owner's instruction, like the TUI form.
func TestCTOSettingsToolAcceptsTitle(t *testing.T) {
	def, ok := ctoTools["cto_settings"]
	if !ok {
		t.Fatal("cto_settings tool is missing")
	}
	field, ok := def.fields["title"]
	if !ok {
		t.Fatal("cto_settings has no title field")
	}
	if field.typ != "string" || !strings.Contains(strings.ToLower(field.desc), "screen") {
		t.Fatalf("title field = %+v", field)
	}
}
