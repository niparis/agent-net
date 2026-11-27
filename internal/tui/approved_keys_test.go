package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestApprovedKeyMapContract(t *testing.T) {
	type check struct {
		context keyContext
		key     string
		want    actionID
	}
	cases := []struct {
		name   string
		checks []check
	}{
		{"01_refresh", []check{{contextSessions, "R", "refresh"}, {contextMessages, "R", "refresh"}, {contextQuotas, "R", "refresh"}, {contextCard, "R", "refresh"}, {contextSessions, "r", ""}}},
		{"02_graph_aliases", []check{{contextGlobal, "2", "graph tab"}, {contextGlobal, "M", ""}, {contextGlobal, "G", ""}, {contextHelp, "G", actLast}}},
		{"03_escape_filter", []check{{contextSessions, "esc", "back"}, {contextGraphFind, "esc", "clear or back"}}},
		{"04_shared_graph_help", []check{{contextGraph, "?", "help"}}},
		{"05_no_session_e", []check{{contextSessions, "e", ""}}},
		{"06_view_v", []check{{contextSessions, "v", "cycle view"}, {contextSessions, "A", ""}, {contextSessions, "a", "archive offline"}}},
		{"10_message_enter", []check{{contextMessages, "enter", "message body"}}},
		{"11_message_end", []check{{contextMessages, "end", actLast}}},
		{"12_no_message_v", []check{{contextMessages, "v", ""}}},
		{"13_body_close", []check{{contextBody, "esc", actClose}, {contextBody, "enter", actClose}, {contextBody, "m", ""}, {contextBody, "backspace", ""}}},
		{"15_quota_enter", []check{{contextQuotas, "enter", ""}}},
		{"16_graph_pan", []check{{contextGraph, "shift+left", "pan left"}, {contextGraph, "h", ""}, {contextGraph, "j", ""}, {contextGraph, "k", ""}, {contextGraph, "l", ""}}},
		{"17_graph_links", []check{{contextGraph, ",", "previous link"}, {contextGraph, ".", "next link"}, {contextGraph, "[", ""}, {contextGraph, "]", ""}}},
		{"18_card_scroll", []check{{contextCard, "ctrl+u", actPageUp}, {contextCard, "ctrl+d", actPageDown}, {contextCard, "[", ""}, {contextCard, "]", ""}}},
		{"19_card_learned", []check{{contextCard, ",", "previous learned"}, {contextCard, ".", "next learned"}, {contextCard, "u", "undo learned"}, {contextCard, "U", ""}}},
		{"26_help_close", []check{{contextHelp, "esc", actClose}, {contextHelp, "?", actAllScreens}, {contextHelp, "enter", ""}, {contextHelp, "backspace", ""}}},
		{"27_notifications", []check{{contextHelp, "N", ""}, {contextHelp, "n", ""}}},
		{"28_confirm", []check{{contextConfirm, "enter", "yes"}, {contextConfirm, "y", "yes"}, {contextConfirm, "esc", "no"}, {contextConfirm, "n", "no"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, check := range tc.checks {
				if got := keyAction(check.context, check.key); got != check.want {
					t.Errorf("%s %q: got %q, want %q", check.context, check.key, got, check.want)
				}
			}
		})
	}
}

func TestApprovedGraphKeys(t *testing.T) {
	m := graphModel(120, 34)
	start := m.graphState.Graph.Selected()
	m = keys(m, ".")
	if m.graphState.Graph.Selected() == start {
		t.Fatal("period did not select the next linked node")
	}
	selected := m.graphState.Graph.Selected()
	m = keys(m, "[", "]", "h", "j", "k", "l")
	if m.graphState.Graph.Selected() != selected {
		t.Fatal("removed graph aliases changed the selected node")
	}
	m = keys(m, "?")
	if !m.help {
		t.Fatal("graph did not open the shared help overlay")
	}
}

func TestApprovedRefreshKeys(t *testing.T) {
	m := newModel(120, 34)
	_, cmd := m.Update(tea.KeyPressMsg{Text: "R", Code: []rune("R")[0]})
	if cmd == nil {
		t.Fatal("R did not refresh Sessions")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Text: "r", Code: []rune("r")[0]})
	if cmd != nil {
		t.Fatal("r still refreshes Sessions")
	}
}

func TestApprovedOverlayCloseKeys(t *testing.T) {
	m := newModel(120, 34)
	m.help = true
	m = keys(m, "enter", "backspace")
	if !m.help {
		t.Fatal("enter or backspace closed help")
	}
	m = keys(m, "?")
	if !m.help || !m.helpAll {
		t.Fatal("? did not switch help to all screens")
	}
	m = keys(m, "esc")
	if m.help {
		t.Fatal("esc did not close the full help")
	}
	m.open = &openMsg{}
	m = keys(m, "m", "backspace")
	if m.open == nil {
		t.Fatal("m or backspace closed message body")
	}
	m = keys(m, "enter")
	if m.open != nil {
		t.Fatal("enter did not close message body")
	}
}
