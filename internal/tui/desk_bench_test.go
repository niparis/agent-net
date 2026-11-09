package tui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

// deskBenchFixture creates a realistic fixture with 100 Desk asks of mixed kinds
// (approval, question, permission, step, held, waiting, check), long texts,
// recommendations, why_you lines, and a realistic agents snapshot.
func deskBenchModel(w, h int) Model {
	snap := fakeSnapshot()
	for i := 0; i < 20; i++ {
		snap.Agents = append(snap.Agents, agent(
			fmt.Sprintf("s_%016x", i),
			fmt.Sprintf("n_host%02d", i%5),
			fmt.Sprintf("Host%02d", i%5),
			"claude",
			fmt.Sprintf("project-%02d", i%4),
			nil,
		))
	}

	kinds := []string{
		protocol.AskKindApproval,
		protocol.AskKindQuestion,
		"permission",
		protocol.AskKindStep,
		protocol.AskKindHeld,
		protocol.AskKindWaiting,
		protocol.AskKindCheck,
	}

	var asks []protocol.Ask
	for i := 0; i < 100; i++ {
		kind := kinds[i%len(kinds)]
		sessionID := fmt.Sprintf("s_%016x", i%20)
		ask := protocol.Ask{
			ID:          fmt.Sprintf("ask_%03d", i),
			State:       protocol.AskStateOpen,
			FromKind:    protocol.AskFromSession,
			From:        fmt.Sprintf("agent-%02d", i%15),
			FromSession: sessionID,
			Priority:    1 + (i % 3),
			CreatedAt:   t0.Add(-time.Duration(i*7+5) * time.Minute),
			WhyYou:      fmt.Sprintf("You are the primary maintainer of subsystem-%02d responsible for this approval decision", i%10),
		}

		switch kind {
		case protocol.AskKindApproval:
			ask.Kind = protocol.AskKindApproval
			ask.Question = fmt.Sprintf("Approve deployment of changes for service-%02d to production environment with canary analysis?", i)
			ask.Options = []string{"approve", "reject"}
			ask.Recommendation = "approve"
			ask.Detail = "Detailed canary analysis metrics and rollout plan for production cluster deployment with automated rollback."
		case protocol.AskKindQuestion:
			ask.Kind = protocol.AskKindQuestion
			ask.Question = fmt.Sprintf("Should migration proceed with database schema v%d, dropping deprecated column user_prefs_legacy?", i+2)
			ask.Options = []string{"yes", "no", "postpone"}
			ask.Recommendation = "yes"
			ask.Detail = "Migration plan includes comprehensive data backup and zero-downtime shadow table creation."
		case "permission":
			ask.Kind = protocol.AskKindApproval
			ask.Question = fmt.Sprintf("Permission request: Execute shell script with network access to download external dependencies for build #%d", i)
			ask.Options = []string{"allow", "deny"}
			ask.Recommendation = "allow"
			ask.Detail = "Script hashes verified against lockfile. Requires network egress to secure repository endpoints."
		case protocol.AskKindStep:
			ask.Kind = protocol.AskKindStep
			ask.AboutKind = protocol.AskAboutChange
			ask.AboutID = fmt.Sprintf("ch_%03d", i)
			ask.AboutTitle = fmt.Sprintf("Implement high-throughput stream processing pipeline part %d", i)
			ask.Question = fmt.Sprintf("Owner verification step %d: Please run latency smoke suite and verify p99 metrics under peak load", i)
			ask.Recommendation = "verify"
		case protocol.AskKindHeld:
			ask.Kind = protocol.AskKindHeld
			ask.AboutKind = protocol.AskAboutSession
			ask.AboutID = fmt.Sprintf("s_%016x", (i+1)%20)
			ask.Question = fmt.Sprintf("Held outbound request from session %d to external API service pending owner review of headers and payload", i)
			ask.Detail = "Outbound payload contains 42 sanitized log entries and request headers without authorization secrets."
			ask.Ref = fmt.Sprintf("held:m_%03d", i)
			ask.MessageID = fmt.Sprintf("m_%03d", i)
		case protocol.AskKindWaiting:
			ask.Kind = protocol.AskKindWaiting
			ask.AboutKind = protocol.AskAboutSession
			ask.AboutID = sessionID
			ask.Question = fmt.Sprintf("Waiting for user input on resolving git rebase conflict in internal/protocol/types_%02d.go", i)
			ask.Detail = "conflict"
			ask.Ref = fmt.Sprintf("waiting:%s", sessionID)
		case protocol.AskKindCheck:
			ask.Kind = protocol.AskKindCheck
			ask.FromKind = protocol.AskFromGateway
			ask.From = "daemon"
			ask.Question = fmt.Sprintf("Disk usage on worker node-%02d is above 85 percent threshold; prune temporary build cache?", i%5)
			ask.Detail = "Available disk space: 4.2 GB of 32 GB. Pruning build cache will recover approximately 12.8 GB."
		}

		asks = append(asks, ask)
	}

	m := deliveryModel(&fakeDelivery{changes: deliveryScenes()}, w, h)
	m.workersLog = false
	m.delivery.on = true
	m.tab = inboxTab
	if cmd := m.deliveryLoad(); cmd != nil {
		next, _ := m.Update(cmd())
		m = next.(Model)
	}
	m.snap = snap
	m.asks = asks
	m.deskLogOpen = true
	m.deskLog = []protocol.DeskLogEntry{
		{ID: 3, At: t0.Add(-5 * time.Minute), Source: "cto", Text: "CTO applied graph edge set app -> ops", Options: []string{"graph_undo", `{"id":42}`}},
		{ID: 2, At: t0.Add(-20 * time.Minute), Source: "gateway", Text: "Reminded 2 sessions about messages they have not answered"},
		{ID: 1, At: t0.Add(-3 * time.Hour), Source: "apps", Text: "Connected: phone (owner_app, Phone, example.test)"},
	}
	m.inboxIndex = 0
	return m
}

// BenchmarkDeskFrameMovingSelection measures one Desk frame View() with the
// selection moving across items.
func BenchmarkDeskFrameMovingSelection(b *testing.B) {
	m := deskBenchModel(120, 34)
	total := m.deskRowCount()
	if total == 0 {
		b.Fatal("no desk rows")
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.inboxIndex = i % total
		_ = m.View()
	}
}

// BenchmarkDeskUpdateKeyUpView measures one key press (Update with KeyUp then View).
func BenchmarkDeskUpdateKeyUpView(b *testing.B) {
	m := deskBenchModel(120, 34)
	total := m.deskRowCount()
	if total == 0 {
		b.Fatal("no desk rows")
	}
	m.inboxIndex = total - 1
	key := tea.KeyPressMsg{Code: tea.KeyUp}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if m.deskIndex() == 0 {
			m.inboxIndex = total - 1
		}
		next, _ := m.Update(key)
		m = next.(Model)
		_ = m.View()
	}
}

// BenchmarkDeskFiftyKeyUpPresses measures the wall time to handle 50 KeyUp presses.
func BenchmarkDeskFiftyKeyUpPresses(b *testing.B) {
	key := tea.KeyPressMsg{Code: tea.KeyUp}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := deskBenchModel(120, 34)
		total := m.deskRowCount()
		m.inboxIndex = min(50, total-1)
		for j := 0; j < 50; j++ {
			next, _ := m.Update(key)
			m = next.(Model)
			_ = m.View()
		}
	}
}

// BenchmarkDeskFiftyKeyUpBurst measures handling a burst of 50 KeyUp presses with coalescing.
func BenchmarkDeskFiftyKeyUpBurst(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := deskBenchModel(120, 34)
		total := m.deskRowCount()
		m.inboxIndex = min(50, total-1)
		queue := make(chan tea.Msg, 50)
		for j := 0; j < 50; j++ {
			queue <- tea.KeyPressMsg{Code: tea.KeyUp}
		}
		m.keyQueue = queue
		for len(queue) > 0 {
			msg := <-queue
			next, _ := m.Update(msg)
			m = next.(Model)
			_ = m.View()
		}
	}
}

// TestDeskBurstKeyUp feeds 50 KeyUp messages in a burst and verifies that they
// coalesce so that the number of View calls is bounded (< 50) and reaches the
// right selection.
func TestDeskBurstKeyUp(t *testing.T) {
	m := deskBenchModel(120, 34)
	total := m.deskRowCount()
	if total < 50 {
		t.Fatalf("total rows %d < 50", total)
	}
	m.inboxIndex = 50
	if m.deskIndex() != 50 {
		t.Fatalf("deskIndex = %d, want 50", m.deskIndex())
	}

	queue := make(chan tea.Msg, 50)
	for i := 0; i < 50; i++ {
		queue <- tea.KeyPressMsg{Code: tea.KeyUp}
	}
	m.keyQueue = queue

	viewCalls := 0
	for len(queue) > 0 {
		msg := <-queue
		next, _ := m.Update(msg)
		m = next.(Model)
		_ = m.View()
		viewCalls++
	}

	if m.deskIndex() != 0 {
		t.Fatalf("deskIndex = %d, want 0", m.deskIndex())
	}
	if viewCalls >= 50 {
		t.Fatalf("burst did not coalesce: %d View calls, want fewer than 50", viewCalls)
	}
}
