package tui

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestEndedVisibility(t *testing.T) {
	m := Model{}
	m.snap.Agents = []protocol.AgentView{{SessionID: "old", Harness: "claude", Cwd: "/work/project", LastSeen: time.Now().Add(-8 * 24 * time.Hour).Unix()}, {SessionID: "latest", Harness: "claude", Cwd: "/work/project", LastSeen: time.Now().Unix()}}
	if g := m.groups(); len(g) != 1 || len(g[0].agents) != 1 || g[0].agents[0].SessionID != "latest" {
		t.Fatalf("latest ended session missing: %+v", g)
	}
	m.filter = "old"
	if g := m.groups(); len(g) != 1 || g[0].agents[0].SessionID != "old" {
		t.Fatalf("filter missed offline session: %+v", g)
	}
}
func TestEndedResumeHint(t *testing.T) {
	if s := resumeHint(protocol.AgentView{Harness: "claude", Cwd: "/repo/.claude/worktrees/topic"}); !strings.Contains(s, "Enter resumes it in /repo/.claude/worktrees/topic") {
		t.Fatal(s)
	}
}
