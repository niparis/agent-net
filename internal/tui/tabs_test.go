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

	"github.com/niparis/agent-net/internal/quotaaxi"
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
		m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-terra", "gpt-6.1-sol", "unknown"}}, {Name: "claude", Models: []string{"claude-opus-5-5"}}}
		m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"}, {Name: "sol", Harness: "codex", Model: "gpt-6.1-sol", Effort: "medium"}, {Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"}}
		m.snap.Nodes[0].Tracks = []protocol.TrackRecord{{Harness: "claude", Model: "claude-opus-5-5:high", Runs: 12, Complete: 10, Failed: 2}}
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
		m.snap.Nodes[0].Harnesses = append(m.snap.Nodes[0].Harnesses, protocol.HarnessInfo{Name: "opencode", Models: []string{"openrouter/anthropic/claude-opus-5.5:high"}})
		m.snap.WorkerProfiles = append(m.snap.WorkerProfiles, protocol.WorkerProfile{Name: "router", Harness: "opencode", Model: "openrouter/anthropic/claude-opus-5.5", Effort: "high"})
		m.snap.Nodes[0].Harnesses[len(m.snap.Nodes[0].Harnesses)-1].Models = []string{"openrouter/anthropic/claude-opus-5.5"}
		golden(t, fmt.Sprintf("quotas_excluded_%dx%d", size[0], size[1]), m.View())
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

func TestQuotaEmptyStateReportsMissingNode(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.usage.raw.Local.QuotaNote = quotaaxi.NeedNodeMessage
	v := ansi.Strip(m.View())
	if !strings.Contains(v, quotaaxi.NeedNodeMessage) {
		t.Fatalf("the Node line is missing:\n%s", v)
	}
	if strings.Contains(v, "not installed on any machine") {
		t.Fatalf("the old empty line is still shown:\n%s", v)
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
		for i, it := range m.feed.items {
			if it.isMessage() {
				it.FromCTO = true
				it.Snippet = "Please check the deployment."
				m.feedCur = i
				break
			}
		}
		golden(t, fmt.Sprintf("messages_cto_%dx%d", size[0], size[1]), m.View())
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

func TestQuotaModelsSection(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-terra", "unknown-model"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"}, {Name: "unscored", Harness: "codex", Model: "unknown-model"}}
	v := ansi.Strip(m.View())
	for _, want := range []string{"Workers · 2 registered", "gpt-6.1-terra:high", "42.3/high", "USED", "scores: Artificial Analysis", "unscored"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in:\n%s", want, v)
		}
	}
}

func TestQuotaModelPicksDemoteRecentFailure(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"fake/a", "fake/b"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "a", Harness: "codex", Model: "fake/a"}, {Name: "b", Harness: "codex", Model: "fake/b"}}
	m.snap.Nodes[0].RecentModelFailures = map[string]map[string]bool{"codex": {"fake/a": true}}
	m.snap.WorkerProviders = []string{"codex"}
	m.snap.Scores = []protocol.ModelScore{{Match: "fake/a", Score: 51, Source: "owner"}, {Match: "fake/b", Score: 51, Source: "owner"}}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "b          codex    fake/b                 51          E E E E G  0 runs 0 done 0 failed  ◀ T,S,N,H,V") {
		t.Fatalf("choice ignored failed run:\n%s", v)
	}
}

func TestQuotaShowsExcludedRegisteredWorker(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "opencode", Models: []string{"openrouter/anthropic/claude-opus-5.5"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "router", Harness: "opencode", Model: "openrouter/anthropic/claude-opus-5.5", Effort: "high"}}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "router") || !strings.Contains(v, "not a worker provider") {
		t.Fatalf("profile missing: %s", v)
	}
}

func TestQuotaModelPicksPreferRecentSuccess(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"fake/a", "fake/b"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "a", Harness: "codex", Model: "fake/a"}, {Name: "b", Harness: "codex", Model: "fake/b"}}
	m.snap.Nodes[0].RecentModelSuccesses = map[string]map[string]bool{"codex": {"fake/b": true}}
	m.snap.Scores = []protocol.ModelScore{{Match: "fake/a", Score: 51, Source: "owner"}, {Match: "fake/b", Score: 51, Source: "owner"}}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "b          codex    fake/b                 51          E E E E G  0 runs 0 done 0 failed  ◀ T,S,N,H,V") {
		t.Fatalf("choice ignored success:\n%s", v)
	}
}

func TestQuotaShowsOnlyWorkerProfiles(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-terra", "gpt-6.1-sol"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "my worker", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"}}
	v := ansi.Strip(m.View())
	for _, want := range []string{"my worker", "gpt-6.1-terra:high", "1 registered"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
		}
	}
	if strings.Contains(v, "gpt-6.1-sol") {
		t.Fatalf("unregistered model listed: %s", v)
	}
}

func TestMessagesHintShowsCTOFocus(t *testing.T) {
	m := keys(newModel(120, 34), "2")
	if !strings.Contains(ansi.Strip(m.helpLine(120)), "tab CTO") {
		t.Fatal("Messages help omits CTO focus")
	}
}

func TestSubscriptionPanelAndUsageRoles(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Add(-40 * time.Second).Unix(), Providers: []protocol.QuotaProvider{{Provider: "test", Windows: []protocol.QuotaWindow{{ID: "low", PercentUsed: 30, Pace: protocol.QuotaPaceBehind}, {ID: "mid", PercentUsed: 70, Pace: protocol.QuotaPaceAhead}, {ID: "high", PercentUsed: 90}}}}}
	v := strings.Join(m.quotasView(120, 40), "\n")
	for _, want := range []string{"Subscriptions · read 40s ago", progressBar(.3, 10, roleGood), progressBar(.7, 10, roleWarning), progressBar(.9, 10, roleError)} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestWorkerPanelPicksAndTrackRecord(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"fake/a"}}}
	m.snap.Nodes[0].Tracks = []protocol.TrackRecord{{Harness: "codex", Model: "fake/a", Runs: 12, Complete: 10, Failed: 2}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "a", Harness: "codex", Model: "fake/a"}}
	m.snap.WorkerProviders = []string{"codex"}
	m.snap.Scores = []protocol.ModelScore{{Match: "fake/a", Score: 51, Source: "artificialanalysis"}}
	v := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	for _, s := range []string{"Workers · 1 registered · scores: Artificial Analysis", "◀", "12 runs", "10 done", "2 failed"} {
		if !strings.Contains(v, s) {
			t.Errorf("missing %q in %s", s, v)
		}
	}
	if strings.Contains(v, "now:") || strings.Contains(v, "E*") {
		t.Fatal(v)
	}
}

func TestWorkersNowIsInsidePanel(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	v := ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "workers now:") {
			if !strings.HasPrefix(line, "│ ") || !strings.HasSuffix(line, " │") {
				t.Fatalf("footer outside panel: %q", line)
			}
			return
		}
	}
	t.Fatal("footer missing")
}
