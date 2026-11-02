package tui

import "strings"

type keyContext string
type actionID string

type keyBinding struct {
	context  keyContext
	keys     []string
	action   actionID
	label    string
	priority int
}

const (
	contextGlobal          keyContext = "global"
	contextSessions        keyContext = "Sessions"
	contextHeld            keyContext = "held request"
	contextSuggestion      keyContext = "learned suggestion"
	contextOutcome         keyContext = "outcome"
	contextProposal        keyContext = "CTO proposal"
	contextProposalDetail  keyContext = "proposal detail"
	contextProposalHistory keyContext = "proposal history"
	contextMessages        keyContext = "Messages"
	contextBody            keyContext = "message body"
	contextInbox           keyContext = "inbox"
	contextLearned         keyContext = "learned notes"
	contextQuotas          keyContext = "Quotas"
	contextGraph           keyContext = "Graph"
	contextGraphFind       keyContext = "Graph find"
	contextCard            keyContext = "agent card"
	contextTools           keyContext = "Tools"
	contextToolsDiff       keyContext = "Tools Differences"
	contextToolsAll        keyContext = "Tools All tools"
	contextToolsProject    keyContext = "Tools By project"
	contextToolsPlan       keyContext = "tools plan"
	contextToolsCompare    keyContext = "tools compare"
	contextToolsPicker     keyContext = "tools picker"
	contextToolsMenu       keyContext = "tools menu"
	contextToolsAdd        keyContext = "tools add"
	contextCTO             keyContext = "CTO box"
	contextHelp            keyContext = "help"
	contextConfirm         keyContext = "confirmations"
	contextForm            keyContext = "forms"
)

var allKeyContexts = []keyContext{
	contextGlobal, contextSessions, contextHeld, contextSuggestion, contextOutcome,
	contextProposal, contextProposalDetail, contextProposalHistory, contextMessages, contextBody, contextInbox,
	contextLearned, contextQuotas, contextGraph, contextGraphFind, contextCard,
	contextTools, contextToolsDiff, contextToolsAll, contextToolsProject, contextToolsPlan, contextToolsCompare,
	contextToolsPicker, contextToolsMenu, contextToolsAdd, contextCTO, contextHelp, contextConfirm, contextForm,
}

func bind(context keyContext, keys string, action actionID, label string, priority int) keyBinding {
	return keyBinding{context, strings.Split(keys, "|"), action, label, priority}
}

const (
	actTips          actionID = "tips"
	actRestartTips   actionID = "restart tips"
	actNotifications actionID = "notifications"
	actQuit          actionID = "quit"
	actClose         actionID = "close"
	actDown          actionID = "down"
	actUp            actionID = "up"
	actPageDown      actionID = "page down"
	actPageUp        actionID = "page up"
	actFirst         actionID = "first"
	actLast          actionID = "last"
)

var keyBindings = []keyBinding{
	bind(contextGlobal, "1", "sessions tab", "Sessions", -1),
	bind(contextGlobal, "2", "graph tab", "Graph", -1),
	bind(contextGlobal, "3", "quotas tab", "Quotas", -1),
	bind(contextGlobal, "4", "messages tab", "Messages", -1),
	bind(contextGlobal, "5", "tools tab", "Tools", -1),
	bind(contextGlobal, "c", "open CTO", "open CTO", -1),
	bind(contextGlobal, "C", "focus CTO", "focus CTO box", -1),
	bind(contextGlobal, "ctrl+c", actQuit, "quit", -1),
	bind(contextSessions, "q", actQuit, "quit", 0),
	bind(contextSessions, "up|k", actUp, "select", 1),
	bind(contextSessions, "down|j", actDown, "select", 1),
	bind(contextSessions, "pgup", actPageUp, "page", -1),
	bind(contextSessions, "pgdown", actPageDown, "page", -1),
	bind(contextSessions, "home|g", actFirst, "first row", -1),
	bind(contextSessions, "end", actLast, "last row", -1),
	bind(contextSessions, "enter", "open session", "open", 1),
	bind(contextSessions, "m", "compose", "msg", 1),
	bind(contextSessions, "n", "new session", "new", 1),
	bind(contextSessions, "x", "stop worker", "stop", -1),
	bind(contextSessions, "h", "hide", "hide", -1),
	bind(contextSessions, "H", "show hidden", "show hidden", -1),
	bind(contextSessions, "A", "active only", "active only", -1),
	bind(contextSessions, "R", "refresh", "refresh", -1),
	bind(contextSessions, "o", "offline", "offline", -1),
	bind(contextSessions, "t", "request rule", "request rule", -1),
	bind(contextSessions, "p", "policy", "policy", -1),
	bind(contextSessions, "I", "integrations", "integrations", -1),
	bind(contextSessions, "N", "rename machine", "rename machine", -1),
	bind(contextSessions, "L", "learned notes", "learned notes", -1),
	bind(contextSessions, "d", "details", "details", -1),
	bind(contextSessions, "l", "event filter", "events", -1),
	bind(contextSessions, "/", "filter", "filter", -1),
	bind(contextSessions, "esc", "back", "back", -1),
	bind(contextSessions, "i", "inbox", "inbox", -1),
	bind(contextSessions, "?", "help", "help", 2),
	bind(contextHeld, "a", "approve", "approve", 1),
	bind(contextHeld, "r", "refuse", "refuse", 1),
	bind(contextSuggestion, "a", "accept", "accept", 1),
	bind(contextSuggestion, "r", "reject", "reject", 1),
	bind(contextOutcome, "s", "let stand", "let stand", 1),
	bind(contextOutcome, "O", "overrule", "overrule", 1),
	bind(contextProposal, "enter", "detail", "detail", 1),
	bind(contextProposal, "a", "approve", "approve", 1),
	bind(contextProposal, "r", "decline", "decline", 1),
	bind(contextProposal, "A", "approve all", "approve all", 1),
	bind(contextProposalDetail, "esc", actClose, "back", 1),
	bind(contextProposalDetail, "up|k", actUp, "scroll", 1),
	bind(contextProposalDetail, "down|j", actDown, "scroll", 1),
	bind(contextProposalDetail, "?", "help", "help", 1),
	bind(contextProposalDetail, "a", "approve", "approve", 1),
	bind(contextProposalDetail, "r", "decline", "decline", 1),
	bind(contextProposalDetail, "A", "approve all", "approve all", 1),
	bind(contextProposalHistory, "esc", "back", "back", 1),
	bind(contextProposalHistory, "?", "help", "help", 1),
	bind(contextProposalHistory, "up|k", actUp, "select", 1),
	bind(contextProposalHistory, "down|j", actDown, "select", 1),
	bind(contextProposalHistory, "enter", "detail", "detail", 1),
	bind(contextMessages, "q", actQuit, "quit", 0),
	bind(contextMessages, "up|k", actUp, "thread", 1),
	bind(contextMessages, "down|j", actDown, "thread", 1),
	bind(contextMessages, "left", "previous message", "msg", 1),
	bind(contextMessages, "right", "next message", "msg", 1),
	bind(contextMessages, "pgup", actPageUp, "page", -1),
	bind(contextMessages, "pgdown", actPageDown, "page", -1),
	bind(contextMessages, "home|g", actFirst, "first thread", -1),
	bind(contextMessages, "end", actLast, "latest", 1),
	bind(contextMessages, "enter", "message body", "body", 1),
	bind(contextMessages, "f", "defect", "defect", -1),
	bind(contextMessages, "w", "worker threads", "workers", -1),
	bind(contextMessages, "e", "system events", "events", -1),
	bind(contextMessages, "l", "event filter", "events", -1),
	bind(contextMessages, "m", "compose", "msg", -1),
	bind(contextMessages, "x", "stop worker", "stop", -1),
	bind(contextMessages, "t", "request rule", "request rule", -1),
	bind(contextMessages, "p", "policy", "policy", -1),
	bind(contextMessages, "I", "integrations", "integrations", -1),
	bind(contextMessages, "N", "rename machine", "rename machine", -1),
	bind(contextMessages, "L", "learned notes", "learned notes", -1),
	bind(contextMessages, "d", "details", "details", -1),
	bind(contextMessages, "/", "filter", "filter", -1),
	bind(contextMessages, "esc", "back", "back", -1),
	bind(contextMessages, "o", "offline", "offline", -1),
	bind(contextMessages, "A", "active only", "active only", -1),
	bind(contextMessages, "R", "refresh", "refresh", -1),
	bind(contextMessages, "i", "inbox", "inbox", -1),
	bind(contextMessages, "?", "help", "help", 2),
	bind(contextBody, "esc|enter", actClose, "close", 1),
	bind(contextBody, "q", actQuit, "quit", 1),
	bind(contextBody, "up|k", actUp, "scroll", 1),
	bind(contextBody, "down|j", actDown, "scroll", 1),
	bind(contextBody, "pgup", actPageUp, "page", -1),
	bind(contextBody, "pgdown", actPageDown, "page", -1),
	bind(contextInbox, "esc", "back", "back", 1),
	bind(contextInbox, "up|k", actUp, "select", 1),
	bind(contextInbox, "down|j", actDown, "select", 1),
	bind(contextInbox, "enter", "details", "details", 1),
	bind(contextInbox, "a", "approve", "approve", 1),
	bind(contextInbox, "r", "refuse", "refuse", 1),
	bind(contextInbox, "t", "answer", "answer", 1),
	bind(contextLearned, "q", actQuit, "quit", 1),
	bind(contextLearned, "esc|L|enter|backspace", actClose, "back", 1),
	bind(contextQuotas, "q", actQuit, "quit", 0),
	bind(contextQuotas, "up|k", actUp, "select", 1),
	bind(contextQuotas, "down|j", actDown, "select", 1),
	bind(contextQuotas, "left", "previous agent", "agent", -1),
	bind(contextQuotas, "right", "next agent", "agent", -1),
	bind(contextQuotas, "p", "protect", "protect", 1),
	bind(contextQuotas, "s", "show unset", "show unset", 2),
	bind(contextQuotas, "d", "quality details", "details", -1),
	bind(contextQuotas, "R", "refresh", "refresh", 2),
	bind(contextQuotas, "?", "help", "help", 2),
	bind(contextGraph, "q", actQuit, "quit", 0),
	bind(contextGraph, "?", "help", "help", 0),
	bind(contextGraph, "/", "find", "find", -1),
	bind(contextGraph, "+|=", "zoom in", "zoom in", -1),
	bind(contextGraph, "-", "zoom out", "zoom out", -1),
	bind(contextGraph, "left", "select left", "select", 1),
	bind(contextGraph, "right", "select right", "select", 1),
	bind(contextGraph, "up", "select up", "select", 1),
	bind(contextGraph, "down", "select down", "select", 1),
	bind(contextGraph, "shift+left", "pan left", "pan", -1),
	bind(contextGraph, "shift+right", "pan right", "pan", -1),
	bind(contextGraph, "shift+up", "pan up", "pan", -1),
	bind(contextGraph, "shift+down", "pan down", "pan", -1),
	bind(contextGraph, "0", "fit", "fit", -1),
	bind(contextGraph, ",", "previous link", "previous neighbour", -1),
	bind(contextGraph, ".", "next link", "next neighbour", -1),
	bind(contextGraph, "t", "node tools", "tools", -1),
	bind(contextGraph, "o", "orbit", "orbit", -1),
	bind(contextGraph, "space", "pause", "pause", -1),
	bind(contextGraph, "n", "new session", "new", -1),
	bind(contextGraph, "enter", "open card", "card", -1),
	bind(contextGraph, "R", "refresh", "refresh", -1),
	bind(contextGraphFind, "esc", "clear or back", "clear or back", 1),
	bind(contextGraphFind, "enter", "select match", "select", 1),
	bind(contextGraphFind, "backspace|ctrl+h", "delete text", "delete", -1),
	bind(contextCard, "q", actQuit, "quit", 0),
	bind(contextCard, "esc", "back", "Graph", 0),
	bind(contextCard, "enter", "sessions", "sessions", 1),
	bind(contextCard, "n", "new session", "new", 1),
	bind(contextCard, "t", "node tools", "tools", 1),
	bind(contextCard, "a", "autonomy", "autonomy", -1),
	bind(contextCard, "D", "rename project", "name project", -1),
	bind(contextCard, "u", "undo learned", "undo", 1),
	bind(contextCard, ",", "previous learned", "previous change", -1),
	bind(contextCard, ".", "next learned", "next change", -1),
	bind(contextCard, "?", "help", "help", 0),
	bind(contextCard, "!", "legend", "legend", 2),
	bind(contextCard, "h", "hide project", "hide", -1),
	bind(contextCard, "H", "show hidden", "show hidden", -1),
	bind(contextCard, "ctrl+d|pgdown", actPageDown, "scroll", 1),
	bind(contextCard, "ctrl+u|pgup", actPageUp, "scroll", 1),
	bind(contextCard, "R", "refresh", "refresh", -1),
	bind(contextTools, "q", actQuit, "quit", 0),
	bind(contextTools, "?", "help", "help", 1),
	bind(contextTools, "j|down", actDown, "move", 0),
	bind(contextTools, "k|up", actUp, "move", 0),
	bind(contextTools, "/", "filter", "filter", 0),
	bind(contextTools, "esc", "clear filter", "clear", 1),
	bind(contextTools, "n", "add", "add", 0),
	bind(contextTools, "R", "rescan", "rescan", -1),
	bind(contextToolsDiff, "a", "accept difference", "accept", 1),
	bind(contextToolsDiff, "enter", "difference detail", "detail", 1),
	bind(contextToolsDiff, ".", "show accepted", "accepted", -1),
	bind(contextToolsDiff, "[", "previous screen", "screen", 2),
	bind(contextToolsDiff, "]", "next screen", "screen", 2),
	bind(contextToolsAll, "enter", "item detail", "detail", 1),
	bind(contextToolsAll, "a", "turn on", "on", 1),
	bind(contextToolsAll, "u", "turn off", "off", 1),
	bind(contextToolsAll, "left", "previous column", "column", 1),
	bind(contextToolsAll, "right", "next column", "column", 1),
	bind(contextToolsAll, "[", "previous screen", "screen", -1),
	bind(contextToolsAll, "]", "next screen", "screen", -1),
	bind(contextToolsProject, "left", "previous column", "column", 1),
	bind(contextToolsProject, "right", "next column", "column", 1),
	bind(contextToolsProject, "[", "previous screen", "screen", -1),
	bind(contextToolsProject, "]", "next screen", "screen", -1),
	bind(contextToolsProject, "v", "next machine", "machine", 1),
	bind(contextToolsPlan, "enter|y|Y", "apply", "apply", 1),
	bind(contextToolsPlan, "esc|n|N", "cancel", "cancel", 1),
	bind(contextToolsPlan, "up|k", actUp, "scroll", 1),
	bind(contextToolsPlan, "down|j", actDown, "scroll", 1),
	bind(contextToolsPlan, "d", "exact folders", "folders", -1),
	bind(contextToolsCompare, "n", "next file", "next", 1),
	bind(contextToolsCompare, "p", "previous file", "previous", 1),
	bind(contextToolsPicker, "esc", "cancel", "cancel", 1),
	bind(contextToolsPicker, "up|k", actUp, "select", 1),
	bind(contextToolsPicker, "down|j", actDown, "select", 1),
	bind(contextToolsPicker, "space", "tick", "tick", 1),
	bind(contextToolsPicker, "/", "filter", "filter", 1),
	bind(contextToolsPicker, "enter", "apply", "apply", 1),
	bind(contextToolsMenu, "esc", "back", "back", 1),
	bind(contextToolsMenu, "up|k", actUp, "move", 1),
	bind(contextToolsMenu, "down|j", actDown, "move", 1),
	bind(contextToolsMenu, "enter", "choose", "choose", 1),
	bind(contextToolsAdd, "esc", "cancel", "cancel", 1),
	bind(contextToolsAdd, "tab|down", "next field", "field", 1),
	bind(contextToolsAdd, "shift+tab|up", "previous field", "field", 1),
	bind(contextToolsAdd, "left", "previous machine", "machine", 1),
	bind(contextToolsAdd, "right", "next machine", "machine", 1),
	bind(contextToolsAdd, "enter", "add", "add", 1),
	bind(contextToolsAdd, "backspace|ctrl+h", "delete text", "delete", -1),
	bind(contextCTO, "ctrl+]|tab", "back", "back", 1),
	bind(contextCTO, "esc", "skip tip", "skip tip", -1),
	bind(contextCTO, "enter", "open CTO", "open", 1),
	bind(contextCTO, "pgup|ctrl+u", actPageUp, "scroll", -1),
	bind(contextCTO, "pgdown|ctrl+d", actPageDown, "scroll", -1),
	bind(contextCTO, "m", "model", "model", 2),
	bind(contextCTO, "n", "new conversation", "new", 2),
	bind(contextCTO, "u", "undo", "undo", 2),
	bind(contextCTO, "w", "drive window", "drive", -1),
	{contextHelp, []string{"T"}, actTips, "toggle tips", 0},
	{contextHelp, []string{"R"}, actRestartTips, "restart tips", 0},
	{contextHelp, []string{"N"}, actNotifications, "toggle notifications", 0},
	{contextHelp, []string{"q"}, actQuit, "quit", 1},
	{contextHelp, []string{"esc", "?"}, actClose, "close help", 1},
	{contextHelp, []string{"down", "j"}, actDown, "scroll down", 1},
	{contextHelp, []string{"up", "k"}, actUp, "scroll up", 1},
	{contextHelp, []string{"pgdown"}, actPageDown, "page down", 0},
	{contextHelp, []string{"pgup"}, actPageUp, "page up", 0},
	{contextHelp, []string{"home", "g"}, actFirst, "first line", 0},
	{contextHelp, []string{"end", "G"}, actLast, "last line", 0},
	bind(contextConfirm, "enter|y|Y", "yes", "yes", 1),
	bind(contextConfirm, "esc|n|N", "no", "no", 1),
	bind(contextForm, "enter", "submit", "submit or next field", 1),
	bind(contextForm, "esc", "cancel", "cancel", 1),
	bind(contextForm, "tab", "tab field", "next field", 1),
	bind(contextForm, "down", "next field", "next field", 1),
	bind(contextForm, "shift+tab|up", "previous field", "previous field", 1),
	bind(contextForm, "left", "previous choice", "change choice", 1),
	bind(contextForm, "right", "next choice", "change choice", 1),
	bind(contextForm, "space", "toggle", "toggle", 1),
}

func keyAction(context keyContext, key string) actionID {
	if key == " " {
		key = "space"
	}
	for _, binding := range keyBindings {
		if binding.context == context {
			for _, k := range binding.keys {
				if k == key {
					return binding.action
				}
			}
		}
	}
	return ""
}

func bindingHint(context keyContext, action actionID) hint {
	for _, b := range keyBindings {
		if b.context == context && b.action == action {
			return hint{key: b.keys[0], label: b.label, prio: b.priority}
		}
	}
	panic("missing key binding: " + string(context) + " " + string(action))
}

func bindingPairHint(context keyContext, first, second actionID) hint {
	a, b := bindingHint(context, first), bindingHint(context, second)
	a.key += "/" + b.key
	// "scroll up" and "scroll down" share a line as "scroll".
	if i := strings.LastIndex(a.label, " "); i > 0 && strings.HasPrefix(b.label, a.label[:i+1]) {
		a.label = a.label[:i]
	}
	return a
}
