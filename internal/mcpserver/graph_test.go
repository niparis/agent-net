package mcpserver

import (
	"context"
	"strings"
	"testing"
)

func TestCTOGraphHasAPublicReadSource(t *testing.T) {
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "agent_graph_read" {
			if !strings.Contains(strings.ToLower(tool.Description), "charter") {
				t.Errorf("CTO graph description: %s", tool.Description)
			}
			props, _ := ctoToolSchema(t, tool)
			for _, field := range []string{"node", "limit", "cursor", "view"} {
				if props[field] == nil {
					t.Errorf("agent_graph_read has no %s field", field)
				}
			}
			return
		}
	}
	t.Fatal("the CTO does not offer agent_graph_read")
}
