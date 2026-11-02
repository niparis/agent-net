package mcpserver

import (
	"context"
	"strings"
	"testing"
)

func TestCTOGraphHasAPublicReadSource(t *testing.T) {
	def, ok := ctoTools["graph"]
	if !ok {
		t.Fatal("the CTO has no graph tool")
	}
	for _, field := range []string{"node", "offset", "limit"} {
		if _, ok := def.fields[field]; !ok {
			t.Errorf("graph has no %s field", field)
		}
	}
	cs := ctoTestSession(t)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "graph" {
			if !strings.Contains(strings.ToLower(tool.Description), "charter") {
				t.Errorf("CTO graph description: %s", tool.Description)
			}
			return
		}
	}
	t.Fatal("the CTO does not offer graph")
}
