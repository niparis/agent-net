package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/protocol"
)

// drainSnippets runs the snippet fetches the way the program does, one at a
// time. The test helpers drop the command that Update returns, so a fetch
// marked as running is cleared first.
func drainSnippets(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < 100; i++ {
		for _, it := range m.feed.items {
			if it.Snip == snipLoading {
				it.Snip = snipNone
			}
		}
		cmd := m.wantSnippets()
		if cmd == nil {
			return m
		}
		m = send(m, cmd())
	}
	t.Fatal("the snippets did not settle")
	return m
}

// feedText draws the whole feed, newest lines included.
func feedText(m Model, w int) string {
	return strings.Join(m.feedLines(w, len(m.feed.items)+5), "\n")
}

func feedModel(events []protocol.FeedEvent, messages map[string]protocol.Message) (Model, *fakeSource) {
	src := &fakeSource{snap: fakeSnapshot(), events: events, messages: messages}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = refresh(m, src)
	return m, src
}

func TestFeedLineQuotesTheFirstLineOfTheBody(t *testing.T) {
	m, _ := feedModel(fakeEvents(), map[string]protocol.Message{
		"m_1": {ID: "m_1", Body: "Deploy done, digest sha256:abc123\nsecond line is not shown"},
	})
	m = drainSnippets(t, m)
	text := feedText(m, 100)
	if !strings.Contains(text, `"Deploy done, digest sha256:abc123"`) {
		t.Fatalf("no quoted snippet in:\n%s", text)
	}
	if strings.Contains(text, "second line") {
		t.Fatalf("more than the first line was shown:\n%s", text)
	}
}

func TestFeedLineShowsOneStateSymbol(t *testing.T) {
	m, _ := feedModel(fakeEvents(), nil)
	text := feedText(m, 100)
	for _, want := range []string{
		"✓", // m_1 was delivered
		"↩", // m_1 was answered by m_2
		"…", // m_5 was only dispatched
		"✗", // m_4 failed
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "delivered") || strings.Contains(text, "dispatched") || strings.Contains(text, "failed") {
		t.Errorf("a state word is still in the line:\n%s", text)
	}
}

func TestFeedRepeatedDeliveryEventsKeepOneLine(t *testing.T) {
	const a, b = "s_0000000000000001", "s_0000000000000002"
	events := []protocol.FeedEvent{
		ev(1, -2*time.Minute, "msg.stored", "m_1", a, b, "request", nil),
		ev(2, -2*time.Minute, "msg.dispatched", "m_1", a, b, "request", nil),
		ev(3, -time.Minute, "msg.delivered", "m_1", a, b, "request", nil),
		ev(4, -time.Minute, "msg.dispatched", "m_1", a, b, "request", nil),
		ev(5, -time.Minute, "msg.delivered", "m_1", a, b, "request", nil),
	}
	m, _ := feedModel(events, nil)
	text := feedText(m, 100)
	if got := strings.Count(text, "tenebrae-53 -> strategies"); got != 1 {
		t.Fatalf("%d lines for one message:\n%s", got, text)
	}
	if !strings.Contains(text, "✓") {
		t.Fatalf("the state symbol is not up to date:\n%s", text)
	}
	if strings.Contains(text, "✗") || strings.Contains(text, "…") {
		t.Fatalf("an old state is still in the line:\n%s", text)
	}
}

func TestFeedNamesGetAHostMarkOnlyWhenTheyRepeat(t *testing.T) {
	m, _ := feedModel(fakeEvents(), nil)
	if text := feedText(m, 100); strings.Contains(text, "@Mac") {
		t.Fatalf("a unique name got a host mark:\n%s", text)
	}
	m.snap.Agents[1].Profile.Name = "tenebrae-53" // the recipient of m_1
	text := feedText(m, 100)
	if !strings.Contains(text, "tenebrae-53@Mac -> tenebrae-53@Mac") {
		t.Fatalf("the repeated name has no short host mark:\n%s", text)
	}
	if strings.Contains(text, "/Users/") || strings.Contains(text, "n_mac") {
		t.Fatalf("the feed shows a full host name or node ID:\n%s", text)
	}
}

func TestFeedNamesHaveNoHost(t *testing.T) {
	m, _ := feedModel(fakeEvents(), nil)
	text := feedText(m, 100)
	for _, bad := range []string{"(Mac)", "(build-vm)", "you (on"} {
		if strings.Contains(text, bad) {
			t.Errorf("the feed shows the host as %q:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, "tenebrae-53 -> strategies") {
		t.Fatalf("the names are not both there:\n%s", text)
	}
}

func TestFeedFetchesOnlyMessagesThisMachineHasAnEndOf(t *testing.T) {
	events := []protocol.FeedEvent{
		ev(1, -2*time.Minute, "msg.stored", "m_remote", "s_0000000000000004", "s_0000000000000099", "request", nil),
		ev(2, -1*time.Minute, "msg.stored", "m_local", "s_0000000000000004", "s_0000000000000001", "request", nil),
	}
	m, src := feedModel(events, map[string]protocol.Message{
		"m_remote": {ID: "m_remote", Body: "this body belongs to two other machines"},
		"m_local":  {ID: "m_local", Body: "this one is ours"},
	})
	m = drainSnippets(t, m)
	for _, id := range src.msgReqs {
		if id == "m_remote" {
			t.Fatalf("asked for the body of a message between two other machines: %v", src.msgReqs)
		}
	}
	text := feedText(m, 100)
	if strings.Contains(text, "this body belongs to two other machines") {
		t.Fatalf("a remote body was shown:\n%s", text)
	}
	if !strings.Contains(text, `"this one is ours"`) {
		t.Fatalf("the local body was not shown:\n%s", text)
	}
}

func TestFeedFetchesEachSnippetOnce(t *testing.T) {
	m, src := feedModel(fakeEvents(), map[string]protocol.Message{
		"m_1": {ID: "m_1", Body: "hello"},
	})
	m = drainSnippets(t, m)
	first := len(src.msgReqs)
	if first == 0 {
		t.Fatal("no snippet was fetched")
	}
	m = refresh(m, src)
	m = drainSnippets(t, m)
	if len(src.msgReqs) != first {
		t.Fatalf("fetched again: %v", src.msgReqs)
	}
}

func TestFeedSnippetCleansControlCharacters(t *testing.T) {
	m, _ := feedModel(fakeEvents(), map[string]protocol.Message{
		"m_1": {ID: "m_1", Body: "line one\x1b[31m red?\x07\nsecond"},
	})
	m = drainSnippets(t, m)
	text := feedText(m, 100)
	if strings.ContainsRune(text, '\x1b') || strings.ContainsRune(text, '\x07') {
		t.Fatalf("control characters are still there: %q", text)
	}
	if !strings.Contains(text, `"line one[31m red?"`) {
		t.Fatalf("the first line is not clean and quoted:\n%s", text)
	}
	if strings.Contains(text, "second") {
		t.Fatalf("the second line leaked in:\n%s", text)
	}
}

func TestFeedReplySitsUnderItsParent(t *testing.T) {
	m, _ := feedModel(fakeEvents(), nil)
	lines := m.feedLines(100, 30)
	reply := -1
	for i, l := range lines {
		if strings.Contains(l, "reply") {
			reply = i
		}
	}
	if reply < 0 {
		t.Fatalf("no reply line:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[reply], "└") {
		t.Fatalf("the reply is not indented: %q", lines[reply])
	}
	if reply == 0 || !strings.Contains(lines[reply-1], "request") {
		t.Fatalf("the reply does not follow its parent:\n%s", strings.Join(lines, "\n"))
	}
}

func TestFeedReplyIsNotIndentedWhenItsParentIsScrolledOut(t *testing.T) {
	m, _ := feedModel(fakeEvents(), nil)
	// Four rows put the reply at the top; its parent is above the window.
	lines := m.feedLines(100, 4)
	if !strings.Contains(lines[0], "reply") {
		t.Fatalf("the window does not start at the reply:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[0], "└") {
		t.Fatalf("a reply is indented with no parent above it: %q", lines[0])
	}
	// Five rows show the parent first, so the reply hangs under it.
	lines = m.feedLines(100, 5)
	if !strings.Contains(lines[1], "└") {
		t.Fatalf("the reply does not hang under its parent:\n%s", strings.Join(lines, "\n"))
	}
}

func TestFeedLifecycleEventsAreHiddenByDefault(t *testing.T) {
	events := []protocol.FeedEvent{
		ev(1, -3*time.Minute, "session.new", "", "", "", "", func(e *protocol.FeedEvent) { e.Detail = "name=fresh harness=claude" }),
		ev(2, -2*time.Minute, "worker.started", "w_1", "", "", "", func(e *protocol.FeedEvent) { e.Detail = "harness=opencode project=shop owner=s_0000000000000001" }),
		ev(3, -time.Minute, "worker.stopped", "w_1", "", "", "", func(e *protocol.FeedEvent) { e.Detail = "reason=idle" }),
		ev(4, 0, "worker.refused", "w_2", "", "", "", func(e *protocol.FeedEvent) { e.Detail = "project=nope reason=no such project" }),
	}
	m, _ := feedModel(events, nil)
	text := feedText(m, 100)
	if strings.Contains(text, "new session") {
		t.Fatalf("a new session line is shown by default:\n%s", text)
	}
	for _, want := range []string{"worker started", "worker stopped", "worker refused"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%s line is missing:\n%s", want, text)
		}
	}
	m = keys(m, "l")
	all := feedText(m, 100)
	if !strings.Contains(all, "new session") || !strings.Contains(all, "worker refused") {
		t.Fatalf("l did not show every lifecycle line:\n%s", all)
	}
	m = keys(m, "l")
	none := feedText(m, 100)
	if strings.Contains(none, "new session") || strings.Contains(none, "worker started") {
		t.Fatalf("l did not hide the lifecycle lines:\n%s", none)
	}
	m = keys(m, "l")
	if !strings.Contains(feedText(m, 100), "worker started") {
		t.Fatalf("l did not return to the default:\n%s", feedText(m, 100))
	}
}

func TestFeedCursorSkipsHiddenLines(t *testing.T) {
	events := []protocol.FeedEvent{
		ev(1, -2*time.Minute, "msg.stored", "m_1", "s_0000000000000001", "s_0000000000000002", "request", nil),
		ev(2, -time.Minute, "session.new", "", "", "", "", func(e *protocol.FeedEvent) { e.Detail = "name=fresh harness=claude" }),
	}
	m, _ := feedModel(events, nil)
	m = keys(m, "2")
	if it := m.feedItemAt(m.feedCur); it == nil || it.Kind != "msg.stored" {
		t.Fatalf("the cursor sits on a hidden line: %+v", it)
	}
	m = keys(m, "up")
	if m.feedCur != 0 {
		t.Fatalf("the cursor moved past the hidden line: %d", m.feedCur)
	}
}

func TestFeedEnterOpensTheConversationOfTheLocalEnd(t *testing.T) {
	m, _ := feedModel(fakeEvents(), nil)
	m = keys(m, "2")
	for m.feed.items[m.feedCur].MsgID != "m_1" {
		m = keys(m, "up")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.conv == nil || m.conv.id != "s_0000000000000002" {
		t.Fatalf("enter opened %+v, want the conversation of the recipient", m.conv)
	}
	if cmd == nil {
		t.Fatal("enter did not start loading the conversation")
	}
}

func TestFeedVOpensTheMessageBody(t *testing.T) {
	m, _ := feedModel(fakeEvents(), nil)
	m = keys(m, "2")
	for m.feed.items[m.feedCur].MsgID != "m_1" {
		m = keys(m, "up")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = next.(Model)
	if m.open == nil || m.open.id != "m_1" || cmd == nil {
		t.Fatalf("v did not open the body: %+v", m.open)
	}
}

func TestGoldenFeedSnippets(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), messages: map[string]protocol.Message{
		"m_1": {ID: "m_1", Body: "Deploy done, digest sha256:9f2c1a7b\nrun the smoke test when you can"},
		"m_2": {ID: "m_2", Body: "Checked on build-vm, all green."},
		"m_3": {ID: "m_3", Body: "Which model do you run for the risk model?"},
		"m_4": {ID: "m_4", Body: "Heads up: the nightly job moved to 03:00."},
		"m_5": {ID: "m_5", Body: "Can you review tenebrae-nexus/truenas-stacks #817?"},
	}}
	src.usage, src.counts, src.tops = fakeUsage(), fakeCounts(), fakeTops()
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	m = drainSnippets(t, m)
	golden(t, "view_120x34_feed_snippets", m.View())
}

func TestHelpListsTheMessageStates(t *testing.T) {
	out := keys(newModel(120, 40), "?").View()
	for _, want := range []string{"pending", "delivered", "replied", "failed", "…", "✓", "↩", "✗"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help lacks %q", want)
		}
	}
}

func TestMessagesPanelAndCTOColors(t *testing.T) {
	m := keys(newModel(120, 34), "2")
	it := m.feed.items[m.feedCur]
	it.FromCTO = true
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.TrueColor)
	v := m.View()
	for _, want := range []string{"Messages ·", "▸", "CTO", "48;2;34;48;58"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(v, lipgloss.NewStyle().Foreground(roleCTO).Render("CTO")) {
		t.Error("CTO sender is not purple")
	}
	for _, tc := range []struct {
		state string
		color lipgloss.TerminalColor
	}{{protocol.StateDelivered, roleGood}, {protocol.StateFailed, roleError}, {protocol.StateReplied, roleAccent}, {protocol.StateStored, roleDim}} {
		if !strings.Contains(messageState(tc.state), lipgloss.NewStyle().Foreground(tc.color).Render(stateSymbol(tc.state))) {
			t.Errorf("wrong state color: %s", tc.state)
		}
	}
}

func TestOlderFeedDetectsCTOSender(t *testing.T) {
	f := newFeed()
	f.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "m_cto", Detail: "type=request by=cto"}}})
	if f.byMsg["m_cto"] == nil || !f.byMsg["m_cto"].FromCTO {
		t.Fatal("CTO sender lost from older feed")
	}
}

func TestReplyIndentUsesBoxDrawingMarker(t *testing.T) {
	m, _ := feedModel(fakeEvents(), nil)
	if !strings.Contains(feedText(m, 100), "└") {
		t.Fatal("reply indentation marker is missing")
	}
}
