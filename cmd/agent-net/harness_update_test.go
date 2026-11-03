package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestHarnessUpdateWide(t *testing.T) {
	var b bytes.Buffer
	printHarnessDetails(&b, []protocol.NodeView{{Hostname: "test", Harnesses: []protocol.HarnessInfo{
		{Name: "pi", Version: "1", Update: &protocol.HarnessUpdate{Result: "updated", CheckedAt: time.Now().Add(-3 * time.Hour)}},
		{Name: "codex", Version: "2", Update: &protocol.HarnessUpdate{Result: "failed", Detail: "broken"}},
	}}})
	for _, s := range []string{"1, updated 3h ago", "2, update failed: broken"} {
		if !strings.Contains(b.String(), s) {
			t.Fatalf("missing %q: %s", s, b.String())
		}
	}
}
