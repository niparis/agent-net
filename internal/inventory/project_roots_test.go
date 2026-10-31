package inventory

import "testing"

func TestProjectRootsAvoidCompatibilityDuplicates(t *testing.T) {
	cases := []struct {
		h         string
		installed []string
		root      string
		conflict  bool
	}{
		{"codex", []string{"codex", "opencode", "pi"}, ".agents/skills", false},
		{"opencode", []string{"codex", "opencode", "pi"}, ".agents/skills", false},
		{"pi", []string{"codex", "opencode", "pi"}, ".agents/skills", false},
		{"claude", []string{"claude", "opencode"}, ".claude/skills", false},
		{"opencode", []string{"claude", "opencode"}, ".claude/skills", false},
		{"opencode", []string{"claude", "codex", "opencode"}, "", true},
	}
	for _, c := range cases {
		root, conflict := ProjectSkillRoot(c.h, c.installed)
		if root != c.root || conflict != c.conflict {
			t.Errorf("%s %v: %q %v", c.h, c.installed, root, conflict)
		}
	}
}
