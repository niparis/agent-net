package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The key line and the help overlay. The model (model.go) supplies the state;
// this file only draws it.

// ---- help ----

// hint is one entry of the key line. A higher prio is dropped first when the
// line is too long.
type hint struct {
	key, label string
	prio       int
}

// hints lists the keys used most on the current screen. The line holds at most
// seven of them, always with help and quit. Every other key is in helpContent.
func (m Model) hints() []hint {
	switch {
	case m.proposalConfirm:
		return []hint{{"up/down", "scroll", 1}, {"y", "approve all", 1}, {"n/esc", "cancel", 1}}
	case m.proposalHistory && m.proposalDetail == nil && !m.help:
		return []hint{{"up/down", "select", 1}, {"enter", "detail", 1}, {"esc", "back", 1}, {"?", "help", 1}}
	case m.proposalDetail != nil && !m.help:
		if m.proposalDetail.proposal.Status != "open" {
			return []hint{{"up/down", "scroll", 1}, {"esc", "back", 1}, {"?", "help", 1}}
		}
		return []hint{{"a", "approve", 1}, {"x", "decline", 1}, {"A", "approve all", 1}, {"esc", "back", 1}, {"?", "help", 1}}
	case m.help:
		return []hint{{"up/down", "scroll", 1}, {"esc", "close help", 1}, {"q", "quit", 1}}
	case m.mapOn && m.focus != focusCTO:
		return []hint{{"enter", "sessions", 1}, {"esc", "Graph", 0}, {"[ ]", "scroll", 1}, {"U/u", "learned/undo", 2}, {"?", "help", 0}, {"q", "quit", 0}}
	case m.open != nil:
		return []hint{{"up/down", "scroll", 1}, {"esc", "close", 1}, {"q", "quit", 1}}
	case m.focus == focusCTO:
		return []hint{{"enter", "open " + m.ctoTitle(), 1}, {"tab", "back", 1}, {"m", "model", 2}, {"n", "new", 2}, {"u", "undo", 2}}
	case m.tab == 1:
		return []hint{{"up/down", "thread", 1}, {"left/right", "msg", 1}, {"enter", "latest", 1}, {"v", "body", 1}, {"tab", m.ctoTitle(), 2}, {"?", "help", 2}, {"q", "quit", 0}}
	case m.tab == 2:
		return []hint{{"up/down", "select", 1}, {"p", "protect", 1}, {"s", "show unset", 2}, {"r", "refresh", 2}, {"tab", m.ctoTitle(), 2}, {"?", "help", 2}, {"q", "quit", 0}}
	case m.tab == graphTab:
		return []hint{{"1-5", "tabs", 1}, {"tab", m.ctoTitle(), 1}, {"?", "help", 2}, {"q", "quit", 0}}
	case m.tab == toolsTab:
		return m.toolsHints()
	}
	for _, e := range m.snap.Outcomes {
		if m.selected == "outcome:"+e.ReplyID {
			return []hint{{"s", "let stand", 1}, {"o", "overrule", 1}, {"?", "help", 2}, {"q", "quit", 0}}
		}
	}
	if _, ok := m.selectedProposal(); ok {
		return []hint{{"enter", "detail", 1}, {"a", "approve", 1}, {"x", "decline", 1}, {"A", "approve all", 1}, {"?", "help", 1}}
	}
	if strings.HasPrefix(m.selected, "learned:") {
		return []hint{{"up/down", "select", 1}, {"a", "accept", 1}, {"r", "reject", 1}, {"?", "help", 2}, {"q", "quit", 0}}
	}
	for _, held := range m.snap.Held {
		if held.ID == m.selected {
			return []hint{{"up/down", "select", 1}, {"a", "approve", 1}, {"r", "refuse", 1}, {"?", "help", 2}, {"q", "quit", 0}}
		}
	}
	hs := []hint{{"up/down", "select", 1}, {"enter", "open", 1}, {"m", "msg", 1}, {"/", "filter", 2}, {"tab", m.ctoTitle(), 2}, {"?", "help", 2}, {"q", "quit", 0}}
	if a, ok := m.selectedAgent(); ok {
		if a.Waiting != nil {
			hs[1].label = "answer"
		}
		if a.Worker != nil {
			hs[3] = hint{"x", "stop", 1}
		}
	}
	return hs
}

// helpLine is the key line: as many hints as fit, the least useful dropped first.
// An action result takes the whole line while it is shown, so the screen keeps
// its height and nothing above moves.
func (m Model) helpLine(w int) string {
	if line, ok := m.noticeLine(w); ok {
		return line
	}
	if m.tab == toolsTab && !m.mapOn && !m.help {
		if line, ok := m.toolsHelpLine(w); ok {
			return line
		}
	}
	if m.tab == graphTab && m.mp.finding {
		return clip("find: "+sanitize(m.mp.findQuery)+sDim.Render("  enter focus  esc cancel"), w)
	}
	if m.inboxOn {
		if m.inboxEditing {
			return clip(m.inboxInput.View()+" enter sends, esc cancels", w)
		}
		return sDim.Render(clip("up/down select  enter details  a approve  r refuse  t answer  esc back", w))
	}

	if m.filtering {
		return clip(m.input.View()+sDim.Render("   enter keeps the filter, esc clears it"), w)
	}
	if m.learned {
		return sDim.Render(clip("L or esc: back to the sessions", w))
	}
	switch m.mode {
	case modeCompose:
		return clip(m.compose.View()+sDim.Render("   enter sends, esc cancels"), w)
	case modeForm:
		return sDim.Render(clip("tab or up/down: field  enter: next or start  esc cancels", w))
	case modeQuotaConfirm:
		if m.quotaUnprotect {
			return clip("Unprotect "+m.quotaConfirm+"? workers may use this provider freely. y/n", w)
		}
		return clip("Protect "+m.quotaConfirm+"? y/n", w)
	case modeConfirm:
		return clip(sBold.Render("Stop worker "+m.confirmName+"? y/n"), w)
	case modeProjectRename:
		return clip(m.rename.View()+sDim.Render("   enter saves, empty clears, esc cancels"), w)
	case modeRename:
		return clip(m.rename.View()+sDim.Render("   enter saves, empty resets, esc cancels"), w)
	case modeTrust:
		return clip(sBold.Render("Let "+m.confirmName+" act on requests from your other agents without asking? y/n"), w)
	case modePolicy:
		return sDim.Render(clip("left/right change  up/down or enter field  enter saves  esc cancels", w))
	}
	hs := m.hints()
	text := func() string {
		var parts []string
		for _, h := range hs {
			parts = append(parts, h.key+" "+h.label)
		}
		return strings.Join(parts, "  ")
	}
	for ansi.StringWidth(text()) > w && len(hs) > 1 {
		worst := 0
		for i, h := range hs {
			if h.prio >= hs[worst].prio {
				worst = i
			}
		}
		hs = append(hs[:worst:worst], hs[worst+1:]...)
	}
	var parts []string
	for _, h := range hs {
		parts = append(parts, h.key, h.label)
	}
	return clip(keyHints(parts...), w)
}

// helpContent builds all help lines so none are lost on a small screen.
func (m Model) helpContent(w int) []string {
	state := "on"
	if m.snap.NotificationsDisabled {
		state = "off"
	}

	rows := [][2]string{
		{"Sessions", ""},
		{"proposal", "enter details; a approve; x decline; A approve all after confirmation"},
		{"1-5", "switch between Sessions, Messages, Quotas, Graph and Tools"},
		{"up/down, j/k", "select a session"},
		{"pgup/pgdn, home/end", "page, first, last"},
		{"enter", "enter the selected session (ctrl+] to return)"},
		{"e", "enter the selected session (alias for Enter)"},
		{"m", "send a message to the session as yourself"},
		{"s", "start a worker (a form: project, harness, model, task)"},
		{"x, h, H", "stop worker; h hides/unhides; H shows hidden sessions (dim)"},
		{"t", "cycle request rule: normal, ask, auto"},
		{"p", "delegation policy: subagent or worker for code changes (a form)"},
		{"I", fmt.Sprintf("integrations for all machines: %d checkboxes (a form)", len(protocol.IntegrationSpecs()))},
		{"", ""},
		{"Quotas", ""},
		{"up/down, j/k", "select a subscription"},
		{"p", "protect or unprotect the selected subscription"},
		{"s", "show or hide subscriptions that are not set up"},
		{"r", "refresh the readings"},
		{"", ""},
		{"Graph", ""},
		{"M, G, 4", "open the network"},
		{"arrows", "select a node; shift+arrows or hjkl pan"},
		{"[ ]", "select a linked neighbour"},
		{"/", "find a node; Enter selects the first match; Esc cancels"},
		{"+ -, 0", "zoom; fit the graph"},
		{"o, t, space", "orbit; show traffic; pause traffic"},
		{"enter", "open the selected agent card"},
		{"Agent card", ""},
		{"enter", "open Sessions filtered to this agent's project"},
		{"esc", "return to Graph with the same node selected"},
		{"D", "name project (empty clears)"},
		{"h, H", "hide or unhide the selected project; show hidden projects"},
		{"U, u", "select the next learned change; undo the selected one"},
		{"[ ], ctrl+u/d", "scroll the charter; pgup/pgdn also work"},
		{"c", "focus the " + m.ctoTitle() + " box"},
		{"r", "refresh"},
		{"", ""},
		{"Tools", ""},
		{"[ ]", "switch screens; left/right also switches, except on All tools"},
		{"j/k, h/l", "select an item; h/l also move between columns on All tools"},
		{"enter", "open the selected fix or plan; on All tools, details are below"},
		{"x", "accept a difference as it is"},
		{"a/u", "turn on or turn off for the selected machine or projects"},
		{"m", "change the machine on By project"},
		{"/", "search or filter"},
		{"r", "rescan"},
		{"tab, C", "focus the " + m.ctoTitle() + " box: enter opens the full " + m.ctoTitle() + ", tab returns, esc skips a tip; n new, u undo, m title, harness and model; pgup/pgdn scroll"},
		{"N", "name the machine of the session (every machine sees it; empty resets)"},
		{"L", "what the network learned: playbook notes and their support"},
		{"/", "filter (words match name, project, branch, model, ...)"},
		{"o, d", "show offline sessions; toggle selected session details"},
		{"a", "active only: hide sessions idle for over an hour"},
		{"Messages", "up/down: conversation; left/right: message; enter: latest; v: body; w: workers; e/l: events"},
		{"r", "refresh now"},
		{"states", "messages: pending ... delivered ✓ replied ↩ failed ✗"},
		{"", ""},
		{"Everywhere", ""},
		{"n", "desktop notifications: " + state + " (toggle)"},
		{"T", "turn onboarding tips off or on"},
		{"R", "restart onboarding from the first step"},
		{"?", "this help"},
		{"q, ctrl+c", "quit"},
		{"", ""},
		{"Reading the screen", ""},
		{"CPU", "machine: all cores. session: one core. (x%): 5 min avg"},
		{"red numbers", "a heavy tree: over 20 processes, 200% CPU or 4G"},
		{"shared", "one process many sessions share; its figures are on the machine line"},
		{"dim row", "offline, or idle for more than an hour"},
		{"?", "a figure that is missing or older than 45 seconds"},
		{"└", "a reply, under the message it answers"},
		{"\"...\"", "the first line of the body, when this machine is in the conversation"},
	}
	if m.tab == toolsTab && !m.mapOn {
		rows = [][2]string{
			{"[ ]", "switch screens"},
			{"left/right", "switch screens (on All tools, select a machine or projects)"},
			{"j/k", "select a difference, tool or project"},
			{"enter", "open the selected fix or plan"},
			{"x", "accept a difference as it is"},
			{".", "show accepted differences"},
			{"a/u", "turn on or turn off for the selected machine or projects"},
			{"m", "change the machine on By project"},
			{"/", "search or filter"}, {"r", "rescan"},
			{"tab, C", "focus the " + m.ctoTitle() + " box"},
			{"esc or ?", "close help"},
		}
	}
	var out []string
	const indent = 23
	for _, r := range rows {
		switch {
		case r[0] == "" && r[1] == "":
			out = append(out, "")
		case r[1] == "":
			out = append(out, " "+sBold.Render(r[0]))
		default:
			lines := wrap(r[1], max(w-indent, 8))
			out = append(out, "  "+sTitle.Render(cell(r[0], 20))+" "+lines[0])
			for _, line := range lines[1:] {
				out = append(out, strings.Repeat(" ", indent)+line)
			}
		}
	}
	return out
}

// helpView keeps the heading and reserves rows for scroll indicators.
func (m Model) helpView(w, h int) []string {
	title := "Keys"
	if m.tab == toolsTab && !m.mapOn {
		title = "Tools keys"
	}
	out := []string{section(title, w)}
	lines := m.helpContent(w)
	if len(lines) <= h-1 {
		return append(out, lines...)
	}
	room := max(h-3, 1)
	start := min(max(m.helpScroll, 0), max(len(lines)-room, 0))
	if start > 0 {
		out = append(out, sDim.Render("  more above"))
	} else {
		out = append(out, "")
	}
	end := min(start+room, len(lines))
	out = append(out, lines[start:end]...)
	if end < len(lines) {
		out = append(out, sDim.Render("  more below"))
	} else {
		out = append(out, "")
	}
	return out
}
