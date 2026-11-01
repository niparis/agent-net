package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCTOTabDoesNotFocusFromAnyTab(t *testing.T) {
	for _, tab := range []string{"1", "2", "3", "4", "5"} {
		t.Run(tab, func(t *testing.T) {
			m := keys(newModel(120, 34), tab)
			focus := m.focus
			m = keys(m, "tab")
			if m.focus != focus {
				t.Fatalf("tab changed focus from %v to %v", focus, m.focus)
			}
		})
	}
}

func TestCTOBackHintsAndKeys(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := keys(newModel(size[0], size[1]), "c")
			box := ansi.Strip(strings.Join(m.ctoView(size[0], m.ctoBoxHeight(m.tab)), "\n"))
			bar := ansi.Strip(m.helpLine(size[0]))
			if !strings.Contains(box, "esc: back") || strings.Contains(box, "tab: back") {
				t.Errorf("focused CTO hint: %s", box)
			}
			if !strings.Contains(bar, "esc back") || strings.Contains(bar, "tab back") {
				t.Errorf("focused CTO key bar: %s", bar)
			}
			help := ansi.Strip(strings.Join(m.helpContent(size[0]), "\n"))
			if !strings.Contains(help, "esc returns") || strings.Contains(help, "tab returns") {
				t.Errorf("CTO help does not describe escape correctly: %s", help)
			}
			for _, key := range []string{"esc", "tab"} {
				next := keys(m, key)
				if next.focus != focusSessions {
					t.Errorf("%s did not return focus to Sessions", key)
				}
			}
		})
	}
}

func TestCTOEscLeavesFocusWithOnboardingTip(t *testing.T) {
	for _, tc := range []struct {
		tab  int
		want focus
	}{{sessionsTab, focusSessions}, {messagesTab, focusFeed}} {
		m, src := onboardingModel()
		m.tab, m.focus = tc.tab, focusCTO
		if got := m.onboardingStep().id; got == "" {
			t.Fatal("fixture has no onboarding tip")
		}
		next, cmd := m.key(tea.KeyMsg{Type: tea.KeyEsc})
		m = onboardingRun(next.(Model), cmd)
		if m.focus != tc.want {
			t.Errorf("tab %d: escape returned focus %v, want %v", tc.tab, m.focus, tc.want)
		}
		if len(src.progress.Steps) != 0 {
			t.Errorf("escape skipped a tip: %+v", src.progress.Steps)
		}
	}
}

func TestCTOOnboardingTipCanStillBeSkipped(t *testing.T) {
	m, src := onboardingModel()
	m.focus = focusCTO
	if hint := ansi.Strip(m.onboardingLine(120)); !strings.Contains(hint, "> skip") {
		t.Errorf("tip does not show the skip key: %s", hint)
	}
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(">")})
	m = onboardingRun(next.(Model), cmd)
	if src.progress.Steps["see"] != "skipped" || m.focus != focusCTO {
		t.Fatalf("skip key did not skip the tip while retaining focus: %+v", src.progress.Steps)
	}
}

func TestCTOLowercaseShortcutFromEveryTab(t *testing.T) {
	for _, tab := range []string{"1", "2", "3", "4", "5"} {
		t.Run(tab, func(t *testing.T) {
			m := keys(newModel(120, 34), tab, "c")
			if m.focus != focusCTO || m.tab != sessionsTab || m.handover != nil {
				t.Fatalf("c: focus=%v tab=%d handover=%v", m.focus, m.tab, m.handover != nil)
			}
		})
	}
}

func TestCTOShortcutFromCardAndStartsWhenMissing(t *testing.T) {
	m := graphModel(80, 24)
	m.mapOn = true
	m = keys(m, "c")
	if m.mapOn || m.tab != sessionsTab || m.focus != focusCTO {
		t.Fatal("c did not leave the Graph card for the CTO")
	}
	for _, key := range []string{"c", "C"} {
		src := &fakeCTOSource{fakeSource: &fakeSource{}}
		m := New(src)
		m.cto = nil
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = next.(Model)
		if m.focus != focusCTO || m.cto == nil || !m.cto.polling || cmd == nil {
			t.Errorf("%s did not start the missing CTO", key)
		}
	}
}

func TestHandOverBriefKeyOnSessionsAndMessages(t *testing.T) {
	t.Run("Sessions", func(t *testing.T) {
		m := pressKey(handoverWaitingModel(100, 30), "b")
		if m.handover == nil || !strings.Contains(m.handover.text, "a session that waits in Needs you") {
			t.Fatalf("b did not open the selected row's hand-over: %+v", m.handover)
		}
	})
	t.Run("Messages", func(t *testing.T) {
		m := keys(newModel(100, 30), "4")
		m.feedCur = feedIndexFor(m, "m_1")
		m = pressKey(m, "b")
		if m.handover == nil || m.handover.msgID != "m_1" {
			t.Fatalf("b did not open the selected message's hand-over: %+v", m.handover)
		}
	})
}

func TestCTOCompactHeightAndLatestLine(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for _, tab := range []string{"1", "2", "3", "4", "5"} {
			t.Run(fmt.Sprintf("%dx%d/tab%s", size[0], size[1], tab), func(t *testing.T) {
				m := keys(newModel(size[0], size[1]), tab)
				m.cto.messages = []string{"old CTO line", "latest CTO line"}
				view := strings.Split(ansi.Strip(m.View()), "\n")
				start := -1
				for i, line := range view {
					if strings.HasPrefix(line, "╭─CTO") {
						start = i
						break
					}
				}
				if start < 0 {
					t.Fatal("CTO box missing")
				}
				end := start + 1
				for end < len(view) && !strings.HasPrefix(view[end], "╰") {
					end++
				}
				if end == len(view) {
					t.Fatal("CTO box has no bottom border")
				}
				want := 6 // Half of 11 rows, rounded up.
				if size[1] == 24 {
					want = 4 // Half of 7 rows, rounded up.
					if tab == "3" {
						want = 3 // Quotas used 6 rows at this height.
					}
				}
				if got := end - start + 1; got != want {
					t.Errorf("CTO box has %d rows, want %d", got, want)
				}
				box := strings.Join(view[start:end+1], "\n")
				if !strings.Contains(box, "latest CTO line") || !strings.Contains(box, "c: open the CTO") {
					t.Errorf("box must show the latest line and the open hint:\n%s", box)
				}
			})
		}
	}
}

func TestCTOCompactSettingsAndTextInputs(t *testing.T) {
	m := keys(newModel(80, 24), "/", "c")
	if m.filter != "c" || m.focus == focusCTO {
		t.Fatalf("c did not stay in the filter: %q", m.filter)
	}
	m = keys(newModel(80, 24), "m", "c")
	if m.mode != modeCompose || m.compose.Value() != "c" {
		t.Fatalf("c did not stay in the message input: %q", m.compose.Value())
	}
	m = keys(graphModel(80, 24), "/", "c")
	if !m.mp.finding || m.mp.findQuery != "c" {
		t.Fatalf("c did not stay in Graph search: %q", m.mp.findQuery)
	}
	m, _ = toolsAtScreen(80, 24, 0)
	m = keys(m, "/", "c")
	if !m.tl.searching || m.tl.search != "c" {
		t.Fatalf("c did not stay in Tools search: %q", m.tl.search)
	}

	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m = keys(New(src), "C", "m")
	for i, label := range []string{"Harness:", "Title:", "Model:"} {
		if i > 0 {
			m = keys(m, "down")
		}
		if view := ansi.Strip(m.View()); !strings.Contains(view, "> "+label) {
			t.Errorf("focused field %s is hidden:\n%s", label, view)
		}
		if i == 1 {
			m.cto.settingsForm.titleInput.SetValue("")
			m = keys(m, "c")
			if got := m.cto.settingsForm.titleInput.Value(); got != "c" {
				t.Errorf("c did not reach the CTO title input: %q", got)
			}
		}
	}
	m.cto.settingsForm.input.SetValue("")
	m = keys(m, "c")
	if got := m.cto.settingsForm.input.Value(); got != "c" {
		t.Errorf("c did not reach the CTO model input: %q", got)
	}
	m.cto.settingsForm.titleInput.SetValue("Atlas [agent-net")
	m = keys(m, "enter")
	if m.cto.settingsForm == nil || !strings.Contains(ansi.Strip(m.View()), "agent-net") {
		t.Fatalf("the compact settings form hid the title error:\n%s", ansi.Strip(m.View()))
	}
}
