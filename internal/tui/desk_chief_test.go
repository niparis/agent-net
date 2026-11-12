package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// deskCallSource records the calls the Desk makes and answers the log.
type deskCallSource struct {
	fakeSource
	mu    sync.Mutex // a refresh asks its reads together
	calls []string
	args  []any
	log   []protocol.DeskLogEntry
}

func (s *deskCallSource) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	s.calls = append(s.calls, method)
	s.args = append(s.args, params)
	s.mu.Unlock()
	switch method {
	case protocol.OpDeskLog:
		return json.Marshal(protocol.DeskLogResult{Entries: s.log})
	case protocol.OpAskUndo, protocol.OpAskMark:
		return json.RawMessage(`{"ok":true}`), nil
	case "inbox_answer":
		return json.RawMessage(`{"item":{}}`), nil
	case protocol.OpAskDiscuss:
		return json.Marshal(protocol.AskDiscussResult{Open: protocol.DeskOwnerChief, Session: "s_chief", Woken: true})
	}
	return nil, errors.New("fake: no such method " + method)
}

// deskPress sends one key to the Desk and returns the model and its command.
func deskPress(t *testing.T, m Model, k tea.KeyPressMsg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(k)
	return next.(Model), cmd
}

// deskRun runs a command, and feeds its messages back to the model, the way
// the program would.
func deskRun(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	var msgs []tea.Msg
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
	} else {
		msgs = append(msgs, msg)
	}
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

// chiefAsk is an ask that waits for someone other than the owner, as the
// gateway lists it.
func deskAskOf(id, next, text string) protocol.Ask {
	return protocol.Ask{ID: id, Kind: protocol.AskKindQuestion, State: protocol.AskWithSomeone, Next: next, FromKind: protocol.AskFromSession,
		FromSession: "s_0000000000000004", From: "strategies", Question: text, CreatedAt: t0.Add(-time.Hour)}
}

// deskChiefModel is the Desk as the owner sees it with a chief: the chief's
// name, what it handled, and a digest of three hours away.
func deskChiefModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := deskModel(t, w, h)
	m.chief, m.chiefLoaded = protocol.ChiefStatus{SessionID: "s_00000000000000c1", Name: "agent-net 2", Online: true, Model: "codex"}, true
	m.asks = append(m.asks, protocol.Ask{ID: "i_why", Kind: protocol.AskKindApproval, State: protocol.AskStateOpen, Next: protocol.AskNextOwner,
		FromKind: protocol.AskFromSession, FromSession: "s_0000000000000004", From: "ingest-forge", Question: "Pay for the new model?",
		Priority: 1, WhyYou: "the ingest-forge charter has no rule for spend", CreatedAt: t0.Add(-40 * time.Minute)})
	m.handled = []protocol.ChiefSort{
		{AskSort: protocol.AskSort{Ask: "i_c1", Result: protocol.AskSortClose, Reason: "CTO notice, moved to the log", At: t0.Add(-time.Hour)}, Question: "CTO applied graph edge set app -> ops"},
		{AskSort: protocol.AskSort{Ask: "i_s1", Result: protocol.AskSortSend, To: "s_00000000000000f1", Reason: "homelab-agent owns the deploy", At: t0.Add(-2 * time.Hour)}, Question: "ingest-forge blocked on deploy sha-6a257e2"},
		{AskSort: protocol.AskSort{Ask: "i_m1", Result: protocol.AskSortMerge, Into: "i_a", Reason: "same release", At: t0.Add(-3 * time.Hour)}, Question: "Release v1.2.3 to the machines again"},
		{AskSort: protocol.AskSort{Ask: "i_u1", Result: protocol.AskSortSend, To: "s_00000000000000f1", Reason: "clear caches", At: t0.Add(-4 * time.Hour)}, Question: "Mac disk 90%", Undone: true},
	}
	m.digest = &deskDigest{since: t0.Add(-3 * time.Hour), away: 3 * time.Hour, loaded: true,
		counts: deskCounts{closed: 22, sentOn: 4, answered: 2, forYou: 3}}
	return m
}

// Needs you lists only the asks that wait for the owner. The tab count, the
// header and the Sessions band count the same list.
func TestDeskNeedsYouShowsOnlyOwnerAsks(t *testing.T) {
	m := deskModel(t, 120, 40)
	m.asks = append(m.asks, deskAskOf("i_chief", protocol.AskNextChief, "Sorted by the chief"),
		deskAskOf("i_session", "s_00000000000000f1", "Waiting on carol"))
	m.asks = append(m.asks, protocol.Ask{ID: "i_why", Kind: protocol.AskKindApproval, State: protocol.AskStateOpen, Next: protocol.AskNextOwner,
		FromKind: protocol.AskFromSession, FromSession: "s_0000000000000004", From: "ingest-forge", Question: "Pay for the new model?",
		Priority: 1, WhyYou: "no spend rule", CreatedAt: t0})
	view := ansi.Strip(m.testView())
	for _, want := range []string{"Needs you · 3", "Desk (3)", "priority 1 · why you: no spend rule"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	for _, not := range []string{"Sorted by the chief", "Waiting on carol"} {
		if strings.Contains(view, not) {
			t.Errorf("an ask that waits for someone else is in Needs you: %q", not)
		}
	}
	if got := m.waitingCount(); got != 3 {
		t.Fatalf("waiting count %d, want 3", got)
	}
}

// Handled for you lists the chief's sorts with the result, the reason and the
// session a send went to. The header names the chief and counts its sorts.
func TestDeskHandledListsChiefSorts(t *testing.T) {
	m := deskChiefModel(t, 120, 40)
	view := ansi.Strip(m.testView())
	for _, want := range []string{
		"Desk · chief of staff agent-net 2 on · sorted 4 asks today",
		"Handled for you · 4",
		"closed   CTO applied graph edge",
		"CTO notice, moved to the log",
		"sent     ingest-forge blocked on deploy",
		"→ ingest-forge",
		"merged   Release v1.2.3 to the machines again",
		"undone   Mac disk 90%",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	m.inboxIndex = m.askRowCount() + len(m.deliveryVisible())
	if sort, ok := m.deskSelectedHandled(); !ok || sort.Ask != "i_c1" {
		t.Fatalf("the cursor is not on the first handled sort: %+v %v", sort, ok)
	}
}

// A chief that cannot work shows as one header line with the reason. A chief
// that can work keeps today's header.
func TestDeskHeaderShowsChiefOfflineReason(t *testing.T) {
	m := deskChiefModel(t, 120, 40)
	m.chief.Down = "codex quota exhausted (weekly window, until 2026-11-15 00:00 UTC)"
	line := ansi.Strip(m.deskHeaderLine(120))
	want := "Desk · chief of staff offline: codex quota exhausted (weekly window, until 2026-11-15 00:00 UTC) · sorted 4 asks today"
	if !strings.HasPrefix(line, want) {
		t.Fatalf("header = %q, want it to start with %q", line, want)
	}
	if strings.Contains(line, "\n") || ansi.StringWidth(line) > 120 {
		t.Fatalf("header is not one line: %q", line)
	}
	m.chief.Down = ""
	if got := ansi.Strip(m.deskHeaderLine(120)); !strings.HasPrefix(got, "Desk · chief of staff agent-net 2 on · sorted 4 asks today") {
		t.Fatalf("a working chief changed the header: %q", got)
	}
}

// The digest counts what happened while the owner was away an hour or more,
// and x hides it. A short absence shows no digest.
func TestDeskDigestAfterAnHourAwayHidesWithX(t *testing.T) {
	src := &deskCallSource{log: []protocol.DeskLogEntry{
		{ID: 9, At: t0.Add(-30 * time.Minute), Source: "chief", Text: "Chief closed ask: Is the thread answered?"},
		{ID: 8, At: t0.Add(-20 * time.Minute), Source: "chief", Text: "Chief sent ask to s_00000000000000f1: Who owns the cache?"},
		{ID: 7, At: t0.Add(-10 * time.Minute), Source: "chief", Text: "Chief sent ask to you (priority 1): Pay for the model?"},
		{ID: 6, At: t0.Add(-15 * time.Minute), Source: "desk", Text: "Closed question ask (owner replied): Merge PR 3?"},
		{ID: 5, At: t0.Add(-3 * time.Hour), Source: "chief", Text: "Chief closed ask: from before the absence"},
	}}
	m := deskModel(t, 120, 40)
	m.src = src
	m.tab = inboxTab
	m.now = func() time.Time { return t0 }
	m.lastInput = t0.Add(-2 * time.Hour)

	m, cmd := deskPress(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.digest == nil {
		t.Fatal("no digest after two hours away")
	}
	m = deskRun(t, m, cmd)
	view := ansi.Strip(m.testView())
	for _, want := range []string{"While you were away (2 h)", "closed 1 · sent on 1 · answered 1 · 1 for you", "x hide"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	if len(src.calls) != 1 || src.calls[0] != protocol.OpDeskLog {
		t.Fatalf("calls %v, want one desk log read", src.calls)
	}

	m, _ = deskPress(t, m, tea.KeyPressMsg{Text: "x", Code: 'x'})
	if strings.Contains(ansi.Strip(m.testView()), "While you were away") {
		t.Fatal("x did not hide the digest")
	}
	if got := m.waitingCount(); got != 2 {
		t.Fatalf("x dismissed an ask: %d need you", got)
	}

	short := deskModel(t, 120, 40)
	short.src = &deskCallSource{}
	short.now = func() time.Time { return t0 }
	short.lastInput = t0.Add(-30 * time.Minute)
	short, _ = deskPress(t, short, tea.KeyPressMsg{Code: tea.KeyDown})
	if short.digest != nil {
		t.Fatal("a digest after thirty minutes away")
	}
}

// u undoes a handled sort. m opens the one line of why and stores the mark.
// n on a Needs you row stores a didn't-need-me mark with its why.
func TestDeskUndoAndMarkKeys(t *testing.T) {
	src := &deskCallSource{}
	m := deskModel(t, 120, 40)
	m.src = src
	m.tab = inboxTab
	m.now = func() time.Time { return t0 }
	m.handled = []protocol.ChiefSort{{AskSort: protocol.AskSort{Ask: "i_1", Result: protocol.AskSortClose, Reason: "answered", At: t0}, Question: "Is the thread answered?"}}
	m.inboxIndex = m.askRowCount() + len(m.deliveryVisible())

	m, cmd := deskPress(t, m, tea.KeyPressMsg{Text: "u", Code: 'u'})
	m = deskRun(t, m, cmd)
	if len(src.calls) != 1 || src.calls[0] != protocol.OpAskUndo || src.args[0] != (protocol.AskUndoRequest{Ask: "i_1"}) {
		t.Fatalf("undo calls %v %v", src.calls, src.args)
	}
	if !strings.Contains(ansi.Strip(m.testView()), "undone") {
		t.Fatal("the undone sort is not marked on screen")
	}

	m, _ = deskPress(t, m, tea.KeyPressMsg{Text: "m", Code: 'm'})
	if m.deskPending == nil || !m.inboxEditing {
		t.Fatal("m did not open the line of why")
	}
	for _, r := range "the thread" {
		m, _ = deskPress(t, m, tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, cmd = deskPress(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = deskRun(t, m, cmd)
	last := src.args[len(src.args)-1]
	if src.calls[len(src.calls)-1] != protocol.OpAskMark || last != (protocol.AskMarkRequest{Ask: "i_1", Mark: protocol.MarkShouldHaveAsked, Why: "the thread"}) {
		t.Fatalf("mark calls %v args %+v", src.calls, last)
	}

	m.inboxIndex = 0
	m, _ = deskPress(t, m, tea.KeyPressMsg{Text: "n", Code: 'n'})
	if m.deskPending == nil {
		t.Fatal("n did not open the line of why on a Needs you row")
	}
	m, cmd = deskPress(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = deskRun(t, m, cmd)
	last = src.args[len(src.args)-1]
	if last != (protocol.AskMarkRequest{Ask: "i_q", Mark: protocol.MarkDidntNeedMe}) {
		t.Fatalf("n stored %+v", last)
	}
}

// An ask the chief sent to the owner stays in Handled for you after he answers
// it. There n stores a didn't-need-me mark (decision 0150, Desk marks on an
// answered item). Run with -v to see the frames at 80x24 and 120x34.
func TestDidntNeedMeOnAnAnsweredAskTheChiefSent(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		src := &deskCallSource{}
		m := deskModel(t, size[0], size[1])
		m.src = src
		m.tab = inboxTab
		m.now = func() time.Time { return t0 }
		m.asks = nil
		m.handled = []protocol.ChiefSort{{AskSort: protocol.AskSort{Ask: "i_9", Result: protocol.AskSortOwner, Priority: 2, WhyYou: "spends money", Reason: "a purchase", At: t0.Add(-time.Hour)}, Question: "Buy more DeepSeek credit?"}}
		m.inboxIndex = m.askRowCount() + len(m.deliveryVisible())
		view := ansi.Strip(m.testView())
		t.Logf("frame %dx%d:\n%s", size[0], size[1], view)
		if !strings.Contains(view, "didn't need me") {
			t.Fatalf("%dx%d: the answered ask offers no didn't-need-me mark:\n%s", size[0], size[1], view)
		}
		m, _ = deskPress(t, m, tea.KeyPressMsg{Text: "n", Code: 'n'})
		if m.deskPending == nil || !m.inboxEditing {
			t.Fatal("n did not open the line of why on a handled ask sent to the owner")
		}
		m, cmd := deskPress(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m = deskRun(t, m, cmd)
		last := src.args[len(src.args)-1]
		if src.calls[len(src.calls)-1] != protocol.OpAskMark || last != (protocol.AskMarkRequest{Ask: "i_9", Mark: protocol.MarkDidntNeedMe}) {
			t.Fatalf("mark calls %v args %+v", src.calls, last)
		}
	}
}

// d on a Needs you ask discusses it (decision 0150): for a chief item the
// gateway tells and wakes the chief, and the TUI enters the chief's session
// the way Enter does. Run with -v to see the frames at 80x24 and 120x34.
func TestDiscussOpensTheChiefsSession(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		src := &deskCallSource{}
		m := deskModel(t, size[0], size[1])
		m.src = src
		m.tab = inboxTab
		m.now = func() time.Time { return t0 }
		m.inboxIndex = 0
		view := ansi.Strip(m.testView())
		t.Logf("frame %dx%d:\n%s", size[0], size[1], view)
		if !strings.Contains(view, "d discuss") {
			t.Fatalf("%dx%d: no discuss hint on an open ask:\n%s", size[0], size[1], view)
		}
		m, cmd := deskPress(t, m, tea.KeyPressMsg{Text: "d", Code: 'd'})
		if cmd == nil {
			t.Fatal("d did nothing")
		}
		next, enter := m.Update(cmd())
		m = next.(Model)
		if src.calls[len(src.calls)-1] != protocol.OpAskDiscuss || src.args[len(src.args)-1] != (protocol.AskDiscussRequest{Ask: "i_q"}) {
			t.Fatalf("discuss calls %v %v", src.calls, src.args)
		}
		if enter == nil {
			t.Fatal("no enter of the chief's session")
		}
		if msg, ok := enter().(enterMsg); !ok || msg.name != "chief of staff" || len(src.enters) != 1 || src.enters[0].SessionID != "s_chief" {
			t.Fatalf("enter %+v, enters %+v", msg, src.enters)
		}
		if !strings.Contains(ansi.Strip(m.testView()), "Waking the chief of staff") {
			t.Fatal("the owner is not told that the chief is woken")
		}
	}
}

// In the details of a question, w answers the marked option and keeps it as a
// standing rule: the owner types "<action> in <area>" (decision 0150). Run
// with -v to see the frames at 80x24 and 120x34.
func TestAnswerAlwaysSendsTheScope(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		src := &deskCallSource{}
		m := deskModel(t, size[0], size[1])
		m.src = src
		m.tab = inboxTab
		m.now = func() time.Time { return t0 }
		m.asks[0].Node = "ingest-forge"
		m.inboxIndex = 0
		m, _ = deskPress(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		view := ansi.Strip(m.testView())
		if !strings.Contains(view, "w answer always") {
			t.Fatalf("%dx%d: details offer no answer always:\n%s", size[0], size[1], view)
		}
		m, _ = deskPress(t, m, tea.KeyPressMsg{Text: "w", Code: 'w'})
		if !m.inboxEditing {
			t.Fatal("w did not open the rule line")
		}
		for _, r := range "merge" {
			m, _ = deskPress(t, m, tea.KeyPressMsg{Text: string(r), Code: r})
		}
		view = ansi.Strip(m.testView())
		t.Logf("frame %dx%d:\n%s", size[0], size[1], view)
		if !strings.Contains(view, "merge in repo:ingest-forge") {
			t.Fatalf("%dx%d: the rule line is not prefilled with the ask's repo:\n%s", size[0], size[1], view)
		}
		m, cmd := deskPress(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("enter sent nothing")
		}
		cmd()
		b, _ := json.Marshal(src.args[len(src.args)-1])
		if src.calls[len(src.calls)-1] != "inbox_answer" || !strings.Contains(string(b), `"answer":"yes"`) ||
			!strings.Contains(string(b), `"area":"repo:ingest-forge"`) || !strings.Contains(string(b), `"action":"merge"`) {
			t.Fatalf("answer call %v %s", src.calls, b)
		}
	}
}
