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
	cto := bindingHint(contextGlobal, "open CTO")
	cto.label, cto.prio = m.ctoTitle(), 2
	sessionHelp, sessionQuit := bindingHint(contextSessions, "help"), bindingHint(contextSessions, actQuit)
	switch {
	case m.proposalConfirm:
		yes, no := bindingHint(contextConfirm, "yes"), bindingHint(contextConfirm, "no")
		yes.key, no.key = "enter/y", "esc/n"
		return []hint{bindingPairHint(contextProposalHistory, actUp, actDown), yes, no}
	case m.proposalHistory && m.proposalDetail == nil && !m.help:
		return []hint{bindingPairHint(contextProposalHistory, actUp, actDown), bindingHint(contextProposalHistory, "detail"), bindingHint(contextProposalHistory, "back"), bindingHint(contextProposalHistory, "help")}
	case m.proposalDetail != nil && !m.help:
		if m.proposalDetail.proposal.Status != "open" {
			return []hint{bindingPairHint(contextProposalDetail, actUp, actDown), bindingHint(contextProposalDetail, actClose), bindingHint(contextProposalDetail, "help")}
		}
		return []hint{bindingHint(contextProposalDetail, "approve"), bindingHint(contextProposalDetail, "decline"), bindingHint(contextProposalDetail, "approve all"), bindingHint(contextProposalDetail, actClose), bindingHint(contextProposalDetail, "help")}
	case m.help:
		return []hint{bindingPairHint(contextHelp, actUp, actDown), bindingHint(contextHelp, actClose), bindingHint(contextHelp, actQuit)}
	case m.mapOn && m.focus != focusCTO:
		return []hint{bindingHint(contextCard, "sessions"), bindingHint(contextCard, "node tools"), bindingHint(contextCard, "back"), bindingPairHint(contextCard, actPageUp, actPageDown), bindingHint(contextCard, "new session"), bindingHint(contextCard, "undo learned"), bindingHint(contextCard, "legend"), bindingHint(contextCard, "help"), bindingHint(contextCard, actQuit)}
	case m.open != nil:
		return []hint{bindingPairHint(contextBody, actUp, actDown), bindingHint(contextBody, actClose), bindingHint(contextBody, actQuit)}
	case m.focus == focusCTO:
		open := bindingHint(contextCTO, "open CTO")
		open.label = "open " + m.ctoTitle()
		return []hint{open, bindingHint(contextCTO, "back"), bindingHint(contextCTO, "model"), bindingHint(contextCTO, "new conversation"), bindingHint(contextCTO, "undo")}
	case m.tab == messagesTab:
		move := bindingPairHint(contextMessages, actUp, actDown)
		msg := bindingPairHint(contextMessages, "previous message", "next message")
		if m.selectedWorkerConversation() {
			return []hint{move, msg, bindingHint(contextMessages, "message body"), bindingHint(contextMessages, "defect"), cto, bindingHint(contextMessages, "help"), bindingHint(contextMessages, actQuit)}
		}
		return []hint{move, msg, bindingHint(contextMessages, "message body"), bindingHint(contextMessages, actLast), cto, bindingHint(contextMessages, "help"), bindingHint(contextMessages, actQuit)}
	case m.tab == quotasTab:
		return []hint{bindingPairHint(contextQuotas, actUp, actDown), bindingHint(contextQuotas, "protect"), bindingHint(contextQuotas, "show unset"), bindingHint(contextQuotas, "refresh"), cto, bindingHint(contextQuotas, "help"), bindingHint(contextQuotas, actQuit)}
	case m.tab == graphTab:
		tabs := hint{key: bindingHint(contextGlobal, "sessions tab").key + "-" + bindingHint(contextGlobal, "tools tab").key, label: "tabs", prio: 1}
		return []hint{tabs, cto, bindingHint(contextGraph, "help"), bindingHint(contextGraph, actQuit)}
	case m.tab == toolsTab:
		return m.toolsHints()
	}
	for _, e := range m.snap.Outcomes {
		if m.selected == "outcome:"+e.ReplyID {
			return []hint{bindingHint(contextOutcome, "let stand"), bindingHint(contextOutcome, "overrule"), sessionHelp, sessionQuit}
		}
	}
	if _, ok := m.selectedProposal(); ok {
		return []hint{bindingHint(contextProposal, "detail"), bindingHint(contextProposal, "approve"), bindingHint(contextProposal, "decline"), bindingHint(contextProposal, "approve all"), sessionHelp}
	}
	if strings.HasPrefix(m.selected, "learned:") {
		return []hint{bindingPairHint(contextSessions, actUp, actDown), bindingHint(contextSuggestion, "accept"), bindingHint(contextSuggestion, "reject"), sessionHelp, sessionQuit}
	}
	for _, held := range m.snap.Held {
		if held.ID == m.selected {
			return []hint{bindingPairHint(contextSessions, actUp, actDown), bindingHint(contextHeld, "approve"), bindingHint(contextHeld, "refuse"), sessionHelp, sessionQuit}
		}
	}
	hs := []hint{bindingPairHint(contextSessions, actUp, actDown), bindingHint(contextSessions, "open session"), bindingHint(contextSessions, "compose"), bindingHint(contextSessions, "new session"), cto, sessionHelp, sessionQuit}
	if a, ok := m.selectedAgent(); ok {
		if a.Waiting != nil {
			hs[1].label = "answer"
		}
		if a.Worker != nil {
			hs[3] = bindingHint(contextSessions, "stop worker")
			hs[3].prio = 1
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
	if m.focus == focusCTO && m.cto != nil && m.cto.settingsForm != nil {
		if m.cto.settingsForm.err != "" {
			return clip(sBad.Render(m.cto.settingsForm.err)+sDim.Render("  up/down field  enter save  esc cancel"), w)
		}
		return sDim.Render(clip("up/down field  left/right harness  tab field  enter save  esc cancel", w))
	}
	if m.learned {
		return sDim.Render(clip("L or esc: back to the sessions", w))
	}
	switch m.mode {
	case modeCompose:
		return clip(m.compose.View()+sDim.Render("   enter sends, esc cancels"), w)
	case modeSessionForm:
		return sDim.Render(clip("up/down field · left/right change · enter start · esc cancel", w))
	case modeQuotaConfirm:
		if m.quotaUnprotect {
			return confirmLine("Unprotect "+sanitize(m.quotaConfirm)+"?", w)
		}
		return confirmLine("Protect "+sanitize(m.quotaConfirm)+"?", w)
	case modeConfirm:
		if m.confirmDefect {
			return clip(sBold.Render(confirmName(fmt.Sprintf("Report a severity %d defect found later on ", m.defectLevel), m.defectName, "?", w)), w)
		}
		if m.confirmOwner {
			return clip(sBold.Render(confirmName("Stop ", m.confirmName, "? Conversation kept: Enter resumes it.", w)), w)
		}
		return clip(sBold.Render(confirmName("Stop worker ", m.confirmName, "?", w)), w)
	case modeDefectNote:
		return clip(m.defect.View()+sDim.Render("   enter asks for severity, esc cancels"), w)
	case modeDefectSeverity:
		return clip(m.defectSeverity.View()+sDim.Render("   enter asks to confirm, esc cancels"), w)
	case modeProjectRename:
		return clip(m.rename.View()+sDim.Render("   enter saves, empty clears, esc cancels"), w)
	case modeRename:
		return clip(m.rename.View()+sDim.Render("   enter saves, empty resets, esc cancels"), w)
	case modeTrust:
		return clip(sBold.Render(confirmName("Let ", m.confirmName, " act on peer requests without asking?", w)), w)
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
		{"1-5", "switch between Sessions, Graph, Quotas, Messages and Tools"},
		{"up/down, j/k", "select a session"},
		{"pgup/pgdn, home/end", "page, first, last"},
		{"enter", "open session in herdr (ctrl+] returns on fallback)"},
		{"m", "send a message to the session as yourself"},
		{"n", "start a session in herdr: project, harness, model, folder and first message (a form). Empty first message starts it and enters it; text starts it in the background and sends it."},
		{"x", "stop the selected worker or session"},
		{"h, H", "hide or unhide the session; show hidden sessions (dim)"},
		{"t", "cycle request rule: normal, ask, auto"},
		{"p", "delegation policy: subagent or worker for code changes (a form)"},
		{"I", fmt.Sprintf("integrations for all machines: %d checkboxes (a form)", len(protocol.IntegrationSpecs()))},
		{"c, C", "c opens the " + m.ctoTitle() + "; C focuses its box: enter opens the full " + m.ctoTitle() + ", ctrl+] returns, esc skips a tip; n new, u undo, w drive 30m, m title, harness and model; pgup/pgdn scroll"},
		{"N", "name the machine of the session (every machine sees it; empty resets)"},
		{"L", "what the network learned: playbook notes and their support"},
		{"/", "filter (words match name, project, branch, model, ...)"},
		{"o, d", "show offline sessions; toggle the selected session details"},
		{"A", "active only: hide sessions idle for over an hour"},
		{"R", "refresh now"},
		{"proposal", "enter details; a approve; r decline; A approve all after confirmation"},
		{"", ""},
		{"Messages", ""},
		{"up/down", "select a conversation"},
		{"left/right", "select a message in the conversation"},
		{"enter", "open the selected message body"},
		{"end", "select the latest message"},
		{"f", "on a worker conversation: report a defect found later"},
		{"w", "show or hide worker threads"},
		{"e, l", "show system events; cycle the event filter"},
		{"R", "refresh now"},
		{"states", "pending ... delivered ✓ replied ↩ failed ✗"},
		{"", ""},
		{"Quotas", ""},
		{"up/down, j/k", "select a subscription and scroll the panels"},
		{"p", "protect or unprotect the selected subscription"},
		{"s", "show or hide subscriptions that are not set up"},
		{"left/right", "cycle the Quality panel agent: all agents, then each caller"},
		{"d", "show or hide the Quality panel detail (fixes and continues)"},
		{"R", "refresh the readings"},
		{"", ""},
		{"Graph", ""},
		{"2", "open the Graph tab"},
		{"arrows", "select a node; shift+arrows pan"},
		{", .", "select a linked neighbour"},
		{"/", "find a node; Enter selects the first match; Esc cancels"},
		{"+ -, 0", "zoom; fit the graph"},
		{"o, space", "orbit; pause traffic"},
		{"t", "open Tools filtered to the selected graph node"},
		{"enter", "open the selected agent card"},
		{"Agent card", ""},
		{"enter", "open Sessions filtered to this agent's project"},
		{"n", "start a new session in this project (the same form, filled from here)"},
		{"esc", "return to Graph with the same node selected"},
		{"D", "name project (empty clears)"},
		{"a", "toggle autonomy off or wake"},
		{"h, H", "hide or unhide the selected project; show hidden projects"},
		{", ., u", "select a learned change; undo the selected one"},
		{"!", "legend: what the edge kinds, request rules and charter fields mean"},
		{"ctrl+u/d", "scroll the card: legend, charter, rules and learned changes; pgup/pgdn also work"},
		{"c", "focus the " + m.ctoTitle() + " box"},
		{"R", "refresh"},
		{"", ""},
		{"Tools", ""},
		{"[ ]", "switch between Differences, All tools and By project"},
		{"j/k, up/down", "move through the selected Tools screen"},
		{"left/right", "move between columns"},
		{"enter", "open the selected difference plan or the selected tool's details and actions"},
		{"a, u", "accept a difference or turn a tool on; turn a tool off"},
		{"v", "next machine on By project"},
		{"/", "combine text, project:NAME, node:NAME and machine:NAME filters"},
		{"n", "add an MCP server or a skill from a folder"},
		{"esc", "back, or clear the list filter"},
		{"", ""},
		{"Everywhere", ""},
		{"N", "desktop notifications: " + state + " (toggle)"},
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
			{"[ ]", "switch between Differences, All tools and By project"},
			{"j/k, up/down", "move through tools, differences or projects; scroll a read view or comparison"},
			{"left/right", "move between columns"},
			{"enter", "open the selected difference plan or the selected tool's details and actions"},
			{"a", "accept a difference on Differences; turn on a tool on All tools"},
			{"u", "turn off the selected tool on All tools"},
			{"v", "select the next machine on By project"},
			{"/", "filter: text project:NAME node:NAME machine:NAME (combine them)"},
			{"n", "add an MCP server or a skill from a folder"},
			{"R", "rescan tool copies"},
			{"space", "tick a machine or project in the picker"},
			{"a/b/...", "in compare only: keep the labelled copy"},
			{"n and p, v", "in compare only: select a file, view its diff"},
			{"tab", "in add form: next field; arrows choose machine"},
			{"esc", "back or cancel; clear a nonempty filter first"},
			{"c, C", "c opens the " + m.ctoTitle() + "; C focuses its box"},
			{"q", "quit from the list"},
			{"esc or ?", "close help"},
		}
	}
	rows = append(rows, [2]string{"", ""}, [2]string{"Bindings", ""})
	for _, context := range allKeyContexts {
		rows = append(rows, [2]string{strings.ReplaceAll(string(context), "CTO", m.ctoTitle()), ""})
		for _, binding := range keyBindings {
			if binding.context != context {
				continue
			}
			label := strings.ReplaceAll(binding.label, "CTO", m.ctoTitle())
			keys := strings.Join(binding.keys, ", ")
			if ansi.StringWidth(keys) <= 20 {
				rows = append(rows, [2]string{keys, label})
				continue
			}
			for _, key := range binding.keys {
				rows = append(rows, [2]string{key, label})
			}
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
