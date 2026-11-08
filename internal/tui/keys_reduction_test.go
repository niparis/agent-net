package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The owner cut direct session settings keys in favour of one S menu.
func TestSessionsHasOneSettingsKeyAndNoDirectOnes(t *testing.T) {
	if got := keyAction(contextSessions, "S"); got != "" {
		t.Fatalf("S still acts: %q", got)
	}
	for _, key := range []string{"t", "I", "N", "L", "o", "A"} {
		if got := keyAction(contextSessions, key); got != "" {
			t.Errorf("removed Sessions key %q still does %q", key, got)
		}
	}
}

// v is the only session view key: active, then all with the offline sessions.
// H is the separate hidden toggle.
func TestSessionsViewCyclesWithOneKey(t *testing.T) {
	m := newModel(120, 34)
	if keyAction(contextSessions, "v") != "cycle view" {
		t.Fatal("v is not the view key")
	}
	if keyAction(contextSessions, "H") != "show hidden" {
		t.Fatal("H is not the hidden toggle")
	}
	if got := m.sessionViewName(); got != "active" || !m.activeOnly {
		t.Fatalf("default view %q", got)
	}
	m = keys(m, "v")
	if m.sessionViewName() != "all" || !m.showOffline || m.activeOnly {
		t.Fatalf("v did not show all: %q", m.sessionViewName())
	}
	m = keys(m, "v")
	if m.sessionViewName() != "active" || m.showOffline || !m.activeOnly {
		t.Fatalf("v did not return to active: %q", m.sessionViewName())
	}
	if !strings.Contains(ansi.Strip(strings.Join(m.sessionsTable(120, 10), "\n")), "view: active") {
		t.Fatalf("the Sessions title does not name the view: %q", ansi.Strip(m.titleLine(120)))
	}
}

// l is the only Messages filter key: all, without worker threads, with system
// events.
func TestMessagesFilterCyclesWithOneKey(t *testing.T) {
	m := newModel(120, 34)
	m.tab = messagesTab
	if keyAction(contextMessages, "l") != "cycle message filter" {
		t.Fatal("l is not the message filter key")
	}
	for _, key := range []string{"w", "e"} {
		if got := keyAction(contextMessages, key); got != "" {
			t.Errorf("removed Messages key %q still does %q", key, got)
		}
	}
	m = keys(m, "l")
	if !m.hideWorkerThreads || m.messageShowEvents {
		t.Fatalf("first l: workers hidden %v events %v", m.hideWorkerThreads, m.messageShowEvents)
	}
	m = keys(m, "l")
	if m.hideWorkerThreads || !m.messageShowEvents {
		t.Fatalf("second l: workers hidden %v events %v", m.hideWorkerThreads, m.messageShowEvents)
	}
	m = keys(m, "l")
	if m.hideWorkerThreads || m.messageShowEvents {
		t.Fatalf("third l: workers hidden %v events %v", m.hideWorkerThreads, m.messageShowEvents)
	}
	if !strings.Contains(ansi.Strip(strings.Join(m.messagesView(120, 12), "\n")), "filter: all") {
		t.Fatalf("the Messages title does not name the filter:\n%s", ansi.Strip(strings.Join(m.messagesView(120, 12), "\n")))
	}
}

// The old graph and card keys are gone, and the graph still fits when it opens.
func TestGraphRemovedKeysDoNothing(t *testing.T) {
	m := graphModel(120, 34)
	zoom := m.graphState.Camera.Zoom
	for _, key := range []string{"o", " ", "0"} {
		if got := keyAction(contextGraph, key); got != "" {
			t.Errorf("removed graph key %q still does %q", key, got)
		}
	}
	m = keys(m, "o", " ", "0")
	if m.tab != graphTab || m.graphState.Orbit || m.graphState.Paused || m.graphState.Camera.Zoom != zoom {
		t.Fatalf("a removed graph key changed the graph: tab %v orbit %v paused %v zoom %v",
			m.tab, m.graphState.Orbit, m.graphState.Paused, m.graphState.Camera.Zoom)
	}
	if keyAction(contextCard, "!") != "" {
		t.Fatal("the card still binds the legend toggle")
	}
}

// helpContent is built from the table: the current screen first, then the
// fixed Global keys.
func TestHelpSectionsFollowTheCurrentTab(t *testing.T) {
	for _, tc := range []struct {
		tab  int
		key  string
		name string
	}{
		{sessionsTab, "1", "Sessions"},
		{graphTab, "2", "Graph"},
		{quotasTab, "3", "Quotas"},
		{messagesTab, "4", "Messages"},
		{toolsTab, "5", "Tools"},
		{settingsTab, "6", "Settings"},
		{inboxTab, "7", "Desk"},
	} {
		m := keys(newModel(120, 40), tc.key, "?")
		text := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
		if !strings.HasPrefix(text, " "+tc.name+"\n") {
			t.Fatalf("%s: current section is not first:\n%s", tc.name, text)
		}
		if !strings.Contains(text, "\n Global keys\n") {
			t.Fatalf("%s: no Global keys section:\n%s", tc.name, text)
		}
		if strings.Contains(text, " In overlays") {
			t.Fatalf("%s: this screen help also shows the full list", tc.name)
		}
		for i, name := range tabNames {
			if i == tc.tab {
				continue
			}
			if strings.Contains(text, "\n "+name+"\n") {
				t.Fatalf("%s: help also shows the %s section", tc.name, name)
			}
		}
	}
}

// Opening help always starts at the current screen's section, then Global
// keys.
func TestHelpOpensAtTheCurrentScreen(t *testing.T) {
	m := keys(newModel(80, 24), "?")
	m = keys(m, "pgdown", "esc")
	m = keys(m, "?")
	if m.helpScroll != 0 {
		t.Fatalf("help reopened at scroll %d", m.helpScroll)
	}
	view := ansi.Strip(strings.Join(m.helpContent(80), "\n"))
	if !strings.HasPrefix(view, " Sessions\n") {
		t.Fatalf("help does not start with the current screen:\n%s", view)
	}
	if !strings.Contains(view, "\n Global keys\n") {
		t.Fatalf("Global keys do not follow the current screen:\n%s", view)
	}
}

// Global rows are the fixed list, never labels joined from other contexts.
func TestHelpHasNoJoinedContextLists(t *testing.T) {
	m := newModel(120, 40)
	m.helpAll = true
	text := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.helpContent(120), "\n"))), " ")
	for _, bad := range []string{
		"back; clear or back",
		"filter; find a node; enter selects the first match and esc cancels",
		"move the selection; select a conversation",
		"field; back; next field",
	} {
		if strings.Contains(text, bad) {
			t.Errorf("help still joins contexts: %q", bad)
		}
	}
}

// Sub-sections inside a tab start on their own line after a blank one.
func TestHelpSubsectionsHaveBlankLines(t *testing.T) {
	m := newModel(120, 60)
	m.helpAll = true
	view := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	for _, heading := range []string{
		"On a held request row", "On a learned suggestion row", "On an outcome row",
		"On a CTO proposal row", "Agent card",
	} {
		if !strings.Contains(view, "\n\n "+heading+"\n") {
			t.Errorf("help has no blank line before %q", heading)
		}
	}
}

// A direction pair shares one row, and a page row is not repeated.
func TestHelpPairsAreMerged(t *testing.T) {
	m := newModel(120, 60)
	m.helpAll = true
	text := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	for _, want := range []string{
		"pgup/pgdn", "home/g, end", "up/down, j/k", "shift+arrows", ", .",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("help lacks the merged pair %q", want)
		}
	}
}

// No hand-written key rows remain: every key row comes from the table.
func TestHelpHasNoHandWrittenKeyRows(t *testing.T) {
	src, err := os.ReadFile("help.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "[][2]string") || strings.Contains(string(src), "[2]string{") {
		t.Fatal("help.go still holds hand-written key rows")
	}
}

// The graph fits itself when it loads, so 0 is not needed.
func TestGraphFitsWhenItLoads(t *testing.T) {
	m := mapModel(120, 34, graphFixture())
	m.applyMap(mapMsg{view: graphFixture(), at: t0})
	w, h := m.graphSize()
	want := m.graphState.Graph.Fit(w, h)
	if m.graphState.Camera.Zoom != want.Zoom || m.graphState.Camera.Center != want.Center {
		t.Fatalf("the first graph did not fit: got %+v want %+v", m.graphState.Camera, want)
	}
}

// Removing a direct key means it does nothing on Sessions and Messages.
func TestRemovedDirectKeysDoNothing(t *testing.T) {
	m := newModel(120, 34)
	view := m.sessionViewName()
	m = keys(m, "t", "p", "I", "N", "L", "o", "A")
	if m.mode != modeNormal || m.sessionViewName() != view {
		t.Fatalf("a removed Sessions key still acted: mode %v view %q", m.mode, m.sessionViewName())
	}
	msg := newModel(120, 34)
	msg.tab = messagesTab
	msg = keys(msg, "w", "e")
	if msg.hideWorkerThreads || msg.messageShowEvents {
		t.Fatalf("removed Messages keys still acted: workers %v events %v", msg.hideWorkerThreads, msg.messageShowEvents)
	}
}

// c opens the CTO everywhere; C focuses its box. The old card row said the
// opposite.
func TestHelpSaysCOpensAndCFocuses(t *testing.T) {
	m := keys(newModel(120, 40), "2", "?")
	m.helpAll = true
	text := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.helpContent(120), "\n"))), " ")
	for _, want := range []string{"c open the CTO session", "C focus the CTO box"} {
		if !strings.Contains(text, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	if strings.Contains(text, "c focus the CTO box") {
		t.Error("help still says c focuses the CTO box")
	}
}

// The card legend moved into the help overlay.
func TestHelpCarriesTheAgentCardLegend(t *testing.T) {
	m := keys(newModel(120, 40), "2", "?")
	text := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	for _, want := range []string{"Legend", "consumer uses provider", "delivered; B judges it", "held in the Inbox"} {
		if !strings.Contains(text, want) {
			t.Errorf("help legend lacks %q", want)
		}
	}
	if strings.Contains(text, "! legend") {
		t.Error("help still offers the removed legend key")
	}
}
