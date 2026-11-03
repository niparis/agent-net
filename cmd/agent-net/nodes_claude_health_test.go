package main

import (
	"bytes"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestNodesWideClaudeDeliveryWarning(t *testing.T) {
	const warning = "Claude delivery broken by Claude Code test"
	var b bytes.Buffer
	info := protocol.NodesInfo{Nodes: []protocol.NodeView{{Hostname: "box", Harnesses: []protocol.HarnessInfo{{Name: "claude", DeliveryWarning: warning}}}}}
	if err := printNodes(&b, info, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), warning) {
		t.Fatal(b.String())
	}
}
