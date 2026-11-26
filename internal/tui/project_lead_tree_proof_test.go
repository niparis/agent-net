package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// homelabTreeFixture mirrors the homelab shape as the gateway places it
// (decision 0193): two live sessions holding the lead agent_id (one pinned as
// lead, one not), two member sessions whose Agent has no lead session, a
// worker started by the non-lead session, and a worker started by the lead.
func homelabTreeFixture() Snapshot {
	const (
		leadID    = "s_dab96cf4869f0e75"
		storageID = "s_b533ad3358f77cbf"
		member2ID = "s_b03b3ac1a0d365fa"
		member3ID = "s_8c7c5eab6beffb76"
		wTruenas  = "s_0000000000000901"
		wLead     = "s_0000000000000902"
	)
	return Snapshot{
		Connected: true,
		NodeID:    "n_homelab",
		Nodes: []protocol.NodeView{
			{NodeID: "n_homelab", Hostname: "homelab", Online: true, Version: "v0.5.0"},
		},
		Agents: []protocol.AgentView{
			agent(leadID, "n_homelab", "homelab", "claude", "homelab-agent - lead", func(a *protocol.AgentView) {
				a.AgentID = "homelab-lead"
				a.AgentLeadSession = leadID
				a.Repo = protocol.RepoInfo{Project: "homelab-agent", Root: "/srv/homelab"}
				a.RepoLead = true
				a.RepoLeadExplicit = true
				a.ReportsTo = protocol.CTONodeName
				a.FirstSeen = t0.Add(-2 * time.Hour).Unix()
				a.LastActive = t0.Add(-5 * time.Minute).Unix()
			}),
			agent(storageID, "n_homelab", "homelab", "claude", "homelab - storage copies", func(a *protocol.AgentView) {
				a.AgentID = "homelab-lead"
				a.AgentLeadSession = leadID
				a.Repo = protocol.RepoInfo{Project: "homelab-agent", Root: "/srv/homelab"}
				a.ReportsTo = protocol.CTONodeName
				a.Parent = leadID
				a.FirstSeen = t0.Add(-90 * time.Minute).Unix()
				a.LastActive = t0.Add(-4 * time.Minute).Unix()
			}),
			agent(member2ID, "n_homelab", "homelab", "claude", "homelab-agent 2", func(a *protocol.AgentView) {
				a.AgentID = "homelab-agent"
				a.Repo = protocol.RepoInfo{Project: "homelab-agent", Root: "/srv/homelab"}
				a.ReportsTo = "homelab-lead"
				// homelab-agent has no lead session: the gateway draws it under
				// the project lead with the marker (decision 0193).
				a.Parent, a.NoParent = leadID, "no lead"
				a.FirstSeen = t0.Add(-80 * time.Minute).Unix()
				a.LastActive = t0.Add(-3 * time.Minute).Unix()
			}),
			agent(member3ID, "n_homelab", "homelab", "claude", "homelab-agent 3", func(a *protocol.AgentView) {
				a.AgentID = "homelab-agent"
				a.Repo = protocol.RepoInfo{Project: "homelab-agent", Root: "/srv/homelab"}
				a.ReportsTo = "homelab-lead"
				// homelab-agent has no lead session: the gateway draws it under
				// the project lead with the marker (decision 0193).
				a.Parent, a.NoParent = leadID, "no lead"
				a.FirstSeen = t0.Add(-70 * time.Minute).Unix()
				a.LastActive = t0.Add(-2 * time.Minute).Unix()
			}),
			agent(wTruenas, "n_homelab", "homelab", "opencode", "truenas-stacks worker", func(a *protocol.AgentView) {
				a.Status = "busy"
				a.Repo = protocol.RepoInfo{Project: "homelab-agent", Root: "/srv/homelab"}
				a.Worker = &protocol.WorkerInfo{ID: "w_truenas", Owner: storageID, Project: "homelab-agent", StartedAt: t0.Add(-10 * time.Minute).Unix(), Task: "truenas-stacks"}
				a.Parent = storageID
				a.FirstSeen = t0.Add(-10 * time.Minute).Unix()
				a.LastActive = t0.Add(-1 * time.Minute).Unix()
			}),
			agent(wLead, "n_homelab", "homelab", "opencode", "homelab worker 1", func(a *protocol.AgentView) {
				a.Status = "busy"
				a.Repo = protocol.RepoInfo{Project: "homelab-agent", Root: "/srv/homelab"}
				a.Worker = &protocol.WorkerInfo{ID: "w_lead", Owner: leadID, Project: "homelab-agent", StartedAt: t0.Add(-8 * time.Minute).Unix(), Task: "backup check"}
				a.Parent = leadID
				a.FirstSeen = t0.Add(-8 * time.Minute).Unix()
				a.LastActive = t0.Unix()
			}),
		},
	}
}

// TestHomelabSingleProjectRoot verifies that a project with a pinned lead shows
// exactly one root: the pinned lead. A non-lead session that still holds the
// lead agent_id sits under the pinned lead, and its workers stay under it.
// Frames at 80x24 and 120x34 go to AGENT_NET_PROOF_DIR when set.
func TestHomelabSingleProjectRoot(t *testing.T) {
	const (
		leadID    = "s_dab96cf4869f0e75"
		storageID = "s_b533ad3358f77cbf"
		member2ID = "s_b03b3ac1a0d365fa"
		member3ID = "s_8c7c5eab6beffb76"
		wTruenas  = "s_0000000000000901"
		wLead     = "s_0000000000000902"
	)
	snap := homelabTreeFixture()
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		w, h := size[0], size[1]
		src := &fakeSource{snap: snap}
		m := New(src)
		m.activeOnly, m.showOffline = false, true
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
		m = settle(refresh(m, src))

		if dir := os.Getenv("AGENT_NET_PROOF_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			frame := ansi.Strip(m.testView())
			label := os.Getenv("AGENT_NET_PROOF_LABEL")
			if label == "" {
				label = "after"
			}
			capture := fmt.Sprintf("build: %s\nstep: open the sessions tree (%s) at %dx%d\naction: open the TUI with homelab lead, non-lead holding lead agent_id, members and workers\nexpected: one homelab root (homelab-agent - lead) with all members and workers nested underneath\nerrors: none\n\n%s\n",
				os.Getenv("AGENT_NET_PROOF_BUILD"), label, w, h, frame)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("homelab-tree-%s-%dx%d.txt", label, w, h)), []byte(capture), 0600); err != nil {
				t.Fatal(err)
			}
		}

		groups := m.groups()
		if len(groups) != 1 {
			t.Fatalf("%dx%d: got %d groups, want 1", w, h, len(groups))
		}
		g := groups[0]
		var roots []string
		for _, a := range g.agents {
			if g.marks[a.SessionID].prefix == "" {
				roots = append(roots, a.SessionID)
			}
		}
		if len(roots) != 1 || roots[0] != leadID {
			t.Errorf("%dx%d: roots = %v, want only pinned lead [%s]", w, h, roots, leadID)
			continue
		}
		for _, child := range []string{storageID, member2ID, member3ID, wLead} {
			mk := g.marks[child]
			if mk.root != leadID || mk.prefix == "" {
				t.Fatalf("%dx%d: %s mark = %+v, want child of %s", w, h, child, mk, leadID)
			}
		}
		// truenas-stacks worker stays under storageID (depth 2 under leadID).
		order := agentIDs(g.agents)
		storageAt := indexOfID(order, storageID)
		truenasAt := indexOfID(order, wTruenas)
		if storageAt < 0 || truenasAt != storageAt+1 {
			t.Fatalf("%dx%d: truenas worker not directly under storage session: order=%v", w, h, order)
		}
		if mk := g.marks[wTruenas]; mk.root != leadID || len(mk.prefix) <= len("└─ ") {
			t.Fatalf("%dx%d: truenas worker mark = %+v, want depth-2 child under %s", w, h, mk, leadID)
		}
	}
}
