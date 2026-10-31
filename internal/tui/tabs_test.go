package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

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
		stale := false
		m := newModel(size[0], size[1])
		m.tab = 2
		m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-terra", "gpt-6.1-sol", "unknown"}}, {Name: "claude", Models: []string{"claude-opus-5-5"}}, {Name: "pi", Models: []string{"openai/gpt-6.1-terra"}}}
		m.snap.Nodes[1].Harnesses = []protocol.HarnessInfo{{Name: "pi", Models: []string{"openai/gpt-6.1-terra"}}}
		available := true
		m.snap.WorkerProfiles = []protocol.WorkerProfile{
			{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
			{Name: "sol", Harness: "codex", Model: "gpt-6.1-sol", Effort: "medium"},
			{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"},
			{Name: "renamed", Harness: "pi", Model: "openai-codex/gpt-6.1-zed", Available: &stale},
			{Name: "terra pi", Harness: "pi", Model: "openai-codex/gpt-6.1-terra", Available: &available,
				MachineAvailability: []protocol.WorkerProfileMachineAvailability{{Machine: "Mac", Available: true, ResolvedModel: "openai/gpt-6.1-terra"}, {Machine: "research", Closest: []string{"openai/gpt-6.1-terra"}}}},
		}
		m.snap.Scores = append(m.snap.Scores, protocol.ModelScore{Match: "openai-codex/gpt-6.1-zed", Score: 77, Source: "owner"})
		m.snap.Nodes[0].Tracks = []protocol.TrackRecord{{Harness: "claude", Model: "claude-opus-5-5:high", Runs: 12, Complete: 10, Failed: 2}}
		m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Add(-40 * time.Second).Unix(), Providers: []protocol.QuotaProvider{
			{Provider: "codex", Plan: "pro", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 92, Pace: protocol.QuotaPaceAhead, ResetsAt: t0.Add(2 * time.Hour).Unix()}}},
			{Provider: "cursor", Plan: "business", Windows: []protocol.QuotaWindow{{ID: "month", PercentUsed: 100, Exhausted: true}}},
			{Provider: "claude", Plan: "max", Stale: true, Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 50, Pace: protocol.QuotaPaceBehind}}},
		}}
		m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "other", Hostname: "other", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: t0.Add(-time.Minute).Unix(), Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}}}}})
		view := m.View()
		if !strings.Contains(ansi.Strip(view), "model gone - ask the CTO to fix") {
			t.Fatalf("stale profile note missing:\n%s", ansi.Strip(view))
		}
		if size[0] == 120 && !strings.Contains(ansi.Strip(view), "not on research") {
			t.Fatalf("partial profile availability note missing:\n%s", ansi.Strip(view))
		}
		for _, line := range lines(ansi.Strip(view)) {
			if strings.Contains(line, "renamed") && strings.Contains(line, "77") {
				t.Fatalf("stale profile still shows its score: %s", line)
			}
		}
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
		if !strings.Contains(ansi.Strip(view), "not used by your agents") {
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
	for i := 0; i < 80; i++ {
		m = keys(m, "down")
	}
	if !strings.Contains(ansi.Strip(m.View()), "Workers ·") {
		t.Fatal("workers panel lost")
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
	m = keys(m, "s")
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
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions  2 Messages  3 Quotas  4 Map  5 Tools  6 Graph │ ") {
		t.Fatal(ansi.Strip(m.titleLine(80)))
	}
	m.tab = 2
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions  2 Messages  3 Quotas  4 Map  5 Tools  6 Graph │ ") {
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
	if !strings.Contains(v, "b codex: picked for") || strings.Contains(v, "◀") {
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
	if v := ansi.Strip(m.View()); !strings.Contains(v, "b codex: picked for") || strings.Contains(v, "◀") {
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
	m.quotaShowUnset = true
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Add(-40 * time.Second).Unix(), Providers: []protocol.QuotaProvider{{Provider: "test", Windows: []protocol.QuotaWindow{{ID: "low", PercentUsed: 30, Pace: protocol.QuotaPaceBehind}, {ID: "mid", PercentUsed: 70, Pace: protocol.QuotaPaceAhead}, {ID: "high", PercentUsed: 90}}}}}
	v := strings.Join(m.quotasView(120, 40), "\n")
	plain := ansi.Strip(v)
	for _, want := range []string{"read 40s ago", "LEFT", "70%", "30%", "10%", quotaBar(.7, 10, roleGood), quotaBar(.3, 10, roleWarning), quotaBar(.1, 10, roleError)} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(plain, "USAGE") {
		t.Fatalf("quota column still reports usage: %s", plain)
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
	for _, s := range []string{"Workers · 1 registered · 0 running · scores: Artificial Analysis", "12 · 83% done · 2 failed", "picked for"} {
		if !strings.Contains(v, s) {
			t.Errorf("missing %q in %s", s, v)
		}
	}
	if strings.Contains(v, "workers now:") || strings.Contains(v, "◀") || strings.Contains(v, "E*") {
		t.Fatal(v)
	}
}

func TestWorkersPanelRedesign(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"openai/gpt-6-luna", "openai-codex/gpt-6.1-terra", "openrouter/anthropic/claude-opus-5.5"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{
		{Name: "luna codex", Harness: "codex", Model: "openai/gpt-6-luna", Effort: "xhigh"},
		{Name: "terra codex", Harness: "codex", Model: "openai-codex/gpt-6.1-terra", Effort: "high"},
		{Name: "router", Harness: "codex", Model: "openrouter/anthropic/claude-opus-5.5"},
	}
	m.snap.Nodes[0].Tracks = []protocol.TrackRecord{{Harness: "codex", Model: "openai/gpt-6-luna:xhigh", Runs: 3, Complete: 2, Failed: 1}, {Harness: "codex", Model: "openai-codex/gpt-6.1-terra:high", Runs: 5, Complete: 5}}
	m.snap.Nodes[0].Harnesses[0].ModelDefaultEfforts = map[string]string{"openai/gpt-6-luna": "xhigh", "openai-codex/gpt-6.1-terra": "high"}
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{Harness: "codex", Online: true, Worker: &protocol.WorkerInfo{ID: "w1"}}, protocol.AgentView{Harness: "codex", Online: false, Worker: &protocol.WorkerInfo{ID: "w2"}})
	m.snap.Scores = []protocol.ModelScore{{Match: "gpt-6-luna", Effort: "xhigh", Score: 30, Source: "artificialanalysis"}, {Match: "gpt-6.1-terra", Effort: "high", Score: 42.3, Source: "artificialanalysis"}}
	m.snap.WorkerProviders = []string{"codex"}

	got := strings.Join(m.quotaModelsView(120), "\n")
	plain := ansi.Strip(got)
	for _, want := range []string{
		"Workers · 3 registered · 1 running · scores: Artificial Analysis",
		"gpt-6-luna:xhigh",
		"gpt-6.1-terra:high",
		"openrouter/anthropic/cla…",
		"3 · 67% done · 1 failed",
		"5 · 100% done",
		"unused",
		"T trivial S simple N normal H hard V very complex",
		"E strong G ok P weak - too weak",
		"terra codex: picked for",
		"Strong at",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "workers now:") || strings.Contains(plain, "◀") {
		t.Fatalf("old workers decoration remains:\n%s", plain)
	}
	if !strings.Contains(got, "48;2;") {
		t.Fatalf("picked fit cell has no highlight background:\n%q", got)
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "T trivial S simple") && ansi.StringWidth(line) > 120 {
			t.Fatalf("key line exceeds panel width: %q", line)
		}
	}
}

func TestWorkersPanelKeyAndLinesFitResponsiveWidths(t *testing.T) {
	const compactKey = "T/S/N/H/V=tri/sim/nor/hard/v.complex·E/G/P/-=strong/ok/weak/too weak·lit=now"
	const fullKey = "T trivial S simple N normal H hard V very complex · E strong G ok P weak - too weak · lit = picked now"
	for _, width := range []int{80, 100, 120} {
		m := newModel(width, 34)
		m.tab = 2
		view := strings.Join(m.quotaModelsView(width), "\n")
		plain := ansi.Strip(view)
		for _, line := range strings.Split(view, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Errorf("Workers line at width %d is %d columns: %q", width, got, ansi.Strip(line))
			}
		}
		wantKey := fullKey
		if width < 110 {
			wantKey = compactKey
		}
		if !strings.Contains(plain, wantKey) {
			t.Errorf("Workers key at width %d is missing %q:\n%s", width, wantKey, plain)
		}
	}
}

func TestWorkerDisplayModelDropsOnlyUnambiguousProviders(t *testing.T) {
	cases := []struct{ harness, model, want string }{
		{"opencode", "opencode-go/deepseek-v4.1-flash", "deepseek-v4.1-flash"},
		{"codex", "openai-codex/gpt-6.1-terra", "gpt-6.1-terra"},
		{"codex", "openai/gpt-6-luna", "gpt-6-luna"},
		{"opencode", "deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"},
		{"opencode", "openrouter/anthropic/claude-opus-5.5", "openrouter/anthropic/claude-opus-5.5"},
	}
	for _, tc := range cases {
		if got := workerDisplayModel(tc.harness, tc.model); got != tc.want {
			t.Errorf("workerDisplayModel(%q, %q) = %q, want %q", tc.harness, tc.model, got, tc.want)
		}
	}
}

func TestWorkersSummaryIsInTitle(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	v := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	if !strings.Contains(v, "Workers · 0 registered · 0 running") || strings.Contains(v, "workers now:") {
		t.Fatalf("workers summary title missing or old footer remains:\n%s", v)
	}
}

func TestQuotaUnconfiguredFold(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "inactive", Error: "credential_unavailable"}, {Provider: "active", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 5}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if !strings.Contains(v, "1 providers not set up · s to show") || strings.Contains(v, "inactive") {
		t.Fatal(v)
	}
	m = keys(m, "s")
	v = ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if !strings.Contains(v, "inactive") || !strings.Contains(v, "credential_unavailable") {
		t.Fatal(v)
	}
}

func TestQuotaReadingSourceLabels(t *testing.T) {
	m := newModel(120, 34)
	m.quotaShowUnset = true
	m.tab = 2
	m.usage.raw.Hostname = "research"
	m.usage.raw.Gateway.Nodes = nil
	p := protocol.QuotaProvider{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 20}}}
	m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Unix(), Providers: []protocol.QuotaProvider{p}}
	show := func() string { return ansi.Strip(strings.Join(m.quotasView(120, 30), "\n")) }
	if v := show(); !strings.Contains(v, "read on research") || strings.Contains(v, "(read on research)") {
		t.Fatal(v)
	}
	m.usage.raw.Gateway.Nodes = []protocol.UsageNode{{NodeID: "other", Hostname: "build", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: t0.Unix(), Providers: []protocol.QuotaProvider{{Provider: "claude"}}}}}}
	if v := show(); !strings.Contains(v, "(read on research)") {
		t.Fatal(v)
	}
}

func TestQuotaEmptyTrack(t *testing.T) {
	m := newModel(120, 34)
	m.quotaShowUnset = true
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "test", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if !strings.Contains(v, "──────────") || strings.Contains(v, "░") {
		t.Fatal(v)
	}
}

func TestQuotaLongWindowNames(t *testing.T) {
	m := newModel(80, 24)
	m.quotaShowUnset = true
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "test", Windows: []protocol.QuotaWindow{{Label: "Fable week"}, {Label: "completions"}, {Label: "premium interactions"}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(80, 40), "\n"))
	for _, name := range []string{"Fable week", "completions", "premium inter…"} {
		if !strings.Contains(v, name) {
			t.Errorf("missing %q: %s", name, v)
		}
	}
}

func TestQuotaMachineErrorAtPanelFooter(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "active", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "err", Hostname: "laptop", Usage: protocol.UsageReport{QuotaError: "exit status 2"}})
	v := ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if strings.Index(v, "active") > strings.Index(v, "laptop: exit status 2") {
		t.Fatal(v)
	}
}

func TestQuotaLiveFixtureGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		m.usage.raw.Hostname = "research"
		m.usage.raw.Gateway.Nodes = []protocol.UsageNode{{NodeID: "laptop", Hostname: "laptop", Usage: protocol.UsageReport{QuotaError: "exit status 2"}}}
		q := &protocol.Quotas{GeneratedAt: t0.Add(-40 * time.Second).Unix(), Providers: []protocol.QuotaProvider{
			{Provider: "claude", Plan: "max", Windows: []protocol.QuotaWindow{{Label: "session", PercentUsed: 12}, {Label: "week", PercentUsed: 60}, {Label: "Fable week", PercentUsed: 5}}},
			{Provider: "codex", Plan: "prolite", Windows: []protocol.QuotaWindow{{Label: "week", PercentUsed: 92}}},
			{Provider: "copilot", Windows: []protocol.QuotaWindow{{Label: "chat"}, {Label: "completions"}, {Label: "premium interactions", Exhausted: true, PercentUsed: 100}}},
			{Provider: "other", Windows: []protocol.QuotaWindow{{Label: "month", PercentUsed: 15}}},
		}}
		for i := 0; i < 17; i++ {
			q.Providers = append(q.Providers, protocol.QuotaProvider{Provider: fmt.Sprintf("unused-%02d", i), Error: "credential_unavailable"})
		}
		m.usage.raw.Local.Quotas = q
		m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"}}
		m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-terra"}}}
		v := m.View()
		if !strings.Contains(ansi.Strip(v), "17 providers not set up · s to show") {
			t.Fatal(v)
		}
		golden(t, fmt.Sprintf("quotas_live_%dx%d", size[0], size[1]), v)
	}
}

func TestQuotaDoesNotShowOrphanWorkerFooter(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	q := &protocol.Quotas{}
	for i := 0; i < 12; i++ {
		q.Providers = append(q.Providers, protocol.QuotaProvider{Provider: fmt.Sprintf("p%02d", i), Windows: []protocol.QuotaWindow{{ID: "week"}}})
	}
	m.usage.raw.Local.Quotas = q
	v := ansi.Strip(m.View())
	if strings.Contains(v, "workers now:") {
		t.Fatal(v)
	}
}

func TestQuotaHintsShowScrollAndExpansion(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	v := ansi.Strip(m.helpLine(80))
	if !strings.Contains(v, "up/down scroll") || !strings.Contains(v, "s show") {
		t.Fatal(v)
	}
}

func TestQuotaSourcesAgreeAcrossPlans(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	providers := []protocol.QuotaProvider{{Provider: "codex", Plan: "a", Windows: []protocol.QuotaWindow{{ID: "week"}}}, {Provider: "codex", Plan: "b", Windows: []protocol.QuotaWindow{{ID: "week"}}}}
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: providers}
	m.usage.raw.Gateway.Nodes = []protocol.UsageNode{{NodeID: "other", Hostname: "other", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{Providers: providers}}}}
	for _, r := range m.quotaRows() {
		if r.machine != "" {
			t.Fatalf("agreed readings include machine: %+v", r)
		}
	}
}

func TestQuotaFoldsUnusedSubscriptions(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Harness: "codex", Model: "gpt-6"}}
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "week"}}}, {Provider: "copilot", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if strings.Contains(v, "copilot") || !strings.Contains(v, "1 more subscription (not used by your agents) · s to show") {
		t.Fatal(v)
	}
	m = keys(m, "s")
	if v = ansi.Strip(strings.Join(m.quotasView(120, 30), "\n")); !strings.Contains(v, "copilot") {
		t.Fatal(v)
	}
}

func TestQuotaReadingPreventsProviderFromFoldingAsNotSetUp(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{
		{Provider: "cursor", Plan: "Premium", Windows: []protocol.QuotaWindow{{ID: "month", PercentUsed: 100, Exhausted: true}}},
		{Provider: "cursor", Plan: "api", Error: "credential_unavailable"},
	}}
	v := ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if strings.Contains(v, "providers not set up") || !strings.Contains(v, "2 more subscriptions (not used by your agents) · s to show") {
		t.Fatal(v)
	}
	m = keys(m, "s")
	v = ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if !strings.Contains(v, "cursor") || !strings.Contains(v, "exhausted") || !strings.Contains(v, "credential_unavailable") {
		t.Fatal(v)
	}
}

func TestWorkerColumnsKeepNamesAndModels(t *testing.T) {
	m := newModel(120, 34)
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "eighteen-char-name", Harness: "codex", Model: "12345678901234567890", Effort: "high"}}
	v := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	if !strings.Contains(v, "eighteen-char-name") || !strings.Contains(v, "12345678901234567890:high") || strings.Contains(v, " · 12345678901234567890") {
		t.Fatal(v)
	}
}

func TestProtectedQuotaProfileMark(t *testing.T) {
	m := Model{}
	m.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "sonnet", Harness: "claude", Model: "sonnet"}}
	view := strings.Join(m.quotaModelsView(120), "\n")
	if !strings.Contains(view, "protected") || !strings.Contains(view, "saved for your orchestrator (anthropic)") {
		t.Fatalf("missing protected note: %s", view)
	}
}

func TestProtectedQuotaGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		m.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}}
		m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "claude", Models: []string{"claude-sonnet-4"}}}
		m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "sonnet", Harness: "claude", Model: "claude-sonnet-4"}}
		m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Unix(), Providers: []protocol.QuotaProvider{{Provider: "claude", Plan: "max", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 30}}}}}
		view := ansi.Strip(m.View())
		if !strings.Contains(view, "🔒 claude") || !strings.Contains(view, "🔒 sonnet") {
			t.Fatalf("missing subscription or profile lock:\n%s", view)
		}
		golden(t, fmt.Sprintf("quotas_protected_%dx%d", size[0], size[1]), m.View())
	}
}

func TestQuotaFailedReadingHasNoBar(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		m.quotaShowUnset = true
		m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Unix(), Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Error: "the OpenCode Go login the daemon found was rejected"}}}
		view := ansi.Strip(m.View())
		for _, line := range lines(view) {
			if strings.Contains(line, "opencode-go") && !strings.Contains(line, "provider_auth_rejected") {
				if strings.Contains(line, "100%") || strings.ContainsAny(line, "█─") || !strings.Contains(line, "-              -") {
					t.Fatalf("failed reading has a quota: %s", line)
				}
			}
		}
		golden(t, fmt.Sprintf("quotas_failed_%dx%d", size[0], size[1]), view)
	}
}

func TestQuotaProtectedSummary(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	if v := ansi.Strip(m.View()); !strings.Contains(v, "no provider protected") {
		t.Fatalf("missing empty summary: %s", v)
	}
	m.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "saved for orchestrator"}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "protected: anthropic · saved for orchestrator") {
		t.Fatalf("missing summary: %s", v)
	}
}
