package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

var agentLine = regexp.MustCompile(`(?m)^\s*agent\s+demo merger\s`)

// TestOrgTreeMergerProofScenario shows what the owner sees once a merger has
// its own Agent (decision 0160): the merger row and its details name the Agent
// "demo merger", not the Repo's Agent "demo". The rows come from a real
// gateway store, started the way merger_start registers a merger.
func TestOrgTreeMergerProofScenario(t *testing.T) {
	st, err := gateway.OpenStore(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	node, _, err := st.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	repo := protocol.RepoInfo{Root: "/work/demo", Project: "demo"}
	lead, err := st.UpsertSession(node, protocol.SessionInfo{SessionID: "s_00000000000000a1", Harness: "claude", NativeID: "lead", Status: "idle", Cwd: "/work/demo", Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	merger, err := st.UpsertSession(node, protocol.SessionInfo{SessionID: "s_00000000000000a2", Harness: "claude", NativeID: "merge", Status: "idle", Cwd: "/work/demo", Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: node, SessionID: merger, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	var rows []protocol.AgentView
	for _, id := range []string{lead, merger} {
		row, ok, err := st.Agent(id)
		if err != nil || !ok {
			t.Fatalf("row %s: ok %v err %v", id, ok, err)
		}
		row.LastSeen, row.FirstSeen = t0.Unix(), t0.Unix() // pin the clock
		rows = append(rows, row)
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		w, h := size[0], size[1]
		src := &fakeSource{snap: Snapshot{Connected: true, NodeID: node, Agents: rows}}
		m := New(src)
		m.activeOnly = false
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
		m = refresh(m, src)
		m = selectID(m, merger)
		list := ansi.Strip(m.testView())
		detail := ansi.Strip(keys(m, "d").testView())
		// The session name already said "demo merger"; the Agent line is new.
		if !agentLine.MatchString(detail) {
			t.Fatalf("%dx%d: the merger details do not name its own Agent:\n%s", w, h, detail)
		}
		dir := os.Getenv("AGENT_NET_PROOF_DIR")
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for _, f := range []struct{ name, step, action, expected, frame string }{
			{"sessions", "open the sessions list", "open the TUI with the repo lead and its merger, select the merger row", "the merger row is the demo merger", list},
			{"merger-detail", "open the merger details", "press d on the merger row", "agent: demo merger", detail},
		} {
			capture := fmt.Sprintf("build: %s\nstep: %s at %dx%d\naction: %s\nexpected: %s\nerrors: none\n\n%s\n",
				os.Getenv("AGENT_NET_PROOF_BUILD"), f.step, w, h, f.action, f.expected, f.frame)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("org-tree-%s-%dx%d.txt", f.name, w, h)), []byte(capture), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
