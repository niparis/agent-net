package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNodesWarningOutput(t *testing.T) {
	// Without warning
	var b1 bytes.Buffer
	info1 := protocol.NodesInfo{
		GatewayVersion: "1.0.0",
		Nodes: []protocol.NodeView{
			{Hostname: "host1", NodeID: "n_1", Version: "1.0.0", Online: true},
		},
	}
	if err := printNodes(&b1, info1, false); err != nil {
		t.Fatal(err)
	}
	out1 := b1.String()
	if strings.Contains(out1, "warning:") {
		t.Fatalf("expected no warning, got:\n%s", out1)
	}

	// With warning
	var b2 bytes.Buffer
	info2 := protocol.NodesInfo{
		GatewayVersion:  "1.0.0",
		GatewayWarnings: []string{"gateway reachable on public address 203.0.113.1"},
		Nodes: []protocol.NodeView{
			{Hostname: "host1", NodeID: "n_1", Version: "1.0.0", Online: true},
		},
	}
	if err := printNodes(&b2, info2, false); err != nil {
		t.Fatal(err)
	}
	out2 := b2.String()
	if !strings.Contains(out2, "warning: gateway reachable on public address 203.0.113.1") {
		t.Fatalf("expected warning in output, got:\n%s", out2)
	}
}
