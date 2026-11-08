package tui

import (
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A connected bot app is an agent node with role "bot" (decision 0092). The
// Graph marks it, and its card shows what the owner sees in Connected apps.

// graphBotMarker comes before the name of a bot on the canvas and in a team frame.
const graphBotMarker = "⚙ "

func graphIsBot(n protocol.Node) bool { return n.Role == "bot" }

// graphBotLabel puts the bot marker before the label of a bot node.
func graphBotLabel(n protocol.Node, label string) string {
	if graphIsBot(n) {
		return graphBotMarker + label
	}
	return label
}

// graphBotCardLines are the card lines that belong to a bot: its client, when
// it was connected and last used, and the shared MCP servers it may call.
func (m Model) graphBotCardLines(n protocol.Node) (lines [][2]string) {
	bot := n.Bot
	if bot == nil {
		bot = &protocol.NodeBot{}
	}
	date := func(ns int64) string {
		if ns == 0 {
			return "never"
		}
		return m.appsDate(time.Unix(0, ns))
	}
	lines = append(lines,
		[2]string{"CLIENT  " + firstNonEmpty(bot.Client, "-"), "label"},
		[2]string{"CONNECTED  " + date(bot.ConnectedAt), "label"},
		[2]string{"LAST USED  " + date(bot.LastUsedAt), "label"})
	if len(bot.CanCall) > 0 {
		lines = append(lines, [2]string{"can call: " + strings.Join(bot.CanCall, ", "), "labelDim"})
	} else {
		lines = append(lines, [2]string{"can call: nothing granted", "labelDim"})
	}
	return lines
}

// graphBotNote is the dim line under a bot's charter.
const graphBotNote = "A bot reads its charter only if its own instructions tell it to; its grants and app limits are what bind it."
