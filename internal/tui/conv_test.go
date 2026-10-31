package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// exec runs a command and feeds its message (or each message of a batch) back
// into the model, the way the program does.
func exec(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = exec(m, c)
		}
	case nil:
	default:
		m = send(m, msg)
	}
	return m
}

// press sends a key and runs the command it returns.
func pressKey(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		msg = tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		msg = tea.KeyMsg{Type: tea.KeyPgDown}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, cmd := m.Update(msg)
	return exec(next.(Model), cmd)
}

// tick runs the clock's refresh, which also follows the open conversation.
func tickOnce(m Model) Model {
	old := tickDelay
	tickDelay = 0
	defer func() { tickDelay = old }()
	m.fetching = false
	next, cmd := m.Update(tickMsg{})
	return exec(next.(Model), cmd)
}

// transcript is a fake session of n entries with cursors c000, c001, ... It
// pages back and forward like the daemon does.
type transcript struct {
	entries []protocol.TextEntry
}

func newTranscript(n int) *transcript {
	t := &transcript{}
	for i := 0; i < n; i++ {
		t.add(fmt.Sprintf("entry %d", i))
	}
	return t
}

func (t *transcript) add(text string) {
	i := len(t.entries)
	role := []string{protocol.RoleUser, protocol.RoleAssistant, protocol.RoleToolCall, protocol.RoleToolResult}[i%4]
	e := protocol.TextEntry{Cursor: fmt.Sprintf("c%03d", i), Time: t0.Add(time.Duration(i) * time.Second).UnixMilli(), Role: role, Text: text}
	if role == protocol.RoleToolCall || role == protocol.RoleToolResult {
		e.Tool = "Bash"
	}
	t.entries = append(t.entries, e)
}

func idx(cursor string) int {
	var i int
	fmt.Sscanf(cursor, "c%d", &i)
	return i
}

func (t *transcript) serve(q protocol.TextQuery) (protocol.TextPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = protocol.TextDefaultLimit
	}
	n := len(t.entries)
	var page protocol.TextPage
	switch {
	case q.After != "":
		start := idx(q.After) + 1
		end := min(start+limit, n)
		page.Entries = t.entries[start:end]
		page.Newer = q.After
		if end > start {
			page.Newer = t.entries[end-1].Cursor
		}
		return page, nil
	case q.Before != "":
		end := idx(q.Before)
		start := max(end-limit, 0)
		page.Entries = t.entries[start:end]
		if start > 0 {
			page.Older = t.entries[start].Cursor
		}
		return page, nil
	}
	start := max(n-limit, 0)
	page.Entries = t.entries[start:]
	if start > 0 {
		page.Older = t.entries[start].Cursor
	}
	if n > 0 {
		page.Newer = t.entries[n-1].Cursor
	}
	return page, nil
}

func convModel(w, h int, tr *transcript) (Model, *fakeSource) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), usage: fakeUsage(), counts: fakeCounts(), tops: fakeTops(), textFn: tr.serve}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src)), src
}

// A fixture with every role, a masked secret, wrapped text and several lines.
func fixtureTranscript() *transcript {
	tr := &transcript{}
	add := func(role, tool, text string, cut bool) {
		i := len(tr.entries)
		tr.entries = append(tr.entries, protocol.TextEntry{Cursor: fmt.Sprintf("c%03d", i), Time: t0.Add(time.Duration(i*7-60) * time.Second).UnixMilli(),
			Role: role, Tool: tool, Text: text, Cut: cut})
	}
	add(protocol.RoleUser, "", "Run the backtest for the risk model and tell me which year has the worst drawdown.", false)
	add(protocol.RoleAssistant, "", "I will start with the data loader, then run the backtest for all ten years and compare the drawdowns. This may take a few minutes because the data set is large.", false)
	add(protocol.RoleToolCall, "Bash", "python3 backtest.py --years 10 --api-key=sk-****", false)
	add(protocol.RoleToolResult, "Bash", "year 2016 drawdown 12.4%\nyear 2017 drawdown 4.1%\nyear 2018 drawdown 19.8%\nyear 2019 drawdown 7.0%", false)
	add(protocol.RoleAssistant, "", "The worst year is 2018 with a drawdown of 19.8%.\n\nWant me to plot it?", false)
	add(protocol.RoleUser, "", "yes, and save the plot to /tmp/dd.png", false)
	add(protocol.RoleToolCall, "Write", "/tmp/plot.py (1.2 KB)", false)
	add(protocol.RoleToolResult, "Write", strings.Repeat("a long output line ", 20), true)
	return tr
}

func TestGoldenConversation(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		m, _ := convModel(sz[0], sz[1], fixtureTranscript())
		m = pressKey(m, "enter")
		golden(t, fmt.Sprintf("conv_%dx%d", sz[0], sz[1]), m.View())
	}
	m, _ := convModel(80, 24, fixtureTranscript())
	m = pressKey(pressKey(m, "c"), "up")
	m.conv.back = 6
	golden(t, "conv_80x24_paused", m.View())
}

func TestConversationOpensWithEnterOrC(t *testing.T) {
	for _, k := range []string{"enter", "c"} {
		m, src := convModel(100, 30, newTranscript(5))
		m = pressKey(m, k)
		if m.conv == nil || m.conv.id != m.selected || len(m.conv.entries) != 5 {
			t.Fatalf("%s: conv = %+v", k, m.conv)
		}
		if len(src.queries) != 1 || src.queries[0].SessionID != m.selected || src.queries[0].Limit != convPage {
			t.Fatalf("%s: query %+v", k, src.queries)
		}
		m = pressKey(m, "esc")
		if m.conv != nil {
			t.Fatalf("%s: esc did not close", k)
		}
	}
	// Enter in Messages selects the latest message in the right-hand thread pane.
	m, _ := convModel(100, 30, newTranscript(5))
	m = pressKey(m, "2")
	m = pressKey(m, "enter")
	if m.conv != nil || m.open != nil {
		t.Fatalf("enter in Messages opened another view: conv=%+v body=%+v", m.conv, m.open)
	}
	row := m.messageConversations()[m.conversationIndex(m.messageConversations())]
	if m.feedCur != row.items[len(row.items)-1] || !strings.Contains(m.View(), "Conversation") {
		t.Fatalf("enter did not select the latest message in its thread: cursor=%d row=%+v", m.feedCur, row)
	}
}

func TestPagingBackLoadsOlderEntriesOnce(t *testing.T) {
	tr := newTranscript(120)
	m, src := convModel(100, 30, tr)
	m = pressKey(m, "enter")
	if len(m.conv.entries) != convPage || m.conv.older != "c070" {
		t.Fatalf("first page: %d entries, older %q", len(m.conv.entries), m.conv.older)
	}
	// Page up until the top of the loaded text, which asks for older entries.
	for i := 0; i < 40 && len(m.conv.entries) == convPage; i++ {
		m = pressKey(m, "pgup")
	}
	if len(m.conv.entries) != 100 || m.conv.older != "c020" {
		t.Fatalf("after paging back: %d entries, older %q", len(m.conv.entries), m.conv.older)
	}
	last := src.queries[len(src.queries)-1]
	if last.Before != "c070" {
		t.Fatalf("older request %+v", last)
	}
	// Entries stay in order with no duplicates.
	for i, e := range m.conv.entries {
		if e.Cursor != fmt.Sprintf("c%03d", 20+i) {
			t.Fatalf("entry %d is %s", i, e.Cursor)
		}
	}
	// Up to the start of the conversation.
	for i := 0; i < 100 && m.conv.older != ""; i++ {
		m = pressKey(m, "pgup")
	}
	if len(m.conv.entries) != 120 || m.conv.older != "" {
		t.Fatalf("at the start: %d entries, older %q", len(m.conv.entries), m.conv.older)
	}
	n := len(src.queries)
	m = pressKey(pressKey(m, "pgup"), "pgup")
	if len(src.queries) != n {
		t.Fatal("asked for older entries at the start")
	}
	if !strings.Contains(m.View(), "start reached") {
		t.Fatalf("no sign of the start:\n%s", m.View())
	}
}

func TestPagingKeepsTheViewWhereItWas(t *testing.T) {
	m, _ := convModel(100, 30, newTranscript(120))
	m = pressKey(m, "enter")
	before := lines(m.View())
	m = pressKey(m, "pgup")
	scrolled := lines(m.View())
	if strings.Join(before, "\n") == strings.Join(scrolled, "\n") {
		t.Fatal("pgup did not scroll")
	}
	// Older entries arriving does not move the lines on screen.
	back := m.conv.back
	m = pressKey(m, "g")
	m = pressKey(m, "G")
	if m.conv.back != 0 {
		t.Fatalf("G left back = %d (was %d)", m.conv.back, back)
	}
}

func TestFollowAddsNewEntriesAndPausesWhenScrolledBack(t *testing.T) {
	tr := newTranscript(10)
	m, _ := convModel(100, 14, tr) // 11 rows of text: ten entries fill them
	m = pressKey(m, "enter")
	if m.conv.newer != "c009" {
		t.Fatalf("newer %q", m.conv.newer)
	}
	tr.add("brand new reply")
	m = tickOnce(m)
	if len(m.conv.entries) != 11 || !strings.Contains(m.View(), "brand new reply") || !strings.Contains(m.View(), "following") {
		t.Fatalf("new entry not followed:\n%s", m.View())
	}
	// Scrolled back: new entries arrive but the view stays put.
	for i := 0; i < 6; i++ {
		tr.add(fmt.Sprintf("later %d", i))
	}
	m = tickOnce(m)
	m = pressKey(m, "up")
	m = pressKey(m, "up")
	for i := 6; i < 12; i++ {
		tr.add(fmt.Sprintf("later %d", i))
	}
	back := m.conv.back
	m = tickOnce(m)
	if len(m.conv.entries) != 23 || m.conv.back != back || !strings.Contains(m.View(), "paused") {
		t.Fatalf("scrolled back view moved: back %d -> %d\n%s", back, m.conv.back, m.View())
	}
	if strings.Contains(m.View(), "later 11") {
		t.Fatal("the newest entry is on screen while paused")
	}
	m = pressKey(m, "G")
	if !strings.Contains(m.View(), "later 11") || !strings.Contains(m.View(), "following") {
		t.Fatalf("G did not follow:\n%s", m.View())
	}
	// A repeated answer adds nothing.
	n := len(m.conv.entries)
	m = tickOnce(m)
	if len(m.conv.entries) != n {
		t.Fatal("entries were added twice")
	}
}

func TestFollowAsksFromTheNewerCursor(t *testing.T) {
	tr := newTranscript(3)
	m, src := convModel(100, 30, tr)
	m = pressKey(m, "enter")
	m = tickOnce(m)
	last := src.queries[len(src.queries)-1]
	if last.After != "c002" || last.Before != "" {
		t.Fatalf("follow query %+v", last)
	}
}

func TestEmptySessionIsFollowedFromTheStart(t *testing.T) {
	tr := &transcript{}
	m, _ := convModel(100, 30, tr)
	m = pressKey(m, "enter")
	if !m.conv.loaded || !strings.Contains(m.View(), "nothing yet") {
		t.Fatalf("empty session:\n%s", m.View())
	}
	tr.add("first words")
	m = tickOnce(m)
	if len(m.conv.entries) != 1 || !strings.Contains(m.View(), "first words") {
		t.Fatalf("the first entry did not arrive:\n%s", m.View())
	}
}

func TestOnlyOneConversationFetchAtATime(t *testing.T) {
	m, src := convModel(100, 30, newTranscript(5))
	m = pressKey(m, "enter")
	n := len(src.queries)
	m.conv.busy = true
	if cmd := m.convCmd(convNewer); cmd != nil {
		t.Fatal("a second fetch started")
	}
	m = tickOnce(m)
	if len(src.queries) != n {
		t.Fatal("the clock started a fetch while one was running")
	}
}

func TestConversationErrorsAreExplained(t *testing.T) {
	m, src := convModel(100, 30, newTranscript(5))
	src.textFn = func(protocol.TextQuery) (protocol.TextPage, error) {
		return protocol.TextPage{}, client.RemoteError("method \"session_text\" is only available to the owner")
	}
	m = pressKey(m, "enter")
	v := m.View()
	if !strings.Contains(v, "Could not read the conversation") || !strings.Contains(v, "only available to the owner") {
		t.Fatalf("error not shown:\n%s", v)
	}
	src.textFn = func(protocol.TextQuery) (protocol.TextPage, error) {
		return protocol.TextPage{}, fmt.Errorf("%w (/x.sock)", ErrDaemonDown)
	}
	m = exec(m, m.convCmd(convFirst))
	if !strings.Contains(m.View(), "The daemon cannot be reached") {
		t.Fatalf("daemon error:\n%s", m.View())
	}
	// It recovers by itself.
	src.textFn = newTranscript(2).serve
	m = exec(m, m.convCmd(convFirst))
	if m.conv.err != "" || len(m.conv.entries) != 2 {
		t.Fatalf("no recovery: %+v", m.conv)
	}
}

func TestConversationShowsTheDaemonsTextAsItCame(t *testing.T) {
	tr := &transcript{}
	tr.add("token=[masked] and sk-**** stay as sent\x1b[31m red? ‮")
	m, _ := convModel(100, 30, tr)
	m = pressKey(m, "enter")
	v := m.View()
	if !strings.Contains(v, "token=[masked] and sk-**** stay as sent") {
		t.Fatalf("masked text changed:\n%s", v)
	}
	if strings.Contains(v, "\x1b") || strings.Contains(v, "‮") {
		t.Fatalf("control characters reached the screen: %q", v)
	}
	// The conversation keeps the whole screen: no table, no feed.
	if strings.Contains(v, "Selected session") || strings.Contains(v, "Messages (live)") {
		t.Fatalf("main screen parts in the conversation view:\n%s", v)
	}
}

func TestConversationLinesWrapToTheWidth(t *testing.T) {
	m, _ := convModel(50, 30, fixtureTranscript())
	m = pressKey(m, "enter")
	for _, l := range m.convLines(50) {
		if len(l.text) > convTextWidth(50)+8 { // a cut word may use up to the width in bytes
			t.Errorf("line too long (%d): %q", len(l.text), l.text)
		}
	}
	// Only the first line of an entry has the time and role.
	firsts := 0
	for _, l := range m.convLines(50) {
		if l.first {
			firsts++
		}
	}
	if firsts != len(m.conv.entries) {
		t.Errorf("%d first lines for %d entries", firsts, len(m.conv.entries))
	}
}

func TestConversationKeysQuitAndHelp(t *testing.T) {
	m, _ := convModel(100, 30, newTranscript(5))
	m = pressKey(m, "enter")
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Fatal("q did not quit from the conversation")
	}
	m = pressKey(m, "?")
	if !m.help {
		t.Fatal("? did not open the help")
	}
}

var _ = errors.New
