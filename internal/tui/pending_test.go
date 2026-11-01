package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestPendingStatusOnlyIdle(t *testing.T) {
	m := Model{now: time.Now}
	a := protocol.AgentView{Online: true, Status: "idle", Pending: []protocol.PendingRequest{{PeerName: "bob", Since: time.Now().Add(-12 * time.Minute).Unix(), Outgoing: true}}}
	if got := m.statusCell(a); !strings.HasPrefix(got, "waiting on bob · ") {
		t.Fatalf("idle: %q", got)
	}
	for _, status := range []string{"busy", "shell", "unknown"} {
		a.Status = status
		if got := m.statusCell(a); strings.Contains(got, "waiting on") {
			t.Fatalf("%s: %q", status, got)
		}
	}
	a.Status = "idle"
	a.Pending[0].Outgoing = false
	if got := m.statusCell(a); got != "owes reply" {
		t.Fatalf("incoming: %q", got)
	}
}

func TestPendingGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m := newModel(size.w, size.h)
		found := 0
		for i := range m.snap.Agents {
			if m.snap.Agents[i].Status == "idle" && m.snap.Agents[i].Online {
				found++
				if found == 2 {
					m.snap.Agents[i].Pending = []protocol.PendingRequest{{MessageID: "m_other", PeerName: "alice", Since: t0.Add(-3 * time.Minute).Unix()}}
					break
				}
				m.snap.Agents[i].Pending = []protocol.PendingRequest{{MessageID: "m_example", PeerName: "a-very-long-recipient-name", Since: t0.Add(-12 * time.Minute).Unix(), Outgoing: true}}
				m.selected = m.snap.Agents[i].SessionID
			}
		}
		golden(t, fmt.Sprintf("pending_%d", size.w), m.View())
	}
}

func TestPendingDetail(t *testing.T) {
	m := newModel(120, 34)
	a := m.snap.Agents[0]
	a.Pending = []protocol.PendingRequest{{MessageID: "m_example", PeerName: "bob", Since: t0.Add(-12 * time.Minute).Unix(), Outgoing: true}}
	_, lines := m.detailLeft(a, 60, 20)
	text := strings.Join(lines, "\n")
	for _, want := range []string{"bob", "m_example", "13:53"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
}
