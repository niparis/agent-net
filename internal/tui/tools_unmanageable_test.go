package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func unmanageableToolsFixture(w, h int) (Model, *fakeTools) {
	m, s := agentToolsFixture(w, h)
	bad := toolSkill("voice-builder", "", toolLoc(toolNodeR, "research", "/repo/housing/.agents/skills/voice-builder", "project", "homelab", "codex"))
	bad.State = protocol.InventoryError
	bad.Locations[0].Tracked = false
	bad.Locations[0].State = protocol.InventoryError
	bad.Locations[0].Reason = "a file is larger than the 64 MiB scan limit (voices/model.onnx, 114 MB)"
	s.items = append(s.items, bad)
	m = pressKey(m, "5")
	m = pressKey(m, "right")
	m = pressKey(m, "/")
	m = typeText(m, "voice-builder")
	m = pressKey(m, "enter")
	return m, s
}

func TestToolsUnmanageableSkillRowAndToggle(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m, s := unmanageableToolsFixture(width, 34)
			view := plain(m)
			if !strings.Contains(view, "cannot manage:") || !strings.Contains(view, "64 MiB") || !strings.Contains(view, "114 MB") {
				t.Fatal("missing cause:\n" + view)
			}
			rowView, _, _ := strings.Cut(view, "╭─Skill information")
			if !strings.Contains(rowView, "cannot manage:") {
				t.Fatal("no row explanation:\n" + rowView)
			}
			m = pressKey(m, " ")
			if len(s.toggles) > 0 || len(s.applied) > 0 || !strings.Contains(m.activeToast(), "cannot manage:") {
				t.Fatalf("unmanageable skill queued a change: %+v\n%s", s.toggles, plain(m))
			}
		})
	}
}

func TestToolsUnmanageableGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := unmanageableToolsFixture(size[0], size[1])
		golden(t, fmt.Sprintf("tools_unmanageable_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestToolsUnmanageableRetainedErrorAndOfflineCopy(t *testing.T) {
	m, _ := unmanageableToolsFixture(120, 34)
	row := m.agentSkills()[0]
	for _, item := range m.tl.items {
		if item.Name == row.Name {
			row.Item = item
			row.Variants = nil
			m.tl.items = nil
			row.Item.Locations[0].State = protocol.InventoryStale
			if !strings.Contains(m.skillLockReason(row, m.agentProject()), "cannot manage:") {
				t.Fatal("retained error hidden")
			}
			row.Item.Locations[0].Reason = ""
			if reason := m.skillLockReason(row, m.agentProject()); reason != "" {
				t.Fatal("offline copy locked:", reason)
			}
			return
		}
	}
	t.Fatal("missing row")
}

func TestToolsScanLockOnlyTouchesProjectCopies(t *testing.T) {
	for _, tc := range []struct {
		name, node, scope, project string
		shared, blocked            bool
	}{
		{name: "other machine", node: toolNodeM, scope: "user"},
		{name: "same machine user copy", node: toolNodeR, scope: "user", blocked: true},
		{name: "other project", node: toolNodeR, scope: "project", project: "other"},
		{name: "same project", node: toolNodeR, scope: "project", project: "homelab", blocked: true},
		{name: "project on both machines", node: toolNodeM, scope: "project", project: "homelab", shared: true, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, s := agentToolsFixture(120, 34)
			m.snap.Projects = []protocol.ProjectView{{Name: "homelab", NodeID: toolNodeR}, {Name: "other", NodeID: toolNodeM}}
			if tc.shared {
				m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: "homelab", NodeID: toolNodeM})
			}
			loc := toolLoc(tc.node, tc.node, "/fixture/archify", tc.scope, tc.project, "codex")
			loc.State = protocol.InventoryError
			loc.Reason = "unreadable file (fixture.txt)"
			loc.Tracked = false
			s.items[0].Locations = append(s.items[0].Locations, loc)
			m = pressKey(m, "5")
			m = pressKey(m, "right")
			m = pressKey(m, "/")
			m = typeText(m, "archify")
			m = pressKey(m, "enter")
			if got := strings.Contains(plain(m), "cannot manage:"); got != tc.blocked {
				t.Fatalf("lock=%v want %v:\n%s", got, tc.blocked, plain(m))
			}
			m = pressKey(m, " ")
			if got := len(s.toggles) > 0; got == tc.blocked {
				t.Fatalf("toggle queued=%v, blocked=%v:\n%s", got, tc.blocked, plain(m))
			}
		})
	}
}

func TestToolsScanLockDoesNotApplyToMCPRow(t *testing.T) {
	var m Model
	skill := toolSkill("shared-name", "hash", toolLoc(toolNodeR, "research", "/skills/shared-name", "user", "", "codex"))
	skill.Locations[0].State = protocol.InventoryError
	skill.Locations[0].Reason = "unreadable file (fixture.txt)"
	m.tl.items = []protocol.ToolItem{skill}
	row := toolsdiff.Row{Kind: protocol.InventoryMCP, Name: skill.Name}
	if reason := m.skillScanLockReason(row, ""); reason != "" {
		t.Fatal("MCP inherited skill failure:", reason)
	}
	if reason := m.toolAttentionReason(row); reason != "" {
		t.Fatal("MCP attention inherited skill failure:", reason)
	}
}
