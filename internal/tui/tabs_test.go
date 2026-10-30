package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestTabSwitching(t *testing.T) {
	m := newModel(80, 24)
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions  2 Messages  3 Quotas") {
		t.Fatal(m.titleLine(80))
	}
	m = keys(m, "3")
	if m.tab != 2 {
		t.Fatal("2 did not select quotas")
	}
	m = keys(m, "1")
	if m.tab != 0 {
		t.Fatal("1 did not select sessions")
	}
	m = keys(m, "/", "2")
	if m.tab != 0 || m.filter != "2" {
		t.Fatal("filter intercepted tab key")
	}
}

func TestMergeQuotaReadings(t *testing.T) {
	m := newModel(80, 24)
	m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: 20, Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}, {Provider: "claude", Plan: "max"}}}
	m.usage.raw.Gateway.Nodes = []protocol.UsageNode{{NodeID: "other", Hostname: "other", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: 10, Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}, {Provider: "claude", Plan: "team"}}}}}}
	rows := m.quotaRows()
	if len(rows) != 3 {
		t.Fatalf("rows: %+v", rows)
	}
	for _, r := range rows {
		if r.provider.Provider == "codex" && (r.at != 20 || r.machine != "") {
			t.Fatalf("merge: %+v", r)
		}
		if r.provider.Provider == "claude" && r.machine == "" {
			t.Fatalf("different plans need machine: %+v", r)
		}
	}
}

func TestQuotaViewGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Add(-40 * time.Second).Unix(), Providers: []protocol.QuotaProvider{
			{Provider: "codex", Plan: "pro", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 92, Pace: protocol.QuotaPaceAhead, ResetsAt: t0.Add(2 * time.Hour).Unix()}}},
			{Provider: "cursor", Plan: "business", Windows: []protocol.QuotaWindow{{ID: "month", PercentUsed: 100, Exhausted: true}}},
			{Provider: "claude", Plan: "max", Stale: true, Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 50, Pace: protocol.QuotaPaceBehind}}},
		}}
		m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "other", Hostname: "other", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: t0.Add(-time.Minute).Unix(), Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}}}}})
		view := m.View()
		for _, line := range lines(view) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("wide line: %q", line)
			}
		}
		for _, line := range lines(ansi.Strip(view)) {
			for _, value := range []string{"behind", "ahead", "exhausted"} {
				if i := strings.Index(line, value); i >= 0 {
					col := ansi.StringWidth(line[:i])
					if value == "exhausted" {
						col = ansi.StringWidth(line[:i+len(value)]) + 1
					}
					if col != 60 {
						t.Errorf("pace starts at column %d in %q", col, line)
					}
				}
			}
		}
		if !strings.Contains(ansi.Strip(view), "exhausted") {
			t.Fatal(view)
		}
		golden(t, fmt.Sprintf("quotas_%dx%d", size[0], size[1]), view)
	}
}

func TestQuotaViewKeepsFooterWithManyRows(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	q := &protocol.Quotas{GeneratedAt: t0.Unix()}
	for i := 0; i < 30; i++ {
		q.Providers = append(q.Providers, protocol.QuotaProvider{Provider: fmt.Sprintf("provider%d", i), Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 50}}})
	}
	m.usage.raw.Local.Quotas = q
	if !strings.Contains(ansi.Strip(m.View()), "workers now:") {
		t.Fatal("footer lost")
	}
}

func TestQuotaViewScrolls(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	q := &protocol.Quotas{}
	for i := 0; i < 30; i++ {
		q.Providers = append(q.Providers, protocol.QuotaProvider{Provider: fmt.Sprintf("p%02d", i), Windows: []protocol.QuotaWindow{{ID: "week"}}})
	}
	m.usage.raw.Local.Quotas = q
	if strings.Contains(ansi.Strip(m.View()), "p29") {
		t.Fatal("last row visible before scroll")
	}
	for i := 0; i < 80; i++ {
		m = keys(m, "down")
	}
	if !strings.Contains(ansi.Strip(m.View()), "p29") {
		t.Fatal("last row not reachable")
	}
}

func TestQuotaEmptyAndMachineErrors(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "No machine reads quotas yet: quota-axi is not installed on any machine (see docs/setup.md, Quota).") || strings.Contains(v, "PROVIDER") {
		t.Fatal(v)
	}
	m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "error-node", Hostname: "build", Usage: protocol.UsageReport{QuotaError: "it failed: exit status 1"}})
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "build: it failed: exit status 1") || strings.Contains(v, "not installed on any machine") || strings.Contains(v, "PROVIDER") {
		t.Fatal(v)
	}
}

func TestTabStyleAndTitleSeparator(t *testing.T) {
	if !sActiveTab.GetBold() || sActiveTab.GetBackground() == nil {
		t.Fatal("active tab must be bold with a background")
	}
	m := newModel(80, 24)
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions  2 Messages  3 Quotas  4 Map │ ") {
		t.Fatal(ansi.Strip(m.titleLine(80)))
	}
	m.tab = 2
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions  2 Messages  3 Quotas  4 Map │ ") {
		t.Fatal(ansi.Strip(m.titleLine(80)))
	}
}

func TestTabsMoveFeedToMessages(t *testing.T) {
	m := newModel(80, 24)
	if !strings.Contains(m.View(), "1 Sessions") {
		t.Fatal("sessions tab missing")
	}
	m = keys(m, "2")
	if !strings.Contains(m.View(), "2 Messages") || !strings.Contains(m.View(), "Messages") {
		t.Fatal("messages tab missing")
	}
	m = keys(m, "3")
	if !strings.Contains(m.View(), "3 Quotas") {
		t.Fatal("quotas tab missing")
	}
}

func TestCTOBoxFocusAndSend(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m := New(src)
	m = keys(m, "tab")
	if m.focus != focusCTO {
		t.Fatal("tab did not focus CTO")
	}
	m = keys(m, "h", "i")
	next, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("send missing")
	}
	cmd()
	if len(src.sent) != 1 || src.sent[0] != "hi" {
		t.Fatalf("sent %v", src.sent)
	}
	m = keys(m, "esc")
	if m.focus != focusSessions {
		t.Fatal("escape did not return focus")
	}
}

func TestCTOBoxAlwaysVisibleAndFocusReturns(t *testing.T) {
	m := newModel(80, 24)
	if !strings.Contains(m.View(), "write to the CTO: set charters") {
		t.Fatal("empty CTO box missing")
	}
	m = keys(m, "2", "C")
	if m.tab != 0 || m.focus != focusCTO {
		t.Fatal("C did not focus CTO from Messages")
	}
	m = keys(m, "tab")
	if m.focus != focusSessions {
		t.Fatal("tab did not return to list")
	}
}

func TestCTONewRequiresConfirmation(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m := keys(New(src), "C")
	next, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if cmd != nil || !m.cto.confirming || src.fresh != 0 {
		t.Fatal("new did not ask")
	}
	next, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("confirmation did not start new conversation")
	}
	cmd()
	if src.fresh != 1 {
		t.Fatal("new conversation was not requested")
	}
}

func TestDetailToggleKeepsCTOVisible(t *testing.T) {
	m := keys(newModel(120, 40), "d")
	if !strings.Contains(m.View(), "Selected session") || !strings.Contains(m.View(), "CTO") {
		t.Fatal("detail or CTO missing")
	}
	m = keys(m, "d")
	if !strings.Contains(m.View(), "NAME") {
		t.Fatal("session list did not return")
	}
}

func detailText(m Model) string { return strings.Join(m.detailLines(m.width, 40), "\n") }

func TestLayoutGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for _, conversation := range []bool{false, true} {
			src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), usage: fakeUsage(), counts: fakeCounts()}}
			if conversation {
				src.events = protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"user","text":"i want to set up the charter for cogforge, help me"}`), json.RawMessage(`{"type":"assistant","text":"Sure. What does cogforge own today?"}`)}, Actions: []protocol.CTOAction{{Text: "read graph"}}}
			}
			m := New(src)
			m.loc, m.now = time.UTC, func() time.Time { return t0 }
			m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = refresh(m, src.fakeSource)
			m.src = src
			m = send(m, m.ctoPollCmd()())
			m = keys(m, "C")
			kind := "empty"
			if conversation {
				kind = "conversation"
			}
			v := m.View()
			for _, line := range lines(v) {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("line exceeds %d: %q", size[0], line)
				}
			}
			golden(t, fmt.Sprintf("sessions_%dx%d_%s_focused", size[0], size[1], kind), v)
		}
		m := keys(newModel(size[0], size[1]), "2")
		v := m.View()
		for _, line := range lines(v) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("line exceeds %d: %q", size[0], line)
			}
		}
		golden(t, fmt.Sprintf("messages_%dx%d", size[0], size[1]), v)
	}
}

func TestCTOFocusFromConversationAndBody(t *testing.T) {
	m := newModel(80, 24)
	m.conv = &convState{id: "session"}
	m = keys(m, "C")
	if m.focus != focusCTO || m.conv != nil || m.tab != 0 {
		t.Fatal("C did not leave conversation for CTO")
	}
	m = keys(m, "esc", "2")
	m.open = &openMsg{id: "message"}
	m = keys(m, "C")
	if m.focus != focusCTO || m.open != nil || m.tab != 0 {
		t.Fatal("C did not leave message body for CTO")
	}
}

func TestCTOKeyHintsFitWithoutCuttingWords(t *testing.T) {
	m := keys(newModel(80, 24), "C")
	for _, width := range []int{40, 60, 80, 120} {
		line := ansi.Strip(m.helpLine(width))
		if ansi.StringWidth(line) > width || strings.Contains(line, "…") {
			t.Fatalf("%d: %q", width, line)
		}
	}
}

func TestCTOFrameOnEveryTab(t *testing.T) {
	for _, key := range []string{"1", "2", "3", "M"} {
		m := keys(newModel(80, 24), key)
		if !strings.Contains(m.View(), "write to the CTO") {
			t.Errorf("%s missing CTO", key)
		}
		m = keys(m, "tab")
		if m.focus != focusCTO {
			t.Errorf("%s did not focus CTO", key)
		}
		m = keys(m, "tab")
		want := focusSessions
		if key == "2" {
			want = focusFeed
		}
		if m.focus != want {
			t.Errorf("%s returned focus %v, want %v", key, m.focus, want)
		}
	}
}
