package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// handoverWaitingModel selects the oldest waiting session, which has an
// announced task and a question.
func handoverWaitingModel(w, h int) Model {
	m := needsModel(w, h)
	m.selected = "s_0000000000000002"
	m.selNeeds = true
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == m.selected && m.snap.Agents[i].Waiting != nil {
			m.snap.Agents[i].Waiting.Options = []string{"Yes", "No"}
		}
	}
	m.ensureSelection()
	return m
}

func TestHandOverPreviewShowsTheWaitingSession(t *testing.T) {
	m := handoverWaitingModel(100, 30)
	next, _ := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = next
	if m.handover == nil {
		t.Fatal("c did not open the hand-over preview")
	}
	for _, want := range []string{
		"Hand-over from the owner",
		"a session that waits in Needs you",
		"Session: strategies (s_0000000000000002)",
		"Announced task: backtest v2",
		"Status: idle",
		"Waiting: question - Merge tenebrae-nexus/truenas-stacks #817?",
		"options: Yes, No",
	} {
		if !strings.Contains(m.handover.text, want) {
			t.Errorf("preview lacks %q:\n%s", want, m.handover.text)
		}
	}
	if !strings.Contains(m.View(), "Hand over to CTO") {
		t.Errorf("preview screen has no heading:\n%s", m.View())
	}
}

func TestHandOverPreviewShowsAHeldRequest(t *testing.T) {
	m := newModel(100, 30)
	m.snap.Held = []protocol.Message{{
		ID: "m_held", From: "s_0000000000000001", To: "s_0000000000000002",
		FromName: "tenebrae-53", Type: protocol.TypeRequest, CreatedAt: t0.Add(-2 * time.Minute),
		Body: "Deploy abc to production?\nThe branch is ready.",
	}}
	m.selected = "m_held"
	m = pressKey(m, "c")
	if m.handover == nil {
		t.Fatal("c did not open the hand-over preview")
	}
	for _, want := range []string{
		"a held request in Needs you",
		"Deploy abc to production?",
		"From: tenebrae-53 (s_0000000000000001)",
		"To: strategies (s_0000000000000002)",
		"The branch is ready.",
	} {
		if !strings.Contains(m.handover.text, want) {
			t.Errorf("preview lacks %q:\n%s", want, m.handover.text)
		}
	}
}

func TestHandOverPreviewShowsTheInboxQuestion(t *testing.T) {
	m := newModel(100, 30)
	m.inboxItems = []protocol.InboxItem{{
		ID: "i_1", Kind: "approval", SessionID: "s_0000000000000002",
		Node: "n_mac", Text: "May I merge the risk model?", Recommendation: "yes, tests pass",
		Status: "open", CreatedAt: t0.Add(-5 * time.Minute),
	}}
	m.selected = "s_0000000000000002"
	m = pressKey(m, "c")
	if m.handover == nil {
		t.Fatal("c did not open the hand-over preview")
	}
	for _, want := range []string{
		"an inbox item in Needs you",
		"Question: May I merge the risk model?",
		"Recommendation: yes, tests pass",
		"Announced task: backtest v2",
	} {
		if !strings.Contains(m.handover.text, want) {
			t.Errorf("preview lacks %q:\n%s", want, m.handover.text)
		}
	}
}

func TestHandOverPreviewShowsAMessageBody(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), messages: map[string]protocol.Message{
		"m_1": {ID: "m_1", ConversationID: "c_1", From: "s_0000000000000001", To: "s_0000000000000002",
			Type: protocol.TypeRequest, State: protocol.StateDelivered, CreatedAt: t0.Add(-9 * time.Minute),
			Body: "Which model do you run?\nPlease answer in one line."},
	}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = settle(refresh(m, src))
	m = keys(m, "2")
	m.feedCur = feedIndexFor(m, "m_1")
	m = pressKey(m, "c")
	if m.handover == nil {
		t.Fatal("c did not open the hand-over preview")
	}
	for _, want := range []string{
		"a message in Messages",
		"Which model do you run?",
		"Please answer in one line.",
		"From: tenebrae-53 (s_0000000000000001)",
		"To: strategies (s_0000000000000002)",
	} {
		if !strings.Contains(m.handover.text, want) {
			t.Errorf("preview lacks %q:\n%s", want, m.handover.text)
		}
	}
}

func feedIndexFor(m Model, id string) int {
	for i, it := range m.feed.items {
		if it.MsgID == id {
			return i
		}
	}
	return -1
}

func TestHandOverSendsExactlyThePreviewedText(t *testing.T) {
	base := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	src := &fakeCTOSource{fakeSource: base}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = settle(refresh(m, src))
	m.snap.Held = []protocol.Message{{
		ID: "m_held", From: "s_0000000000000001", To: "s_0000000000000002",
		FromName: "tenebrae-53", Type: protocol.TypeRequest, CreatedAt: t0.Add(-2 * time.Minute),
		Body: "Deploy abc to production?\nThe branch is ready.",
	}}
	m.selected = "m_held"
	m = pressKey(m, "c")
	if m.handover == nil {
		t.Fatal("c did not open the hand-over preview")
	}
	want := m.handover.text
	m = pressKey(m, "enter")
	if m.handover != nil {
		t.Fatal("enter did not close the preview")
	}
	if len(src.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(src.sent))
	}
	if src.sent[0] != want {
		t.Fatalf("sent text differs from the preview.\n--- sent ---\n%s\n--- preview ---\n%s", src.sent[0], want)
	}
}

func TestHandOverEscCancels(t *testing.T) {
	base := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	src := &fakeCTOSource{fakeSource: base}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = settle(refresh(m, src))
	m.selected = "s_0000000000000002"
	m = pressKey(m, "c")
	m = pressKey(m, "esc")
	if m.handover != nil {
		t.Fatal("esc did not close the preview")
	}
	if len(src.sent) != 0 {
		t.Fatalf("esc sent %d messages", len(src.sent))
	}
}

func TestHandOverCapsAndScrollsLongText(t *testing.T) {
	m := newModel(80, 24)
	m.snap.Agents = append(m.snap.Agents, agent("s_long", "n_mac", "Mac", "claude", "long-session", func(a *protocol.AgentView) {
		a.Profile.Task = strings.Repeat("word ", 2000)
	}))
	m.selected = "s_long"
	m = pressKey(m, "c")
	if m.handover == nil {
		t.Fatal("c did not open the hand-over preview")
	}
	if got := len([]rune(m.handover.text)); got > maxHandoverRunes {
		t.Fatalf("preview holds %d runes, limit is %d", got, maxHandoverRunes)
	}
	if !strings.Contains(m.handover.text, "cut at 4000 characters") {
		t.Fatalf("no cut marker:\n%s", m.handover.text)
	}
	before := m.View()
	m = pressKey(m, "pgdown")
	after := m.View()
	if before == after {
		t.Fatal("pgdown did not scroll the preview")
	}
}

func TestHandOverViewFitsEverySize(t *testing.T) {
	for _, w := range []int{80, 120} {
		for _, h := range []int{24, 34} {
			m := handoverWaitingModel(w, h)
			m = pressKey(m, "c")
			out := m.View()
			if ls := lines(out); len(ls) != h {
				t.Fatalf("%dx%d: %d lines, want %d", w, h, len(ls), h)
			}
			for i, line := range lines(out) {
				if ansi.StringWidth(line) > w {
					t.Fatalf("%dx%d line %d is %d columns: %q", w, h, i, ansi.StringWidth(line), line)
				}
			}
		}
	}
}

func TestHandOverGolden(t *testing.T) {
	m := handoverWaitingModel(80, 24)
	m = pressKey(m, "c")
	golden(t, "handover_80x24", m.View())

	m = handoverWaitingModel(120, 34)
	m = pressKey(m, "c")
	golden(t, "handover_120x34", m.View())
}
