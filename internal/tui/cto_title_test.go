package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func titledCTO(t *testing.T) (Model, *fakeCTOSource) {
	t.Helper()
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}, settings: protocol.CTOSettings{
		Title: "Atlas", Model: "fake/m1", Harness: "opencode", OpenCodeModel: "fake/m1",
	}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = refresh(m, src.fakeSource)
	m.snap = src.snap
	m.src = src
	m = send(m, m.ctoPollCmd()())
	return m, src
}

func TestCTOTitleOnOwnerScreens(t *testing.T) {
	m, _ := titledCTO(t)
	if m.cto.title != "Atlas" {
		t.Fatalf("polled title = %q; want Atlas", m.cto.title)
	}
	box := ansi.Strip(strings.Join(m.ctoView(80, 6), "\n"))
	for _, want := range []string{"Atlas · opencode", "c: open the Atlas"} {
		if !strings.Contains(box, want) {
			t.Errorf("CTO box lacks %q:\n%s", want, box)
		}
	}

	m.tab, m.focus, m.mapOn = messagesTab, focusFeed, false
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{
		Event:          protocol.Event{ID: 1, Time: t0.Add(-time.Minute), Kind: "msg.stored", MessageID: "m_cto"},
		From:           "s_0000000000000002",
		To:             "s_0000000000000001",
		Type:           "notification",
		FromCTO:        true,
		ConversationID: "c_cto",
	}})
	m.feed.byMsg["m_cto"].Snippet = "Please check the deployment."
	rows := m.messageConversations()
	if len(rows) != 1 {
		t.Fatalf("conversations = %+v", rows)
	}
	chat := ansi.Strip(strings.Join(m.conversationChat(rows[0], 80), "\n"))
	if !strings.Contains(chat, "Atlas") || strings.Contains(chat, "CTO") {
		t.Errorf("Messages sender is not the owner's title:\n%s", chat)
	}
	line := ansi.Strip(m.messageConversationLine(rows[0], 100, false))
	if !strings.Contains(line, "Atlas ↔") || strings.Contains(line, "CTO ↔") {
		t.Errorf("conversation row is not the owner's title: %s", line)
	}

	help := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.helpContent(120), "\n"))), " ")
	for _, want := range []string{"focus the Atlas box", "enter opens the full Atlas", "open the Atlas session"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "CTO") {
		t.Errorf("help still says CTO:\n%s", help)
	}

	m.focus = focusCTO
	if line := ansi.Strip(m.helpLine(120)); !strings.Contains(line, "open Atlas") {
		t.Errorf("CTO key line: %s", line)
	}
	m.focus = focusSessions
	if line := ansi.Strip(m.helpLine(120)); !strings.Contains(line, "c Atlas") {
		t.Errorf("Sessions key line: %s", line)
	}
}

func TestCTOTitleFormSavesTheTitle(t *testing.T) {
	m, src := titledCTO(t)
	m.focus = focusCTO
	next, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = next.(Model)
	if m.cto.settingsForm == nil {
		t.Fatal("the m form did not open")
	}
	next, _ = m.update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.cto.settingsForm.field != 1 || m.cto.settingsForm.titleInput.Value() != "Atlas" {
		t.Fatalf("Title field not focused: %+v", m.cto.settingsForm)
	}
	m.cto.settingsForm.titleInput.SetValue("Harbour")
	next, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("save did not start")
	}
	next, _ = m.update(cmd())
	m = next.(Model)
	if src.saved.Title == nil || *src.saved.Title != "Harbour" {
		t.Fatalf("saved title = %v", src.saved.Title)
	}
	if !strings.Contains(m.View(), "Harbour · opencode") {
		t.Fatalf("the box did not take the saved title:\n%s", m.View())
	}
}

func TestCTOTitleFormRefusesMarkers(t *testing.T) {
	m, _ := titledCTO(t)
	m.focus = focusCTO
	next, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = next.(Model)
	next, _ = m.update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	m.cto.settingsForm.titleInput.SetValue("Atlas [agent-net")
	next, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("an invalid title was sent")
	}
	if m.cto.settingsForm == nil || !strings.Contains(ansi.Strip(strings.Join(m.ctoView(80, 10), "\n")), "agent-net") {
		t.Fatalf("the form did not explain the refusal:\n%s", strings.Join(m.ctoView(80, 10), "\n"))
	}
}

func TestCTOTitleGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := titledCTO(t)
		m.width, m.height = size[0], size[1]
		box := strings.Join(m.ctoView(size[0], 10), "\n")
		golden(t, fmt.Sprintf("cto_title_box_%dx%d", size[0], size[1]), box)

		help := strings.Join(m.helpContent(size[0]), "\n")
		golden(t, fmt.Sprintf("cto_title_help_%dx", size[0]), help)

		m.tab, m.focus, m.mapOn = messagesTab, focusFeed, false
		m.feed = newFeed()
		m.feed.apply([]protocol.FeedEvent{{
			Event:          protocol.Event{ID: 1, Time: t0.Add(-time.Minute), Kind: "msg.stored", MessageID: "m_cto"},
			From:           "s_0000000000000002",
			To:             "s_0000000000000001",
			Type:           "notification",
			FromCTO:        true,
			ConversationID: "c_cto",
		}})
		m.feed.byMsg["m_cto"].Snippet = "Please check the deployment."
		m.feedCur = 0
		src := m.src.(*fakeCTOSource)
		src.messages = map[string]protocol.Message{"m_cto": {ID: "m_cto", ConversationID: "c_cto", Body: "Please check the deployment.\nIt is on staging."}}
		m = drainSnippets(t, m)
		golden(t, fmt.Sprintf("cto_title_messages_%dx%d", size[0], size[1]), m.View())
	}
}
