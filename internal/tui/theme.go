package tui

import "github.com/charmbracelet/lipgloss"

// The light variants are dark enough to read against a white terminal.
var (
	roleSelection lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#e6f1f2", Dark: "#22303a"}
	roleMature    lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#315da8", Dark: "#7aa7f5"}
	roleAccent    lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#00666b", Dark: "#5fc4c9"}
	roleGood      lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#246b32", Dark: "#7ccf8a"}
	roleWarning   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#865000", Dark: "#f0b35a"}
	roleError     lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#a12b35", Dark: "#f07178"}
	roleCTO       lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#6445a1", Dark: "#b49cf5"}
	roleDim       lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#596171", Dark: "#6f7889"}
	roleFaint     lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#747b87", Dark: "#454c5b"}
	roleText      lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#242a34", Dark: "#d7dce5"}
	roleBase      lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#11151c"}
)
