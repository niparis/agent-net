package tui

import (
	"strings"
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
		{"06_view_v", []check{{contextSessions, "v", "cycle view"}, {contextSessions, "A", ""}, {contextSessions, "a", ""}}},
		{"07_held_answer", []check{{contextHeld, "a", "approve"}, {contextHeld, "r", "refuse"}, {contextHeld, "A", ""}, {contextHeld, "R", ""}}},
		{"08_outcome_overrule", []check{{contextOutcome, "O", "overrule"}, {contextOutcome, "o", ""}, {contextSessions, "o", ""}}},
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
		{"20_tools_one_list", []check{{contextTools, "]", ""}, {contextTools, "[", ""}, {contextTools, "left", ""}, {contextTools, "right", ""}, {contextTools, "R", "rescan"}, {contextTools, "r", ""}}},
		{"21_no_tools_hl", []check{{contextTools, "h", ""}, {contextTools, "l", ""}, {contextTools, "v", ""}, {contextTools, "m", ""}}},
		{"22_no_tools_screens", []check{{contextTools, "1", ""}, {contextTools, "2", ""}, {contextTools, "tab", ""}}},
		{"23_no_accept_key", []check{{contextTools, "a", ""}, {contextTools, "x", ""}, {contextTools, ".", ""}}},
		{"24_list_details", []check{{contextTools, "enter", "open actions"}}},
		{"25_picker_space", []check{{contextToolsPicker, " ", "tick"}, {contextToolsPicker, "x", ""}}},
		{"26_help_close", []check{{contextHelp, "esc", actClose}, {contextHelp, "?", actClose}, {contextHelp, "enter", ""}, {contextHelp, "backspace", ""}}},
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
	m.tab = inboxTab
	m.inboxDetail = true
	m = keys(m, "backspace")
	if !m.inboxDetail {
		t.Fatal("backspace closed inbox details")
	}
}

func TestApprovedSessionsAndHeldKeys(t *testing.T) {
	m := newModel(120, 34)
	m = keys(m, "v", "v", "v")
	if !m.activeOnly {
		t.Fatal("v did not reach active only")
	}
	m = keys(m, "v")
	if m.activeOnly {
		t.Fatal("v did not leave active only")
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
	m, _ := toolsAtScreen(120, 34)
	m = keys(m, "]", "[", "right", "left")
	if v := plain(m); !strings.Contains(v, "NAME") || !strings.Contains(v, "STATUS") {
		t.Fatalf("Tools is not the one list:\n%s", v)
	}
	if strings.Contains(plain(m), "Differences") || strings.Contains(plain(m), "By project") {
		t.Fatalf("the old Tools screens are back:\n%s", plain(m))
	}
	m = keys(m, "enter")
	if m.tl.modal == nil {
		t.Fatal("enter did not open the selected item")
	}
}
