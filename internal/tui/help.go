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
	case m.mp.removal != nil:
		return []hint{{key: "enter/y", label: "yes"}, {key: "esc/n", label: "no"}}
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
		toggle := bindingHint(contextHelp, actAllScreens)
		if m.helpAll {
			toggle.label = "this screen"
		}
		return []hint{bindingPairHint(contextHelp, actUp, actDown), toggle, bindingHint(contextHelp, actClose)}
	case m.mode == modeTeams:
		return []hint{
			bindingPairHint(contextTeams, actUp, actDown),
			bindingHint(contextTeams, "show team"),
			bindingHint(contextTeams, "create team"),
			bindingHint(contextTeams, "rename team"),
			bindingHint(contextTeams, "edit purpose"),
			bindingHint(contextTeams, "archive team"),
			bindingHint(contextTeams, "member"),
			bindingHint(contextTeams, "lead"),
			bindingHint(contextTeams, "back"),
			bindingHint(contextTeams, "help"),
		}
	case m.mode == modeApps:
		switch m.apps.stage {
		case "url":
			return []hint{{key: "enter", label: "save URL"}, bindingHint(contextApps, "back")}
		case "code":
			return []hint{{key: "enter", label: "look up"}, bindingHint(contextApps, "back")}
		case "approve":
			kind := bindingHint(contextApps, "kind")
			kind.key, kind.label = "left/right", "kind"
			name := hint{key: "up/down", label: "kind/name"}
			back := bindingHint(contextApps, "back")
			back.label = "cancel"
			return []hint{kind, name, bindingHint(contextApps, "approve"), bindingHint(contextApps, "refuse"), back}
		case "confirm", "public-confirm":
			return []hint{bindingHint(contextConfirm, "yes"), bindingHint(contextConfirm, "no")}
		default:
			return []hint{bindingHint(contextApps, "public"), bindingHint(contextApps, "url"), bindingHint(contextApps, "add"), bindingHint(contextApps, "revoke"), bindingHint(contextApps, "back")}
		}
	case m.mapOn && m.focus != focusCTO:
		hs := []hint{bindingHint(contextCard, "sessions"), bindingHint(contextCard, "node tools")}
		if node, ok := m.mapNode(m.mp.selected); ok && node.Ref != nil && node.Ref.Kind == "repo" {
			hs = append(hs, bindingHint(contextCard, "lead"))
		}
		return append(hs, bindingHint(contextCard, "back"), bindingPairHint(contextCard, actPageUp, actPageDown), bindingHint(contextCard, "new session"), bindingHint(contextCard, "undo learned"), bindingHint(contextCard, "help"), bindingHint(contextCard, actQuit))
	case m.open != nil:
		return []hint{bindingPairHint(contextBody, actUp, actDown), bindingHint(contextBody, actClose), bindingHint(contextBody, actQuit)}
	case m.focus == focusCTO:
		open := bindingHint(contextCTO, "open CTO")
		open.label = "open " + m.ctoTitle()
		return []hint{open, bindingHint(contextCTO, "back"), bindingHint(contextCTO, "model"), bindingHint(contextCTO, "new conversation"), bindingHint(contextCTO, "undo")}
	case m.tab == inboxTab:
		return m.inboxHints()
	case m.tab == messagesTab:
		move := bindingPairHint(contextMessages, actUp, actDown)
		msg := bindingPairHint(contextMessages, "previous message", "next message")
		if m.selectedWorkerConversation() {
			return []hint{move, msg, bindingHint(contextMessages, "message body"), bindingHint(contextMessages, "defect"), cto, bindingHint(contextMessages, "help"), bindingHint(contextMessages, actQuit)}
		}
		return []hint{move, msg, bindingHint(contextMessages, "message body"), bindingHint(contextMessages, actLast), cto, bindingHint(contextMessages, "help"), bindingHint(contextMessages, actQuit)}
	case m.tab == quotasTab && m.qualityDetail:
		return []hint{bindingPairHint(contextQualityDetails, actUp, actDown), bindingPairHint(contextQualityDetails, actPageUp, actPageDown), bindingPairHint(contextQualityDetails, "previous agent", "next agent"), bindingHint(contextQualityDetails, "sub tab")}
	case m.tab == deliveryTab:
		return m.deliveryHints()
	case m.tab == quotasTab && m.workersLog:
		if m.workerLogDetailsFocus {
			return []hint{{key: "tab", label: "sub tab"}, {key: "↑↓", label: "scroll details"}, {key: "←", label: "runs"}, sessionHelp, sessionQuit}
		}
		return []hint{{key: "tab", label: "sub tab"}, {key: "↑↓", label: "select run"}, {key: "→", label: "details"}, sessionHelp, sessionQuit}
	case m.tab == quotasTab:
		return []hint{bindingPairHint(contextQuotas, actUp, actDown), bindingHint(contextQuotas, "protect"), bindingHint(contextQuotas, "show unset"), cto, bindingHint(contextQuotas, "help"), bindingHint(contextQuotas, actQuit)}
	case m.tab == settingsTab:
		item := m.settingsItems()[min(max(m.settingsRow, 0), len(m.settingsItems())-1)]
		if item.kind == "machine" {
			return []hint{bindingPairHint(contextSettings, actUp, actDown), {key: "tab/[ ]", label: "section"}, bindingHint(contextSettings, "open"), {key: "x", label: "remove", prio: 1}, bindingHint(contextSettings, "rotate token"), bindingHint(contextSettings, "help"), bindingHint(contextSettings, actQuit)}
		}
		if m.settingsItems()[min(max(m.settingsRow, 0), len(m.settingsItems())-1)].section == 0 {
			return []hint{bindingPairHint(contextSettings, actUp, actDown), {key: "tab/[ ]", label: "section"}, bindingHint(contextSettings, "open"), bindingHint(contextSettings, "toggle"), bindingHint(contextSettings, "public"), bindingHint(contextSettings, "url"), bindingHint(contextSettings, "add"), bindingHint(contextSettings, "revoke"), bindingHint(contextSettings, "help"), bindingHint(contextSettings, actQuit)}
		}
		return []hint{bindingPairHint(contextSettings, actUp, actDown), {key: "tab/[ ]", label: "section"}, bindingHint(contextSettings, "open"), bindingHint(contextSettings, "toggle"), bindingHint(contextSettings, "help"), bindingHint(contextSettings, actQuit)}
	case m.tab == graphTab:
		tabs := hint{key: bindingHint(contextGlobal, "sessions tab").key + "-" + bindingHint(contextGlobal, "delivery tab").key, label: "tabs", prio: 1}
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

	if m.tab == sessionsTab && m.sessionCell > 0 {
		if a, ok := m.selectedAgent(); ok && !protocol.IsOwner(a.Harness) && a.Harness != "bot" {
			if m.sessionCell == 3 && canChangeSessionAgent(a) {
				return []hint{{key: "left/right", label: "cell"}, {key: "space", label: "next agent"}, {key: "esc", label: "name"}, sessionHelp, sessionQuit}
			}
			if m.sessionCell == 1 {
				rule := m.wildcardRule(firstNonEmpty(a.Repo.Project, a.Profile.Project))
				next := "ask"
				if rule == "ask" {
					next = "auto"
				} else if rule == "auto" {
					next = "normal"
				}
				return []hint{{key: "left/right", label: "cell"}, {key: "space", label: rule + " -> " + next}, {key: "esc", label: "name"}, sessionHelp, sessionQuit}
			}
			return []hint{{key: "left/right", label: "cell"}, {key: "enter", label: "delegation policy"}, {key: "esc", label: "name"}, sessionHelp, sessionQuit}
		}
	}
	pin := bindingHint(contextSessions, "pin")
	pin.prio = 1
	hs := []hint{bindingPairHint(contextSessions, actUp, actDown), bindingHint(contextSessions, "open session"), bindingHint(contextSessions, "compose"), {key: "x", label: "stop or remove", prio: 1}, {key: "X", label: "remove all offline", prio: 2}, pin, sessionHelp, sessionQuit}
	if a, ok := m.selectedAgent(); ok {
		if a.Waiting != nil {
			hs[1].label = "answer"
		}
		if a.PinnedAt > 0 {
			hs[5].label = "unpin"
		}
		if a.Harness == "bot" && a.Online {
			hs[1].label = "messages"
			hs[3] = hint{key: "x", label: "revoke", prio: 1}
		}
	}

	return hs
}

// helpLine is the key line: as many hints as fit, the least useful dropped first.
// An action result takes the whole line while it is shown, so the screen keeps
// its height and nothing above moves.
func (m Model) helpLine(w int) string {
	if m.finder {
		return clip(keyHints("type", "to search", "up/down", "select", "enter", "open", "esc", "close"), w)
	}
	if m.archive != nil {
		return clip(m.sessionsArchiveHints(), w)
	}
	if m.tab == toolsTab && !m.mapOn && !m.help {
		if line, ok := m.toolsHelpLine(w); ok {
			return line
		}
	}
	if line, ok := m.noticeLine(w); ok {
		return line
	}
	if m.tab == graphTab && m.mp.finding {
		return clip("find: "+sanitize(m.mp.findQuery)+sDim.Render("  enter focus  esc cancel"), w)
	}
	if m.tab == inboxTab && m.inboxEditing {
		return clip(m.inboxInput.View()+" enter sends, esc cancels", w)
	}
	if m.tab == deliveryTab && m.delivery.reporting {
		return clip(m.delivery.reportInput.View()+" enter sends, esc cancels", w)
	}

	if m.filtering {
		return clip(m.input.View()+sDim.Render("   enter keeps the filter, esc clears and leaves"), w)
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
	case modeLeadPicker:
		return sDim.Render(clip("up/down select · enter save · esc cancel", w))
	case modeTeamForm:
		return sDim.Render(clip("up/down field · enter saves or moves on · esc cancels", w))
	case modeQuotaConfirm:
		target := sanitize(m.quotaConfirm)
		if m.quotaConfirmDisplay != "" && !strings.EqualFold(m.quotaConfirmDisplay, m.quotaConfirm) {
			target = sanitize(m.quotaConfirmDisplay) + " (" + target + ")"
		}
		if m.quotaUnprotect {
			return confirmLine("Unprotect "+target+"?", w)
		}
		return confirmLine("Protect "+target+"?", w)
	case modeQuotaFloor:
		return clip(m.rename.View()+sDim.Render("   enter saves, esc cancels, 0 = off"), w)
	case modeConfirm:
		if m.confirmMachine != "" {
			prefix := "Remove machine "
			suffix := " and revoke its token?"
			if m.confirmMachine == protocol.OpMachineRotate {
				prefix = "Rotate token for "
				suffix = "?"
			}
			return clip(sBold.Render(confirmName(prefix, sanitize(m.confirmName), suffix, w)), w)
		}
		if m.confirmDefect {
			return clip(sBold.Render(confirmName(fmt.Sprintf("Report a severity %d defect found later on ", m.defectLevel), m.defectName, "?", w)), w)
		}
		if len(m.removeIDs) > 0 {
			if m.removeAll {
				return clip(sBold.Render(fmt.Sprintf("Remove all %d offline sessions shown? Conversations stay on disk.", len(m.removeIDs))), w)
			}
			return clip(sBold.Render("Remove "+clip(m.confirmName, max(0, w-len("Remove ")-len(" from agent-net for good? The conversation stays on disk.")))+" from agent-net for good? The conversation stays on disk."), w)
		}
		if m.confirmOwner {
			if m.confirmClose {
				return clip(sBold.Render(confirmNameWithHint("Close ", m.confirmName, "? Conversation kept.", "enter/y: close · esc/n: keep", w)), w)
			}
			if m.confirmTerminal {
				return clip(sBold.Render(confirmNameWithHint("End ", m.confirmName, " in your terminal? Conversation kept.", stopConfirmHint, w)), w)
			}
			return clip(sBold.Render(confirmNameWithHint("Stop ", m.confirmName, "? Conversation kept.", stopConfirmHint, w)), w)
		}
		return clip(sBold.Render(confirmName("Stop worker ", m.confirmName, "?", w)), w)
	case modeDefectNote:
		return clip(m.defect.View()+sDim.Render("   enter asks for severity, esc cancels"), w)
	case modeDefectSeverity:
		return clip(m.defectSeverity.View()+sDim.Render("   enter asks to confirm, esc cancels"), w)
	case modeProjectRename:
		return clip(m.rename.View()+sDim.Render("   enter saves, empty clears, esc cancels"), w)
	case modeSettingUndo:
		return clip(sBold.Render(fmt.Sprintf("Undo all changes in revision %d? Enter confirms, esc cancels.", m.settingUndoRevision)), w)
	case modeSettingRepo:
		return clip(m.rename.View()+sDim.Render("   enter selects, empty selects network, esc cancels"), w)
	case modeSettingValue:
		return clip(m.rename.View()+sDim.Render("   enter saves JSON, esc cancels"), w)
	case modeTimeZone:
		return clip(m.rename.View()+sDim.Render("   enter saves, auto follows your machine, esc cancels"), w)
	case modeRename:
		return clip(m.rename.View()+sDim.Render("   enter saves, empty resets, esc cancels"), w)
	case modeTrust:
		return clip(sBold.Render(confirmName("Let ", m.confirmName, " act on peer requests without asking?", w)), w)
	case modePolicy:
		return sDim.Render(clip("left/right change  up/down or enter field  enter saves  esc cancels", w))
	}
	hs := m.hints()
	if w <= 80 {
		for i := range hs {
			if hs[i].key == "enter" && hs[i].label == "open (ctrl+] back)" {
				hs[i].label = "open"
			}
		}
	}
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

// helpContent builds the overlay from keyBindings. The current screen comes
// first, then the fixed Global keys. The full list keeps the old order: Global
// keys, the current tab, the other tabs in tab order, and the overlays last.
// No key row is written by hand.
func (m Model) helpContent(w int) []string {
	if m.helpAll {
		return m.helpAllContent(w)
	}
	out := m.helpSectionContent(m.currentHelpSection(), w)
	out = append(out, "", " "+sBold.Render("Global keys"))
	out = append(out, m.globalHelpLines(w)...)
	return out
}

func (m Model) helpAllContent(w int) []string {
	var out []string
	out = append(out, " "+sBold.Render("Global keys"))
	out = append(out, m.globalHelpLines(w)...)

	for _, section := range m.helpTabSections() {
		out = append(out, "")
		out = append(out, m.helpSectionContent(section, w)...)
	}

	out = append(out, "", " "+sBold.Render("In overlays"))
	for i, context := range m.overlayContexts() {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, " "+sBold.Render(m.helpSubsectionTitle(context)))
		out = append(out, m.contextHelpLines(context, w, false)...)
	}
	return out
}

func (m Model) helpSectionContent(section helpSection, w int) []string {
	out := []string{" " + sBold.Render(section.title)}
	skipGlobal := !m.overlayContext(section.contexts[0])
	for _, context := range section.contexts {
		if context != section.contexts[0] {
			out = append(out, "", " "+sBold.Render(m.helpSubsectionTitle(context)))
		}
		out = append(out, m.contextHelpLines(context, w, skipGlobal)...)
	}
	if section.title == "Messages" {
		out = append(out, "  "+sTitle.Render(cell("states", 20))+" pending ... delivered ✓ replied ↩ failed ✗")
	}
	if section.title == "Sessions" {
		out = append(out, m.helpRow("◐", "runs in the background: no terminal is attached", w)...)
		out = append(out, m.helpRow("★", "repo or team lead; dim when no lead session is live", w)...)
	}
	if section.title == "Graph" {
		out = append(out, "")
		out = append(out, m.helpRow("live state", "needs you: highlighted; busy: bright; idle: normal; offline: dim", w)...)
		out = append(out, m.helpRow("★", "team lead; each box lists the team members", w)...)
		out = append(out, m.helpRow("dormant", "no online session or activity in 30 days, archived, or a temporary name", w)...)
		out = append(out, mapLegendLines(w)...)
	}
	return out
}

type helpSection struct {
	title    string
	contexts []keyContext
}

// currentHelpSection names the screen the owner was on: an open overlay or
// form when the key table has a context for it, else the current tab.
func (m Model) currentHelpSection() helpSection {
	switch {
	case m.proposalConfirm:
		return m.helpContextSection(contextConfirm)
	case m.proposalHistory:
		return m.helpContextSection(contextProposalHistory)
	case m.proposalDetail != nil:
		return m.helpContextSection(contextProposalDetail)
	case m.mode == modeApps:
		return m.helpContextSection(contextApps)
	case m.mode == modeTeams:
		return m.helpContextSection(contextTeams)
	case m.mode == modeConfirm, m.mode == modeQuotaConfirm, m.mode == modeTrust:
		return m.helpContextSection(contextConfirm)
	case m.mode != modeNormal:
		return m.helpContextSection(contextForm)
	case m.finder:
		return m.helpContextSection(contextFinder)
	case m.qualityDetail && m.tab == quotasTab:
		return m.helpContextSection(contextQualityDetails)
	case m.focus == focusCTO && m.cto != nil:
		return m.helpContextSection(contextCTO)
	case m.open != nil:
		return m.helpContextSection(contextBody)
	case m.tab == deliveryTab:
		return m.helpContextSection(contextDelivery)
	case m.tab == quotasTab && m.workersLog:
		return m.helpContextSection(contextWorkersLog)
	case m.mapOn:
		return m.helpContextSection(contextCard)
	case m.tab == graphTab && m.mp.finding:
		return m.helpContextSection(contextGraphFind)
	case m.tab == toolsTab && m.tl.modal != nil:
		return m.toolsHelpSection()
	case m.tab == toolsTab && !m.tl.allTools:
		return helpSection{title: "Tools", contexts: []keyContext{m.agentToolsContext()}}
	}
	return helpTabSection(m.tab)
}

func (m Model) helpContextSection(context keyContext) helpSection {
	return helpSection{title: m.helpSubsectionTitle(context), contexts: []keyContext{context}}
}

// toolsHelpSection names the Tools overlay on screen.
func (m Model) toolsHelpSection() helpSection {
	md := m.tl.modal
	context := contextToolsPlan
	switch {
	case md.menu != nil:
		context = contextToolsMenu
	case md.form != nil:
		context = contextToolsAdd
	case md.picker != nil && !md.loading:
		context = contextToolsPicker
	}
	return m.helpContextSection(context)
}

func helpSections() []helpSection {
	return []helpSection{
		{"Sessions", []keyContext{contextSessions}},
		{"Graph", []keyContext{contextGraph, contextGraphFind, contextCard}},
		{"Quotas", []keyContext{contextQuotas, contextWorkersLog}},
		{"Messages", []keyContext{contextMessages}},
		{"Tools", []keyContext{contextToolsAgents, contextToolsSkills, contextTools}},
		{"Settings", []keyContext{contextSettings}},
		{"Inbox", []keyContext{contextInbox, contextHeld, contextSuggestion, contextOutcome, contextProposal}},
		{"Delivery", []keyContext{contextDelivery}},
	}
}

func helpTabSection(tab int) helpSection {
	sections := helpSections()
	return sections[min(max(tab, 0), len(sections)-1)]
}

// helpTabSections puts the current tab first, then the others in tab order.
func (m Model) helpTabSections() []helpSection {
	sections := helpSections()
	tab := min(max(m.tab, 0), len(sections)-1)
	ordered := []helpSection{sections[tab]}
	ordered = append(ordered, sections[:tab]...)
	ordered = append(ordered, sections[tab+1:]...)
	return ordered
}

func (m Model) overlayContexts() []keyContext {
	return []keyContext{
		contextBody, contextApps, contextQualityDetails, contextProposalDetail, contextProposalHistory,
		contextToolsPlan, contextToolsPicker, contextToolsMenu, contextToolsAdd, contextToolsDiscard, contextToolsFilter, contextToolsCosts,
		contextCTO, contextHelp, contextConfirm, contextForm, contextTeams, contextFinder,
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

func (m Model) globalHelpLines(w int) []string {
	out := make([]string, 0, len(globalHelpRows))
	for _, row := range globalHelpRows {
		out = append(out, m.helpRow(row.keys, strings.ReplaceAll(row.help, "CTO", m.ctoTitle()), w)...)
	}
	return out
}

func (m Model) helpSubsectionTitle(context keyContext) string {
	switch context {
	case contextHeld:
		return "On a held request row"
	case contextSuggestion:
		return "On a learned suggestion row"
	case contextOutcome:
		return "On an outcome row"
	case contextProposal:
		return "On a " + m.ctoTitle() + " proposal row"
	case contextCard:
		return "Agent card"
	case contextBody:
		return "Message body"
	case contextInbox:
		return "Inbox"
	case contextApps:
		return "Connected apps"
	case contextProposalDetail:
		return "Proposal detail"
	case contextProposalHistory:
		return "Proposal history"
	case contextToolsPlan:
		return "Tools plan"
	case contextToolsPicker:
		return "Tools picker"
	case contextToolsMenu:
		return "Tools menu"
	case contextToolsAdd:
		return "Tools add"
	case contextCTO:
		return m.ctoTitle() + " box"
	case contextHelp:
		return "Help"
	case contextConfirm:
		return "Confirmations"
	case contextForm:
		return "Forms"
	case contextTeams:
		return "Teams"
	case contextFinder:
		return "Session finder"
	}
	return strings.ReplaceAll(string(context), "CTO", m.ctoTitle())
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

func (m Model) bindingHelpText(binding keyBinding) string {
	help := binding.help
	if help == "" {
		help = binding.label
	}
	return strings.ReplaceAll(help, "CTO", m.ctoTitle())
}

type helpEntry struct {
	keys   []string
	help   string
	action actionID
}

func (m Model) contextHelpLines(context keyContext, w int, skipGlobal bool) []string {
	var entries []helpEntry
	for _, binding := range keyBindings {
		if context == contextApps && binding.action == "back" {
			continue
		}
		if binding.context != context {
			continue
		}
		if skipGlobal && !binding.local && bindingUsesAnyKey(binding, globalSameKeys) {
			continue
		}
		help := m.bindingHelpText(binding)
		entries = append(entries, helpEntry{keys: append([]string(nil), binding.keys...), help: help, action: binding.action})
	}
	entries = mergeHelpEntries(entries)
	var out []string
	for _, entry := range entries {
		keys := strings.Join(entry.keys, ", ")
		if ansi.StringWidth(keys) <= helpKeyWidth {
			out = append(out, m.helpRow(keys, entry.help, w)...)
			continue
		}
		for _, key := range entry.keys {
			out = append(out, m.helpRow(key, entry.help, w)...)
		}
	}
	return out
}

// mergeHelpEntries puts a direction pair on one row: pgup/pgdn, up/down,
// left/right, shift+arrows, and first/last. A pair shares one help text.
func mergeHelpEntries(in []helpEntry) []helpEntry {
	out := make([]helpEntry, 0, len(in))
	for i := 0; i < len(in); {
		if i+1 < len(in) && in[i].action == actFirst && in[i+1].action == actLast {
			out = append(out, mergeFirstLast(in[i], in[i+1]))
			i += 2
			continue
		}
		if i+1 < len(in) && sameHelp(in[i], in[i+1]) && allShift(in[i]) && allShift(in[i+1]) {
			j := i + 1
			for j+1 < len(in) && sameHelp(in[i], in[j+1]) && allShift(in[j+1]) {
				j++
			}
			out = append(out, helpEntry{keys: []string{"shift+arrows"}, help: in[i].help, action: in[i].action})
			i = j + 1
			continue
		}
		if i+1 < len(in) && sameHelp(in[i], in[i+1]) && mergeablePair(in[i], in[i+1]) {
			out = append(out, mergePair(in[i], in[i+1]))
			i += 2
			continue
		}
		out = append(out, in[i])
		i++
	}
	return out
}

func sameHelp(a, b helpEntry) bool {
	return a.help == b.help
}

func allShift(e helpEntry) bool {
	if len(e.keys) == 0 {
		return false
	}
	for _, key := range e.keys {
		if !strings.HasPrefix(key, "shift+") {
			return false
		}
	}
	return true
}

func mergeablePair(a, b helpEntry) bool {
	if len(a.keys) == 0 || len(b.keys) == 0 {
		return false
	}
	if len(a.keys) != len(b.keys) {
		return false
	}
	switch a.keys[0] {
	case "up", "down", "left", "right", "j", "k", "pgup", "pgdown", "home", "end", "ctrl+u", "ctrl+d", ",", ".", "[", "]", "tab":
		return true
	}
	return false
}

func mergeFirstLast(a, b helpEntry) helpEntry {
	keys := strings.Join(a.keys, "/") + ", " + strings.Join(b.keys, "/")
	help := a.help + ", " + b.help
	first, last := a.help, b.help
	if strings.HasPrefix(first, "first ") && strings.HasPrefix(last, "last ") {
		help = "first, last " + strings.TrimPrefix(last, "last ")
	}
	return helpEntry{keys: []string{keys}, help: help, action: a.action}
}

func mergePair(a, b helpEntry) helpEntry {
	if len(a.keys) == len(b.keys) && reversePair(a.keys[0], b.keys[0]) {
		a, b = b, a
	}
	if len(a.keys) == 1 && len(b.keys) == 1 {
		switch a.keys[0] {
		case ",":
			return helpEntry{keys: []string{", ."}, help: a.help, action: a.action}
		case "[":
			return helpEntry{keys: []string{"[ ]"}, help: a.help, action: a.action}
		}
		return helpEntry{keys: []string{pairKey(a.keys[0], b.keys[0])}, help: a.help, action: a.action}
	}
	head := pairKey(a.keys[0], b.keys[0])
	tail := pairKey(a.keys[1], b.keys[1])
	return helpEntry{keys: []string{head + ", " + tail}, help: a.help, action: a.action}
}

func reversePair(a, b string) bool {
	switch a {
	case "down":
		return b == "up"
	case "right":
		return b == "left"
	case "pgdown":
		return b == "pgup"
	case "ctrl+d":
		return b == "ctrl+u"
	}
	return false
}

func pairKey(a, b string) string {
	if strings.HasPrefix(a, "ctrl+") && strings.HasPrefix(b, "ctrl+") {
		return a + "/" + strings.TrimPrefix(b, "ctrl+")
	}
	a, b = shortKey(a), shortKey(b)
	if len(a) == 1 && len(b) == 1 && a > b {
		a, b = b, a
	}
	return a + "/" + b
}

func shortKey(key string) string {
	if key == "pgdown" {
		return "pgdn"
	}
	return key
}

const helpKeyWidth = 22

func (m Model) helpRow(keys, help string, w int) []string {
	const indent = 25
	lines := wrap(help, max(w-indent, 8))
	out := []string{"  " + sTitle.Render(cell(keys, helpKeyWidth)) + " " + lines[0]}
	for _, line := range lines[1:] {
		out = append(out, strings.Repeat(" ", indent)+line)
	}
	return out
}

// openHelp shows the overlay at its first line, on the current screen.
func (m *Model) openHelp() {
	m.help = true
	m.helpAll = false
	m.helpScroll = 0
}

// helpTitle names the view: this screen first, then the full list.
func (m Model) helpTitle() string {
	if m.helpAll {
		return "Keys · all screens"
	}
	return "Keys · " + m.currentHelpSection().title
}

// helpView keeps the heading and reserves rows for scroll indicators.
func (m Model) helpView(w, h int) []string {
	out := []string{section(m.helpTitle(), w)}
	lines := m.helpContent(w)
	if !m.helpAll && len(lines) > 0 {
		if strings.TrimSpace(ansi.Strip(lines[0])) == m.currentHelpSection().title {
			lines = lines[1:]
		}
	}
	if len(lines) <= h-1 {
		return append(out, lines...)
	}
	room := max(h-3, 1)
	start := min(max(m.helpScroll, 0), max(len(lines)-room, 0))
	if start > 0 {
		out = append(out, sDim.Render("  more above"))
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
