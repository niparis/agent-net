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

// scrollFixture is a Messages tab with more conversations than fit and one
// conversation with more messages than fit.
func scrollFixture(t *testing.T, w, h int) (Model, *fakeSource) {
	t.Helper()
	const a, b = "s_0000000000000001", "s_0000000000000002"
	src := &fakeSource{snap: fakeSnapshot()}
	var rows []protocol.ConversationView
	for i := 0; i < 60; i++ {
		from, to, fromName := a, b, "tenebrae-53"
		if i > 0 {
			from, to, fromName = fmt.Sprintf("s_peer%02d", i), b, fmt.Sprintf("peer%02d", i)
		}
		rows = append(rows, conversationView(fmt.Sprintf("c_%02d", i), from, to, fromName, "strategies",
			3, fmt.Sprintf("stored conversation %02d", i), protocol.StateReplied, t0.Add(-time.Duration(i)*time.Minute)))
	}
	src.convFirst = protocol.ConversationPage{Conversations: rows, Total: 60}
	var msgs []protocol.Message
	for i := 79; i >= 0; i-- { // newest first, like the gateway
		from, to := a, b
		if i%2 == 1 {
			from, to = b, a
		}
		msgs = append(msgs, protocol.Message{ID: fmt.Sprintf("m_%02d", i), ConversationID: "c_00", From: from, To: to,
			Type: protocol.TypeRequest, Body: fmt.Sprintf("thread message %02d", i), CreatedAt: t0.Add(-time.Duration(80-i) * time.Second)})
	}
	src.convMsgs = map[string][]protocol.Message{"c_00": msgs}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = refreshAndRun(t, m, src)
	return m, src
}

func scrollRowName(i int) string {
	if i == 0 {
		return "tenebrae-53 ↔"
	}
	return fmt.Sprintf("peer%02d", i)
}

func pressRun(t *testing.T, m Model, msg tea.Msg, n int) Model {
	t.Helper()
	for i := 0; i < n; i++ {
		next, cmd := m.Update(msg)
		m = runCommands(t, next.(Model), cmd)
	}
	return m
}

func screen(m Model) string { return ansi.Strip(m.testView()) }

func wheel(x, y int, down bool) tea.MouseWheelMsg {
	b := tea.MouseWheelUp
	if down {
		b = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: b}
}

func TestMessagesListScrollsWithKeys(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		size := size
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m, _ := scrollFixture(t, size[0], size[1])
			if !strings.Contains(screen(m), "tenebrae-53 ↔") {
				t.Fatalf("first row missing:\n%s", screen(m))
			}
			for _, key := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: 'j', Text: "j"}} {
				m2 := pressRun(t, m, key, 25)
				if m2.convSel != 25 || !strings.Contains(screen(m2), "peer25") {
					t.Fatalf("%v x25: convSel=%d, row 25 not on screen:\n%s", key, m2.convSel, screen(m2))
				}
				m3 := pressRun(t, m2, tea.KeyPressMsg{Code: tea.KeyUp}, 5)
				if !strings.Contains(screen(m3), "peer20") {
					t.Fatalf("up x5: row 20 not on screen:\n%s", screen(m3))
				}
			}
			m2 := pressRun(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown}, 2)
			if m2.convSel < 10 || !strings.Contains(screen(m2), scrollRowName(m2.convSel)) {
				t.Fatalf("pgdown x2: convSel=%d not on screen:\n%s", m2.convSel, screen(m2))
			}
			m3 := pressRun(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown}, 30)
			if m3.convSel != 59 || !strings.Contains(screen(m3), "peer59") {
				t.Fatalf("pgdown x30: convSel=%d, last row not on screen:\n%s", m3.convSel, screen(m3))
			}
			m4 := pressRun(t, m3, tea.KeyPressMsg{Code: tea.KeyHome}, 1)
			if m4.convSel != 0 || !strings.Contains(screen(m4), "tenebrae-53 ↔") {
				t.Fatalf("home: convSel=%d:\n%s", m4.convSel, screen(m4))
			}
		})
	}
}

func TestMessagesListScrollsWithTheMouseWheel(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		size := size
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m, _ := scrollFixture(t, size[0], size[1])
			if m.View().MouseMode != tea.MouseModeCellMotion {
				t.Fatalf("the Messages tab does not ask for mouse events, so the wheel never arrives")
			}
			m2 := pressRun(t, m, wheel(5, 6, true), 10)
			if m2.convSel == 0 || !strings.Contains(screen(m2), scrollRowName(m2.convSel)) {
				t.Fatalf("wheel down on the list: convSel=%d:\n%s", m2.convSel, screen(m2))
			}
			m3 := pressRun(t, m2, wheel(5, 6, false), 30)
			if m3.convSel != 0 {
				t.Fatalf("wheel up on the list: convSel=%d", m3.convSel)
			}
		})
	}
}

func TestMessagesThreadScrollsWithKeysAndWheel(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		size := size
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m, _ := scrollFixture(t, size[0], size[1])
			view := screen(m)
			if !strings.Contains(view, "thread message 79") {
				t.Fatalf("a long thread does not open on its newest message:\n%s", view)
			}
			if strings.Contains(view, "thread message 00") {
				t.Fatalf("fixture too small: the whole thread fits:\n%s", view)
			}
			// The wheel over the conversation pane (right side at 120, lower
			// half at 80) scrolls the thread, not the list.
			x, y := 100, 10
			if size[0] < 100 {
				x, y = 10, size[1]-4
			}
			up := pressRun(t, m, wheel(x, y, false), 6)
			if up.convSel != 0 {
				t.Fatalf("wheel over the thread moved the list to %d", up.convSel)
			}
			if strings.Contains(screen(up), "thread message 79") {
				t.Fatalf("wheel up over the thread did not scroll it:\n%s", screen(up))
			}
			back := pressRun(t, up, wheel(x, y, true), 10)
			if !strings.Contains(screen(back), "thread message 79") {
				t.Fatalf("wheel down did not return to the newest message:\n%s", screen(back))
			}
			// Keys: page up scrolls the thread all the way to its oldest message.
			top := pressRun(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, 120)
			if !strings.Contains(screen(top), "thread message 00") {
				t.Fatalf("ctrl+u x120 did not reach the oldest message:\n%s", screen(top))
			}
			down := pressRun(t, top, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, 120)
			if !strings.Contains(screen(down), "thread message 79") {
				t.Fatalf("ctrl+d x120 did not return to the newest message:\n%s", screen(down))
			}
			// A stored thread has no marked message, so left and right scroll it.
			sel := pressRun(t, m, tea.KeyPressMsg{Code: tea.KeyLeft}, 10)
			if strings.Contains(screen(sel), "thread message 79") {
				t.Fatalf("left did not scroll a stored thread:\n%s", screen(sel))
			}
			if rt := pressRun(t, sel, tea.KeyPressMsg{Code: tea.KeyRight}, 20); !strings.Contains(screen(rt), "thread message 79") {
				t.Fatalf("right did not return to the newest message:\n%s", screen(rt))
			}
			if end := pressRun(t, up, tea.KeyPressMsg{Code: tea.KeyEnd}, 1); !strings.Contains(screen(end), "thread message 79") {
				t.Fatalf("end did not return to the newest message:\n%s", screen(end))
			}
		})
	}
}

func TestMessagesThreadKeepsItsScrollWhenNewMessagesArrive(t *testing.T) {
	m, src := scrollFixture(t, 120, 34)
	m = pressRun(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, 3)
	before := screen(m)
	if strings.Contains(before, "thread message 79") {
		t.Fatalf("scroll did not move:\n%s", before)
	}
	// A refresh, a new page and a new stored message in the open thread.
	d := m.convDetail["c_00"]
	d.msgs = append([]protocol.Message{{ID: "m_80", ConversationID: "c_00", From: "s_0000000000000001", To: "s_0000000000000002",
		Type: protocol.TypeRequest, Body: "thread message 80", CreatedAt: t0}}, d.msgs...)
	m.convDetail["c_00"] = d
	m = refreshAndRun(t, m, src)
	if after := screen(m); after != before {
		t.Fatalf("a new message moved the thread.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestMessagesScrolledScreens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := scrollFixture(t, size[0], size[1])
		list := pressRun(t, m, tea.KeyPressMsg{Code: tea.KeyDown}, 25)
		golden(t, fmt.Sprintf("messages_list_scrolled_%dx%d", size[0], size[1]), list.testView())
		thread := pressRun(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, 3)
		golden(t, fmt.Sprintf("messages_thread_scrolled_%dx%d", size[0], size[1]), thread.testView())
	}
}
