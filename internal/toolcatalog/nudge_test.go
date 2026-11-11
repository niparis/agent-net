package toolcatalog

import "testing"

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
