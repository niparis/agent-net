package gateway

import (
	"errors"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func mcpSpec(name, node string) protocol.MCPServer {
	return protocol.MCPServer{Name: name, NodeID: node, Transport: protocol.MCPHTTP, URL: "https://h/mcp",
		Headers:     []protocol.MCPHeader{{Name: "Authorization", Env: "HOMELAB_TOKEN"}},
		AddedByNode: node, AddedBySession: "s_1", AddedByHarness: "claude",
		Tools: []protocol.MCPTool{{Name: "b"}, {Name: "a"}}, Health: protocol.HealthOK}
}

func TestMCPStore(t *testing.T) {
	s, _ := newTestStore(t)
	n1, _, _ := s.CreateNode("nas", "linux")
	n2, _, _ := s.CreateNode("laptop", "linux")

	if err := s.AddMCPServer(mcpSpec("homelab", n1)); err != nil {
		t.Fatal(err)
	}
	// Names are unique, and checked by the same rules as everywhere.
	err := s.AddMCPServer(mcpSpec("homelab", n2))
	var rj Rejection
	if !errors.As(err, &rj) || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate: %v", err)
	}
	if err := s.AddMCPServer(mcpSpec("Bad_Name", n1)); !errors.As(err, &rj) {
		t.Fatalf("bad name: %v", err)
	}

	got, ok, err := s.MCPServer("homelab")
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if got.Hostname != "nas" || got.NodeID != n1 || !got.Enabled || got.AddedBySession != "s_1" || got.AddedByHarness != "claude" ||
		got.Headers[0].Env != "HOMELAB_TOKEN" || got.URL != "https://h/mcp" || len(got.Tools) != 2 {
		t.Fatalf("stored = %+v", got)
	}

	// Reports update health and tools; a nil list keeps the cache.
	changed, err := s.SetMCPReport("homelab", protocol.HealthOK, "", []protocol.MCPTool{{Name: "a"}, {Name: "b"}})
	if err != nil || changed {
		t.Fatalf("same tools reported as changed (%v, %v)", changed, err)
	}
	changed, _ = s.SetMCPReport("homelab", protocol.HealthOK, "", []protocol.MCPTool{{Name: "a"}, {Name: "b"}, {Name: "c"}})
	if !changed {
		t.Fatal("new tool not noticed")
	}
	changed, _ = s.SetMCPReport("homelab", protocol.HealthError, "down", nil)
	got, _, _ = s.MCPServer("homelab")
	if changed || got.Health != protocol.HealthError || got.HealthDetail != "down" || len(got.Tools) != 3 {
		t.Fatalf("after failed check: %+v", got)
	}

	if ok, _ := s.SetMCPEnabled("homelab", false, n1, ""); !ok {
		t.Fatal("disable")
	}
	got, _, _ = s.MCPServer("homelab")
	if got.Enabled {
		t.Fatal("still enabled")
	}
	if ok, _ := s.SetMCPEnabled("nope", false, n1, ""); ok {
		t.Fatal("disabled a missing server")
	}

	list, _ := s.MCPServers()
	if len(list) != 1 {
		t.Fatalf("list = %d", len(list))
	}
	if ok, _ := s.RemoveMCPServer("homelab", n1, ""); !ok {
		t.Fatal("remove")
	}
	if _, ok, _ := s.MCPServer("homelab"); ok {
		t.Fatal("still there")
	}

	// The audit log names who did what and never holds a value.
	for _, kind := range []string{"mcp.added", "mcp.disabled", "mcp.removed"} {
		evs, _ := s.Events(kind)
		if len(evs) != 1 {
			t.Errorf("%s events = %v", kind, evs)
		}
	}
}

func TestNodeByHostOrID(t *testing.T) {
	s, _ := newTestStore(t)
	n1, _, _ := s.CreateNode("nas", "linux")
	s.CreateNode("twin", "linux")
	s.CreateNode("twin", "linux")
	if id, err := s.NodeByHostOrID("nas"); err != nil || id != n1 {
		t.Fatal(id, err)
	}
	if id, err := s.NodeByHostOrID(n1); err != nil || id != n1 {
		t.Fatal(id, err)
	}
	if _, err := s.NodeByHostOrID("twin"); err == nil || !strings.Contains(err.Error(), "node ID") {
		t.Fatalf("ambiguous host: %v", err)
	}
	if _, err := s.NodeByHostOrID("ghost"); err == nil {
		t.Fatal("unknown host accepted")
	}
}
