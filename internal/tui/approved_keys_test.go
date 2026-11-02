package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
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
		{"01_refresh", []check{{contextSessions, "R", "refresh"}, {contextMessages, "R", "refresh"}, {contextQuotas, "R", "refresh"}, {contextCard, "R", "refresh"}, {contextTools, "R", "rescan"}, {contextSessions, "r", ""}}},
		{"02_graph_aliases", []check{{contextGlobal, "2", "graph tab"}, {contextGlobal, "M", ""}, {contextGlobal, "G", ""}, {contextHelp, "G", actLast}}},
		{"03_escape_filter", []check{{contextSessions, "esc", "back"}, {contextGraphFind, "esc", "clear or back"}, {contextToolsPicker, "esc", "cancel"}}},
		{"04_shared_graph_help", []check{{contextGraph, "?", "help"}}},
		{"05_no_session_e", []check{{contextSessions, "e", ""}}},
		{"06_active_A", []check{{contextSessions, "A", "active only"}, {contextSessions, "a", ""}}},
		{"07_held_answer", []check{{contextHeld, "a", "approve"}, {contextHeld, "r", "refuse"}, {contextHeld, "A", ""}, {contextHeld, "R", ""}}},
		{"08_outcome_overrule", []check{{contextOutcome, "O", "overrule"}, {contextOutcome, "o", ""}, {contextSessions, "o", "offline"}}},
		{"09_proposal_decline", []check{{contextProposal, "r", "decline"}, {contextProposalDetail, "r", "decline"}, {contextProposal, "x", ""}}},
		{"10_message_enter", []check{{contextMessages, "enter", "message body"}}},
		{"11_message_end", []check{{contextMessages, "end", actLast}}},
		{"12_no_message_v", []check{{contextMessages, "v", ""}}},
		{"13_body_close", []check{{contextBody, "esc", actClose}, {contextBody, "enter", actClose}, {contextBody, "m", ""}, {contextBody, "backspace", ""}}},
		{"14_inbox_back", []check{{contextInbox, "esc", "back"}, {contextInbox, "backspace", ""}}},
		{"15_quota_enter", []check{{contextQuotas, "enter", ""}}},
		{"16_graph_pan", []check{{contextGraph, "shift+left", "pan left"}, {contextGraph, "h", ""}, {contextGraph, "j", ""}, {contextGraph, "k", ""}, {contextGraph, "l", ""}}},
		{"17_graph_links", []check{{contextGraph, ",", "previous link"}, {contextGraph, ".", "next link"}, {contextGraph, "[", ""}, {contextGraph, "]", ""}}},
		{"18_card_scroll", []check{{contextCard, "ctrl+u", actPageUp}, {contextCard, "ctrl+d", actPageDown}, {contextCard, "[", ""}, {contextCard, "]", ""}}},
		{"19_card_learned", []check{{contextCard, ",", "previous learned"}, {contextCard, ".", "next learned"}, {contextCard, "u", "undo learned"}, {contextCard, "U", ""}}},
		{"20_tools_columns", []check{{contextToolsAll, "left", "previous column"}, {contextToolsAll, "right", "next column"}, {contextToolsAll, "[", "previous screen"}, {contextToolsAll, "]", "next screen"}}},
		{"21_no_tools_hl", []check{{contextToolsAll, "h", ""}, {contextToolsAll, "l", ""}}},
		{"22_tools_machine", []check{{contextToolsProject, "v", "next machine"}, {contextToolsProject, "m", ""}}},
		{"23_accept_difference", []check{{contextToolsDiff, "a", "accept difference"}, {contextToolsDiff, "x", ""}}},
		{"24_all_details", []check{{contextToolsAll, "enter", "item detail"}}},
		{"25_picker_space", []check{{contextToolsPicker, " ", "tick"}, {contextToolsPicker, "x", ""}}},
		{"26_help_close", []check{{contextHelp, "esc", actClose}, {contextHelp, "?", actClose}, {contextHelp, "enter", ""}, {contextHelp, "backspace", ""}}},
		{"27_notifications", []check{{contextHelp, "N", actNotifications}, {contextHelp, "n", ""}}},
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
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd == nil {
		t.Fatal("R did not refresh Sessions")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
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
	if m.help {
		t.Fatal("question mark did not close help")
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
	m.inboxOn = true
	m = keys(m, "backspace")
	if !m.inboxOn {
		t.Fatal("backspace closed inbox")
	}
}

func TestApprovedSessionsAndHeldKeys(t *testing.T) {
	m := newModel(120, 34)
	m = keys(m, "A")
	if !m.activeOnly {
		t.Fatal("A did not toggle active only")
	}
	m = keys(m, "a")
	if !m.activeOnly {
		t.Fatal("a still toggles active only")
	}
	src := &heldTestSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
	src.snap.Held = []protocol.Message{{ID: "m_held", FromName: "peer", To: "s_0000000000000002", Body: "hello"}}
	n := New(src)
	n.snap = src.snap
	n.selected = "m_held"
	_, cmd := n.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd != nil && src.answer.MessageID != "" {
		t.Fatal("R answered held request")
	}
}

func TestApprovedToolsModes(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, toolsDiffs)
	m = keys(m, "]")
	if m.tl.screen != toolsAll {
		t.Fatal("] did not switch to All tools")
	}
	m = keys(m, "right")
	if m.tl.screen != toolsAll || m.tl.col != 1 {
		t.Fatal("right did not move the machine column")
	}
	m = keys(m, "enter")
	if m.tl.modal == nil {
		t.Fatal("enter did not open the selected item")
	}
	m = keys(m, "esc", "]")
	if m.tl.screen != toolsProject {
		t.Fatal("] did not switch to By project")
	}
	start := m.tl.machine
	m = keys(m, "v")
	if len(m.tl.cat.Machines) > 1 && m.tl.machine == start {
		t.Fatal("v did not select the next machine")
	}
}
