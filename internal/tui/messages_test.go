package tui

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestMessageConversationsGroupAndSort(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{
		{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "one", Time: t0.Add(-3 * time.Minute)}, ConversationID: "a", From: "s_0000000000000001", To: "s_0000000000000002", Type: "request"},
		{Event: protocol.Event{ID: 2, Kind: "msg.stored", MessageID: "two", Time: t0.Add(-2 * time.Minute)}, ConversationID: "b", From: "s_0000000000000001", To: "s_0000000000000002", Type: "notification"},
		{Event: protocol.Event{ID: 3, Kind: "msg.stored", MessageID: "three", Time: t0.Add(-time.Minute)}, ConversationID: "a", Parent: "one", From: "s_0000000000000002", To: "s_0000000000000001", Type: "reply"},
	})
	m.feed.byMsg["one"].Snippet = "first topic"
	m.feed.byMsg["two"].Snippet = "second topic"
	rows := m.messageConversations()
	if len(rows) != 2 || rows[0].id != "a" || len(rows[0].items) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	v := ansi.Strip(strings.Join(m.messagesView(120, 20), "\n"))
	if !strings.Contains(v, "first topic") || !strings.Contains(v, "second topic") {
		t.Fatal(v)
	}
}

func TestMessageConversationNavigation(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	for i, id := range []string{"first", "second"} {
		m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: int64(i + 1), Kind: "msg.stored", MessageID: id, Time: t0.Add(time.Duration(i) * time.Minute)}, ConversationID: id}})
	}
	m.feedCur = 1
	m = keys(m, "down")
	if got := m.feed.items[m.feedCur].MsgID; got != "first" {
		t.Fatalf("selected %s", got)
	}
}
