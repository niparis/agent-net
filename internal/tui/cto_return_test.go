package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCTOKeysReturnAndSkip(t *testing.T) {
	for _, tc := range []struct {
		tab  int
		want focus
	}{{sessionsTab, focusSessions}, {messagesTab, focusFeed}} {
		t.Run(fmt.Sprint(tc.tab), func(t *testing.T) {
			m, src := onboardingModel()
			m.tab, m.focus = tc.tab, focusCTO
			next, cmd := m.key(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
			m = onboardingRun(next.(Model), cmd)
			if m.focus != tc.want {
				t.Errorf("ctrl+] focus = %v, want %v", m.focus, tc.want)
			}
			if len(src.progress.Steps) != 0 {
				t.Errorf("ctrl+] skipped a tip: %+v", src.progress.Steps)
			}
		})
	}
	t.Run("escape skips and stays", func(t *testing.T) {
		m, src := onboardingModel()
		m.focus = focusCTO
		next, cmd := m.key(tea.KeyMsg{Type: tea.KeyEsc})
		m = onboardingRun(next.(Model), cmd)
		if m.focus != focusCTO || src.progress.Steps["see"] != "skipped" {
			t.Errorf("esc focus = %v, progress = %+v; want focus CTO and skipped tip", m.focus, src.progress.Steps)
		}
	})
	t.Run("escape without tip stays", func(t *testing.T) {
		m := keys(newModel(120, 34), "C", "esc")
		if m.focus != focusCTO {
			t.Errorf("esc changed focus to %v", m.focus)
		}
	})
	t.Run("greater-than does not skip", func(t *testing.T) {
		m, src := onboardingModel()
		m.focus = focusCTO
		next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(">")})
		m = onboardingRun(next.(Model), cmd)
		if m.focus != focusCTO || len(src.progress.Steps) != 0 {
			t.Errorf("> changed focus or skipped a tip: focus=%v progress=%+v", m.focus, src.progress.Steps)
		}
	})
}

func TestCTOKeysReturnHints(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := keys(newModel(size[0], size[1]), "C")
			box := ansi.Strip(strings.Join(m.ctoView(size[0], m.ctoBoxHeight(m.tab)), "\n"))
			bar := ansi.Strip(m.helpLine(size[0]))
			if !strings.Contains(box, "ctrl+]: back") {
				t.Errorf("focused CTO hint: %s", box)
			}
			if !strings.Contains(bar, "ctrl+] back") {
				t.Errorf("focused CTO key bar: %s", bar)
			}
		})
	}
}

func TestCTOKeysReturnDuringConfirmation(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyCtrlCloseBracket, tea.KeyTab} {
		t.Run(tea.KeyMsg{Type: key}.String(), func(t *testing.T) {
			m := keys(newModel(120, 34), "C")
			m.cto.confirming = true
			next, cmd := m.key(tea.KeyMsg{Type: key})
			m = next.(Model)
			if cmd != nil || m.focus != focusSessions || m.cto.confirming {
				t.Errorf("return key left focus=%v confirming=%v command=%v", m.focus, m.cto.confirming, cmd != nil)
			}
		})
	}
}

func TestHandoverKeysAndSourceRemoved(t *testing.T) {
	for _, tab := range []string{"1", "4"} {
		t.Run("tab "+tab, func(t *testing.T) {
			m := keys(newModel(120, 34), tab)
			before := m.View()
			next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
			if cmd != nil || next.(Model).View() != before {
				t.Errorf("b changed the screen or started a command")
			}
		})
	}
	t.Run("source", func(t *testing.T) {
		if _, err := os.Stat("handover.go"); !os.IsNotExist(err) {
			t.Errorf("handover.go still exists or cannot be checked: %v", err)
		}
	})
}
