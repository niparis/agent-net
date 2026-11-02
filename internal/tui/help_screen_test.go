package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func helpText(m Model) string {
	return ansi.Strip(strings.Join(m.helpContent(m.width), "\n"))
}

func TestHelpShowsTheCurrentScreenFirst(t *testing.T) {
	for _, tc := range []struct {
		name string
		tab  int
		keys []string
	}{
		{"Sessions", sessionsTab, []string{"?"}},
		{"Tools", toolsTab, []string{"5", "?"}},
		{"Graph", graphTab, []string{"2", "?"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := keys(newModel(120, 40), tc.keys...)
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "Keys · "+tc.name) {
				t.Fatalf("help title does not name %s:\n%s", tc.name, view)
			}
			body := helpText(m)
			want := "\n " + tc.name + "\n"
			if strings.HasPrefix(body, " "+tc.name+"\n") {
				want = " " + tc.name + "\n"
			}
			current := strings.Index(body, want)
			global := strings.Index(body, "\n Global keys\n")
			if current < 0 || global < 0 || current > global {
				t.Fatalf("%s section is not before Global keys:\n%s", tc.name, body)
			}
			for i, name := range tabNames {
				if i == tc.tab || name == tc.name {
					continue
				}
				if strings.Contains(body, "\n "+name+"\n") {
					t.Fatalf("%s help also shows the %s section:\n%s", tc.name, name, body)
				}
			}
		})
	}
}

func TestHelpQuestionTogglesAllScreens(t *testing.T) {
	m := keys(newModel(120, 40), "?")
	if m.helpAll {
		t.Fatal("help opened on the full list")
	}
	m = keys(m, "?")
	if !m.help || !m.helpAll {
		t.Fatalf("second ? did not open the full list: help %v all %v", m.help, m.helpAll)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Keys · all screens") {
		t.Fatalf("the full list has no title:\n%s", view)
	}
	body := helpText(m)
	if !strings.HasPrefix(body, " Global keys\n") {
		t.Fatalf("the full list does not start with Global keys:\n%s", body)
	}
	for _, name := range tabNames {
		if !strings.Contains(body, "\n "+name+"\n") {
			t.Fatalf("the full list lacks %s:\n%s", name, body)
		}
	}
	if !strings.Contains(body, "\n In overlays\n") {
		t.Fatalf("the full list lacks In overlays:\n%s", body)
	}
	m = keys(m, "?")
	if m.helpAll {
		t.Fatal("third ? did not return to this screen")
	}
	if !strings.Contains(ansi.Strip(m.View()), "Keys · Sessions") {
		t.Fatalf("this screen did not return:\n%s", ansi.Strip(m.View()))
	}
}

func TestHelpEscClosesEitherView(t *testing.T) {
	m := keys(newModel(120, 40), "?")
	if !m.help {
		t.Fatal("? did not open help")
	}
	m = keys(m, "esc")
	if m.help {
		t.Fatal("esc did not close this screen help")
	}
	m = keys(m, "?", "?")
	if !m.helpAll {
		t.Fatal("? ? did not open the full list")
	}
	m = keys(m, "esc")
	if m.help {
		t.Fatal("esc did not close the full list")
	}
}

func TestQualityDetailsHelpShowsItsOwnSection(t *testing.T) {
	m := keys(newModel(120, 40), "3", "d")
	if !m.qualityDetail {
		t.Fatal("d did not open Quality details")
	}
	m = keys(m, "?")
	if !m.help {
		t.Fatal("? did not open help on Quality details")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Keys · Quality details") {
		t.Fatalf("help title does not name Quality details:\n%s", view)
	}
	body := helpText(m)
	if !strings.HasPrefix(body, " Quality details\n") {
		t.Fatalf("Quality details is not the first section:\n%s", body)
	}
	if strings.Contains(body, "\n Quotas\n") {
		t.Fatalf("Quality details help also shows the Quotas section:\n%s", body)
	}
	if !strings.Contains(body, "\n Global keys\n") {
		t.Fatalf("Quality details help lacks Global keys:\n%s", body)
	}
}

func TestToolsMenuHelpShowsItsOwnSection(t *testing.T) {
	m := keys(newModel(120, 40), "5")
	m.tl.modal = &toolsModal{menu: &toolMenu{}}
	m = keys(m, "?")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Keys · Tools menu") {
		t.Fatalf("help title does not name the Tools menu:\n%s", view)
	}
	body := helpText(m)
	if !strings.HasPrefix(body, " Tools menu\n") {
		t.Fatalf("Tools menu is not the first section:\n%s", body)
	}
	if strings.Contains(body, "\n Tools\n") {
		t.Fatalf("Tools menu help also shows the Tools tab section:\n%s", body)
	}
}

func TestCTOBoxHelpShowsItsOwnSection(t *testing.T) {
	m := keys(newModel(120, 40))
	m.tab, m.focus = sessionsTab, focusCTO
	m.cto = &ctoState{generation: 1}
	m = keys(m, "?")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Keys · CTO box") {
		t.Fatalf("help title does not name the CTO box:\n%s", view)
	}
	if !strings.HasPrefix(helpText(m), " CTO box\n") {
		t.Fatalf("CTO box is not the first section:\n%s", helpText(m))
	}
}

func TestHelpKeyBarNamesTheToggle(t *testing.T) {
	m := keys(newModel(120, 40), "?")
	bar := ansi.Strip(m.View())
	for _, want := range []string{"? all screens", "up/down scroll", "esc close"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("this screen help bar lacks %q:\n%s", want, bar)
		}
	}
	m = keys(m, "?")
	bar = ansi.Strip(m.View())
	for _, want := range []string{"? this screen", "up/down scroll", "esc close"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("full help bar lacks %q:\n%s", want, bar)
		}
	}
}
