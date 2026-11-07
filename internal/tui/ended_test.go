package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestEndedVisibility(t *testing.T) {
	m := newModel(120, 34)
	m.activeOnly, m.showOffline = false, true // all, which is where ended sessions show
	recent := func(id, project string, age time.Duration) protocol.AgentView {
		return protocol.AgentView{SessionID: id, Harness: "claude", Cwd: project, LastSeen: m.now().Add(-age).Unix()}
	}
	m.snap.Agents = []protocol.AgentView{
		recent("older", "/work/project", time.Hour),
		recent("latest", "/work/project", time.Minute),
		recent("expired", "/work/expired", 8*24*time.Hour),
		recent("hidden", "/work/hidden", time.Minute),
		recent("worker", "/work/worker", time.Minute),
		recent("bot", "/work/bot", time.Minute),
		recent("owner", "/work/owner", time.Minute),
		recent("dormant", "/work/dormant", time.Minute),
	}
	m.hiddenSessions["hidden"] = true
	m.snap.Agents[4].Worker = &protocol.WorkerInfo{}
	m.snap.Agents[5].Harness = "bot"
	m.snap.Agents[6].Harness = "owner"
	m.snap.Agents[7].Status = protocol.StatusDormant
	m.snap.Agents[7].Online = true
	ids := func() string {
		var ids []string
		for _, g := range m.groups() {
			for _, a := range g.agents {
				ids = append(ids, a.SessionID)
			}
		}
		return strings.Join(ids, ",")
	}
	if got := ids(); got == "" {
		t.Fatal("all showed no ended sessions")
	}
	for _, want := range []string{"older", "latest", "expired", "dormant"} {
		if !strings.Contains(ids(), want) {
			t.Errorf("all does not show %s: %s", want, ids())
		}
	}
	for _, gone := range []string{"hidden", "worker", "owner"} {
		if strings.Contains(ids(), gone) {
			t.Errorf("all shows %s: %s", gone, ids())
		}
	}
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "live", Harness: "claude", Cwd: "/work/project", Online: true})
	if got := ids(); !strings.Contains(got, "live") {
		t.Fatalf("all does not show the online project session: %s", got)
	}
	for _, filter := range []string{"older", "expired", "dormant"} {
		m.filter = filter
		if got := ids(); got != filter {
			t.Fatalf("filter %q: %s", filter, got)
		}
	}
	m.filter = "worker"
	if got := ids(); got != "" {
		t.Fatalf("ended worker: %s", got)
	}
}

func TestEndedWorktreesGroupedByProject(t *testing.T) {
	m := newModel(120, 34)
	m.activeOnly, m.showOffline = false, true // all, which is where ended sessions show
	m.snap.Agents = []protocol.AgentView{
		{SessionID: "older", NodeID: "node", Harness: "claude", RepoContext: "/repo/worktree-a", Repo: protocol.RepoInfo{Project: "project", Root: "/repo/worktree-a"}, LastSeen: m.now().Add(-time.Hour).Unix()},
		{SessionID: "newer", NodeID: "node", Harness: "claude", RepoContext: "/repo/worktree-b", Repo: protocol.RepoInfo{Project: "project", Root: "/repo/worktree-b"}, LastSeen: m.now().Add(-time.Minute).Unix()},
	}
	groups := m.groups()
	if len(groups) != 1 || len(groups[0].agents) != 2 {
		t.Fatalf("ended worktrees should share one project group, got %+v", groups)
	}
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "live", NodeID: "node", Harness: "claude", Online: true, Repo: protocol.RepoInfo{Project: "project", Root: "/repo/main"}})
	groups = m.groups()
	if len(groups) != 1 || len(groups[0].agents) != 3 {
		t.Fatalf("expected all three sessions in one project group, got %+v", groups)
	}
}

func TestEndedUnknownHarnessResumeHint(t *testing.T) {
	a := protocol.AgentView{Harness: "agy", NativeID: "session-id", Cwd: "/repo/worktree"}
	got := resumeHint(a)
	if got != "/repo/worktree: resume it from agy in this folder" {
		t.Fatalf("unexpected hint: %q", got)
	}
}

func TestEndedResumeHint(t *testing.T) {
	m := newModel(120, 34)
	a := protocol.AgentView{SessionID: "ended", Harness: "claude", Cwd: "/repo/.claude/worktrees/topic", LastSeen: m.now().Add(-12 * time.Minute).Unix()}
	m.snap.Agents = []protocol.AgentView{a}
	m.selected = a.SessionID
	got := strings.Join(m.detailLines(120, 20), "\n")
	if !strings.Contains(got, "Enter resumes it in") || !strings.Contains(got, a.Cwd) {
		t.Fatal(got)
	}
}

func TestEndedScene(t *testing.T) {
	m := newModel(120, 34)
	m.activeOnly, m.showOffline = false, true // all shows the ended session
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "ended-worktree", NodeID: "n_mac", Hostname: "Mac", Harness: "claude", Cwd: "/repo/.claude/worktrees/topic", Profile: protocol.Profile{Name: "office-politics"}, LastSeen: m.now().Add(-12 * time.Minute).Unix()})
	golden(t, "sessions_ended_120x34", m.testView())
}
