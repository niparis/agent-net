// Package tabs is the one list of the TUI screens and the number keys that
// open them. The setup summary reads the same list, so a tab reorder cannot
// leave its key hints pointing at the wrong screen.
package tabs

// Tab is one screen in the tab bar.
type Tab struct {
	Name   string // the full name shown at wide widths
	Short  string // the name shown when the bar is tight
	Key    string // the number key that opens the tab
	Action string // the TUI action that opens the tab
}

// CTOKey opens the CTO session from any tab. The CTO is not a tab.
const CTOKey = "c"

// All is the tab bar, left to right. The Desk (key 6) is hidden since
// decision 0196: what needs the owner goes to the chief of staff.
var All = []Tab{
	{Name: "Sessions", Short: "Sess", Key: "1", Action: "sessions tab"},
	{Name: "Graph", Short: "Graph", Key: "2", Action: "graph tab"},
	{Name: "Quotas", Short: "Quota", Key: "3", Action: "quotas tab"},
	{Name: "Messages", Short: "Msg", Key: "4", Action: "messages tab"},
	{Name: "Settings", Short: "Set", Key: "5", Action: "settings tab"},
}

// Names returns the full tab names in tab order.
func Names() []string {
	names := make([]string, len(All))
	for i, tab := range All {
		names[i] = tab.Name
	}
	return names
}

// ShortNames returns the short tab names in tab order.
func ShortNames() []string {
	names := make([]string, len(All))
	for i, tab := range All {
		names[i] = tab.Short
	}
	return names
}

// Key returns the number key that opens the tab with this name, or "" when
// there is no such tab.
func Key(name string) string {
	for _, tab := range All {
		if tab.Name == name {
			return tab.Key
		}
	}
	return ""
}
