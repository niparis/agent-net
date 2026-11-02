package tui

import "strings"

type keyContext string
type actionID string

type keyBinding struct {
	context  keyContext
	keys     []string
	action   actionID
	label    string
	help     string // longer text for the help overlay; label when empty
	local    bool   // show in a tab section even when the key is global
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
	contextApps            keyContext = "connected apps"
	contextSettings        keyContext = "Settings"
	contextQuotas          keyContext = "Quotas"
	contextQualityDetails  keyContext = "Quality details"
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
	contextSettings, contextApps, contextQuotas, contextQualityDetails, contextGraph, contextGraphFind, contextCard,
	contextTools, contextToolsDiff, contextToolsAll, contextToolsProject, contextToolsPlan, contextToolsCompare,
	contextToolsPicker, contextToolsMenu, contextToolsAdd, contextCTO, contextHelp, contextConfirm, contextForm,
}

func bind(context keyContext, keys string, action actionID, label string, priority int) keyBinding {
	return keyBinding{context: context, keys: strings.Split(keys, "|"), action: action, label: label, priority: priority}
}

// withHelp adds the overlay text for a binding whose label is too short for it.
func withHelp(b keyBinding, help string) keyBinding {
	b.help = help
	return b
}

// withLocal keeps a binding in its tab section when its key is also global.
func withLocal(b keyBinding) keyBinding {
	b.local = true
	return b
}

// globalHelpRows is the fixed Global keys list. It is written once here and
// never built by joining other contexts' labels.
var globalHelpRows = []struct{ keys, help string }{
	{"1-7", "switch tabs: Sessions, Graph, Quotas, Messages, Tools, Settings, Inbox"},
	{"enter", "go into the selected thing"},
	{"arrows, j/k", "move"},
	{"esc", "back; first clears a filter you typed"},
	{"/", "filter or search"},
	{"R", "refresh"},
	{"c", "open the CTO session"},
	{"C", "focus the CTO box"},
	{"?", "help"},
	{"q, ctrl+c", "quit"},
}

// globalSameKeys are the keys the Global list describes. A tab section hides
// one unless the binding is marked local and so means something different.
var globalSameKeys = []string{
	"1", "2", "3", "4", "5", "6", "7", "enter", "up", "down", "left", "right", "j", "k",
	"esc", "/", "R", "c", "C", "?", "q", "ctrl+c",
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
	bind(contextGlobal, "6", "settings tab", "Settings", -1),
	bind(contextGlobal, "7", "inbox tab", "Inbox", -1),
	withHelp(bind(contextGlobal, "c", "open CTO", "open CTO", -1), "open the CTO session"),
	withHelp(bind(contextGlobal, "C", "focus CTO", "focus CTO box", -1), "focus the CTO box for its own keys: enter opens the full CTO, ctrl+] returns, esc skips a tip"),
	bind(contextGlobal, "ctrl+c", actQuit, "quit", -1),
	withHelp(bind(contextSessions, "q", actQuit, "quit", 0), "quit"),
	withHelp(bind(contextSessions, "up|k", actUp, "select", 1), "move the selection"),
	withHelp(bind(contextSessions, "down|j", actDown, "select", 1), "move the selection"),
	bind(contextSessions, "pgup", actPageUp, "page", -1),
	bind(contextSessions, "pgdown", actPageDown, "page", -1),
	bind(contextSessions, "home|g", actFirst, "first row", -1),
	bind(contextSessions, "end", actLast, "last row", -1),
	withLocal(withHelp(bind(contextSessions, "enter", "open session", "open", 1), "go into the selected session on NAME, or open the delegation policy on DELEGATE")),
	withHelp(bind(contextSessions, "m", "compose", "msg", 1), "send a message to the session as yourself"),
	withHelp(bind(contextSessions, "n", "new session", "new", 1), "start a session in herdr: project, harness, model, folder and first message (a form)"),
	withHelp(bind(contextSessions, "x", "stop worker", "stop", -1), "stop the selected worker or session"),
	withHelp(bind(contextSessions, "h", "hide", "hide", -1), "hide or unhide the selected session"),
	withLocal(withHelp(bind(contextSessions, "left|right", "cell", "cell", 1), "move between NAME, RULE and DELEGATE cells")),
	withHelp(bind(contextSessions, "space", "change cell", "change", 1), "cycle the request rule: normal, ask, auto"),
	withHelp(bind(contextSessions, "v", "cycle view", "view", -1), "cycle the view: active only, all, all with offline, all with offline and hidden"),
	bind(contextSessions, "R", "refresh", "refresh", -1),
	withHelp(bind(contextSessions, "d", "details", "details", -1), "show or hide the selected session details"),
	bind(contextSessions, "/", "filter", "filter", -1),
	bind(contextSessions, "esc", "back", "back", -1),
	bind(contextSessions, "?", "help", "help", 2),
	bind(contextHeld, "a", "approve", "approve", 1),
	bind(contextHeld, "r", "refuse", "refuse", 1),
	bind(contextSuggestion, "a", "accept", "accept", 1),
	bind(contextSuggestion, "r", "reject", "reject", 1),
	bind(contextOutcome, "s", "let stand", "let stand", 1),
	bind(contextOutcome, "O", "overrule", "overrule", 1),
	withLocal(withHelp(bind(contextProposal, "enter", "detail", "detail", 1), "open the proposal detail")),
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
	withLocal(withHelp(bind(contextProposalHistory, "enter", "detail", "detail", 1), "open the proposal detail")),
	bind(contextMessages, "q", actQuit, "quit", 0),
	withHelp(bind(contextMessages, "up|k", actUp, "thread", 1), "select a conversation"),
	withHelp(bind(contextMessages, "down|j", actDown, "thread", 1), "select a conversation"),
	withLocal(withHelp(bind(contextMessages, "left", "previous message", "msg", 1), "select the previous or next message")),
	withLocal(withHelp(bind(contextMessages, "right", "next message", "msg", 1), "select the previous or next message")),
	bind(contextMessages, "pgup", actPageUp, "page", -1),
	bind(contextMessages, "pgdown", actPageDown, "page", -1),
	bind(contextMessages, "home|g", actFirst, "first thread", -1),
	withHelp(bind(contextMessages, "end", actLast, "latest", 1), "latest message"),
	withLocal(withHelp(bind(contextMessages, "enter", "message body", "body", 1), "open the selected message body")),
	withHelp(bind(contextMessages, "f", "defect", "defect", -1), "report a defect found later on the selected worker conversation"),
	bind(contextMessages, "l", "cycle message filter", "filter", -1),
	withHelp(bind(contextMessages, "m", "compose", "msg", -1), "send a message to the session as yourself"),
	withHelp(bind(contextMessages, "x", "stop worker", "stop", -1), "stop the selected worker or session"),
	withHelp(bind(contextMessages, "d", "details", "details", -1), "show or hide details"),
	bind(contextMessages, "/", "filter", "filter", -1),
	bind(contextMessages, "esc", "back", "back", -1),
	bind(contextMessages, "R", "refresh", "refresh", -1),
	bind(contextMessages, "?", "help", "help", 2),
	bind(contextBody, "esc|enter", actClose, "close", 1),
	bind(contextBody, "q", actQuit, "quit", 1),
	bind(contextBody, "up|k", actUp, "scroll", 1),
	bind(contextBody, "down|j", actDown, "scroll", 1),
	bind(contextBody, "pgup", actPageUp, "page", -1),
	bind(contextBody, "pgdown", actPageDown, "page", -1),
	bind(contextInbox, "esc", "back", "back", 1),
	withHelp(bind(contextInbox, "up|k", actUp, "select", 1), "move the selection"),
	withHelp(bind(contextInbox, "down|j", actDown, "select", 1), "move the selection"),
	bind(contextInbox, "pgup", actPageUp, "page", -1),
	bind(contextInbox, "pgdown", actPageDown, "page", -1),
	bind(contextInbox, "home|g", actFirst, "first row", -1),
	bind(contextInbox, "end", actLast, "last row", -1),
	withHelp(bind(contextInbox, "enter", "details", "details", 1), "open the details, the proposal, or the session"),
	bind(contextInbox, "a", "approve", "approve", 1),
	bind(contextInbox, "r", "refuse", "refuse", 1),
	bind(contextInbox, "s", "let stand", "let stand", 1),
	bind(contextInbox, "O", "overrule", "overrule", 1),
	bind(contextInbox, "A", "approve all", "approve all", 1),
	bind(contextInbox, "t", "answer", "answer", 1),
	bind(contextInbox, "x", "dismiss", "dismiss", 1),
	bind(contextInbox, "?", "help", "help", 1),
	bind(contextInbox, "q", actQuit, "quit", 0),
	bind(contextApps, "esc", "back", "back", 1),
	bind(contextApps, "up|k", actUp, "move", 1),
	bind(contextApps, "down|j", actDown, "move", 1),
	bind(contextApps, "p", "public", "internet access", 1),
	bind(contextApps, "u", "url", "tunnel URL", 1),
	bind(contextApps, "a", "add", "add", 1),
	bind(contextApps, "x", "revoke", "revoke", 1),
	bind(contextApps, "enter", "approve", "approve", 1),
	bind(contextApps, "r", "refuse", "refuse", 1),
	bind(contextApps, "left|right", "kind", "change kind", 1),
	bind(contextApps, "tab", "name", "edit name", 1),
	withHelp(bind(contextSettings, "up|k", actUp, "row", 1), "move between settings rows"),
	withHelp(bind(contextSettings, "down|j", actDown, "row", 1), "move between settings rows"),
	withHelp(bind(contextSettings, "tab|]", "next section", "section", 1), "jump to the next section"),
	withHelp(bind(contextSettings, "shift+tab|[", "previous section", "section", 1), "jump to the previous section"),
	withLocal(withHelp(bind(contextSettings, "enter", "open", "open", 1), "open the selected setting: integrations, name this machine, or restart tips")),
	withHelp(bind(contextSettings, "space", "toggle", "toggle", 1), "toggle internet access, desktop notifications or tips"),
	withHelp(bind(contextSettings, "p", "public", "internet", 1), "switch internet access in Connected apps"),
	withHelp(bind(contextSettings, "u", "url", "URL", 1), "set the tunnel URL in Connected apps"),
	withHelp(bind(contextSettings, "a", "add", "add", 1), "add a connected app"),
	withHelp(bind(contextSettings, "x", "revoke", "revoke", 1), "revoke the selected app"),
	bind(contextSettings, "?", "help", "help", 1),
	bind(contextSettings, "q", actQuit, "quit", 0),
	bind(contextQuotas, "q", actQuit, "quit", 0),
	withHelp(bind(contextQuotas, "up|k", actUp, "select", 1), "select a subscription"),
	withHelp(bind(contextQuotas, "down|j", actDown, "select", 1), "select a subscription"),
	withLocal(withHelp(bind(contextQuotas, "left", "previous agent", "agent", -1), "cycle the Quality panel agent")),
	withLocal(withHelp(bind(contextQuotas, "right", "next agent", "agent", -1), "cycle the Quality panel agent")),
	withHelp(bind(contextQuotas, "p", "protect", "protect", 1), "protect or unprotect this subscription"),
	withHelp(bind(contextQuotas, "s", "show unset", "show unset", 2), "show or hide subscriptions that are not set up"),
	withHelp(bind(contextQuotas, "d", "quality details", "details", -1), "open Quality details"),
	bind(contextQuotas, "R", "refresh", "refresh", 2),
	bind(contextQualityDetails, "esc", actClose, "back", 1),
	bind(contextQualityDetails, "up|k", actUp, "scroll", 1),
	bind(contextQualityDetails, "down|j", actDown, "scroll", 1),
	bind(contextQualityDetails, "pgup", actPageUp, "page", 1),
	bind(contextQualityDetails, "pgdown", actPageDown, "page", 1),
	bind(contextQualityDetails, "left", "previous agent", "agent", 1),
	bind(contextQualityDetails, "right", "next agent", "agent", 1),
	bind(contextQuotas, "?", "help", "help", 2),
	bind(contextGraph, "q", actQuit, "quit", 0),
	bind(contextGraph, "?", "help", "help", 0),
	withLocal(withHelp(bind(contextGraph, "/", "find", "find", -1), "find a node; enter selects the first match")),
	bind(contextGraph, "+|=", "zoom in", "zoom in", -1),
	bind(contextGraph, "-", "zoom out", "zoom out", -1),
	withHelp(bind(contextGraph, "left", "select left", "select", 1), "move the selection between nodes"),
	withHelp(bind(contextGraph, "right", "select right", "select", 1), "move the selection between nodes"),
	withHelp(bind(contextGraph, "up", "select up", "select", 1), "move the selection between nodes"),
	withHelp(bind(contextGraph, "down", "select down", "select", 1), "move the selection between nodes"),
	bind(contextGraph, "shift+left", "pan left", "pan", -1),
	bind(contextGraph, "shift+right", "pan right", "pan", -1),
	bind(contextGraph, "shift+up", "pan up", "pan", -1),
	bind(contextGraph, "shift+down", "pan down", "pan", -1),
	withHelp(bind(contextGraph, ",", "previous link", "previous neighbour", -1), "select a linked neighbour"),
	withHelp(bind(contextGraph, ".", "next link", "next neighbour", -1), "select a linked neighbour"),
	withHelp(bind(contextGraph, "n", "new session", "new", -1), "start a session in this project"),
	withLocal(withHelp(bind(contextGraph, "enter", "open card", "card", -1), "open the selected agent card")),
	bind(contextGraph, "R", "refresh", "refresh", -1),
	bind(contextGraphFind, "esc", "clear or back", "clear or back", 1),
	withLocal(withHelp(bind(contextGraphFind, "enter", "select match", "select", 1), "select the first match")),
	bind(contextGraphFind, "backspace|ctrl+h", "delete text", "delete", -1),
	bind(contextCard, "q", actQuit, "quit", 0),
	bind(contextCard, "esc", "back", "Graph", 0),
	withLocal(withHelp(bind(contextCard, "enter", "sessions", "sessions", 1), "open Sessions filtered to this agent's project")),
	withHelp(bind(contextCard, "n", "new session", "new", 1), "start a new session in this project (the same form, filled from here)"),
	withHelp(bind(contextCard, "t", "node tools", "tools", 1), "open Tools filtered to this node"),
	withHelp(bind(contextCard, "a", "autonomy", "autonomy", -1), "toggle autonomy off or wake"),
	withHelp(bind(contextCard, "D", "rename project", "name project", -1), "name this project"),
	withHelp(bind(contextCard, "u", "undo learned", "undo", 1), "undo the selected one (a learned change)"),
	withHelp(bind(contextCard, ",", "previous learned", "previous change", -1), "select a previous or next learned change"),
	withHelp(bind(contextCard, ".", "next learned", "next change", -1), "select a previous or next learned change"),
	bind(contextCard, "?", "help", "help", 0),
	withHelp(bind(contextCard, "h", "hide project", "hide", -1), "hide or unhide this project"),
	withHelp(bind(contextCard, "H", "show hidden", "show hidden", -1), "show hidden projects"),
	withHelp(bind(contextCard, "ctrl+d|pgdown", actPageDown, "scroll", 1), "scroll the card: charter, rules and learned changes"),
	withHelp(bind(contextCard, "ctrl+u|pgup", actPageUp, "scroll", 1), "scroll the card: charter, rules and learned changes"),
	bind(contextCard, "R", "refresh", "refresh", -1),
	bind(contextTools, "q", actQuit, "quit", 0),
	bind(contextTools, "?", "help", "help", 1),
	bind(contextTools, "j|down", actDown, "move", 0),
	bind(contextTools, "k|up", actUp, "move", 0),
	bind(contextTools, "/", "filter", "filter", 0),
	bind(contextTools, "esc", "clear filter", "clear", 1),
	withHelp(bind(contextTools, "n", "add", "add", 0), "add an MCP server or a skill from a folder"),
	withLocal(withHelp(bind(contextTools, "R", "rescan", "rescan", -1), "rescan tool copies")),
	withHelp(bind(contextToolsDiff, "a", "accept difference", "accept", 1), "accept the difference as it is"),
	withLocal(withHelp(bind(contextToolsDiff, "enter", "difference detail", "detail", 1), "open the difference plan")),
	withHelp(bind(contextToolsDiff, ".", "show accepted", "accepted", -1), "show accepted copies"),
	withHelp(bind(contextToolsDiff, "[", "previous screen", "screen", 2), "switch screens"),
	withHelp(bind(contextToolsDiff, "]", "next screen", "screen", 2), "switch screens"),
	withLocal(withHelp(bind(contextToolsAll, "enter", "item detail", "detail", 1), "open the item details and actions")),
	withHelp(bind(contextToolsAll, "a", "turn on", "on", 1), "turn on the selected tool"),
	withHelp(bind(contextToolsAll, "u", "turn off", "off", 1), "turn off the selected tool"),
	withLocal(withHelp(bind(contextToolsAll, "left", "previous column", "column", 1), "move between columns")),
	withLocal(withHelp(bind(contextToolsAll, "right", "next column", "column", 1), "move between columns")),
	withHelp(bind(contextToolsAll, "[", "previous screen", "screen", -1), "switch screens"),
	withHelp(bind(contextToolsAll, "]", "next screen", "screen", -1), "switch screens"),
	withLocal(withHelp(bind(contextToolsProject, "left", "previous column", "column", 1), "move between columns")),
	withLocal(withHelp(bind(contextToolsProject, "right", "next column", "column", 1), "move between columns")),
	withHelp(bind(contextToolsProject, "[", "previous screen", "screen", -1), "switch screens"),
	withHelp(bind(contextToolsProject, "]", "next screen", "screen", -1), "switch screens"),
	withHelp(bind(contextToolsProject, "v", "next machine", "machine", 1), "select the next machine"),
	bind(contextToolsPlan, "enter|y|Y", "apply", "apply", 1),
	bind(contextToolsPlan, "esc|n|N", "cancel", "cancel", 1),
	bind(contextToolsPlan, "up|k", actUp, "scroll", 1),
	bind(contextToolsPlan, "down|j", actDown, "scroll", 1),
	withHelp(bind(contextToolsPlan, "d", "exact folders", "folders", -1), "use the exact folders"),
	withHelp(bind(contextToolsCompare, "n", "next file", "next", 1), "select the next file"),
	withHelp(bind(contextToolsCompare, "p", "previous file", "previous", 1), "select the previous file"),
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
	withHelp(bind(contextToolsAdd, "tab|down", "next field", "field", 1), "move to the next field"),
	withHelp(bind(contextToolsAdd, "shift+tab|up", "previous field", "field", 1), "previous field"),
	withHelp(bind(contextToolsAdd, "left", "previous machine", "machine", 1), "choose the machine"),
	withHelp(bind(contextToolsAdd, "right", "next machine", "machine", 1), "choose the machine"),
	bind(contextToolsAdd, "enter", "add", "add", 1),
	bind(contextToolsAdd, "backspace|ctrl+h", "delete text", "delete", -1),
	withHelp(bind(contextCTO, "ctrl+]|tab", "back", "back", 1), "back to Sessions"),
	bind(contextCTO, "esc", "skip tip", "skip tip", -1),
	withHelp(bind(contextCTO, "enter", "open CTO", "open", 1), "opens the full CTO"),
	bind(contextCTO, "pgup|ctrl+u", actPageUp, "scroll", -1),
	bind(contextCTO, "pgdown|ctrl+d", actPageDown, "scroll", -1),
	bind(contextCTO, "m", "model", "model", 2),
	bind(contextCTO, "n", "new conversation", "new", 2),
	bind(contextCTO, "u", "undo", "undo", 2),
	bind(contextCTO, "w", "drive window", "drive", -1),
	{contextHelp, []string{"q"}, actQuit, "quit", "", false, 1},
	{contextHelp, []string{"esc", "?"}, actClose, "close help", "", false, 1},
	{contextHelp, []string{"down", "j"}, actDown, "scroll down", "scroll", false, 1},
	{contextHelp, []string{"up", "k"}, actUp, "scroll up", "scroll", false, 1},
	{contextHelp, []string{"pgdown"}, actPageDown, "page down", "page", false, 0},
	{contextHelp, []string{"pgup"}, actPageUp, "page up", "page", false, 0},
	{contextHelp, []string{"home", "g"}, actFirst, "first line", "", false, 0},
	{contextHelp, []string{"end", "G"}, actLast, "last line", "", false, 0},
	bind(contextConfirm, "enter|y|Y", "yes", "yes", 1),
	bind(contextConfirm, "esc|n|N", "no", "no", 1),
	bind(contextForm, "enter", "submit", "submit or next field", 1),
	bind(contextForm, "esc", "cancel", "cancel", 1),
	withHelp(bind(contextForm, "tab", "tab field", "next field", 1), "move to the next field"),
	withHelp(bind(contextForm, "down", "next field", "next field", 1), "move to the next field"),
	bind(contextForm, "shift+tab|up", "previous field", "previous field", 1),
	withHelp(bind(contextForm, "left", "previous choice", "change choice", 1), "change the choice"),
	withHelp(bind(contextForm, "right", "next choice", "change choice", 1), "change the choice"),
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
