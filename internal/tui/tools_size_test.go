package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func oversizedToolsFixture(w, h int) (Model, *fakeTools) {
	m, s := newToolsModel(w, h)
	large := toolSkill("oversize-archify", "large", toolLoc(toolNodeR, "research", "/tmp/skills/oversize-archify", "user", "", "codex"))
	large.StartTokens = 310
	large.Locations[0].Manifest = &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 1 << 20}, {Path: "data.bin", Size: 10 << 20}}}
	s.items = append(s.items, large)
	m = pressKey(m, "5")
	m = filterTable(m, "oversize")
	return m, s
}

// filterTable types a skill name filter into the table.
func filterTable(m Model, q string) Model {
	m = pressKey(m, "esc")
	m = pressKey(m, "/")
	m = typeText(m, q)
	return pressKey(m, "enter")
}

func TestTableOversizedSkillIsDimAndSpaceExplains(t *testing.T) {
	m, s := oversizedToolsFixture(120, 34)
	reason := "too large to manage (11 MB, the limit is 2 MB)"
	v := plain(m)
	if !strings.Contains(v, "×") || !strings.Contains(v, reason) {
		t.Fatalf("missing dim row and its reason:\n%s", v)
	}
	m = pressKey(m, " ")
	if len(s.toggles) != 0 || len(s.applied) != 0 {
		t.Fatalf("a locked skill asked the gateway: %v %v", s.toggles, s.applied)
	}
	if !strings.Contains(m.activeToast(), reason) {
		t.Fatalf("space must explain the lock: %q", m.activeToast())
	}
}

func TestTableOversizedGoldens(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m, _ := oversizedToolsFixture(size[0], size[1])
		golden(t, fmt.Sprintf("tools_table_oversized_%dx%d", size[0], size[1]), m.View())
	}
}

func TestToolsSkillSizeManifestRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  []protocol.SkillFile
		copies int
		want   string
	}{
		{"at limit", []protocol.SkillFile{{Size: 2 << 20}}, 1, ""},
		{"over limit", []protocol.SkillFile{{Size: 2 << 20}, {Size: 1}}, 1, "too large"},
		{"decimal size", []protocol.SkillFile{{Size: 47 * (1 << 20) / 10}}, 1, "4.7 MB"},
		{"duplicate copies", []protocol.SkillFile{{Size: 3 * (1 << 20) / 2}}, 2, ""},
		{"no manifest", nil, 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := toolSkill("fixture", "hash")
			for i := 0; i < tc.copies; i++ {
				loc := toolLoc(toolNodeR, "research", "/tmp/fixture", "user", "", "codex")
				if tc.files != nil {
					loc.Manifest = &protocol.SkillManifest{Files: tc.files}
				}
				item.Locations = append(item.Locations, loc)
			}
			got := skillSizeReason(skillItemBytes(item))
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A big skill whose scan stopped listing files is still locked.
func TestTableSkillMarkedTooLargeByTheScanIsLocked(t *testing.T) {
	m, s := newToolsModel(120, 34)
	large := toolSkill("big-skill", "large", toolLoc(toolNodeR, "research", "/tmp/skills/big-skill", "user", "", "codex"))
	large.Locations[0].Manifest = &protocol.SkillManifest{TooLarge: true}
	s.items = append(s.items, large)
	m = pressKey(m, "5")
	m = filterTable(m, "big-skill")
	if !strings.Contains(plain(m), "too large to manage (over 200 files or 2 MB)") {
		t.Fatal("missing lock for a skill the scan marked too large:\n" + plain(m))
	}
	m = pressKey(m, " ")
	if len(s.toggles) != 0 {
		t.Fatal("a skill marked too large asked the gateway")
	}
}

// A plugin or system skill cannot be switched off either.
func TestTablePluginSkillIsDim(t *testing.T) {
	m, s := newToolsModel(120, 34)
	plugin := toolSkill("plugin-skill", "plug", toolLoc(toolNodeR, "research", "/opt/plugin/plugin-skill", "user", "", "claude"))
	plugin.Locations[0].Source = protocol.InventoryPlugin
	s.items = append(s.items, plugin)
	m = pressKey(m, "5")
	m = filterTable(m, "plugin-skill")
	if !strings.Contains(plain(m), "a plugin or system skill; agent-net leaves it alone") {
		t.Fatalf("missing plugin reason:\n%s", plain(m))
	}
	m = pressKey(m, " ")
	if len(s.toggles) != 0 {
		t.Fatal("a plugin skill asked the gateway")
	}
}
