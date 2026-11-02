package tui

type keyContext string
type actionID string

type keyBinding struct {
	context  keyContext
	keys     []string
	action   actionID
	label    string
	priority int
}

const contextHelp keyContext = "help"

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
	{contextHelp, []string{"T"}, actTips, "toggle tips", 0},
	{contextHelp, []string{"R"}, actRestartTips, "restart tips", 0},
	{contextHelp, []string{"n"}, actNotifications, "toggle notifications", 0},
	{contextHelp, []string{"q"}, actQuit, "quit", 1},
	{contextHelp, []string{"esc", "?", "enter", "backspace"}, actClose, "close help", 1},
	{contextHelp, []string{"down", "j"}, actDown, "scroll down", 1},
	{contextHelp, []string{"up", "k"}, actUp, "scroll up", 1},
	{contextHelp, []string{"pgdown"}, actPageDown, "page down", 0},
	{contextHelp, []string{"pgup"}, actPageUp, "page up", 0},
	{contextHelp, []string{"home", "g"}, actFirst, "first line", 0},
	{contextHelp, []string{"end", "G"}, actLast, "last line", 0},
}

func keyAction(context keyContext, key string) actionID {
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
