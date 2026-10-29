package main

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMCPTarget(t *testing.T) {
	long := protocol.MCPServer{Transport: protocol.MCPStdio, Command: "/usr/bin/env", Args: strings.Fields(
		"PATH=/home/n/.local/bin:/usr/bin /home/n/.local/bin/just --justfile /home/n/strategies/justfile --working-directory /home/n/strategies library-mcp")}
	if got := mcpTarget(long, false); got != "just library-mcp" {
		t.Fatalf("short = %q", got)
	}
	if got := mcpTarget(long, true); !strings.HasPrefix(got, "/usr/bin/env PATH=") || !strings.HasSuffix(got, "library-mcp") {
		t.Fatalf("wide = %q", got)
	}
	plain := protocol.MCPServer{Transport: protocol.MCPStdio, Command: "/usr/local/bin/mytool"}
	if got := mcpTarget(plain, false); got != "mytool" {
		t.Fatalf("plain = %q", got)
	}
	npx := protocol.MCPServer{Transport: protocol.MCPStdio, Command: "npx", Args: []string{"-y", "pkg", "--root", "/tmp"}}
	if got := mcpTarget(npx, false); got != "npx /tmp" {
		t.Fatalf("npx = %q", got)
	}
	big := protocol.MCPServer{Transport: protocol.MCPStdio, Command: "x", Args: []string{strings.Repeat("a", 80)}}
	if got := mcpTarget(big, false); len(got) != targetMax || !strings.HasSuffix(got, "...") {
		t.Fatalf("big = %q", got)
	}
	u := protocol.MCPServer{Transport: protocol.MCPHTTP, URL: "https://nas.example/mcp"}
	if got := mcpTarget(u, false); got != u.URL {
		t.Fatalf("url = %q", got)
	}
}

func TestMCPAddedBy(t *testing.T) {
	cases := []struct {
		s    protocol.MCPServer
		want string
	}{
		{protocol.MCPServer{NodeID: "n_1", Hostname: "nas", AddedByNode: "n_1", AddedByHarness: "cli"}, "cli on nas"},
		{protocol.MCPServer{NodeID: "n_1", Hostname: "nas", AddedByNode: "n_2", AddedByHarness: "cli"}, "cli on n_2"},
		{protocol.MCPServer{AddedByHarness: "claude", AddedBySession: "s_9"}, "claude s_9"},
	}
	for _, c := range cases {
		if got := mcpAddedBy(c.s); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}
