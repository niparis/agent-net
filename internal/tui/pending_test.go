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
	if got := m.statusCell(a); got != "wait" {
		t.Fatalf("idle: %q", got)
	}
	for _, status := range []string{"busy", "shell", "unknown"} {
		a.Status = status
		if got := m.statusCell(a); got == "wait" || got == "owes" {
			t.Fatalf("%s: %q", status, got)
		}
	}
	a.Status = "idle"
	a.Pending[0].Outgoing = false
	if got := m.statusCell(a); got != "owes" {
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

func TestPendingTaskAndLayout(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := newModel(width, 34)
		before := m.sessionsTable(width, 12)
		for i := range m.snap.Agents {
			if m.snap.Agents[i].Profile.Name == "strategies" {
				m.snap.Agents[i].Pending = []protocol.PendingRequest{{PeerName: "a-very-long-name", Since: t0.Add(-12 * time.Minute).Unix(), Outgoing: true}}
			}
		}
		after := m.sessionsTable(width, 12)
		if before[0] != after[0] || !strings.Contains(after[0], "RULE") {
			t.Fatalf("width %d: headers %q vs %q", width, before[0], after[0])
		}
		for i := range before {
			if strings.Contains(before[i], "strategies") {
				if (width >= 120 && !strings.Contains(after[i], "12m")) || !strings.Contains(after[i], "wait") {
					t.Fatalf("width %d: %q", width, after[i])
				}
				for _, marker := range []string{" - ", "240%", "4.6G"} {
					if strings.Index(before[i], marker) != strings.Index(after[i], marker) {
						t.Fatalf("width %d: %s shifted: %q vs %q", width, marker, before[i], after[i])
					}
				}
				continue
			}
			if before[i] != after[i] {
				t.Fatalf("width %d: row shifted: %q vs %q", width, before[i], after[i])
			}
		}
	}
	m := newModel(120, 34)
	a := protocol.AgentView{Online: true, Status: "idle", Pending: []protocol.PendingRequest{{PeerName: "alice", Since: t0.Add(-5 * time.Minute).Unix()}}}
	if got := m.pendingTask(a, 50); got != "owes reply to alice · 5m" {
		t.Fatalf("owed task: %q", got)
	}
	if got := m.pendingTask(a, 10); !strings.HasSuffix(got, " · 5m") {
		t.Fatalf("lost time: %q", got)
	}
	a.Status = "busy"
	if got := m.pendingTask(a, 50); got != "" {
		t.Fatalf("busy task: %q", got)
	}
}
