package tui

import (
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

// hints lists the keys of the current screen. Other actions add theirs here.
func (m Model) hints() []hint {
	switch {
	case m.help:
		return []hint{{"up/down", "scroll", 1}, {"esc", "close help", 1}, {"q", "quit", 1}}
	case m.mapOn && m.focus != focusCTO && m.mp.overview:
		return []hint{{"D", "name", 0}, {"up/down", "select/scroll", 1}, {"left/right", "select", 1}, {"pgup/pgdn", "scroll", 1}, {"enter", "focus", 1}, {"h", "hide", 0}, {"H", "hidden", 0}, {"?", "help", 2}}
	case m.mapOn && m.focus != focusCTO:
		return []hint{{"D", "name", 0}, {"up/down", "projects", 1}, {"left/right", "follow", 1}, {"/", "find", 1}, {"z", "overview", 0}, {"[ ]", "charter", 3}, {"U/u", "learned/undo", 1}, {"h", "hide", 0}, {"H", "hidden", 0}, {"enter", "sessions", 1}, {"c", "CTO", 1}, {"?", "help", 2}, {"q", "quit", 1}}
	case m.open != nil:
		return []hint{{"up/down", "scroll", 1}, {"esc", "close", 1}, {"q", "quit", 1}}
	case m.focus == focusCTO:
		return []hint{{"enter", "open CTO", 1}, {"tab", "back", 1}, {"esc", "back", 3}, {"m", "model", 2}, {"n", "new", 2}, {"u", "undo", 2}}
	case m.tab == 1:
		return []hint{{"up/down", "select", 1}, {"left/right", "message", 1}, {"enter", "thread", 1}, {"w", "workers", 2}, {"v", "body", 1}, {"e", "events", 2}, {"l", "filter", 2}, {"tab", "CTO", 1}, {"?", "help", 3}}
	case m.tab == 2:
		return []hint{{"up/down", "scroll/select", 1}, {"p", "protect", 1}, {"s", "show not set up", 1}, {"tab", "CTO", 1}, {"1", "sessions", 2}, {"?", "help", 2}, {"q", "quit", 2}}
	case m.tab == 5:
		return []hint{{"1-6", "tabs", 1}, {"tab", "CTO", 1}, {"?", "keys", 1}, {"q", "quit", 1}}
	case m.tab == toolsTab:
		return m.toolsHints()
	}
	for _, e := range m.snap.Outcomes {
		if m.selected == "outcome:"+e.ReplyID {
			return []hint{{"s", "let stand", 1}, {"o", "overrule", 1}, {"?", "help", 2}, {"q", "quit", 1}}
		}
	}
	return []hint{{"1-6", "tabs", 2}, {"up/down", "select", 1}, {"enter", "open session", 1}, {"m", "msg", 1}, {"s", "start", 2}, {"x", "stop", 1}, {"h/H", "hide/show", 1}, {"t", "trust", 2}, {"p", "policy", 6}, {"N", "name machine", 6}, {"L", "learned", 7},
		{"/", "filter", 3}, {"o", "offline", 4}, {"a", "active", 6}, {"d", "detail", 3}, {"tab", "CTO", 2}, {"r", "refresh", 6}, {"?", "help", 2}, {"q", "quit", 0}}
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
	if m.mapOn && m.mp.finding {
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
		return sDim.Render(clip("tab completes  up/down or enter: field  enter on the task starts  esc cancels", w))
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
		return sDim.Render(clip("left/right: change a choice  up/down or enter: field  enter on Scope saves  esc cancels", w))
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
		{"n", "desktop notifications: " + state + " (toggle)"},
		{"Sessions", ""},
		{"1-6", "switch between Sessions, Messages, Quotas, Map, Tools and Graph"},
		{"up/down, j/k", "select a session"},
		{"pgup/pgdn, g/G", "page, first, last"},
		{"enter", "enter the selected session (ctrl+] to return)"},
		{"e", "enter the selected session (alias for Enter)"},
		{"m", "send a message to the session as yourself"},
		{"s", "start a worker (a form: project, harness, model, task)"},
		{"x, h, H", "stop worker; h hides/unhides; H shows hidden sessions (dim)"},
		{"t", "cycle request rule: normal, ask, auto"},
		{"p", "delegation policy: subagent or worker for code changes (a form)"},
		{"I", "integrations for all machines: six checkboxes (a form)"},
		{"4/M", "map: charters and edges; z switches overview"},
		{"map D", "name project (empty clears)"},
		{"map arrows", "focus: up/down projects, left/right edges, / find, [ ] scroll, U select learned change, u undo, h hide, H show hidden"},
		{"5 Tools", "[ ] screens; enter fix, x accept, a/u turn on/off, m machine; a plan shows first"},
		{"C, tab", "focus the CTO box (tab or esc returns); enter opens the full CTO, n starts new, u undoes, m changes model"},
		{"N", "name the machine of the session (every machine sees it; empty resets)"},
		{"L", "what the network learned: playbook notes and their support"},
		{"/", "filter (words match name, project, branch, model, ...)"},
		{"o, d", "show offline sessions; toggle selected session details"},
		{"a", "active only: hide sessions idle for over an hour"},
		{"Messages", "up/down: conversation; left/right: message; v: body; e/l: events"},
		{"r", "refresh now"},
		{"states", "messages: pending ... delivered ✓ replied ↩ failed ✗"},
		{"", ""},
		{"Everywhere", ""},
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
			{"/", "search or filter"}, {"r", "rescan"}, {"esc or ?", "close help"},
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
