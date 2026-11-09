package tui

// slowText is what a screen says in place of a raw "context deadline exceeded":
// the answer is slow, the last data stays on screen, and the request is made
// again (operator.IsSlow tells a slow answer from a refusal).
const slowText = "slow, retrying"

// deliverySlowText is the line of the changes list after a load timed out.
func deliverySlowText(haveList bool) string {
	if haveList {
		return "Changes: " + slowText + ". Showing the last list."
	}
	return "Changes: " + slowText + "."
}

// screenNames are the names of the tabs in tabNames, as the daemon log and the
// gateway counters show them.
var screenNames = []string{"sessions", "graph", "quotas", "messages", "tools", "settings", "desk"}

// screenName names the tab or view that is on screen.
func (m Model) screenName() string {
	switch {
	case m.cto != nil && m.focus == focusCTO:
		return "cto"
	case m.tab >= 0 && int(m.tab) < len(screenNames):
		return screenNames[m.tab]
	}
	return "other"
}

// tellScreen gives the source the name of the screen that is on, if it keeps
// one. The operator client sends it with a call that fails, so the daemon log
// says which screen made the slow request.
func (m Model) tellScreen() {
	if s, ok := m.src.(interface{ SetScreen(string) }); ok {
		s.SetScreen(m.screenName())
	}
}
