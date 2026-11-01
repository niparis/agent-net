package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func keyEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func keyEsc() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }
func keyRune(r string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)}
}

func confirmHintShown(t *testing.T, m Model) {
	t.Helper()
	if v := ansi.Strip(m.View()); !strings.Contains(v, confirmHint) {
		t.Fatalf("missing confirm hint %q:\n%s", confirmHint, v)
	}
}

func TestStopWorkerConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		yes  bool
	}{
		{"enter", keyEnter(), true},
		{"y", keyRune("y"), true},
		{"Y", keyRune("Y"), true},
		{"esc", keyEsc(), false},
		{"n", keyRune("n"), false},
		{"N", keyRune("N"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, src := workerModel(120, 34)
			m = selectID(m, workerID)
			m, _ = press(m, keyRune("x"))
			if m.mode != modeConfirm {
				t.Fatal("stop did not ask")
			}
			confirmHintShown(t, m)
			m, cmd := press(m, tc.key)
			if tc.yes {
				m = finish(t, m, cmd)
				if src.stopped != workerID || m.notice.err {
					t.Fatalf("stop %q notice %+v", src.stopped, m.notice)
				}
				return
			}
			if cmd != nil || m.mode != modeNormal || src.stopped != "" {
				t.Fatalf("cancel: mode %v cmd %v stopped %q", m.mode, cmd != nil, src.stopped)
			}
		})
	}
}

func TestStopOwnerConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		yes  bool
	}{
		{"enter", keyEnter(), true},
		{"y", keyRune("y"), true},
		{"esc", keyEsc(), false},
		{"n", keyRune("n"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, src := workerModel(120, 34)
			m = selectID(m, "s_0000000000000001")
			m, _ = press(m, keyRune("x"))
			if m.mode != modeConfirm || !m.confirmOwner {
				t.Fatal("owner stop did not ask")
			}
			confirmHintShown(t, m)
			m, cmd := press(m, tc.key)
			if tc.yes {
				m = finish(t, m, cmd)
				if src.stopped != "s_0000000000000001" || m.notice.err {
					t.Fatalf("stop %q notice %+v", src.stopped, m.notice)
				}
				return
			}
			if cmd != nil || m.mode != modeNormal || src.stopped != "" {
				t.Fatalf("cancel: mode %v cmd %v stopped %q", m.mode, cmd != nil, src.stopped)
			}
		})
	}
}

func quotaConfirmModel(t *testing.T) (Model, *quotaActionSource) {
	t.Helper()
	m := newModel(120, 34)
	source := &quotaActionSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m.src = source
	m.tab = 2
	m.quotaShowUnset = true
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	m = keys(m, "p")
	if m.mode != modeQuotaConfirm {
		t.Fatal("protect did not ask")
	}
	return m, source
}

func TestQuotaProtectConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		yes  bool
	}{
		{"enter", keyEnter(), true},
		{"y", keyRune("y"), true},
		{"Y", keyRune("Y"), true},
		{"esc", keyEsc(), false},
		{"n", keyRune("n"), false},
		{"N", keyRune("N"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, source := quotaConfirmModel(t)
			confirmHintShown(t, m)
			m, cmd := press(m, tc.key)
			if tc.yes {
				if cmd == nil {
					t.Fatal("missing save command")
				}
				result := cmd()
				if source.method != protocol.OpProtectedProvidersSet || len(source.value.Providers) != 1 || source.value.Providers[0] != "anthropic" {
					t.Fatalf("save: method %q value %+v", source.method, source.value)
				}
				next, _ := m.Update(result)
				m = next.(Model)
				if m.activeNotice() == nil || m.mode != modeNormal {
					t.Fatalf("save did not close with a notice: %+v", m.notice)
				}
				return
			}
			if cmd != nil || m.mode != modeNormal || source.method != "" {
				t.Fatalf("cancel: mode %v cmd %v call %q", m.mode, cmd != nil, source.method)
			}
		})
	}
}

func TestQuotaUnprotectConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		yes  bool
	}{
		{"enter", keyEnter(), true},
		{"y", keyRune("y"), true},
		{"esc", keyEsc(), false},
		{"n", keyRune("n"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(120, 34)
			source := &quotaActionSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
			source.fakeSource.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}}
			m.src = source
			m.tab = 2
			m.quotaShowUnset = true
			m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
			m.snap.ProtectedProviders = protocol.ProtectedProviders{Providers: []string{"anthropic"}}
			m = keys(m, "p")
			if m.mode != modeQuotaConfirm || !m.quotaUnprotect {
				t.Fatal("unprotect did not ask")
			}
			confirmHintShown(t, m)
			m, cmd := press(m, tc.key)
			if tc.yes {
				if cmd == nil {
					t.Fatal("missing save command")
				}
				cmd()
				if source.method != protocol.OpProtectedProvidersSet || len(source.value.Providers) != 0 {
					t.Fatalf("save: method %q value %+v", source.method, source.value)
				}
				return
			}
			if cmd != nil || m.mode != modeNormal || source.method != "" {
				t.Fatalf("cancel: mode %v cmd %v call %q", m.mode, cmd != nil, source.method)
			}
		})
	}
}

func TestTrustConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		yes  bool
	}{
		{"enter", keyEnter(), true},
		{"y", keyRune("y"), true},
		{"esc", keyEsc(), false},
		{"n", keyRune("n"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, src := workerModel(120, 34)
			m.mode = modeTrust
			m.confirmID = workerID
			m.confirmName = "tenebrae (Mac)"
			confirmHintShown(t, m)
			m, cmd := press(m, tc.key)
			if tc.yes {
				m = finish(t, m, cmd)
				if len(src.trusts) != 1 || src.trusts[0].id != workerID || !src.trusts[0].on {
					t.Fatalf("trusts %+v", src.trusts)
				}
				return
			}
			if cmd != nil || m.mode != modeNormal || len(src.trusts) != 0 {
				t.Fatalf("cancel: mode %v cmd %v trusts %+v", m.mode, cmd != nil, src.trusts)
			}
		})
	}
}

func proposalConfirmModel(t *testing.T) (Model, *proposalSource, protocol.CTOProposal) {
	t.Helper()
	p := proposalFixture()
	s := &proposalSource{proposals: []protocol.CTOProposal{p}}
	m := New(s)
	m.proposals = s.proposals
	m.selected = proposalKey(p)
	m.openProposalConfirm()
	return m, s, p
}

func TestProposalConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		yes  bool
	}{
		{"enter", keyEnter(), true},
		{"y", keyRune("y"), true},
		{"Y", keyRune("Y"), true},
		{"esc", keyEsc(), false},
		{"n", keyRune("n"), false},
		{"N", keyRune("N"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, s, p := proposalConfirmModel(t)
			confirmHintShown(t, m)
			m, cmd := press(m, tc.key)
			if tc.yes {
				if cmd == nil {
					t.Fatal("no answer command")
				}
				cmd()
				if len(s.answers) != 1 || s.answers[0].Action != "A" || len(s.answers[0].Confirm) != 1 || s.answers[0].Confirm[0].ID != p.ID {
					t.Fatalf("answers %+v", s.answers)
				}
				if m.proposalConfirm {
					t.Fatal("confirm stayed open")
				}
				return
			}
			if cmd != nil || m.proposalConfirm || len(s.answers) != 0 {
				t.Fatalf("cancel: open %v cmd %v answers %+v", m.proposalConfirm, cmd != nil, s.answers)
			}
		})
	}
}

func TestCTONewConfirmGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
		m := New(src)
		m.width, m.height = size.w, size.h
		m.focus = focusCTO
		m, _ = press(m, keyRune("n"))
		if !m.cto.confirming {
			t.Fatal("new conversation did not ask")
		}
		confirmHintShown(t, m)
		golden(t, "cto_new_confirm_"+itoa(size.w)+"x"+itoa(size.h), m.View())
	}
}

func TestCTONewConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		yes  bool
	}{
		{"enter", keyEnter(), true},
		{"y", keyRune("y"), true},
		{"Y", keyRune("Y"), true},
		{"esc", keyEsc(), false},
		{"n", keyRune("n"), false},
		{"N", keyRune("N"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
			m := New(src)
			m.width, m.height = 120, 34
			m.focus = focusCTO
			m, _ = press(m, keyRune("n"))
			if !m.cto.confirming {
				t.Fatal("new conversation did not ask")
			}
			confirmHintShown(t, m)
			m, cmd := press(m, tc.key)
			if tc.yes {
				if cmd == nil {
					t.Fatal("no new conversation command")
				}
				cmd()
				if src.fresh != 1 || m.cto.confirming {
					t.Fatalf("fresh %d confirming %v", src.fresh, m.cto.confirming)
				}
				return
			}
			if cmd != nil || m.cto.confirming || src.fresh != 0 {
				t.Fatalf("cancel: confirming %v cmd %v fresh %d", m.cto.confirming, cmd != nil, src.fresh)
			}
			if m.focus != focusCTO {
				t.Fatal("cancel left the CTO box")
			}
		})
	}
}

func TestResumeConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		yes  bool
	}{
		{"enter", keyEnter(), true},
		{"y", keyRune("y"), true},
		{"Y", keyRune("Y"), true},
		{"esc", keyEsc(), false},
		{"n", keyRune("n"), false},
		{"N", keyRune("N"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := workerModel(120, 34)
			m.snap.Agents[0].Online = false
			m.snap.Agents[0].LastSeen = t0.Add(-3 * time.Hour).Unix()
			m.showOffline = true
			m = selectID(m, "s_0000000000000001")
			m, _ = press(m, keyEnter())
			if m.resumeConfirm == nil {
				t.Fatal("resume did not ask")
			}
			confirmHintShown(t, m)
			m, cmd := press(m, tc.key)
			if tc.yes {
				if cmd == nil || m.resumeConfirm != nil {
					t.Fatal("resume did not start")
				}
				return
			}
			if cmd != nil || m.resumeConfirm != nil {
				t.Fatal("resume cancel did not stop")
			}
			if strings.Contains(ansi.Strip(m.View()), confirmHint) {
				t.Fatal("hint stayed after cancel")
			}
		})
	}
}

func TestToolsApplyConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		yes  bool
	}{
		{"enter", "enter", true},
		{"y", "y", true},
		{"Y", "Y", true},
		{"esc", "esc", false},
		{"n", "n", false},
		{"N", "N", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, src := toolsAtScreen(120, 34, 0)
			for i, d := range m.shownDiffs() {
				if d.Kind == toolsdiff.KindDrift {
					m.tl.row = i
				}
			}
			m = resolveTools(m.openFix())
			if m.tl.modal == nil {
				t.Fatal("enter did not open the plan")
			}
			m = pressKey(m, tc.key)
			if tc.yes {
				if len(src.applied) != 1 {
					t.Fatalf("applied %v", src.applied)
				}
				return
			}
			if m.tl.modal != nil || len(src.applied) != 0 {
				t.Fatalf("cancel: modal %v applied %v", m.tl.modal != nil, src.applied)
			}
		})
	}
}

func TestHandoverConfirmKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		yes  bool
	}{
		{"enter", "enter", true},
		{"y", "y", true},
		{"Y", "Y", true},
		{"esc", "esc", false},
		{"n", "n", false},
		{"N", "N", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}}
			m := New(src)
			m.loc, m.now = time.UTC, func() time.Time { return t0 }
			m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
			m = settle(refresh(m, src))
			m.selected = "s_0000000000000002"
			m = pressKey(m, "c")
			if m.handover == nil {
				t.Fatal("c did not open the preview")
			}
			want := m.handover.text
			m = pressKey(m, tc.key)
			if m.handover != nil {
				t.Fatalf("%s did not close the preview", tc.key)
			}
			if tc.yes {
				if src.sentBody != want {
					t.Fatalf("sent %q, want the preview %q", src.sentBody, want)
				}
				return
			}
			if src.sentBody != "" {
				t.Fatalf("cancel sent %q", src.sentBody)
			}
		})
	}
}
