package daemon

import (
	"regexp"
	"testing"

	"github.com/niparis/agent-net/internal/mcpserver"
)

// Every tool the brief names must exist in the CTO tool list. Old names fail
// here, so a rename cannot leave the brief pointing at a removed tool.
func TestCTOBriefNamesOnlyExistingTools(t *testing.T) {
	names := map[string]bool{}
	for _, name := range mcpserver.CTOToolNames() {
		names[name] = true
	}
	re := regexp.MustCompile("`([^`]+)`")
	matches := re.FindAllStringSubmatch(ctoBrief, -1)
	if len(matches) == 0 {
		t.Fatal("the CTO brief names no tools")
	}
	for _, match := range matches {
		if !names[match[1]] {
			t.Errorf("the CTO brief names %q, which is not a CTO tool", match[1])
		}
	}
}
