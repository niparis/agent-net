package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestPrintTools(t *testing.T) {
	var out bytes.Buffer
	printTools(&out, []protocol.InventoryReport{{Machine: "fixture", Items: []protocol.InventoryItem{{Name: "sample", Kind: protocol.InventorySkill, Harness: "codex", Scope: protocol.InventoryUser, State: protocol.InventoryOK}}}})
	for _, part := range []string{"NAME", "KIND", "HARNESS", "SCOPE", "PROJECT", "MACHINE", "STATE", "sample", "skill", "codex", "fixture"} {
		if !strings.Contains(out.String(), part) {
			t.Errorf("missing %q in %q", part, out.String())
		}
	}
}
