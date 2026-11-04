package tui

import (
	"image/color"

	"github.com/niparis/agent-net/internal/tui/palette"
)

// The light variants are dark enough to read against a white terminal.
var (
	roleSelection color.Color = palette.Color("#e6f1f2", "#22303a")
	roleMature    color.Color = palette.Color("#315da8", "#7aa7f5")
	roleAccent    color.Color = palette.Color("#00666b", "#5fc4c9")
	roleGood      color.Color = palette.Color("#246b32", "#7ccf8a")
	roleWarning   color.Color = palette.Color("#865000", "#f0b35a")
	roleError     color.Color = palette.Color("#a12b35", "#f07178")
	roleCTO       color.Color = palette.Color("#6445a1", "#b49cf5")
	roleDim       color.Color = palette.Color("#596171", "#6f7889")
	roleFaint     color.Color = palette.Color("#747b87", "#454c5b")
	roleText      color.Color = palette.Color("#242a34", "#d7dce5")
	roleBase      color.Color = palette.Color("#ffffff", "#11151c")
)
