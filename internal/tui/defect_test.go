package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// defectMessagesModel opens Messages on a worker conversation.
func defectMessagesModel(t *testing.T, w, h int) (Model, *fakeSource) {
	t.Helper()
	snap := fakeSnapshot()
	worker := snap.Agents[0]
	worker.SessionID = "s_worker"
	worker.Profile.Name = "build worker 8"
	worker.Worker = &protocol.WorkerInfo{ID: "w_8", Owner: "s_0000000000000001", Task: "Polish the sessions panel"}
	snap.Agents = append(snap.Agents, worker)
	a := "s_0000000000000001"
	events := []protocol.FeedEvent{
		ev(1, -9*time.Minute, "msg.stored", "m_a", a, "s_worker", "request", func(e *protocol.FeedEvent) { e.ConversationID = "c_a" }),
		ev(2, -7*time.Minute, "msg.stored", "m_b", "s_worker", a, "reply", func(e *protocol.FeedEvent) { e.ConversationID = "c_a"; e.Parent = "m_a" }),
	}
	src := &fakeSource{snap: snap, events: events, messages: map[string]protocol.Message{
		"m_a": {ID: "m_a", ConversationID: "c_a", Body: "Polish the sessions panel"},
		"m_b": {ID: "m_b", ConversationID: "c_a", Body: "[agent-net worker notice]\nstatus: done"},
	}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = refresh(m, src)
	m = drainSnippets(t, m)
	m = keys(m, "4")
	for _, row := range m.messageConversations() {
		if row.worker {
			m.feedCur = row.items[0]
			break
		}
	}
	return m, src
}

// f on a worker conversation asks for a one-line note and the shared confirm,
// then files the owner's defect report.
func TestMessagesDefectKeyFilesReportAfterConfirm(t *testing.T) {
	m, src := defectMessagesModel(t, 120, 34)
	m, _ = press(m, keyRune("f"))
	if m.mode != modeDefectNote {
		t.Fatalf("f did not open the note prompt: mode %v notice %+v", m.mode, m.notice)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "defect") || !strings.Contains(v, "worker 8") {
		t.Fatalf("the note prompt does not name the worker:\n%s", v)
	}
	m.defect.SetValue("the old path stayed after the merge")
	m, _ = press(m, keyEnter())
	if m.mode != modeConfirm || !m.confirmDefect {
		t.Fatalf("the note did not ask for confirmation: mode %v", m.mode)
	}
	confirmHintShown(t, m)
	m, cmd := press(m, keyEnter())
	if cmd == nil {
		t.Fatal("confirming the defect did not call the daemon")
	}
	m = finish(t, m, cmd)
	if len(src.defects) != 1 {
		t.Fatalf("defects reported: %+v", src.defects)
	}
	got := src.defects[0]
	if got.worker != "s_worker" || got.note != "the old path stayed after the merge" {
		t.Fatalf("defect call: %+v", got)
	}
	if m.notice.err || !strings.Contains(strings.ToLower(m.notice.text), "defect") {
		t.Fatalf("defect notice: %+v", m.notice)
	}
}

// Esc cancels both the note and the confirm, and nothing is filed.
func TestMessagesDefectKeyCancels(t *testing.T) {
	m, src := defectMessagesModel(t, 120, 34)
	m, _ = press(m, keyRune("f"))
	m.defect.SetValue("not really a defect")
	m, _ = press(m, keyEnter())
	m, cmd := press(m, keyEsc())
	if cmd != nil || m.mode != modeNormal || len(src.defects) != 0 {
		t.Fatalf("cancel: mode %v cmd %v defects %+v", m.mode, cmd != nil, src.defects)
	}
}

// f on a conversation that is not a worker does nothing.
func TestMessagesDefectKeyIgnoresOtherConversations(t *testing.T) {
	m := newModel(120, 34)
	m.tab = messagesTab
	m.focus = focusFeed
	m, _ = press(m, keyRune("f"))
	if m.mode != modeNormal || !strings.Contains(m.notice.text, "worker") {
		t.Fatalf("non-worker row: mode %v notice %+v", m.mode, m.notice)
	}
}
