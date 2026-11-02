package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

type onboardingGateway struct {
	*fakeSource
	progress protocol.Onboarding
}

func (f *onboardingGateway) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	if f.progress.Steps == nil {
		f.progress.Steps = map[string]string{}
	}
	switch method {
	case "onboarding_get", protocol.OpProtectedProvidersSet:
	case "onboarding_set":
		u := params.(protocol.OnboardingUpdate)
		if u.Disabled != nil {
			f.progress.Disabled = *u.Disabled
		} else {
			f.progress.Steps[u.Step] = u.State
		}
	case "onboarding_restart":
		f.progress = protocol.Onboarding{Steps: map[string]string{}}
	default:
		return nil, fmt.Errorf("unexpected %s", method)
	}
	return json.Marshal(f.progress)
}
func onboardingModel() (Model, *onboardingGateway) {
	src := &onboardingGateway{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.snap = src.snap
	m.onboarding = protocol.Onboarding{Steps: map[string]string{}}
	m.onboardingLoaded = true
	m.width, m.height = 120, 40
	return m, src
}
func onboardingRun(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = onboardingRun(m, c)
		}
		return m
	}
	next, follow := m.Update(msg)
	if follow != nil {
		return onboardingRun(next.(Model), follow)
	}
	return next.(Model)
}
func TestOnboardingTriggersAdvance(t *testing.T) {
	steps := []string{"see", "see-machine", "talk", "cto", "map", "work-worker", "work-protect", "tools"}
	for i, step := range steps {
		t.Run(step, func(t *testing.T) {
			m, src := onboardingModel()
			for _, previous := range steps[:i] {
				m.onboarding.Steps[previous] = "done"
				if src.progress.Steps == nil {
					src.progress.Steps = map[string]string{}
				}
				src.progress.Steps[previous] = "done"
			}
			m.onboardingToolsKnown, m.onboardingToolsCount = true, 1
			m.usage.have = true
			m.usage.raw = Usage{NodeID: "n_mac", Local: &protocol.UsageReport{Quotas: &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}}}
			if step == "see-machine" {
				m.snap.Nodes = m.snap.Nodes[:1]
			}
			if got := m.onboardingStep().id; got != step {
				t.Fatalf("want step %s, got %s", step, got)
			}
			var msg tea.Msg
			switch step {
			case "see":
				msg = enterDoneMsg{name: "tenebrae"}
			case "see-machine":
				snap := m.snap
				snap.Nodes = src.snap.Nodes
				msg = fetchedMsg{snap: snap}
			case "talk":
				msg = m.sendCmd("s_peer", "peer", "hello")()
			case "cto":
				m.onboardingCTOLoaded = true
				msg = ctoPollMsg{generation: m.cto.generation, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"user","text":"hello"}`)}}}
			case "map":
				snap := m.snap
				snap.Agents = append([]protocol.AgentView(nil), snap.Agents...)
				snap.Agents[0].Charter.Purpose = "build"
				msg = fetchedMsg{snap: snap}
			case "work-worker":
				msg = fetchedMsg{snap: m.snap, events: []protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "worker.started"}}}}
			case "work-protect":
				msg = actionMsg{onboarding: "work-protect"}
			case "tools":
				msg = toolsAcceptMsg{accepted: true}
			}
			next, cmd := m.Update(msg)
			m = onboardingRun(next.(Model), cmd)
			if src.progress.Steps[step] != "done" || m.onboarding.Steps[step] != "done" {
				t.Fatalf("%s not saved: %+v", step, src.progress)
			}
		})
	}
}

func TestOnboardingFailedActionsDoNotAdvance(t *testing.T) {
	m, src := onboardingModel()
	next, cmd := m.Update(enterDoneMsg{name: "session", err: fmt.Errorf("failed")})
	m = onboardingRun(next.(Model), cmd)
	if len(src.progress.Steps) != 0 {
		t.Fatal("failed entry advanced")
	}
}

func TestOnboardingSkipAndRestart(t *testing.T) {
	m, src := onboardingModel()
	if got := m.onboardingStep(); got.id != "see" {
		t.Fatalf("first step: %+v", got)
	}
	m.focus = focusSessions
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m = onboardingRun(next.(Model), cmd)
	if len(src.progress.Steps) != 0 {
		t.Fatal("unfocused escape skipped")
	}
	m.focus = focusCTO
	next, cmd = m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m = onboardingRun(next.(Model), cmd)
	if src.progress.Steps["see"] != "skipped" {
		t.Fatalf("focused skip: %+v", src.progress)
	}
	fresh := New(src)
	fresh.snap = src.snap
	fresh = onboardingRun(fresh, fresh.fetchCmd())
	if fresh.onboarding.Steps["see"] != "skipped" {
		t.Fatal("step did not survive restart")
	}
	m.focus = focusSessions
	m.tab = settingsTab
	for i, row := range m.settingsItems() {
		if row.kind == "tips" {
			m.settingsRow = i
		}
	}
	next, cmd = m.key(tea.KeyMsg{Type: tea.KeySpace})
	m = onboardingRun(next.(Model), cmd)
	if !m.onboarding.Disabled || strings.Contains(strings.Join(m.ctoView(120, 6), "\n"), "1/7") {
		t.Fatal("tips not hidden")
	}
	m.settingsRow++
	next, cmd = m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m = onboardingRun(next.(Model), cmd)
	if m.onboarding.Disabled || m.onboardingStep().id != "see" {
		t.Fatalf("restart: %+v", m.onboarding)
	}
	// A new model retrieves the gateway's persisted progress.
	m.settingsRow--
	next, cmd = m.key(tea.KeyMsg{Type: tea.KeySpace})
	m = onboardingRun(next.(Model), cmd)
	fresh = New(src)
	fresh.snap = src.snap
	fresh = onboardingRun(fresh, fresh.fetchCmd())
	if !fresh.onboarding.Disabled {
		t.Fatal("preference did not survive restart")
	}
	next, cmd = m.key(tea.KeyMsg{Type: tea.KeySpace})
	m = onboardingRun(next.(Model), cmd)
	if m.onboarding.Disabled || !strings.Contains(strings.Join(m.ctoView(120, 6), "\n"), "1/7") {
		t.Fatal("tips did not turn on")
	}
}
func TestOnboardingGoldens(t *testing.T) {
	for _, w := range []int{80, 120} {
		for _, focused := range []bool{false, true} {
			m, _ := onboardingModel()
			m.width = w
			m.haveData = true
			if w == 80 {
				m.height = 24
			}
			if focused {
				m.focus = focusCTO
			}
			golden(t, fmt.Sprintf("onboarding_%d_focused_%t", w, focused), m.View())
		}
	}
}

func TestOnboardingHelpGoldens(t *testing.T) {
	for _, width := range []int{80, 120} {
		m, _ := onboardingModel()
		m.width = width
		if width == 80 {
			m.height = 24
		}
		m.help = true
		for i, line := range m.helpContent(width) {
			if strings.Contains(line, "desktop notifications") {
				m.helpScroll = i - 1
				break
			}
		}
		golden(t, fmt.Sprintf("onboarding_help_%d", width), m.View())
	}
}

func TestOnboardingDetectsNewCTOMessageWhenHistoryWindowIsFull(t *testing.T) {
	m, src := onboardingModel()
	for _, step := range []string{"see", "see-machine", "talk"} {
		m.onboarding.Steps[step] = "done"
	}
	old := ctoPollMsg{generation: m.cto.generation, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"user","id":"old"}`)}}}
	next, _ := m.Update(old)
	m = next.(Model)
	newer := ctoPollMsg{generation: m.cto.generation, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"user","id":"new"}`)}}}
	next, cmd := m.Update(newer)
	m = onboardingRun(next.(Model), cmd)
	if src.progress.Steps["cto"] != "done" {
		t.Fatal("new user message in a full history window did not advance")
	}
}

func TestOnboardingRemoteRestartWinsOverOldVisibleStep(t *testing.T) {
	m, src := onboardingModel()
	for _, step := range []string{"see", "see-machine", "talk", "cto", "map"} {
		m.onboarding.Steps[step] = "done"
	}
	if got := m.onboardingStep().id; got != "work-worker" {
		t.Fatalf("step %s", got)
	}
	msg := fetchedMsg{snap: m.snap, onboardingAsked: true, onboarding: protocol.Onboarding{Steps: map[string]string{}}, events: []protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "worker.started"}}}}
	next, cmd := m.Update(msg)
	m = onboardingRun(next.(Model), cmd)
	if len(src.progress.Steps) != 0 || m.onboardingStep().id != "see" {
		t.Fatalf("remote reset lost: %+v", src.progress)
	}
}

func TestOnboardingProtectionAction(t *testing.T) {
	m, src := onboardingModel()
	for _, step := range []string{"see", "see-machine", "talk", "cto", "map", "work-worker"} {
		m.onboarding.Steps[step] = "done"
	}
	m.usage.have = true
	m.usage.raw = Usage{NodeID: "n_mac", Local: &protocol.UsageReport{Quotas: &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}}}
	if got := m.onboardingStep().id; got != "work-protect" {
		t.Fatalf("step: %s", got)
	}
	m.quotaConfirm, m.mode = "anthropic", modeQuotaConfirm
	next, cmd := m.quotaConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = onboardingRun(next.(Model), cmd)
	if src.progress.Steps["work-protect"] != "done" {
		t.Fatal("protection did not advance")
	}
}

func TestOnboardingSessionEntryRequiresSuccessfulReturn(t *testing.T) {
	m, src := onboardingModel()
	m.selected = src.snap.Agents[0].SessionID
	src.enterPlan = protocol.EnterPlan{Argv: []string{"fake-agent"}}
	_, _, _, done := captureName(&m, "tenebrae-53 (Mac)")
	m, cmd := pressE(m)
	if cmd == nil {
		t.Fatal("entry not requested")
	}
	next, cmd := m.Update(cmd())
	m = next.(Model)
	if cmd == nil || *done == nil {
		t.Fatal("entry plan did not run")
	}
	if len(src.progress.Steps) != 0 {
		t.Fatal("step advanced before return")
	}
	next, cmd = m.Update((*done)(nil))
	m = onboardingRun(next.(Model), cmd)
	if src.progress.Steps["see"] != "done" {
		t.Fatal("session return did not advance")
	}
}

func TestOnboardingActionWhileSettingIsSaving(t *testing.T) {
	m, src := onboardingModel()
	disabled := true
	m, first := m.onboardingWrite("onboarding_set", protocol.OnboardingUpdate{Disabled: &disabled})
	m, second := m.onboardingComplete("see")
	if second != nil {
		t.Fatal("concurrent save should wait for the first")
	}
	m = onboardingRun(m, first)
	if !src.progress.Disabled || src.progress.Steps["see"] != "done" {
		t.Fatalf("lost action while saving: %+v", src.progress)
	}
}
