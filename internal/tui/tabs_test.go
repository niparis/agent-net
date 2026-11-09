package tui

import (
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"fmt"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/quotaaxi"
)

func TestTabSwitching(t *testing.T) {
	m := newModel(80, 24)
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions 2 Graph 3 Quotas 4 Messages 5 Tools 6 Settings") {
		t.Fatal(ansi.Strip(m.titleLine(80)))
	}
	m = keys(m, "3")
	if m.tab != quotasTab {
		t.Fatal("3 did not select quotas")
	}
	m = keys(m, "1")
	if m.tab != sessionsTab {
		t.Fatal("1 did not select sessions")
	}
	m = keys(m, "/", "2")
	if m.tab != sessionsTab || m.filter != "2" {
		t.Fatal("filter intercepted tab key")
	}
}

func TestNumberKeysOpenGraphAndMessages(t *testing.T) {
	m := newModel(80, 24)
	m = keys(m, "2")
	if m.tab != graphTab {
		t.Fatalf("2 opened tab %d, not Graph (%d)", m.tab, graphTab)
	}
	m = keys(m, "4")
	if m.tab != messagesTab {
		t.Fatalf("4 opened tab %d, not Messages (%d)", m.tab, messagesTab)
	}
	if m.focus != focusFeed {
		t.Fatal("4 did not focus the message thread")
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
		m.snap.Nodes[0].QualityPickModels = map[string]string{
			"trivial": "sol medium", "simple": "sol medium", "normal": "terra high", "hard": "terra high", "very_complex": "opus high",
		}
		m.snap.Nodes[0].Tracks = []protocol.TrackRecord{{Harness: "claude", Model: "claude-opus-5-5:high", Runs: 12, Complete: 10, Failed: 2}}
		m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Add(-40 * time.Second).Unix(), Providers: []protocol.QuotaProvider{
			{Provider: "codex", Plan: "pro", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 92, Pace: protocol.QuotaPaceAhead, ResetsAt: t0.Add(2 * time.Hour).Unix()}}},
			{Provider: "cursor", Plan: "business", Windows: []protocol.QuotaWindow{{ID: "month", PercentUsed: 100, Exhausted: true}}},
			{Provider: "claude", Plan: "max", Stale: true, Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 50, Pace: protocol.QuotaPaceBehind}}},
		}}
		m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "other", Hostname: "other", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: t0.Add(-time.Minute).Unix(), Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}}}}})
		view := m.testView()
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
		paceColumn := -1
		for _, line := range lines(ansi.Strip(view)) {
			for _, value := range []string{"on pace", "ahead"} {
				if i := strings.Index(line, value); i >= 0 {
					col := ansi.StringWidth(line[:i])
					if paceColumn < 0 {
						paceColumn = col
					} else if col != paceColumn {
						t.Errorf("pace columns differ: %d and %d in %q", paceColumn, col, line)
					}
				}
			}
		}
		if !strings.Contains(ansi.Strip(view), "other subscription") {
			t.Fatal(view)
		}
		golden(t, fmt.Sprintf("quotas_%dx%d", size[0], size[1]), view)
		m.snap.Nodes[0].Harnesses = append(m.snap.Nodes[0].Harnesses, protocol.HarnessInfo{Name: "opencode", Models: []string{"openrouter/anthropic/claude-opus-5.5:high"}})
		m.snap.WorkerProfiles = append(m.snap.WorkerProfiles, protocol.WorkerProfile{Name: "router", Harness: "opencode", Model: "openrouter/anthropic/claude-opus-5.5", Effort: "high"})
		m.snap.Nodes[0].Harnesses[len(m.snap.Nodes[0].Harnesses)-1].Models = []string{"openrouter/anthropic/claude-opus-5.5"}
		golden(t, fmt.Sprintf("quotas_excluded_%dx%d", size[0], size[1]), m.testView())
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
	if !strings.Contains(ansi.Strip(m.testView()), "Workers ·") {
		t.Fatal("workers panel lost")
	}
}

func TestQuotaViewClosesPanelAtCTOWhenRowsOverflow(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = quotasTab
		for i := 0; i < 30; i++ {
			m.snap.WorkerProfiles = append(m.snap.WorkerProfiles, protocol.WorkerProfile{
				Name: fmt.Sprintf("profile-%02d", i), Harness: "codex", Model: fmt.Sprintf("model-%02d", i),
			})
		}
		view := lines(ansi.Strip(m.testView()))
		for i, line := range view {
			if strings.HasPrefix(line, "╭─CTO") {
				if i == 0 || !strings.HasPrefix(view[i-1], "╰") {
					t.Fatalf("%dx%d: quota panel is not closed above the CTO:\n%s", size[0], size[1], strings.Join(view, "\n"))
				}
				break
			}
		}
		m.quotaScroll = 100
		if !strings.Contains(ansi.Strip(m.testView()), "model-29") {
			t.Errorf("%dx%d: scrolling cannot reach the last profile", size[0], size[1])
		}
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
	if strings.Contains(ansi.Strip(m.testView()), "p29") {
		t.Fatal("last row visible before scroll")
	}
	for i := 0; i < 80; i++ {
		m = keys(m, "down")
	}
	if !strings.Contains(ansi.Strip(m.testView()), "p29") {
		t.Fatal("last row not reachable")
	}
}

func TestQuotaEmptyAndMachineErrors(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	v := ansi.Strip(m.testView())
	if !strings.Contains(v, "No machine reads quotas yet: quota-axi is not installed on any machine (see docs/setup.md, Quota).") || strings.Contains(v, "PROVIDER") {
		t.Fatal(v)
	}
	m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "error-node", Hostname: "build", Usage: protocol.UsageReport{QuotaError: "it failed: exit status 1"}})
	v = ansi.Strip(m.testView())
	if !strings.Contains(v, "build: it failed: exit status 1") || strings.Contains(v, "not installed on any machine") || strings.Contains(v, "PROVIDER") {
		t.Fatal(v)
	}
}

func TestQuotaEmptyStateReportsMissingNode(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.usage.raw.Local.QuotaNote = quotaaxi.NeedNodeMessage
	v := ansi.Strip(m.testView())
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
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions 2 Graph 3 Quotas 4 Messages 5 Tools 6 Settings 7 Desk") {
		t.Fatal(ansi.Strip(m.titleLine(80)))
	}
	m.tab = 2
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions 2 Graph 3 Quotas 4 Messages 5 Tools 6 Settings 7 Desk") {
		t.Fatal(ansi.Strip(m.titleLine(80)))
	}
}

func TestTabsMoveFeedToMessages(t *testing.T) {
	m := newModel(80, 24)
	if !strings.Contains(m.testView(), "1 Sess") {
		t.Fatal("sessions tab missing")
	}
	m = keys(m, "4")
	if !strings.Contains(m.testView(), "4 Messages") || !strings.Contains(m.testView(), "Messages") {
		t.Fatal("messages tab missing")
	}
	m = keys(m, "3")
	if !strings.Contains(m.testView(), "3 Quota") {
		t.Fatal("quotas tab missing")
	}
}

func TestCTOBoxFocusAndEnterOpensCTO(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m := New(src)
	m = keys(m, "C")
	if m.focus != focusCTO {
		t.Fatal("C did not focus CTO")
	}
	m = keys(m, "h", "i")
	if len(src.sent) != 0 {
		t.Fatalf("the box sent a message: %v", src.sent)
	}
	next, cmd := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("enter did not open the CTO")
	}
	if _, ok := cmd().(enterMsg); !ok {
		t.Fatal("enter did not return an enter plan")
	}
	m = send(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	if m.focus != focusSessions {
		t.Fatal("ctrl+] did not return focus")
	}
}

func TestCTOBoxAlwaysVisibleAndFocusReturns(t *testing.T) {
	m := newModel(80, 24)
	if !strings.Contains(m.testView(), "c: open the CTO") {
		t.Fatal("CTO box hint missing")
	}
	m = keys(m, "4", "C")
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
	next, cmd := m.update(tea.KeyPressMsg{Text: "n", Code: []rune("n")[0]})
	m = next.(Model)
	if cmd != nil || !m.cto.confirming || src.fresh != 0 {
		t.Fatal("new did not ask")
	}
	next, cmd = m.update(tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
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
	if !strings.Contains(m.testView(), "Selected session") || !strings.Contains(m.testView(), "CTO") {
		t.Fatal("detail or CTO missing")
	}
	m = keys(m, "d")
	if !strings.Contains(m.testView(), "NAME") {
		t.Fatal("session list did not return")
	}
}

func detailText(m Model) string { return testOutput(strings.Join(m.detailLines(m.width, 40), "\n")) }

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
			m = quietFixtures(m)
			m.src = src
			m = send(m, m.ctoPollCmd()())
			m = keys(m, "C")
			kind := "empty"
			if conversation {
				kind = "conversation"
			}
			v := m.testView()
			for _, line := range lines(v) {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("line exceeds %d: %q", size[0], line)
				}
			}
			golden(t, fmt.Sprintf("sessions_%dx%d_%s_focused", size[0], size[1], kind), v)
		}
		m := keys(newModel(size[0], size[1]), "4")
		for id, body := range map[string]string{
			"m_1": "Can you check the latest deployment plan?\nThe release is planned for tonight.",
			"m_2": "The deployment plan looks good.\nI checked the rollback steps too.",
			"m_3": "Build passed for the release candidate.\nAll required checks are green.",
			"m_4": "Rollback is ready if we need it.\nThe previous version is still available.",
			"m_5": "Which environment should I use?\nI can start as soon as you confirm.",
		} {
			if it := m.feed.byMsg[id]; it != nil {
				it.Body = body
				it.Snippet = strings.SplitN(body, "\n", 2)[0]
				it.Snip = snipDone
			}
		}
		for i, it := range m.feed.items {
			if it.MsgID == "m_1" {
				m.feedCur = i
				break
			}
		}
		v := m.testView()
		plain := ansi.Strip(v)
		if strings.Contains(plain, "loading body") || !strings.Contains(plain, "The deployment plan looks good.") || !strings.Contains(plain, "Can you check the latest deployment plan?") {
			t.Fatalf("the Messages golden fixture should show fetched previews and thread bodies:\n%s", plain)
		}
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
		golden(t, fmt.Sprintf("messages_cto_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestCTOFocusFromMessageBody(t *testing.T) {
	m := newModel(80, 24)
	m = keys(m, "esc", "4")
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
	for _, key := range []string{"1", "2", "3", "4", "5"} {
		m := keys(newModel(80, 24), key)
		hasBox := strings.Contains(m.testView(), "c: open the CTO")
		if key != "2" && !hasBox {
			t.Errorf("%s missing CTO", key)
		}
		if key == "2" && hasBox {
			t.Errorf("Graph shows an empty CTO box")
		}
		m = keys(m, "C")
		if m.focus != focusCTO {
			t.Errorf("%s did not focus CTO", key)
		}
		m = keys(m, "tab")
		if m.focus != focusSessions {
			t.Errorf("%s returned focus %v, want %v", key, m.focus, focusSessions)
		}
	}
}

func TestQuotaModelsSection(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-terra", "unknown-model"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"}, {Name: "unscored", Harness: "codex", Model: "unknown-model"}}
	v := ansi.Strip(m.testView())
	for _, want := range []string{"Workers · 2 registered", "gpt-6.1-terra:high", "42.3/high", "USED", "scores: Artificial Analysis", "unknown-model"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in:\n%s", want, v)
		}
	}
}

func TestQuotaShowsExcludedRegisteredWorker(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "opencode", Models: []string{"openrouter/anthropic/claude-opus-5.5"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "router", Harness: "opencode", Model: "openrouter/anthropic/claude-opus-5.5", Effort: "high"}}
	v := ansi.Strip(m.testView())
	if !strings.Contains(v, "claude-opus-5.5:high") || !strings.Contains(v, "not a worker provider") {
		t.Fatalf("profile missing: %s", v)
	}
}

func TestQuotaShowsOnlyWorkerProfiles(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-terra", "gpt-6.1-sol"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "my worker", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"}}
	v := ansi.Strip(m.testView())
	for _, want := range []string{"gpt-6.1-terra:high", "1 registered"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
		}
	}
	if strings.Contains(v, "gpt-6.1-sol") {
		t.Fatalf("unregistered model listed: %s", v)
	}
}

func TestMessagesHintShowsCTOFocus(t *testing.T) {
	m := keys(newModel(120, 34), "4")
	if !strings.Contains(ansi.Strip(m.helpLine(120)), "c CTO") {
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
	m.snap.Nodes[0].Tracks = []protocol.TrackRecord{{Harness: "codex", Model: "fake/a", Runs: 12, Complete: 10, Failed: 2, Continue: 3, Fix: 1}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "a", Harness: "codex", Model: "fake/a"}}
	m.snap.WorkerProviders = []string{"codex"}
	m.snap.Scores = []protocol.ModelScore{{Match: "fake/a", Score: 51, Source: "artificialanalysis"}}
	v := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	for _, s := range []string{"Workers · 1 registered · 0 running · scores: Artificial Analysis", "12 · 83% done · 2 failed"} {
		if !strings.Contains(v, s) {
			t.Errorf("missing %q in %s", s, v)
		}
	}
	if strings.Contains(v, "workers now:") || strings.Contains(v, "◀") || strings.Contains(v, "E*") {
		t.Fatal(v)
	}
}

func TestWorkersPanelRedesign(t *testing.T) {
	profile := testColorProfile
	setTestColorProfile(termenv.TrueColor)
	defer setTestColorProfile(profile)
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
	m.snap.Nodes[0].QualityPickModels = map[string]string{
		"trivial": "terra high", "simple": "terra high", "normal": "terra high", "hard": "luna xhigh", "very_complex": "terra high",
	}
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{Harness: "codex", Online: true, Worker: &protocol.WorkerInfo{ID: "w1"}}, protocol.AgentView{Harness: "codex", Online: false, Worker: &protocol.WorkerInfo{ID: "w2"}})
	m.snap.Scores = []protocol.ModelScore{{Match: "gpt-6-luna", Effort: "xhigh", Score: 30, Source: "artificialanalysis"}, {Match: "gpt-6.1-terra", Effort: "high", Score: 42.3, Source: "artificialanalysis"}}
	m.snap.WorkerProviders = []string{"codex"}

	got := strings.Join(m.quotaModelsView(120), "\n")
	plain := ansi.Strip(got)
	for _, want := range []string{
		"Workers · 3 registered · 1 running · scores: Artificial Analysis",
		"gpt-6-luna:xhigh",
		"gpt-6.1-terra:high",
		"opus-5.5",
		"3 · 67% done · 1 failed",
		"5 · 100% done",
		"unused",
		"T:trivial S:simple N:normal H:hard V:very complex",
		"T terra codex · S terra codex · N terra codex",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "workers now:") || strings.Contains(plain, "◀") {
		t.Fatalf("old workers decoration remains:\n%s", plain)
	}
	if !strings.Contains(plain, "▸") {
		t.Fatalf("the picker preview is not marked:\n%s", plain)
	}
}

func TestWorkersPanelShowsGatewayPreview(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"openai-codex/gpt-6.1-terra", "openai/gpt-6-luna"}}}
	m.snap.Nodes[0].Harnesses[0].ModelDefaultEfforts = map[string]string{"openai-codex/gpt-6.1-terra": "high", "openai/gpt-6-luna": "xhigh"}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{
		{Name: "terra codex", Harness: "codex", Model: "openai-codex/gpt-6.1-terra", Effort: "high"},
		{Name: "luna codex", Harness: "codex", Model: "openai/gpt-6-luna", Effort: "xhigh"},
	}
	m.snap.Scores = []protocol.ModelScore{{Match: "gpt-6.1-terra", Effort: "high", Score: 42.3}, {Match: "gpt-6-luna", Effort: "xhigh", Score: 30}}
	m.snap.WorkerProviders = []string{"codex"}
	m.snap.Nodes[0].QualityPickModels = map[string]string{
		"trivial": "terra high", "simple": "terra high", "normal": "terra high", "hard": "luna xhigh", "very_complex": "terra high",
	}
	view := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	for _, want := range []string{"gpt-6.1-terra:high", "gpt-6-luna:xhigh", "▸", "T terra codex", "H luna codex"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
	if strings.Contains(view, "share of next 20") || strings.Contains(view, "░") {
		t.Errorf("the simulated heatmap remains:\n%s", view)
	}
}

func TestWorkersPanelKeyAndLinesFitResponsiveWidths(t *testing.T) {
	legend := []string{"T:trivial S:simple N:normal H:hard V:very complex", "picker preview at the middle"}
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
		for _, want := range legend {
			if !strings.Contains(plain, want) {
				t.Errorf("Workers legend at width %d is missing %q:\n%s", width, want, plain)
			}
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
	if !strings.Contains(v, "2 other subscriptions · s to show") || strings.Contains(v, "inactive") {
		t.Fatal(v)
	}
	m = keys(m, "s")
	v = ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if !strings.Contains(v, "inactive") || !strings.Contains(v, "no key") || strings.Contains(v, "credential_unavailable") {
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
	if v := show(); !strings.Contains(v, "RESEARCH") {
		t.Fatal(v)
	}
	m.usage.raw.Gateway.Nodes = []protocol.UsageNode{{NodeID: "other", Hostname: "build", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: t0.Unix(), Providers: []protocol.QuotaProvider{{Provider: "claude"}}}}}}
	if v := show(); !strings.Contains(v, "BUILD") {
		t.Fatal(v)
	}
}

func TestQuotaDisplayDedupesEmptyProvidersAndFitsColumns(t *testing.T) {
	m := newModel(140, 34)
	m.tab = quotasTab
	m.quotaShowUnset = true
	m.usage.raw.Hostname = "research"
	m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Unix(), Providers: []protocol.QuotaProvider{
		{Provider: "opencode-go", Plan: "individual premium", Windows: []protocol.QuotaWindow{{Label: "premium interactions"}}},
		{Provider: "cursor", Error: "cursor_sign_in_required"},
		{Provider: "deepseek", Error: "deepseek_credential_unavailable"},
	}}
	m.usage.raw.Gateway.Nodes = []protocol.UsageNode{{NodeID: "mbp", Hostname: "Nicolas-MBP", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: t0.Add(-time.Second).Unix(), Providers: []protocol.QuotaProvider{
		{Provider: "opencode-go", Plan: "unused", Error: "opencode_go_credential_unavailable"},
		{Provider: "cursor", Plan: "live", Windows: []protocol.QuotaWindow{{ID: "week"}}},
		{Provider: "deepseek", Error: "deepseek_credential_unavailable"},
		{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week", Pace: protocol.QuotaPaceBehind}}},
	}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(140, 34), "\n"))
	if strings.Count(v, "opencode-go") != 1 || !strings.Contains(v, "premium interactions") || !strings.Contains(v, "individual premium") || !strings.Contains(v, "no key") {
		t.Fatalf("provider rows or long columns: %s", v)
	}
	if strings.Count(v, "cursor") != 1 || strings.Count(v, "deepseek") != 1 || !strings.Contains(v, "sign in") || !strings.Contains(v, "no key") || strings.Contains(v, "credential_unavailable") || !strings.Contains(v, "on pace") || strings.Contains(v, "behind") {
		t.Fatalf("error or pace label: %s", v)
	}
	golden(t, "quotas_machine_status_140x34", m.testView())
	narrow := ansi.Strip(strings.Join(m.quotasView(80, 34), "\n"))
	if !strings.Contains(narrow, "…") || strings.Contains(narrow, "premium interactions") {
		t.Fatalf("narrow columns should truncate: %s", narrow)
	}
}

func TestQuotaErrorLabels(t *testing.T) {
	for input, want := range map[string]string{
		"deepseek_credential_unavailable": "no credential found",
		"bl_cli_unavailable":              "CLI not installed",
		"commandcode_sign_in_required":    "sign-in required",
		"Grok sign-in required":           "Grok sign-in required",
		"Antigravity/agy is not running":  "Antigravity/agy is not running",
	} {
		if got := quotaErrorLabel(input); got != want {
			t.Errorf("quotaErrorLabel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestQuotaEmptyTrack(t *testing.T) {
	m := newModel(120, 34)
	m.quotaShowUnset = true
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "test", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if !strings.Contains(v, "──────────") {
		t.Fatal(v)
	}
	if strings.Contains(v, "share of next 20") {
		t.Fatalf("the old heatmap remains: %s", v)
	}
}

func TestQuotaLongWindowNames(t *testing.T) {
	m := newModel(80, 24)
	m.quotaShowUnset = true
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "test", Windows: []protocol.QuotaWindow{{Label: "Fable week"}, {Label: "completions"}, {Label: "premium interactions"}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(80, 40), "\n"))
	for _, name := range []string{"Fable", "complet", "premium"} {
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
		v := m.testView()
		if !strings.Contains(ansi.Strip(v), "19 other subscriptions · s to show") || strings.Contains(ansi.Strip(v), "copilot") {
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
	v := ansi.Strip(m.testView())
	if strings.Contains(v, "workers now:") {
		t.Fatal(v)
	}
}

func TestQuotaHintsShowScrollAndExpansion(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	v := ansi.Strip(m.helpLine(80))
	if !strings.Contains(v, "up/down select") || !strings.Contains(v, "s show") {
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

func TestQuotaShowsLiveUnusedSubscriptionsByDefault(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Harness: "codex", Model: "gpt-6"}}
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "week"}}}, {Provider: "copilot", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if strings.Contains(v, "copilot") || !strings.Contains(v, "1 other subscription · s to show") {
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
	if strings.Contains(v, "cursor") || !strings.Contains(v, "1 other subscription") {
		t.Fatal(v)
	}
	m = keys(m, "s")
	v = ansi.Strip(strings.Join(m.quotasView(120, 30), "\n"))
	if !strings.Contains(v, "cursor") || !strings.Contains(v, "exhausted") || !strings.Contains(v, "live") || strings.Contains(v, "credential_unavailable") {
		t.Fatal(v)
	}
}

func TestWorkerColumnsShowTheModelKeyNotTheProfileName(t *testing.T) {
	m := newModel(120, 34)
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "eighteen-char-name", Harness: "codex", Model: "12345678901234567890", Effort: "high"}}
	v := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	if !strings.Contains(v, "12345678901234567890:high") || strings.Contains(v, "eighteen-char-name") {
		t.Fatal(v)
	}
}

func TestProtectedQuotaProfileMark(t *testing.T) {
	m := Model{}
	m.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "sonnet", Harness: "claude", Model: "sonnet"}}
	view := strings.Join(m.quotaModelsView(120), "\n")
	if !strings.Contains(view, "protected") || !strings.Contains(view, "🔒") {
		t.Fatalf("missing protected note: %s", view)
	}
}

func TestProtectedQuotaGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		m.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "saved for your orchestrator"}
		m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "claude", Models: []string{"claude-sonnet-4"}}}
		m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "sonnet", Harness: "claude", Model: "claude-sonnet-4"}}
		m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Unix(), Providers: []protocol.QuotaProvider{{Provider: "claude", Plan: "max", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 30}}}}}
		view := ansi.Strip(m.testView())
		if !strings.Contains(view, "🔒 claude") {
			t.Fatalf("missing subscription lock:\n%s", view)
		}
		// The profile lock is plain text in the PROT column, not an emoji in the row.
		if !strings.Contains(view, "PROT") {
			t.Fatalf("missing PROT column:\n%s", view)
		}
		profileLocked := false
		for _, line := range lines(view) {
			if strings.Contains(line, "sonnet") && strings.Contains(line, "prot") {
				profileLocked = true
			}
			if strings.Contains(line, "sonnet") && strings.Contains(line, "🔒") {
				t.Fatalf("profile row still has the lock emoji: %s", line)
			}
		}
		if !profileLocked {
			t.Fatalf("missing profile lock in the PROT column:\n%s", view)
		}
		golden(t, fmt.Sprintf("quotas_protected_%dx%d", size[0], size[1]), m.testView())
		m = keys(m, "p")
		golden(t, fmt.Sprintf("quotas_unprotect_confirm_%dx%d", size[0], size[1]), m.testView())
		m = keys(m, "n")
		m.snap.ProtectedProviders = protocol.ProtectedProviders{}
		m = keys(m, "p")
		golden(t, fmt.Sprintf("quotas_protect_confirm_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestQuotaFailedReadingHasNoBar(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		m.quotaShowUnset = true
		m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Unix(), Copy: &protocol.QuotaCopy{Version: "0.1.66", Dir: "/opt/homebrew/bin"}, Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Error: "provider_auth_rejected", NeedsAttention: true}}}
		view := ansi.Strip(m.testView())
		for _, line := range lines(view) {
			if strings.Contains(line, "opencode-go") && !strings.Contains(line, "provider_auth_rejected") {
				if strings.Contains(line, "%") || strings.Contains(line, "█") {
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
	if v := ansi.Strip(m.testView()); !strings.Contains(v, "no provider protected") {
		t.Fatalf("missing empty summary: %s", v)
	}
	m.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "saved for orchestrator"}
	if v := ansi.Strip(m.testView()); !strings.Contains(v, "protected: anthropic · saved for orchestrator") {
		t.Fatalf("missing summary: %s", v)
	}
}

func TestQuotaProtectionConfirm(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	m = keys(m, "s", "p")
	if v := ansi.Strip(m.testView()); !strings.Contains(v, "workers use others first") {
		t.Fatalf("missing confirmation: %s", v)
	}
	m = keys(m, "n")
	if v := ansi.Strip(m.testView()); strings.Contains(v, "workers use others first") {
		t.Fatal("confirmation did not close")
	}
}

func TestQuotaSelectionVisible(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	m.quotaShowUnset = true
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week"}}}, {Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	v := ansi.Strip(m.testView())
	if !strings.Contains(v, "> claude") {
		t.Fatalf("selection missing: %s", v)
	}
	m = keys(m, "down")
	v = ansi.Strip(m.testView())
	if !strings.Contains(v, "> codex") {
		t.Fatalf("selection did not move: %s", v)
	}
}

func TestQuotaCopyAndAttention(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.quotaShowUnset = true
	m.usage.raw.Local.Quotas = &protocol.Quotas{Copy: &protocol.QuotaCopy{Version: "0.1.66", Dir: "/opt/homebrew/bin"}, Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Error: "provider_auth_rejected", NeedsAttention: true}}}
	view := ansi.Strip(m.testView())
	for _, want := range []string{"quota-axi 0.1.66 · yours (/opt/homebrew/bin)", "provider_…"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	m.usage.raw.Local.Quotas.Copy.Private = true
	view = ansi.Strip(m.testView())
	if !strings.Contains(view, "agent-net's copy") {
		t.Fatal(view)
	}
}

func TestQuotaCopyShownBeforeFirstReading(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 2
	m.usage.raw.Local.Quotas = nil
	m.usage.raw.Local.QuotaCopy = &protocol.QuotaCopy{Version: "0.1.66", Dir: "/opt/homebrew/bin"}
	m.usage.raw.Local.QuotaError = "it failed"
	if view := ansi.Strip(m.testView()); !strings.Contains(view, "quota-axi 0.1.66 · yours (/opt/homebrew/bin)") {
		t.Fatal(view)
	}
}

func TestQuotaMachineStatusColumns(t *testing.T) {
	m := newModel(120, 40)
	m.tab = quotasTab
	m.quotaShowUnset = true
	m.usage.raw.Hostname = "alpha"
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "one", Windows: []protocol.QuotaWindow{{ID: "week"}}}, {Provider: "two", Error: "two_credential_unavailable"}, {Provider: "three", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	m.usage.raw.Gateway.Nodes = []protocol.UsageNode{{NodeID: "other", Hostname: "beta", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "one", Error: "one_sign_in_required"}, {Provider: "two", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}}}}
	v := ansi.Strip(strings.Join(m.quotasView(120, 40), "\n"))
	lines := strings.Split(v, "\n")
	for _, want := range []string{"ALPHA", "BETA"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %s in %s", want, v)
		}
	}
	alpha, beta := -1, -1
	for _, line := range lines {
		if strings.Contains(line, "ALPHA") && strings.Contains(line, "BETA") {
			alpha, beta = strings.Index(line, "ALPHA"), strings.Index(line, "BETA")
		}
	}
	if alpha < 0 || beta < 0 {
		t.Fatal("missing machine columns")
	}
	for _, tc := range []struct{ provider, a, b string }{{"one", "live", "sign in"}, {"two", "no key", "live"}, {"three", "live", "-"}} {
		found := false
		for _, line := range lines {
			if strings.Contains(line, tc.provider) && len(line) >= beta+10 {
				found = strings.TrimSpace(line[alpha:alpha+10]) == tc.a && strings.TrimSpace(line[beta:beta+10]) == tc.b
				if found {
					break
				}
			}
		}
		if !found {
			t.Errorf("wrong statuses for %s: %s", tc.provider, v)
		}
	}

	if strings.Contains(v, "(live on") {
		t.Fatal(v)
	}
}

func TestQuotaHarnessProviders(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Nodes = []protocol.NodeView{{Harnesses: []protocol.HarnessInfo{{Name: "opencode", Models: []string{"deepseek/model", "openai/model"}}, {Name: "pi", Models: []string{"github-copilot/model"}}}}}
	relevant := m.quotaUsableProviders()
	for _, name := range []string{"claude", "codex", "opencode-go", "agy", "deepseek", "copilot"} {
		if !relevant[name] {
			t.Errorf("missing %s", name)
		}
	}
	if relevant["cursor"] {
		t.Error("unexpected cursor")
	}
}

func TestTabBarKeepsSeparatorWhenInboxHasItemsAt80(t *testing.T) {
	m := newModel(80, 24)
	m.usage.counts = &protocol.SessionCounts{Online: 12}
	m.asks = []protocol.Ask{
		askFromHeld(protocol.Message{ID: "h1", To: "s_0000000000000001", CreatedAt: t0}),
		askFromHeld(protocol.Message{ID: "h2", To: "s_0000000000000002", CreatedAt: t0}),
	}
	line := ansi.Strip(m.titleLine(80))
	// Seven tabs fit by their full names at 80 columns, count and all.
	for _, want := range []string{"1 Sessions", "2 Graph", "3 Quotas", "4 Messages", "5 Tools", "6 Settings", "7 Desk (2)", "│ 12 online"} {
		if !strings.Contains(line, want) {
			t.Errorf("80 column tab bar is missing %q: %q", want, line)
		}
	}
	if !strings.Contains(line, "│") {
		t.Errorf("80 column tab bar lost its separator: %q", line)
	}
	if ansi.StringWidth(line) > 80 {
		t.Errorf("tab bar is %d columns wide: %q", ansi.StringWidth(line), line)
	}
	if wide := ansi.Strip(m.titleLine(120)); !strings.Contains(wide, "1 Sessions 2 Graph 3 Quotas 4 Messages") {
		t.Errorf("120 column tab bar lost its full names: %q", wide)
	}
}

func TestQuotaSubscriptionsAt80UseAProviderBadge(t *testing.T) {
	m := newModel(80, 24)
	m.tab = quotasTab
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "claude", Plan: "max", Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 50, Pace: protocol.QuotaPaceBehind}}}}}
	m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "other", Hostname: "other", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}}}}})
	view := m.quotasView(80, 40)
	plain := ansi.Strip(strings.Join(view, "\n"))
	header := ""
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "PROVIDER") {
			header = line
			break
		}
	}
	for _, want := range []string{"PROVIDER", "PLAN", "WINDOW", "USAGE", "PACE", "RESETS"} {
		if !strings.Contains(header, want) {
			t.Errorf("80 column quota header is missing %q: %q", want, header)
		}
	}
	for _, gone := range []string{"MAC", "OTHER"} {
		if strings.Contains(header, gone) {
			t.Errorf("80 column quota header still has a %s column: %q", gone, header)
		}
	}
	if !strings.Contains(plain, "claude (Mac)") {
		t.Errorf("the provider has no machine badge:\n%s", plain)
	}
	if !strings.Contains(strings.Join(view, "\n"), quotaBar(.5, 8, roleGood)) {
		t.Errorf("the 80 column bar is not 8 cells wide:\n%s", plain)
	}
	if wide := ansi.Strip(strings.Join(m.quotasView(120, 40), "\n")); strings.Contains(wide, "claude (Mac)") || !strings.Contains(wide, "LEFT") {
		t.Errorf("120 column subscriptions changed:\n%s", wide)
	}
}

func TestFullTabNamesFitWithCompactStatus(t *testing.T) {
	m := newModel(80, 24)
	for _, width := range []int{80, 100} {
		line := ansi.Strip(m.titleLine(width))
		t.Logf("title %d: %s", width, line)
		if !strings.Contains(line, "1 Sessions 2 Graph 3 Quotas 4 Messages 5 Tools 6 Settings 7 Desk") {
			t.Errorf("width %d: %s", width, line)
		}
		if width > 80 && !strings.Contains(line, "12") {
			t.Errorf("width %d lost online count: %s", width, line)
		}
	}
}
