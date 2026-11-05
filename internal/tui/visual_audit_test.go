package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func TestVisualAuditFixes(t *testing.T) {
	t.Run("Item1_QuotasProtectConfirmNamesBoth", func(t *testing.T) {
		m := newModel(80, 24)
		m.tab = 2
		m.snap.Nodes = []protocol.NodeView{{
			NodeID: "node1", Hostname: "Mac",
			Harnesses: []protocol.HarnessInfo{{Name: "claude", Models: []string{"claude-sonnet-4"}}},
		}}
		m.usage.raw.Local.Quotas = &protocol.Quotas{
			GeneratedAt: t0.Unix(),
			Providers: []protocol.QuotaProvider{{
				Provider: "claude", Plan: "max", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 30}},
			}},
		}
		m = keys(m, "p")
		if m.mode != modeQuotaConfirm {
			t.Fatalf("expected modeQuotaConfirm, got %v", m.mode)
		}
		help := m.helpLine(80)
		if !strings.Contains(help, "Protect claude (Mac) (anthropic)?") {
			t.Errorf("expected confirmation to name both display name and provider, got: %q", help)
		}
	})

	t.Run("Item2_ToolsOwnerPickerVisibleCountsAgree", func(t *testing.T) {
		m, _ := newToolsModel(80, 24)
		m = openTools(m)
		m = toolFilter(m, "design-taste-frontend")
		m = pressKey(m, "enter")
		m = toolChoose(t, m, "Turn on for...")
		view := m.testView()
		if strings.Contains(view, "15 of 15 targets shown") && strings.Contains(view, "and 8 more") {
			t.Errorf("picker counts contradict each other in view:\n%s", view)
		}
		if !strings.Contains(view, "1-7 of 15 targets shown") {
			t.Errorf("expected '1-7 of 15 targets shown', got view:\n%s", view)
		}
	})

	t.Run("Item3_ToolsCompareIdentifiesByHarnessAndPath", func(t *testing.T) {
		m, _ := toolsAtScreen(80, 24)
		newer := toolsdiff.Copy{Name: "demo", Item: "new", NodeID: toolNodeR, Machine: "research", Location: "/home/test/.claude/skills/demo", Hash: "new", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "new"}}}}
		older := toolsdiff.Copy{Name: "demo", Item: "old", NodeID: toolNodeR, Machine: "research", Location: "/home/test/.pi/agent/skills/demo", Hash: "old", Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Fingerprint: "old"}}}}
		m.tl.cat.Diffs = []toolsdiff.Difference{{Key: "two-copies:demo:research", Kind: toolsdiff.KindTwoCopies, Severity: toolsdiff.SevErr, Name: "demo", Title: "demo has 2 different copies on research", Fix: "compare, then keep one", Action: toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: "demo", Copies: []toolsdiff.Copy{newer, older}}}}
		m.tl.row = 0
		m = enterToolsPlan(m)
		view := ansi.Strip(m.testView())
		if strings.Contains(view, "a  research's copy") && strings.Contains(view, "b  research's copy") {
			t.Errorf("both copies labelled 'research's copy':\n%s", view)
		}
		if !strings.Contains(view, "claude's copy") || !strings.Contains(view, "pi's copy") {
			t.Errorf("expected harness labels in view:\n%s", view)
		}
		if strings.Count(view, "path") != 1 || !strings.Contains(view, ".claude/skills/demo") {
			t.Errorf("expected one path row identifying the copy:\n%s", view)
		}
	})

	t.Run("Item4_GraphCardRefusalCutoffWithEllipsis", func(t *testing.T) {
		m := openMap(t, mapModel(80, 24, richGraph()))
		m.mp.selected = "tenebrae"
		lines := m.mapFocusView(80, 19)
		plain := ansi.Strip(strings.Join(lines, "\n"))
		if strings.Contains(plain, "one-off client\nRECENT") {
			t.Errorf("refusal cutoff has no ellipsis or scroll hint:\n%s", plain)
		}
		if !strings.Contains(plain, "... (ctrl+d)") {
			t.Errorf("expected '... (ctrl+d)' scroll hint at cutoff:\n%s", plain)
		}
	})

	t.Run("Item5_ComposeEditorMultipleLines", func(t *testing.T) {
		m, _ := workerModel(80, 24)
		compose, _ := press(selectID(m, workerID), tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})
		typed := typeText(compose, "also run the tests")
		view := ansi.Strip(typed.testView())
		if !strings.Contains(view, "Message to tenebrae (Mac)") {
			t.Errorf("expected compose panel in body, got view:\n%s", view)
		}
	})

	t.Run("Item6_CTOErrorUsesAvailableWidth", func(t *testing.T) {
		src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
		m = refresh(m, src.fakeSource)
		m.src = src
		m = send(m, m.ctoPollCmd()())
		m = keys(m, "C")
		m.cto.err = "Provider request failed because the account has insufficient credits. Add credits or choose another model before retrying the request. The request was not completed."
		view := ansi.Strip(m.testView())
		if strings.Contains(view, "insufficient…") {
			t.Errorf("error was truncated after insufficient with room remaining:\n%s", view)
		}
		if !strings.Contains(view, "insufficient credit") {
			t.Errorf("expected line to use available width up to credits, got:\n%s", view)
		}
	})

	t.Run("Item7_SessionDetailValuesNotTruncatedAt80", func(t *testing.T) {
		m := keys(newModel(80, 24), "d")
		view := ansi.Strip(m.testView())
		if strings.Contains(view, "remote   https://git.example.com/team/…") {
			t.Errorf("remote URL is truncated at 80 cols:\n%s", view)
		}
		if strings.Contains(view, "source   seen in a session and registe…") {
			t.Errorf("source is truncated at 80 cols:\n%s", view)
		}
	})

	t.Run("Item8_QuotasHeadingsExplained", func(t *testing.T) {
		m := newModel(80, 24)
		m.tab = 2
		view := ansi.Strip(strings.Join(m.quotaModelsView(80), "\n"))
		if !strings.Contains(view, "T:trivial") || !strings.Contains(view, "S:simple") {
			t.Errorf("expected difficulty headings explained in legend:\n%s", view)
		}
	})

	t.Run("Item9_MessagePreviewsAt120", func(t *testing.T) {
		m := newModel(120, 34)
		m.tab = messagesTab
		m.feed.items = []*feedItem{
			{MsgID: "m_1", From: "tenebrae-53", To: "strategies", Snippet: "The deployment plan looks good."},
			{MsgID: "m_2", From: "strategies", To: "tenebrae-53", Snippet: "I checked the rollback steps too."},
		}
		r := messageConversation{
			at:    t0.Add(-8 * time.Minute),
			items: []int{0, 1},
		}
		line := ansi.Strip(m.messageConversationLine(r, 58, false))
		if !strings.Contains(line, "8m") || !strings.Contains(line, "2 msgs") {
			t.Errorf("expected abbreviated timestamp/count in 58-col list, got: %q", line)
		}
		if strings.Contains(line, "The deployme…") {
			t.Errorf("preview was overly truncated in 58-col list: %q", line)
		}
	})

	t.Run("Item10_MachineHarnessesIndicatorAt80", func(t *testing.T) {
		m := newModel(80, 24)
		m.snap.Nodes = []protocol.NodeView{
			{NodeID: "node1", Hostname: "Mac", Harnesses: []protocol.HarnessInfo{{Name: "claude"}, {Name: "opencode"}}},
			{NodeID: "node2", Hostname: "build-vm", Harnesses: []protocol.HarnessInfo{{Name: "pi"}}},
		}
		view := ansi.Strip(m.testView())
		if !strings.Contains(view, "no claude") {
			t.Errorf("expected missing harness indicator at 80 cols, got view:\n%s", view)
		}
	})

	t.Run("Item11_ConnectedAppsAddressesSeparatedAt80", func(t *testing.T) {
		m := newModel(80, 24)
		m.tab = settingsTab
		m.mode = modeApps
		m.apps.public.On = true
		m.apps.public.URL = "https://host.example.ts.net:8443"
		view := ansi.Strip(m.testView())
		if strings.Contains(view, "https://host.exampl…") {
			t.Errorf("addresses are truncated on one line:\n%s", view)
		}
		if !strings.Contains(view, "phone  https://host.example.ts.net:8443/") || !strings.Contains(view, "bots   https://host.example.ts.net:8443/mcp") {
			t.Errorf("expected labelled addresses on separate lines:\n%s", view)
		}
	})

	t.Run("Item12_SettingsMachinesMoreBelow", func(t *testing.T) {
		m := newModel(80, 24)
		m.tab = settingsTab
		m.apps.public.On = true
		m.apps.public.Port = 8443
		m.apps.public.URL = "https://example.test:8443"
		m.apps.public.OtherRoutes = json.RawMessage(`[10000]`)
		m.apps.apps = []connectedApp{{ID: "a1", Name: "phone", Kind: "owner_app", ClientName: "Safari"}, {ID: "a2", Name: "helper", Kind: "bot", ClientName: "Cursor"}}
		view := ansi.Strip(m.testView())
		if !strings.Contains(view, "Machines (more below)") && !strings.Contains(view, "more below") {
			t.Errorf("expected cue that more content follows Machines section:\n%s", view)
		}
	})

	t.Run("Item13_SingularSubscription", func(t *testing.T) {
		m := newModel(80, 24)
		m.tab = 2
		m.usage.raw.Local.Quotas = &protocol.Quotas{
			Providers: []protocol.QuotaProvider{
				{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week"}}},
				{Provider: "copilot", Windows: []protocol.QuotaWindow{{ID: "week"}}},
			},
		}
		view := ansi.Strip(strings.Join(m.quotasView(80, 24), "\n"))
		if strings.Contains(view, "1 other subscriptions") {
			t.Errorf("uses plural for 1 subscription:\n%s", view)
		}
		if !strings.Contains(view, "1 other subscription") {
			t.Errorf("expected singular '1 other subscription':\n%s", view)
		}
	})

	t.Run("Item14_ToolsHintLineNoWrapAt80", func(t *testing.T) {
		m, _ := newToolsModel(80, 24)
		m = openTools(m)
		lines := m.toolsKeyLines(80)
		if len(lines) > 1 {
			t.Errorf("tools hint line wraps at 80 cols: %v", lines)
		}
	})

	t.Run("Item15_ToolsCombinedVersionWarning", func(t *testing.T) {
		m, _ := newToolsModel(80, 24)
		m = openTools(m)
		view := ansi.Strip(m.testView())
		if strings.Contains(view, "on: v1 on Nicolas-MBP, v3 on research\n│ ! v1 on Nicolas-MBP, v3 on research") {
			t.Errorf("repeated version mismatch lines:\n%s", view)
		}
		if !strings.Contains(view, "! on: v1 on Nicolas-MBP, v3 on research") {
			t.Errorf("expected combined '! on: ...' line:\n%s", view)
		}
	})

	t.Run("Item16_InboxEmptyStateNoSelectHint", func(t *testing.T) {
		m := newModel(80, 24)
		m.tab = inboxTab
		help := m.helpLine(80)
		if strings.Contains(help, "select") {
			t.Errorf("inbox empty state should not show select hint, got: %q", help)
		}
	})

	t.Run("Item17_HelpNoDuplicateHeading", func(t *testing.T) {
		m := newModel(80, 24)
		m.help = true
		view := ansi.Strip(strings.Join(m.helpView(80, 24), "\n"))
		lines := strings.Split(view, "\n")
		if len(lines) >= 3 && strings.Contains(lines[0], "Sessions") && strings.TrimSpace(lines[2]) == "Sessions" {
			t.Errorf("duplicate Sessions heading in help view:\n%s", view)
		}
	})

	t.Run("Item18_WorkersLogTitlesMoreSpace", func(t *testing.T) {
		r := protocol.WorkerLogEntry{
			StartedAt:  1700000000,
			Title:      "Review worker history and difficulty scores",
			Model:      "test-model:high",
			Status:     "running",
			Difficulty: "normal",
			Caller:     "Agent One",
		}
		loc := time.UTC
		line := ansi.Strip(workerLogLine(r, 76, false, loc))
		if !strings.Contains(line, "Review worker history and diff") {
			t.Errorf("worker log title truncated too short at 80 cols: %q", line)
		}
	})

	t.Run("Item19_QuotasLiveLabelsNotTruncated", func(t *testing.T) {
		m := newModel(80, 24)
		m.tab = 2
		m.usage.raw.Local.Quotas = &protocol.Quotas{
			Providers: []protocol.QuotaProvider{
				{Provider: "claude", Plan: "prolific", Windows: []protocol.QuotaWindow{{ID: "Fable window", PercentUsed: 95}}},
			},
		}
		view := ansi.Strip(strings.Join(m.quotasView(80, 24), "\n"))
		if strings.Contains(view, "Fable w…") {
			t.Errorf("window label truncated to Fable w…:\n%s", view)
		}
		if !strings.Contains(view, "Fable window") {
			t.Errorf("expected full 'Fable window' label:\n%s", view)
		}
	})

	t.Run("Item20_MessagesSpaceAllocatedDynamically", func(t *testing.T) {
		m := newModel(80, 24)
		m.tab = messagesTab
		view := ansi.Strip(strings.Join(m.messagesView(80, 19), "\n"))
		// In 80x24, the Conversation panel should not contain blank lines inside the box
		lines := strings.Split(view, "\n")
		insideConv := false
		blankInside := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "╭─Conversation") {
				insideConv = true
				continue
			}
			if insideConv && strings.HasPrefix(l, "╰─") {
				insideConv = false
				break
			}
			if insideConv && strings.Trim(strings.TrimPrefix(strings.TrimSuffix(l, "│"), "│"), " ") == "" {
				blankInside++
			}
		}
		if blankInside > 0 {
			t.Errorf("Conversation panel has %d blank lines inside:\n%s", blankInside, view)
		}
	})

	t.Run("Item21_TitleBarAt80NoBareNumber", func(t *testing.T) {
		m := newModel(80, 24)
		line := ansi.Strip(m.titleLine(80))
		if strings.HasSuffix(line, "│12") || strings.Contains(line, "Delivery│12") {
			t.Errorf("title bar ends in bare unlabelled number: %q", line)
		}
	})
}
