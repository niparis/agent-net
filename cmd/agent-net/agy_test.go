package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// nodes --wide shows the agy row like the other harnesses: version, models
// grouped by vendor, and sign-in state (decision 0049, build step 2).
func TestAgyWideRow(t *testing.T) {
	var b bytes.Buffer
	printHarnessDetails(&b, []protocol.NodeView{{Hostname: "research", Harnesses: []protocol.HarnessInfo{{
		Name: "agy", Path: "/home/fixture/.local/bin/agy", Version: "1.4.12", SignedIn: "yes",
		Models: []string{"claude-sonnet-5-5-medium", "gemini-3.8-flash-low", "gemini-4-argon-high", "gpt-oss-120b-medium"},
	}}}})
	out := b.String()
	for _, want := range []string{"research", "agy", "1.4.12", "4 (anthropic, google, openai)", "/home/fixture/.local/bin/agy (signed in)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	t.Log("\n" + out)
}
