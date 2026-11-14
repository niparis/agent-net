package mcpserver

import (
	"strings"
	"testing"
)

// The scope tools describe delegated authority as the daemon enforces it since
// decision 0166, not as stored data.
func TestScopeSetDescriptionSaysScopesAreEnforced(t *testing.T) {
	tool, ok := ctoConcreteTools["scope_set"]
	if !ok {
		t.Fatal("scope_set is missing from the CTO tools")
	}
	if !strings.Contains(tool.desc, "without asking the owner") {
		t.Fatalf("scope_set description still reads as stored data: %q", tool.desc)
	}
}
