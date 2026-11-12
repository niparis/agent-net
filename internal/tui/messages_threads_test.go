package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// threadHistoryRows makes count stored threads, newest first, spread over 30
// days. The two fixture sessions are the pair of every thread.
func threadHistoryRows(count int) []protocol.ConversationView {
	const a, b = "s_0000000000000001", "s_0000000000000002"
	rows := make([]protocol.ConversationView, 0, count)
	for i := 0; i < count; i++ {
		at := t0.Add(-time.Duration(i) * 30 * 24 * time.Hour / time.Duration(count))
		state := protocol.StateReplied
		typ := protocol.TypeReply
		if i%4 == 1 {
			state, typ = protocol.StateStored, protocol.TypeRequest
		}
		row := conversationView(fmt.Sprintf("c_%03d", i), a, b, "tenebrae-53", "strategies",
			i%5+1, fmt.Sprintf("Thread %03d: the last line of the conversation", i), state, at)
		row.Type = typ
		rows = append(rows, row)
	}
	return rows
}

// pageThreads answers the list in pages of the size the screen asks for, with
// a version, the way the gateway does.
func pageThreads(src *fakeSource, rows []protocol.ConversationView) {
	src.convVersion = 7
	page := func(start, end int, next string) protocol.ConversationPage {
		return protocol.ConversationPage{Conversations: rows[start:end], Total: len(rows), Next: next}
	}
	end := min(conversationPageSize, len(rows))
	src.convFirst = page(0, end, "")
	src.convMore = map[string]protocol.ConversationPage{}
	if end < len(rows) {
		src.convFirst.Next = fmt.Sprint(end)
	}
	for start := end; start < len(rows); start += conversationPageSize {
		stop := min(start+conversationPageSize, len(rows))
		next := ""
		if stop < len(rows) {
			next = fmt.Sprint(stop)
		}
		src.convMore[fmt.Sprint(start)] = page(start, stop, next)
	}
}

func TestMessageListLoadsEveryStoredThread(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot()}
	pageThreads(src, threadHistoryRows(500))
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = refreshAndRun(t, m, src)

	rows := m.messageConversations()
	if len(rows) != 500 {
		t.Fatalf("the list has %d threads, want all 500", len(rows))
	}
	if got := rows[len(rows)-1].lastLine; got != "Thread 499: the last line of the conversation" {
		t.Fatalf("the oldest thread is %q", got)
	}
	view := ansi.Strip(strings.Join(m.messagesView(120, 34), "\n"))
	if !strings.Contains(view, "500 threads") {
		t.Fatalf("the footer does not count every thread:\n%s", view)
	}
	golden(t, "messages_all_threads_120x34", m.testView())

	// The oldest thread is reachable: scroll the list to its end.
	m.convSel = len(rows) - 1
	if got := m.selectedConversationIndex(m.messageConversations()); got != len(rows)-1 {
		t.Fatalf("the list did not reach its last row: %d", got)
	}
	view = ansi.Strip(strings.Join(m.messagesView(120, 34), "\n"))
	age := t0.Sub(rows[len(rows)-1].at).Truncate(time.Minute)
	ageText := strings.TrimSuffix(age.String(), "0s") + " ago"
	if !strings.Contains(view, ageText) {
		t.Fatalf("the oldest thread (%s) is not on screen:\n%s", ageText, view)
	}
	golden(t, "messages_all_threads_oldest_120x34", m.testView())
}

func TestOpeningAnOldThreadReadsOnlyItsMessages(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot()}
	pageThreads(src, threadHistoryRows(500))
	src.convMsgs = map[string][]protocol.Message{
		"c_350": {
			{ID: "m_350_request", ConversationID: "c_350", From: "s_0000000000000001", To: "s_0000000000000002",
				Type: protocol.TypeRequest, Body: "the full body of thread 350", CreatedAt: t0.Add(-20 * 24 * time.Hour)},
			{ID: "m_350_reply", ConversationID: "c_350", From: "s_0000000000000002", To: "s_0000000000000001",
				Type: protocol.TypeReply, Body: "the reply in thread 350", CreatedAt: t0.Add(-20*24*time.Hour + time.Minute)},
		},
	}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = refreshAndRun(t, m, src)

	// The list itself carries no message body.
	for _, row := range m.messageConversations() {
		if strings.Contains(row.lastLine, "the full body") {
			t.Fatalf("a list row carries a body: %q", row.lastLine)
		}
	}
	rows := m.messageConversations()
	for i, row := range rows {
		if row.convID == "c_350" {
			m.convSel = i
			break
		}
	}
	// The first selected row was already read after the load; opening another
	// thread is its own read.
	src.convMsgCalls = nil
	m = runCommands(t, m, m.wantConversationDetails())
	if len(src.convMsgCalls) != 1 || src.convMsgCalls[0] != "c_350" {
		t.Fatalf("opening the thread asked for %+v, want its own messages", src.convMsgCalls)
	}
	view := ansi.Strip(strings.Join(m.messagesView(120, 34), "\n"))
	if !strings.Contains(view, "the full body of thread 350") {
		t.Fatalf("the opened thread does not show its messages:\n%s", view)
	}
}

func TestUnchangedThreadPollSendsNoRows(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot()}
	pageThreads(src, threadHistoryRows(500))
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = refreshAndRun(t, m, src)
	if src.convSentRows != 500 {
		t.Fatalf("the first load sent %d rows, want 500", src.convSentRows)
	}

	m = refreshAndRun(t, m, src)
	last := src.convCalls[len(src.convCalls)-1]
	if last.IfVersion != src.convVersion || last.Cursor != "" {
		t.Fatalf("the poll did not send the held version: %+v", last)
	}
	if src.convSentRows != 500 {
		t.Fatalf("an unchanged poll sent %d more rows", src.convSentRows-500)
	}
	if got := len(m.messageConversations()); got != 500 {
		t.Fatalf("the list lost rows on an unchanged poll: %d", got)
	}
}
