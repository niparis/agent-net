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
		return []hint{bindingHint(contextCard, "sessions"), bindingHint(contextCard, "node tools"), bindingHint(contextCard, "back"), bindingPairHint(contextCard, actPageUp, actPageDown), bindingHint(contextCard, "new session"), bindingHint(contextCard, "undo learned"), bindingHint(contextCard, "help"), bindingHint(contextCard, actQuit)}
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
		return []hint{bindingPairHint(contextQualityDetails, actUp, actDown), bindingPairHint(contextQualityDetails, actPageUp, actPageDown), bindingHint(contextQualityDetails, actClose)}
	case m.tab == quotasTab:
		return []hint{bindingPairHint(contextQuotas, actUp, actDown), bindingHint(contextQuotas, "protect"), bindingHint(contextQuotas, "show unset"), bindingHint(contextQuotas, "refresh"), cto, bindingHint(contextQuotas, "help"), bindingHint(contextQuotas, actQuit)}
	case m.tab == settingsTab:
		if m.settingsItems()[min(max(m.settingsRow, 0), len(m.settingsItems())-1)].section == 0 {
			return []hint{bindingPairHint(contextSettings, actUp, actDown), {key: "tab/[ ]", label: "section"}, bindingHint(contextSettings, "open"), bindingHint(contextSettings, "toggle"), bindingHint(contextSettings, "public"), bindingHint(contextSettings, "url"), bindingHint(contextSettings, "add"), bindingHint(contextSettings, "revoke"), bindingHint(contextSettings, "help"), bindingHint(contextSettings, actQuit)}
		}
		return []hint{bindingPairHint(contextSettings, actUp, actDown), {key: "tab/[ ]", label: "section"}, bindingHint(contextSettings, "open"), bindingHint(contextSettings, "toggle"), bindingHint(contextSettings, "help"), bindingHint(contextSettings, actQuit)}
	case m.tab == graphTab:
		tabs := hint{key: bindingHint(contextGlobal, "sessions tab").key + "-" + bindingHint(contextGlobal, "inbox tab").key, label: "tabs", prio: 1}
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
	cell := bindingHint(contextSessions, "cell")
	cell.key = "left/right"
	hs := []hint{bindingPairHint(contextSessions, actUp, actDown), bindingHint(contextSessions, "open session"), bindingHint(contextSessions, "compose"), bindingHint(contextSessions, "new session"), cell, sessionHelp, sessionQuit}
	if a, ok := m.selectedAgent(); ok {
		if a.Waiting != nil {
			hs[1].label = "answer"
		}
		if a.Harness == "bot" {
			hs[1].label = "messages"
			hs[3] = hint{key: "x", label: "revoke", prio: 1}
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
	if m.tab == inboxTab && m.inboxEditing {
		return clip(m.inboxInput.View()+" enter sends, esc cancels", w)
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
				out = append(out, "", " "+sBold.Render(m.helpSubsectionTitle(context)))
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
	for i, context := range m.overlayContexts() {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, " "+sBold.Render(m.helpSubsectionTitle(context)))
		out = append(out, m.contextHelpLines(context, w, false)...)
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
		{"Sessions", []keyContext{contextSessions}},
		{"Graph", []keyContext{contextGraph, contextGraphFind, contextCard}},
		{"Quotas", []keyContext{contextQuotas}},
		{"Messages", []keyContext{contextMessages}},
		{"Tools", []keyContext{contextTools, contextToolsDiff, contextToolsAll, contextToolsProject, contextToolsCompare}},
		{"Settings", []keyContext{contextSettings}},
		{"Inbox", []keyContext{contextInbox, contextHeld, contextSuggestion, contextOutcome, contextProposal}},
	}
	tab := min(max(m.tab, 0), len(sections)-1)
	ordered := []helpSection{sections[tab]}
	ordered = append(ordered, sections[:tab]...)
	ordered = append(ordered, sections[tab+1:]...)
	return ordered
}

func (m Model) overlayContexts() []keyContext {
	return []keyContext{
		contextBody, contextApps, contextQualityDetails, contextProposalDetail, contextProposalHistory,
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
	case contextToolsDiff:
		return "Differences"
	case contextToolsAll:
		return "All tools"
	case contextToolsProject:
		return "By project"
	case contextToolsCompare:
		return "Compare"
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
