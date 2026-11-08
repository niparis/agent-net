package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOCharterSetWithLevel(t *testing.T) {
	cs := ctoTestSession(t)
	for _, level := range []string{"global", "team", "repo", "agent"} {
		out, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "graph_change", Arguments: map[string]any{"action": "charter_set", "level": level, "node": "demo", "attr": "rules", "value": "plain English"}})
		if err != nil || out.IsError {
			t.Fatalf("%s: %+v %v", level, out, err)
		}
	}
}

func TestGraphReadPreservesEffectiveCharterSources(t *testing.T) {
	sources := protocol.CharterSources{"rules": {{Level: "team", Scope: "alpha", Value: "plain text"}}}
	nodes := roGraphNodes([]protocol.PublicGraphNode{{Charter: &protocol.Charter{Maturity: "mature", MaturityNote: "daily use", Rules: "plain text", Escalate: "team lead", Sources: &sources}}})
	raw, _ := json.Marshal(nodes)
	if !strings.Contains(string(raw), `"sources"`) || !strings.Contains(string(raw), `"rules":"plain text"`) {
		t.Fatalf("lost effective charter: %s", raw)
	}
}
