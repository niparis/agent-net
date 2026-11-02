package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
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
	case m.settingsMenuOn:
		return []hint{bindingPairHint(contextSettingsMenu, actUp, actDown), bindingHint(contextSettingsMenu, "choose"), bindingHint(contextSettingsMenu, actClose)}
	case m.mapOn && m.focus != focusCTO:
		return []hint{bindingHint(contextCard, "sessions"), bindingHint(contextCard, "node tools"), bindingHint(contextCard, "back"), bindingPairHint(contextCard, actPageUp, actPageDown), bindingHint(contextCard, "new session"), bindingHint(contextCard, "undo learned"), bindingHint(contextCard, "help"), bindingHint(contextCard, actQuit)}
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

// helpContent builds the overlay from keyBindings. The sections follow the
// tabs: Global keys, the current tab, the other tabs in tab order, and the
// overlays last. No key row is written by hand.
func (m Model) helpContent(w int) []string {
	var out []string
	out = append(out, " "+sBold.Render("Global keys"))
	out = append(out, m.globalHelpLines(w)...)

	for _, section := range m.helpTabSections() {
		out = append(out, "")
		out = append(out, " "+sBold.Render(section.title))
		for _, context := range section.contexts {
			if context != section.contexts[0] {
				out = append(out, " "+sBold.Render(strings.ReplaceAll(string(context), "CTO", m.ctoTitle())))
			}
			out = append(out, m.contextHelpLines(context, w, true)...)
		}
		if section.title == "Messages" {
			out = append(out, "  "+sTitle.Render(cell("states", 20))+" pending ... delivered ✓ replied ↩ failed ✗")
		}
		if section.title == "Graph" {
			out = append(out, "")
			out = append(out, mapLegendLines(w)...)
		}
	}

	out = append(out, "", " "+sBold.Render("In overlays"))
	for _, context := range m.overlayContexts() {
		out = append(out, " "+sBold.Render(strings.ReplaceAll(string(context), "CTO", m.ctoTitle())))
		out = append(out, m.contextHelpLines(context, w, false)...)
		if context == contextSettingsMenu {
			for _, entry := range settingsEntries() {
				out = append(out, m.helpRow(entry.label, entry.help, w)...)
			}
		}
	}
	return out
}

type helpSection struct {
	title    string
	contexts []keyContext
}

// helpTabSections puts the current tab first, then the others in tab order.
func (m Model) helpTabSections() []helpSection {
	sections := []helpSection{
		{"Sessions", []keyContext{contextSessions, contextHeld, contextSuggestion, contextOutcome, contextProposal}},
		{"Graph", []keyContext{contextGraph, contextGraphFind, contextCard}},
		{"Quotas", []keyContext{contextQuotas}},
		{"Messages", []keyContext{contextMessages}},
		{"Tools", []keyContext{contextTools, contextToolsDiff, contextToolsAll, contextToolsProject, contextToolsCompare}},
	}
	tab := min(max(m.tab, 0), len(sections)-1)
	ordered := []helpSection{sections[tab]}
	ordered = append(ordered, sections[:tab]...)
	ordered = append(ordered, sections[tab+1:]...)
	return ordered
}

func (m Model) overlayContexts() []keyContext {
	return []keyContext{
		contextBody, contextInbox, contextSettingsMenu, contextProposalDetail, contextProposalHistory,
		contextToolsPlan, contextToolsPicker, contextToolsMenu, contextToolsAdd,
		contextCTO, contextHelp, contextConfirm, contextForm,
	}
}

func (m Model) overlayContext(context keyContext) bool {
	for _, c := range m.overlayContexts() {
		if c == context {
			return true
		}
	}
	return false
}

// globalHelpKeys is the order the shared keys are listed in. The text for
// each group comes from the bindings that use it.
var globalHelpKeys = []struct {
	label string
	keys  []string
}{
	{"1-5", []string{"1", "2", "3", "4", "5"}},
	{"c", []string{"c"}},
	{"C", []string{"C"}},
	{"tab", []string{"tab"}},
	{"?", []string{"?"}},
	{"/", []string{"/"}},
	{"esc", []string{"esc"}},
	{"R", []string{"R"}},
	{"q, ctrl+c", []string{"q", "ctrl+c"}},
	{"enter", []string{"enter"}},
	{"up/down, j/k", []string{"up", "down", "j", "k"}},
	{"left/right", []string{"left", "right"}},
}

func (m Model) globalHelpLines(w int) []string {
	var out []string
	for _, group := range globalHelpKeys {
		var helps []string
		for _, binding := range keyBindings {
			if m.overlayContext(binding.context) {
				continue
			}
			if !bindingUsesAnyKey(binding, group.keys) {
				continue
			}
			helps = appendUnique(helps, m.bindingHelpText(binding))
		}
		// tab is only bound in the forms; the Global list still names it.
		if len(helps) == 0 {
			for _, binding := range keyBindings {
				if bindingUsesAnyKey(binding, group.keys) {
					helps = appendUnique(helps, m.bindingHelpText(binding))
				}
			}
		}
		if len(helps) == 0 {
			continue
		}
		out = append(out, m.helpRow(group.label, strings.Join(helps, "; "), w)...)
	}
	return out
}

func bindingUsesAnyKey(binding keyBinding, keys []string) bool {
	for _, key := range keys {
		if key == " " {
			key = "space"
		}
		for _, k := range binding.keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

func appendUnique(list []string, value string) []string {
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

func (m Model) bindingHelpText(binding keyBinding) string {
	help := binding.help
	if help == "" {
		help = binding.label
	}
	return strings.ReplaceAll(help, "CTO", m.ctoTitle())
}

func (m Model) contextHelpLines(context keyContext, w int, skipGlobal bool) []string {
	var out []string
	for _, binding := range keyBindings {
		if binding.context != context {
			continue
		}
		if skipGlobal {
			global := false
			for _, group := range globalHelpKeys {
				if bindingUsesAnyKey(binding, group.keys) {
					global = true
					break
				}
			}
			if global {
				continue
			}
		}
		help := m.bindingHelpText(binding)
		if context == contextHelp && binding.action == actNotifications {
			state := "on"
			if m.snap.NotificationsDisabled {
				state = "off"
			}
			help = "desktop notifications: " + state + " (toggle)"
		}
		keys := strings.Join(binding.keys, ", ")
		if ansi.StringWidth(keys) <= 20 {
			out = append(out, m.helpRow(keys, help, w)...)
			continue
		}
		for _, key := range binding.keys {
			out = append(out, m.helpRow(key, help, w)...)
		}
	}
	return out
}

func (m Model) helpRow(keys, help string, w int) []string {
	const indent = 23
	lines := wrap(help, max(w-indent, 8))
	out := []string{"  " + sTitle.Render(cell(keys, 20)) + " " + lines[0]}
	for _, line := range lines[1:] {
		out = append(out, strings.Repeat(" ", indent)+line)
	}
	return out
}

// openHelp shows the overlay at its first line.
func (m *Model) openHelp() {
	m.help = true
	m.helpScroll = 0
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
