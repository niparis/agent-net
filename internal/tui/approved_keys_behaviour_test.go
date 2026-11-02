package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

// These tests send the approved key to a model in its context and check what
// it does, and that the key it replaced does nothing. The table itself is
// checked by TestApprovedKeyMapContract in approved_keys_test.go.

func TestApproved01RefreshOnEveryScreen(t *testing.T) {
	pressRune := func(m Model, key string) (Model, tea.Cmd) {
		return press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
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

	tools, src := toolsAtScreen(120, 34, toolsDiffs)
	if _, cmd := pressRune(tools, "R"); cmd == nil {
		t.Fatal("R did not rescan Tools")
	}
	if _, cmd := pressRune(tools, "r"); cmd != nil {
		t.Fatal("r still rescans Tools")
	}
	if src.rescans != 0 {
		t.Fatalf("rescan ran too early: %d", src.rescans)
	}
}

func TestApproved05SessionEIsNotEnter(t *testing.T) {
	m := newModel(120, 34)
	src := m.src.(*fakeSource)
	if _, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}); cmd != nil {
		t.Fatal("e still enters a session")
	}
	if len(src.enters) != 0 {
		t.Fatal("e asked to enter a session")
	}
	_, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter no longer enters a session")
	}
	cmd()
	if len(src.enters) != 1 {
		t.Fatalf("enter called session_enter %d times", len(src.enters))
	}
}

func TestApproved07HeldRequestDoesNotAnswerOnShiftKeys(t *testing.T) {
	src := &heldTestSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.snap = src.snap
	m.snap.Held = []protocol.Message{{ID: "m_held", FromName: "peer", To: "s_0000000000000002", Body: "hello"}}
	m.selected = "m_held"
	for _, key := range []string{"A", "R"} {
		_, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd != nil && src.answer.MessageID != "" {
			t.Fatalf("%s answered the held request", key)
		}
		if src.answer.MessageID != "" {
			t.Fatalf("%s answered the held request with %q", key, src.answer.Action)
		}
	}
	_, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("a no longer approves a held request")
	}
	cmd()
	if src.answer.MessageID != "m_held" || src.answer.Action != "a" {
		t.Fatalf("a answered %+v", src.answer)
	}
}

func TestApproved08OutcomeOIsNotOverrule(t *testing.T) {
	src := &outcomeTestSource{}
	m := New(src)
	m.snap = Snapshot{Outcomes: []protocol.OutcomeInboxEntry{{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r"}, Sender: "sender", Recipient: "receiver"}}}
	m.selected = "outcome:r"
	next, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if cmd != nil || src.action != "" {
		t.Fatalf("o still overrules: action %q", src.action)
	}
	if !next.showOffline {
		t.Fatal("o no longer shows offline sessions")
	}
}

func TestApproved09ProposalDeclineOnRAndNotX(t *testing.T) {
	p := proposalFixture()
	s := &proposalSource{proposals: []protocol.CTOProposal{p}}
	m := New(s)
	m.proposals = s.proposals
	m.selected = proposalKey(p)
	if _, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); cmd != nil || len(s.answers) != 0 {
		t.Fatal("x still declines a proposal")
	}
	_, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("r did not decline a proposal")
	}
	cmd()
	if len(s.answers) != 1 || s.answers[0].Action != "x" {
		t.Fatalf("answers %+v", s.answers)
	}

	detail := &proposalSource{proposals: []protocol.CTOProposal{p}}
	m = New(detail)
	m.proposalDetail = &proposalDetail{proposal: p}
	_, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("r did not decline in the proposal detail")
	}
	cmd()
	if len(detail.answers) != 1 || detail.answers[0].Action != "x" {
		t.Fatalf("detail answers %+v", detail.answers)
	}
}

func TestApproved12MessageVIsNotTheBody(t *testing.T) {
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "one", Time: t0}, ConversationID: "a"}})
	if _, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")}); cmd != nil {
		t.Fatal("v still opens the message body")
	}
	if m.open != nil {
		t.Fatal("v still opens the message body")
	}
	next, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
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

func TestApproved19CardCommaSelectsThePreviousLearnedChange(t *testing.T) {
	m := graphModel(120, 40)
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-02"})
	m.snap.Learned = []protocol.LearnedSuggestion{
		{ID: 1, Node: "project-02", Field: "needs", Phrase: "ops", Status: "applied", By: "learned", HistoryID: 42},
		{ID: 2, Node: "project-02", Field: "needs", Phrase: "backup", Status: "applied", By: "learned", HistoryID: 43},
	}
	m = keys(m, "enter", ".")
	if m.mp.learnedIndex != 1 {
		t.Fatalf("period selected index %d", m.mp.learnedIndex)
	}
	m = keys(m, ",")
	if m.mp.learnedIndex != 0 {
		t.Fatalf("comma selected index %d", m.mp.learnedIndex)
	}
}

func TestApproved23AcceptDifferenceOnA(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, toolsDiffs)
	ds := m.shownDiffs()
	row := -1
	for i, d := range ds {
		if !strings.HasPrefix(d.Key, "group:") && !d.Info {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no difference to accept")
	}
	m.tl.row = row
	key := ds[row].Key
	if _, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); cmd != nil {
		t.Fatal("x still accepts a difference")
	}
	if m.tl.accepted[key] {
		t.Fatal("x still accepts a difference")
	}
	next, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil || !next.tl.accepted[key] {
		t.Fatal("a no longer accepts a difference")
	}
}

func TestApproved24AllToolsEnterOpensTheItem(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, toolsDiffs)
	m = keys(m, "]")
	if m.tl.screen != toolsAll {
		t.Fatal("] did not switch to All tools")
	}
	m = keys(m, "right")
	m = keys(m, "enter")
	if m.tl.modal == nil {
		t.Fatal("enter did not open the selected item")
	}
	if strings.Contains(plain(m), "details below") {
		t.Fatal("the details below message is back")
	}
}

func TestApproved25PickerTicksWithSpaceOnly(t *testing.T) {
	m, _ := newToolsModel(120, 34)
	m = openTools(m)
	m = rowByName(m, "found-skill-10")
	m.tl.col += 2
	m = resolveTools(m.openRowPlan(true))
	p := m.tl.modal.picker
	if p == nil {
		t.Fatal("turn on did not open the project picker")
	}
	m = pressKey(m, "x")
	if len(p.sel) != 0 {
		t.Fatalf("x ticked %v", p.sel)
	}
	m = pressKey(m, " ")
	if len(p.sel) != 1 {
		t.Fatalf("space ticked %v", p.sel)
	}
}

func TestApproved27HelpNIsTheNotificationKey(t *testing.T) {
	src := &notificationSource{fakeSource: &fakeSource{}}
	m := newModel(100, 30)
	m.src = src
	m.help = true
	if _, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}); cmd != nil || src.disabled {
		t.Fatal("n still toggles desktop notifications")
	}
	if _, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")}); cmd == nil {
		t.Fatal("N no longer toggles desktop notifications")
	}
}
