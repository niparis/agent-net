package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/protocol"
)

// headerModel is the fake screen with an owner-actionable header: three busy
// sessions, one session waiting, four online workers and one queued start on
// the build VM.
func headerModel(w, h int) Model {
	snap := fakeSnapshot()
	for i := range snap.Agents {
		switch snap.Agents[i].SessionID {
		case "s_0000000000000001", "s_0000000000000002", "s_0000000000000003":
			snap.Agents[i].Status = "busy"
		case "s_0000000000000004":
			waiting(&snap.Agents[i], -3*time.Minute, protocol.WaitingPermission, "Bash(git push origin main)")
		}
	}
	for i := 0; i < 4; i++ {
		snap.Agents = append(snap.Agents, agent(
			[]string{"s_0000000000000101", "s_0000000000000102", "s_0000000000000103", "s_0000000000000104"}[i],
			"n_mac", "Mac", "opencode", "worker", func(a *protocol.AgentView) {
				a.Worker = &protocol.WorkerInfo{Owner: "s_0000000000000002"}
			}))
	}
	u := fakeUsage()
	u.Gateway.Nodes[0].Usage.Workers = &protocol.WorkerLoad{Running: 0, Queued: 1, Room: protocol.RoomCPU}
	src := &fakeSource{snap: snap, events: fakeEvents(), usage: u, counts: fakeCounts(), tops: fakeTops()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src))
}

func TestHeaderCountsOnlineBusyNeedsAndWorkers(t *testing.T) {
	head := lines(headerModel(160, 30).View())[0]
	want := "12 online · 3 busy · 1 needs you · workers 4 running, 1 queued"
	if !strings.Contains(head, want) {
		t.Fatalf("header %q lacks %q", head, want)
	}
	if strings.Contains(head, "offline") {
		t.Fatalf("the offline count is still in the header: %q", head)
	}
}

func TestHeaderHidesNeedsYouAtZero(t *testing.T) {
	head := lines(newModel(160, 30).View())[0]
	if strings.Contains(head, "needs you") {
		t.Fatalf("header shows needs you with nobody waiting: %q", head)
	}
	if !strings.Contains(head, "12 online · 1 busy") {
		t.Fatalf("header lacks the busy count: %q", head)
	}
}

func TestHeaderNeedsYouUsesTheWarningColour(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	head := lines(headerModel(160, 30).View())[0]
	if !strings.Contains(head, sNeeds.Render("1 needs you")) {
		t.Fatalf("needs you is not in the warning colour: %q", head)
	}
}

func TestMachineCPUAt100Fits(t *testing.T) {
	for _, w := range []int{80, 100, 120} {
		u := fakeUsage()
		u.Local.Machine.CPU, u.Local.Machine.CPUAvg = 100, 100
		m := modelWith(fakeSnapshot(), u, w, 24)
		var line string
		for _, l := range lines(m.View()) {
			if strings.Contains(l, "Mac (here)") && strings.Contains(l, "cpu") {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("%d columns: no machine line:\n%s", w, m.View())
		}
		if strings.Contains(line, "10…") {
			t.Fatalf("%d columns: the CPU cell cuts 100%%: %q", w, line)
		}
		if !strings.Contains(line, "100% (100%)") {
			t.Fatalf("%d columns: the CPU cell lacks 100%%: %q", w, line)
		}
	}
}
