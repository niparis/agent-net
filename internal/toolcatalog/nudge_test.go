package toolcatalog

import (
	"strings"
	"testing"
)

func TestNudgeStatsRolesAndHelper(t *testing.T) {
	tool, ok := toolNamed("agent_nudge_stats")
	if !ok {
		t.Fatal("agent_nudge_stats is not in the catalog")
	}
	if len(tool.Roles) != 1 || tool.Roles[0] != RoleAgent {
		t.Fatalf("roles = %v", tool.Roles)
	}
	if !tool.Hints.ReadOnly {
		t.Fatal("agent_nudge_stats is not read-only")
	}
	if !strings.Contains(tool.Description, "30 minutes") {
		t.Fatalf("description does not name the follow window: %q", tool.Description)
	}
}

func TestNudgeSchemaRoles(t *testing.T) {
	for _, name := range []string{"agent_nudge_pause", "agent_nudge_resume", "agent_nudge_pauses_find"} {
		tool, ok := toolNamed(name)
		if !ok {
			t.Errorf("missing %s", name)
			continue
		}
		if len(tool.Roles) != 1 || tool.Roles[0] != RoleAgent {
			t.Errorf("%s roles=%v", name, tool.Roles)
		}
	}
}
