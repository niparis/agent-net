package graphview

import "github.com/charmbracelet/lipgloss"

var (
	voiceColor     = lipgloss.AdaptiveColor{Light: "#946300", Dark: "#ffd35a"}
	talkColor      = lipgloss.AdaptiveColor{Light: "#b00069", Dark: "#ff8dce"}
	talkFaintColor = lipgloss.AdaptiveColor{Light: "#a76c8e", Dark: "#80536f"}
	// AccentColor marks the selected node and the edges that reach it.
	AccentColor = lipgloss.AdaptiveColor{Light: "#865000", Dark: "#f0b35a"}
)
