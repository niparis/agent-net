package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNudgeStatsSettingsTable(t *testing.T) {
	m := Model{nudgeStats: protocol.NudgeStatsResult{
		Since: "2026-11-11T00:00:00Z", Fires: 14, Followed: 2, UsefulRate: 0.1429,
		Kinds: []protocol.NudgeStat{
			{Kind: "idle_check", Agent: "a1", AgentName: "chief", Fires: 9, Followed: 1, UsefulRate: 0.1111},
			{Kind: "wake_failed", Agent: "a2", AgentName: "worker", Fires: 5, Followed: 1, UsefulRate: 0.2},
		},
	}}
	items := m.settingsItems()
	var nudge []string
	for _, item := range items {
		if item.section == 8 {
			nudge = append(nudge, item.text)
		}
	}
	if len(nudge) != 3 {
		t.Fatalf("nudge rows = %v", nudge)
	}
	if !strings.Contains(nudge[0], "useful 2 of 14") || !strings.Contains(nudge[0], "14%") {
		t.Errorf("total line = %q", nudge[0])
	}
	if !strings.Contains(nudge[1], "idle_check") || !strings.Contains(nudge[1], "chief") || !strings.Contains(nudge[1], "1/9") {
		t.Errorf("first row = %q", nudge[1])
	}
	if !strings.Contains(nudge[2], "wake_failed") {
		t.Errorf("second row = %q", nudge[2])
	}
}
