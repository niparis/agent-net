package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
			m := keys(newModel(size[0], size[1]), "C")
			box := ansi.Strip(strings.Join(m.ctoView(size[0], m.ctoBoxHeight(m.tab)), "\n"))
			bar := ansi.Strip(m.helpLine(size[0]))
			if !strings.Contains(box, "ctrl+]: back") || strings.Contains(box, "tab: back") {
				t.Errorf("focused CTO hint: %s", box)
			}
			if !strings.Contains(bar, "ctrl+] back") || strings.Contains(bar, "tab back") {
				t.Errorf("focused CTO key bar: %s", bar)
			}
			help := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.helpContent(size[0]), "\n"))), " ")
			if !strings.Contains(help, "back to Sessions") || strings.Contains(help, "tab returns") {
				t.Errorf("CTO help does not describe the return key correctly: %s", help)
			}
			for _, key := range []tea.KeyPressMsg{{Code: ']', Mod: tea.ModCtrl}, {Code: tea.KeyTab}} {
				next := send(m, key)
				if next.focus != focusSessions {
					t.Errorf("%v did not return focus to Sessions", key)
				}
			}
		})
	}
}

func TestCTOOnboardingTipCanStillBeSkipped(t *testing.T) {
	m, src := onboardingModel()
	m.focus = focusCTO
	if hint := ansi.Strip(m.onboardingLine(120)); !strings.Contains(hint, "esc skip") {
		t.Errorf("tip does not show the skip key: %s", hint)
	}
	next, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = onboardingRun(next.(Model), cmd)
	if src.progress.Steps["see"] != "skipped" || m.focus != focusCTO {
		t.Fatalf("skip key did not skip the tip while retaining focus: %+v", src.progress.Steps)
	}
}

func TestCTOShortcutFromEveryTab(t *testing.T) {
	for _, tab := range []string{"1", "2", "3", "4", "5"} {
		for _, key := range []string{"C"} {
			t.Run(tab+"/"+key, func(t *testing.T) {
				m := keys(newModel(120, 34), tab, key)
				if m.focus != focusCTO || m.tab != sessionsTab {
					t.Fatalf("%s: focus=%v tab=%d", key, m.focus, m.tab)
				}
			})
		}
	}
}

func TestCTOShortcutFromCardAndStartsWhenMissing(t *testing.T) {
	m := graphModel(80, 24)
	m.mapOn = true
	m = keys(m, "C")
	if m.mapOn || m.tab != sessionsTab || m.focus != focusCTO {
		t.Fatal("c did not leave the Graph card for the CTO")
	}
	for _, key := range []string{"C"} {
		src := &fakeCTOSource{fakeSource: &fakeSource{}}
		m := New(src)
		m.cto = nil
		next, cmd := m.Update(tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
		m = next.(Model)
		if m.focus != focusCTO || m.cto == nil || !m.cto.polling || cmd == nil {
			t.Errorf("%s did not start the missing CTO", key)
		}
	}
}

func TestCTOCompactHeightAndLatestLine(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for _, tab := range []string{"1", "3", "4", "5"} {
			t.Run(fmt.Sprintf("%dx%d/tab%s", size[0], size[1], tab), func(t *testing.T) {
				m := keys(newModel(size[0], size[1]), tab)
				m.cto.messages = []string{"old CTO line", "latest CTO line"}
				view := strings.Split(ansi.Strip(m.testView()), "\n")
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
				if tab == "4" {
					want = 4 // The conversation pane keeps the rows; the strip is small.
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
	m, _ = toolsAtScreen(80, 24)
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
		if view := ansi.Strip(m.testView()); !strings.Contains(view, "> "+label) {
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
	if m.cto.settingsForm == nil || !strings.Contains(ansi.Strip(m.testView()), "agent-net") {
		t.Fatalf("the compact settings form hid the title error:\n%s", ansi.Strip(m.testView()))
	}
}

// c opens the CTO session in one key from every tab: no box focus, no Enter.
func TestCTOcOpensSessionDirectly(t *testing.T) {
	for _, tab := range []string{"1", "2", "3", "4", "5"} {
		t.Run(tab, func(t *testing.T) {
			src := &fakeCTOSource{fakeSource: &fakeSource{}}
			m := keys(New(src), tab)
			focus := m.focus
			next, cmd := m.Update(tea.KeyPressMsg{Text: "c", Code: []rune("c")[0]})
			m = next.(Model)
			if m.focus != focus {
				t.Errorf("c moved focus from %v to %v", focus, m.focus)
			}
			if cmd == nil || m.cto == nil || !m.cto.opening {
				t.Fatal("c did not open the CTO")
			}
			runCmds(cmd)
			if len(src.enters) != 1 || src.enters[0].SessionID != m.ctoSessionID() {
				t.Fatalf("c sent %+v, want one session_enter for the CTO", src.enters)
			}
		})
	}
}

// runCmds runs a command and the commands of a batch, without following the
// messages they return.
func runCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmds(c)
		}
	}
}
