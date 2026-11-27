package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// These tests send the approved key to a model in its context and check what
// it does, and that the key it replaced does nothing. The table itself is
// checked by TestApprovedKeyMapContract in approved_keys_test.go.

func TestApproved01RefreshOnEveryScreen(t *testing.T) {
	pressRune := func(m Model, key string) (Model, tea.Cmd) {
		return press(m, tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
	}

	messages := newModel(120, 34)
	messages.tab, messages.focus = messagesTab, focusFeed
	if _, cmd := pressRune(messages, "R"); cmd == nil {
		t.Fatal("R did not refresh Messages")
	}
	if _, cmd := pressRune(messages, "r"); cmd != nil {
		t.Fatal("r still refreshes Messages")
	}

	quotas := newModel(120, 34)
	quotas.tab = quotasTab
	if _, cmd := pressRune(quotas, "R"); cmd == nil {
		t.Fatal("R did not refresh Quotas")
	}
	if _, cmd := pressRune(quotas, "r"); cmd != nil {
		t.Fatal("r still refreshes Quotas")
	}

	card := keys(graphModel(120, 34), "enter")
	if !card.mapOn {
		t.Fatal("enter did not open the agent card")
	}
	if _, cmd := pressRune(card, "R"); cmd == nil {
		t.Fatal("R did not refresh the agent card")
	}
	if _, cmd := pressRune(card, "r"); cmd != nil {
		t.Fatal("r still refreshes the agent card")
	}
}

func TestApproved05SessionEIsNotEnter(t *testing.T) {
	m := newModel(120, 34)
	src := m.src.(*fakeSource)
	if _, cmd := press(m, tea.KeyPressMsg{Text: "e", Code: []rune("e")[0]}); cmd != nil {
		t.Fatal("e still enters a session")
	}
	if len(src.enters) != 0 {
		t.Fatal("e asked to enter a session")
	}
	_, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter no longer enters a session")
	}
	cmd()
	if len(src.enters) != 1 {
		t.Fatalf("enter called session_enter %d times", len(src.enters))
	}
}

func TestApproved12MessageVIsNotTheBody(t *testing.T) {
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "one", Time: t0}, ConversationID: "a"}})
	if _, cmd := press(m, tea.KeyPressMsg{Text: "v", Code: []rune("v")[0]}); cmd != nil {
		t.Fatal("v still opens the message body")
	}
	if m.open != nil {
		t.Fatal("v still opens the message body")
	}
	next, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || next.open == nil {
		t.Fatal("enter no longer opens the message body")
	}
}

func TestApproved16GraphLettersDoNotPan(t *testing.T) {
	m := graphModel(120, 34)
	camera := m.graphState.Camera
	m = keys(m, "h", "j", "k", "l")
	if m.graphState.Camera != camera {
		t.Fatal("h, j, k or l still pans the graph")
	}
	m = keys(m, "shift+left")
	if m.graphState.Camera == camera {
		t.Fatal("shift+left no longer pans the graph")
	}
}

func TestApproved17GraphCommaSelectsThePreviousNode(t *testing.T) {
	m := graphModel(120, 34)
	start := m.graphState.Graph.Selected()
	m = keys(m, ",")
	if m.graphState.Graph.Selected() == start {
		t.Fatal("comma did not select a linked node")
	}
}

func TestApproved18CardBracketsDoNotScroll(t *testing.T) {
	m := openMap(t, mapModel(80, 24, richGraph()))
	m.mp.selected = "tenebrae"
	if m.mp.charterScroll != 0 {
		t.Fatalf("card starts at %d", m.mp.charterScroll)
	}
	m = keys(m, "[", "]")
	if m.mp.charterScroll != 0 {
		t.Fatalf("brackets still scroll the card: %d", m.mp.charterScroll)
	}
	m = keys(m, "ctrl+d")
	if m.mp.charterScroll == 0 {
		t.Fatal("ctrl+d no longer scrolls the card")
	}
}

func TestApproved19CardCommaSelectsThePreviousDraft(t *testing.T) {
	m := openMap(t, mapModel(120, 40, fakeGraph()))
	m.snap.Drafts = []protocol.CharterDraft{testDraft(3, "purpose", "Generic endpoints"), testDraft(4, "refuses", "Client endpoints")}
	m.mp.selected = "cogforge"
	m = keys(m, ".")
	if m.mp.draftIndex != 1 {
		t.Fatalf("period selected index %d", m.mp.draftIndex)
	}
	m = keys(m, ",")
	if m.mp.draftIndex != 0 {
		t.Fatalf("comma selected index %d", m.mp.draftIndex)
	}
}

func TestApproved27HelpNIsTheNotificationKey(t *testing.T) {
	src := &notificationSource{fakeSource: &fakeSource{}}
	m := newModel(100, 30)
	m.src = src
	m.help = true
	if _, cmd := press(m, tea.KeyPressMsg{Text: "n", Code: []rune("n")[0]}); cmd != nil || src.disabled {
		t.Fatal("n still toggles desktop notifications")
	}
	if _, cmd := press(m, tea.KeyPressMsg{Text: "N", Code: []rune("N")[0]}); cmd != nil {
		t.Fatal("N still toggles desktop notifications")
	}
}

type notificationSource struct {
	*fakeSource
	disabled bool
}

func (s *notificationSource) NotificationSettings(_ context.Context, disabled *bool) (bool, error) {
	if disabled != nil {
		s.disabled = *disabled
	}
	return s.disabled, nil
}
