package mcpserver

import "testing"

func TestCTOPart9ReadTools(t *testing.T) {
	for _, name := range []string{"overview", "sessions", "session", "proposals", "feed", "messages", "graph", "machines", "projects", "workers", "tools", "settings"} {
		if _, ok := ctoTools[name]; !ok {
			t.Errorf("missing read tool %s", name)
		}
	}
}
