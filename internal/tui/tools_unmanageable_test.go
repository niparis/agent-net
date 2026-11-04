package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func unmanageableToolsFixture(w, h int) (Model, *fakeTools) {
	m, s := agentToolsFixture(w, h)
	bad := toolSkill("voice-builder", "", toolLoc(toolNodeR, "research", "/repo/housing/.agents/skills/voice-builder", "project", "housing", "codex"))
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
			if !strings.Contains(m.skillLockReason(row), "cannot manage:") {
				t.Fatal("retained error hidden")
			}
			row.Item.Locations[0].Reason = ""
			if reason := m.skillLockReason(row); reason != "" {
				t.Fatal("offline copy locked:", reason)
			}
			return
		}
	}
	t.Fatal("missing row")
}
