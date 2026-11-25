package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// TestSoloRepoLeadProofScenario shows the sessions tree for a Repo lead whose
// Repo is in no Team: the lead sits under the CTO, not at the top level. The
// rows come from a real gateway store, so the gateway computes the parent.
// Frames at 80x24 and 120x34 go to AGENT_NET_PROOF_DIR when it is set.
func TestSoloRepoLeadProofScenario(t *testing.T) {
	st, err := gateway.OpenStore(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	node, _, err := st.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	cto, err := st.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	lead, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "solo-lead", Status: "idle",
		Cwd: "/work/solo", Repo: protocol.RepoInfo{Root: "/work/solo", Project: "solo"}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	reports := map[string]string{}
	for i := range rows {
		rows[i].FirstSeen, rows[i].LastSeen = t0.Unix(), t0.Unix() // pin the clock
		reports[rows[i].SessionID] = rows[i].ReportsTo
	}
	if reports[lead] != "cto" {
		t.Fatalf("the gateway says the solo repo lead reports to %q, want cto", reports[lead])
	}

	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		w, h := size[0], size[1]
		snap := Snapshot{Connected: true, NodeID: node, Agents: rows}
		src := &fakeSource{snap: snap}
		m := New(src)
		m.activeOnly, m.showOffline = false, true
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
		m = settle(refresh(m, src))

		order := ids(m)
		ctoAt, leadAt := indexOfID(order, cto), indexOfID(order, lead)
		if ctoAt < 0 || leadAt < 0 {
			t.Fatalf("%dx%d: the CTO and the solo repo lead are not both shown: %v", w, h, order)
		}
		if leadAt != ctoAt+1 {
			t.Fatalf("%dx%d: the solo repo lead is not under the CTO: %v", w, h, order)
		}
		mark := treeMark{}
		for _, g := range m.groups() {
			if mk, ok := g.marks[lead]; ok {
				mark = mk
			}
		}
		if mark.prefix == "" || mark.root != cto {
			t.Fatalf("%dx%d: the solo repo lead has tree mark %+v, want a child of the CTO", w, h, mark)
		}

		dir := os.Getenv("AGENT_NET_PROOF_DIR")
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		frame := ansi.Strip(m.testView())
		capture := fmt.Sprintf("build: %s\nstep: open the sessions tree at %dx%d\naction: open the TUI with a CTO session and the lead of a Repo in no Team\nexpected: the solo repo lead is a child row under the CTO\nerrors: none\n\n%s\n",
			os.Getenv("AGENT_NET_PROOF_BUILD"), w, h, frame)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("solo-repo-lead-%dx%d.txt", w, h)), []byte(capture), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
