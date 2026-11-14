package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// quietModel is one idle session that last did something at the given age.
// It shows the Groups view, where the Working and Idle bands are drawn.
func quietModel(age time.Duration, online bool) Model {
	snap := Snapshot{Connected: true, Agents: []protocol.AgentView{{
		SessionID: "quiet", NodeID: "one", Hostname: "desk", Online: online,
		Status: "idle", Harness: "claude", LastActive: t0.Add(-age).Unix(),
		Profile: protocol.Profile{Name: "quiet"},
	}}}
	m := treeModel(120, 34, snap)
	m.groupedSessions = true
	return m
}

// A session that did something in the last hour stays in the Working band.
// After an hour of quiet it drops to Idle.
func TestRecentActivityKeepsSessionWorking(t *testing.T) {
	if got := groupOf(quietModel(59*time.Minute, true), "quiet").host; got != "Working" {
		t.Fatalf("59 minutes quiet: group %q, want Working", got)
	}
	m := quietModel(61*time.Minute, true)
	m.activeOnly = false
	if got := groupOf(m, "quiet").host; got != "Idle" {
		t.Fatalf("61 minutes quiet: group %q, want Idle", got)
	}
	m.activeOnly = true
	if strings.Contains(strings.Join(names(m), ","), "quiet") {
		t.Fatalf("quiet session not folded: %v", names(m))
	}
	if view := ansi.Strip(m.testView()); !strings.Contains(view, "1 more idle · v to show") {
		t.Fatalf("missing idle fold:\n%s", view)
	}
}

// A live session stays in Working until an hour after its real last activity,
// whatever its harness, status and place in a caller tree. AgentView.LastActive
// is that real activity: the daemon fills it from the harness's own output as
// well as from turn boundaries, so a missed turn start or end does not make a
// session that just worked look unused.
func TestEveryHarnessStaysWorkingForAnHour(t *testing.T) {
	for _, tc := range []struct {
		harness, status string
	}{
		{"claude", "idle"}, {"claude", "shell"}, {"codex", "idle"},
		{"opencode", "idle"}, {"pi", "idle"}, {"agy", "idle"},
	} {
		t.Run(tc.harness+"-"+tc.status, func(t *testing.T) {
			root := protocol.AgentView{SessionID: "s_root", NodeID: "one", Hostname: "desk", Harness: tc.harness,
				Online: true, Status: tc.status, LastActive: t0.Add(-24 * time.Minute).Unix(), LastSeen: t0.Unix(),
				Profile: protocol.Profile{Name: "root"}}
			// A worker under the root is old and must not decide the band.
			kid := protocol.AgentView{SessionID: "s_kid", NodeID: "one", Hostname: "desk", Harness: "opencode",
				Online: true, Status: "idle", LastActive: t0.Add(-2 * time.Hour).Unix(), LastSeen: t0.Unix(),
				Profile: protocol.Profile{Name: "kid"}, Worker: &protocol.WorkerInfo{ID: "w_kid", Owner: "s_root"}}
			m := treeModel(120, 40, Snapshot{Connected: true, Agents: []protocol.AgentView{root, kid}})
			m.groupedSessions = true
			if got := groupOf(m, "s_root").host; got != "Working" {
				t.Fatalf("24 minutes after activity: group %q, want Working", got)
			}

			// The same session quiet for over an hour is Idle.
			root.LastActive = t0.Add(-61 * time.Minute).Unix()
			m = treeModel(120, 40, Snapshot{Connected: true, Agents: []protocol.AgentView{root}})
			m.activeOnly, m.groupedSessions = false, true
			if got := groupOf(m, "s_root").host; got != "Idle" {
				t.Fatalf("61 minutes after activity: group %q, want Idle", got)
			}
		})
	}
}

// Being offline is never recent, however fresh the last activity time is.
func TestOfflineSessionIsNeverRecent(t *testing.T) {
	m := quietModel(time.Minute, false)
	m.activeOnly, m.showOffline = false, true
	if got := groupOf(m, "quiet").host; got != "Idle" {
		t.Fatalf("offline session with recent activity: group %q, want Idle", got)
	}
}

func TestRecentActivityGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		snap := Snapshot{Connected: true, Agents: []protocol.AgentView{
			{SessionID: "recent", NodeID: "one", Hostname: "desk", Harness: "claude", Online: true, Status: "idle",
				LastActive: t0.Add(-59 * time.Minute).Unix(), Profile: protocol.Profile{Name: "recent", Task: "just finished a turn"}},
			{SessionID: "older", NodeID: "two", Hostname: "build", Harness: "codex", Online: true, Status: "idle",
				LastActive: t0.Add(-61 * time.Minute).Unix(), Profile: protocol.Profile{Name: "older", Task: "quiet for over an hour"}},
		}}
		m := treeModel(size[0], size[1], snap)
		m.width, m.height = size[0], size[1]
		m.activeOnly, m.showOffline = true, false
		m.haveData, m.problem = true, ProblemNone
		m.snap.Connected = true
		m.snap.Nodes = []protocol.NodeView{
			{NodeID: "one", Hostname: "desk", Online: true},
			{NodeID: "two", Hostname: "build", Online: true},
		}
		for i := range m.snap.Agents {
			m.snap.Agents[i].Model = "gpt-6.1-sol"
		}
		m.selected = "recent"
		m.ensureSelection()
		golden(t, fmt.Sprintf("sessions_recent_activity_%dx%d", size[0], size[1]), m.testView())
	}
}
